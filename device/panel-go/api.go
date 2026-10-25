//go:build !(js && wasm)

package main

// api.go —— 路由分发 + 各端点（契约对原 Python server.py，字段名/状态码一致）

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

func (p *panel) route(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	// 屏幕模块布局：GET 读、POST 写，两种方法都要到这里（routeGET/routePOST 是分开的）
	if path == "/api/screen/modules" {
		p.screenModules(w, r)
		return
	}
	if path == "/api/screen/config" {
		p.screenCfgHandler(w, r)
		return
	}
	if path == "/api/screen/preview" {
		p.screenPreview(w, r)
		return
	}
	// USER 按键动作配置（GET 读 / POST 写；MASK 键不接管）
	if path == "/api/user-button" {
		p.userButtonHandler(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		p.routeGET(w, r)
	case http.MethodPost:
		p.routePOST(w, r)
	case http.MethodPut, http.MethodPatch:
		if p.user(r) == "" {
			p.errJSON(w, r, 404, "not found")
			return
		}
		p.consoleProxy(w, r, r.URL.RequestURI(), true)
	case http.MethodDelete:
		p.catchall(w, r)
	default:
		p.errJSON(w, r, 405, "method not allowed")
	}
}

func (p *panel) routeGET(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/healthz" {
		p.json(w, r, 200, map[string]any{"ok": true, "v": Version}, nil)
		return
	}
	// 界面语言（公开偏好，非秘密）：GET 供页面初始化；切换见 routePOST
	if path == "/api/lang" {
		p.json(w, r, 200, map[string]any{"lang": langGet(), "langs": langList[:]}, nil)
		return
	}
	if switchState(p.cfg) == "locked" {
		p.lockedPage(w, r)
		return
	}
	if path == "/" || path == "/login" {
		if p.user(r) != "" {
			p.redirect(w, r, "/portal")
			return
		}
		p.page(w, r, "login.html", 200, nil)
		return
	}
	if path == "/portal" {
		if p.user(r) == "" {
			p.redirect(w, r, "/")
			return
		}
		p.page(w, r, "portal.html", 200, nil)
		return
	}
	if path == "/firstboot" {
		if fileExists(p.cfg.firstbootFlag) {
			p.redirect(w, r, "/portal")
			return
		}
		if p.user(r) == "" {
			p.redirect(w, r, "/")
			return
		}
		p.page(w, r, "firstboot.html", 200, nil)
		return
	}
	// 虚拟屏（调试）：随会话门控；LOCKED 时监听整个关闭 → 与管理面同生共死
	if path == "/screen/ws" {
		if p.user(r) == "" {
			p.errJSON(w, r, 401, "no session")
			return
		}
		p.screenWS(w, r)
		return
	}
	if path == "/screen" || path == "/screen/frame.png" || path == "/screen/state" {
		if p.user(r) == "" {
			p.errJSON(w, r, 401, "no session")
			return
		}
		p.screenHandlers(w, r, path)
		return
	}
	// 直操纵像素（/v1/screen/*）：draw/font/mode/frame/state，会话门控；manual 模式内置轮播让位
	if path == "/v1/screen" || strings.HasPrefix(path, "/v1/screen/") {
		if p.user(r) == "" {
			p.errJSON(w, r, 401, "no session")
			return
		}
		p.screenDrvHandler(w, r, strings.TrimPrefix(path, "/v1/screen"))
		return
	}
	if path == "/api/status" {
		if p.user(r) == "" {
			p.errJSON(w, r, 401, "no session")
			return
		}
		p.apiStatus(w, r)
		return
	}
	if path == "/api/models" {
		if p.user(r) == "" {
			p.errJSON(w, r, 401, "no session")
			return
		}
		p.apiModels(w, r)
		return
	}
	if path == "/api/keyring" {
		p.keyringList(w, r)
		return
	}
	if path == "/api/agents" {
		p.apiAgents(w, r)
		return
	}
	if path == "/api/gen-config" {
		p.genConfig(w, r)
		return
	}
	if strings.HasPrefix(path, "/api/icon/") {
		p.icon(w, r, strings.TrimPrefix(path, "/api/icon/"))
		return
	}
	if path == "/api/system/update" {
		p.updateStatus(w, r)
		return
	}
	if path == "/api/system/info" || path == "/api/system/backups" {
		p.system(w, r, path)
		return
	}
	if strings.HasPrefix(path, "/api/system/backup/") {
		p.systemDownload(w, r, strings.TrimPrefix(path, "/api/system/backup/"))
		return
	}
	if path == "/api/stats" || path == "/api/quotas" || path == "/v1/stats/snapshot" {
		if path == "/v1/stats/snapshot" && p.cfg.screenToken != "" {
			if got := r.Header.Get("X-Screen-Token"); got != "" && hmacEq(got, p.cfg.screenToken) {
				p.json(w, r, 200, p.stats.snapshot(), nil)
				return
			}
		}
		if p.user(r) == "" {
			p.errJSON(w, r, 401, "no session")
			return
		}
		switch path {
		case "/v1/stats/snapshot":
			p.json(w, r, 200, p.stats.snapshot(), nil)
		case "/api/quotas":
			p.json(w, r, 200, map[string]any{"pools": p.stats.snapshot()["pools"]}, nil)
		default:
			p.json(w, r, 200, p.stats.snapshot(), nil)
		}
		return
	}
	if path == "/console" || strings.HasPrefix(path, "/console/") {
		if p.user(r) == "" {
			p.redirect(w, r, "/")
			return
		}
		q := r.URL.RawQuery
		up := "/" + strings.TrimPrefix(path, "/console")
		if q != "" {
			up += "?" + q
		}
		p.consoleProxy(w, r, up, true)
		return
	}
	if switchState(p.cfg) == "locked" || p.user(r) == "" {
		p.send(w, r, 404, []byte("not found"), "text/plain; charset=utf-8", nil)
		return
	}
	p.catchall(w, r)
}

