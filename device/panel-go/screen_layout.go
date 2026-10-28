package main

// screen_layout.go —— 小屏「模块」布局：显示什么、什么顺序，以及状态机。
//
//   · **取消常驻顶部栏**（网络图标/时钟）。联网图标没必要：连不上网时整个设备不可用，
//     此时整屏用于告警更有价值；联网正常也不必专门播报。时钟同理（开发设备自己有时钟）。
//   · **模块由用户选**（Web 可编辑）：槽高合计 ≤ 屏高，最多 screenModuleMax 个。
//
// 状态优先级：LOCKED → 网络不可用 → 统计不可达 → 模块栈。

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	screenModuleMax    = 64 // 只是防呆上限（真正约束是 240px 预算）；模块可自由增删排序
	screenModuleSlot0  = 0  // 模块栈起始 y（无顶部栏）
	screenLayoutEnvKey = "SCREEN_LAYOUT_FILE"
)

// 模块**内容高**（像素）。槽高 = 内容高 + screenModuleGap，因此
// "槽高合计 ≤ 屏高" 就是唯一的排版约束（Web 端只需做加法）。
const (
	modHQuota       = bandPoolN * bandPoolH       // 3 × 36 = 108（详细行）
	modHCompact     = bandCompactN * bandCompactH // 5 × 20 = 100（简易行）
	modHUsage       = 96                          // 标题 12 + 四格 26 + 直方图 37 + 时间轴 15 + 间隔
	screenModuleGap = 6                           // 模块之间的呼吸
)

// screenModule：一个可选模块。Height 是槽高（含其后的间隔）。
// Render 的 qs 只对配额模块有意义（同栈互补切片）；其余模块忽略。
type screenModule struct {
	ID      string
	LabelZh string
	LabelEn string
	DescZh  string
	DescEn  string
	Height  int
	Render  func(c *Canvas, t *Txt, snap map[string]any, hist *History, y, h int, cfg moduleCfg)
}

var (
	modQuota = screenModule{ID: "quota", LabelZh: "配额", LabelEn: "Quota",
		DescZh: "配额窗口 · 图标 + 名字/周期 + 条或余额（简易模式一行一条）", DescEn: "Quota windows · icon + name/period + bar or balance",
		Height: modHQuota + screenModuleGap, Render: renderModuleQuota}
	// 简易行：一行一条（图标 18 + 周期 + 进度条），信息密度最高
	modUsage = screenModule{ID: "usage", LabelZh: "用量", LabelEn: "Usage",
		DescZh: "近24小时 / 7日 / 30日调用 / 输入 / 输出 / 缓存命中", DescEn: "Calls / in / out / cache hit over 24h / 7d / 30d",
		Height: modHUsage + screenModuleGap, Render: renderModuleUsage}
)

// screenModules：注册表（顺序即 Web 端下拉框顺序）。
var screenModules = []screenModule{modQuota, modUsage}

// moduleContentH：模块内容高（槽高去掉尾部间隔）。
func moduleContentH(id string, doc layoutDoc) int {
	switch id {
	case "quota":
		if doc.Compact {
			return modHCompact
		}
		return modHQuota
	case "usage":
		return modHUsage
	default:
		return 0
	}
}

// 默认布局：只放"配额 + 用量"——两者都是累计/慢变量，且都不需要钻取细节。
var screenDefaultModules = []string{"quota", "usage"}

func moduleByID(id string) (screenModule, bool) {
	for _, m := range screenModules {
		if m.ID == id {
			return m, true
		}
	}
	return screenModule{}, false
}

// moduleSlot：模块在屏上占的高度（内置 = 内容高 + 间隔；自定义 = 推导高 + 间隔）。
func moduleSlot(id string, doc layoutDoc) (int, bool) {
	if id == "quota" { // 密度由 layout 的 compact 决定 → 槽高跟着变
		return moduleContentH("quota", doc) + screenModuleGap, true
	}
	if m, ok := moduleByID(id); ok {
		return m.Height, true
	}
	return 0, false
}

// customKeyOf："custom:mine" → ("mine", true)；其它 → false。

