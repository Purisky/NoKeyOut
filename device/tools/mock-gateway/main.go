// mock-gateway —— 假 magpie 网关（PC 上调屏/联调用，Go 单文件、无依赖）
//
// 让 Go panel 在开发机上完整跑起来（GATEWAY_BASE 指到这里），从而无硬件验证
// 屏渲染线程（SCREEN_BACKEND=png|console）与统计链路：
//   go run ./device/tools/mock-gateway --port 3425 --scenario happy
//   SCREEN_BACKEND=png SCREEN_PNG_DIR=/tmp/frames \
//     GATEWAY_BASE=http://127.0.0.1:3425 UPSTREAM_WEB=http://127.0.0.1:3425 \
//     PANEL_ALLOW_INSECURE_HTTP=1 PANEL_DEV_AUTH=1 ./device/panel-go/panel
//
// 契约（panel stats 轮询用）：/v1/magpie/usage?period=today、/v1/magpie/quotas、

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

type state struct {
	mu       sync.Mutex
	scenario string
	t0       time.Time
}

var st = &state{scenario: "happy", t0: time.Now()}

func (s *state) usage(period string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	dt := time.Since(s.t0).Seconds()
	if s.scenario == "empty" || s.scenario == "zero" {
		return map[string]any{"input": 0, "output": 0, "calls": 0, "errors": 0,
			"cache_read": 0, "period": period, "bucket": bucketOf(period), "series": series(period)}
	}
	f := func(a, b float64) float64 { return a + b*dt }
	return map[string]any{
		"input": int(f(120000, 90)), "output": int(f(80000, 60)),
		"calls": int(f(500, 1.7)), "errors": int(math.Max(0, f(-10, 0.01))),
		"cache_read": int(f(30000, 40)), "period": period,
		"bucket": bucketOf(period), "series": series(period),
	}
}

// bucketOf / series：与真实 magpie 同一契约（today 按小时，7d/30d 按天），
// 桶里带 input/output/cache_read，面板自己算 tokens = input+output。
func bucketOf(period string) string {
	if period == "today" {
		return "hour"
	}
	return "day"
}

func series(period string) []any {
	n := 24
	if period == "7d" {
		n = 7
	} else if period == "30d" {
		n = 30
	}
	out := make([]any, 0, n)
	for i := 0; i < n; i++ {
		// 确定性的起伏（不用随机数：便于对照）
		in := 4000 + (i*7919)%90000
		out = append(out, map[string]any{
			"label": fmt.Sprintf("%02d", i), "input": in, "output": in / 3,
			"cache_read": in * 40, "calls": 1 + (i % 9),
		})
	}
	return out
}

func (s *state) quotas() map[string]any {
	dt := time.Since(s.t0).Seconds()
	//   · 订阅型（kind=plan）：windows[{name,used,remaining,resetsAt?}]，used/remaining 是 0-100 百分比
	//   · key 型：**没有 windows**，只有 balance / balances[] + currency
	// 这里**不编造任何窗口**：某个供应商没有哪档额度，就不要写进夹具——
	// 否则会被误读成"面板能算出来"。
	now := time.Now().UTC()
	pools := []any{
		map[string]any{"provider": "opencode-go", "name": "OpenCode Go", "kind": "plan", "windows": []any{
			map[string]any{"name": "5 hours", "used": int(math.Mod(dt*3, 100)), "remaining": 100 - int(math.Mod(dt*3, 100))},
			map[string]any{"name": "7 days", "used": 22, "remaining": 78,
				"resetsAt": now.Add(72 * time.Hour).Format(time.RFC3339)},
			map[string]any{"name": "Month", "used": 100, "remaining": 0,
				"resetsAt": now.Add(6 * 24 * time.Hour).Format(time.RFC3339)},
		}},
		// key 型：账上余额（USD）
		map[string]any{"provider": "openrouter", "name": "OpenRouter", "kind": "key",
			"balance": 12.34, "currency": "USD"},
		// 同一供应商的**两个账号**：窗口名完全相同，只能靠 accountNames 区分
		map[string]any{"provider": "codex", "name": "Codex", "kind": "plan",
			"accountNames": []any{"work@example.com"}, "windows": []any{
				map[string]any{"name": "5 hours", "used": 40, "remaining": 60,
					"resetsAt": now.Add(3 * time.Hour).Format(time.RFC3339)},
				map[string]any{"name": "Week", "used": 70, "remaining": 30,
					"resetsAt": now.Add(4 * 24 * time.Hour).Format(time.RFC3339)},
			}},
		map[string]any{"provider": "codex", "name": "Codex", "kind": "plan",
			"accountNames": []any{"personal@example.com"}, "windows": []any{
				map[string]any{"name": "5 hours", "used": 5, "remaining": 95,
					"resetsAt": now.Add(2 * time.Hour).Format(time.RFC3339)},
				map[string]any{"name": "Week", "used": 12, "remaining": 88,
					"resetsAt": now.Add(5 * 24 * time.Hour).Format(time.RFC3339)},
			}},
		// key 型：账上余额（CNY），且**没有任何按月额度**
		map[string]any{"provider": "deepseek", "name": "DeepSeek", "kind": "key",
			"balance": 8.62, "currency": "CNY"},
	}
	if s.scenario == "empty" {
		pools = []any{}
	}
	return map[string]any{"data": pools, "object": "list"}
}

func (s *state) models() map[string]any {
	list := []any{
		map[string]any{"id": "gpt-4o", "owned_by": "openai", "display_name": "GPT-4o", "context_window": 128000},
		map[string]any{"id": "claude-sonnet-4", "owned_by": "anthropic", "display_name": "Claude Sonnet 4", "context_window": 200000},
		map[string]any{"id": "deepseek-chat", "owned_by": "deepseek", "display_name": "DeepSeek Chat", "context_window": 64000},
	}
	return map[string]any{"data": list}
}

func jwrite(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	port := flag.Int("port", 3425, "监听端口")
	sc := flag.String("scenario", "happy", "happy|empty|zero")
	flag.Parse()
	if *sc != "happy" && *sc != "empty" && *sc != "zero" {
		fmt.Fprintln(os.Stderr, "scenario 只支持 happy|empty|zero")
		os.Exit(2)
	}
	st.scenario = *sc

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/magpie/usage", func(w http.ResponseWriter, r *http.Request) {
		jwrite(w, 200, st.usage(r.URL.Query().Get("period")))
	})
	mux.HandleFunc("/v1/magpie/quotas", func(w http.ResponseWriter, r *http.Request) {
		jwrite(w, 200, st.quotas())
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		jwrite(w, 200, st.models())
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		jwrite(w, 200, map[string]any{"ok": true})
	})
	addr := "127.0.0.1:" + strconv.Itoa(*port)
	log.Printf("mock-gateway on http://%s scenario=%s", addr, *sc)
	log.Fatal(http.ListenAndServe(addr, mux))
}
