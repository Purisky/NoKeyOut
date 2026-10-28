package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 用量模块：周期选择 + 直方图。所有像素必须落在本模块色带内，柱数随周期变化。
func TestUsageModuleHistogram(t *testing.T) {
	snap := testSnap()
	for _, per := range []string{"today", "7d", "30d"} {
		c := newCanvas(screenW, screenH)
		c.fillRect(0, 0, screenW, screenH, screenBG) // 哨兵底色
		const y0 = 40
		renderModuleUsage(c, newTxt("zh"), snap, newHistory(), y0, moduleContentH("usage", layoutDoc{}), moduleCfg{Usage: per})
		top, bot := y0, y0+moduleContentH("usage", layoutDoc{})-1
		bg := uint16(screenBG)
		for y := 0; y < screenH; y++ {
			for x := 0; x < screenW; x++ {
				px := uint16(c.Buf[(y*screenW+x)*2])<<8 | uint16(c.Buf[(y*screenW+x)*2+1])
				if px == bg {
					continue
				}
				if y < top || y > bot {
					t.Fatalf("[%s] 越界：y=%d x=%d", per, y, x)
				}
				// 首行是通栏分隔线（模块边界，允许 0..239）；其余内容必须在内缩 8px 内
				if y > top && (x < 8 || x > screenW-9) {
					t.Fatalf("[%s] 内容越界：y=%d x=%d", per, y, x)
				}
			}
		}
		// 1px 一列铺满整宽（不再"一桶一根柱"）：底行必须连续、无空隙
		base := y0 + 42 + (moduleContentH("usage", layoutDoc{}) - 42 - 15) - 1
		if got := countBars(c, base); got != 1 {
			t.Fatalf("[%s] 直方图应连续铺满（1px 一列），得到 %d 段", per, got)
		}
		cols := 0
		for x := 8; x < screenW-8; x++ {
			if pxAt(c, x, base) != uint16(screenBG) {
				cols++
			}
		}
		if cols != screenW-16 {
			t.Fatalf("[%s] 每 1px 都应有一列，得到 %d / %d", per, cols, screenW-16)
		}
		// 台阶数 = 桶数：相邻 px 的高度变化次数应 ≈ 桶数（每桶一格台阶）
		steps := 0
		prev := -1
		for x := 8; x < screenW-8; x++ {
			top := -1
			for y := y0 + 42; y <= base; y++ {
				if pxAt(c, x, y) != uint16(screenBG) {
					top = y
					break
				}
			}
			if prev >= 0 && top != prev {
				steps++
			}
			prev = top
		}
		if wantSt := map[string]int{"today": 24, "7d": 7, "30d": 30}[per]; steps < wantSt/2 || steps > wantSt+2 {
			t.Fatalf("[%s] 台阶数=%d，与桶数 %d 不匹配", per, steps, wantSt)
		}
	}
}

// countBars：某一行上被底色隔开的柱段数（连续剖面 = 1 段）。
func countBars(c *Canvas, y int) int {
	n, in := 0, false
	for x := 0; x < screenW; x++ {
		if pxAt(c, x, y) != uint16(screenBG) {
			if !in {
				n++
				in = true
			}
		} else {
			in = false
		}
	}
	return n
}

// 配额行：所有内容必须落在本行矩形内（真机的行高 = 名称+条+明细，改字号/偏移很容易越界）。
func TestPoolRowNeverOverflows(t *testing.T) {
	row := func(m map[string]any) *Canvas {
		c := newCanvas(screenW, screenH)
		c.fillRect(0, 0, screenW, screenH, screenBG) // 哨兵
		drawQuotaRowDetailed(c, newTxt("zh"), 0, true, m, 0, false, 0)
		return c
	}
	bg := uint16(screenBG)
	// 最宽情形：长供应商名 + 有重置时间 + 有百分比
	m := map[string]any{
		"name": "OpenCode Go Long Provider Name", "provider": "opencode-go", "window": "7 days",
		"has_val": true, "remain_pct": 78.0, "used_pct": 22.0,
		"has_reset": true, "reset_at": float64(time.Now().Add(72 * time.Hour).Unix()),
		"period_s": int64(7 * 24 * 3600),
	}
	c := row(m)
	top, bot := 0, bandPoolH-1
	for y := 0; y < screenH; y++ {
		for x := 0; x < screenW; x++ {
			px := uint16(c.Buf[(y*screenW+x)*2])<<8 | uint16(c.Buf[(y*screenW+x)*2+1])
			if px == bg {
				continue
			}
			if y < top || y > bot {
				t.Fatalf("配额行越界：y=%d x=%d（行 %d..%d）", y, x, top, bot)
			}
		}
	}
}

