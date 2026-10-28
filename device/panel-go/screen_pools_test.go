package main

// screen_pools_test.go —— 配额端到端回归：真实 pollQuotas → snapshot → 渲染。
//
//   used/remaining 是**百分比**（相加=100），没有 total/unit；resetsAt 只有部分窗口有。

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const quotaFixture = `{"data":[{"provider":"opencode-go","name":"OpenCode Go","kind":"plan","windows":[
  {"name":"5 hours","used":0,"remaining":100},
  {"name":"7 days","used":22,"remaining":78,"resetsAt":"2026-10-19T00:00:00Z"},
  {"name":"Month","used":100,"remaining":0,"resetsAt":"2026-10-22T06:01:37Z"}
],"lastServedAt":"2026-10-13T08:12:17.758330561Z","last":true}],"object":"list"}`

func TestPollQuotasFeedsPoolsRenderer(t *testing.T) {
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(quotaFixture))
	}))
	defer gw.Close()

	s := &stats{
		cfg:      &cfg{gatewayBase: gw.URL, panelGatewayKey: "k"},
		interval: 0,
		stop:     make(chan struct{}),
	}
	s.pollQuotas()

	snap := s.snapshot()
	pools, ok := snap["pools"].([]any)
	if !ok || len(pools) != 3 {
		t.Fatalf("snapshot pools 类型/长度不对: ok=%v len=%d", ok, len(pools))
	}

	rows := singleRows(snap)
	if len(rows) != 3 {
		t.Fatalf("渲染层读到 %d 行，want 3", len(rows))
	}

	// 排序：剩余最少在最前（Month 0% → 7 days 78% → 5 hours 100%）
	if got := snapS(rows[0], "window"); got != "Month" {
		t.Fatalf("首行应为剩余最少的 Month，got %q", got)
	}
	if v, _ := snapF(rows[0], "remain_pct"); v != 0 {
		t.Fatalf("Month 剩余应为 0%%，got %v", v)
	}
	if v, _ := snapF(rows[2], "remain_pct"); v != 100 {
		t.Fatalf("5 hours 剩余应为 100%%，got %v", v)
	}

	// 重置时间只在给了 resetsAt 的窗口上出现，且周期由窗口名解析
	if !snapB(rows[0], "has_reset") {
		t.Fatal("Month 有 resetsAt，has_reset 应为 true")
	}
	if got, _ := snapF(rows[0], "period_s"); got != float64(31*24*3600) {
		t.Fatalf("Month 周期应为 31 天（2026-10），got %v", got)
	}
	if snapB(rows[2], "has_reset") {
		t.Fatal("5 hours 没有 resetsAt，has_reset 应为 false（不编造）")
	}
	if got, _ := snapF(rows[1], "period_s"); got != float64(7*24*3600) {
		t.Fatalf("7 days 周期应为 7 天，got %v", got)
	}

	// 状态分级：0% → crit
	if got, _ := snapF(rows[0], "state"); int(got) != int(poolCrit) {
		t.Fatalf("剩余 0%% 应为 crit，got %v", got)
	}
}

// 活动供应商归因：magpie 的 lastServedAt 决定"最近使用"，渲染层据此点亮图标。
func TestActiveProviderFromLastServedAt(t *testing.T) {
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(quotaFixture))
	}))
	defer gw.Close()
	s := &stats{cfg: &cfg{gatewayBase: gw.URL, panelGatewayKey: "k"}, interval: 0,
		stop: make(chan struct{})}
	s.pollQuotas()
	snap := s.snapshot()
	if got := snapS(snap, "active_provider"); got != "opencode-go" {
		t.Fatalf("active_provider = %q，want opencode-go", got)
	}
}

