//go:build !(js && wasm)

package main

// agents.go —— 客户端配置生成的单一清单。
//
// 每个客户端只声明元数据和一个纯渲染函数；Web 下拉（/api/agents）与
// 配置生成（/api/gen-config）都从这里取，新增客户端不必改前端。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// agentConfig 是渲染器需要的全部输入：网关根地址（不含 /v1）、真实密钥、
// 用户在模型页勾选的模型 id。
type agentConfig struct {
	Root   string
	Key    string
	Models []string
}

func (c agentConfig) baseURL() string { return c.Root + "/v1" }

func (c agentConfig) model() string {
	if len(c.Models) > 0 {
		return c.Models[0]
	}
	return "<model-id>"
}

type agentSpec struct {
	ID       string
	Name     string // 中文名
	NameEN   string
	Icon     string // magpie 图标名；缺失时面板回退通用图标
	Target   string // 中文目标文件/设置位置
	TargetEN string
	Kind     string // file | env | ui
	Protocol string // openai | anthropic | both
	Note     string
	NoteEN   string
	Render   func(agentConfig) string
}

var agentSpecs = []agentSpec{
	{
		ID: "omp", Name: "omp", NameEN: "omp", Icon: "omp",
		Target:   "~/.omp/agent/nokeyout-provider.json",
		TargetEN: "~/.omp/agent/nokeyout-provider.json",
		Kind:     "file", Protocol: "openai",
		Note:   "安装 omp-nokeyout-provider 插件后原样保存此文件；改完重启会话。",
		NoteEN: "Install the omp-nokeyout-provider plugin, save this file verbatim, then restart the session.",
		Render: renderOMP,
	},
	{
		ID: "codex", Name: "Codex", NameEN: "Codex", Icon: "codex-color",
		Target:   "~/.codex/config.toml",
		TargetEN: "~/.codex/config.toml",
		Kind:     "file", Protocol: "openai",
		Note:   "写入 config.toml，并在环境中导出 NOKEYOUT_API_KEY；勾选的模型只用于静态清单。",
		NoteEN: "Write config.toml and export NOKEYOUT_API_KEY; checked models only feed the static list.",
		Render: renderCodex,
	},
	{
		ID: "claude", Name: "Claude Code", NameEN: "Claude Code", Icon: "claudecode-color",
		Target:   "~/.claude/settings.json",
		TargetEN: "~/.claude/settings.json",
		Kind:     "file", Protocol: "anthropic",
		Note:   "把 env 块合并进 settings.json；ANTHROPIC_MODEL 取第一个勾选模型。",
		NoteEN: "Merge the env block into settings.json; ANTHROPIC_MODEL is the first checked model.",
		Render: renderClaude,
	},
	{
		ID: "env", Name: "环境变量", NameEN: "Env vars", Icon: "generic",
		Target:   "shell / CI 环境",
		TargetEN: "shell / CI environment",
		Kind:     "env", Protocol: "both",
		Note:   "同时给出 OpenAI 与 Anthropic 两种协议的变量；按需导出。",
		NoteEN: "Both OpenAI and Anthropic variable sets; export the one you need.",
		Render: renderEnv,
	},
	{
		ID: "opencode", Name: "OpenCode", NameEN: "OpenCode", Icon: "opencode",
		Target:   "~/.config/opencode/opencode.json",
		TargetEN: "~/.config/opencode/opencode.json",
		Kind:     "file", Protocol: "openai",
		Note:   "保存为全局 opencode.json；勾选的模型写入 provider.models，未勾选时需自行补。",
		NoteEN: "Save as the global opencode.json; checked models become provider.models entries—add them yourself if none are checked.",
		Render: renderOpenCode,
	},
	{
		ID: "crush", Name: "Crush", NameEN: "Crush", Icon: "crush",
		Target:   "~/.config/crush/crush.json",
		TargetEN: "~/.config/crush/crush.json",
		Kind:     "file", Protocol: "openai",
		Note:   "保存到 crush.json；勾选的模型写入 providers.nokeyout.models，未勾选时需自行补。",
		NoteEN: "Save as crush.json; checked models become providers.nokeyout.models entries—add them yourself if none are checked.",
		Render: renderCrush,
	},
	{
		ID: "continue", Name: "Continue", NameEN: "Continue", Icon: "generic",
		Target:   "~/.continue/config.yaml",
		TargetEN: "~/.continue/config.yaml",
		Kind:     "file", Protocol: "openai",
		Note:   "保存到 config.yaml；每个勾选模型生成一条 models 记录。",
		NoteEN: "Save as config.yaml; each checked model becomes a models entry.",
		Render: renderContinue,
	},
	{
		ID: "aider", Name: "Aider", NameEN: "Aider", Icon: "generic",
		Target:   "~/.aider.conf.yml",
		TargetEN: "~/.aider.conf.yml",
		Kind:     "file", Protocol: "openai",
		Note:   "保存到 ~/.aider.conf.yml；Aider 的模型名必须带 openai/ 前缀。",
		NoteEN: "Save as ~/.aider.conf.yml; Aider requires the openai/ model prefix.",
		Render: renderAider,
	},
	{
		ID: "qwen", Name: "Qwen Code", NameEN: "Qwen Code", Icon: "qwen-color",
		Target:   "~/.qwen/settings.json",
		TargetEN: "~/.qwen/settings.json",
		Kind:     "file", Protocol: "openai",
		Note:   "保存到 settings.json；密钥放在 env 块，selectedType=openai。",
		NoteEN: "Save as settings.json; the key lives in the env block with selectedType=openai.",
		Render: renderQwen,
	},
	{
		ID: "zed", Name: "Zed", NameEN: "Zed", Icon: "zed",
		Target:   "~/.config/zed/settings.json",
		TargetEN: "~/.config/zed/settings.json",
		Kind:     "file", Protocol: "openai",
		Note:   "合并 language_models 块；Zed 不读文件里的密钥——按末尾提示导出 NOKEYOUT_API_KEY 后在 LLM Providers 选择；max_tokens 按实际上下文调整。",
		NoteEN: "Merge the language_models block; Zed does not read keys from settings.json—export NOKEYOUT_API_KEY as shown, then pick the provider in LLM Providers; adjust max_tokens to the real context window.",
		Render: renderZed,
	},
	{
		ID: "cline", Name: "Cline", NameEN: "Cline", Icon: "cline",
		Target:   "Cline 扩展设置",
		TargetEN: "Cline extension settings",
		Kind:     "ui", Protocol: "openai",
		Note:   "打开 Cline 设置 → API Provider 选 OpenAI Compatible，填入下面四项。",
		NoteEN: "Open Cline settings → API Provider: OpenAI Compatible, then fill in the four fields below.",
		Render: renderUI("Cline"),
	},
	{
		ID: "roo", Name: "Roo Code", NameEN: "Roo Code", Icon: "roocode",
		Target:   "Roo Code 扩展设置",
		TargetEN: "Roo Code extension settings",
		Kind:     "ui", Protocol: "openai",
		Note:   "打开 Roo Code 设置 → API Provider 选 OpenAI Compatible，填入下面四项。",
		NoteEN: "Open Roo Code settings → API Provider: OpenAI Compatible, then fill in the four fields below.",
		Render: renderUI("Roo Code"),
	},
	{
		ID: "kilo", Name: "Kilo Code", NameEN: "Kilo Code", Icon: "kilocode",
		Target:   "Kilo Code 扩展设置",
		TargetEN: "Kilo Code extension settings",
		Kind:     "ui", Protocol: "openai",
		Note:   "Kilo Code 设置 → API Provider 选 OpenAI Compatible；Base URL 有效时会自动拉取模型列表。",
		NoteEN: "Kilo Code settings → API Provider: OpenAI Compatible; a valid Base URL auto-fetches the model list.",
		Render: renderUI("Kilo Code"),
	},
}