// 周期标签配色：按窗口长度分档（5h 青 / 7d 蓝 / 月 紫），不按名字硬编码。
// 档位色只做类型区分，不占用语义色（绿=正常/黄=警告/红=危险）。
func TestWindowColors(t *testing.T) {
	cases := []struct {
		sec  float64
		win  string
		want uint16
	}{
		{5 * 3600, "5 hours", screenWinShort},
		{7 * 86400, "7 days", screenWinMid},
		{31 * 86400, "Month", screenWinLong},
		{0, "5 hours", screenWinShort}, // 没给 period_s 时按名字解析
		{0, "Month", screenWinLong},
		{0, "???", screenMuted}, // 认不出就不编颜色
	}
	for _, c := range cases {
		m := map[string]any{"window": c.win}
		if c.sec > 0 {
			m["period_s"] = c.sec
		}
		if got := windowColor(m); got != c.want {
			t.Errorf("%s(period_s=%.0f)=%04x want %04x", c.win, c.sec, got, c.want)
		}
	}
}

// 值区文字（耗尽文案 / "--"）必须落在本行可用宽度内（点阵 11px：CJK 13px/字、ASCII 7px/字）——
// 超宽会被 screenTrunc 吞掉尾巴，等于信息丢失。
func TestQuotaOutTextFitsRow(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	old := nowFn
	nowFn = func() time.Time { return now }
	defer func() { nowFn = old }()

	pools := []map[string]any{
		{"name": "OpenCode Go", "provider": "opencode-go", "window": "Month",
			"has_val": true, "remain_pct": 0.0, "period_s": int64(31 * 24 * 3600),
			"has_reset": true, "reset_at": float64(now.Add(6*24*time.Hour + 4*time.Hour).Unix())},
		{"name": "OpenCode Go", "provider": "opencode-go", "window": "7 days",
			"has_val": true, "remain_pct": 0.0, "period_s": int64(7 * 24 * 3600),
			"has_reset": true, "reset_at": float64(now.Add(26 * 24 * time.Hour).Unix())},
		{"name": "X", "provider": "x", "window": "5 hours", "has_val": true, "remain_pct": 0.0},
	}
	for lang := range screenMSG {
		tx := newTxt(lang)
		for _, m := range pools {
			txt := quotaOutText(tx, m)
			for _, c := range []struct {
				name  string
				avail int
			}{
				// 值区起点：8px 边距 + 图标 + 6 + 固定 20px 周期槽；右缘 8px 边距
				{"简易", (screenW - 8) - (8 + iconCompact + 6 + labelSlotW())},
				{"详细", (screenW - 8) - (8 + iconDetailed + 6 + labelSlotW())},
			} {
				if w := mixedWidth(txt, SM, CJKFont); w > c.avail {
					t.Errorf("[%s/%s] %q 宽 %dpx > 可用 %dpx", lang, c.name, txt, w, c.avail)
				}
			}
		}
	}
}

// 无重置信息就只说过尽状态，不编倒计时。
func TestQuotaOutTextLocalized(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	old := nowFn
	nowFn = func() time.Time { return now }
	defer func() { nowFn = old }()

	reset := func(d time.Duration) map[string]any {
		return map[string]any{
			"has_val": true, "period_s": int64(7 * 24 * 3600),
			"has_reset": true, "reset_at": float64(now.Add(d).Unix()),
		}
	}
	noReset := func() map[string]any {
		return map[string]any{"has_val": true, "remain_pct": 0.0, "period_s": int64(5 * 3600)}
	}
	cases := []struct {
		lang, name string
		m          map[string]any
		want       string
	}{
		{"zh", "耗尽·6天4小时", reset(6*24*time.Hour + 4*time.Hour), "6天4小时后刷新"},
		{"en", "耗尽·6天4小时", reset(6*24*time.Hour + 4*time.Hour), "resets in 6d4h"},
		{"zh", "耗尽·2小时13分", reset(2*time.Hour + 13*time.Minute), "2小时13分后刷新"},
		{"en", "耗尽·2小时13分", reset(2*time.Hour + 13*time.Minute), "resets in 2h13m"},
		{"zh", "耗尽·38秒", reset(38 * time.Second), "38秒后刷新"},
		{"en", "耗尽·38秒", reset(38 * time.Second), "resets in 38s"},
		{"zh", "耗尽·26天", reset(26 * 24 * time.Hour), "26天后刷新"},
		{"en", "耗尽·26天", reset(26 * 24 * time.Hour), "resets in 26d"},
		{"zh", "耗尽无重置", noReset(), "配额用尽"},
		{"en", "耗尽无重置", noReset(), "Exhausted"},
	}
	for _, c := range cases {
		if got := quotaOutText(newTxt(c.lang), c.m); got != c.want {
			t.Errorf("[%s/%s] got %q want %q", c.lang, c.name, got, c.want)
		}
	}
}

