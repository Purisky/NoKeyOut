package main

// screen_fb.go —— 240×240 RGB565 画布 + 绘图原语 + 帧间差分

import (
	"bytes"
	"log"
	"math"
)

// 调色板（16 位 RGB565）
var (
	screenBG        = rgb565(14, 17, 23)
	screenCard      = rgb565(22, 28, 38)
	screenCard2     = rgb565(30, 38, 52)
	screenBorder    = rgb565(44, 55, 72)
	screenFG        = rgb565(232, 238, 244)
	screenMuted     = rgb565(138, 152, 168)
	screenAccent    = rgb565(76, 141, 255)
	screenAccentDim = rgb565(34, 66, 122) // 趋势/次要图形：压暗，避免与状态色抢眼
	screenOK        = rgb565(63, 178, 127)
	screenWarn      = rgb565(229, 163, 60)
	screenDanger    = rgb565(229, 83, 75)
	screenLockBG    = rgb565(36, 14, 16)
	// ADMIN 访问页：OpenDesign 方案的单一青色强调（与 LOCK 页的蓝色区分状态），
	// 白色只用于产品标识与文字；红色只在超过 15 分钟阈值后出现。
	adminAccent      = rgb565(40, 196, 178)
	adminAccentTrack = rgb565(18, 46, 48)
	adminBarTrack    = rgb565(30, 37, 44)
	screenDim        = rgb565(58, 68, 84)  // 未连接/未知（暗点）
	screenDangerDim  = rgb565(122, 44, 42) // 锁定脉冲暗相位
	// 周期档位色（5h 青 / 7d 蓝 / 月 紫）：标签色只做**类型区分**，绝不与
	// 语义色（OK 绿 / WARN 黄 / DANGER 红）撞脸——否则「5h」的绿和
	screenWinShort = rgb565(94, 199, 214)  // 青
	screenWinMid   = rgb565(111, 160, 255) // 蓝
	screenWinLong  = rgb565(192, 140, 255) // 紫
)

// rgb565expand：RGB565 → 8bit（与设备端/黄金样本同一映射）。
func rgb565expand(v uint16) (r, g, b uint8) {
	r5, g6, b5 := (v>>11)&0x1F, (v>>5)&0x3F, v&0x1F
	return uint8(r5<<3 | r5>>2), uint8(g6<<2 | g6>>4), uint8(b5<<3 | b5>>2)
}

func rgb565(r, g, b uint8) uint16 {
	return (uint16(r&0xF8) << 8) | (uint16(g&0xFC) << 3) | uint16(b>>3)
}

// screenDev：屏幕后端接口（真机 SPI / 虚拟屏 / WASM 不需要）。flush 用到。
type screenDev interface {
	pushRect(x0, y0, x1, y1 int, data []byte) error
}

// Canvas：W×H，Buf 为「线上字节序」帧缓冲（每像素 2 字节、高字节在前，即 ST7789 RAMWR 顺序）。
// prev 为已推送的上一帧，dirty() 逐行比对求最小差异矩形。
type Canvas struct {
	W, H int
	Buf  []byte
	prev []byte
}

func newCanvas(w, h int) *Canvas {
	c := &Canvas{W: w, H: h, Buf: make([]byte, w*h*2)}
	c.prev = append([]byte(nil), c.Buf...)
	return c
}

func (c *Canvas) clear(color uint16) {
	hi, lo := byte(color>>8), byte(color)
	c.Buf[0], c.Buf[1] = hi, lo
	for i := 2; i < len(c.Buf); i *= 2 {
		copy(c.Buf[i:], c.Buf[:i])
	}
}

// invalidate：让下一帧 flush 推送整幅。刚上电/刚唤醒时物理屏 RAM 的内容
// 未必等于画布以为的（prev），差分推屏会漏掉其余区域。
func (c *Canvas) invalidate() {
	for i := range c.prev {
		c.prev[i] = ^c.Buf[i]
	}
}

func (c *Canvas) pixel(x, y int, color uint16) {
	if uint(x) < uint(c.W) && uint(y) < uint(c.H) {
		i := (y*c.W + x) * 2
		c.Buf[i] = byte(color >> 8)
		c.Buf[i+1] = byte(color)
	}
}

// blitFrom：把 src 整幅贴到当前画布的 (0, dy)。用于"先在离屏画布按偏移排好，
// 再整块贴上"——离屏画布自带边界裁剪，滚出视口的行会被自动切掉。
func (c *Canvas) blitFrom(dy int, src *Canvas) {
	if src.W != c.W {
		return
	}
	row := src.W * 2
	for y := 0; y < src.H; y++ {
		ty := dy + y
		if ty < 0 || ty >= c.H {
			continue
		}
		copy(c.Buf[ty*row:ty*row+row], src.Buf[y*row:y*row+row])
	}
}

func (c *Canvas) fillRect(x, y, w, h int, color uint16) {
	if x < 0 {
		w += x
		x = 0
	}
	if y < 0 {
		h += y
		y = 0
	}
	if w > c.W-x {
		w = c.W - x
	}
	if h > c.H-y {
		h = c.H - y
	}
	if w <= 0 || h <= 0 {
		return
	}
	row := bytes.Repeat([]byte{byte(color >> 8), byte(color)}, w)
	stride := c.W * 2
	for yy := y; yy < y+h; yy++ {
		copy(c.Buf[yy*stride+x*2:], row)
	}
}

