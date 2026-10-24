//go:build !(js && wasm)

package main

// keyring.go —— magpie 网关密钥（只读下拉）+ 配置生成 + 系统页签 + 首启向导

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func hmacEq(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// ---------------------------------------------------------------- magpie gateway keys

// magpieKey is the on-disk shape of magpie's caller-keys.json, exported by
// the root-owned CLI wrapper. The panel never stores gateway keys itself:
// magpie is the single source of truth.
type magpieKey struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Secret string `json:"secret"`
}

func parseMagpieKeys(raw []byte) ([]magpieKey, error) {
	var rows []magpieKey
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	out := rows[:0]
	for _, k := range rows {
		if k.Name == "" || k.Secret == "" {
			continue
		}
		// 面板自己的服务密钥不暴露给配置生成。
		if k.Name == "nokeyout-panel" {
			continue
		}
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (p *panel) magpieKeys() ([]magpieKey, error) {
	rc, out, _ := runCLI([]string{"gateway-key", "export"}, 10)
	if rc != 0 {
		return nil, fmt.Errorf("读取 magpie 网关密钥失败 (rc=%d)", rc)
	}
	keys, err := parseMagpieKeys([]byte(out))
	if err != nil {
		return nil, fmt.Errorf("解析 magpie 网关密钥失败: %w", err)
	}
	return keys, nil
}

func (p *panel) magpieKey(name string) (*magpieKey, error) {
	keys, err := p.magpieKeys()
	if err != nil {
		return nil, err
	}
	for i := range keys {
		if keys[i].Name == name || keys[i].ID == name {
			return &keys[i], nil
		}
	}
	return nil, nil
}

func maskSecret(s string) string {
	if len(s) >= 6 {
		return s[len(s)-6:]
	}
	return "?"
}

func (p *panel) keyringList(w http.ResponseWriter, r *http.Request) {
	if p.user(r) == "" {
		p.errJSON(w, r, 401, "no session")
		return
	}
	keys, err := p.magpieKeys()
	if err != nil {
		p.errJSON(w, r, 502, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, map[string]any{
			"name": k.Name, "kid": k.ID, "masked": maskSecret(k.Secret),
		})
	}
	p.json(w, r, 200, map[string]any{"keys": out},
		[][2]string{{"Cache-Control", "no-store"}})
}

func (p *panel) keysTest(w http.ResponseWriter, r *http.Request) {
	if p.user(r) == "" {
		p.errJSON(w, r, 401, "no session")
		return
	}
	b := p.bodyJSON(r)
	key, _ := b["key"].(string)
	key = strings.TrimSpace(key)
	if key == "" {
		p.errJSON(w, r, 400, "先粘贴密钥值")
		return
	}
	st, _, body := p.httpGet(p.cfg.gatewayBase+"/v1/models",
		map[string]string{"Authorization": "Bearer " + key}, 10*time.Second)
	if st != 200 {
		p.json(w, r, 200, map[string]any{"ok": false, "status": st,
			"error": string(body[:min(len(body), 200)])}, nil)
		return
	}
	var m struct {
		Data []json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &m) != nil {
		p.json(w, r, 200, map[string]any{"ok": false, "error": "bad json"}, nil)
		return
	}
	p.json(w, r, 200, map[string]any{"ok": true, "models": len(m.Data)}, nil)
}

// ---------------------------------------------------------------- gen-config

func (p *panel) genConfig(w http.ResponseWriter, r *http.Request) {
	if p.user(r) == "" {
		p.errJSON(w, r, 401, "no session")
		return
	}
	b := p.bodyJSON(r)
	agent, _ := b["agent"].(string)
	agent = strings.TrimSpace(agent)
	name, _ := b["keyName"].(string)
	name = strings.TrimSpace(name)
	models := toStringSlice(b["models"])
	e, err := p.magpieKey(name)
	if err != nil {
		p.errJSON(w, r, 502, err.Error())
		return
	}
	if e == nil {
		p.errJSON(w, r, 404, "magpie 里没有这个网关密钥")
		return
	}
	host := hostOnly(r.Host)
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	root := strings.TrimRight(p.cfg.gatewayPublic, "/")
	if root == "" {
		root = fmt.Sprintf("http://%s:3425", host)
	}
	root = strings.TrimSuffix(root, "/v1")
	spec := agentByID(agent)
	if spec == nil {
		p.errJSON(w, r, 400, fmt.Sprintf("未知 agent: %s", agent))
		return
	}
	out := spec.Render(agentConfig{Root: root, Key: e.Secret, Models: models})
	p.json(w, r, 200, map[string]any{
		"ok": true, "config": out,
		"target": spec.Target, "targetEn": spec.TargetEN,
		"note": spec.Note, "noteEn": spec.NoteEN,
		"protocol": spec.Protocol,
	}, nil)
}

func toStringSlice(v any) []string {
	var out []string
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					out = append(out, s)
				}
			}
		}
	case string:
		for _, s := range strings.Fields(t) {
			out = append(out, s)
		}
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// ---------------------------------------------------------------- 系统页签