// 已用涨则条变长、色变急）+ 右对齐百分比；**只有全部耗尽才改显文本**（条空着没得可画，
// 只写"多久后刷新"）；无数据 "--"。
func TestQuotaRowValueStates(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	old := nowFn
	nowFn = func() time.Time { return now }
	defer func() { nowFn = old }()

	pool := func(remain float64, hasVal bool) map[string]any {
		m := map[string]any{"name": "X", "provider": "x", "window": "7 days", "period_s": int64(7 * 24 * 3600)}
		if !hasVal {
			return m
		}
		m["has_val"], m["remain_pct"], m["used_pct"] = true, remain, 100-remain
		if remain < 1 {
			m["has_reset"] = true
			m["reset_at"] = float64(now.Add(6*24*time.Hour + 4*time.Hour).Unix())
		}
		return m
	}
	render := func(draw func(c *Canvas, t *Txt, y int, first bool, m map[string]any, inset int, active bool, now float64), m map[string]any) *Canvas {
		c := newCanvas(screenW, screenH)
		c.fillRect(0, 0, screenW, screenH, screenBG)
		draw(c, newTxt("zh"), 0, true, m, 0, false, 0)
		return c
	}
	// 值区几何（与实现同一套常量）：详细 x=66、文字盒 y+19、条 y+21..26；
	// 简易 x=52、文字盒 y+4、条 y+6..11；右缘 = 240-8。
	dx0 := 8 + iconDetailed + 6 + labelSlotW()
	cx0 := 8 + iconCompact + 6 + labelSlotW()
	right := screenW - 8
	maxRun := func(c *Canvas, y0, y1 int, col uint16) int {
		best := 0
		for y := y0; y <= y1; y++ {
			run := 0
			for x := 0; x < screenW; x++ {
				if pxAt(c, x, y) == col {
					run++
					if run > best {
						best = run
					}
				} else {
					run = 0
				}
			}
		}
		return best
	}
	ink := func(c *Canvas, x0, x1, y0, y1 int) int {
		n := 0
		for y := y0; y <= y1; y++ {
			for x := x0; x <= x1; x++ {
				if pxAt(c, x, y) != uint16(screenBG) {
					n++
				}
			}
		}
		return n
	}
	// 条宽按**固定数值槽**（"100%"）预留，与当前字符串位数无关 → 每行条尾同一 x。
	barGeom := func(x0 int, used float64) (bw, fw int) {
		bw = right - x0 - SM.width("100%") - 6
		return bw, int(float64(bw) * used / 100)
	}

	// 1) 非耗尽：条 = 轨道（card2）+ 填充（已用涨），百分比在条右侧
	{
		c := render(drawQuotaRowDetailed, pool(78, true))
		bw, fw := barGeom(dx0, 22)
		if got := maxRun(c, 21, 26, uint16(screenOK)); got != fw {
			t.Fatalf("详细/已用 22%%：填充段 %dpx，want %dpx", got, fw)
		}
		if got := maxRun(c, 21, 26, uint16(screenCard2)); got != bw-fw {
			t.Fatalf("详细/已用 22%%：轨道余段 %dpx，want %dpx", got, bw-fw)
		}
		if pxAt(c, dx0, 20) != uint16(screenBG) || pxAt(c, dx0, 27) != uint16(screenBG) {
			t.Fatal("详细/已用 22%：条越出了 y+21..y+26")
		}
		if n := ink(c, right-SM.width("22%")-1, right-1, 19, 29); n == 0 {
			t.Fatal("详细/已用 22%：条右侧没有百分比文字")
		}
	}
	// 2) 紧凑值域换色：已用 65%（剩 35%）黄、已用 90%（剩 10%）红
	for _, c := range []struct {
		remain float64
		col    uint16
	}{
		{35, screenWarn}, {10, screenDanger},
	} {
		cv := render(drawQuotaRowDetailed, pool(c.remain, true))
		used := 100 - c.remain
		_, fw := barGeom(dx0, used)
		if got := maxRun(cv, 21, 26, uint16(c.col)); got != fw {
			t.Fatalf("详细/已用 %.0f%%（剩 %.0f%%）：填充段 %dpx（色 %04x），want %dpx", used, c.remain, got, c.col, fw)
		}
	}
	// 3) 全部耗尽：不画条，只写倒计时（红）
	{
		c := render(drawQuotaRowDetailed, pool(0, true))
		if got := maxRun(c, 0, bandPoolH-1, uint16(screenCard2)); got > 6 {
			t.Fatalf("详细/耗尽：不应有进度条轨道（出现 %dpx 连续段）", got)
		}
		if n := ink(c, dx0, right-1, 19, 29); n == 0 {
			t.Fatal("详细/耗尽：值区没有倒计时文字")
		}
		if got := maxRun(c, 19, 29, uint16(screenDanger)); got == 0 {
			t.Fatal("详细/耗尽：倒计时应为危险色")
		}
	}
	// 4) 无数据：不画条，写 "--"
	{
		c := render(drawQuotaRowDetailed, pool(0, false))
		if got := maxRun(c, 0, bandPoolH-1, uint16(screenCard2)); got > 6 {
			t.Fatalf("详细/无数据：不应有进度条轨道（出现 %dpx 连续段）", got)
		}
		if n := ink(c, dx0, right-1, 19, 29); n == 0 {
			t.Fatal("详细/无数据：值区没有 \"--\"")
		}
	}
	// 5) 简易行同一套三态
	{
		c := render(drawQuotaRowCompact, pool(45, true))
		bw, fw := barGeom(cx0, 55)
		if got := maxRun(c, 6, 11, uint16(screenOK)); got != fw {
			t.Fatalf("简易/已用 55%%：填充段 %dpx，want %dpx", got, fw)
		}
		if got := maxRun(c, 6, 11, uint16(screenCard2)); got != bw-fw {
			t.Fatalf("简易/已用 55%%：轨道余段 %dpx，want %dpx", got, bw-fw)
		}
		ce := render(drawQuotaRowCompact, pool(0, true))
		if got := maxRun(ce, 0, bandCompactH-1, uint16(screenCard2)); got > 6 {
			t.Fatalf("简易/耗尽：不应有进度条轨道（出现 %dpx 连续段）", got)
		}
		if n := ink(ce, cx0, right-1, 4, 14); n == 0 {
			t.Fatal("简易/耗尽：值区没有倒计时文字")
		}
	}
}

