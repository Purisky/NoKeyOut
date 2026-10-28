package main

// screen_ui.go —— 240×240 界面（NoKeyOut 设备屏）
// 设计 token：间距 4/8/12/16，字阶 Display24/Title16/Body14/Caption10（8×14），
// 8px 网格；动效由 screen_run.go 的 animState 驱动（翻页滑入/圆环扫掠/锁定脉冲）。

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"
)

// screenW/screenH：UI 逻辑画布尺寸，由 screen_cfg.go 在启动时按
// 「面板尺寸 + 方向」发布（240×240 默认；240×320 面板支持 0/90/180/270）。
var (
	screenW = 240
	screenH = 240
)

const (
	screenHeadH  = 22 // 页头：设备状态 + 时钟
	screenFootH  = 16 // 页脚：页点 + 页名 + 数据新鲜度
	screenMX     = 4  // 页边距（4px gutter）
	screenSparkN = 60 // 趋势条数（≈ 一次轮播采样一次）

	// adminWarnSeconds：管理面端口开放超过 15 分钟后，ADMIN 页开始闪烁提醒。
	adminWarnSeconds = 15 * 60.0
)

var screenMSG = map[string]map[string]string{
	"zh": {
		"nodata": "无数据",
		"e_401":  "密钥无效或过期", "e_403": "无权限", "e_404": "端点不存在",
		"e_408": "超时", "e_429": "限流", "e_500": "上游故障", "e_502": "上游故障",
		"e_503": "上游故障", "e_504": "上游超时",
		"u_today": "近24小时用量", "u_7d": "近7日用量", "u_30d": "近30日用量",
		"calls_today": "今日调用", "calls_p": "调用",
		"tokens_in": "输入", "tokens_out": "输出",
		"data": "数据", "hit": "命中",
		"errors_today": "今日错误",
		"admin_hint":   "端口开放，用完拨回 LOCK",
		"admin_open":   "已开放", "admin_over": "开放超 15 分钟，拨回 LOCK",
		"err_title": "面板不可达", "err_wait": "数据未就绪", "err_hint": "检查 8443 与令牌",
		"net_eth_t": "网线未连接", "net_eth_h": "插好网口后自动恢复",
		"net_inet_t": "无外网连通", "net_inet_h": "检查上游与代理设置",
		"net_note":   "此刻设备无法提供模型调用",
		"no_modules": "未选择显示模块",
		"all_shown":  "已全部显示",
		"q_na":       "--", "q_out": "配额用尽",
		"q_out_reset": "%s后刷新",
	},
	"en": {
		"nodata": "No data",
		"e_401":  "bad/expired key", "e_403": "forbidden", "e_404": "no endpoint",
		"e_408": "timeout", "e_429": "rate limited", "e_500": "upstream error",
		"e_502": "upstream error", "e_503": "upstream error", "e_504": "upstream timeout",
		"u_today": "Last 24 hours", "u_7d": "Last 7 days", "u_30d": "Last 30 days",
		"calls_today": "Calls", "calls_p": "Calls",
		"tokens_in": "In", "tokens_out": "Out",
		"data": "Data", "hit": "Hit",
		"errors_today": "Errors today",
		"admin_hint":   "Ports open. Flip back to LOCK.",
		"admin_open":   "Open for", "admin_over": "Open >15 min. Flip back to LOCK.",
		"err_title": "Panel unreachable", "err_wait": "Data not ready",
		"err_hint":  "Check :8443 and token",
		"net_eth_t": "No cable", "net_eth_h": "Recovers when the port is connected",
		"net_inet_t": "No internet", "net_inet_h": "Check upstream and proxy",
		"net_note":   "The device cannot serve model calls now",
		"no_modules": "No modules selected",
		"all_shown":  "All shown",
		"q_na":       "--", "q_out": "Exhausted",
		"q_out_reset": "resets in %s",
	},
}

type Txt struct {
	d    map[string]string
	Lang string // 当前语言：自定义模块的 label 要按它选 zh/en
}

func newTxt(lang string) *Txt {
	d := screenMSG[lang]
	if d == nil {
		d, lang = screenMSG["zh"], "zh"
	}
	return &Txt{d, lang}
}

func (t *Txt) get(k string) string {
	if s, ok := t.d[k]; ok && s != "" {
		return s
	}
	return screenMSG["zh"][k]
}

// ---------------------------------------------------------------- 数值格式

