package main

// screen_dump_test.go —— 设计评审转储：把所有状态渲染成 PNG（3× 最近邻），供人眼对比版式。
// 默认跳过（不进 CI 常规路径）；用法：
//
//	$env:SCREEN_DUMP_DIR="<dir>"; go test ./ -run TestVisualDump -count=1
//
// 输出：01..14_*（各布局/状态/详细+简易组合）+ g_*（黄金样本六例）。
// 版式改动的标准流程：转储 → 目检/对比 before-after → 改代码 → 重转 → `-update` 黄金样本 → review diff。

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVisualDump(t *testing.T) {
	dir := os.Getenv("SCREEN_DUMP_DIR")
	if dir == "" {
		t.Skip("SCREEN_DUMP_DIR 未设置")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	snap := testSnap()
	hist := &History{m: map[string][]float64{"traffic.tok_s": {
		3, 5, 2, 8, 12, 9, 4, 6, 15, 22, 18, 10, 7, 20, 35, 28, 14, 9, 11, 25,
		42, 38, 27, 16, 8, 5, 13, 30, 45, 52, 33, 19, 12, 7, 4, 10, 26, 40, 31, 17,
		22, 28, 36, 44, 39, 24, 15, 9, 6, 12, 19, 27, 35, 48, 41, 29, 18, 11, 8, 21,
	}}}

	dump := func(name string, doc layoutDoc, s map[string]any, lang string) {
		c := newCanvas(screenW, screenH)
		c.clear(screenBG)
		renderModulesWith(doc, c, newTxt(lang), s, hist, animState{})
		if err := os.WriteFile(filepath.Join(dir, name+".png"), canvasPNG(c, 3), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dumpFull := func(name string, page string, s map[string]any, lang string) {
		c := newCanvas(screenW, screenH)
		uiRender(page, s, hist, c, 0, lang, "12:34", animState{})
		if err := os.WriteFile(filepath.Join(dir, name+".png"), canvasPNG(c, 3), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 默认布局：quota + usage + alerts
	dump("01_default_zh", layoutDoc{Modules: []string{"quota", "usage"}}, snap, "zh")
	dump("02_default_en", layoutDoc{Modules: []string{"quota", "usage"}}, snap, "en")
	// 简易配额 5 行
	snap5 := testSnap()
	pools := snap5["pools"].([]any)
	snap5["pools"] = append(pools,
		map[string]any{"name": "Anthropic Claude", "provider": "anthropic", "window": "7 days",
			"kind": "plan", "state": int(poolOK), "has_val": true,
			"used_pct": 45.0, "remain_pct": 55.0,
			"has_reset": true, "reset_at": float64(nowFn().Add(2 * 24 * 60 * 60 * 1e9).Unix()),
			"period_s": int64(7 * 24 * 3600)},
		map[string]any{"name": "OpenAI", "provider": "openai", "window": "5 hours",
			"kind": "plan", "state": int(poolOK), "has_val": true,
			"used_pct": 10.0, "remain_pct": 90.0,
			"has_reset": true, "reset_at": float64(nowFn().Add(2 * 60 * 60 * 1e9).Unix()),
			"period_s": int64(5 * 3600)},
	)
	dump("03_compact_zh", layoutDoc{Modules: []string{"quota", "usage"}, Compact: true}, snap5, "zh")
	dump("04_compact_en", layoutDoc{Modules: []string{"quota", "usage"}, Compact: true}, snap5, "en")
	// 速率 + 告警
	dump("05_detailed", layoutDoc{Modules: []string{"quota"}}, snap, "zh")
	// 三周期用量（同一模块换周期）
	dump("06_usage_7d", layoutDoc{Modules: []string{"quota_s", "usage"}, Usage: "7d"}, snap5, "zh")
	dump("06b_usage_30d", layoutDoc{Modules: []string{"quota_s", "usage"}, Usage: "30d"}, snap5, "en")
	// 配额自定义（点名两条）
	dump("06c_quota_picks", layoutDoc{
		Modules: []string{"quota"},
		Quotas:  []quotaPick{{Provider: "opencode-go", Window: "7 days"}, {Provider: "deepseek", Window: "Month"}},
	}, snap, "zh")
	// 状态整屏页
	dumpFull("07_admin", "main", map[string]any{"switch": "unlocked", "url": "https://192.0.2.20:8443"}, "zh")
	dumpFull("07b_admin_over", "main", map[string]any{"switch": "unlocked", "url": "https://192.0.2.20:8443", "admin_s": 1000.0}, "zh")
	dumpFull("07e_admin_en", "main", map[string]any{"switch": "unlocked", "url": "https://192.0.2.20:8443", "admin_s": 192.0}, "en")
	{
		c := newCanvas(screenW, screenH)
		uiRender("main", map[string]any{"switch": "unlocked", "url": "https://192.0.2.20:8443", "admin_s": 192.0},
			hist, c, 0, "zh", "12:34", animState{})
		drawToast(c, newTxt("zh").get("toast_admin_off"))
		if err := os.WriteFile(filepath.Join(dir, "07g_admin_toast.png"), canvasPNG(c, 3), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dumpFull("07f_admin_en_over", "main", map[string]any{"switch": "unlocked", "url": "https://192.0.2.20:8443", "admin_s": 1000.0}, "en")
	{
		c := newCanvas(screenW, screenH)
		uiRender("main", map[string]any{"switch": "unlocked", "url": "https://192.0.2.20:8443", "admin_s": 1000.0},
			hist, c, 0, "zh", "12:34", animState{now: 1.1})
		if err := os.WriteFile(filepath.Join(dir, "07h_admin_alert.png"), canvasPNG(c, 3), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	{
		c := newCanvas(screenW, screenH)
		uiRender("main", map[string]any{"switch": "unlocked", "url": "https://192.0.2.20:8443", "admin_s": 192.0},
			hist, c, 0, "zh", "12:34", animState{now: 1.1})
		if err := os.WriteFile(filepath.Join(dir, "07d_admin_anim.png"), canvasPNG(c, 3), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 供应商活动：图标外圈 M3 不确定圆形指示器（动画时钟固定 1.1s 便于目检）
	snapAct := testSnap()
	snapAct["active_provider"] = "opencode-go"
	snapAct["active_until"] = nowFn().Add(time.Hour).Unix()
	{
		c := newCanvas(screenW, screenH)
		uiRender("main", snapAct, hist, c, 0, "zh", "12:34", animState{now: 1.1})
		if err := os.WriteFile(filepath.Join(dir, "07c_provider_active.png"), canvasPNG(c, 3), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dumpFull("08_nonet", "main", map[string]any{"net": map[string]any{"eth": "down", "inet": false}}, "zh")
	dumpFull("09_error", "main", map[string]any{"error": "connect: refused", "usage_age_s": 12.0}, "zh")
	// 无数据
	dump("10_nodata", layoutDoc{Modules: []string{"quota", "usage"}}, map[string]any{}, "zh")
	// 9 条配额：验证详细行 +N 与简易 5 行 +N
	snap9 := testSnap()
	clone := []any{}
	base := snap9["pools"].([]any)
	for i := 0; i < 9; i++ {
		m := map[string]any{}
		for k, v := range base[i%len(base)].(map[string]any) {
			m[k] = v
		}
		m["provider"] = fmt.Sprintf("p%d", i)
		m["name"] = fmt.Sprintf("P%d", i)
		m["window"] = "7 days"
		clone = append(clone, m)
	}
	snap9["pools"] = clone

	// 配额密度由 compact 决定（老布局里的 quota_s 会自动迁移）
	dump("12_quota_more", layoutDoc{Modules: []string{"quota"}}, snap9, "zh")
	dump("13_quota_compact", layoutDoc{Modules: []string{"quota"}, Compact: true}, snap, "zh")
	dump("14_quota_compact_more", layoutDoc{Modules: []string{"quota"}, Compact: true}, snap9, "zh")

	// 黄金样本四例原样渲染（-update 前目检这份就是"将要写回的期望值"）
	for _, cs := range loadGolden(t) {
		c := newCanvas(screenW, screenH)
		hist := &History{m: map[string][]float64{"traffic.tok_s": cs.Hist}}
		uiRender(cs.Page, cs.Snap, hist, c, cs.Prog, cs.Lang, cs.Now, animState{})
		if err := os.WriteFile(filepath.Join(dir, "g_"+cs.Name+".png"), canvasPNG(c, 3), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