// 右侧数字必须成**一列**：位数不同（5% / 95%）的两行，条尾与数字右缘都要落在同一 x。
func TestQuotaValueColumnAligned(t *testing.T) {
	pool := func(used float64) map[string]any {
		return map[string]any{
			"name": "X", "provider": "x", "window": "7 days", "period_s": int64(7 * 24 * 3600),
			"has_val": true, "remain_pct": 100 - used, "used_pct": used,
		}
	}
	// 值区起点与条宽（与实现同一套常量）
	x0 := 8 + iconDetailed + 6 + labelSlotW()
	bw := (screenW - 8) - x0 - SM.width("100%") - 6
	type geo struct{ barEnd, numRight int }
	measure := func(used float64) geo {
		c := newCanvas(screenW, screenH)
		c.fillRect(0, 0, screenW, screenH, screenBG)
		drawQuotaRowDetailed(c, newTxt("zh"), 0, true, pool(used), 0, false, 0)
		barEnd := -1
		for y := 21; y <= 26; y++ {
			for x := x0; x < screenW; x++ {
				if pxAt(c, x, y) == uint16(screenCard2) && x > barEnd {
					barEnd = x
				}
			}
		}
		// 数字右缘：条右侧区域的文字墨迹最后一列
		numRight := -1
		for x := screenW - 1; x > x0+bw; x-- {
			hit := false
			for y := 19; y <= 29; y++ {
				if pxAt(c, x, y) != uint16(screenBG) {
					hit = true
					break
				}
			}
			if hit {
				numRight = x
				break
			}
		}
		return geo{barEnd, numRight}
	}
	a, b := measure(5), measure(95)
	if a != b {
		t.Fatalf("右侧未对齐：已用5%% → %+v，已用95%% → %+v", a, b)
	}
	wantBarEnd := x0 + bw - 1
	if a.barEnd != wantBarEnd {
		t.Fatalf("条尾 x=%d，want %d", a.barEnd, wantBarEnd)
	}
	if a.numRight < x0+bw {
		t.Fatalf("数字没有落在条右侧：numRight=%d 条尾=%d", a.numRight, a.barEnd)
	}
}

// 时间追赶标记：窗口已过比例处必须有 2px 白线，且压在填充之上（填充越过它时也看得见）。
func TestQuotaTimeMarker(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	old := nowFn
	nowFn = func() time.Time { return now }
	defer func() { nowFn = old }()

	const period = 7 * 24 * time.Hour
	m := map[string]any{
		"name": "X", "provider": "x", "window": "7 days",
		"has_val": true, "remain_pct": 35.0, "used_pct": 65.0,
		"period_s":  int64(period / time.Second),
		"has_reset": true,
		"reset_at":  float64(now.Add(period / 2).Unix()), // 窗口已过 50%
	}
	c := newCanvas(screenW, screenH)
	c.fillRect(0, 0, screenW, screenH, screenBG)
	drawQuotaRowDetailed(c, newTxt("zh"), 0, true, m, 0, false, 0)

	x0 := 8 + iconDetailed + 6 + labelSlotW()
	bw := (screenW - 8) - x0 - SM.width("100%") - 6
	mx := x0 + int(0.5*float64(bw-2))
	for _, x := range []int{mx, mx + 1} {
		if got := pxAt(c, x, 23); got != uint16(screenFG) {
			t.Fatalf("时间标记缺失：x=%d 应为白色，得到 %04x", x, got)
		}
	}
	// 标记左右两侧都应是已用填充色（标记在填充内部，压住了填充）
	for _, x := range []int{mx - 1, mx + 2} {
		if got := pxAt(c, x, 23); got != remainColor(35) {
			t.Fatalf("标记应压在填充上：x=%d 得到 %04x", x, got)
		}
	}
	// 上下各探出 1px（条 y=21..26 → 标记 y=20..27）
	for _, y := range []int{20, 27} {
		if got := pxAt(c, mx, y); got != uint16(screenFG) {
			t.Fatalf("标记应在条外多出 1px：y=%d 得到 %04x", y, got)
		}
	}
	for _, y := range []int{19, 28} {
		if got := pxAt(c, mx, y); got == uint16(screenFG) {
			t.Fatalf("标记只应多出 1px：y=%d 不该是白色", y)
		}
	}
	// 标记不得越出条
	if mx < x0 || mx+1 > x0+bw-1 {
		t.Fatalf("标记越出条：mx=%d 条=%d..%d", mx, x0, x0+bw-1)
	}
}