func agentByID(id string) *agentSpec {
	for i := range agentSpecs {
		if agentSpecs[i].ID == id {
			return &agentSpecs[i]
		}
	}
	return nil
}

func (p *panel) apiAgents(w http.ResponseWriter, r *http.Request) {
	if p.user(r) == "" {
		p.errJSON(w, r, 401, "no session")
		return
	}
	out := make([]map[string]any, 0, len(agentSpecs))
	for _, a := range agentSpecs {
		out = append(out, map[string]any{
			"id": a.ID, "name": a.Name, "nameEn": a.NameEN, "icon": a.Icon,
			"target": a.Target, "targetEn": a.TargetEN, "kind": a.Kind,
			"protocol": a.Protocol, "note": a.Note, "noteEn": a.NoteEN,
		})
	}
	p.json(w, r, 200, map[string]any{"agents": out},
		[][2]string{{"Cache-Control", "no-store"}})
}

// ---------------------------------------------------------------- 渲染器

// ompProviderConfig returns exactly what the omp provider reads from
// ~/.omp/agent/nokeyout-provider.json. It must stay pure JSON: the plugin
// parses this file with JSON.parse, so a trailing // comment would make it
// fall back to defaults and silently drop the gateway key.
func ompProviderConfig(root, key string) string {
	return renderOMP(agentConfig{Root: root, Key: key})
}

