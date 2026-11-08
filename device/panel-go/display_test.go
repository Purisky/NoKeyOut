package main

// display_test.go —— 显示偏好与账号标签的回归：单账号不显示、多账号序号稳定、时区校验。

import (
	"path/filepath"
	"testing"
	"time"
)

func TestQuotaAccountLabel(t *testing.T) {
	single := map[string]any{"account": "a@example.com", "accounts_n": 1.0, "account_idx": 1.0}
	if got := quotaAccount(single, false); got != "" {
		t.Fatalf("单账号不应显示账号，得到 %q", got)
	}
	multi := map[string]any{"account": "b@example.com", "accounts_n": 2.0, "account_idx": 2.0}
	if got := quotaAccount(multi, false); got != "b@example.com" {
		t.Fatalf("未脱敏应显示账号，得到 %q", got)
	}
	if got := quotaAccount(multi, true); got != "#2" {
		t.Fatalf("脱敏后应显示序号，得到 %q", got)
	}
	noIdx := map[string]any{"account": "c@example.com", "accounts_n": 3.0}
	if got := quotaAccount(noIdx, true); got != "" {
		t.Fatalf("缺序号时不应显示账号，得到 %q", got)
	}
}

func TestDisplayCfgRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DISPLAY_CFG_FILE", filepath.Join(dir, "display.json"))
	if err := saveDisplayCfg(displayCfg{MaskAccounts: true, Timezone: "Asia/Shanghai"}); err != nil {
		t.Fatal(err)
	}
	got := loadDisplayCfg()
	if !got.MaskAccounts || got.Timezone != "Asia/Shanghai" {
		t.Fatalf("回读 = %+v", got)
	}
	if got := loadLocation(got.Timezone); got == nil || got.String() != "Asia/Shanghai" {
		t.Fatalf("时区 = %v", got)
	}
	if err := saveDisplayCfg(displayCfg{Timezone: "Not/AZone"}); err == nil {
		t.Fatal("非法时区应被拒绝")
	}
}

func TestDisplayFromSnap(t *testing.T) {
	snap := map[string]any{"display": map[string]any{
		"mask_accounts": true, "timezone": "UTC",
	}}
	d := displayFromSnap(snap)
	if !d.MaskAccounts || d.Timezone != "UTC" {
		t.Fatalf("displayFromSnap = %+v", d)
	}
	if d := displayFromSnap(map[string]any{}); d.MaskAccounts || d.Timezone != "" {
		t.Fatalf("缺字段应退回默认，得到 %+v", d)
	}
}

func TestAccountIndexStable(t *testing.T) {
	pools := []Pool{
		{Provider: "p", Account: "b@x"},
		{Provider: "p", Account: "a@x"},
		{Provider: "q", Account: "solo@x"},
	}
	out := poolsToAny(pools)
	byAcct := map[string]map[string]any{}
	for _, raw := range out {
		m := raw.(map[string]any)
		byAcct[snapS(m, "account")] = m
	}
	if got := snapI(byAcct["a@x"], "account_idx"); got != 1 {
		t.Fatalf("a@x idx = %d, want 1（字典序）", got)
	}
	if got := snapI(byAcct["b@x"], "account_idx"); got != 2 {
		t.Fatalf("b@x idx = %d, want 2", got)
	}
	if got := snapI(byAcct["a@x"], "accounts_n"); got != 2 {
		t.Fatalf("accounts_n = %d, want 2", got)
	}
	if got := snapI(byAcct["solo@x"], "accounts_n"); got != 1 {
		t.Fatalf("单账号 accounts_n = %d, want 1", got)
	}
}

