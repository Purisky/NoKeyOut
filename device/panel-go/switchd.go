//go:build !(js && wasm)

package main

// switchd.go —— 物理开关的状态权威（原 switchd.py 的 Go 移植，契约一致）
//
//   GET  /v1/state    {"locked":bool,"since":ts,"reason":"file|ttl|gpio|stub"}
//   GET  /v1/events   SSE（500ms 轮询状态文件，变更推送）
//   POST /v1/state    {"locked":bool}   仅回环
//   POST /v1/unlock   {"ttl_s":300}     仅回环

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	stateFile = sync.OnceValue(func() string {
		if v := os.Getenv("SWITCHD_STATE_FILE"); v != "" {
			return v
		}
		return "/var/lib/nokeyout/switch-state"
	})
	ttlUntil   float64
	ttlSince   float64 // TTL 解锁起点（unix 秒；0=未知）
	ttlUntilMu sync.Mutex
	procStart  = time.Now() // 无状态文件（开发机）时，端口开放计时从进程启动算起
)

func defaultLocked() bool { return os.Getenv("PANEL_DEFAULT_LOCKED") == "1" }

// readState：TTL 解锁期内恒 unlocked（reason=ttl）；否则读状态文件；
// 文件不存在 = 开发机无开关，默认 unlocked（不能把管理面锁死；失效方向 = 开放）。
func readState() (bool, string) {
	ttlUntilMu.Lock()
	until := ttlUntil
	ttlUntilMu.Unlock()
	if until > float64(time.Now().Unix()) {
		return false, "ttl"
	}
	b, err := os.ReadFile(stateFile())
	if err != nil {
		return defaultLocked(), "stub"
	}
	return strings.TrimSpace(string(b)) == "locked", "file"
}

// unlockedSince：管理面端口开放的起点。TTL 解锁 = 请求时刻；状态文件 = 文件 mtime
// （物理开关/CLI 每次落盘都会更新）；无文件（开发机 stub）= 进程启动时刻。
func unlockedSince() time.Time {
	ttlUntilMu.Lock()
	until, since := ttlUntil, ttlSince
	ttlUntilMu.Unlock()
	if until > float64(time.Now().Unix()) {
		if since > 0 {
			return time.Unix(int64(since), 0)
		}
		return procStart
	}
	if fi, err := os.Stat(stateFile()); err == nil {
		return fi.ModTime()
	}
	return procStart
}

// adminOpenSeconds：端口已开放多久（秒）。给屏幕的 ADMIN 页计时/闪烁用。
func adminOpenSeconds() float64 {
	d := time.Since(unlockedSince()).Seconds()
	if d < 0 {
		return 0
	}
	return d
}

// portOpen：管理面端口是否开放（unlocked）。nil cfg（纯单元测试）视为未开放。
func (p *panel) portOpen() bool {
	if p.cfg == nil {
		return false
	}
	return switchState(p.cfg) != "locked"
}

func writeState(locked bool) {
	_ = os.MkdirAll(dirOf(stateFile()), 0o755)
	v := "unlocked"
	if locked {
		v = "locked"
	}
	_ = os.WriteFile(stateFile(), []byte(v), 0o644)
}

// ---------------------------------------------------------------- 登录挑战码
//
// 失败升级挑战：第 N 次登录失败起，直到成功为止，每次尝试都要求一个
// **当次新**的挑战码。码只在本机产生、只在本机显示：绝不走网络、绝不落日志。
//
//	交付：① 屏/串口 = GET /v1/challenge（仅回环 = 本地信任边界）
//	     ② 无屏/纯 Linux 服务器 = root-only 文件
//
// 调用方必须遵守的顺序：**先验码，再验口令**；失败一律同一句错误
// （否则"口令对但码错"会泄露口令是否正确）。

var (
	chalFile = sync.OnceValue(func() string {
		if v := os.Getenv("SWITCHD_CHALLENGE_FILE"); v != "" {
			return v
		}
		return "/var/lib/nokeyout/login-challenge"
	})
)

