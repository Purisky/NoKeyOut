package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// 布局校验：id 合法 / 不重复 / 数量 ≤ max / 槽高合计 ≤ 屏高。
func TestValidateModules(t *testing.T) {
	ok := [][]string{
		{}, {"quota"}, {"quota", "usage"}, {"usage"},
	}
	for _, ids := range ok {
		if err := validateLayout(layoutDoc{Modules: ids}); err != nil {
			t.Errorf("validateModules(%v) 应通过，got %v", ids, err)
		}
	}
	bad := map[string][]string{
		"未知模块":   {"quota", "nope"},
		"重复":     {"quota", "quota"},
		"超过数量":   {"quota", "usage", "nope", "x"},
	}
	for name, ids := range bad {
		if err := validateLayout(layoutDoc{Modules: ids}); err == nil {
			t.Errorf("%s：validateModules(%v) 应报错", name, ids)
		}
	}
	// 最大的两个模块必须放得下：quota 114 + quota_s 106 = 220 ≤ 240
	if err := validateLayout(layoutDoc{Modules: []string{"quota", "quota_s"}}); err != nil {
		t.Fatalf("quota+quota_s 应放得下：%v", err)
	}
}

// 布局落盘 + 热加载：save 后 current 立刻生效；坏文件沿用上一份。
func TestLayoutStoreRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "screen-layout.json")
	t.Setenv(screenLayoutEnvKey, p)
	s := &layoutStore{}
	if got := s.current().Modules; len(got) != len(screenDefaultModules) {
		t.Fatalf("文件不存在时应回落到默认布局，got %v", got)
	}
		if err := s.save(layoutDoc{Modules: []string{"quota_s", "usage"}}); err != nil {
		t.Fatal(err)
	}
	if got := s.current(); len(got.Modules) != 2 || got.Modules[0] != "quota" || got.Modules[1] != "usage" || !got.Compact {
		t.Fatalf("quota_s 应迁移为 quota+compact，got %+v", got)
	}
	// 新 schema 原样往返
	if err := s.save(layoutDoc{Modules: []string{"quota"}, Compact: true, Usage: "7d"}); err != nil {
		t.Fatal(err)
	}
	if got := s.current(); got.Compact != true || got.Usage != "7d" {
		t.Fatalf("compact/usage 应持久化，got %+v", got)
	}
	if err := s.save(layoutDoc{Modules: []string{"bogus"}}); err == nil {
		t.Fatal("非法布局必须被 save 拒绝")
	}
	// 坏文件：直接写坏 JSON，current 不能崩，且沿用其默认布局
	if err := os.WriteFile(p, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s2 := &layoutStore{}
	if got := s2.current().Modules; len(got) != len(screenDefaultModules) {
		t.Fatalf("坏文件应回落到默认布局，got %v", got)
	}
}

// isolateLayout：把全局布局存储换成指向临时文件的新实例（避免用例之间互相串状态），
// 用例结束后**还原**——否则后面的黄金样本会用到被改过的布局（测试间污染）。
func isolateLayout(t *testing.T) {
	t.Helper()
	prev := layout
	t.Setenv(screenLayoutEnvKey, filepath.Join(t.TempDir(), "screen-layout.json"))
	layout = &layoutStore{doc: layoutDoc{Modules: screenDefaultModules}}
	t.Cleanup(func() { layout = prev })
}

// 网络状态机：网线未插 / 无外网都要整屏告警；正常与"拿不到快照"不得误报。
func TestNetProblem(t *testing.T) {
	cases := []struct {
		name string
		net  map[string]any
		want string
	}{
		{"网线未插", map[string]any{"eth": "down", "inet": false}, "ETH"},
		{"无外网", map[string]any{"eth": "up", "inet": false}, "INET"},
		{"正常", map[string]any{"eth": "up", "inet": true}, ""},
		{"无快照不误报", nil, ""},
		{"未知状态不误报", map[string]any{"eth": "unknown", "inet": false}, ""},
	}
	for _, c := range cases {
		snap := map[string]any{}
		if c.net != nil {
			snap["net"] = c.net
		}
		if got := netProblem(snap); got != c.want {
			t.Errorf("%s：netProblem=%q，want %q", c.name, got, c.want)
		}
	}
}

// 网络不可用时必须走整屏告警页，而不是模块栈（屏上显示配额/速率没有意义）。
func TestNoNetTakesOverScreen(t *testing.T) {
	snap := testSnap()
	snap["net"] = map[string]any{"eth": "down", "inet": false}
	c := newCanvas(screenW, screenH)
	uiRender("main", snap, newHistory(), c, 0, "zh", "12:34", animState{})
	if allZero(c.Buf) {
		t.Fatal("告警页不应为空白")
	}
	// 模块栈里第一行配额在顶部；告警页顶部应无配额行的品牌色方块
	if got := pxAt(c, 4+iconDetailed/2, 8); got == providerBrand("opencode-go", "OpenCode Go").color {
		t.Fatal("网络不可用时不应渲染模块栈")
	}
}

