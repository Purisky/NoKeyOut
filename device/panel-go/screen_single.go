package main

// screen_single.go —— 屏幕的绘制构件（配额行、取值助手）与"网络不可用"整屏页。
//
// 版面本身已交给模块系统（screen_layout.go）：屏上是哪几个模块、什么顺序，由用户配置。
// 本文件只提供构件，不再假设"固定三段式"布局，也不再画常驻页头（见 screen_layout.go 的产品说明）。

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	//   详细 36px = 图标 32 + 第一行(名字) + 第二行(周期 + 条/倒计时)
	//   简易 20px = 图标 18 + 周期(5h/7d/m) + 条/倒计时，全在一行
	bandPoolH    = 36
	bandCompactH = 20
	iconCompact  = 18
	bandPoolN    = 3 // 详细行最多 3 行
	bandCompactN = 5 // 简易行最多 5 行
	iconDetailed = 32
	quotaBarH    = 6 // 配额条高（与自定义 bars 元素同高）

	// 兼容测试引用的名字
	singlePoolH = bandPoolH
)

// nowFn：渲染取"当前时间"的唯一入口。真实运行 = time.Now；
// 测试替换成固定时刻，否则时间标记位置与 `R6d` 倒计时会随墙上时钟变化，黄金样本必然抖动。
var nowFn = time.Now

// clampPct：把百分比夹到 0..100。
func clampPct(v float64) float64 { return math.Max(0, math.Min(100, v)) }

// remainColor：剩余 ≥40% 绿 / 15–40% 黄 / <15% 红（与 remainState 的阈值同一套）。
func remainColor(remain float64) uint16 {
	switch {
	case remain >= 40:
		return screenOK
	case remain >= 15:
		return screenWarn
	default:
		return screenDanger
	}
}

// singleRows：顶层快照的 pools（pollQuotas 已排序：剩余最少在最前）。
func singleRows(snap map[string]any) []map[string]any {
	pools, _ := snap["pools"].([]any)
	rows := make([]map[string]any, 0, len(pools))
	for _, raw := range pools {
		if m, ok := raw.(map[string]any); ok {
			rows = append(rows, m)
		}
	}
	return rows
}

// rowRemain：剩余 %（quota 用 remain_pct，缺字段退回 used_pct）。
func rowRemain(m map[string]any) (float64, bool) {
	if v, ok := snapF(m, "remain_pct"); ok {
		return clampPct(v), true
	}
	if v, ok := snapF(m, "used_pct"); ok {
		return clampPct(100 - v), true
	}
	return 0, false
}

// resetIn：距重置还剩多久；上游没给重置时刻 → ok=false（不画标记、不做倒计时，不猜）。
func resetIn(m map[string]any) (time.Duration, bool) {
	ra, ok := snapF(m, "reset_at")
	if !ok || !snapB(m, "has_reset") {
		return 0, false
	}
	return time.Unix(int64(ra), 0).Sub(nowFn()), true
}

// resetFrac：窗口已过时间比例；缺重置时刻或缺周期 → -1（不画时间标记，不猜）。
func resetFrac(m map[string]any) float64 {
	d, ok := resetIn(m)
	if !ok {
		return -1
	}
	ps, ok := snapF(m, "period_s")
	if !ok || ps <= 0 {
		return -1
	}
	period := time.Duration(ps) * time.Second
	return clamp01(1 - d.Seconds()/period.Seconds())
}

