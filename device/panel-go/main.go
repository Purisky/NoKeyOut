//go:build !(js && wasm)

// nokeyout-panel —— NoKeyOut 设备管理面（Go 重写版）
//
// 契约与原 Python server.py 逐条对齐（路由/字段/状态码/安全头），portal.html 等
// 静态页零改动。PAM 校验经 root helper（生产面板本就以 nokeyout 非 root 运行，
// Python 版的 python3-pam 原生路径与 crypt 回落在生产均不可达，故 Go 版只保留
// helper 一条路：缺失即拒绝——fail closed）。
//
// 与 magpie 的边界不变：本进程只经 HTTP 与 magpie 交互（3425 网关 / 3430 web）。
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const Version = "0.1.0" // 面板自身版本（NoKeyOut 发布版见 /var/lib/nokeyout/VERSION）
const authHelper = "/usr/local/bin/nokeyout-auth"
const upgradeBin = "/usr/local/bin/nokeyout-upgrade"
const maxBody = 32 << 20
const maxJSONBody = 64 << 10

func envFile(p string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(p)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		k, v, _ := strings.Cut(line, "=")
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out
}

type cfg struct {
	host            string
	port            int
	tlsCert, tlsKey string
	allowPlainHTTP  bool
	gateMode        string
	switchPoll      float64
	sessionTTL      int
	devAuth         bool
	gatewayBase     string
	upstreamWeb     string
	switchdURL      string
	switchFile      string
	secretFile      string
	panelGatewayKey string
	magpieWebKey    string
	screenToken     string
	cliWrapper      string
	gatewayPublic   string
	pluginURL       string
	firstbootFlag   string
	secret          []byte
	secretOnce      sync.Once
	// 屏幕（进程内渲染线程）
	screenBackend  string // "" | console | st7789
	screenSPI      string
	screenHz       uint32
	screenDC       int
	screenRST      int // -1 = 不接硬件复位
	screenMADCTL   int
	screenXOff     int
	screenYOff     int
	screenInvert   bool
	screenCfg      screenCfg // 面板尺寸/方向（screen.json > 环境变量）
	screenSelfTest bool
	screenGate     string // 上电门文件（存在前屏幕熄闭）；空=不设门
	screenBL       int    // 背光 GPIO（全局号）；-1=不控
	bl             *gpioOut
	screenPngDir   string
	screenPngScale int
	screensDir     string
	// USER 按键（板载，默认熄屏/亮屏）
	userButtonGPIO      int // 全局 GPIO 号；-1=不启用
	userButtonActiveLow bool
	userButtonPollMs    int
	userButtonDebounce  int

	usageLogPath  string // magpie 逐请求流水：用于首次导入 magpie 流水（之后只用自建聚合）
	usageHistPath string // 面板自建的用量历史（细粒度直方图；空/off = 只用 API 粗桶）
}

var envFileCache = sync.OnceValue(func() map[string]string { return envFile("/etc/nokeyout/panel.env") })

func cfgGet(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	if v := envFileCache()[k]; v != "" {
		return v
	}
	return d
}