// validateLayout：模块 id 合法（内置或已定义的自定义）、不重复、数量 ≤ max、槽高合计 ≤ 屏高。
func validateLayout(doc layoutDoc) error {
	doc = normalizeDoc(doc)
	if err := validatePicks(doc); err != nil {
		return err
	}
	if len(doc.Modules) > screenModuleMax {
		return fmt.Errorf("最多 %d 个模块，收到 %d 个", screenModuleMax, len(doc.Modules))
	}
	seen := map[string]bool{}
	total := 0
	for _, id := range doc.Modules {
		slot, ok := moduleSlot(id, doc)
		if !ok {
			return fmt.Errorf("未知模块 %q", id)
		}
		if seen[id] {
			return fmt.Errorf("模块 %q 重复", id)
		}
		seen[id] = true
		total += slot
	}
	if total > screenH {
		return fmt.Errorf("模块槽高合计 %dpx 超过屏高 %dpx", total, screenH)
	}
	return nil
}

// ---------------------------------------------------------------- 配置读写

// layoutStore：布局文件 + mtime 缓存（渲染每帧调用，最多每秒 stat 一次）。
type layoutStore struct {
	mu        sync.Mutex
	doc       layoutDoc
	mtime     time.Time
	checkedAt time.Time
}

// layoutDoc：屏上显示什么。自定义模块的定义与布局**同一份文件**——两者必须一起校验
// （引用了不存在的自定义模块就没法渲染），所以不做成两处配置。
type layoutDoc struct {
	Modules []string    `json:"modules"`
	Quotas  []quotaPick `json:"quotas,omitempty"`  // 配额自定义（空 = 自动：全部行，按剩余升序）
	Usage   string      `json:"usage,omitempty"`   // 用量周期：today|7d|30d（空 = today）
	Compact bool        `json:"compact,omitempty"` // 配额行密度：true = 简易（一行一条），false = 详细
}

// quotaPick：用户点名要上屏的一条配额。Provider 必填；Window 为空 = 该供应商的
// **固定余额**行（余额没有窗口名）。
type quotaPick struct {
	Provider string `json:"provider"`
	Window   string `json:"window"`
	// Account：magpie 的**账号**（同一供应商多账号时窗口名会完全一样）。
	// 空 = 不限账号：匹配到的第一行（兼容老配置与单账号供应商）。
	Account string `json:"account,omitempty"`
}

// moduleCfg：随 layoutDoc 一起下发给每个模块渲染器的配置。
type moduleCfg struct {
	Quotas  []quotaPick
	Usage   string
	Compact bool    // 配额简易模式
	Now     float64 // 动画时钟（秒）；0=静态
}

// pickRows：按"配额自定义"列表挑行，顺序即屏上顺序；列表为空 = 自动（原样返回）。
// 找不到的条目直接跳过——供应商下线/窗口消失时不留空行。
// pickRowsFor：按配置组装行，**并保证配置过的行不消失**。
// 若 picks 点名的窗口现在取不到（上游 401/超时 → windows 空、只带 error），
// 就用该供应商的错误行补位：窗口名沿用 pick 里的名字，错误写在条里。
func pickRowsFor(all []map[string]any, picks []quotaPick) []map[string]any {
	if len(picks) == 0 {
		return all
	}
	out := make([]map[string]any, 0, len(picks))
	for _, p := range picks {
		matched := false
		for _, m := range all {
			if snapS(m, "provider") != p.Provider || snapS(m, "window") != p.Window {
				continue
			}
			if p.Account != "" && snapS(m, "account") != p.Account {
				continue
			}
			out = append(out, m)
			matched = true
			break
		}
		if matched {
			continue
		}
		// 补位：同供应商（同账号）的错误行
		for _, m := range all {
			if snapS(m, "provider") != p.Provider || snapS(m, "err") == "" {
				continue
			}
			if p.Account != "" && snapS(m, "account") != p.Account {
				continue
			}
			row := map[string]any{
				"provider": p.Provider, "account": p.Account, "window": p.Window,
				"name": snapS(m, "name"), "kind": snapS(m, "kind"), "mode": "period",
				"has_val": false, "err": snapS(m, "err"),
			}
			out = append(out, row)
			break
		}
	}
	return out
}

