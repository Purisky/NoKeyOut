//go:build !(js && wasm)

package main

// errors.go —— 从 magpie 逐请求流水收集调用错误，按类型分类，供管理面浏览/过滤。
//
// 只收用户调用错误：magpie 自己的 model_list 探针（kind/source/ep=GET）不收。
// 配额不足/限流/订阅到期/取消这类噪音由前端默认隐藏，用户可随时取消过滤。

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type callError struct {
	Time     string `json:"time"`
	Category string `json:"category"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Caller   string `json:"caller,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Message  string `json:"message"`
	Status   int    `json:"status"`
	Rejected bool   `json:"rejected,omitempty"`
}

type errorRing struct {
	mu    sync.Mutex
	items []callError
	max   int
}

func (r *errorRing) reset() {
	r.mu.Lock()
	r.items = nil
	r.mu.Unlock()
}

func (r *errorRing) add(row usageLogRow, ts time.Time) {
	if !isCallError(row) {
		return
	}
	msg := strings.TrimSpace(row.Err)
	if msg == "" {
		if row.Status > 0 {
			msg = fmt.Sprintf("HTTP %d", row.Status)
		} else if row.Rejected {
			msg = "rejected"
		} else {
			msg = "error"
		}
	}
	model := row.Model
	if model == "" {
		model = row.Served
	}
	if model == "" {
		model = row.Req
	}
	e := callError{
		Time:     ts.Format(time.RFC3339),
		Category: classifyCallError(row),
		Provider: row.Provider,
		Model:    model,
		Agent:    row.Agent,
		Caller:   row.Caller,
		Endpoint: row.EP,
		Message:  msg,
		Status:   row.Status,
		Rejected: row.Rejected,
	}
	r.mu.Lock()
	if r.max <= 0 {
		r.max = 500
	}
	r.items = append(r.items, e)
	if len(r.items) > r.max {
		r.items = append([]callError(nil), r.items[len(r.items)-r.max:]...)
	}
	r.mu.Unlock()
}

func (r *errorRing) list(limit int) []callError {
	if limit <= 0 {
		limit = 200
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.items)
	if n > limit {
		n = limit
	}
	out := make([]callError, 0, n)
	for i := len(r.items) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, r.items[i])
	}
	return out
}

// isCallError：真正的用户调用错误；排除 magpie 自身的模型列表探针。
func isCallError(row usageLogRow) bool {
	if row.Source == "probe" || row.Kind == "model_list" {
		return false
	}
	if strings.HasPrefix(row.EP, "GET ") {
		return false
	}
	return row.Err != "" || row.Status >= 400 || row.Rejected
}

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// classifyCallError：错误类型。顺序有意为之：先认取消/配额/订阅/限流，
// 再看认证、配置、模型、上游、网络、请求参数。
func classifyCallError(row usageLogRow) string {
	msg := strings.ToLower(row.Err + " " + row.ErrType)
	switch {
	case row.Status == 499 || containsAny(msg, "canceled", "cancelled", "canceled the request"):
		return "canceled"
	case containsAny(msg, "subscription", "plan expired", "plan has expired", "membership",
		"trial expired", "renew", "not subscribed"):
		return "subscription"
	case row.Status == 402 || containsAny(msg, "insufficient_balance", "insufficient balance",
		"insufficient_quota", "insufficient quota", "quota is exhausted", "quota exceeded",
		"out of credits", "balance is insufficient", "payment required", "billing", "credit"):
		return "quota"
	case row.Status == 429 || containsAny(msg, "rate limit", "too many requests",
		"concurrency", "rpm", "tpm", "限流", "频率"):
		return "limit"
	case row.Status == 401 || row.Status == 403 || containsAny(msg, "authentication",
		"unauthorized", "forbidden", "invalid api key", "invalid key", "permission", "not allowed"):
		return "auth"
	case containsAny(msg, "provider switched off", "not configured", "disabled"):
		return "config"
	case containsAny(msg, "unknown model", "model not found", "unsupported model",
		"no available channel", "model does not exist"):
		return "model"
	case row.Status >= 500 || containsAny(msg, "bad gateway", "overloaded", "upstream",
		"internal server", "service unavailable"):
		return "upstream"
	case containsAny(msg, "timeout", "timed out", "dial", "connection", "refused", "eof",
		"proxy", "network", "dns"):
		return "network"
	case row.Status == 400 || containsAny(msg, "context length", "invalid", "bad request"):
		return "request"
	default:
		return "other"
	}
}

func (s *stats) recentErrors(limit int) []callError {
	if s == nil || s.hist == nil {
		return []callError{}
	}
	return s.hist.errs.list(limit)
}

func (p *panel) errorsHandler(w http.ResponseWriter, r *http.Request) {
	if p.user(r) == "" {
		p.errJSON(w, r, 401, "no session")
		return
	}
	if r.Method != http.MethodGet {
		p.errJSON(w, r, 405, "只支持 GET")
		return
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	p.json(w, r, 200, map[string]any{
		"errors":       p.stats.recentErrors(limit),
		"generated_at": time.Now().Unix(),
	}, nil)
}