// fmtDur：距刷新还剩多久，按语言写（点阵 11px 下尽量短）：
// zh「6天4小时」「2小时13分」「45分」「38秒」；en「6d4h」「2h13m」「45m」「38s」。
func fmtDur(lang string, d time.Duration) string {
	if d < 0 {
		d = 0
	}
	dd := int(d / (24 * time.Hour))
	hh := int(d/time.Hour) % 24
	mm := int(d/time.Minute) % 60
	ss := int(d/time.Second) % 60
	zh := lang != "en"
	switch {
	case dd > 0:
		if zh {
			if hh > 0 && dd < 10 {
				return fmt.Sprintf("%d天%d小时", dd, hh)
			}
			return fmt.Sprintf("%d天", dd)
		}
		if hh > 0 && dd < 10 {
			return fmt.Sprintf("%dd%dh", dd, hh)
		}
		return fmt.Sprintf("%dd", dd)
	case hh > 0:
		if zh {
			if mm > 0 {
				return fmt.Sprintf("%d小时%d分", hh, mm)
			}
			return fmt.Sprintf("%d小时", hh)
		}
		if mm > 0 {
			return fmt.Sprintf("%dh%dm", hh, mm)
		}
		return fmt.Sprintf("%dh", hh)
	case mm > 0:
		if zh {
			return fmt.Sprintf("%d分", mm)
		}
		return fmt.Sprintf("%dm", mm)
	}
	if zh {
		return fmt.Sprintf("%d秒", ss)
	}
	return fmt.Sprintf("%ds", ss)
}

// 上游没给重置时刻就只说过尽状态，**不编倒计时**。
func quotaOutText(t *Txt, m map[string]any) string {
	d, hasReset := resetIn(m)
	if !hasReset {
		return t.get("q_out")
	}
	return fmt.Sprintf(t.get("q_out_reset"), fmtDur(t.Lang, d))
}

// drawQuotaValue：配额行值区（周期标签右侧）三态互斥：
//
//	· 有值且未耗尽 → 进度条（填充 = **已用**份额，色 = 剩余严重度）+ 右对齐百分比——
//	  与自定义 bars 元素（used_pct 填充）同一读法：已用涨则条变长、色变急；
//	· 全部耗尽 → 只写"多久后刷新"（条空着没得画，文字顶上）；
//	· 无数据 → "--"。
//
// x = 值区左缘；lineY = 文字盒顶；barY = 条顶（两种行密度各传自己的 y）；
// right = 值区右缘（已含 "+N" 预留）。
func drawQuotaValue(c *Canvas, t *Txt, x, lineY, barY, right int, m map[string]any) {
	// **上游报错**：行照常留着（图标+名称+周期），错误直接写进条里，
	// 不用"--"糊弄，更不能把整条抹掉。
	if e := snapS(m, "err"); e != "" {
		c.fillRect(x, barY, right-x, quotaBarH, screenCard2)
		drawMixed(c, x+3, barY+(quotaBarH-11)/2, screenTrunc("! "+shortErr(t, e), right-x-6), screenDanger, SM, CJKFont)
		return
	}
	// **固定余额配额**（mode=balance）：没有周期、没有总量，百分比无意义 →
	// 不画条、不算倒计时，直接报"账上还剩多少"。与周期窗口共用同一行几何。
	if snapS(m, "mode") == "balance" {
		col := screenFG
		if a, ok := snapF(m, "amount"); ok && a <= 0 {
			col = screenDanger
		}
		drawMixed(c, x, lineY, screenTrunc(moneyValue(m), right-x), col, SM, CJKFont)
		return
	}
	remain, hasVal := rowRemain(m)
	if !hasVal {
		drawMixed(c, x, lineY, t.get("q_na"), screenMuted, SM, CJKFont)
		return
	}
	col := remainColor(remain)
	if remain < 1 { // 全部耗尽：只说"多久后刷新"
		drawMixed(c, x, lineY, screenTrunc(quotaOutText(t, m), right-x), col, SM, CJKFont)
		return
	}
	used := clampPct(100 - remain)
	pct := fmt.Sprintf("%.0f%%", used)
	// **固定宽度数值槽**：按 "100%" 预留（而不是按当前字符串宽度）——
	// 否则 "5%" 与 "100%" 会让每行条尾落在不同 x，右侧数字看着就该左该右。
	pctW := SM.width("100%")
	bw := right - x - pctW - 6
	if bw < 16 { // 极窄（异常的 +N 预留）：保数字，舍条
		drawMixedRight(c, right, lineY, pct, col, SM, CJKFont)
		return
	}
	drawMixedRight(c, right, lineY, pct, col, SM, CJKFont)
	c.fillRect(x, barY, bw, quotaBarH, screenCard2)
	if fw := int(float64(bw) * used / 100); fw > 0 {
		c.fillRect(x, barY, fw, quotaBarH, col)
	}
	// **时间追赶标记**：窗口已过比例处画 2px 白线。填充越过它 = 消耗跑在时间前面；
	// 画在填充之后（压住填充），任何已用比例下都看得见。
	// 上下各多出 1px：6px 的条里 2px 白线太短，探出条外一点才读得出是"标记"而不是"填充末端"。
	if tf := resetFrac(m); tf >= 0 {
		mx := x + int(clamp01(tf)*float64(bw-2))
		c.fillRect(mx, barY-1, 2, quotaBarH+2, screenFG)
	}
}