func loadCfg() *cfg {
	c := &cfg{}
	c.host = cfgGet("PANEL_HOST", "0.0.0.0")
	c.port, _ = strconv.Atoi(cfgGet("PANEL_PORT", "8443"))
	c.tlsCert = cfgGet("PANEL_TLS_CERT", "")
	c.tlsKey = cfgGet("PANEL_TLS_KEY", "")
	c.allowPlainHTTP = cfgGet("PANEL_ALLOW_INSECURE_HTTP", "") == "1"
	c.gateMode = cfgGet("PANEL_GATE", "process")
	c.switchPoll, _ = strconv.ParseFloat(cfgGet("PANEL_SWITCH_POLL", "0.5"), 64)
	c.sessionTTL, _ = strconv.Atoi(cfgGet("PANEL_SESSION_TTL", "1800"))
	c.devAuth = cfgGet("PANEL_DEV_AUTH", "") == "1"
	c.gatewayBase = strings.TrimRight(cfgGet("GATEWAY_BASE", "http://127.0.0.1:3425"), "/")
	c.upstreamWeb = strings.TrimRight(cfgGet("UPSTREAM_WEB", "http://127.0.0.1:3430"), "/")
	c.switchdURL = cfgGet("SWITCHD_URL", "")
	c.switchFile = cfgGet("SWITCHD_STATE_FILE", "/var/lib/nokeyout/switch-state")
	c.secretFile = cfgGet("PANEL_SECRET_FILE", "/var/lib/nokeyout/secrets/session.key")
	c.panelGatewayKey = readKeyFile(cfgGet("PANEL_GATEWAY_KEY", ""), cfgGet("PANEL_GATEWAY_KEY_FILE", "/var/lib/nokeyout/secrets/panel-gateway.key"))
	c.magpieWebKey = readKeyFile(cfgGet("MAGPIE_WEB_KEY", ""), cfgGet("MAGPIE_WEB_KEY_FILE", "/var/lib/nokeyout/secrets/magpie-web.key"))
	c.screenToken = readKeyFile(cfgGet("PANEL_SCREEN_TOKEN", ""), cfgGet("PANEL_SCREEN_TOKEN_FILE", "/var/lib/nokeyout/secrets/screen.token"))
	c.cliWrapper = cfgGet("NOKEYOUT_CLI", "/usr/local/bin/nokeyout-magpie-cli")
	c.gatewayPublic = cfgGet("NOKEYOUT_GATEWAY_PUBLIC", "")
	c.usageLogPath = cfgGet("MAGPIE_USAGE_LOG", "/var/lib/nokeyout/magpie-config/magpie/usage.jsonl")
	c.usageHistPath = cfgGet("USAGE_HIST_FILE", "/var/lib/nokeyout/state/usage-hist.bin")
	c.pluginURL = strings.TrimRight(cfgGet("NOKEYOUT_PLUGIN_URL", "https://nokeyout.com/omp-provider"), "/")
	c.firstbootFlag = cfgGet("FIRSTBOOT_FLAG", "/var/lib/nokeyout/firstboot.done")
	// 屏幕（可选组件）：SCREEN_BACKEND=console|st7789 才会起渲染线程
	c.screenBackend = cfgGet("SCREEN_BACKEND", "")
	c.screenSPI = cfgGet("SCREEN_SPI", spiDefaultDev)
	hz, _ := strconv.ParseUint(cfgGet("SCREEN_HZ", "40000000"), 10, 32)
	c.screenHz = uint32(hz)
	c.screenDC = cfgInt("SCREEN_DC", "128")
	c.screenRST = cfgInt("SCREEN_RST", "140")
	c.screenMADCTL = cfgIntHex(cfgGet("SCREEN_MADCTL", "0x00"))
	c.screenXOff = cfgInt("SCREEN_XOFF", "0")
	c.screenYOff = cfgInt("SCREEN_YOFF", "0")
	c.screenInvert = cfgGet("SCREEN_INVERT", "1") != "0"
	c.screenSelfTest = cfgGet("SCREEN_SELFTEST", "") == "1"
	// 面板尺寸与方向：screen.json（管理面可写）覆盖环境默认值；逻辑尺寸在这里发布
	c.screenCfg = loadScreenCfg()
	setScreenGeometry(c.screenCfg)
	if c.screenCfg.Invert != nil {
		c.screenInvert = *c.screenCfg.Invert
	}
	// 上电门：门文件存在前，硬件屏保持睡眠/熄背光，不渲染。
	// 空 = 不设门（默认）；文件一旦存在即放行，删除即重新熄屏。
	c.screenGate = cfgGet("SCREEN_GATE_FILE", "/var/lib/nokeyout/state/screen.gate")
	c.screenBL = cfgInt("SCREEN_BL", "-1") // 背光 GPIO（全局号）；-1=不控背光
	c.screenPngDir = cfgGet("SCREEN_PNG_DIR", "screen-debug")
	c.screenPngScale = cfgInt("SCREEN_PNG_SCALE", "1")
	// 用户自定义屏页（JSON 描述符，无代码执行）；空目录=只有内置三页
	c.screensDir = cfgGet("SCREENS_DIR", "/var/lib/nokeyout/screens")
	// USER 按键：板载 K2_USR = GPIO4_B2 = 138（按下短接到 GND）。
	// 动作映射可经 Web 改（USER_BUTTON_FILE）；屏幕开关状态只存内存。
	c.userButtonGPIO = cfgInt("USER_BUTTON_GPIO", "138")
	c.userButtonActiveLow = cfgGet("USER_BUTTON_ACTIVE_LOW", "1") != "0"
	c.userButtonPollMs = cfgInt("USER_BUTTON_POLL_MS", "30")
	c.userButtonDebounce = cfgInt("USER_BUTTON_DEBOUNCE_MS", "60")
	return c
}

func cfgInt(k, def string) int {
	v, err := strconv.Atoi(cfgGet(k, def))
	if err != nil {
		v, _ = strconv.Atoi(def)
	}
	return v
}

