//go:build !(js && wasm)

package main

// proxy.go —— 上游助手：3430 web cookie 握手、GET 助手、控制台/兜底反代、统计轮询

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// webCookies：一次性拿 3430 的会话 cookie（?k= 握手），只在服务端持有。
// TTL 5 分钟强制重握手（网关/容器重建后上游会话会变）。
type webCookies struct {
	mu     sync.Mutex
	cookie []string
	ts     time.Time
	cfg    *cfg
}

func newWebCookies(c *cfg) *webCookies { return &webCookies{cfg: c} }

func (wc *webCookies) get(force bool) []string {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	if wc.cfg.magpieWebKey == "" {
		return nil
	}
	now := time.Now()
	if !force && wc.cookie != nil && now.Sub(wc.ts) < 5*time.Minute {
		return wc.cookie
	}
	u, err := url.Parse(wc.cfg.upstreamWeb)
	if err != nil {
		return nil
	}
	cl := &http.Client{
		Timeout:       8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := cl.Get(u.String() + "/?k=" + url.QueryEscape(wc.cfg.magpieWebKey))
	if err != nil {
		wc.cookie = nil
		return nil
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	var ck []string
	if sc := resp.Header.Get("Set-Cookie"); sc != "" {
		ck = []string{sc}
	}
	wc.cookie, wc.ts = ck, time.Now()
	return ck
}

// httpGet：panel 内部的 GET 助手（status/models/icons/stats/system-info 用）
func (p *panel) httpGet(rawURL string, headers map[string]string, timeout time.Duration) (int, map[string]string, []byte) {
	cl := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, nil, nil
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := cl.Do(req)
	if err != nil {
		return 0, nil, nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	hd := map[string]string{}
	for k, v := range resp.Header {
		if len(v) > 0 {
			hd[k] = v[0]
		}
	}
	return resp.StatusCode, hd, b
}

func logProxyRead(err error) { log.Printf("upstream read: %v", err) }

// consoleProxy：透传到 magpie web（:3430，注入上游 cookie）。两个用途：
//
//	① /console/* 剥前缀后转发；② catch-all 兜底（magpie web 的 API 全是绝对路径）。
func (p *panel) consoleProxy(w http.ResponseWriter, r *http.Request, upPath string, retry bool) {
	ck := p.cookies.get(false)
	if ck == nil {
		p.send(w, r, 502, []byte("upstream web unreachable (magpie-web key?)"), "text/plain; charset=utf-8", nil)
		return
	}
	var body []byte
	if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch || r.Method == http.MethodDelete {
		body = p.body(r)
	}
	out, err := http.NewRequestWithContext(r.Context(), r.Method, strings.TrimRight(p.cfg.upstreamWeb, "/")+upPath, nil)
	if err != nil {
		p.errJSON(w, r, 400, "bad path")
		return
	}
	if body != nil {
		out.Body = io.NopCloser(strings.NewReader(string(body)))
		out.ContentLength = int64(len(body))
	}
	for k, vs := range r.Header {
		lk := strings.ToLower(k)
		if dropHdrs[lk] || lk == "host" {
			continue
		}
		for _, v := range vs {
			out.Header.Add(k, v)
		}
	}
	out.Header.Set("Cookie", strings.Join(ck, "; "))
	cl := &http.Client{Timeout: 60 * time.Second}
	resp, err := cl.Do(out)
	if err != nil {
		p.send(w, r, 502, []byte(fmt.Sprintf("upstream unreachable: %v", err)), "text/plain; charset=utf-8", nil)
		return
	}
	defer resp.Body.Close()
	if retry && (resp.StatusCode == 303 || resp.StatusCode == 401) && r.Method == http.MethodGet {
		ck2 := p.cookies.get(true)
		if len(ck2) > 0 && strings.Join(ck2, ";") != strings.Join(ck, ";") {
			p.consoleProxy(w, r, upPath, false)
			return
		}
	}
	h := w.Header()
	for k, vs := range resp.Header {
		if dropHdrs[strings.ToLower(k)] {
			continue
		}
		for _, v := range vs {
			h.Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	fl, _ := w.(http.Flusher)
	buf := make([]byte, 64<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
		}
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) {
				logProxyRead(rerr)
			}
			return
		}
	}
}

// ---------------------------------------------------------------- stats

// stats：轮询网关 usage/quotas，算增量。契约：GET /api/stats、/api/quotas、
// /v1/stats/snapshot（screend 用 X-Screen-Token）。
type stats struct {
	cfg      *cfg
	interval time.Duration
	mu       sync.Mutex
	cur      map[string]any // 今日累计（兼容旧字段）
	usage    map[string]any // 三周期用量视图：{"today":…,"7d":…,"30d":…}
	pools    []any
	hist     *usageHist // 面板自建 5 分钟桶 → 1px 一桶（不足则回退 API 粗桶）
	ts       time.Time
	err      string
	stop     chan struct{}
	once     sync.Once

	// 最近服务过的供应商与活动截止时间。
	lastProvider string
	lastTokens   int64
	activeUntil  time.Time
}

// providerActiveHold：活动指示保持时长。
const providerActiveHold = 8 * time.Second

func newStats(c *cfg) *stats {
	h := newUsageHist(c.usageHistPath)
	if h != nil {
		if _, err := h.importJSONL(c.usageLogPath); err == nil {
			h.save(true)
		}
	}
	return &stats{cfg: c, interval: 2 * time.Second, stop: make(chan struct{}), hist: h}
}

func (s *stats) authHdr() map[string]string {
	if s.cfg.panelGatewayKey != "" {
		return map[string]string{"Authorization": "Bearer " + s.cfg.panelGatewayKey}
	}
	return nil
}

func (s *stats) pollUsage() {
	usage := map[string]any{}
	var today map[string]any

	for _, per := range usagePeriods {
		st, _, body := s.httpGetHelper(s.cfg.gatewayBase+"/v1/magpie/usage?period="+per, 8*time.Second)
		if st != 200 {
			s.setErr(fmt.Sprintf("usage HTTP %d", st))
			continue
		}
		var u map[string]any
		if json.Unmarshal(body, &u) != nil {
			s.setErr("usage 响应不是 JSON")
			continue
		}
		view := useView(u)
		usage[per] = view
		if per == "today" {
			today = map[string]any{
				"cache_hit_pct": cacheHitPct(u),
				"tokens_today":  map[string]any{"input": i64(u["input"]), "output": i64(u["output"])},
				"calls_today":   i64(u["calls"]),
				"errors_today":  i64(u["errors"]),
			}
		}
	}
	now := time.Now()
	// 记录增量（tokens/调用/输入/输出）到 1 分钟槽，再用它构造滚动窗口。
	if s.hist != nil {
		if tv, ok := usage["today"].(map[string]any); ok {
			tok, _ := snapF(tv, "tokens")
			calls, _ := snapF(tv, "calls")
			in, _ := snapF(tv, "input")
			out, _ := snapF(tv, "output")
			s.hist.observe(now, histSlot{tok: int64(tok), calls: int64(calls), in: int64(in), out: int64(out)})
			s.hist.save(false)
		}
		for per, raw := range usage {
			view, _ := raw.(map[string]any)
			if view == nil {
				continue
			}
			colDur, nCols := columnSpec(usagePeriodLen(per), screenW-16)
			if per == "today" {
				// 滚动 24h：窗口末端是当前时刻，不是本地 0 点。
				since := now.Add(-usagePeriodLen(per))
				win := s.hist.window(since, usagePeriodLen(per))
				if win.tok > 0 {
					fine := s.hist.series(since, usagePeriodLen(per), nCols)
					view["since"] = since.Format(time.RFC3339)
					view["tokens"] = win.tok
					view["calls"] = win.calls
					view["input"] = win.in
					view["output"] = win.out
					view["buckets"] = fineBuckets(fine)
					view["buckets_src"] = "hist"
					view["col_seconds"] = int64(colDur / time.Second)
					continue
				}
			}
			since, err := time.Parse(time.RFC3339, snapS(view, "since"))
			if err != nil {
				continue
			}
			fine := s.hist.series(since, usagePeriodLen(per), nCols)
			if len(fine) == 0 {
				continue
			}
			sum := int64(0)
			for _, n := range fine {
				sum += n
			}
			total, _ := snapF(view, "tokens")
			// 自检：自建桶明显少于 API 总量（停机缺口/刚启动）→ 退回 API 粗桶。
			if total <= 0 || float64(sum) >= 0.9*total {
				view["buckets"] = fineBuckets(fine)
				view["buckets_src"] = "hist"
				view["col_seconds"] = int64(colDur / time.Second)
			} else {
				view["buckets_src"] = "api(partial-hist)"
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if today != nil {
		s.cur = today
		// 今日 tokens 增长 = 刚刚又发生了一次调用。用 magpie 的“最近服务供应商”
		// 归因，点亮该行图标 providerActiveHold；没有新调用则到期自动熄灭。
		var tok int64
		if tt, ok := today["tokens_today"].(map[string]any); ok {
			in, _ := tt["input"].(int64)
			out, _ := tt["output"].(int64)
			tok = in + out
		}
		if s.lastTokens > 0 && tok > s.lastTokens && s.lastProvider != "" {
			s.activeUntil = now.Add(providerActiveHold)
		}
		s.lastTokens = tok
	}
	if len(usage) > 0 {
		s.usage = usage
	}
	s.ts = now
	s.err = ""
}

// bucketVals：快照里的分桶 → 数值切片（播种用）。
func bucketVals(v map[string]any) []float64 {
	arr, _ := v["buckets"].([]any)
	out := make([]float64, 0, len(arr))
	for _, raw := range arr {
		m, _ := raw.(map[string]any)
		if n, ok := snapF(m, "tokens"); ok {
			out = append(out, n)
		}
	}
	return out
}

// fineBuckets：[]int64 → 渲染层吃的那种 [{label,tokens}]（label 在这条路径上没意义）。
func fineBuckets(v []int64) []any {
	out := make([]any, 0, len(v))
	for _, n := range v {
		out = append(out, map[string]any{"label": "", "tokens": n})
	}
	return out
}

// useView：上游 usage → 屏上周期视图。
// tokens = input + output（"这个周期干了多少活"）；cache_read 单列，不混进直方图——
// 它常是 input 的几十倍，混进去会把柱子压成一条平线。buckets 是**上游自带的分桶**
// （today 按小时，7d/30d 按天），不是面板采样。
func useView(u map[string]any) map[string]any {
	buckets := make([]any, 0, 32)
	if arr, ok := u["series"].([]any); ok {
		for _, raw := range arr {
			m, _ := raw.(map[string]any)
			in, _ := toF(m["input"])
			out, _ := toF(m["output"])
			buckets = append(buckets, map[string]any{
				"label":  snapS(m, "label"),
				"tokens": int64(in + out),
			})
		}
	}
	in, _ := toF(u["input"])
	out, _ := toF(u["output"])
	cr, _ := toF(u["cache_read"])
	calls, _ := toF(u["calls"])
	errs, _ := toF(u["errors"])
	cost, _ := toF(u["cost"])
	hit := 0.0
	if int64(cr+in) > 0 {
		hit = round1(100 * cr / (cr + in))
	}
	return map[string]any{
		"period": snapS(u, "period"), "since": snapS(u, "since"),
		"calls": int64(calls), "errors": int64(errs),
		"input": int64(in), "output": int64(out), "cache_read": int64(cr),
		"tokens": int64(in + out), "cost": cost, "cache_hit_pct": hit,
		"buckets": buckets,
	}
}

func (s *stats) pollQuotas() {
	st, _, body := s.httpGetHelper(s.cfg.gatewayBase+"/v1/magpie/quotas", 8*time.Second)
	if st != 200 {
		s.setErr(fmt.Sprintf("quotas HTTP %d", st))
		return
	}
	var q struct {
		Data []struct {
			Provider string `json:"provider"`
			Name     string `json:"name"`
			Kind     string `json:"kind"`
			Windows  []struct {
				Name      string   `json:"name"`
				Used      *float64 `json:"used"`      // 已用 %（指针：区分「缺字段」与「值为 0」）
				Remaining *float64 `json:"remaining"` // 剩余 %
				ResetsAt  string   `json:"resetsAt"`  // RFC3339；只有部分窗口有
			} `json:"windows"`
			// 上游错误：magpie 取数失败时会带这个（如 "401 Unauthorized: key expired"），
			// 并把 windows 置空。屏上要**留行报错**，不能整条消失。
			Error string `json:"error"`
			// 账号：同一供应商下可能有多个账号（窗口名往往一模一样）
			Account      string   `json:"account"`
			AccountID    string   `json:"accountId"`
			AccountNames []string `json:"accountNames"`
			Last         bool     `json:"last"` // magpie 标"最近服务过的那一个"
			// LastServedAt：magpie 记录的最近一次调用时间（RFC3339）。
			// 用于"供应商正在被调用"的图标动画归因。
			LastServedAt string `json:"lastServedAt"`
			// 固定余额（key 型供应商）：magpie 给 balance，或 balances[]（可能多币种）
			Balance  *float64 `json:"balance"`
			Currency string   `json:"currency"`
			Balances []struct {
				Currency  string   `json:"currency"`
				Total     *float64 `json:"total"`
				Remaining *float64 `json:"remaining"`
				Amount    *float64 `json:"amount"`
				Balance   *float64 `json:"balance"`
			} `json:"balances"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &q) != nil {
		return
	}
	var pools []Pool
	var newestAt time.Time
	var newestProvider string
	for _, p := range q.Data {
		// lastServedAt：取所有供应商里最新的一次调用，作为"最近使用"。
		if ts, err := time.Parse(time.RFC3339Nano, p.LastServedAt); err == nil {
			if ts.After(newestAt) {
				newestAt, newestProvider = ts, p.Provider
			}
		} else if p.Last && newestProvider == "" {
			newestProvider = p.Provider
		}
		name := p.Name
		if name == "" {
			name = p.Provider
		}
		if name == "" {
			name = "?"
		}
		acct := strings.TrimSpace(p.Account)
		if acct == "" {
			acct = strings.TrimSpace(p.AccountID)
		}
		if acct == "" && len(p.AccountNames) == 1 {
			acct = strings.TrimSpace(p.AccountNames[0])
		}
		for _, w := range p.Windows {
			wn := w.Name
			if wn == "" {
				wn = "?"
			}
			var usedPct, remainPct float64
			has := w.Used != nil && w.Remaining != nil
			if w.Used != nil {
				usedPct = *w.Used
			}
			if w.Remaining != nil {
				remainPct = *w.Remaining
			}
			pl := buildPool(p.Provider, name, p.Kind, wn, usedPct, remainPct, has)
			pl.Account = acct
			if t, err := time.Parse(time.RFC3339, w.ResetsAt); err == nil {
				pl.Reset, pl.HasReset = t, true
			}
			pools = append(pools, pl)
		}
		// 取不到窗口（上游报错/尚未就绪）：仍然出一条**错误行**，保证供应商不消失。
		if len(p.Windows) == 0 {
			pl := buildPool(p.Provider, name, p.Kind, "", 0, 0, false)
			pl.Account = acct
			pl.Err = strings.TrimSpace(p.Error)
			pools = append(pools, pl)
		}
		// 固定余额：没有周期、没有重置，单独成一条行（window=""）。
		if p.Balance != nil {
			bp := buildBalancePool(p.Provider, name, *p.Balance, p.Currency)
			bp.Account = acct
			pools = append(pools, bp)
		} else if len(p.Balances) > 0 {
			b := p.Balances[0]
			amt := 0.0
			for _, cand := range []*float64{b.Remaining, b.Amount, b.Balance, b.Total} {
				if cand != nil {
					amt = *cand
					break
				}
			}
			cur := b.Currency
			if cur == "" {
				cur = p.Currency
			}
			bp := buildBalancePool(p.Provider, name, amt, cur)
			bp.Account = acct
			pools = append(pools, bp)
		}
	}
	sortPools(pools)
	s.mu.Lock()
	s.pools = poolsToAny(pools)
	if newestProvider != "" {
		s.lastProvider = newestProvider
	}
	s.mu.Unlock()
}

// poolsToAny：快照契约 []any（map 形式）。字段名与渲染层/用户 JSON 页约定一致。
// used_pct 保留（用户页在用 pools[0].used_pct）。
func poolsToAny(ps []Pool) []any {
	out := make([]any, 0, len(ps))
	for _, p := range ps {
		m := map[string]any{
			"name": p.Name, "provider": p.Provider, "window": p.Window, "kind": p.Kind,
			"account": p.Account,
			"state":   int(p.State), "has_val": p.HasVal, "has_reset": p.HasReset,
		}
		if p.HasVal {
			m["used_pct"] = round1(p.UsedPct)
			m["remain_pct"] = round1(p.RemainPct)
		} else {
			m["used_pct"] = nil
		}
		if p.HasReset {
			m["reset_at"] = p.Reset.Unix()
			m["period_s"] = int64(p.Period.Seconds())
		}
		m["mode"] = p.Mode
		if p.Err != "" {
			m["err"] = p.Err
		}
		if p.HasAmount {
			m["amount"] = p.Amount
			m["unit"] = p.Unit
		}
		out = append(out, m)
	}
	return out
}

func (s *stats) httpGetHelper(u string, d time.Duration) (int, map[string]string, []byte) {
	cl := &http.Client{Timeout: d}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, nil
	}
	for k, v := range s.authHdr() {
		req.Header.Set(k, v)
	}
	resp, err := cl.Do(req)
	if err != nil {
		return 0, nil, nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	return resp.StatusCode, nil, b
}

func (s *stats) setErr(e string) {
	s.mu.Lock()
	s.err = e
	s.mu.Unlock()
}

func (s *stats) snapshot() map[string]any {
	locked, _ := readState()
	if s.cfg.switchdURL != "" {
		locked = switchState(s.cfg) == "locked"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var usageAge any
	if !s.ts.IsZero() {
		usageAge = round1(time.Since(s.ts).Seconds())
	}
	sw := "unlocked"
	if locked {
		sw = "locked"
	}
	panelURL := ""
	if ip := lanIP(); ip != "" {
		panelURL = fmt.Sprintf("https://%s:%d", ip, s.cfg.port)
	}
	return map[string]any{
		"generated_at": time.Now().Unix(),
		"switch":       sw,
		"lang":         langGet(),
		"net":          netmon.snapshot(),
		"traffic":      s.cur,
		"usage":        s.usage,
		"pools":        s.pools,
		"usage_age_s":  usageAge,
		"dash":         s.dashSnapLocked(),
		"url":          panelURL,
		"error":        s.err,
		// 活动供应商：渲染层据此给该行图标加不确定进度指示器（M3）。
		"active_provider": s.lastProvider,
		"active_until":    s.activeUntil.Unix(),
	}
}

// lanIP：默认路由的源地址（UDP connect 不发包）。拿不到就返回空。
func lanIP() string {
	c, err := net.Dial("udp4", "223.5.5.5:53")
	if err != nil {
		return ""
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}
	return ""
}

// dashSnapLocked：渲染层消费的结构化结论（调用方已持 s.mu）。
// 数据龄 = usage 最近一次成功轮询的距今秒数；超过 staleAfter 即过期。
func (s *stats) dashSnapLocked() map[string]any {
	age := -1.0
	if !s.ts.IsZero() {
		age = time.Since(s.ts).Seconds()
	}
	return map[string]any{
		"age_s": age,
		"stale": age < 0 || age > staleAfter.Seconds(),
	}
}

func (s *stats) start() {
	s.once.Do(func() {
		s.pollUsage()
		s.pollQuotas()
		go func() {
			t := time.NewTicker(s.interval)
			defer t.Stop()
			n := 0
			for {
				select {
				case <-s.stop:
					return
				case <-t.C:
					s.pollUsage()
					n++
					if n%15 == 0 {
						s.pollQuotas()
					}
				}
			}
		}()
	})
}

// 数值助手（magpie usage 的数字可能是 float64 或字符串）
// cacheHitPct：今日**累计**缓存命中率 = cache_read / (cache_read + input)。
//
// 必须用累计值，不能用两次轮询的增量：轮询间隔 2s，绝大多数窗口内根本没有调用，
// 命中率是"账本比例"（整天累计），和 tok/s 这种瞬时速率不是一类量。
func cacheHitPct(u map[string]any) float64 {
	cr, inp := i64(u["cache_read"]), i64(u["input"])
	if cr+inp <= 0 {
		return 0
	}
	return round1(100 * float64(cr) / float64(cr+inp))
}

func i64(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case string:
		var n int64
		fmt.Sscanf(t, "%d", &n)
		return n
	}
	return 0
}

func round1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }
func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }
func round3(f float64) float64 { return float64(int64(f*1000+0.5)) / 1000 }