func chalTTL() time.Duration {
	if v := os.Getenv("SWITCHD_CHALLENGE_TTL"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return time.Duration(f * float64(time.Second))
		}
	}
	return 60 * time.Second
}

func chalDigits() int {
	if v := os.Getenv("SWITCHD_CHALLENGE_DIGITS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 4 && n <= 10 {
			return n
		}
	}
	return 6
}

func challengeAfter() int {
	if v := os.Getenv("PANEL_CHALLENGE_AFTER"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			return n
		}
	}
	return 5
}

func challengeIdleReset() time.Duration {
	if v := os.Getenv("PANEL_CHALLENGE_IDLE_RESET"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return time.Duration(f * float64(time.Second))
		}
	}
	return 30 * time.Minute
}

type challengeState struct {
	mu   sync.Mutex
	code string
	at   time.Time
}

var chal = &challengeState{}

// issue：签发一个新码并交付（屏走 GET，无屏走 root-only 文件）。
func (c *challengeState) issue() string {
	d := chalDigits()
	limit := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(d)), nil)
	v, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return ""
	}
	code := fmt.Sprintf("%0*d", d, v)
	c.mu.Lock()
	c.code, c.at = code, time.Now()
	c.mu.Unlock()
	c.persist(code)
	return code
}

func (c *challengeState) persist(code string) {
	_ = os.MkdirAll(dirOf(chalFile()), 0o755)
	_ = os.WriteFile(chalFile(), []byte(code+"\n"), 0o600)
	_ = os.Chmod(chalFile(), 0o600) // 已存在时 WriteFile 不变更权限，显式压一次
}

// verify：常量时间比较；**单次消费**（无论成败都作废）。
func (c *challengeState) verify(code string) bool {
	c.mu.Lock()
	cur, at := c.code, c.at
	c.code = ""
	c.mu.Unlock()
	if cur == "" || time.Since(at) > chalTTL() {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(code)), []byte(cur)) == 1
}

// peek：给屏/串口读（仅回环）。已过期/已消费的码不返回。
func (c *challengeState) peek() map[string]any {
	c.mu.Lock()
	cur, at := c.code, c.at
	c.mu.Unlock()
	if cur == "" || time.Since(at) > chalTTL() {
		return map[string]any{"active": false}
	}
	return map[string]any{
		"active": true, "code": cur,
		"expires_at": at.Add(chalTTL()).Unix(), "digits": chalDigits(),
	}
}

func (c *challengeState) clear() {
	c.mu.Lock()
	c.code = ""
	c.mu.Unlock()
	_ = os.Remove(chalFile())
}

// 登录失败计数（进程内；自上次成功起，全局不分 IP）。
var (
	loginFails   int
	loginFailsMu sync.Mutex
	loginFailsAt time.Time
)

// challengeActive：失败达阈值、且未空闲超时 → 需要挑战码。
func challengeActive() bool {
	loginFailsMu.Lock()
	defer loginFailsMu.Unlock()
	if loginFails > 0 && !loginFailsAt.IsZero() && time.Since(loginFailsAt) > challengeIdleReset() {
		loginFails = 0
	}
	return loginFails >= challengeAfter()
}

func noteLoginFailure() {
	loginFailsMu.Lock()
	loginFails++
	loginFailsAt = time.Now()
	active := loginFails >= challengeAfter()
	loginFailsMu.Unlock()
	if active {
		chal.issue() // 升级态：每次失败都换一个新码
	}
}

func noteLoginSuccess() {
	loginFailsMu.Lock()
	loginFails, loginFailsAt = 0, time.Time{}
	loginFailsMu.Unlock()
	chal.clear()
}

func switchState(c *cfg) string {
	if c.switchdURL != "" {
		u := strings.TrimRight(c.switchdURL, "/")
		cl := &http.Client{Timeout: 3 * time.Second}
		resp, err := cl.Get(u + "/v1/state")
		if err == nil {
			var d struct {
				Locked bool `json:"locked"`
			}
			func() {
				defer resp.Body.Close()
				_ = json.NewDecoder(resp.Body).Decode(&d)
			}()
			if d.Locked {
				return "locked"
			}
			return "unlocked"
		}
		return "unlocked"
	}
	locked, _ := readState()
	if locked {
		return "locked"
	}
	return "unlocked"
}