func cfgIntHex(s string) int {
	if v, err := strconv.ParseInt(strings.TrimSpace(s), 0, 32); err == nil {
		return int(v)
	}
	return 0x70
}

func readKeyFile(direct, path string) string {
	if direct != "" {
		return direct
	}
	if b, err := os.ReadFile(path); err == nil {
		return strings.TrimSpace(string(b))
	}
	return ""
}

func (c *cfg) getSecret() []byte {
	c.secretOnce.Do(func() {
		if b, err := os.ReadFile(c.secretFile); err == nil && len(b) > 0 {
			c.secret = b
			return
		}
		b := make([]byte, 32)
		if _, err := rand.Read(b); err == nil {
			c.secret = b
			_ = os.MkdirAll(dirOf(c.secretFile), 0o755)
			_ = os.WriteFile(c.secretFile, b, 0o600)
		}
	})
	return c.secret
}

func dirOf(p string) string {
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return "."
	}
	return p[:i]
}

func nonRoot() bool { return syscall.Geteuid() != 0 }

// rotateSessionSecret：锁定时轮换 session secret 并清空所有会话
func (p *panel) rotateSessionSecret() {
	sec := make([]byte, 32)
	if _, err := rand.Read(sec); err != nil {
		log.Printf("session: 轮换失败（%v）——保留原 secret", err)
		return
	}
	p.cfg.secret = sec
	_ = os.MkdirAll(dirOf(p.cfg.secretFile), 0o700)
	if err := os.WriteFile(p.cfg.secretFile, sec, 0o600); err != nil {
		log.Printf("session: secret 写盘失败（%v）——内存轮换仍生效", err)
	}
	p.sess.dropAll()
}

// cmdLock：`panel lock on|off|status` —— 无屏/纯 Linux 形态的一键锁
// （等价于 nokeyout-lock；硬件形态仍用拨动开关）。

// cmdScreenGate：屏幕上电门（无服务形态）。
//
//	panel-go screen-gate pass    # 放行屏幕（写门文件）
//	panel-go screen-gate revoke  # 撤销 → 熄屏
//	panel-go screen-gate status  # 查状态
//
// 门文件默认 /var/lib/nokeyout/state/screen.gate（SCREEN_GATE_FILE 可改）。
func cmdScreenGate(args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	gate := cfgGet("SCREEN_GATE_FILE", "/var/lib/nokeyout/state/screen.gate")
	switch sub {
	case "pass":
		if err := os.MkdirAll(filepath.Dir(gate), 0o755); err != nil {
			log.Printf("screen-gate: 建目录失败: %v", err)
			return 1
		}
		body := fmt.Sprintf("ok %s screen-gate:pass\n", time.Now().UTC().Format(time.RFC3339))
		if err := os.WriteFile(gate, []byte(body), 0o644); err != nil {
			log.Printf("screen-gate: 写 %s 失败: %v", gate, err)
			return 1
		}
		fmt.Printf("screen-gate: 已放行屏幕（%s）\n", gate)
		return 0
	case "revoke":
		if err := os.Remove(gate); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("screen-gate: 删 %s 失败: %v", gate, err)
			return 1
		}
		fmt.Printf("screen-gate: 已撤销（%s）——屏幕熄闭\n", gate)
		return 0
	case "status":
		_, err := os.Stat(gate)
		if err == nil {
			fmt.Printf("screen-gate: OPEN（%s 存在，屏幕允许点亮）\n", gate)
			return 0
		}
		fmt.Printf("screen-gate: CLOSED（%s 不存在，屏幕保持熄闭）\n", gate)
		return 1
	default:
		fmt.Print("用法: panel-go screen-gate pass|revoke|status\n")
		return 2
	}
}

func cmdLock(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: panel lock on|off|status")
		return 2
	}
	switch args[0] {
	case "on", "lock":
		writeState(true)
		fmt.Println("locked")
	case "off", "unlock":
		writeState(false)
		fmt.Println("unlocked")
	case "status", "st":
		locked, reason := readState()
		state := "unlocked"
		if locked {
			state = "locked"
		}
		fmt.Printf("%s (reason=%s)\n", state, reason)
	default:
		fmt.Fprintf(os.Stderr, "unknown %q: on|off|status\n", args[0])
		return 2
	}
	return 0
}

// ---------------------------------------------------------------- server