var backupNameRE = regexp.MustCompile(`^panel-backup-[0-9-]+\.tar\.gz(\.enc)?$`)

func (p *panel) system(w http.ResponseWriter, r *http.Request, path string) {
	if p.user(r) == "" {
		p.errJSON(w, r, 401, "no session")
		return
	}
	if path == "/api/system/backups" {
		rc, out, errs := p.wrapUpgrade([]string{"backups"}, nil, 30)
		var items []map[string]any
		for _, ln := range strings.Split(out, "\n") {
			parts := strings.Split(ln, "\t")
			if len(parts) == 3 {
				n, _ := strconv.ParseInt(parts[1], 10, 64)
				items = append(items, map[string]any{"name": parts[0], "bytes": n, "mtime": parts[2]})
			}
		}
		if items == nil {
			items = []map[string]any{}
		}
		errStr := ""
		if rc != 0 {
			errStr = errs
		}
		p.json(w, r, 200, map[string]any{"backups": items, "error": errStr}, nil)
		return
	}
	// info：current/latest（magpie web /api/update）+ last-good + NoKeyOut 版本 + 备份数
	var cur, latest any
	for attempt := range 2 {
		ck := p.cookies.get(attempt == 1)
		hdr := map[string]string{}
		if len(ck) > 0 {
			hdr["Cookie"] = strings.Join(ck, "; ")
		}
		st, _, ub := p.httpGet(p.cfg.upstreamWeb+"/api/update", hdr, 8*time.Second)
		if st == 200 {
			var u struct {
				Current any `json:"current"`
				Latest  any `json:"latest"`
			}
			if json.Unmarshal(ub, &u) == nil {
				cur, latest = u.Current, u.Latest
				break
			}
		}
	}
	good := readTrim("/var/lib/nokeyout/last-good-version")
	nkv := readTrim("/var/lib/nokeyout/VERSION")
	_, bout, _ := p.wrapUpgrade([]string{"backups"}, nil, 30)
	nBackups := 0
	for _, ln := range strings.Split(bout, "\n") {
		if strings.TrimSpace(ln) != "" {
			nBackups++
		}
	}
	p.json(w, r, 200, map[string]any{
		"current": cur, "latest": latest,
		"last_good": good, "nokeyout": nkv, "backups": nBackups,
	}, nil)
}

