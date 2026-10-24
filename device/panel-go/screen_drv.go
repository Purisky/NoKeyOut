//go:build !(js && wasm)

package main

// screen_drv.go —— 直操纵像素：外部驱动通过 draw-op API 完全接管屏幕
//
// 但 ST7789 的意义就是自由画屏。这里把画布与设备指针共享出来，外部（任意语言，
// curl 都行）按批次下 draw-op，panel 负责执行 + 推屏；manual 模式下内置轮播让位。
//
// 安全：会话门控（与 /screen 一致）；body ≤256KB；字体 charset ≤4096 字；
// **不执行任何用户代码**——只执行白名单原语（和内置页用的是同一套 Canvas 方法）。

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type screenControl struct {
	mu     sync.Mutex
	canvas *Canvas
	dev    screenDev
	manual atomic.Bool
	fonts  map[string]*Font // 用户上传字体（1bpp）
	ops    atomic.Int64
	ver    atomic.Int64
	ticks  atomic.Int64 // 诊断用：渲染循环真实节拍数
}

var scr = &screenControl{fonts: map[string]*Font{}}

func (s *screenControl) ensure(w, h int) {
	if s.canvas == nil {
		s.canvas = newCanvas(w, h)
	}
}

func (s *screenControl) flushLocked() {
	if s.dev == nil || s.canvas == nil {
		return
	}
	s.canvas.flush(s.dev)
	s.ver.Add(1)
}

// ---------------------------------------------------------------- 颜色解析
// 接受 0xRRGGBB / "#RRGGBB" / [r,g,b] / 十进制 565 整数（0..65535）。

func parseColor(v any) (uint16, bool) {
	switch x := v.(type) {
	case float64:
		if x >= 0 && x <= 0xFFFF {
			return uint16(x), true
		}
	case json.Number:
		n, err := x.Int64()
		if err == nil && n >= 0 && n <= 0xFFFF {
			return uint16(n), true
		}
	case string:
		s := strings.TrimPrefix(strings.TrimSpace(x), "#")
		if len(s) == 6 {
			if n, err := strconv.ParseUint(s, 16, 32); err == nil {
				r, g, b := (n>>16)&0xFF, (n>>8)&0xFF, n&0xFF
				return rgb565(uint8(r), uint8(g), uint8(b)), true
			}
		}
		if n, err := strconv.ParseUint(s, 16, 32); err == nil && n <= 0xFFFF {
			return uint16(n), true
		}
	case []any:
		if len(x) == 3 {
			c := make([]uint8, 3)
			for i, e := range x {
				f, ok := toF(e)
				if !ok || f < 0 || f > 255 {
					return 0, false
				}
				c[i] = uint8(f)
			}
			return rgb565(c[0], c[1], c[2]), true
		}
	}
	return 0, false
}

func num(m map[string]any, k string, def float64) float64 {
	if v, ok := m[k]; ok {
		if f, ok2 := toF(v); ok2 {
			return f
		}
	}
	return def
}

// ---------------------------------------------------------------- draw-op 执行

type drawReq struct {
	Ops  []map[string]any `json:"ops"`
	Font map[string]any   `json:"font"` // 可选：随批上传字体
}