// windowColor：周期档位色。按**解析出的窗口长度**分档（不按名字硬编码）：
// ≤1 天 → 绿（5h），≤8 天 → 蓝（7d），更长 → 紫（月）。
func windowColor(m map[string]any) uint16 {
	if ps, ok := snapF(m, "period_s"); ok && ps > 0 {
		switch {
		case ps <= 24*3600:
			return screenWinShort
		case ps <= 8*24*3600:
			return screenWinMid
		}
		return screenWinLong
	}
	if p := windowPeriod(snapS(m, "window"), nowFn()); p > 0 {
		switch {
		case p <= 24*time.Hour:
			return screenWinShort
		case p <= 8*24*time.Hour:
			return screenWinMid
		}
		return screenWinLong
	}
	return screenMuted
}

// labelSlotW：周期标签在**条外**占用的固定宽度（2 字符 + 间隔）。
// 固定宽度保证每行的条起点完全一致——`m`（1 字符）与 `7d`（2 字符）不会让条左右跳动。
func labelSlotW() int { return SM.Adv*2 + 6 }

// drawWindowLabel：周期标签（固定 2 字符槽，按窗口档位着色；y 是文字基线）。
// 固定余额行没有窗口名 → 该槽画**币种符号**（$ / € / ¥ / 单位前两位）。
func drawWindowLabel(c *Canvas, x, y int, m map[string]any) {
	if snapS(m, "err") != "" {
		return // 错误行：标签槽留空，错误写在条里（见 drawQuotaValue）
	}
	if snapS(m, "mode") == "balance" {
		// 余额行没有窗口名：标签槽**一律留空**——120px 的槽放不下"余额"（26px），
		// 放币种符号又会和金额区重复（`$ $12.34`）；币种统一由 moneyValue 带在金额里。
		return
	}
	SM.draw(c, x, y, shortWindow(snapS(m, "window")), windowColor(m))
}

// shortErr：把上游长文案压成屏上读得下的一小段。
// 条里只有 ~22 个字符的宽度，而 magpie 的原话常常是
// "OpenCode didn't take this key (401 Unauthorized: This API key has expired…)" ——
// 直接截断只剩 "OpenCode didn't tak…"，信息量为零。所以优先取 **HTTP 状态码 + 本地化短因由**，
// 认不出状态码才退回原话截断。
func shortErr(t *Txt, msg string) string {
	low := strings.ToLower(msg)
	code := ""
	for _, c := range []string{"401", "403", "404", "408", "429", "500", "502", "503", "504"} {
		if strings.Contains(low, c) {
			code = c
			break
		}
	}
	if code != "" {
		return code + " " + t.get("e_"+code)
	}
	return msg
}

// moneyValue：金额 + 币种。≥1000 用 k 缩写（货币数字太长会把行挤爆），否则两位小数。
// 币种：字体有符号（只带 `$`）就用符号，否则用**单位代码**（`CNY 8.62`）——
// 单位代码一定画得出来，也不会被误读成同币种。
func moneyValue(m map[string]any) string {
	a, ok := snapF(m, "amount")
	if !ok {
		return "-"
	}
	amt := fmt.Sprintf("%.2f", a)
	if math.Abs(a) >= 1000 {
		amt = kfmt(a)
	}
	// `€` 在源字体里是双格宽、按单格切会缺角，所以欧元退回单位代码。
	switch strings.ToUpper(strings.TrimSpace(snapS(m, "unit"))) {
	case "USD":
		return "$" + amt
	case "CNY", "RMB":
		return "¥" + amt
	case "GBP":
		return "£" + amt
	}
	if u := strings.ToUpper(strings.TrimSpace(snapS(m, "unit"))); u != "" {
		return u + " " + amt
	}
	return amt
}

