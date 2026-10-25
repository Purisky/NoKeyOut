//go:build !(js && wasm)

package main

// screen_backend_web.go —— 虚拟屏：把"设备屏"映射成 PC 上可看的 HTTP 端点
//
// 一块内存里的"虚拟屏"：pushRect 打进来的每一块都贴进去，因此内容与真机逐像素一致
// （和 SCREEN_BACKEND=png 同一份帧缓冲逻辑，只是出口从文件变成 HTTP）。
//
//   GET /screen            调试页（pixelated 放大的 <img>，MJPEG 直出）
//   GET /screen/frame.png  当前整幅帧（curl/脚本友好）
//   WS   /screen/ws        原始 RGB565 帧推送（canvas 渲染，零编解码）
//   GET /screen/state      调试遥测：页名/语言/推送次数/上次推送字节/版本/帧龄
//
// 门控：随 panel 路由走会话鉴权（LOCKED 时监听关闭 → 同管理面一起不可达，与产品
// 闸门语义一致；要单独看锁定页用 console/png 后端）。

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// shadowScreen：虚拟屏设备（实现 screenDev）。dir 非空时同时落盘 frame_NNNNNN.png。
type shadowScreen struct {
	dir      string
	scale    int
	mu       sync.Mutex
	fb       []byte // 虚拟屏（线上字节序）
	ver      atomic.Int64
	pushes   atomic.Int64
	lastByte atomic.Int64
	page     atomic.Value // string
	lang     atomic.Value // string
	updated  atomic.Int64 // unix nano
	wantFile atomic.Bool  // 有待落盘帧（异步写）
}


func newShadowScreen(dir string, scale int) *shadowScreen {
	if scale < 1 {
		scale = 1
	}
	s := &shadowScreen{dir: dir, scale: scale}
	s.page.Store("")
	s.lang.Store("")
	if dir != "" {
		go s.fileWriter() // 落盘异步化
	}
	return s
}

func (s *shadowScreen) pushRect(x0, y0, x1, y1 int, data []byte) error {
	if s.fb == nil {
		s.fb = make([]byte, screenW*screenH*2)
	}
	s.mu.Lock()
	w := x1 - x0 + 1
	for y := y0; y <= y1; y++ {
		src := data[(y-y0)*w*2 : (y-y0+1)*w*2]
		dst := (y*screenW + x0) * 2
		copy(s.fb[dst:dst+w*2], src)
	}
	s.mu.Unlock()
	s.ver.Add(1)
	s.pushes.Add(1)
	s.lastByte.Store(int64(len(data)))
	s.updated.Store(time.Now().UnixNano())
	s.wantFile.Store(true)
	return nil
}

// fileWriter：异步落盘（编码 480² PNG 约几 ms，但 10fps 节拍下不该占渲染路径；
// 忙则丢中间帧——只保证最终收敛到最新画面）
func (s *shadowScreen) fileWriter() {
	for {
		if !s.wantFile.Swap(false) {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		n := int(s.pushes.Load())
		img := s.scaled(s.image())
		if img == nil {
			continue
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			continue
		}
		_ = os.WriteFile(fmt.Sprintf("%s/frame_%06d.png", s.dir, n), buf.Bytes(), 0o644)
	}
}

func (s *shadowScreen) setPage(page, lang string) {
	s.page.Store(page)
	s.lang.Store(lang)
}

func (s *shadowScreen) image() *image.RGBA {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fb == nil {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, screenW, screenH))
	for y := range screenH {
		for x := range screenW {
			i := (y*screenW + x) * 2
			r, g, b := rgb565expand(uint16(s.fb[i])<<8 | uint16(s.fb[i+1]))
			img.SetRGBA(x, y, color.RGBA{r, g, b, 0xFF})
		}
	}
	return img
}

func (s *shadowScreen) scaled(img *image.RGBA) *image.RGBA {
	if s.scale <= 1 || img == nil {
		return img
	}
	big := image.NewRGBA(image.Rect(0, 0, screenW*s.scale, screenH*s.scale))
	for y := range screenH * s.scale {
		for x := range screenW * s.scale {
			big.SetRGBA(x, y, img.RGBAAt(x/s.scale, y/s.scale))
		}
	}
	return big
}

func (s *shadowScreen) frameRaw() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fb == nil {
		return nil
	}
	return append([]byte(nil), s.fb...)
}

func (s *shadowScreen) framePNG() []byte {
	img := s.scaled(s.image())
	if img == nil {
		return nil
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func (s *shadowScreen) frameJPEG() []byte {
	img := s.image()
	if img == nil {
		return nil
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	return buf.Bytes()
}

func (s *shadowScreen) writePNG(n int) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, s.scaled(s.image())); err != nil {
		return
	}
	_ = os.WriteFile(fmt.Sprintf("%s/frame_%06d.png", s.dir, n), buf.Bytes(), 0o644)
}

func (s *shadowScreen) state() map[string]any {
	age := float64(time.Now().UnixNano()-s.updated.Load()) / 1e9
	if s.updated.Load() == 0 {
		age = -1
	}
	page, _ := s.page.Load().(string)
	lang, _ := s.lang.Load().(string)
	ctlLang, _ := screenctl.langOverride.Load().(string)
	return map[string]any{
		"backend":         "web",
		"page":            page,
		"lang":            lang,
		"lang_override":   ctlLang,
		"error_injected":  screenctl.injected(),
		"pushes":          s.pushes.Load(),
		"last_push_bytes": s.lastByte.Load(),
		"version":         s.ver.Load(),
		"updated_age_s":   age,
	}
}

// ---------------------------------------------------------------- HTTP（panel 路由调）

func (p *panel) screenHandlers(w http.ResponseWriter, r *http.Request, path string) {
	sc := p.screen
	if sc == nil {
		p.errJSON(w, r, 404, "screen backend not enabled（SCREEN_BACKEND=web 或 png）")
		return
	}
	switch path {
	case "/screen":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(screenPageHTML()))
	case "/screen/frame.png":
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(sc.framePNG())
	case "/screen/state":
		p.json(w, r, 200, sc.state(), nil)
	default:
		p.errJSON(w, r, 404, "unknown screen path")
	}
}