// applyOps：在锁内执行一批 op；批末自动 flush（除非 op 里显式 {"op":"noflush"}）。
func (s *screenControl) applyOps(ops []map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensure(screenW, screenH)
	autoflush := true
	for i, op := range ops {
		kind, _ := op["op"].(string)
		if kind == "noflush" {
			autoflush = false
			continue
		}
		x, y := int(num(op, "x", 0)), int(num(op, "y", 0))
		w, h := int(num(op, "w", 0)), int(num(op, "h", 0))
		col, _ := parseColor(op["color"])
		switch kind {
		case "clear":
			s.canvas.clear(col)
		case "px":
			s.canvas.pixel(x, y, col)
		case "rect":
			if b, _ := op["fill"].(bool); b {
				s.canvas.fillRect(x, y, w, h, col)
			} else {
				s.canvas.rect(x, y, w, h, col)
			}
		case "hline":
			s.canvas.hline(x, int(num(op, "x2", float64(x+w))), y, col)
		case "vline":
			s.canvas.vline(x, y, int(num(op, "y2", float64(y+h))), col)
		case "line":
			s.canvas.line(x, y, int(num(op, "x2", 0)), int(num(op, "y2", 0)), col)
		case "circle":
			if b, _ := op["fill"].(bool); b {
				s.canvas.circle(x, y, int(num(op, "r", 0)), col, true)
			} else {
				s.canvas.circle(x, y, int(num(op, "r", 0)), col, false)
			}
		case "arc":
			a0 := num(op, "a0", 0) * math.Pi / 180
			a1 := num(op, "a1", 0) * math.Pi / 180
			s.canvas.arc(x, y, int(num(op, "r", 0)), a0, a1, col, int(num(op, "thick", 1)))
		case "text":
			text, _ := op["text"].(string)
			fname, _ := op["font"].(string)
			// 内置字体（sm/md/num/cjk）与用户上传字体同一个命名空间查；
			// 查不到才回落 SM（并让调用方从 state 的 fonts 列表自查）。
			if f := fonts[fname]; f != nil {
				f.draw(s.canvas, x, y, text, col)
			} else if f := s.fonts[fname]; f != nil {
				f.draw(s.canvas, x, y, text, col)
			} else {
				SM.draw(s.canvas, x, y, text, col)
			}
		case "bitmap":
			data, err := decodeHex(op["data"])
			if err != nil {
				return fmt.Errorf("op %d: bitmap data: %w", i, err)
			}
			rb := (w + 7) / 8
			s.canvas.bitmap(x, y, data, w, rb, h, col)
		default:
			return fmt.Errorf("op %d: 未知类型 %q", i, kind)
		}
	}
	s.ops.Add(int64(len(ops)))
	if autoflush {
		s.flushLocked()
	}
	return nil
}

func decodeHex(v any) ([]byte, error) {
	s, _ := v.(string)
	s = strings.TrimSpace(strings.ReplaceAll(s, " ", ""))
	if s == "" {
		return nil, fmt.Errorf("empty")
	}
	return hex.DecodeString(s)
}

// registerFont：上传 1bpp 字模（行位图，高位在左；与 screen_gen.go 同布局）。
func (s *screenControl) registerFont(m map[string]any) error {
	name, _ := m["name"].(string)
	if name == "" || len(name) > 24 {
		return fmt.Errorf("font.name 必填且 ≤24")
	}
	if _, ok := fonts[name]; ok {
		return fmt.Errorf("字体名与内置冲突：%s", name)
	}
	if _, ok := s.fonts[name]; ok && m["overwrite"] != true {
		return fmt.Errorf("字体 %s 已存在（overwrite=true 才覆盖）", name)
	}
	charset, _ := m["charset"].(string)
	if charset == "" || len([]rune(charset)) > 4096 {
		return fmt.Errorf("font.charset 必填且 ≤4096 字符")
	}
	data, err := decodeHex(m["data"])
	if err != nil {
		return fmt.Errorf("font.data: %w", err)
	}
	w := int(num(m, "w", 8))
	h := int(num(m, "h", 16))
	if w < 1 || h < 1 || w > 128 || h > 128 {
		return fmt.Errorf("font.w/h 需在 1..128")
	}
	rows := (w + 7) / 8
	stride := rows * h
	rs := []rune(charset)
	if len(data) != stride*len(rs) {
		return fmt.Errorf("font.data 长度 %d ≠ %d×%d×%d（字模数需与 charset 一致）",
			len(data), len(rs), rows, h)
	}
	f := &Font{
		Key: name, H: h, Adv: int(num(m, "adv", float64(w+1))),
		Variable: false, Ascent: 0, // 用户字体统一 y=top，简单可预期
		data: data, tofu: -1,
	}
	f.idx = make(map[rune]int, len(rs))
	f.widths = make([]int, len(rs))
	f.offs = make([]int, len(rs))
	f.rowbytes = make([]int, len(rs))
	for i, r := range rs {
		f.idx[r] = i
		f.widths[i] = w
		f.offs[i] = i * stride
		f.rowbytes[i] = rows
	}
	s.mu.Lock()
	s.fonts[name] = f
	s.mu.Unlock()
	return nil
}

