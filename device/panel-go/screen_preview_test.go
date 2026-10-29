package main

import (
	"bytes"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// 配额自定义：按 (provider, window) 挑行，顺序即屏上顺序；空列表 = 自动（原样）。
func TestPickRows(t *testing.T) {
	all := singleRows(testSnap())
	if got := pickRows(all, nil); len(got) != len(all) {
		t.Fatalf("空列表应原样返回 %d 条，got %d", len(all), len(got))
	}
	// 点名两条，顺序按列表（而不是按剩余升序）
	got := pickRows(all, []quotaPick{
		{Provider: "deepseek", Window: "Month"},
		{Provider: "opencode-go", Window: "7 days"},
	})
	if len(got) != 2 {
		t.Fatalf("应挑出 2 条，got %d", len(got))
	}
	if snapS(got[0], "provider") != "deepseek" || snapS(got[1], "provider") != "opencode-go" {
		t.Fatalf("顺序应跟列表走，got %v / %v", snapS(got[0], "provider"), snapS(got[1], "provider"))
	}
	// 找不到的条目跳过（供应商下线/窗口消失不留空行）
	got = pickRows(all, []quotaPick{{Provider: "nope", Window: "x"}, {Provider: "deepseek", Window: "Month"}})
	if len(got) != 1 || snapS(got[0], "provider") != "deepseek" {
		t.Fatalf("找不到的条目应跳过，got %v", got)
	}
}

// 可选配额表：给编辑器下拉用，前端不写死供应商/窗口。
func TestQuotaChoices(t *testing.T) {
	list := quotaChoices(testSnap())
	if len(list) != 3 {
		t.Fatalf("应列出 3 条，got %d", len(list))
	}
	if list[0]["provider"] != "opencode-go" || list[0]["window"] != "TermX" && list[0]["window"] != "Month" {
		t.Fatalf("第一条不对：%v", list[0])
	}
	if list[0]["mode"] != "period" {
		t.Errorf("周期配额 mode 应为 period，got %v", list[0]["mode"])
	}
	if got := quotaChoices(map[string]any{}); len(got) != 0 {
		t.Errorf("空快照应为空表，got %v", got)
	}
	// 失败行没有 window：不能误标成 balance，要让人看出是取数失败
	errSnap := map[string]any{"pools": []any{map[string]any{
		"provider": "x", "name": "X", "kind": "plan", "mode": "period", "window": "", "err": "boom"}}}
	if got := quotaChoices(errSnap); len(got) != 1 || !strings.HasSuffix(got[0]["label"].(string), " · error") {
		t.Errorf("错误行标签应以 · error 结尾：%v", got)
	}
}

// 固定余额配额：只报金额（不画条、不算倒计时），与周期配额共用同一行几何。
func TestBalanceRowRendersAmount(t *testing.T) {
	m := map[string]any{
		"provider": "openrouter", "name": "OpenRouter", "window": "", "kind": "balance",
		"mode": "balance", "amount": 12.34, "unit": "USD", "state": int(poolOK),
	}
	c := newCanvas(screenW, screenH)
	c.fillRect(0, 0, screenW, screenH, screenBG)
	drawQuotaRowDetailed(c, newTxt("zh"), 0, true, m, 0, false, 0)
	// 不得出现条轨道（card2）
	if run := maxRunColor(c, bandPoolH, uint16(screenCard2)); run > 6 {
		t.Fatalf("余额行不该画进度条（出现 %dpx 轨道）", run)
	}
	// 值区必须有墨迹（金额），标签槽只有字体画得出的符号（USD=$）才放东西
	x0 := 8 + iconDetailed + 6 + labelSlotW()
	if n := inkCount(c, x0, screenW-9, 19, 29); n == 0 {
		t.Fatal("余额行值区没有金额")
	}
	if n := inkCount(c, 8+iconDetailed+6, 8+iconDetailed+6+labelSlotW()-1, 19, 29); n != 0 {
		t.Fatalf("余额行标签槽应留空（币种由金额区带），得到 %d 像素", n)
	}
	// 非 USD：字模没有 ¥/€/£ → 标签槽留空，金额区带单位代码（不能"看起来没币种"）
	cn := map[string]any{"provider": "x", "name": "X", "mode": "balance", "amount": 8.62, "unit": "CNY"}
	c3 := newCanvas(screenW, screenH)
	c3.fillRect(0, 0, screenW, screenH, screenBG)
	drawQuotaRowDetailed(c3, newTxt("zh"), 0, true, cn, 0, false, 0)
	if n := inkCount(c3, 8+iconDetailed+6, 8+iconDetailed+6+labelSlotW()-1, 19, 29); n != 0 {
		t.Fatalf("CNY 标签槽应留空（字模没有 ¥），得到 %d 像素", n)
	}
	if n := inkCount(c3, x0, screenW-9, 19, 29); n == 0 {
		t.Fatal("CNY 余额行值区没有金额/单位")
	}
	// 币种文字必须落在值区右缘内（不越界）
	if w := mixedWidth(moneyValue(cn), SM, CJKFont); x0+w > screenW-8 {
		t.Fatalf("余额文字宽 %d 超出值区（x0=%d）", w, x0)
	}
	// 人民币用 ¥（字模已烘入），不是 "CNY 8.62"
	if got := moneyValue(cn); got != "¥8.62" {
		t.Fatalf("人民币应显示 ¥，got %q", got)
	}
	for unit, want := range map[string]string{"USD": "$1.00", "CNY": "¥1.00", "GBP": "£1.00", "EUR": "EUR 1.00"} {
		m := map[string]any{"mode": "balance", "amount": 1.0, "unit": unit}
		if got := moneyValue(m); got != want {
			t.Errorf("%s: got %q want %q", unit, got, want)
		}
	}
	// 金额为 0 → 危险色
	z := map[string]any{"provider": "x", "name": "X", "mode": "balance", "amount": 0.0, "unit": "USD"}
	c2 := newCanvas(screenW, screenH)
	c2.fillRect(0, 0, screenW, screenH, screenBG)
	drawQuotaRowDetailed(c2, newTxt("zh"), 0, true, z, 0, false, 0)
	if n := inkCountColor(c2, x0, screenW-9, 19, 29, uint16(screenDanger)); n == 0 {
		t.Fatal("余额为 0 时应为危险色")
	}
}

// History 要在别的 goroutine 里并发读（预览接口）——加了锁才安全。
func TestHistoryConcurrentPushRead(t *testing.T) {
	h := newHistory()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			h.push(map[string]any{"traffic": map[string]any{"tok_s": float64(i)}})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = h.of("traffic.tok_s") // 与 push 并发：没锁会 fatal error: concurrent map read and map write
		}
	}()
	wg.Wait()
	// History 是环形缓冲（screenSparkN 点），并发 push 不会撑爆
	if got := h.of("traffic.tok_s"); len(got) != screenSparkN {
		t.Fatalf("history 长度 %d want %d", len(got), screenSparkN)
	}
	// of() 返回副本：改写不能影响内部
	got := h.of("traffic.tok_s")
	got[0] = -1
	if h.of("traffic.tok_s")[0] == -1 {
		t.Fatal("of() 必须返回副本")
	}
}

