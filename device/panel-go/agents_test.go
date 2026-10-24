package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func testAgentConfig() agentConfig {
	return agentConfig{
		Root:   "http://192.0.2.10:3425",
		Key:    "test-gateway-key",
		Models: []string{"model-a", "model-b"},
	}
}

// 清单是 Web 下拉与生成的共同来源：id 必须唯一，图标名必须能过 /api/icon
// 的白名单，每个渲染器都要把网关地址与密钥写进结果。
func TestAgentRegistry(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range agentSpecs {
		if a.ID == "" || a.Name == "" || a.NameEN == "" || a.Target == "" || a.TargetEN == "" {
			t.Errorf("agent %q has incomplete metadata: %+v", a.ID, a)
		}
		if seen[a.ID] {
			t.Errorf("duplicate agent id %q", a.ID)
		}
		seen[a.ID] = true
		if a.Render == nil {
			t.Errorf("agent %q has no renderer", a.ID)
		}
		if !iconNameRE.MatchString(a.Icon) {
			t.Errorf("agent %q icon %q fails iconNameRE", a.ID, a.Icon)
		}
		out := a.Render(testAgentConfig())
		if out == "" {
			t.Errorf("agent %q rendered empty config", a.ID)
		}
		if !strings.Contains(out, testAgentConfig().Key) {
			t.Errorf("agent %q config does not embed the gateway key", a.ID)
		}
		if !strings.Contains(out, testAgentConfig().Root) {
			t.Errorf("agent %q config does not contain the gateway root", a.ID)
		}
	}
}

func TestAgentByID(t *testing.T) {
	if a := agentByID("opencode"); a == nil || a.ID != "opencode" {
		t.Fatalf("agentByID(opencode) = %+v", a)
	}
	if a := agentByID("nope"); a != nil {
		t.Fatalf("agentByID(nope) = %+v, want nil", a)
	}
}

// 文件型客户端必须是可解析的 JSON；Claude 与 Zed 的尾随说明不属于文件内容。
func TestJSONAgentsParse(t *testing.T) {
	c := testAgentConfig()
	trimmed := map[string]string{
		"claude": strings.SplitN(renderClaude(c), "\n// ", 2)[0],
		"zed":    strings.SplitN(renderZed(c), "\n\n# ", 2)[0],
	}
	for _, id := range []string{"omp", "opencode", "crush", "qwen", "claude", "zed"} {
		out := trimmed[id]
		if out == "" {
			out = agentByID(id).Render(c)
		}
		var v any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("%s config is not valid JSON: %v\n%s", id, err, out)
		}
	}
}

func TestOpenCodeConfig(t *testing.T) {
	c := testAgentConfig()
	var cfg struct {
		Provider map[string]struct {
			NPM     string                    `json:"npm"`
			Options map[string]string         `json:"options"`
			Models  map[string]map[string]any `json:"models"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(renderOpenCode(c)), &cfg); err != nil {
		t.Fatal(err)
	}
	p := cfg.Provider["nokeyout"]
	if p.NPM != "@ai-sdk/openai-compatible" {
		t.Fatalf("npm = %q", p.NPM)
	}
	if p.Options["baseURL"] != "http://192.0.2.10:3425/v1" || p.Options["apiKey"] != "test-gateway-key" {
		t.Fatalf("options = %+v", p.Options)
	}
	if len(p.Models) != 2 || p.Models["model-a"] == nil {
		t.Fatalf("models = %+v", p.Models)
	}
	// 未勾选模型时不能写空 models（OpenCode 会把它当成"没有模型"）。
	var empty map[string]any
	if err := json.Unmarshal([]byte(renderOpenCode(agentConfig{Root: c.Root, Key: c.Key})), &empty); err != nil {
		t.Fatal(err)
	}
	if _, ok := empty["provider"].(map[string]any)["nokeyout"].(map[string]any)["models"]; ok {
		t.Fatal("empty model selection should omit provider.models")
	}
}

func TestCrushConfig(t *testing.T) {
	var cfg struct {
		Providers map[string]struct {
			Type    string           `json:"type"`
			BaseURL string           `json:"base_url"`
			APIKey  string           `json:"api_key"`
			Models  []map[string]any `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal([]byte(renderCrush(testAgentConfig())), &cfg); err != nil {
		t.Fatal(err)
	}
	p := cfg.Providers["nokeyout"]
	if p.Type != "openai-compat" || p.BaseURL != "http://192.0.2.10:3425/v1" || p.APIKey != "test-gateway-key" {
		t.Fatalf("provider = %+v", p)
	}
	if len(p.Models) != 2 || p.Models[0]["id"] != "model-a" {
		t.Fatalf("models = %+v", p.Models)
	}
}

func TestContinueConfig(t *testing.T) {
	out := renderContinue(testAgentConfig())
	for _, want := range []string{
		"provider: openai",
		"apiBase: 'http://192.0.2.10:3425/v1'",
		"apiKey: 'test-gateway-key'",
		"model: 'model-a'",
		"model: 'model-b'",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("continue config missing %q:\n%s", want, out)
		}
	}
}

func TestAiderConfig(t *testing.T) {
	out := renderAider(testAgentConfig())
	for _, want := range []string{
		"openai-api-base: 'http://192.0.2.10:3425/v1'",
		"openai-api-key: 'test-gateway-key'",
		"model: 'openai/model-a'",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("aider config missing %q:\n%s", want, out)
		}
	}
}