func TestActivityBadge(t *testing.T) {
	for _, n := range []int{21, 15} {
		var frames [][]byte
		switch n {
		case 21:
			frames = whaleFrames21
		case 15:
			frames = whaleFrames15
		}
		if len(frames) != 60 {
			t.Fatalf("%dpx 帧数 = %d, want 60", n, len(frames))
		}
		if got, want := len(frames[0]), n*((n+3)/4); got != want {
			t.Fatalf("%dpx 帧字节 = %d, want %d", n, got, want)
		}
	}
	base := newCanvas(screenW, screenH)
	drawStaticIcon(base, providerBrand("deepseek", "DeepSeek"), 8, 8, 28)
	deep := newCanvas(screenW, screenH)
	drawStaticIcon(deep, providerBrand("deepseek", "DeepSeek"), 8, 8, 28)
	drawActivityBadge(deep, 8, 8, 28, 0.05, true, false)
	if string(deep.Buf) == string(base.Buf) {
		t.Fatal("DeepSeek 徽章应改变画面")
	}
	generic := newCanvas(screenW, screenH)
	drawStaticIcon(generic, providerBrand("openai", "OpenAI"), 8, 8, 28)
	drawActivityBadge(generic, 8, 8, 28, 0.05, false, false)
	if string(generic.Buf) == string(base.Buf) {
		t.Fatal("通用徽章应改变画面")
	}
	if string(generic.Buf) == string(deep.Buf) {
		t.Fatal("DeepSeek 徽章与通用徽章不应相同")
	}
	// 60 帧 × 50ms 循环：相差整周期时是同一帧。
	a, b := newCanvas(screenW, screenH), newCanvas(screenW, screenH)
	drawActivityBadge(a, 8, 8, 28, 0.05, true, false)
	drawActivityBadge(b, 8, 8, 28, 0.05+60*0.05, true, false)
	if string(a.Buf) != string(b.Buf) {
		t.Fatal("徽章动画周期应为 60×50ms")
	}
}

func TestModelAttributionPrefersDeepseek(t *testing.T) {
	today := map[string]any{"models": []any{
		map[string]any{"id": "opendesign/deepseek-v4.1-flash", "provider": "opendesign", "model": "deepseek-v4.1-flash", "calls": 11.0},
		map[string]any{"id": "openai/gpt-6", "provider": "openai", "model": "gpt-6", "calls": 40.0},
	}}
	prev := map[string]int64{"opendesign/deepseek-v4.1-flash": 10, "openai/gpt-6": 30}
	_, prov, deep := modelAttribution(prev, today)
	if prov != "opendesign" || !deep {
		t.Fatalf("DeepSeek 模型增长应优先归因：prov=%q deep=%v", prov, deep)
	}
	// 没有 DeepSeek 增长时按最大增量归因。
	prev = map[string]int64{"opendesign/deepseek-v4.1-flash": 11, "openai/gpt-6": 30}
	_, prov, deep = modelAttribution(prev, today)
	if prov != "openai" || deep {
		t.Fatalf("非 DeepSeek 增长：prov=%q deep=%v", prov, deep)
	}
}

func TestUsageChartSamplesTimestamps(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	pts := []usagePoint{{ts: now.Add(-2 * time.Hour).Unix(), v: 10}, {ts: now.Add(-time.Hour).Unix(), v: 20}}
	c := newCanvas(screenW, screenH)
	drawUsageChart(c, 8, 0, 100, 20, pts, 3600, now.Add(-3*time.Hour).Unix(), now.Unix(), false)
	lit := 0
	for y := 0; y < 20; y++ {
		for x := 8; x < 108; x++ {
			if pxAt(c, x, y) != uint16(screenBG) {
				lit++
			}
		}
	}
	if lit == 0 {
		t.Fatal("窗口内有数据时必须画柱")
	}
	// 窗口整体在数据左侧 → 全空。
	empty := newCanvas(screenW, screenH)
	empty.clear(screenBG)
	drawUsageChart(empty, 8, 0, 100, 20, pts, 3600, now.Add(-10*time.Hour).Unix(), now.Add(-9*time.Hour).Unix(), false)
	for y := 0; y < 19; y++ { // 最后一行的 1px 基线是设计内的，柱身不应出现
		for x := 8; x < 108; x++ {
			if pxAt(empty, x, y) != uint16(screenBG) {
				t.Fatal("窗口外不应出现柱")
			}
		}
	}
}