// ---------------------------------------------------------------- 帧读取

func (s *screenControl) framePNGBytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.canvas == nil {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, s.canvas.W, s.canvas.H))
	for y := range s.canvas.H {
		for x := range s.canvas.W {
			i := (y*s.canvas.W + x) * 2
			r, g, b := rgb565expand(uint16(s.canvas.Buf[i])<<8 | uint16(s.canvas.Buf[i+1]))
			img.SetRGBA(x, y, color.RGBA{r, g, b, 0xFF})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func (s *screenControl) frameRaw() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.canvas == nil {
		return nil
	}
	return append([]byte(nil), s.canvas.Buf...)
}

func (s *screenControl) state() map[string]any {
	fonts := []string{}
	s.mu.Lock()
	for n := range s.fonts {
		fonts = append(fonts, n)
	}
	s.mu.Unlock()
	return map[string]any{
		"mode": func() string {
			if s.manual.Load() {
				return "manual"
			}
			return "auto"
		}(),
		"ops":     s.ops.Load(),
		"ticks":   s.ticks.Load(),
		"pushes":  s.ver.Load(),
		"fonts":   fonts,
		"updated": time.Now().Unix(),
	}
}

// ---------------------------------------------------------------- HTTP（panel 路由调）

func (p *panel) screenDrvHandler(w http.ResponseWriter, r *http.Request, rest string) {
	scr.ensure(screenW, screenH)
	scr.mu.Lock()
	scr.dev = p.screen // 共享屏后端（web/png/st7789 任一）
	scr.mu.Unlock()
	switch rest {
	case "/draw":
		var body drawReq
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&body); err != nil {
			p.errJSON(w, r, 400, "bad json: "+err.Error())
			return
		}
		if body.Font != nil {
			if err := scr.registerFont(body.Font); err != nil {
				p.errJSON(w, r, 400, err.Error())
				return
			}
		}
		if len(body.Ops) > 0 {
			if err := scr.applyOps(body.Ops); err != nil {
				p.errJSON(w, r, 400, err.Error())
				return
			}
		}
		p.json(w, r, 200, map[string]any{"ok": true, "ops": scr.ops.Load()}, nil)
	case "/font":
		var body struct {
			Font map[string]any `json:"font"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512*1024)).Decode(&body); err != nil {
			p.errJSON(w, r, 400, "bad json: "+err.Error())
			return
		}
		if err := scr.registerFont(body.Font); err != nil {
			p.errJSON(w, r, 400, err.Error())
			return
		}
		p.json(w, r, 200, map[string]any{"ok": true}, nil)
	case "/mode":
		var body struct {
			Mode string `json:"mode"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch body.Mode {
		case "manual":
			scr.manual.Store(true)
		case "auto":
			scr.manual.Store(false)
		default:
			p.errJSON(w, r, 400, "mode 需 manual|auto")
			return
		}
		p.json(w, r, 200, map[string]any{"ok": true, "mode": body.Mode}, nil)
	case "/frame":
		if r.URL.Query().Get("raw") == "1" {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(scr.frameRaw())
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(scr.framePNGBytes())
	case "/state":
		p.json(w, r, 200, scr.state(), nil)
	default:
		p.errJSON(w, r, 404, "unknown screen op")
	}
}