type panel struct {
	bl      *gpioOut // 背光 GPIO 句柄（SCREEN_BL≥0 时懒打开）
	cfg     *cfg
	sess    *sessions
	rl      *rateLimit
	stats   *stats
	cookies *webCookies
	icons   *iconCache
	client  *http.Client
	screen  *shadowScreen // 虚拟屏（SCREEN_BACKEND=web|png 时非 nil）
	// screenOff：USER 键/Web 触发的熄屏状态。**仅内存**——重启/上电一律亮屏。
	screenOff atomic.Bool
	// 所以用原子指针发布（History 自身带锁），渲染线程未启动时为 nil。
	screenHist  atomic.Pointer[History]
	screenToast atomic.Pointer[screenToastMsg]
}

var (
	secHeaders = [][2]string{
		{"X-Content-Type-Options", "nosniff"},
		{"X-Frame-Options", "SAMEORIGIN"},
		{"Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; " +
				"style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; " +
				"connect-src 'self'; frame-src 'self'; worker-src 'self' blob:"},
		{"Referrer-Policy", "no-referrer"},
	}
	dropHdrs = map[string]bool{
		"content-length": true, "connection": true, "keep-alive": true,
		"proxy-authenticate": true, "proxy-authorization": true, "te": true,
		"trailers": true, "transfer-encoding": true, "upgrade": true, "set-cookie": true,
	}
)

func newPanel() *panel {
	c := loadCfg()
	p := &panel{
		cfg:     c,
		sess:    newSessions(c.sessionTTL),
		rl:      newRateLimit(5, 300),
		stats:   newStats(c),
		cookies: newWebCookies(c),
		icons:   newIconCache(),
		client:  &http.Client{Timeout: 15 * time.Second},
	}
	return p
}

func (p *panel) send(w http.ResponseWriter, r *http.Request, code int, body []byte, ctype string, extra [][2]string) {
	h := w.Header()
	h.Set("Content-Type", ctype)
	for _, kv := range secHeaders {
		h.Set(kv[0], kv[1])
	}
	for _, kv := range extra {
		h.Set(kv[0], kv[1])
	}
	w.WriteHeader(code)
	if r.Method != http.MethodHead && len(body) > 0 {
		_, _ = w.Write(body)
	}
}

func (p *panel) json(w http.ResponseWriter, r *http.Request, code int, obj any, extra [][2]string) {
	b, err := json.Marshal(obj)
	if err != nil {
		b = []byte(`{"error":"marshal"}`)
	}
	p.send(w, r, code, b, "application/json; charset=utf-8", extra)
}

func (p *panel) errJSON(w http.ResponseWriter, r *http.Request, code int, msg string) {
	p.json(w, r, code, map[string]string{"error": msg}, nil)
}

var webDir = sync.OnceValue(func() string {
	// 生产：/var/lib/nokeyout/app/web（setup.sh 装的 web/ 就在这）；开发回落到源码树
	if _, err := os.Stat("/var/lib/nokeyout/app/web/login.html"); err == nil {
		return "/var/lib/nokeyout/app/web"
	}
	return "web"
})()

func (p *panel) page(w http.ResponseWriter, r *http.Request, name string, code int, extra [][2]string) {
	b, err := os.ReadFile(webDir + "/" + name)
	if err != nil {
		p.send(w, r, 404, []byte("page not found"), "text/html; charset=utf-8", extra)
		return
	}
	p.send(w, r, code, b, "text/html; charset=utf-8", extra)
}

func (p *panel) redirect(w http.ResponseWriter, r *http.Request, loc string) {
	w.Header().Set("Location", loc)
	for _, kv := range secHeaders {
		w.Header().Set(kv[0], kv[1])
	}
	w.WriteHeader(302)
}

func (p *panel) body(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(r.Body, maxBody))
	return b
}

func (p *panel) bodyJSON(r *http.Request) map[string]any {
	if r.ContentLength <= 0 || r.ContentLength > maxJSONBody {
		return map[string]any{}
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, maxJSONBody))
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return map[string]any{}
	}
	return m
}

func (p *panel) sessionCookie(r *http.Request) string {
	for _, part := range strings.Split(r.Header.Get("Cookie"), ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "nk_sid=") {
			return strings.TrimPrefix(part, "nk_sid=")
		}
	}
	return ""
}