func TestQwenConfig(t *testing.T) {
	var cfg struct {
		Env            map[string]string `json:"env"`
		ModelProviders map[string][]struct {
			ID      string `json:"id"`
			EnvKey  string `json:"envKey"`
			BaseURL string `json:"baseUrl"`
		} `json:"modelProviders"`
		Security struct {
			Auth struct {
				SelectedType string `json:"selectedType"`
			} `json:"auth"`
		} `json:"security"`
		Model struct {
			Name string `json:"name"`
		} `json:"model"`
	}
	if err := json.Unmarshal([]byte(renderQwen(testAgentConfig())), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Env["NOKEYOUT_API_KEY"] != "test-gateway-key" {
		t.Fatalf("env = %+v", cfg.Env)
	}
	if cfg.Security.Auth.SelectedType != "openai" || cfg.Model.Name != "model-a" {
		t.Fatalf("security/model = %+v / %+v", cfg.Security, cfg.Model)
	}
	rows := cfg.ModelProviders["openai"]
	if len(rows) != 2 || rows[0].ID != "model-a" || rows[0].EnvKey != "NOKEYOUT_API_KEY" ||
		rows[0].BaseURL != "http://192.0.2.10:3425/v1" {
		t.Fatalf("modelProviders = %+v", rows)
	}
}

func TestZedConfig(t *testing.T) {
	out := renderZed(testAgentConfig())
	if !strings.Contains(out, "export NOKEYOUT_API_KEY=test-gateway-key") {
		t.Fatalf("zed config does not explain how to carry the key:\n%s", out)
	}
	body := strings.SplitN(out, "\n\n# ", 2)[0]
	var cfg struct {
		LanguageModels struct {
			OpenAICompatible map[string]struct {
				APIURL          string           `json:"api_url"`
				AvailableModels []map[string]any `json:"available_models"`
			} `json:"openai_compatible"`
		} `json:"language_models"`
	}
	if err := json.Unmarshal([]byte(body), &cfg); err != nil {
		t.Fatal(err)
	}
	p := cfg.LanguageModels.OpenAICompatible["nokeyout"]
	if p.APIURL != "http://192.0.2.10:3425/v1" || len(p.AvailableModels) != 2 {
		t.Fatalf("zed provider = %+v", p)
	}
}

func TestUIAgentsConfig(t *testing.T) {
	for _, id := range []string{"cline", "roo", "kilo"} {
		out := agentByID(id).Render(testAgentConfig())
		for _, want := range []string{
			"API Provider: OpenAI Compatible",
			"Base URL: http://192.0.2.10:3425/v1",
			"API Key:  test-gateway-key",
			"Model ID: model-a",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("%s config missing %q:\n%s", id, want, out)
			}
		}
	}
}

func TestYAMLQuote(t *testing.T) {
	if got := yamlQuote("a'b"); got != "'a''b'" {
		t.Fatalf("yamlQuote = %q", got)
	}
}