// 预览端点：候选配置直接出图（不落盘），配置变化时图也跟着变。
func TestScreenPreviewRendersCandidate(t *testing.T) {
	isolateLayout(t)
	p := &panel{
		cfg: &cfg{}, // secretFile 为空 → getSecret() 会生成随机密钥
		stats: &stats{
			cfg: &cfg{}, stop: make(chan struct{}), ts: time.Now(),
			usage: testSnap()["usage"].(map[string]any),
			pools: poolsToAny([]Pool{
				buildPool("opencode-go", "OpenCode Go", "plan", "7 days", 22, 78, true),
				buildPool("deepseek", "DeepSeek", "plan", "Month", 68, 32, true),
				buildPool("opencode-go", "OpenCode Go", "plan", "Month", 100, 0, true),
			}),
		},
		sess: &sessions{ttl: time.Minute, d: map[string]sessionEntry{}},
	}
	p.screenHist.Store(newHistory())
	secret := p.cfg.getSecret() // 必须先初始化，再拿同一个密钥签 cookie
	cookie := p.sess.create("tester", secret)

	post := func(body string) []byte {
		req := httptest.NewRequest(http.MethodPost, "/api/screen/preview", bytes.NewBufferString(body))
		req.Header.Set("Cookie", "nk_sid="+cookie)
		rec := httptest.NewRecorder()
		p.screenPreview(rec, req)
		if rec.Code != 200 {
			t.Fatalf("预览应 200，got %d %s", rec.Code, rec.Body.String())
		}
		return rec.Body.Bytes()
	}

	// 候选配置：quota + usage（用量周期 7d），不落盘
	body := `{"modules":["quota","usage"],"scale":2,
	  "quotas":[{"provider":"deepseek","window":"Month"}],"usage":"7d"}`
	pngA := post(body)
	img, err := png.Decode(bytes.NewReader(pngA))
	if err != nil {
		t.Fatalf("预览不是合法 PNG：%v", err)
	}
	if b := img.Bounds(); b.Dx() != screenW*2 || b.Dy() != screenH*2 {
		t.Fatalf("预览尺寸 %v，want %dx%d", b, screenW*2, screenH*2)
	}
	// 没落盘（预览不该改设备配置）
	if len(layout.current().Modules) != len(screenDefaultModules) {
		t.Fatal("预览不应写入配置")
	}
	// 换用量周期：图必须跟着变
	body2 := `{"modules":["quota","usage"],"scale":2,
	  "quotas":[{"provider":"deepseek","window":"Month"}],"usage":"30d"}`
	if bytes.Equal(pngA, post(body2)) {
		t.Fatal("候选配置变了，预览图应该跟着变")
	}
	// 非法 scale：回落 2（不报错）
	_ = post(`{"modules":[],"scale":99}`)
	// 未登录：401
	req := httptest.NewRequest(http.MethodPost, "/api/screen/preview", bytes.NewBufferString("{}"))
	rec := httptest.NewRecorder()
	p.screenPreview(rec, req)
	if rec.Code != 401 {
		t.Fatalf("未登录应 401，got %d", rec.Code)
	}
	// 坏 JSON：400
	req2 := httptest.NewRequest(http.MethodPost, "/api/screen/preview", bytes.NewBufferString("{oops"))
	req2.Header.Set("Cookie", "nk_sid="+cookie)
	rec2 := httptest.NewRecorder()
	p.screenPreview(rec2, req2)
	if rec2.Code != 400 {
		t.Fatalf("坏 JSON 应 400，got %d", rec2.Code)
	}
	// 模块配置接口：带当前配额自定义、用量周期与可选条目
	var got struct {
		Modules      []string         `json:"modules"`
		Quotas       []quotaPick      `json:"quotas"`
		Usage        string           `json:"usage"`
		UsagePeriods []string         `json:"usage_periods"`
		Choices      []map[string]any `json:"quota_choices"`
	}
	req3 := httptest.NewRequest(http.MethodGet, "/api/screen/modules", nil)
	req3.Header.Set("Cookie", "nk_sid="+cookie)
	rec3 := httptest.NewRecorder()
	p.screenModules(rec3, req3)
	if err := json.Unmarshal(rec3.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.UsagePeriods) != 3 {
		t.Fatalf("usage_periods 应有 3 项，got %v", got.UsagePeriods)
	}
	if len(got.Choices) != 3 {
		t.Fatalf("quota_choices 应有 3 条，got %d", len(got.Choices))
	}
	if got.Choices[0]["provider"] != "opencode-go" {
		t.Errorf("quota_choices 第一条不对：%v", got.Choices[0])
	}
}