// fmtPct：测试里复算百分比标签（与实现同格式）。
func fmtPct(v float64) string { return fmt.Sprintf("%.0f%%", v) }

// 命中率必须用**今日累计**口径：按 2s 增量算时，多数窗口内没有调用 → 恒为 0（上屏等于噪声）。
func TestCacheHitPctUsesCumulative(t *testing.T) {
	// 设备真实量级：cache_read 250,121,856 / input 3,409,689 → 98.7%
	u := map[string]any{
		"cache_read": float64(250121856), "input": float64(3409689), "calls": float64(1114),
	}
	if got := cacheHitPct(u); math.Abs(got-98.7) > 0.05 {
		t.Fatalf("累计命中率 = %.1f%%，want ≈98.7%%", got)
	}
	// 该 2s 窗口内没有新调用（增量 = 0）时，结果**不能**变成 0
	prev := map[string]any{"cache_read": float64(250121856), "input": float64(3409689)}
	if cacheHitPct(u) != cacheHitPct(prev) {
		t.Fatal("同一份累计数据两次采样结果不一致")
	}
	for _, bad := range []map[string]any{{}, {"cache_read": 0.0, "input": 0.0}} {
		if got := cacheHitPct(bad); got != 0 {
			t.Fatalf("无数据应为 0，got %.1f", got)
		}
	}
}

// manyPoolsSnap：n 条配额（剩余递减），用于溢出/滚动测试。
func manyPoolsSnap(n int) map[string]any {
	snap := testSnap()
	base := snap["pools"].([]any)[0].(map[string]any)
	pools := make([]any, 0, n)
	for i := 0; i < n; i++ {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		m["provider"] = fmt.Sprintf("p%d", i)
		m["name"] = fmt.Sprintf("P%d", i)
		m["window"] = "7 days"
		m["remain_pct"] = float64(99 - i)
		m["used_pct"] = float64(1 + i)
		pools = append(pools, m)
	}
	snap["pools"] = pools
	return snap
}

// 配额超出容量 → 1px/s 自动滚动：同一秒内不抖动、下一秒整体上移 1px、
// 且**绝不越过模块视口**（不能渗到下一个模块）。
func TestQuotaScrollsWhenOverflow(t *testing.T) {
	now := time.Unix(1700000000, 0)
	old := nowFn
	nowFn = func() time.Time { return now }
	defer func() { nowFn = old }()

	snap := manyPoolsSnap(7) // 容量 3（详细）→ 溢出
	const y0 = 40
	viewH := bandPoolN * bandPoolH
	render := func() *Canvas {
		c := newCanvas(screenW, screenH)
		c.clear(screenBG)
		renderModuleQuota(c, newTxt("zh"), snap, newHistory(), y0, bandPoolN*bandPoolH, moduleCfg{})
		return c
	}
	a := render()
	if b := render(); !bytes.Equal(a.Buf, b.Buf) {
		t.Fatal("同一秒内两次渲染应完全一致（相位只由整秒决定）")
	}
	// 视口外不得有墨迹
	for y := 0; y < screenH; y++ {
		if y >= y0 && y < y0+viewH {
			continue
		}
		for x := 0; x < screenW; x++ {
			if pxAt(a, x, y) != uint16(screenBG) {
				t.Fatalf("内容越出视口：y=%d x=%d", y, x)
			}
		}
	}
	// 视口内不能出现"整行空带"（> 行高的空白 = 漏画了一条）
	if run := maxBlankRun(a, y0, viewH); run >= bandPoolH {
		t.Fatalf("视口内出现 %dpx 空白带（漏画一条）", run)
	}
	// 下一秒：整体上移 1px
	now = now.Add(time.Second)
	c2 := render()
	// 比到倒数第二行：最底 1px 是新滚进来的内容，本来就不在上一帧里
	for y := y0 + 1; y < y0+viewH-1; y++ {
		for x := 0; x < screenW; x++ {
			if pxAt(c2, x, y) != pxAt(a, x, y+1) {
				t.Fatalf("1px/s 滚动不成立：y=%d x=%d %04x != %04x", y, x, pxAt(c2, x, y), pxAt(a, x, y+1))
			}
		}
	}
}

// 滚动循环：走到末尾下一秒回到开头，视口仍被填满（不留空档）。
func TestQuotaScrollWraps(t *testing.T) {
	snap := manyPoolsSnap(5)
	contentH := 5 * bandPoolH
	const y0 = 40
	viewH := bandPoolN * bandPoolH
	render := func(sec int64) *Canvas {
		now := time.Unix(sec, 0)
		old := nowFn
		nowFn = func() time.Time { return now }
		defer func() { nowFn = old }()
		c := newCanvas(screenW, screenH)
		c.clear(screenBG)
		renderModuleQuota(c, newTxt("zh"), snap, newHistory(), y0, bandPoolN*bandPoolH, moduleCfg{})
		return c
	}
	// 选一个相位正好在末尾（off = contentH-1）与回到开头（off = 0）
	end := render(int64(contentH - 1))
	start := render(int64(contentH))
	for name, c := range map[string]*Canvas{"末尾": end, "接回开头": start} {
		if run := maxBlankRun(c, y0, viewH); run >= bandPoolH {
			t.Fatalf("%s：出现 %dpx 空白带（循环接续有洞）", name, run)
		}
	}
}