func TestProviderCallDeltas(t *testing.T) {
	today := map[string]any{"providers": map[string]any{
		"deepseek":    map[string]any{"calls": 12.0},
		"opendesign":  map[string]any{"calls": 30.0},
		"opencode-go": map[string]any{"calls": 5.0},
	}}
	prev := map[string]int64{"deepseek": 10, "opendesign": 30, "opencode-go": 4}
	cur, best := providerCallDeltas(prev, today)
	if best != "deepseek" {
		t.Fatalf("best = %q, want deepseek（+2 是唯一增长）", best)
	}
	if cur["opendesign"] != 30 || cur["opencode-go"] != 5 {
		t.Fatalf("cur = %v", cur)
	}
	// 跨零点清零：调用数变小不得归因。
	prev = map[string]int64{"deepseek": 100}
	if _, best := providerCallDeltas(prev, today); best != "" {
		t.Fatalf("清零后不应归因，得到 %q", best)
	}
	// 无 providerKeys（老 magpie）：返回空 map，调用方走旧回退。
	if cur, best := providerCallDeltas(prev, map[string]any{}); best != "" || len(cur) != 0 {
		t.Fatalf("空数据 = %v / %q", cur, best)
	}
}

func TestActivityBadgePeakIsRed(t *testing.T) {
	blue := newCanvas(screenW, screenH)
	drawStaticIcon(blue, providerBrand("deepseek", "DeepSeek"), 8, 8, 28)
	drawActivityBadge(blue, 8, 8, 28, 0.05, true, false)

	red := newCanvas(screenW, screenH)
	drawStaticIcon(red, providerBrand("deepseek", "DeepSeek"), 8, 8, 28)
	drawActivityBadge(red, 8, 8, 28, 0.05, true, true)

	if string(blue.Buf) == string(red.Buf) {
		t.Fatal("峰价徽章应与低谷徽章不同")
	}
	redDominant, blueDominant := false, false
	for y := 8; y < 8+28; y++ {
		for x := 8; x < 8+28; x++ {
			r, g, b := softwareRGB(pxAt(red, x, y))
			if r > b+2 && r > g+2 {
				redDominant = true
			}
			r, g, b = softwareRGB(pxAt(blue, x, y))
			if b > r+2 {
				blueDominant = true
			}
		}
	}
	if !redDominant {
		t.Fatal("峰价徽章应出现红色像素")
	}
	if !blueDominant {
		t.Fatal("低谷徽章应保留蓝色鲸鱼尾")
	}
}

func softwareRGB(c uint16) (r, g, b uint8) {
	return uint8((c >> 11) & 0x1f), uint8((c >> 5) & 0x3f), uint8(c & 0x1f)
}

func TestUsageChartPeakRed(t *testing.T) {
	now := time.Unix(1790000000, 0)
	c := newCanvas(screenW, 40)
	c.clear(screenBG)
	pts := []usagePoint{{ts: now.Unix(), v: 100, peak: 40}}
	drawUsageChart(c, 8, 0, 100, 20, pts, 3600, now.Unix(), now.Unix()+3600, true)
	// 柱高 20，峰价占 40% → 底部 8px 红，上方蓝。
	x := 58
	if got := pxAt(c, x, 19); got != peakInk {
		t.Fatalf("柱底应为峰价红 %04x，got %04x", peakInk, got)
	}
	if got := pxAt(c, x, 11); got != screenAccent {
		t.Fatalf("峰价段上方应为蓝色 %04x，got %04x", screenAccent, got)
	}
}

func TestDisplayPeakSetting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DISPLAY_CFG_FILE", filepath.Join(dir, "display.json"))
	if !loadDisplayCfg().deepseekPeakOn() {
		t.Fatal("缺省应开启 DeepSeek 峰价提醒")
	}
	off := false
	if err := saveDisplayCfg(displayCfg{DeepSeekPeak: &off}); err != nil {
		t.Fatal(err)
	}
	if loadDisplayCfg().deepseekPeakOn() {
		t.Fatal("显式关闭后应保持关闭")
	}
	on := true
	if err := saveDisplayCfg(displayCfg{DeepSeekPeak: &on}); err != nil {
		t.Fatal(err)
	}
	if !loadDisplayCfg().deepseekPeakOn() {
		t.Fatal("显式开启后应开启")
	}
}