func kfmt(v float64) string {
	a := math.Abs(v)
	switch {
	case a >= 1_000_000:
		return fmt.Sprintf("%.1fM", v/1_000_000)
	case a >= 1_000:
		k := v / 1_000
		if k >= 10 {
			return fmt.Sprintf("%.0fk", k)
		}
		return fmt.Sprintf("%.1fk", k)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}

func f1(v float64) string { return fmt.Sprintf("%.1f", v) }
func f2(v float64) string { return fmt.Sprintf("%.2f", v) }

func ageStr(age any) string {
	f, ok := toF(age)
	if !ok {
		return "-"
	}
	if f < 60 {
		return fmt.Sprintf("%ds", int(f))
	}
	return fmt.Sprintf("%dm", int(f/60))
}

func toF(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}

func snapMap(m map[string]any, k string) map[string]any {
	if v, ok := m[k]; ok {
		if mm, ok := v.(map[string]any); ok {
			return mm
		}
	}
	return nil
}

func snapF(m map[string]any, k string) (float64, bool) {
	if v, ok := m[k]; ok {
		return toF(v)
	}
	return 0, false
}

func snapS(m map[string]any, k string) string {
	if v, ok := m[k]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func snapHas(m map[string]any, k string) bool {
	v, ok := m[k]
	return ok && v != nil
}

func screenTrunc(s string, maxW int) string {
	if mixedWidth(s, SM, CJKFont) <= maxW {
		return s
	}
	out := []rune(s)
	for len(out) > 0 && mixedWidth(string(out)+"…", SM, CJKFont) > maxW {
		out = out[:len(out)-1]
	}
	return string(out) + "…"
}

// animState：动效参数（屏幕循环按时间推进；测试传零值=静态）
type animState struct {
	slide float64 // 翻页滑入位移 0..1（1=完全在场）
	ring  float64 // 圆环当前值 0..1（向目标扫掠）
	pulse float64 // 锁定脉冲相位 0..1
	now   float64 // 动画时钟（秒）；0=静态（测试/预览默认，保证黄金样本可复现）
}

func (a animState) static() bool { return a.slide == 0 && a.ring == 0 && a.pulse == 0 && a.now == 0 }

// 不加锁就是数据竞争（map 并发读写直接崩）。
type History struct {
	mu sync.Mutex
	m  map[string][]float64
}

func newHistory() *History { return &History{m: map[string][]float64{}} }

func (h *History) push(snap map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	collectNums("", snap, h)
}

// of：返回副本（调用方可能长期持有，不能被后续 push 改写）。
func (h *History) of(path string) []float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	src := h.m[path]
	if len(src) == 0 {
		return nil
	}
	out := make([]float64, len(src))
	copy(out, src)
	return out
}

func collectNums(prefix string, node any, h *History) {
	switch v := node.(type) {
	case map[string]any:
		for k, sub := range v {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			collectNums(p, sub, h)
		}
	case []any:
		for i, sub := range v {
			collectNums(fmt.Sprintf("%s[%d]", prefix, i), sub, h)
		}
	case float64:
		ring := append(h.m[prefix], v)
		if len(ring) > screenSparkN {
			ring = ring[len(ring)-screenSparkN:]
		}
		h.m[prefix] = ring
	}
}

// ---------------------------------------------------------------- 公共构件

// dot：状态点（圆角方点，动画里也省算力）
// screenHeader：页头只放设备状态与时钟——无 logo、无「开放」chip（产品要求）
func screenHeader(c *Canvas, t *Txt, clock string, err string, net map[string]any) {
	c.fillRect(0, 0, screenW, screenHeadH, screenCard)
	c.hline(0, screenW-1, screenHeadH-1, screenBorder)
	inet := false
	if net != nil {
		inet = snapS(net, "inet") == "up" || net["inet"] == true
	}
	// 单一网络图标（地球仪线框）：绿=有网络，红=未插线/无网络。色即状态。
	globeIcon(c, 6, 5, colIf(inet, screenOK, screenDanger)) // 与 ! / 时钟同一行视觉基线
	if err != "" {                                          // 统计异常：图标旁红色 !（保持控制台版同款警示）
		SM.draw(c, 20, 4, "!", screenDanger)
	}
	SM.drawRight(c, screenW-6, 4, clock, screenMuted)
}

// globeIcon：地球仪线框（11×11）——圆轮廓 + 赤道 + 南北纬线。
// 绿=有网络，红=无。全部曲线/圆：**没有方形接头**（节点若用点一律 circle()）。
func globeIcon(c *Canvas, x, y int, color uint16) {
	cx, cy, r := x+5, y+5, 5
	c.circle(cx, cy, r, color, false) // 圆轮廓
	c.hline(cx-3, cx+3, cy, color)    // 赤道
	c.hline(cx-4, cx+4, cy-3, color)  // 北纬线
	c.hline(cx-4, cx+4, cy+3, color)  // 南纬线
}

func colIf(on bool, a, b uint16) uint16 {
	if on {
		return a
	}
	return b
}

// screenFooterSpec：用户页页脚（无页点：单屏模式不再切页）
func screenFooterSpec(c *Canvas, title, age string) {
	c.hline(0, screenW-1, screenH-screenFootH, screenBorder)
	drawMixed(c, 6, screenH-14, screenTrunc(title, 120), screenMuted, SM, CJKFont)
	drawMixedRight(c, screenW-6, screenH-14, screenTrunc(age, 60), screenMuted, SM, CJKFont)
}

func drawScreenCard(c *Canvas, x, y, w, h int) {
	c.fillRect(x, y, w, h, screenCard)
	c.rect(x, y, w, h, screenBorder)
}

func screenCell(c *Canvas, x, y, w, h int, label, value string) {
	drawScreenCard(c, x, y, w, h)
	drawMixed(c, x+8, y+3, screenTrunc(label, w-16), screenMuted, SM, CJKFont)
	MD.draw(c, x+8, y+13, screenTrunc(value, w-16), screenFG)
}

func screenBar(c *Canvas, x, y, w, h int, pct float64) {
	c.fillRect(x, y, w, h, screenCard2)
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	fw := int(float64(w) * pct / 100)
	if fw <= 0 {
		return
	}
	col := screenAccent
	switch {
	case pct >= 85:
		col = screenDanger
	case pct >= 60:
		col = screenWarn
	}
	c.fillRect(x, y, fw, h, col)
}

func screenCenter(c *Canvas, cx, y int, s string, color uint16) {
	drawMixed(c, cx-mixedWidth(s, SM, CJKFont)/2, y, s, color, SM, CJKFont)
}

// sparkBars：迷你趋势（数据驱动的动效基础组件）
func sparkBars(c *Canvas, x, y, w, h int, vs []float64, col uint16) {
	if len(vs) == 0 || w <= 0 || h <= 0 {
		return
	}
	// 比屏幕宽时先降采样到 w 列（求平均），保证每个像素都有一列。
	if len(vs) > w {
		out := make([]float64, w)
		for i := range out {
			lo := i * len(vs) / w
			hi := (i + 1) * len(vs) / w
			if hi <= lo {
				hi = lo + 1
			}
			sum := 0.0
			for j := lo; j < hi && j < len(vs); j++ {
				sum += vs[j]
			}
			out[i] = sum / float64(hi-lo)
		}
		vs = out
	}
	top := 1.0
	for _, v := range vs {
		if v > top {
			top = v
		}
	}
	step := w / len(vs)
	if step < 1 {
		step = 1
	}
	for i, v := range vs {
		bh := int(float64(h) * v / top)
		if bh < 1 {
			bh = 1
		}
		bx := x + i*step
		if bx >= x+w {
			break
		}
		c.fillRect(bx, y+h-bh, maxi(1, step-1), bh, col)
	}
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---------------------------------------------------------------- 管理页 / 错误

// screenPageAdmin：端口开放（ADMIN 位）时的访问页。
func screenPageAdmin(c *Canvas, t *Txt, snap map[string]any, anim animState) {
	sec, ok := snapF(snap, "admin_s")
	if !ok || sec < 0 {
		sec = 0
	}
	over := sec >= adminWarnSeconds
	timeCol, hintCol := screenFG, screenMuted
	if over {
		timeCol, hintCol = screenDanger, screenWarn
	}

	c.clear(screenBG)
	c.rect(0, 0, screenW, screenH, screenBorder)

	drawMaskFlow(c, (screenW-productMarkSize)/2, 18, productMark48, productMarkSize,
		productMarkSize/8, productMarkSize, anim.now)

	word := "NOKEYOUT"
	drawWordmarkFlow(c, screenW/2, 74, word, anim.now)

	url := snapS(snap, "url")
	if url == "" {
		url = "https://<device-ip>:8443"
	}
	SM.drawCenter(c, screenW/2, 108, screenTrunc(url, screenW-32), screenFG)
	SM.drawCenter(c, screenW/2, 122, "ssh :22", screenMuted)

	drawCountdown(c, screenW/2, 146, fmtAdminDur(sec), timeCol, 1)

	hint := t.get("admin_hint")
	if over {
		hint = t.get("admin_over")
	}
	screenCenter(c, screenW/2, 204, screenTrunc(hint, screenW-16), hintCol)
}

func fmtAdminDur(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	total := int(sec)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func screenPageError(c *Canvas, t *Txt, snap map[string]any) {
	drawScreenCard(c, screenMX, 34, screenW-2*screenMX, 150)
	c.rect(screenMX, 34, screenW-2*screenMX, 150, screenWarn)
	c.line(120, 56, 146, 94, screenWarn)
	c.line(146, 94, 94, 94, screenWarn)
	c.line(94, 94, 120, 56, screenWarn)
	SM.drawCenter(c, screenW/2, 68, "!", screenWarn)
	screenCenter(c, screenW/2, 112, screenTrunc(t.get("err_title"), 200), screenDanger)
	screenCenter(c, screenW/2, 130, screenTrunc(t.get("err_wait"), 200), screenMuted)
	screenCenter(c, screenW/2, 148, screenTrunc(snapS(snap, "error"), 180), screenMuted)
	screenCenter(c, screenW/2, 166, screenTrunc(t.get("err_hint"), 200), screenMuted)
}

// ---------------------------------------------------------------- 入口

func uiRender(name string, snap map[string]any, hist *History, c *Canvas, prog float64, lang, now string, anim animState) {
	uiRenderWith(name, snap, hist, c, prog, lang, now, anim, layout.current())
}

// uiRenderWith：与 uiRender 完全同一条页面状态链，只把布局换成候选文档。
// Web 预览因此和真机/首页看到的是同一套锁定、断网、统计异常与模块渲染结果。
func uiRenderWith(name string, snap map[string]any, hist *History, c *Canvas, prog float64, lang, now string, anim animState, doc layoutDoc) {
	t := newTxt(lang)
	if snapS(snap, "switch") == "unlocked" {
		screenPageAdmin(c, t, snap, anim)
		return
	}
	c.clear(screenBG) // 每帧从底开始：单屏无翻页，但仍需清底防止残影
	// 状态优先级：网络不可用 → 统计不可达 → 模块栈。
	// （LOCKED 已在上面返回；网络/统计异常时屏上显示配额/速率没有意义，整屏让给告警。）
	if netProblem(snap) != "" {
		screenPageNoNet(c, t, snap)
		return
	}
	if !snapHas(snap, "traffic") {
		screenPageError(c, t, snap)
		return
	}
	renderModulesWith(doc, c, t, snap, hist, anim)
}

// progLine：轮播进度线（页头下沿细线）。
// **8s × 30fps = 240 帧，0..239 像素逐帧 +1**：一条校准过的等速线，
// 用来肉眼验证节拍无卡顿（丢帧/慢帧立刻表现为跳 2px 或停 1 帧）。
func progLine(c *Canvas, prog float64) {
	if prog <= 0 {
		return
	}
	if prog > 1 {
		prog = 1
	}
	n := int(float64(screenW-1) * prog) // 0..239
	if n >= screenW {
		n = screenW - 1
	}
	c.hline(0, n, screenHeadH-1, screenAccent)
}

func mini(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------- console 帧

func frameConsole(name string, snap map[string]any, t *Txt, clock string) string {
	traffic := snapMap(snap, "traffic")
	if snapS(snap, "switch") == "unlocked" {
		sec, _ := snapF(snap, "admin_s")
		return fmt.Sprintf("\nNoKeyOut  ADMIN   %s  open %s\n\n  %s\n  ssh :22\n",
			clock, fmtAdminDur(sec), snapS(snap, "url"))
	}
	net := snapMap(snap, "net")
	netline := ""
	if net != nil {
		netline = fmt.Sprintf("  eth=%s inet=%v\n", snapS(net, "eth"), net["inet"])
	}
	switch name {
	case "main":
		in, _ := snapF(snapMap(traffic, "tokens_today"), "input")
		out, _ := snapF(snapMap(traffic, "tokens_today"), "output")
		callsT, _ := snapF(traffic, "calls_today")
		errT, _ := snapF(traffic, "errors_today")
		s := fmt.Sprintf("\nNoKeyOut  %s   %s\n%s", netline, clock, "")
		s += fmt.Sprintf("%-12s%s (%v err)\n", t.get("calls_today"), kfmt(callsT), int(errT))
		s += fmt.Sprintf("%-10s%s   %-10s%s\n", t.get("tokens_in"), kfmt(in), t.get("tokens_out"), kfmt(out))
		if e := snapS(snap, "error"); e != "" {
			s += "\nstats error: " + e + "\n"
		}
		return s
	case "pools":
		s := fmt.Sprintf("\nNoKeyOut · %s   %s\n%s\n", t.get("p_pools"), clock, netline)
		if pools, _ := snap["pools"].([]any); len(pools) == 0 {
			s += "  " + t.get("err_wait") + "\n"
		}
		return s
	}
	hit, _ := snapF(traffic, "cache_hit_pct")
	errT, _ := snapF(traffic, "errors_today")
	s := fmt.Sprintf("\nNoKeyOut · %s   %s\n%s\n", t.get("p_quality"), clock, netline)
	s += fmt.Sprintf("%-12s%.1f%%\n", t.get("hit"), hit)
	s += fmt.Sprintf("%-12s%.0f\n", t.get("errors_today"), errT)
	if e := snapS(snap, "error"); e != "" {
		s += "\nstats error: " + e + "\n"
	}
	return s
}