// maxBlankRun：视口内最长的"整行全底色"连续高度（用于判断漏画/接续缺口）。
func maxBlankRun(c *Canvas, y0, h int) int {
	best, run := 0, 0
	for y := y0; y < y0+h; y++ {
		blank := true
		for x := 0; x < screenW; x++ {
			if pxAt(c, x, y) != uint16(screenBG) {
				blank = false
				break
			}
		}
		if blank {
			run++
			if run > best {
				best = run
			}
		} else {
			run = 0
		}
	}
	return best
}

// 配额条数不设上限（只受屏高预算与去重约束）。
func TestQuotaPicksUnlimited(t *testing.T) {
	picks := make([]quotaPick, 0, 40)
	for i := 0; i < 40; i++ {
		picks = append(picks, quotaPick{Provider: fmt.Sprintf("p%d", i), Window: "7 days"})
	}
	if err := validateLayout(layoutDoc{Modules: []string{"quota"}, Quotas: picks}); err != nil {
		t.Fatalf("40 条配额自定义应被接受：%v", err)
	}
}

func TestQuotaErrorRowKeepsRow(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	old := nowFn
	nowFn = func() time.Time { return now }
	defer func() { nowFn = old }()

	// 造一个"magpie 只给 error、windows 为空"的池（与真机得到一致）
	snap := testSnap()
	snap["pools"] = poolsToAny([]Pool{{Provider: "opencode-go", Name: "OpenCode Go", Kind: "plan",
		Err: "OpenCode didn't take this key (401 Unauthorized: This API key has expired.)"}})
	// 配置里点名的 3 个窗口都取不到 → 用错误行补位
	picks := []quotaPick{
		{Provider: "opencode-go", Window: "Month"},
		{Provider: "opencode-go", Window: "7 days"},
		{Provider: "opencode-go", Window: "5 hours"},
	}
	rows := pickRowsFor(singleRows(snap), picks)
	if len(rows) != 3 {
		t.Fatalf("配置的 3 行都应保留，got %d", len(rows))
	}
	for _, m := range rows {
		if snapS(m, "err") == "" {
			t.Fatalf("补位行应带 err：%v", m)
		}
		if snapS(m, "window") != "" && snapS(m, "name") != "OpenCode Go" {
			t.Fatalf("补位行应保留供应商名：%v", m)
		}
	}
	// 渲染：三条都在，且每条条内有危险色文字（不是 "--"）
	c := newCanvas(screenW, screenH)
	c.fillRect(0, 0, screenW, screenH, screenBG)
	renderModuleQuota(c, newTxt("zh"), snap, newHistory(), 0, bandPoolN*bandPoolH, moduleCfg{Quotas: picks})
	x0 := 8 + iconDetailed + 6 + labelSlotW()
	for i := 0; i < 3; i++ {
		y0 := i * bandPoolH
		if n := inkCount(c, x0, screenW-9, y0+19, y0+30); n == 0 {
			t.Fatalf("第 %d 行条里没有内容（错误没写进去）", i+1)
		}
		if n := inkCountColor(c, x0, screenW-9, y0+19, y0+30, uint16(screenDanger)); n == 0 {
			t.Fatalf("第 %d 行条里没有危险色文字", i+1)
		}
		// 名称行仍要有内容（行没被抹掉）
		if n := inkCount(c, 8, screenW-9, y0+3, y0+14); n == 0 {
			t.Fatalf("第 %d 行名称/图标也没了", i+1)
		}
	}
}

