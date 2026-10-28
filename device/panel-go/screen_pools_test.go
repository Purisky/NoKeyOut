package main

// screen_pools_test.go —— 配额端到端回归：真实 pollQuotas → snapshot → 渲染。
//
//   used/remaining 是**百分比**（相加=100），没有 total/unit；resetsAt 只有部分窗口有。

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