// rowIcon：图标 = **深色块 + 品牌色字形**（源 SVG 全是单色 currentColor，
// 所以「白字形压品牌色块」在淡品牌色上会糊；见 inkOnDark）。
func rowIcon(c *Canvas, m map[string]any, x, y, size int) {
	mk := providerBrand(snapS(m, "provider"), snapS(m, "name"))
	// 不垫色块：源图标本身是"套框"形状（opencode 就是方框标），色块+字形会读成
	// "控件边框"而不是 logo。直接让品牌色字形落在行背景上，更像标志、也更通透。
	if blob, ok := logoMask(mk, size); ok {
		c.bitmap(x, y, blob, size, (size+7)/8, size, inkOnDark(mk.color))
		return
	}
	SM.drawCenter(c, x+size/2, y+size/2-6, mk.mark, inkOnDark(mk.color))
}

// providerActive：该供应商当前是否处于活动窗口。
func providerActive(snap map[string]any, provider string) bool {
	if provider == "" || snapS(snap, "active_provider") != provider {
		return false
	}
	until, ok := snapF(snap, "active_until")
	return ok && nowFn().Unix() < int64(until)
}

// drawProviderSpinner：图标外圈的不确定指示器。
func drawProviderSpinner(c *Canvas, cx, cy, r int, now float64) {
	drawM3Indeterminate(c, cx, cy, r, 2, screenAccent, now)
}

// drawQuotaRowDetailed：详细行（36px）——图标 32 ｜ 第一行：供应商名 ｜ 第二行：周期 + 值区（条/倒计时）。
// 内容 8px 边距（与其它模块同一网格）；行分隔线通栏（0..屏宽-1）。
// inset = 右侧预留宽度（模块末尾的 "+N" 提示占位）。
func drawQuotaRowDetailed(c *Canvas, t *Txt, y int, first bool, m map[string]any, inset int, active bool, now float64) {
	c.fillRect(0, y, screenW, bandPoolH, screenBG)
	if !first {
		c.hline(0, screenW-1, y, screenBorder)
	}
	const x = 8
	rowIcon(c, m, x, y+2, iconDetailed)
	if active {
		drawProviderSpinner(c, x+iconDetailed/2, y+2+iconDetailed/2, iconDetailed/2+1, now)
	}

	tx := x + iconDetailed + 6
	name := snapS(m, "name")
	if name == "" {
		name = "?"
	}
	// 同一供应商的多个账号窗口名完全一样 → 名称行必须带账号，否则两条一模一样
	if acc := snapS(m, "account"); acc != "" {
		name += " · " + acc
	}
	drawMixed(c, tx, y+3, screenTrunc(name, screenW-8-tx-inset), screenFG, SM, CJKFont)

	drawWindowLabel(c, tx, y+19, m)
	sx := tx + labelSlotW()
	// 条与 11px 文字盒（y+19..y+29）同一视觉中线：条顶 = 盒顶 + 2
	drawQuotaValue(c, t, sx, y+19, y+21, screenW-8-inset, m)
}

// drawQuotaRowCompact：简易行（20px）——图标 + 周期 + 值区（条/倒计时），整行一条。
func drawQuotaRowCompact(c *Canvas, t *Txt, y int, first bool, m map[string]any, inset int, active bool, now float64) {
	c.fillRect(0, y, screenW, bandCompactH, screenBG)
	if !first {
		c.hline(0, screenW-1, y, screenBorder)
	}
	const x = 8
	rowIcon(c, m, x, y+1, iconCompact)
	if active {
		drawProviderSpinner(c, x+iconCompact/2, y+1+iconCompact/2, iconCompact/2, now)
	}

	bx := x + iconCompact + 6
	drawWindowLabel(c, bx, y+4, m)
	sx := bx + labelSlotW()
	drawQuotaValue(c, t, sx, y+4, y+6, screenW-8-inset, m)
}