func pickRows(all []map[string]any, picks []quotaPick) []map[string]any {
	if len(picks) == 0 {
		return all
	}
	out := make([]map[string]any, 0, len(picks))
	for _, p := range picks {
		for _, m := range all {
			if snapS(m, "provider") != p.Provider || snapS(m, "window") != p.Window {
				continue
			}
			// Account 空 = 不限（取第一行）；非空 = 必须同一个账号
			if p.Account != "" && snapS(m, "account") != p.Account {
				continue
			}
			out = append(out, m)
			break
		}
	}
	return out
}

var layout = &layoutStore{doc: layoutDoc{Modules: screenDefaultModules}}

// file：布局文件路径。每次都重读 env（getenv 很便宜，且 current() 每秒最多走一次），
func (s *layoutStore) file() string {
	if v := os.Getenv(screenLayoutEnvKey); v != "" {
		return v
	}
	return "/var/lib/nokeyout/state/screen-layout.json"
}

// current：当前布局（热加载：文件 mtime 变了就重读；坏文件保留上一份好的）。
func (s *layoutStore) current() layoutDoc {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.checkedAt) < time.Second {
		return s.doc
	}
	s.checkedAt = time.Now()
	fi, err := os.Stat(s.file())
	if err != nil {
		// 文件不存在（新设备）→ 默认布局
		if s.doc.Modules == nil {
			return layoutDoc{Modules: screenDefaultModules}
		}
		return s.doc
	}
	if fi.ModTime().Equal(s.mtime) {
		return s.doc
	}
	b, err := os.ReadFile(s.file())
	if err != nil {
		return s.doc
	}
	var doc layoutDoc
	if json.Unmarshal(b, &doc) != nil || validateLayout(doc) != nil {
		log.Printf("screen: 布局文件损坏，沿用上一份（%s）", s.file())
		s.mtime = fi.ModTime()
		if s.doc.Modules == nil {
			return layoutDoc{Modules: screenDefaultModules}
		}
		return s.doc
	}
	s.doc, s.mtime = normalizeDoc(doc), fi.ModTime()
	return s.doc
}