// 屏上错误文案：优先 "状态码 + 本地化短因由"，认不出才截断原话。
func TestShortErr(t *testing.T) {
	tx := newTxt("zh")
	cases := []struct{ in, want string }{
		{"OpenCode didn't take this key (401 Unauthorized: expired)", "401 密钥无效或过期"},
		{"HTTP 429 too many requests", "429 限流"},
		{"dial tcp: i/o timeout (504)", "504 上游超时"},
		{"something odd", "something odd"},
	}
	for _, c := range cases {
		if got := shortErr(tx, c.in); got != c.want {
			t.Errorf("shortErr(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestUsageHistObserve(t *testing.T) {
	h := &usageHist{path: filepath.Join(t.TempDir(), "h.bin"), slots: map[int64]histSlot{}}
	day := time.Date(2026, 10, 18, 9, 0, 0, 0, time.Local)
	h.observe(day, histSlot{tok: 1000}) // 首次只记基准
	if len(h.slots) != 0 {
		t.Fatalf("首次采样不应记账，got %v", h.slots)
	}
	h.observe(day.Add(2*time.Second), histSlot{tok: 1600}) // +600
	h.observe(day.Add(4*time.Second), histSlot{tok: 1600}) // 无变化
	if got := h.slots[day.Unix()/histSlotSec].tok; got != 600 {
		t.Fatalf("增量应为 600，got %d", got)
	}
	h.observe(day.Add(6*time.Second), histSlot{tok: 900}) // 上游回退：只重置基准
	if got := h.slots[day.Unix()/histSlotSec].tok; got != 600 {
		t.Fatalf("回退不应产生负数，got %d", got)
	}
	// 跨日：累计归零 → 重置基准，旧槽不动
	next := day.Add(24 * time.Hour)
	h.observe(next, histSlot{tok: 50})
	h.observe(next.Add(2*time.Second), histSlot{tok: 250})
	if got := h.slots[next.Unix()/histSlotSec].tok; got != 200 {
		t.Fatalf("跨日后第一个增量为 200，got %d", got)
	}
	if got := h.slots[day.Unix()/histSlotSec].tok; got != 600 {
		t.Fatalf("旧槽不该被改，got %d", got)
	}
}

func TestUsageHistImportJSONL(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "usage.jsonl")
	now := time.Now()
	var b strings.Builder
	for i := 0; i < 6; i++ {
		b.WriteString(fmt.Sprintf(`{"t":%q,"in":10,"out":5}`+"\n", now.Add(-time.Duration(i)*time.Minute).Format(time.RFC3339)))
	}
	b.WriteString(fmt.Sprintf(`{"t":%q,"in":99,"out":0}`+"\n", now.AddDate(0, 0, -40).Format(time.RFC3339))) // 超出保留期
	os.WriteFile(logPath, []byte(b.String()), 0o644)

	h := &usageHist{path: filepath.Join(dir, "h.bin"), slots: map[int64]histSlot{}}
	n, err := h.importJSONL(logPath)
	if err != nil || n != 6 {
		t.Fatalf("应导入 6 行（40 天前的丢弃），got n=%d err=%v", n, err)
	}
	sum := int64(0)
	for _, v := range h.slots {
		sum += v.tok
	}
	if sum != 90 { // 6 × (10+5)
		t.Fatalf("导入总量应为 90，got %d", sum)
	}
	// 再导入一次：已有数据 → 跳过
	if n2, _ := h.importJSONL(logPath); n2 != 0 {
		t.Fatalf("已有数据不应重复导入，got %d", n2)
	}
	// 文件不存在：静默返回错误，不影响运行
	h3 := &usageHist{path: filepath.Join(dir, "h3.bin"), slots: map[int64]histSlot{}}
	if _, err := h3.importJSONL(filepath.Join(dir, "nope.jsonl")); err == nil {
		t.Fatal("文件不存在应返回错误（调用方忽略即可）")
	}
}

// 自建桶 → 1px 一桶：桶数、落桶、总量守恒。
func TestUsageHistSeries(t *testing.T) {
	h := &usageHist{path: filepath.Join(t.TempDir(), "h.bin"), slots: map[int64]histSlot{}}
	since := time.Date(2026, 10, 18, 0, 0, 0, 0, time.Local)
	h.slots[since.Unix()/histSlotSec] = histSlot{tok: 10}                   // 0%
	h.slots[since.Add(12*time.Hour).Unix()/histSlotSec] = histSlot{tok: 20} // 50%
	h.slots[since.Add(23*time.Hour).Unix()/histSlotSec] = histSlot{tok: 30} // 96%
	h.slots[since.Add(-2*time.Hour).Unix()/histSlotSec] = histSlot{tok: 99} // 周期外

	n := screenW - 16
	v := h.series(since, 24*time.Hour, n)
	if len(v) != n {
		t.Fatalf("桶数应为 %d，got %d", n, len(v))
	}
	if v[0] != 10 {
		t.Fatalf("0%% 的点应落第 0 桶，got %d", v[0])
	}
	if v[n/2] != 20 {
		t.Fatalf("50%% 的点应落第 %d 桶，got %d", n/2, v[n/2])
	}
	sum := int64(0)
	for _, x := range v {
		sum += x
	}
	if sum != 60 {
		t.Fatalf("周期内总量应守恒 60，got %d", sum)
	}
}

// 落盘往返：只存非零槽，读回来一致。
func TestUsageHistPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.bin")
	// 用当前时间附近的槽（保存时会剪掉 31 天前的）
	now := time.Now().Unix() / histSlotSec
	h := &usageHist{path: path, slots: map[int64]histSlot{now: {tok: 5}, now - 1: {tok: 7}}}
	h.dirty = true
	h.save(true)
	h2 := newUsageHist(path)
	if !h2.ready() || h2.slots[now].tok != 5 || h2.slots[now-1].tok != 7 {
		t.Fatalf("落盘往返失败：%+v", h2.slots)
	}
	if len(h2.slots) == 0 {
		t.Fatal("落盘后应能读回槽表")
	}
}

// 端到端：pollUsage 有逐请求流水时，用 1px 一桶替换 API 的小时/天桶。
func TestPollUsageUsesFineBuckets(t *testing.T) {
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	// API：today 给 24 个小时桶，总量 100（与自建桶一致）；30d 给 3000（远大于自建桶）
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		per := r.URL.Query().Get("period")
		n := 24
		if per != "today" {
			n = map[string]int{"7d": 7, "30d": 30}[per]
		}
		tot := int64(100)
		if per == "30d" {
			tot = 3000
		}
		series := []any{}
		for i := 0; i < n; i++ {
			v := int64(0)
			if i == 0 {
				v = tot
			}
			series = append(series, map[string]any{"label": fmt.Sprintf("%d", i), "input": v, "output": 0})
		}
		json.NewEncoder(w).Encode(map[string]any{
			"period": per, "since": midnight.Format(time.RFC3339),
			"input": tot, "output": 0, "calls": n,
			"series": series,
		})
	}))
	defer srv.Close()

	histPath := filepath.Join(t.TempDir(), "hist.bin")
	mkStats := func() *stats {
		s := &stats{cfg: &cfg{gatewayBase: srv.URL, panelGatewayKey: "k", usageHistPath: histPath},
			interval: time.Second, stop: make(chan struct{})}
		s.hist = newUsageHist(histPath)
		return s
	}
	s := mkStats()
	// 自建桶：今天 100 tokens（10 次 × 10）
	slot := now.Add(-time.Hour).Unix() / histSlotSec // 滚动 24h 窗口内，不依赖当前时刻
	s.hist.slots[slot] = histSlot{tok: 100}
	s.hist.dirty = true
	s.pollUsage()

	tod, _ := s.usage["today"].(map[string]any)
	if got := snapS(tod, "buckets_src"); got != "hist" {
		t.Fatalf("today 应走自建桶，got src=%q", got)
	}
	buckets, _ := tod["buckets"].([]any)
	// 列宽整数化后，today 是 144 列（10 分钟/列），不再是 224
	if _, want := columnSpec(24*time.Hour, screenW-16); len(buckets) != want {
		t.Fatalf("桶数应为 %d（10 分钟/列），got %d", want, len(buckets))
	}
	sum := int64(0)
	for _, raw := range buckets {
		m, _ := raw.(map[string]any)
		if v, ok := snapF(m, "tokens"); ok {
			sum += int64(v)
		}
	}
	if sum != 100 {
		t.Fatalf("桶总量应 = 自建历史 100，got %d", sum)
	}
	// 自建桶覆盖不足（30d 需要 3000，只有 100）→ 退回 API 粗桶
	m30, _ := s.usage["30d"].(map[string]any)
	if got := snapS(m30, "buckets_src"); got == "hist" {
		t.Fatalf("覆盖不足时不该用自建桶，got src=%q", got)
	}
	if bs, _ := m30["buckets"].([]any); len(bs) != 30 {
		t.Fatalf("覆盖不足时应回退 API 的 30 个桶，got %d", len(bs))
	}
}