func readTrim(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (p *panel) systemDownload(w http.ResponseWriter, r *http.Request, name string) {
	if p.user(r) == "" {
		p.errJSON(w, r, 401, "no session")
		return
	}
	if !backupNameRE.MatchString(name) {
		p.errJSON(w, r, 400, "bad name")
		return
	}
	// 备份目录 root 0700；panel 非 root，经 wrapper 读（二进制流）
	cmdArgs := []string{upgradeBin, "read", name}
	if nonRoot() {
		cmdArgs = append([]string{"sudo", "-n"}, cmdArgs...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...).Output()
	if err != nil {
		msg := "not found"
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			msg = strings.TrimSpace(string(ee.Stderr))
		}
		p.errJSON(w, r, 404, msg)
		return
	}
	p.send(w, r, 200, out, "application/octet-stream", [][2]string{
		{"Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name)},
	})
}

func (p *panel) systemPost(w http.ResponseWriter, r *http.Request, action string) {
	if p.user(r) == "" {
		p.errJSON(w, r, 401, "no session")
		return
	}
	b := p.bodyJSON(r)
	switch action {
	case "update/check":
		p.updateCheck(w, r)
		return
	case "update/install":
		p.updateInstall(w, r)
		return
	case "update/rollback":
		p.updateRollback(w, r)
		return
	case "backup":
		pw, _ := b["passphrase"].(string)
		pw = strings.TrimSpace(pw)
		args := []string{"backup"}
		if pw != "" {
			args = append(args, pw)
		}
		rc, out, errs := p.wrapUpgrade(args, nil, 180)
		code := 200
		if rc != 0 {
			code = 500
		}
		p.json(w, r, code, map[string]any{"ok": rc == 0, "file": out, "error": errs}, nil)
	case "upgrade":
		ver, _ := b["version"].(string)
		ver = strings.TrimSpace(ver)
		if ver == "" {
			p.errJSON(w, r, 400, "必须指定版本号（x.y.z）——不默认追最新")
			return
		}
		rc, out, errs := p.wrapUpgrade([]string{"upgrade", ver}, nil, 900)
		code := 200
		if rc != 0 {
			code = 500
		}
		p.json(w, r, code, map[string]any{"ok": rc == 0, "out": out, "error": errs}, nil)
	case "rollback":
		rc, out, errs := p.wrapUpgrade([]string{"rollback"}, nil, 900)
		code := 200
		if rc != 0 {
			code = 500
		}
		p.json(w, r, code, map[string]any{"ok": rc == 0, "out": out, "error": errs}, nil)
	case "restore":
		name, _ := b["file"].(string)
		name = strings.TrimSpace(name)
		pw, _ := b["passphrase"].(string)
		pw = strings.TrimSpace(pw)
		var env map[string]string
		if pw != "" {
			env = map[string]string{"NK_RESTORE_PW": pw}
		}
		rc, out, errs := p.wrapUpgrade([]string{"restore", name}, env, 300)
		code := 200
		if rc != 0 {
			code = 500
		}
		p.json(w, r, code, map[string]any{"ok": rc == 0, "out": out, "error": errs}, nil)
	default:
		p.errJSON(w, r, 404, "unknown action")
	}
}

// ---------------------------------------------------------------- 首启向导

func (p *panel) firstboot(w http.ResponseWriter, r *http.Request, step string) {
	if fileExists(p.cfg.firstbootFlag) {
		p.errJSON(w, r, 404, "firstboot 已完成")
		return
	}
	user := p.user(r)
	if user == "" {
		p.errJSON(w, r, 401, "no session")
		return
	}
	b := p.bodyJSON(r)
	switch step {
	case "password":
		old, _ := b["old"].(string)
		newpw, _ := b["new"].(string)
		if len([]rune(newpw)) < 12 {
			p.errJSON(w, r, 400, "新口令至少 12 位")
			return
		}
		if v := verifyPassword(user, old); v == nil || !*v {
			p.errJSON(w, r, 403, "旧口令不对")
			return
		}
		cmdArgs := []string{"chpasswd"}
		if nonRoot() {
			cmdArgs = append([]string{"sudo", "-n"}, cmdArgs...)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
		cmd.Stdin = strings.NewReader(user + ":" + newpw + "\n")
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			p.errJSON(w, r, 500, "改口令失败: "+strings.TrimSpace(string(out)))
			return
		}
		p.json(w, r, 200, map[string]any{"ok": true, "note": "口令已改；下次登录用新口令"}, nil)
	case "provider":
		preset, _ := b["preset"].(string)
		preset = strings.TrimSpace(preset)
		key, _ := b["key"].(string)
		key = strings.TrimSpace(key)
		if preset == "" || key == "" {
			p.errJSON(w, r, 400, "preset 与 key 必填")
			return
		}
		rc, out, errs := runCLI([]string{"provider", "add", preset, key}, 30)
		if rc != 0 {
			p.errJSON(w, r, 502, firstNonEmpty(errs, out, fmt.Sprintf("provider add 失败 rc=%d", rc)))
			return
		}
		p.json(w, r, 200, map[string]any{"ok": true, "out": out}, nil)
	case "done":
		if err := os.MkdirAll(filepath.Dir(p.cfg.firstbootFlag), 0o755); err == nil {
			if err := os.WriteFile(p.cfg.firstbootFlag, []byte(time.Now().Format("2006-01-02T15:04:05")+"\n"), 0o644); err == nil {
				p.json(w, r, 200, map[string]bool{"ok": true}, nil)
				return
			}
		}
		p.errJSON(w, r, 500, "写完成标记失败")
	default:
		p.errJSON(w, r, 404, fmt.Sprintf("未知步骤 %s", step))
	}
}