// 余额卡：magpie 的 balance 是**展示字符串**，不是数字。旧解析把整份配额
// Unmarshal 成错误——新增一条余额供应商后，屏上所有配额都停在旧值。
// 这里同时放余额卡和周期卡，确认两者都能进 snapshot。
func TestPollQuotasBalanceString(t *testing.T) {
	fixture := `{"data":[
      {"provider":"relay","name":"Relay","kind":"balance","balance":"$12.34","user":"acct@example.com","windows":[]},
      {"provider":"points","name":"Points","kind":"balance","balance":"6,291 points left","windows":[]},
      {"provider":"opencode-go","name":"OpenCode Go","kind":"plan","windows":[
        {"name":"7 days","used":22,"remaining":78}]}
    ],"object":"list"}`
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(fixture))
	}))
	defer gw.Close()

	s := &stats{cfg: &cfg{gatewayBase: gw.URL}, stop: make(chan struct{})}
	s.pollQuotas()
	rows := singleRows(s.snapshot())
	if len(rows) != 3 { // 2 个余额卡 + 1 张周期卡
		t.Fatalf("rows = %d, want 3: %+v", len(rows), rows)
	}

	var relay map[string]any
	for _, r := range rows {
		if snapS(r, "provider") == "relay" && snapS(r, "mode") == "balance" {
			relay = r
		}
	}
	if relay == nil {
		t.Fatal("relay 余额行缺失（整份配额可能解析失败）")
	}
	if got := snapS(relay, "balance_text"); got != "$12.34" {
		t.Fatalf("balance_text = %q, want $12.34", got)
	}
	if got := snapS(relay, "account"); got != "acct@example.com" {
		t.Fatalf("account = %q, want acct@example.com", got)
	}
	if got, _ := snapF(relay, "amount"); got != 12.34 {
		t.Fatalf("amount = %v, want 12.34", got)
	}
	if got := moneyValue(relay); got != "$12.34" {
		t.Fatalf("moneyValue = %q, want $12.34", got)
	}

	for _, r := range rows {
		if snapS(r, "provider") == "points" && snapS(r, "mode") == "balance" {
			if got := moneyValue(r); got != "6,291 points left" {
				t.Fatalf("points moneyValue = %q", got)
			}
			if got, _ := snapF(r, "amount"); got != 6291 {
				t.Fatalf("points amount = %v, want 6291", got)
			}
		}
	}
}

func TestQuotaBalanceAcceptsNumber(t *testing.T) {
	var b quotaBalance
	if err := b.UnmarshalJSON([]byte(`12.5`)); err != nil {
		t.Fatal(err)
	}
	if b.Text != "12.5" || !b.HasAmount || b.Amount != 12.5 {
		t.Fatalf("quotaBalance = %+v", b)
	}
	if err := b.UnmarshalJSON([]byte(`null`)); err != nil {
		t.Fatal(err)
	}
	if b.Text != "" || b.HasAmount {
		t.Fatalf("null 应清空: %+v", b)
	}
}

// 回归：magpie 里新增一个余额型供应商后，下一次轮询必须把新行带进屏上快照——
// 不能因为 balance 是字符串就整份 JSON 解析失败、继续显示旧数据。
func TestPollQuotasRefreshesWhenProviderAdded(t *testing.T) {
	next := quotaFixture
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(next))
	}))
	defer gw.Close()

	s := &stats{cfg: &cfg{gatewayBase: gw.URL}, stop: make(chan struct{})}
	s.pollQuotas()
	if rows := singleRows(s.snapshot()); len(rows) != 3 {
		t.Fatalf("首次轮询 rows = %d, want 3", len(rows))
	}

	next = `{"data":[{"provider":"relay","name":"Relay","kind":"balance","balance":"$5.00","windows":[]},` +
		quotaFixture[len(`{"data":[`):]
	s.pollQuotas()
	rows := singleRows(s.snapshot())
	if len(rows) != 4 {
		t.Fatalf("新增供应商后 rows = %d, want 4（旧数据被冻结？）", len(rows))
	}
	found := false
	for _, r := range rows {
		if snapS(r, "provider") == "relay" && snapS(r, "balance_text") == "$5.00" {
			found = true
		}
	}
	if !found {
		t.Fatalf("新增 relay 余额行未出现在屏上快照: %+v", rows)
	}
}

// 供应商变更 → 立即刷新（而不是等 30s 定时拍子）。
func TestRefreshQuotasSoon(t *testing.T) {
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(quotaFixture))
	}))
	defer gw.Close()
	s := &stats{cfg: &cfg{gatewayBase: gw.URL}, stop: make(chan struct{})}
	s.refreshQuotasSoon()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(singleRows(s.snapshot())) == 3 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("refreshQuotasSoon 未在 2s 内更新 pools")
}