// 列宽用整数：今天 10 分钟/列（144 列）、7d 60 分钟/列（168 列）、30d 4 小时/列（180 列）。
func TestColumnSpec(t *testing.T) {
	width := screenW - 16
	cases := []struct {
		period time.Duration
		dur    time.Duration
		n      int
	}{
		{24 * time.Hour, 10 * time.Minute, 144},
		{7 * 24 * time.Hour, time.Hour, 168},
		{30 * 24 * time.Hour, 4 * time.Hour, 180},
	}
	for _, c := range cases {
		d, n := columnSpec(c.period, width)
		if d != c.dur || n != c.n {
			t.Errorf("columnSpec(%v) = %v/%d，want %v/%d", c.period, d, n, c.dur, c.n)
		}
		if n > width {
			t.Errorf("%v：列数 %d 超过宽度 %d", c.period, n, width)
		}
		if int64(d)*int64(n) != int64(c.period) {
			t.Errorf("%v：列宽×列数 != 周期", c.period)
		}
	}
}

// 整数列宽的分桶：桶数 = 周期/列宽，值落到正确的列，总量守恒。
func TestUsageHistSeriesIntegerColumns(t *testing.T) {
	h := &usageHist{path: filepath.Join(t.TempDir(), "h.bin"), slots: map[int64]histSlot{}}
	since := time.Date(2026, 10, 18, 0, 0, 0, 0, time.Local)
	// 0 点整、10 分钟整、23:50 各一笔，分别应落在第 0、1、143 列
	h.slots[since.Unix()/histSlotSec] = histSlot{tok: 7}
	h.slots[since.Add(10*time.Minute).Unix()/histSlotSec] = histSlot{tok: 11}
	h.slots[since.Add(23*time.Hour+50*time.Minute).Unix()/histSlotSec] = histSlot{tok: 13}
	_, n := columnSpec(24*time.Hour, screenW-16)
	v := h.series(since, 24*time.Hour, n)
	if len(v) != n {
		t.Fatalf("列数应为 %d，got %d", n, len(v))
	}
	if v[0] != 7 || v[1] != 11 || v[n-1] != 13 {
		t.Fatalf("落列错误：%d %d %d", v[0], v[1], v[n-1])
	}
	sum := int64(0)
	for _, x := range v {
		sum += x
	}
	if sum != 31 {
		t.Fatalf("总量应守恒 31，got %d", sum)
	}
}
