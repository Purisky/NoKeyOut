package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClassifyCallError(t *testing.T) {
	cases := []struct {
		name string
		row  usageLogRow
		want string
	}{
		{"配额", usageLogRow{Status: 402, Err: "Open Design: [code=insufficient_balance] Coding Plan quota is exhausted and wallet balance is insufficient."}, "quota"},
		{"配额 err_type", usageLogRow{Status: 403, ErrType: "insufficient_quota"}, "quota"},
		{"限流", usageLogRow{Status: 429, Err: "rate limit exceeded, too many requests"}, "limit"},
		{"订阅到期", usageLogRow{Status: 402, Err: "subscription expired; please renew"}, "subscription"},
		{"认证", usageLogRow{Status: 401, Err: "DeepSeek: Authentication Fails, Your api key is invalid"}, "auth"},
		{"配置", usageLogRow{Status: 404, Err: "provider switched off"}, "config"},
		{"模型", usageLogRow{Status: 404, Err: "unknown model"}, "model"},
		{"上游", usageLogRow{Status: 502, Err: "bad gateway"}, "upstream"},
		{"网络", usageLogRow{Status: 0, Err: "dial tcp: connect: connection refused"}, "network"},
		{"请求", usageLogRow{Status: 400, Err: "context length exceeds limit"}, "request"},
		{"取消", usageLogRow{Status: 499, Err: "the agent canceled the request"}, "canceled"},
		{"其他", usageLogRow{Status: 418, Err: "teapot"}, "other"},
	}
	for _, c := range cases {
		if got := classifyCallError(c.row); got != c.want {
			t.Errorf("%s: classify = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestIsCallErrorExcludesProbes(t *testing.T) {
	if isCallError(usageLogRow{Kind: "model_list", Err: "proxyconnect"}) {
		t.Fatal("model_list 探针不应算调用错误")
	}
	if isCallError(usageLogRow{Source: "probe", Err: "proxyconnect"}) {
		t.Fatal("probe 不应算调用错误")
	}
	if isCallError(usageLogRow{EP: "GET /v1/models", Err: "proxyconnect"}) {
		t.Fatal("GET 探针不应算调用错误")
	}
	if !isCallError(usageLogRow{EP: "/v1/chat/completions", Status: 401, Err: "invalid key"}) {
		t.Fatal("真实调用 401 应算错误")
	}
	if isCallError(usageLogRow{EP: "/v1/chat/completions", Status: 200}) {
		t.Fatal("成功调用不应算错误")
	}
}

func TestErrorRingOrderAndFilter(t *testing.T) {
	var r errorRing
	base := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	r.add(usageLogRow{EP: "GET /v1/models", Err: "probe"}, base)
	r.add(usageLogRow{EP: "/v1/chat/completions", Status: 401, Err: "invalid key"}, base.Add(time.Second))
	r.add(usageLogRow{EP: "/v1/chat/completions", Status: 402, Err: "insufficient_balance"}, base.Add(2*time.Second))
	r.add(usageLogRow{EP: "/v1/chat/completions", Status: 499, Err: "canceled"}, base.Add(3*time.Second))
	got := r.list(10)
	if len(got) != 3 {
		t.Fatalf("应只有 3 条调用错误，got %d", len(got))
	}
	if got[0].Category != "canceled" || got[1].Category != "quota" || got[2].Category != "auth" {
		t.Fatalf("顺序/分类错误：%+v", got)
	}
	if got[1].Message == "" || got[1].Time == "" {
		t.Fatalf("消息/时间不应为空：%+v", got[1])
	}
}

func TestErrorRingMax(t *testing.T) {
	var r errorRing
	base := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 505; i++ {
		r.add(usageLogRow{EP: "/v1/chat/completions", Status: 500, Err: fmt.Sprintf("e%d", i)}, base.Add(time.Duration(i)*time.Second))
	}
	got := r.list(1000)
	if len(got) != 500 {
		t.Fatalf("环形缓冲应保留 500 条，got %d", len(got))
	}
	if got[0].Message != "e504" || got[499].Message != "e5" {
		t.Fatalf("环形缓冲边界错误：first=%q last=%q", got[0].Message, got[len(got)-1].Message)
	}
}

func TestUsageHistCollectsErrors(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "usage.jsonl")
	at := time.Now().Add(-time.Hour).Format(time.RFC3339)
	lines := []string{
		fmt.Sprintf(`{"t":%q,"provider":"deepseek","model":"deepseek-flash","in":10,"out":5,"ep":"/v1/chat/completions","status":200}`, at),
		fmt.Sprintf(`{"t":%q,"provider":"deepseek","model":"deepseek-flash","in":0,"out":0,"ep":"/v1/chat/completions","status":402,"err":"insufficient_balance"}`, at),
		fmt.Sprintf(`{"t":%q,"agent":"magpie","provider":"x","in":0,"out":0,"ep":"GET /v1/models","kind":"model_list","source":"probe","err":"proxyconnect"}`, at),
	}
	if err := os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &usageHist{path: filepath.Join(dir, "hist.bin"), slots: map[int64]histSlot{}}
	h.initPeak(logPath)
	got := h.errs.list(10)
	if len(got) != 1 || got[0].Category != "quota" {
		t.Fatalf("应收集 1 条配额错误，got %+v", got)
	}
	// 增量 tail 也要收错误。
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(f, `{"t":%q,"provider":"deepseek","model":"deepseek-flash","in":0,"out":0,"ep":"/v1/chat/completions","status":401,"err":"invalid key"}`+"\n", at)
	_ = f.Close()
	h.syncPeak()
	got = h.errs.list(10)
	if len(got) != 2 || got[0].Category != "auth" || got[1].Category != "quota" {
		t.Fatalf("增量错误错误：%+v", got)
	}
}