func (c *Canvas) rect(x, y, w, h int, color uint16) {
	c.hline(x, x+w-1, y, color)
	c.hline(x, x+w-1, y+h-1, color)
	c.vline(x, y, y+h-1, color)
	c.vline(x+w-1, y, y+h-1, color)
}

func (c *Canvas) hline(x0, x1, y int, color uint16) {
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	c.fillRect(x0, y, x1-x0+1, 1, color)
}

func (c *Canvas) vline(x, y0, y1 int, color uint16) {
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	c.fillRect(x, y0, 1, y1-y0+1, color)
}

func (c *Canvas) line(x0, y0, x1, y1 int, color uint16) {
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := 1, 1
	if x0 >= x1 {
		sx = -1
	}
	if y0 >= y1 {
		sy = -1
	}
	err := dx + dy
	for {
		c.pixel(x0, y0, color)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func (c *Canvas) circle(cx, cy, r int, color uint16, fill bool) {
	if r <= 0 {
		return
	}
	x, y, err := r, 0, 1-r
	for x >= y {
		if fill {
			c.hline(cx-x, cx+x, cy+y, color)
			c.hline(cx-y, cx+y, cy+x, color)
			c.hline(cx-x, cx+x, cy-y, color)
			c.hline(cx-y, cx+y, cy-x, color)
		} else {
			for _, p := range [][2]int{{x, y}, {y, x}, {-x, y}, {-y, x}, {x, -y}, {y, -x}, {-x, -y}, {-y, -x}} {
				c.pixel(cx+p[0], cy+p[1], color)
			}
		}
		y++
		if err < 0 {
			err += 2*y + 1
		} else {
			x--
			err += 2*(y-x) + 1
		}
	}
}

func (c *Canvas) arc(cx, cy, r int, a0, a1 float64, color uint16, thick int) {
	if r <= 0 || thick <= 0 {
		return
	}
	steps := int(math.Abs(a1-a0) * float64(r) * 2)
	if steps < 1 {
		steps = 1
	}
	for i := range steps + 1 {
		a := a0 + (a1-a0)*float64(i)/float64(steps)
		for t := range thick {
			rr := float64(r - t)
			if rr <= 0 {
				continue
			}
			c.pixel(int(math.Round(float64(cx)+rr*math.Cos(a))),
				int(math.Round(float64(cy)+rr*math.Sin(a))), color)
		}
	}
}

// 5bit/6bit → 8bit 展开表：AA 混合必须在**统一的 8bit 空间**里算，
// 重新打包时绿又 >>2 —— 绿色被压到 1/4，白字一律渲染成洋红）。
var (
	exp5 [32]int
	exp6 [64]int
)

func init() {
	for i := range exp5 {
		exp5[i] = i * 255 / 31
	}
	for i := range exp6 {
		exp6[i] = i * 255 / 63
	}
}

// bitmapAA：2bpp 覆盖度位图（每字节 4 像素、每像素 2 位、高位在前）以
// 4 级（0/25/50/100%）**真 alpha 合成**画到 (x,y)：out = bg + (fg-bg)*cov/255。
// 要读回帧缓冲里的背景像素——所以背景是卡片色/渐变都正确（与固定底色近似不同）。
func (c *Canvas) bitmapAA(x, y int, cov []byte, w, rb, rows int, color uint16) {
	fr, fgn, fb := exp5[(color>>11)&0x1F], exp6[(color>>5)&0x3F], exp5[color&0x1F]
	blend := func(bgCh, fgCh, lev int) int {
		return bgCh + (fgCh-bgCh)*lev/255
	}
	stride := c.W * 2
	inCanvas := x >= 0 && y >= 0 && x+w <= c.W && y+rows <= c.H
	for gy := range rows {
		src := cov[gy*rb : gy*rb+rb]
		if allZero(src) {
			continue
		}
		for bi, bv := range src {
			if bv == 0 {
				continue
			}
			for k := range 4 {
				px := bi*4 + k
				if px >= w {
					break
				}
				lvl := (bv >> (6 - 2*uint(k))) & 3
				if lvl == 0 {
					continue
				}
				lev := int(lvl) * 85 // 1→85 2→170 3→255
				xx, yy := x+px, y+gy
				if !inCanvas && (xx < 0 || yy < 0 || xx >= c.W || yy >= c.H) {
					continue
				}
				off := yy*stride + xx*2
				if off+1 >= len(c.Buf) {
					continue
				}
				bg := uint16(c.Buf[off])<<8 | uint16(c.Buf[off+1])
				r := blend(exp5[(bg>>11)&0x1F], fr, lev)
				g := blend(exp6[(bg>>5)&0x3F], fgn, lev)
				b := blend(exp5[bg&0x1F], fb, lev)
				out := uint16((r>>3)<<11 | (g>>2)<<5 | b>>3)
				c.Buf[off] = byte(out >> 8)
				c.Buf[off+1] = byte(out)
			}
		}
	}
}

// bitmap：1bpp 行位图（每字节 8 列、高位在左）以 color 画到 (x,y)。
// bitmapScale：1bpp 掩码按整数倍放大（每个墨点画成 scale×scale 方块）。
// 用于把 11px 字模放大成大字标；保持像素边缘，不做插值。
func (c *Canvas) bitmapScale(x, y int, blob []byte, w, rb, rows int, color uint16, scale int) {
	if scale <= 1 {
		c.bitmap(x, y, blob, w, rb, rows, color)
		return
	}
	for row := 0; row < rows; row++ {
		for col := 0; col < w; col++ {
			if blob[row*rb+col/8]&(0x80>>(col%8)) == 0 {
				continue
			}
			c.fillRect(x+col*scale, y+row*scale, scale, scale, color)
		}
	}
}

func (c *Canvas) bitmap(x, y int, blob []byte, w, rb, rows int, color uint16) {
	hi, lo := byte(color>>8), byte(color)
	inCanvas := x >= 0 && y >= 0 && x+w <= c.W && y+rows <= c.H
	if inCanvas {
		stride := c.W * 2
		for gy := range rows {
			src := blob[gy*rb : gy*rb+rb]
			if allZero(src) {
				continue
			}
			base := (y+gy)*stride + x*2
			for bi, bv := range src {
				if bv == 0 {
					continue
				}
				dst := base + bi*16
				for bit := range 8 {
					if (bv>>(7-uint(bit)))&1 == 1 {
						c.Buf[dst+bit*2] = hi
						c.Buf[dst+bit*2+1] = lo
					}
				}
			}
		}
		return
	}
	c.bitmapClipped(x, y, blob, w, rb, rows, hi, lo)
}

func (c *Canvas) bitmapClipped(x, y int, blob []byte, w, rb, rows int, hi, lo byte) {
	for gy := range rows {
		yy := y + gy
		if yy < 0 || yy >= c.H {
			continue
		}
		src := blob[gy*rb : gy*rb+rb]
		for bi, bv := range src {
			if bv == 0 {
				continue
			}
			for bit := range 8 {
				if (bv>>(7-uint(bit)))&1 == 1 && bi*8+bit < w {
					xx := x + bi*8 + bit
					if xx >= 0 && xx < c.W {
						o := (yy*c.W + xx) * 2
						c.Buf[o] = hi
						c.Buf[o+1] = lo
					}
				}
			}
		}
	}
}

type screenBox struct{ x0, y0, x1, y1 int }

// lastPushErr 只在渲染 goroutine 里读写，用于压住重复的 SPI 错误日志。
var lastPushErr string

// dirty：与 prev 的最小差异外接矩形；无差异返回 nil。
func (c *Canvas) dirty() *screenBox {
	stride := c.W * 2
	y0, y1 := -1, -1
	for y := range c.H {
		if !bytes.Equal(c.prev[y*stride:(y+1)*stride], c.Buf[y*stride:(y+1)*stride]) {
			if y0 < 0 {
				y0 = y
			}
			y1 = y
		}
	}
	if y0 < 0 {
		return nil
	}
	x0, x1 := c.W-1, 0
	for y := y0; y <= y1; y++ { // 非零起点 / 闭区间：保持显式
		ra, rb := c.prev[y*stride:(y+1)*stride], c.Buf[y*stride:(y+1)*stride]
		if bytes.Equal(ra, rb) {
			continue
		}
		for x := range c.W {
			o := x * 2
			if ra[o] != rb[o] || ra[o+1] != rb[o+1] {
				if x < x0 {
					x0 = x
				}
				if x > x1 {
					x1 = x
				}
			}
		}
	}
	return &screenBox{x0, y0, x1, y1}
}

// flush：把差异矩形推给屏后端（实现 screenDev 接口）。
// 只有整块写成功才把 prev 前移；SPI 半截/报错时保留差异，下一帧重试，
// 否则 prev 会“谎报”已送达，屏上的旧像素再也没人覆盖（更新残留）。
func (c *Canvas) flush(dev screenDev) bool {
	box := c.dirty()
	if box == nil {
		return false
	}
	stride := c.W * 2
	data := make([]byte, 0, (box.x1-box.x0+1)*(box.y1-box.y0+1)*2)
	for y := box.y0; y <= box.y1; y++ {
		data = append(data, c.Buf[y*stride+box.x0*2:y*stride+(box.x1+1)*2]...)
	}
	if err := dev.pushRect(box.x0, box.y0, box.x1, box.y1, data); err != nil {
		// 同一错误只记一次：SPI 坏掉时不要每帧刷日志。
		if msg := err.Error(); msg != lastPushErr {
			log.Printf("screen: pushRect %v failed: %v", box, err)
			lastPushErr = msg
		}
		return false
	}
	lastPushErr = ""
	for y := box.y0; y <= box.y1; y++ {
		copy(c.prev[y*stride:(y+1)*stride], c.Buf[y*stride:(y+1)*stride])
	}
	return true
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}