func swJSON(w http.ResponseWriter, code int, obj any) {
	b, _ := json.Marshal(obj)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

func loopbackOnly(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	return host == "127.0.0.1" || host == "::1"
}

func switchdMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/state", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			locked, reason := readState()
			since := time.Now().Unix()
			if locked {
				if fi, err := os.Stat(stateFile()); err == nil {
					since = fi.ModTime().Unix()
				}
			} else {
				since = unlockedSince().Unix()
			}
			swJSON(w, 200, map[string]any{"locked": locked, "since": since, "reason": reason})
		case http.MethodPost:
			if !loopbackOnly(r) {
				swJSON(w, 403, map[string]string{"error": "loopback only"})
				return
			}
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			v, ok := b["locked"]
			if !ok {
				swJSON(w, 400, map[string]string{"error": "missing locked"})
				return
			}
			writeState(asBool(v))
			swJSON(w, 200, map[string]bool{"ok": true})
		default:
			swJSON(w, 404, map[string]string{"error": "not found"})
		}
	})
	mux.HandleFunc("/v1/unlock", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			swJSON(w, 404, map[string]string{"error": "not found"})
			return
		}
		if !loopbackOnly(r) {
			swJSON(w, 403, map[string]string{"error": "loopback only"})
			return
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		ttl := 300
		if v, ok := b["ttl_s"].(float64); ok {
			ttl = int(v)
		}
		if ttl < 1 {
			ttl = 1
		}
		if ttl > 86400 {
			ttl = 86400
		}
		ttlUntilMu.Lock()
		now := time.Now()
		ttlSince = float64(now.Unix())
		ttlUntil = float64(now.Add(time.Duration(ttl) * time.Second).Unix())
		until := ttlUntil
		ttlUntilMu.Unlock()
		swJSON(w, 200, map[string]any{"ok": true, "until": int(until)})
	})
	mux.HandleFunc("/v1/challenge", func(w http.ResponseWriter, r *http.Request) {
		if !loopbackOnly(r) {
			swJSON(w, 403, map[string]string{"error": "loopback only"})
			return
		}
		switch r.Method {
		case http.MethodGet: // 屏/串口/headless 读取器
			swJSON(w, 200, chal.peek())
		case http.MethodPost: // panel：收到登录尝试时签发
			chal.issue()
			swJSON(w, 200, map[string]bool{"ok": true})
		default:
			swJSON(w, 404, map[string]string{"error": "not found"})
		}
	})
	mux.HandleFunc("/v1/challenge/verify", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !loopbackOnly(r) {
			swJSON(w, 403, map[string]string{"error": "loopback only"})
			return
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		code, _ := b["code"].(string)
		ok := chal.verify(code)
		if !ok {
			chal.issue() // 失败也换新码：下一次尝试仍是新挑战
		}
		swJSON(w, 200, map[string]bool{"ok": ok})
	})
	mux.HandleFunc("/v1/challenge/clear", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !loopbackOnly(r) {
			swJSON(w, 403, map[string]string{"error": "loopback only"})
			return
		}
		chal.clear()
		swJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			swJSON(w, 404, map[string]string{"error": "not found"})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(200)
		fl, _ := w.(http.Flusher)
		var last *bool
		for {
			locked, _ := readState()
			if last == nil || locked != *last {
				l := locked
				last = &l
				b, _ := json.Marshal(map[string]bool{"locked": locked})
				_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
				if fl != nil {
					fl.Flush()
				}
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		swJSON(w, 404, map[string]string{"error": "not found"})
	})
	return mux
}

// serveSwitchd：只构造 server（不监听）；调用方负责 ListenAndServe。
func serveSwitchd(host string, port int) *http.Server {
	return &http.Server{Addr: net.JoinHostPort(host, strconv.Itoa(port)), Handler: switchdMux()}
}

func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true" || t == "1"
	case float64:
		return t != 0
	}
	return false
}