func (p *panel) catchall(w http.ResponseWriter, r *http.Request) {
	if p.user(r) == "" {
		p.errJSON(w, r, 404, "not found")
		return
	}
	p.consoleProxy(w, r, r.URL.RequestURI(), true)
}

func (p *panel) routePOST(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	// 直操纵像素（/v1/screen/*）：draw/font/mode/...，随会话门控
	if path == "/v1/screen" || strings.HasPrefix(path, "/v1/screen/") {
		if p.user(r) == "" {
			p.errJSON(w, r, 401, "no session")
			return
		}
		p.screenDrvHandler(w, r, strings.TrimPrefix(path, "/v1/screen"))
		return
	}
	// 虚拟屏调试控制（/screen/ctl）：随会话门控
	if path == "/screen/ctl" {
		if p.user(r) == "" {
			p.errJSON(w, r, 401, "no session")
			return
		}
		p.screenCtlHandler(w, r)
		return
	}
	// 界面语言切换（屏与 Web 同源）：不涉密，登录前也允许（否则登录页无法切语言）
	if path == "/api/lang" {
		var body struct {
			Lang string `json:"lang"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		ok := langSet(body.Lang)
		p.json(w, r, map[bool]int{true: 200, false: 400}[ok],
			map[string]any{"lang": langGet(), "ok": ok}, nil)
		return
	}
	if path == "/login" {
		p.login(w, r)
		return
	}
	if path == "/logout" {
		p.sess.drop(p.sessionCookie(r))
		p.json(w, r, 200, map[string]bool{"ok": true}, [][2]string{
			{"Set-Cookie", "nk_sid=; HttpOnly; Path=/; Max-Age=0"},
		})
		return
	}
	if strings.HasPrefix(path, "/api/firstboot/") {
		p.firstboot(w, r, path[strings.LastIndex(path, "/")+1:])
		return
	}
	if path == "/api/gen-config" {
		p.genConfig(w, r)
		return
	}
	if path == "/api/client-key" {
		p.errJSON(w, r, 410, "此接口已停用：密钥请在 magpie 控制台创建（唯一来源）")
		return
	}
	if path == "/api/keys/test" {
		p.keysTest(w, r)
		return
	}
	if strings.HasPrefix(path, "/api/system/") {
		p.systemPost(w, r, strings.TrimPrefix(path, "/api/system/"))
		return
	}
	p.catchall(w, r)
}

// ---------------------------------------------------------------- login

func (p *panel) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !p.rl.check(ip) {
		p.errJSON(w, r, 429, "失败次数过多，请 5 分钟后再试")
		return
	}
	b := p.bodyJSON(r)
	user, _ := b["user"].(string)
	user = strings.TrimSpace(user)
	pw, _ := b["pass"].(string)
	code, _ := b["code"].(string)
	// ① 先验挑战码（升级态）：先验码 = 绝不泄露"口令是否正确"
	if challengeActive() && !chal.verify(code) {
		p.rl.fail(ip)
		noteLoginFailure()
		time.Sleep(p.rl.penalty(ip)) // 稀释时序侧信道
		p.json(w, r, 401, map[string]any{"error": "账号或口令错误", "challenge": true}, nil)
		return
	}
	ok := false
	if p.cfg.devAuth && user == envOr("PANEL_DEV_USER", "dev") && pw != "" {
		ok = true
	} else if user != "" && pw != "" {
		v := verifyPassword(user, pw)
		ok = v != nil && *v
	}
	if !ok {
		noteLoginFailure()
		time.Sleep(p.rl.penalty(ip))
		p.json(w, r, 401, map[string]any{"error": "账号或口令错误", "challenge": challengeActive()}, nil)
		return
	}
	p.rl.ok(ip)
	noteLoginSuccess()
	sid := p.sess.create(user, p.cfg.getSecret())
	_, firstboot := os.Stat(p.cfg.firstbootFlag)
	p.json(w, r, 200, map[string]any{"ok": true, "user": user, "firstboot": os.IsNotExist(firstboot)}, [][2]string{
		{"Set-Cookie", fmt.Sprintf("nk_sid=%s; HttpOnly; Secure; SameSite=Strict; Path=/; Max-Age=%d", sid, p.cfg.sessionTTL)},
	})
}

// hostOnly：取 Host 的 host 部分（Go 的 Host 在 r.Host，不在 r.Header）
func hostOnly(h string) string {
	if i := strings.LastIndex(h, ":"); i > 0 {
		return h[:i]
	}
	return h
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// ---------------------------------------------------------------- 页面素材

func (p *panel) lockedPage(w http.ResponseWriter, r *http.Request) {
	b, err := os.ReadFile(webDir + "/locked.html")
	if err != nil {
		b = []byte("<h1>设备处于关闭模式</h1>")
	}
	p.send(w, r, 503, b, "text/html; charset=utf-8", nil)
}

func (p *panel) apiStatus(w http.ResponseWriter, r *http.Request) {
	modelsN := -1
	st := 0
	if s, _, body := p.httpGet(p.cfg.gatewayBase+"/v1/models", p.authHdr(), 6*time.Second); s == 200 {
		var m struct {
			Data []json.RawMessage `json:"data"`
		}
		if json.Unmarshal(body, &m) == nil {
			st, modelsN = 200, len(m.Data)
		}
	}
	stw := 0
	if s, _, _ := p.httpGet(p.cfg.upstreamWeb+"/", nil, 4*time.Second); s != 0 {
		stw = s
	}
	gwURL := p.cfg.gatewayPublic
	if gwURL == "" {
		host := hostOnly(r.Host)
		if i := strings.LastIndex(host, ":"); i > 0 {
			host = host[:i]
		}
		gwURL = fmt.Sprintf("http://%s:3425/v1", host)
	}
	p.json(w, r, 200, map[string]any{
		"gateway":    map[string]any{"status": st, "models": modelsN},
		"gatewayUrl": gwURL,
		"web":        map[string]any{"status": stw, "key_configured": p.cfg.magpieWebKey != ""},
		"switch":     switchState(p.cfg),
		"version":    Version,
		"pluginUrl":  p.cfg.pluginURL,
	}, nil)
}

func (p *panel) authHdr() map[string]string {
	if p.cfg.panelGatewayKey != "" {
		return map[string]string{"Authorization": "Bearer " + p.cfg.panelGatewayKey}
	}
	return nil
}

func (p *panel) apiModels(w http.ResponseWriter, r *http.Request) {
	// 面板与 magpie 同在设备上，走 127.0.0.1 回环；magpie 对回环不做网关鉴权。
	// PANEL_GATEWAY_KEY 仍可选（非回环部署），但不再是必需项。
	st, hd, body := p.httpGet(p.cfg.gatewayBase+"/v1/models", p.authHdr(), 10*time.Second)
	if st == 0 {
		p.errJSON(w, r, 502, "gateway 不可达")
		return
	}
	ct := hd["Content-Type"]
	if ct == "" {
		ct = "application/json; charset=utf-8"
	}
	p.send(w, r, st, body, ct, nil)
}

// ---------------------------------------------------------------- 图标

var iconNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,48}$`)

func (p *panel) icon(w http.ResponseWriter, r *http.Request, name string) {
	if p.user(r) == "" {
		p.errJSON(w, r, 401, "no session")
		return
	}
	if !iconNameRE.MatchString(name) {
		p.errJSON(w, r, 400, "bad name")
		return
	}
	p.icons.mu.Lock()
	if hit, ok := p.icons.d[name]; ok && time.Since(hit.ts) < 600*time.Second {
		body := hit.body
		p.icons.mu.Unlock()
		p.send(w, r, 200, body, "image/svg+xml", nil)
		return
	}
	p.icons.mu.Unlock()

	candidates := []string{name}
	if i := strings.Index(name, "-"); i > 0 {
		candidates = append(candidates, name[:i])
	}
	ck := p.cookies.get(false)
	for _, cand := range candidates {
		hdr := map[string]string{"Accept": "image/svg+xml"}
		if len(ck) > 0 {
			hdr["Cookie"] = strings.Join(ck, "; ")
		}
		st, _, body := p.httpGet(strings.TrimRight(p.cfg.upstreamWeb, "/")+"/icons/"+cand+".svg", hdr, 8*time.Second)
		if st == 200 && strings.Contains(string(body[:min(len(body), 200)]), "<svg") {
			p.icons.mu.Lock()
			p.icons.d[name] = iconEntry{body: body, ts: time.Now()}
			p.icons.mu.Unlock()
			p.send(w, r, 200, body, "image/svg+xml", nil)
			return
		}
	}
	p.icons.mu.Lock()
	p.icons.d[name] = iconEntry{body: genericSVG, ts: time.Now()}
	p.icons.mu.Unlock()
	p.send(w, r, 200, genericSVG, "image/svg+xml", nil)
}

// ---------------------------------------------------------------- CLI / wrapper

// runCLI：经白名单包装器调 magpie CLI。非 root 走 sudo -n（sudoers 只放行本包装器）。
func runCLI(args []string, timeoutSec int) (int, string, string) {
	base := strings.Fields(panelCfg.cliWrapper)
	if len(base) == 0 {
		return 127, "", "NOKEYOUT_CLI 为空"
	}
	cmdArgs := append(append([]string{}, base...), args...)
	if nonRoot() {
		cmdArgs = append([]string{"sudo", "-n"}, cmdArgs...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...).CombinedOutput()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), strings.TrimSpace(string(out)), ""
		}
		return 127, "", err.Error()
	}
	return 0, strings.TrimSpace(string(out)), ""
}

// wrapUpgrade：系统页签的 root 动作（备份/升级/回退/恢复）走 sudoers 白名单 wrapper
func (p *panel) wrapUpgrade(args []string, env map[string]string, timeoutSec int) (int, string, string) {
	cmdArgs := []string{upgradeBin}
	cmdArgs = append(cmdArgs, args...)
	if nonRoot() {
		cmdArgs = append([]string{"sudo", "-n"}, cmdArgs...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), strings.TrimSpace(string(out)), ""
		}
		return 127, "", err.Error()
	}
	return 0, strings.TrimSpace(string(out)), ""
}