func renderOMP(c agentConfig) string {
	j, _ := json.MarshalIndent(map[string]any{
		"baseUrl": c.baseURL(), "gatewayKey": c.Key, "providerId": "nokeyout",
	}, "", "  ")
	return string(j)
}

func renderCodex(c agentConfig) string {
	var list strings.Builder
	for _, m := range c.Models {
		list.WriteString("\n#   " + m)
	}
	return fmt.Sprintf("# ~/.codex/config.toml\n[model_providers.nokeyout]\nname = \"nokeyout\"\n"+
		"base_url = \"%s\"\nenv_key = \"NOKEYOUT_API_KEY\"\nwire_api = \"chat\"\n\n"+
		"# 环境变量（真实密钥）：\n# NOKEYOUT_API_KEY=%s\n\nmodel = \"%s\"\n\n"+
		"# 勾选模型（%d）：%s", c.baseURL(), c.Key, c.model(), len(c.Models), list.String())
}

func renderClaude(c agentConfig) string {
	j, _ := json.MarshalIndent(map[string]any{"env": map[string]any{
		"ANTHROPIC_BASE_URL": c.Root, "ANTHROPIC_AUTH_TOKEN": c.Key,
		"ANTHROPIC_MODEL": c.model(), "ANTHROPIC_SMALL_FAST_MODEL": c.model(),
	}}, "", "  ")
	return string(j) + "\n// ~/.claude/settings.json 的 env 块"
}

func renderEnv(c agentConfig) string {
	var list strings.Builder
	for _, m := range c.Models {
		list.WriteString("\n#   " + m)
	}
	return fmt.Sprintf("# 通用 OpenAI 兼容客户端\nOPENAI_BASE_URL=%s\nOPENAI_API_KEY=%s\n\n"+
		"# Anthropic 协议客户端（Claude Code 等）\nANTHROPIC_BASE_URL=%s\nANTHROPIC_AUTH_TOKEN=%s\n\n"+
		"# 勾选模型（%d）：%s", c.baseURL(), c.Key, c.Root, c.Key, len(c.Models), list.String())
}

func renderOpenCode(c agentConfig) string {
	prov := map[string]any{
		"npm":  "@ai-sdk/openai-compatible",
		"name": "NoKeyOut",
		"options": map[string]any{
			"baseURL": c.baseURL(),
			"apiKey":  c.Key,
		},
	}
	if len(c.Models) > 0 {
		models := make(map[string]any, len(c.Models))
		for _, m := range c.Models {
			models[m] = map[string]any{"name": m}
		}
		prov["models"] = models
	}
	j, _ := json.MarshalIndent(map[string]any{
		"$schema":  "https://opencode.ai/config.json",
		"provider": map[string]any{"nokeyout": prov},
	}, "", "  ")
	return string(j)
}

