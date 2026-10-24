//go:build !(js && wasm)

package main

// lang.go —— 界面语言（屏幕与 Web 同源）：zh（默认）/ en
// 契约与 Python server.py 对齐：PANEL_LANG 环境量 + /var/lib/nokeyout/lang 持久化选择。

import (
	"os"
	"path/filepath"
)

var langFile = "/var/lib/nokeyout/lang"

var langList = [2]string{"zh", "en"}

func init() {
	if v := os.Getenv("NOKEYOUT_LANG_FILE"); v != "" {
		langFile = v
	}
}

func langGet() string {
	if b, err := os.ReadFile(langFile); err == nil {
		for _, l := range langList {
			if len(b) >= len(l) && string(b[:len(l)]) == l {
				return l
			}
		}
	}
	if v := os.Getenv("PANEL_LANG"); langOK(v) {
		return v
	}
	return "zh"
}

func langSet(v string) bool {
	if !langOK(v) {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(langFile), 0o755); err != nil {
		return false
	}
	tmp := langFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(v+"\n"), 0o644); err != nil {
		return false
	}
	return os.Rename(tmp, langFile) == nil
}

func langOK(v string) bool {
	return v == "zh" || v == "en"
}
