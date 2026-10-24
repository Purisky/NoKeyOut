package main

import (
	"encoding/json"
	"testing"
)

// 面板只从 magpie 的 caller-keys.json 读网关密钥；服务密钥不暴露给配置生成，
// 空名字/空密钥的行必须跳过，掩码只保留末 6 位。
func TestParseMagpieKeys(t *testing.T) {
	raw := []byte(`[
		{"id":"1","name":"nokeyout-panel","secret":"panel-gateway-secret"},
		{"id":"2","name":"devbox","secret":"devbox-gateway-secret-123456"},
		{"id":"3","name":"","secret":"empty-name-secret"},
		{"id":"4","name":"empty","secret":""}
	]`)
	keys, err := parseMagpieKeys(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].Name != "devbox" || keys[0].ID != "2" {
		t.Fatalf("parseMagpieKeys = %+v", keys)
	}
	if got := maskSecret(keys[0].Secret); got != "123456" {
		t.Fatalf("maskSecret = %q, want 123456", got)
	}
	if _, err := parseMagpieKeys([]byte(`not json`)); err == nil {
		t.Fatal("malformed JSON should error")
	}
}

// 面板生成的 omp 配置会被原样保存成 nokeyout-provider.json；插件用
// JSON.parse 读取，任何尾随注释都会让它回退到默认值并丢掉密钥。
func TestOMPProviderConfigIsPureJSON(t *testing.T) {
	out := ompProviderConfig("http://192.0.2.10:3425", "test-gateway-key")
	var cfg struct {
		BaseURL    string `json:"baseUrl"`
		GatewayKey string `json:"gatewayKey"`
		ProviderID string `json:"providerId"`
	}
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatalf("omp provider config is not pure JSON: %v\n%s", err, out)
	}
	if cfg.BaseURL != "http://192.0.2.10:3425/v1" {
		t.Fatalf("baseUrl = %q", cfg.BaseURL)
	}
	if cfg.GatewayKey != "test-gateway-key" {
		t.Fatalf("gatewayKey = %q", cfg.GatewayKey)
	}
	if cfg.ProviderID != "nokeyout" {
		t.Fatalf("providerId = %q", cfg.ProviderID)
	}
}