// 多账号：同一 provider 的两个账号窗口名完全一样，必须靠 account 区分。
func TestMultiAccountPoolsAreDistinct(t *testing.T) {
	// 模拟 magpie /v1/magpie/quotas 的两个 codex 账号条目
	now := time.Now().Unix()
	mk := func(acct string, remain float64) map[string]any {
		pl := buildPool("codex", "Codex", "plan", "Week", 100-remain, remain, true)
		pl.Account = acct
		pl.Reset, pl.HasReset = time.Unix(now+3600, 0), true
		return poolsToAny([]Pool{pl})[0].(map[string]any)
	}
	all := []map[string]any{mk("work@example.com", 30), mk("personal@example.com", 88)}
	if snapS(all[0], "account") == snapS(all[1], "account") {
		t.Fatal("两条行应带不同 account")
	}
	// 明确账号 → 精确选中
	got := pickRows(all, []quotaPick{{Provider: "codex", Window: "Week", Account: "personal@example.com"}})
	if len(got) != 1 || snapS(got[0], "account") != "personal@example.com" {
		t.Fatalf("按账号选择失败：%v", got)
	}
	// 空账号 → 不限（第一行），保持老配置可用
	got = pickRows(all, []quotaPick{{Provider: "codex", Window: "Week"}})
	if len(got) != 1 || snapS(got[0], "account") != "work@example.com" {
		t.Fatalf("空账号应取第一行：%v", got)
	}
	// 编辑器下拉：标签必须带账号，否则两个选项一模一样
	list := quotaChoices(map[string]any{"pools": []any{all[0], all[1]}})
	if len(list) != 2 {
		t.Fatalf("应列出 2 条，got %d", len(list))
	}
	if list[0]["label"] == list[1]["label"] {
		t.Fatalf("两个账号的 label 不能相同：%v / %v", list[0]["label"], list[1]["label"])
	}
	if !strings.Contains(list[0]["label"].(string), "work@example.com") {
		t.Fatalf("label 应含账号：%v", list[0]["label"])
	}
	// 校验：同 provider/window 但不同账号 → 允许；完全相同的两条 → 拒绝
	if err := validateLayout(layoutDoc{Modules: []string{"quota"}, Quotas: []quotaPick{
		{Provider: "codex", Window: "Week", Account: "a"},
		{Provider: "codex", Window: "Week", Account: "b"},
	}}); err != nil {
		t.Fatalf("不同账号应允许同屏：%v", err)
	}
	// 完全相同的两条**允许**（列表就是屏上条目；重复用于凑条数看滚动也合法）
	if err := validateLayout(layoutDoc{Modules: []string{"quota"}, Quotas: []quotaPick{
		{Provider: "codex", Window: "Week", Account: "a"},
		{Provider: "codex", Window: "Week", Account: "a"},
	}}); err != nil {
		t.Fatalf("重复条目应被允许：%v", err)
	}
	// 但缺少供应商仍然拒
	if err := validateLayout(layoutDoc{Modules: []string{"quota"}, Quotas: []quotaPick{{Window: "x"}}}); err == nil {
		t.Fatal("缺供应商应被拒")
	}
}

// OpenDesign 走插件后 provider id 变成 opendesign；小屏必须有真图标，不能退回灰色字母。
func TestOpenDesignBrandIcon(t *testing.T) {
	mk := providerBrand("opendesign", "Open Design")
	if mk.logo != "opendesign" {
		t.Fatalf("opendesign logo key = %q, want opendesign", mk.logo)
	}
	for _, size := range []int{18, 32} {
		if _, ok := logoMaskAA(mk, size); !ok {
			t.Fatalf("缺 %dpx opendesign 2bpp 图标掩码", size)
		}
	}
}