func renderCrush(c agentConfig) string {
	prov := map[string]any{
		"type":     "openai-compat",
		"base_url": c.baseURL(),
		"api_key":  c.Key,
	}
	if len(c.Models) > 0 {
		models := make([]map[string]any, 0, len(c.Models))
		for _, m := range c.Models {
			models = append(models, map[string]any{"id": m, "name": m})
		}
		prov["models"] = models
	}
	j, _ := json.MarshalIndent(map[string]any{
		"$schema":   "https://charm.land/crush.json",
		"providers": map[string]any{"nokeyout": prov},
	}, "", "  ")
	return string(j)
}

func renderContinue(c agentConfig) string {
	models := c.Models
	if len(models) == 0 {
		models = []string{c.model()}
	}
	var b strings.Builder
	b.WriteString("name: NoKeyOut\nversion: 1.0.0\nschema: v1\nmodels:\n")
	for _, m := range models {
		b.WriteString("  - name: " + yamlQuote(m) + "\n")
		b.WriteString("    provider: openai\n")
		b.WriteString("    model: " + yamlQuote(m) + "\n")
		b.WriteString("    apiBase: " + yamlQuote(c.baseURL()) + "\n")
		b.WriteString("    apiKey: " + yamlQuote(c.Key) + "\n")
	}
	return b.String()
}

func renderAider(c agentConfig) string {
	return "# ~/.aider.conf.yml\n" +
		"openai-api-base: " + yamlQuote(c.baseURL()) + "\n" +
		"openai-api-key: " + yamlQuote(c.Key) + "\n" +
		"model: " + yamlQuote("openai/"+c.model()) + "\n"
}

func renderQwen(c agentConfig) string {
	models := c.Models
	if len(models) == 0 {
		models = []string{c.model()}
	}
	entries := make([]map[string]any, 0, len(models))
	for _, m := range models {
		entries = append(entries, map[string]any{
			"id": m, "name": m, "envKey": "NOKEYOUT_API_KEY", "baseUrl": c.baseURL(),
		})
	}
	j, _ := json.MarshalIndent(map[string]any{
		"env":            map[string]any{"NOKEYOUT_API_KEY": c.Key},
		"modelProviders": map[string]any{"openai": entries},
		"security":       map[string]any{"auth": map[string]any{"selectedType": "openai"}},
		"model":          map[string]any{"name": c.model()},
	}, "", "  ")
	return string(j)
}

func renderZed(c agentConfig) string {
	models := c.Models
	if len(models) == 0 {
		models = []string{c.model()}
	}
	available := make([]map[string]any, 0, len(models))
	for _, m := range models {
		// max_tokens 是上下文窗口；占位值，按真实模型调整。
		available = append(available, map[string]any{
			"name": m, "display_name": m, "max_tokens": 128000,
		})
	}
	j, _ := json.MarshalIndent(map[string]any{
		"language_models": map[string]any{
			"openai_compatible": map[string]any{
				"nokeyout": map[string]any{
					"api_url":          c.baseURL(),
					"available_models": available,
				},
			},
		},
	}, "", "  ")
	return string(j) + "\n\n# 密钥不写入 settings.json；启动 Zed 前导出：\nexport NOKEYOUT_API_KEY=" + c.Key
}

func renderUI(name string) func(agentConfig) string {
	return func(c agentConfig) string {
		var list strings.Builder
		for _, m := range c.Models {
			list.WriteString("\n#   " + m)
		}
		return fmt.Sprintf("# %s → API Provider: OpenAI Compatible\n"+
			"Base URL: %s\nAPI Key:  %s\nModel ID: %s\n\n"+
			"# 网关模型（勾选后列出；也可在扩展里手动填写）：%s",
			name, c.baseURL(), c.Key, c.model(), list.String())
	}
}

// yamlQuote 用单引号包裹 YAML 标量；单引号本身加倍转义。
func yamlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