// 模块栈：按配置顺序把模块画在各自的槽位上，**且铺满整屏**（不再在底部留底色）。
func TestRenderModulesKeepsSlots(t *testing.T) {
	doc := layoutDoc{Modules: []string{"quota", "usage"}}
	isolateLayout(t)
	if err := layout.save(doc); err != nil {
		t.Fatal(err)
	}
	c := newCanvas(screenW, screenH)
	c.fillRect(0, 0, screenW, screenH, screenBG)
	renderModulesWith(layout.current(), c, newTxt("zh"), testSnap(), newHistory(), animState{})

	heights, gaps := moduleHeights(doc, testSnap())
	if len(heights) != 2 {
		t.Fatalf("应分配 2 个模块高度，got %v", heights)
	}
	sum := heights[0] + heights[1] + gaps
	if sum != screenH {
		t.Fatalf("模块高度合计 %d ≠ 屏高 %d（没有铺满）", sum, screenH)
	}
	// 每个模块只能画在自己的高度区间内（间隔 6px 必须保持底色）
	bg := uint16(screenBG)
	ranges := [][2]int{{0, heights[0]}, {heights[0] + gaps, heights[0] + gaps + heights[1]}}
	for y := 0; y < screenH; y++ {
		inside := false
		for _, r := range ranges {
			if y >= r[0] && y < r[1] {
				inside = true
			}
		}
		for x := 0; x < screenW; x++ {
			px := uint16(c.Buf[(y*screenW+x)*2])<<8 | uint16(c.Buf[(y*screenW+x)*2+1])
			if px == bg {
				continue
			}
			if !inside {
				t.Fatalf("模块画出了自己的区间：y=%d x=%d（ranges=%v）", y, x, ranges)
			}
		}
	}
}

func pxAt(c *Canvas, x, y int) uint16 {
	return uint16(c.Buf[(y*screenW+x)*2])<<8 | uint16(c.Buf[(y*screenW+x)*2+1])
}

// 配额模块高度**跟着条数走**：加条数就长，去掉就缩；整屏始终铺满。
func TestQuotaHeightFollowsRows(t *testing.T) {
	sum := func(v []int) int {
		s := 0
		for _, x := range v {
			s += x
		}
		return s
	}
	cases := []struct {
		name  string
		doc   layoutDoc
		picks int
	}{
		{"2 条/只有配额", layoutDoc{Modules: []string{"quota"}}, 2},
		{"6 条/只有配额", layoutDoc{Modules: []string{"quota"}}, 6},
		{"6 条/配额+用量", layoutDoc{Modules: []string{"quota", "usage"}}, 6},
		{"6 条/简易+用量", layoutDoc{Modules: []string{"quota", "usage"}, Compact: true}, 6},
	}
	for _, c := range cases {
		snap := manyPoolsSnap(c.picks)
		doc := c.doc
		doc.Quotas = nil // 自动：全部
		h, gaps := moduleHeights(doc, snap)
		if got := sum(h) + gaps; got != screenH {
			t.Fatalf("%s：合计 %d ≠ %d（没铺满）", c.name, got, screenH)
		}
		// 配额自然高 = 条数 × 行高；只有它自己时应恰好等于屏高
		rowH := bandPoolH
		if doc.Compact {
			rowH = bandCompactH
		}
		if n := len(doc.Modules); n == 1 && h[0] != screenH {
			t.Fatalf("%s：单模块应吃满屏高，got %d", c.name, h[0])
		}
		if c.picks*rowH <= screenH && len(doc.Modules) == 1 && h[0] < c.picks*rowH {
			t.Fatalf("%s：配額高 %d 小于内容高 %d", c.name, h[0], c.picks*rowH)
		}
	}
	// 条数变化必须改变分配（6 条 > 3 条）
	a, _ := moduleHeights(layoutDoc{Modules: []string{"quota", "usage"}}, manyPoolsSnap(3))
	b, _ := moduleHeights(layoutDoc{Modules: []string{"quota", "usage"}}, manyPoolsSnap(6))
	if a[0] >= b[0] {
		t.Fatalf("6 条的配额应比 3 条拿到更多高度：%v vs %v", a, b)
	}
}

// 预览和真机必须走同一条 uiRender 状态链；候选布局只替换模块栈。
func TestPreviewUsesHardwareRenderPath(t *testing.T) {
	isolateLayout(t)
	doc := layoutDoc{Modules: []string{"quota", "usage"}, Usage: "7d"}
	if err := layout.save(doc); err != nil {
		t.Fatal(err)
	}
	snap := testSnap()
	hw := newCanvas(screenW, screenH)
	preview := newCanvas(screenW, screenH)
	uiRender("main", snap, newHistory(), hw, 0, "zh", "12:00", animState{})
	uiRenderWith("main", snap, newHistory(), preview, 0, "zh", "12:00", animState{}, doc)
	if !bytes.Equal(hw.Buf, preview.Buf) {
		t.Fatal("候选布局与当前布局相同时，预览必须和真机逐字节一致")
	}
}