// save：原子写（临时文件 + rename），调用方负责已用会话鉴权与校验。
func (s *layoutStore) save(doc layoutDoc) error {
	doc = normalizeDoc(doc)
	if err := validateLayout(doc); err != nil {
		return err
	}
	if doc.Modules == nil {
		doc.Modules = []string{}
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	p := s.file()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	s.mu.Lock()
	s.doc, s.mtime, s.checkedAt = doc, time.Time{}, time.Time{}
	s.mu.Unlock()
	return nil
}

// moduleInfoList：给 Web 端的可用模块表。
func moduleInfoList() []map[string]any {
	out := make([]map[string]any, 0, len(screenModules))
	for _, m := range screenModules {
		out = append(out, map[string]any{
			"id": m.ID, "height": m.Height,
			"label": map[string]any{"zh": m.LabelZh, "en": m.LabelEn},
			"desc":  map[string]any{"zh": m.DescZh, "en": m.DescEn},
		})
	}
	return out
}

// ---------------------------------------------------------------- 状态机

// netProblem：网络不可用时返回原因文案（空串 = 正常）。
// 「网线没插/没连通」和「连了但出不了外网」都算不可用：前者设备完全没用，
// 后者网关调不到上游模型，屏上显示配额/速率同样没有意义。
func netProblem(snap map[string]any) string {
	net := snapMap(snap, "net")
	if net == nil {
		return "" // 拿不到网络快照时不误报（保守放行）
	}
	eth := snapS(net, "eth")
	inet := snapS(net, "inet") == "up" || net["inet"] == true
	switch {
	case eth == "down":
		return "ETH" // 网线未连接
	case eth != "" && eth != "unknown" && !inet:
		return "INET" // 无外网连通
	}
	return ""
}

// ---------------------------------------------------------------- 模块渲染

// renderModulesWith：按**给定**布局把模块自顶向下堆叠（真机、WASM、预览共用）。
// 配额模块在栈内共享行游标（quotaPlan）：详细/简易同屏时简易接续详细，不重复行。
func renderModulesWith(doc layoutDoc, c *Canvas, t *Txt, snap map[string]any, hist *History, anim animState) {
	cfg := moduleCfg{Quotas: doc.Quotas, Usage: doc.Usage, Compact: doc.Compact, Now: anim.now}
	heights, gaps := moduleHeights(doc, snap)
	type slot struct {
		id   string
		h    int // 内容高
		draw func(y, h int)
	}
	slots := make([]slot, 0, len(doc.Modules))
	for i, id := range doc.Modules {
		m, ok := moduleByID(id)
		if !ok {
			continue
		}
		h := heights[i]
		slots = append(slots, slot{id: id, h: h,
			draw: func(y, hh int) { m.Render(c, t, snap, hist, y, hh, cfg) }})
	}
	if len(slots) == 0 {
		screenCenter(c, screenW/2, screenH/2-8, t.get("no_modules"), screenMuted)
		return
	}
	y := screenModuleSlot0
	for i, sl := range slots {
		sl.draw(y, sl.h)
		y += sl.h
		if i < len(slots)-1 {
			// 模块间隔：保持底色（模块自己负责自己的视觉边界）
			if gaps > 0 {
				c.fillRect(0, y, screenW, screenModuleGap, screenBG)
				y += screenModuleGap
			}
			// 被分配高度整除不了时，行/图不会画到槽底，补一条底色
		}
	}
	if y < screenH {
		c.fillRect(0, y, screenW, screenH-y, screenBG)
	}
}

// moduleNatural：模块的**自然内容高**。
//
//	· 配额：跟着条数走（n × 行高），不再固定 3 行/5 行——加多少条就长多高；
//	· 用量：固定（标题 12 + 四格 26 + 图表 32 + 余量）。
//
// 自然高只是"想要多少"，最终按屏高缩放，见 moduleHeights。
func moduleNatural(id string, doc layoutDoc, snap map[string]any) int {
	switch id {
	case "quota":
		rowH := bandPoolH
		if doc.Compact {
			rowH = bandCompactH
		}
		n := len(pickRowsFor(singleRows(snap), doc.Quotas))
		if n == 0 {
			n = 3 // 无数据：留一块显示"数据未就绪"
		}
		return n * rowH
	case "usage":
		return modHUsage
	}
	return 0
}

// moduleHeights：按布局算出每个模块的**实际内容高**——让所有模块自然铺满整屏。
//
//  1. 先取自然高（配额跟着条数长，用量固定）；
//  2. 装得下 → 剩余空间按自然高比例**均摊**（只增不减，余数给最后一个）；
//  3. 装不下 → 按自然高**等比压缩**（配额因此可能放不全，由它的 1px/s 滚动兜底）。
//
// 结果永远满足 Σ高 + 间隔 == 屏高（有模块时），底部不留死带。
func moduleHeights(doc layoutDoc, snap map[string]any) (heights []int, gaps int) {
	n := len(doc.Modules)
	if n == 0 {
		return nil, 0
	}
	gaps = screenModuleGap * (n - 1)
	natural := make([]int, n)
	sum := 0
	for i, id := range doc.Modules {
		if _, ok := moduleByID(id); !ok {
			continue
		}
		h := moduleNatural(id, doc, snap)
		natural[i] = h
		sum += h
	}
	usable := screenH - gaps
	heights = make([]int, n)
	if sum <= 0 {
		return natural, gaps
	}
	if sum <= usable {
		left := usable - sum
		for i := range natural {
			add := left * natural[i] / sum
			heights[i] = natural[i] + add
			left -= add
			if left < 0 {
				left = 0
			}
		}
		heights[n-1] += left
		return heights, gaps
	}
	// 装不下：等比压缩（余数补给最后一个，仍然铺满）
	for i := range natural {
		heights[i] = natural[i] * usable / sum
		if heights[i] < 1 {
			heights[i] = 1
		}
	}
	total := 0
	for _, h := range heights {
		total += h
	}
	if d := usable - total; d != 0 {
		heights[n-1] += d
		if heights[n-1] < 1 {
			heights[n-1] = 1
		}
	}
	return heights, gaps
}

func normalizeDoc(doc layoutDoc) layoutDoc {
	out := doc
	mods := make([]string, 0, len(doc.Modules))
	hasQuota := false
	for _, id := range doc.Modules {
		if id != "quota_s" {
			mods = append(mods, id)
			if id == "quota" {
				hasQuota = true
			}
			continue
		}
		out.Compact = true
		if hasQuota { // 旧的「quota + quota_s 同屏」合并成一块
			continue
		}
		hasQuota = true
		mods = append(mods, "quota")
	}
	out.Modules = mods
	return out
}

// validatePicks：配额自定义列表与用量周期。
//   - 用量周期只认 today / 7d / 30d（空 = today）；
//   - 每条 pick 必须有 provider（window 空 = 固定余额行）；
//   - 同一 (provider, window) 不允许重复（重复只会在屏上重复画同一行）。
func validatePicks(doc layoutDoc) error {
	switch doc.Usage {
	case "", "today", "7d", "30d":
	default:
		return fmt.Errorf("用量周期 %q 不支持（today/7d/30d）", doc.Usage)
	}
	// 用户想重复放同一条（比如临时凑够条数看滚动）也应该允许；真正约束只有屏高预算。
	for i, p := range doc.Quotas {
		if strings.TrimSpace(p.Provider) == "" {
			return fmt.Errorf("配额自定义第 %d 条缺少供应商", i+1)
		}
	}
	return nil
}

//	false（默认）= 详细 36px/条  ·  true = 简易 20px/条
//
// **条数不设限**：超出模块容量时整块变成**滚动视口**——内容以 **1px/s** 匀速上滚、
// 因此每帧只差 1px、也不需要跨帧保存状态；测试里 nowFn 固定 → 黄金样本稳定。
func renderModuleQuota(c *Canvas, t *Txt, snap map[string]any, hist *History, y, h int, cfg moduleCfg) {
	rows := pickRowsFor(singleRows(snap), cfg.Quotas)
	rowH := bandPoolH
	if cfg.Compact {
		rowH = bandCompactH
	}
	// 视口高 = 分配到的内容高（铺满整屏后可能比自然容量高）；容量按行高向下取整
	viewH := h
	if viewH <= 0 {
		viewH = rowH * bandPoolN
	}
	cap := viewH / rowH
	c.fillRect(0, y, screenW, viewH, screenBG)
	if len(rows) == 0 {
		screenCenter(c, screenW/2, y+40, t.get("err_wait"), screenMuted)
		return
	}
	drawRow := func(dst *Canvas, ry, i int) {
		active := providerActive(snap, snapS(rows[i], "provider"))
		if cfg.Compact {
			drawQuotaRowCompact(dst, t, ry, i == 0, rows[i], 0, active, cfg.Now)
		} else {
			drawQuotaRowDetailed(dst, t, ry, i == 0, rows[i], 0, active, cfg.Now)
		}
	}
	// 装得下：**行高固定**（36px/20px 的设计单元，不随容器拉高），且**顶部对齐**
	// （用户要求往上排；富余高度留在容器底部，不居中）。
	if len(rows) <= cap {
		for i := range rows {
			drawRow(c, y+i*rowH, i)
		}
		return
	}
	// 装不下：离屏画布按偏移排两遍（第二遍是循环接续），整块贴回来
	contentH := len(rows) * rowH
	off := int(nowFn().Unix() % int64(contentH)) // 1px/s：秒数即像素偏移
	scratch := newCanvas(screenW, viewH)
	scratch.clear(screenBG)
	for pass := 0; pass < 2; pass++ {
		for i := range rows {
			ry := pass*contentH + i*rowH - off
			if ry >= viewH || ry+rowH <= 0 {
				continue
			}
			drawRow(scratch, ry, i)
		}
	}
	c.blitFrom(y, scratch)
}

// renderModuleUsage：周期用量——**四格文字（调用/输入/输出/命中）+ token 直方图**。
// 四格与图表都取同一个周期（cfg.Usage），切换周期时两者一起变。
func renderModuleUsage(c *Canvas, t *Txt, snap map[string]any, hist *History, y, h int, cfg moduleCfg) {
	if h <= 0 {
		h = moduleContentH("usage", layoutDoc{})
	}
	period := cfg.Usage
	if period == "" {
		period = "today"
	}
	u := snapMap(snapMap(snap, "usage"), period)
	c.hline(0, screenW-1, y, screenBorder)
	drawMixed(c, 8, y+2, t.get("u_"+period), screenMuted, SM, CJKFont)
	total, totalOK := snapF(u, "tokens")
	if snapS(u, "period") == "" {
		totalOK = false
	}
	num := func(k string) string {
		v, ok := snapF(u, k)
		if !ok {
			return "-"
		}
		return kfmt(v)
	}
	calls, in, out := num("calls"), num("input"), num("output")
	hitS := "-"
	if v, ok := snapF(u, "cache_hit_pct"); ok {
		hitS = f1(v) + "%"
	}
	if totalOK {
		drawMixedRight(c, screenW-8, y+2, kfmt(total)+" tok", screenFG, SM, CJKFont)
	} else {
		drawMixedRight(c, screenW-8, y+2, "-", screenMuted, SM, CJKFont)
	}
	pad, gap := 8, 4
	half := (screenW - 2*pad - gap) / 2
	left, right := pad, pad+half+gap
	drawKV(c, left, y+15, half, t.get("calls_p"), calls, screenFG)
	drawKV(c, right, y+15, half, t.get("tokens_in"), in, screenFG)
	drawKV(c, left, y+28, half, t.get("tokens_out"), out, screenMuted)
	drawKV(c, right, y+28, half, t.get("hit"), hitS, screenMuted)
	// 直方图 + 底部时间轴：图吃掉剩余高度，轴固定 15px（基线 1 + 刻度 2 + 标签 11 + 余量）
	axisH := 15
	chartH := h - 42 - axisH
	if chartH < 6 {
		chartH = 6
	}
	histBars(c, 8, y+42, screenW-16, chartH, bucketsOf(u))
	colDur, nCols := columnSpec(usagePeriodLen(period), screenW-16)
	// 轴的起点必须与柱子的起点一致：优先用上游的 since；缺了就从当前时刻回推一个周期
	since, err := time.Parse(time.RFC3339, snapS(u, "since"))
	if err != nil || since.IsZero() {
		since = nowFn().Add(-usagePeriodLen(period))
	}
	drawTimeAxis(c, t, 8, y+42+chartH, screenW-16, since, usagePeriodLen(period), colDur, nCols, period)
}

// drawTimeAxis：直方图下方的**时间轴**（刻度 + 少量标签）。
//
//	· today  → 每小时一个刻度（10 分钟/列时 = 每 6 列一格），每 6 小时一个标签（0/6/12/18）
//	· 7d     → 每天一个刻度，**每天一个标签**（日号）
//	· 30d    → 每天一个刻度，每 7 天一个标签（够宽、够读）
//
// 刻度画在基线下方 2px，标签再往下 11px（点阵字高度）。
func drawTimeAxis(c *Canvas, t *Txt, x, yb, w int, since time.Time, period, colDur time.Duration, n int, per string) {
	if period <= 0 || w <= 0 {
		return
	}
	c.fillRect(x, yb, w, 1, screenBorder) // 基线
	tickEvery, labelEvery := time.Hour, 6*time.Hour
	switch per {
	case "7d":
		tickEvery, labelEvery = 24*time.Hour, 24*time.Hour
	case "30d":
		tickEvery, labelEvery = 24*time.Hour, 7*24*time.Hour
	}
	pos := func(d time.Duration) int { // 时间偏移 → 像素
		px := x + int(float64(d)/float64(period)*float64(w))
		if px < x {
			px = x
		}
		if px > x+w-1 {
			px = x + w - 1
		}
		return px
	}
	// 刻度从**本地整点/零点**起算（滚动窗口的起点通常不是整点）。
	align := since
	switch per {
	case "today":
		align = since.Truncate(time.Hour)
		if align.Before(since) {
			align = align.Add(time.Hour)
		}
	case "7d", "30d":
		align = time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, since.Location())
		if align.Before(since) {
			align = align.AddDate(0, 0, 1)
		}
	}
	for d := align.Sub(since); d < period; d += tickEvery {
		px := pos(d)
		c.fillRect(px, yb+1, 1, 2, screenBorder)
	}
	labelStart := align
	if per == "today" {
		// 标签落在本地 0/6/12/18 点，而不是“恰好 6 小时之后”。
		labelStart = time.Date(align.Year(), align.Month(), align.Day(), align.Hour()/6*6, 0, 0, 0, align.Location())
		if labelStart.Before(since) {
			labelStart = labelStart.Add(6 * time.Hour)
		}
	}
	for d := labelStart.Sub(since); d < period-1; d += labelEvery {
		lbl := axisLabel(since.Add(d), per)
		tw := SM.width(lbl)
		lx := pos(d) - tw/2
		if lx < x {
			lx = x
		}
		if lx+tw > x+w {
			lx = x + w - tw
		}
		SM.draw(c, lx, yb+4, lbl, screenMuted)
	}
}