func (p *panel) user(r *http.Request) string {
	return p.sess.user(p.sessionCookie(r), p.cfg.getSecret())
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---------------------------------------------------------------- main

func mainCLI() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("nokeyout-panel ")

	// 无屏/服务器形态的一键锁（不启动服务）
	if len(os.Args) > 1 && os.Args[1] == "lock" {
		os.Exit(cmdLock(os.Args[2:]))
	}
	// 屏幕门：pass=放行屏幕，revoke=重新熄屏，status=查状态（不启动服务）
	if len(os.Args) > 1 && os.Args[1] == "screen-gate" {
		os.Exit(cmdScreenGate(os.Args[2:]))
	}
	// 安装阶段接管小屏：画一行标题/副标题/进度，然后退出（不启动服务）
	if len(os.Args) > 1 && os.Args[1] == "screen-msg" {
		os.Exit(cmdScreenMsg(os.Args[2:]))
	}

	p := newPanel()
	panelInstance = p // screen_ctl 用（screensDir 等进程级查询）

	// 拒绝无 TLS 启动（调试除外）
	if p.cfg.tlsCert == "" || p.cfg.tlsKey == "" {
		if !p.cfg.allowPlainHTTP {
			log.SetPrefix("")
			fmt.Fprint(os.Stderr,
				"拒绝启动：未配置 TLS。设 PANEL_ALLOW_INSECURE_HTTP=1 仅限本机调试。\n"+
					"签发自签证书：\n"+
					"  sudo nokeyout-cert --ip <设备IP>\n")
			os.Exit(2)
		}
	}

	p.stats.start()
	startNetmon(cfgGet("NET_IF", "auto")) // 屏头 ETH/NET 指示灯的数据源（auto=默认路由接口）

	// 屏幕（可选组件，SCREEN_BACKEND=console|st7789|png|web；默认关）
	if ms, err := strconv.Atoi(cfgGet("SCREEN_TICK_MS", "33")); err == nil && ms >= 20 && ms <= 2000 {
		screenTick = time.Duration(ms) * time.Millisecond
	}
	p.startScreen()
	p.startUserButton()

	swSrv := serveSwitchd("127.0.0.1", 8799)
	go func() {
		if err := swSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("switchd 8799: %v", err)
		}
	}()

	scheme := "http"
	if p.cfg.tlsCert != "" && p.cfg.tlsKey != "" {
		scheme = "https"
	}
	log.Printf("%s on %s://%s:%d (switch=%s, gate=%s)", Version, scheme, p.cfg.host, p.cfg.port,
		switchState(p.cfg), p.cfg.gateMode)

	mux := http.NewServeMux()
	mux.HandleFunc("/", p.route)

	if p.cfg.gateMode == "process" {
		go p.gateLoop(mux)
	} else {
		go func() {
			srv := p.httpServer(mux)
			if err := listenServe(srv); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("listen: %v", err)
			}
		}()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if curSrv != nil {
		_ = curSrv.Shutdown(ctx)
	}
}

// curSrv：当前管理面监听（gate 切换时替换）
var (
	curSrv   *http.Server
	curSrvMu sync.Mutex
)

func (p *panel) httpServer(mux *http.ServeMux) *http.Server {
	return &http.Server{
		Addr:              net.JoinHostPort(p.cfg.host, strconv.Itoa(p.cfg.port)),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

func listenServe(srv *http.Server) error {
	if srv.TLSConfig != nil || hasTLS() {
		return srv.ListenAndServeTLS(tlsCertPath(), tlsKeyPath())
	}
	return srv.ListenAndServe()
}

func hasTLS() bool { return tlsCertPath() != "" && tlsKeyPath() != "" }

func tlsCertPath() string { return panelCfg.tlsCert }
func tlsKeyPath() string  { return panelCfg.tlsKey }

var panelCfg = loadCfg()

// gateLoop：进程级门控。locked → 关闭监听（新连接直接拒）；unlocked → 重新绑定。
func (p *panel) gateLoop(mux *http.ServeMux) {
	for {
		if switchState(p.cfg) == "locked" {
			curSrvMu.Lock()
			if curSrv != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = curSrv.Shutdown(ctx)
				cancel()
				curSrv = nil
				//  。
				p.rotateSessionSecret()
				log.Print("panel: switch=LOCKED → 监听已关闭（门控）")
			}
			curSrvMu.Unlock()
			time.Sleep(time.Duration(p.cfg.switchPoll * float64(time.Second)))
			continue
		}
		curSrvMu.Lock()
		if curSrv == nil {
			srv := p.httpServer(mux)
			curSrv = srv
			go func() {
				if err := listenServe(srv); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Printf("listen: %v", err)
				}
			}()
			log.Print("panel: switch=UNLOCKED → 监听已开启")
		}
		curSrvMu.Unlock()
		time.Sleep(time.Duration(p.cfg.switchPoll * float64(time.Second)))
	}
}