// shortWindow：窗口名 → 短写法："5 hours"→"5h"、"7 days"→"7d"、"Month"→"m"。
func shortWindow(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	fields := strings.Fields(s)
	unit := func(u string) string {
		if u == "" {
			return ""
		}
		return u[:1]
	}
	if len(fields) == 2 {
		if _, err := strconv.Atoi(fields[0]); err == nil {
			return fields[0] + unit(strings.TrimSuffix(fields[1], "s"))
		}
	}
	if s != "" {
		return unit(s)
	}
	return "-"
}

// drawKV：一格内「标签（左）+ 数值（右对齐）」。放不下先牺牲标签，保证数字永远可见。
func drawKV(c *Canvas, x, y, w int, label, value string, vcol uint16) {
	vw := mixedWidth(value, SM, CJKFont)
	if value != "" {
		drawMixedRight(c, x+w, y, value, vcol, SM, CJKFont)
	}
	if label == "" {
		return
	}
	if lw := mixedWidth(label, SM, CJKFont); lw+6+vw <= w {
		drawMixed(c, x, y, label, screenMuted, SM, CJKFont)
	}
}

// durStr：时长的紧凑 ASCII 表示（避免缺字）：<1h 用分钟，<1d 用小时，否则天。
func durStr(sec float64) string {
	switch {
	case sec < 3600:
		return fmt.Sprintf("%.0fm", math.Max(1, sec/60))
	case sec < 86400:
		return fmt.Sprintf("%.1fh", sec/3600)
	default:
		return fmt.Sprintf("%.0fd", sec/86400)
	}
}

// snapB：读取布尔字段（缺失或类型不对返回 false）。
func snapB(m map[string]any, k string) bool {
	v, _ := m[k].(bool)
	return v
}

// ---------------------------------------------------------------- 网络不可用整屏页

// screenPageNoNet：网线未插/无外网时**整屏用于告警**。
// 理由：这时设备调不到上游模型，屏上显示配额/速率没有任何意义（见 screen_layout.go 的产品说明）。
func screenPageNoNet(c *Canvas, t *Txt, snap map[string]any) {
	c.clear(screenBG)
	why := netProblem(snap)

	// 断线图形（侧视）：左边是连着网线的插头，右边是带簧片的插座，中间虚线 = 断口。
	cy := 88
	c.fillRect(22, cy-1, 86, 2, screenWarn)  // 网线
	c.rect(106, cy-10, 28, 20, screenWarn)   // 插头体
	c.fillRect(134, cy-6, 8, 12, screenWarn) // 插头尖（窄于本体，方向朝向插座）
	c.fillRect(118, cy-12, 6, 3, screenWarn) // RJ45 卡扣（贴着插头上沿）

	c.fillRect(146, cy-1, 5, 2, screenDim) // 断口虚线
	c.fillRect(155, cy-1, 5, 2, screenDim)

	c.hline(162, 204, cy-13, screenWarn)   // 插座：上边
	c.hline(162, 204, cy+13, screenWarn)   // 下边
	c.vline(204, cy-13, cy+13, screenWarn) // 右边
	c.vline(162, cy-13, cy-4, screenWarn)  // 左缘留槽口（中间 8px 开口）
	c.vline(162, cy+4, cy+13, screenWarn)
	c.hline(162, 172, cy-4, screenWarn) // 三根簧片
	c.hline(162, 172, cy, screenWarn)
	c.hline(162, 172, cy+4, screenWarn)

	title := t.get("net_eth_t")
	hint := t.get("net_eth_h")
	if why == "INET" {
		title, hint = t.get("net_inet_t"), t.get("net_inet_h")
	}
	screenCenter(c, screenW/2, 128, screenTrunc(title, 220), screenDanger)
	screenCenter(c, screenW/2, 152, screenTrunc(hint, 220), screenMuted)
	screenCenter(c, screenW/2, 176, screenTrunc(t.get("net_note"), 220), screenMuted)
}