// axisLabel：轴上的文字。today 用本地时钟（18/00/06/12），其余用日号。
func axisLabel(at time.Time, per string) string {
	if per == "today" {
		return at.Format("15")
	}
	return strconv.Itoa(at.Day())
}

// bucketsOf：快照里的分桶 → 数值序列（按上游顺序，不重排）。
func bucketsOf(u map[string]any) []float64 {
	arr, _ := u["buckets"].([]any)
	out := make([]float64, 0, len(arr))
	for _, raw := range arr {
		m, _ := raw.(map[string]any)
		if v, ok := snapF(m, "tokens"); ok {
			out = append(out, v)
		}
	}
	return out
}

// histBars：把上游的桶按**时间**铺满整条宽度——1px = 周期 / 宽度。
//
// 上游最细只有两档：today 按小时（24 桶）、7d/30d 按天（7/30 桶），没有更细的序列
// 而是**每 1px 一列**：该像素覆盖的时间片中点落在哪个桶，就用那个桶的值。
// 结果是一张台阶状剖面（柱宽 = 宽度/桶数，today ≈ 9px、30d ≈ 7px），**柱间不留空隙**——
// 留空隙会让 0 值桶变成 1px 碎屑，反而更花。
func histBars(c *Canvas, x, y, w, h int, vs []float64) {
	n := len(vs)
	if n == 0 || h < 4 || w < 8 {
		return
	}
	// 宽度比桶数还窄：先并桶（求平均），保证每列都有值
	if n > w {
		agg := make([]float64, w)
		for i := 0; i < w; i++ {
			lo, hi := i*n/w, (i+1)*n/w
			if hi <= lo {
				hi = lo + 1
			}
			sum := 0.0
			for k := lo; k < hi; k++ {
				sum += vs[k]
			}
			agg[i] = sum / float64(hi-lo)
		}
		vs, n = agg, w
	}
	mx := 0.0
	for _, v := range vs {
		if v > mx {
			mx = v
		}
	}
	for i := 0; i < w; i++ {
		bi := int((float64(i) + 0.5) * float64(n) / float64(w))
		if bi >= n {
			bi = n - 1
		}
		bh := 1 // 0 值也留 1px 基线：看得出"这段没用量"
		if mx > 0 && vs[bi] > 0 {
			bh = int(float64(h) * vs[bi] / mx)
			if bh < 1 {
				bh = 1
			}
		}
		col := screenAccentDim
		if bi == n-1 { // 当前桶（当前小时 / 今天）高亮
			col = screenAccent
		}
		c.fillRect(x+i, y+h-bh, 1, bh, col)
	}
}