func screenPageHTML() string {
	page := `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>NoKeyOut · 虚拟屏</title>
<style>
  body { font: 13px/1.5 ui-monospace, Menlo, Consolas, monospace; margin: 0; padding: 16px;
         background: #0e1117; color: #e8eef4; }
  .wrap { display: flex; gap: 20px; align-items: flex-start; justify-content: center; flex-wrap: wrap; }
  .col { display: flex; flex-direction: column; gap: 8px; align-items: center; }
  /* 边框放在外层容器上：canvas 元素本身刚好是 480/240 px，便于对照真实像素尺寸 */
  .frame { border: 1px solid #2c3848; border-radius: 8px; overflow: hidden; line-height: 0; }
  canvas { display: block; image-rendering: pixelated; background: #081010; }
  /* 大图 2× 放大：看细节（整数倍放大，不糊） */
  #big { width: {{W2}}px; height: {{H2}}px; }
  /* 小图 1× = 240×240 CSS px：屏上的真实尺寸，用来看实际视觉效果与信息密度 */
  #small { width: {{W}}px; height: {{H}}px; }
  .cap { color: #8a97a8; font-size: 12px; }
  .bar { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; margin-bottom: 4px; }
  button { font: inherit; padding: 4px 10px; border-radius: 6px; cursor: pointer;
           border: 1px solid #2c3848; background: transparent; color: inherit; }
  .muted { color: #8a97a8; font-size: 12px; }
  #st { max-width: 460px; }
</style>
</head>
<body>
<div class="wrap">
  <div class="col">
    <div class="bar">
      <b>虚拟屏</b>
      <button onclick="ctl('lang')">中/EN</button>
      <button onclick="ctl('error')">注入统计异常</button>
      <button onclick="ctl('lock')">模拟锁定(仅画面)</button>
    </div>
    <div class="frame"><canvas id="big" width="{{W}}" height="{{H}}"></canvas></div>
    <div class="cap">2× 放大 · 看细节（与屏渲染线程同一份帧，WebSocket 原始 RGB565，零编解码）</div>
    <div class="cap muted" id="st">…</div>
  </div>
  <div class="col">
    <div class="cap">真实尺寸 {{W}}×{{H}}</div>
    <div class="frame"><canvas id="small" width="{{W}}" height="{{H}}"></canvas></div>
    <div class="cap">这个方块就是设备屏上的实际大小 · 模块选择在 管理面 → 系统 → 屏幕模块</div>
  </div>
</div>
<script>
async function tick() {
  try {
    const s = await (await fetch('/screen/state')).json();
    document.getElementById('st').textContent =
      "语言=" + s.lang + " 推送=" + s.pushes +
      " 上次=" + s.last_push_bytes + "B 版本=" + s.version +
      "";  // 数据龄不再显示（它是采样节奏，不是信息；新鲜度只用于"数据过期"告警）
  } catch {}
}
setInterval(tick, 1000); tick();
// WS 原始帧：115,200B RGB565(高字节在前) → RGBA → 同时贴到两块 canvas（一次解码，两处显示）
(function ws() {
  const big = document.getElementById('big').getContext('2d');
  const small = document.getElementById('small').getContext('2d');
  const img = big.createImageData({{W}}, {{H}}), d = img.data;
  // 5/6 位 → 8 位按 255/31、255/63 精确展开（<<3 会偏暗，看不出真实观感）
  const e5 = new Uint8Array(32), e6 = new Uint8Array(64);
  for (let i = 0; i < 32; i++) e5[i] = Math.round(i * 255 / 31);
  for (let i = 0; i < 64; i++) e6[i] = Math.round(i * 255 / 63);
  const proto = location.protocol === 'https:' ? 'wss://' : 'ws://';
  function connect() {
    const s = new WebSocket(proto + location.host + '/screen/ws');
    s.binaryType = 'arraybuffer';
    s.onmessage = ev => {
      const u8 = new Uint8Array(ev.data);
      if (u8.length < {{BYTES}}) return;
      let o = 0;
      for (let i = 0; i < {{W}} * {{H}}; i++) {
        const v = (u8[o] << 8) | u8[o + 1]; o += 2;
        d[i * 4]     = e5[(v >> 11) & 0x1F];
        d[i * 4 + 1] = e6[(v >> 5) & 0x3F];
        d[i * 4 + 2] = e5[v & 0x1F];
        d[i * 4 + 3] = 255;
      }
      big.putImageData(img, 0, 0);
      small.putImageData(img, 0, 0);
    };
    s.onclose = () => setTimeout(connect, 1000);
  }
  connect();
})();
// 调试控制： POST /screen/ctl {action: lang|error|lock}
async function ctl(action) {
  try { await fetch('/screen/ctl', { method: 'POST',
    headers: {'Content-Type': 'application/json'}, body: JSON.stringify({ action }) }); } catch (e) {}
  tick();
}
</script>
</body>
</html>`
	return strings.NewReplacer(
		"{{W}}", strconv.Itoa(screenW),
		"{{H}}", strconv.Itoa(screenH),
		"{{W2}}", strconv.Itoa(screenW*2),
		"{{H2}}", strconv.Itoa(screenH*2),
		"{{BYTES}}", strconv.Itoa(screenW*screenH*2),
	).Replace(page)
}
