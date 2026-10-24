package main

// screen_crt.go —— 显像管（CRT）开关机动画
//
// 熄屏（crtPlayOff）：
//  1. 整幅画面纵向压扁成一条横线（行压缩 + 束流提亮）
//  2. 横线向中心收成一个亮点
//  3. 亮点余辉熄灭 → 全黑
// 开屏（crtPlayOn）：
//  1. 中心亮点
//  2. 亮点横向展开成一条全宽亮线
//  3. 亮线纵向展开成整幅画面（亮度从过曝回落到正常）
//
// 全部画在 Canvas 上，仍走既有 flush() 差分推屏；st7789 / png / web 三种后端
// 看到的是同一组帧。动画只由屏幕渲染线程调用（该线程独占屏幕硬件）。

import (
	"math"
	"time"
)

const (
	crtOffDur = 320 * time.Millisecond // 熄屏总时长
	crtOnDur  = 420 * time.Millisecond // 开屏总时长

	crtOffSquashEnd = 0.62 // 纵向压扁结束点（占熄屏总进度）
	crtOffShrinkEnd = 0.88 // 横向收线结束点
	crtOnDotEnd     = 0.14 // 亮点生长结束点（占开屏总进度）
	crtOnLineEnd    = 0.44 // 横向展开结束点
)

// crtFrameGap：动画帧间隔。跟随 SCREEN_TICK_MS，但夹到 [16ms,40ms]——
// 太慢看不出收缩，太快只是徒增 SPI 帧开销。
func crtFrameGap() time.Duration {
	d := screenTick
	if d < 16*time.Millisecond {
		d = 16 * time.Millisecond
	}
	if d > 40*time.Millisecond {
		d = 40 * time.Millisecond
	}
	return d
}

func crtFrameCount(dur time.Duration) int {
	n := int(dur / crtFrameGap())
	if n < 6 {
		n = 6
	}
	return n
}

// crtPlayOff：在 dev 上播放关机动画。c 是当前显示帧，播放后停在最后的全黑帧。
func crtPlayOff(dev screenDev, c *Canvas) {
	if dev == nil || c == nil {
		return
	}
	frozen := newCanvas(c.W, c.H)
	copy(frozen.Buf, c.Buf)
	n := crtFrameCount(crtOffDur)
	gap := crtFrameGap()
	for i := 1; i <= n; i++ {
		crtFrameOff(c, frozen, float64(i)/float64(n))
		c.flush(dev)
		time.Sleep(gap)
	}
}

// crtPlayOn：在 dev 上播放开机动画，最后一帧就是 src。
func crtPlayOn(dev screenDev, c, src *Canvas) {
	if dev == nil || c == nil || src == nil {
		return
	}
	if c == src { // 防御：动画要一边读源一边写目标
		frozen := newCanvas(c.W, c.H)
		copy(frozen.Buf, c.Buf)
		src = frozen
	}
	n := crtFrameCount(crtOnDur)
	gap := crtFrameGap()
	// 唤醒/上电时物理屏 RAM 未必等于画布认为的内容——首帧强制推整幅。
	c.invalidate()
	for i := 1; i <= n; i++ {
		crtFrameOn(c, src, float64(i)/float64(n))
		c.flush(dev)
		time.Sleep(gap)
	}
}

// crtFrameOff：把 src 画成熄屏进度 t（0..1）的一帧到 dst。
func crtFrameOff(dst, src *Canvas, t float64) {
	dst.clear(0)
	if t <= 0 {
		copy(dst.Buf, src.Buf)
		return
	}
	if t >= 1 {
		return
	}
	cx, cy := dst.W/2, dst.H/2
	switch {
	case t < crtOffSquashEnd: // 纵向压扁
		u := t / crtOffSquashEnd
		h := float64(dst.H) * math.Pow(1-u, 1.7)
		if h < 2 {
			h = 2
		}
		rows := int(math.Round(h))
		if rows < 2 {
			rows = 2
		}
		top := cy - rows/2
		mul := 1 + 2.4*u  // 束流增强
		white := 0.55 * u // 往白里烧
		for y := top; y < top+rows; y++ {
			if y < 0 || y >= dst.H {
				continue
			}
			sy := int(math.Floor((float64(y-top) + 0.5) * float64(src.H) / float64(rows)))
			if sy < 0 {
				sy = 0
			}
			if sy >= src.H {
				sy = src.H - 1
			}
			di := y * dst.W * 2
			si := sy * src.W * 2
			crtTintRow(dst.Buf[di:di+dst.W*2], src.Buf[si:si+src.W*2], mul, white)
		}
		// 压到后半程，中心扫描线本身开始发亮
		if u > 0.30 {
			k := (u - 0.30) / 0.70
			crtBeamLine(dst, cx, cy, dst.W-4, 0.30+0.70*k, 0.45*k)
		}
	case t < crtOffShrinkEnd: // 横向收线
		u := (t - crtOffSquashEnd) / (crtOffShrinkEnd - crtOffSquashEnd)
		w := int(float64(dst.W) * math.Pow(1-u, 1.6))
		if w < 2 {
			w = 2
		}
		crtBeamLine(dst, cx, cy, w, 1.0, 0.75)
	default: // 光点余辉
		u := (t - crtOffShrinkEnd) / (1 - crtOffShrinkEnd)
		i := 1 - u
		crtBeamDot(dst, cx, cy, 1+int(math.Round(3*i)), i)
	}
}

// crtFrameOn：把 src 画成开屏进度 t（0..1）的一帧到 dst。
// t=1 时逐字节等于 src。
func crtFrameOn(dst, src *Canvas, t float64) {
	dst.clear(0)
	if t <= 0 {
		return
	}
	if t >= 1 {
		copy(dst.Buf, src.Buf)
		return
	}
	cx, cy := dst.W/2, dst.H/2
	switch {
	case t < crtOnDotEnd: // 中心光点
		u := t / crtOnDotEnd
		crtBeamDot(dst, cx, cy, 1+int(math.Round(4*u)), 0.35+0.65*u)
	case t < crtOnLineEnd: // 横向扫开
		u := (t - crtOnDotEnd) / (crtOnLineEnd - crtOnDotEnd)
		w := 3 + int(math.Round(float64(dst.W-3)*easeOutCubic(u)))
		crtBeamLine(dst, cx, cy, w, 1.0, 0.85)
	default: // 纵向展开
		u := (t - crtOnLineEnd) / (1 - crtOnLineEnd)
		h := 2 + float64(dst.H-2)*easeOutCubic(u)
		rows := int(math.Round(h))
		if rows < 2 {
			rows = 2
		}
		if rows > dst.H {
			rows = dst.H
		}
		top := cy - rows/2
		mul := 2.6 - 1.6*u // 过曝 → 正常
		white := 0.85 * (1 - u)
		for y := top; y < top+rows; y++ {
			if y < 0 || y >= dst.H {
				continue
			}
			sy := int(math.Floor((float64(y-top) + 0.5) * float64(src.H) / float64(rows)))
			if sy < 0 {
				sy = 0
			}
			if sy >= src.H {
				sy = src.H - 1
			}
			di := y * dst.W * 2
			si := sy * src.W * 2
			crtTintRow(dst.Buf[di:di+dst.W*2], src.Buf[si:si+src.W*2], mul, white)
		}
		if k := 1 - u; k > 0.02 { // 束流核心淡出（平方衰减：后期只剩画面）
			k *= k
			crtBeamLine(dst, cx, cy, dst.W-4, 0.60*k, 0.50*k)
		}
	}
}

// crtTintRow：整行 RGB565 提亮 mul 倍，再按 white（0..1）向白场混合。
func crtTintRow(dst, src []byte, mul, white float64) {
	for i := 0; i+1 < len(src) && i+1 < len(dst); i += 2 {
		v := uint16(src[i])<<8 | uint16(src[i+1])
		r := float64(exp5[(v>>11)&0x1F]) * mul
		g := float64(exp6[(v>>5)&0x3F]) * mul
		b := float64(exp5[v&0x1F]) * mul
		if r > 255 {
			r = 255
		}
		if g > 255 {
			g = 255
		}
		if b > 255 {
			b = 255
		}
		r += (255 - r) * white
		g += (255 - g) * white
		b += (255 - b) * white
		out := uint16(r)>>3<<11 | uint16(g)>>2<<5 | uint16(b)>>3
		dst[i] = byte(out >> 8)
		dst[i+1] = byte(out)
	}
}

// crtBeam：束流颜色——强度 i（0..1）映射到偏冷的白（CRT 荧光粉色温）。
func crtBeam(i float64) uint16 {
	i = clamp01(i)
	v := 255 * i
	return rgb565(uint8(v*0.86), uint8(v*0.94), uint8(v))
}

// crtBeamLine：以 (cx,cy) 为中心画一条宽 w 的亮线，带上下辉光。
func crtBeamLine(c *Canvas, cx, cy, w int, core, halo float64) {
	if w < 1 || core <= 0.01 {
		return
	}
	x0 := cx - w/2
	x1 := x0 + w - 1
	if x0 < 0 {
		x0 = 0
	}
	if x1 >= c.W {
		x1 = c.W - 1
	}
	if x1 < x0 {
		return
	}
	if halo > 0.02 {
		for d := 2; d >= 1; d-- {
			k := halo * (1 - float64(d-1)*0.45) * 0.5
			c.hline(x0, x1, cy-d, crtBeam(k))
			c.hline(x0, x1, cy+d, crtBeam(k))
		}
	}
	c.hline(x0, x1, cy, crtBeam(core))
}

// crtBeamDot：中心光点（外圈辉光 + 实心核心）。
func crtBeamDot(c *Canvas, cx, cy, r int, i float64) {
	if i <= 0.01 {
		return
	}
	if r < 1 {
		r = 1
	}
	c.circle(cx, cy, r+2, crtBeam(i*0.20), true)
	c.circle(cx, cy, r, crtBeam(i), true)
}

func easeOutCubic(x float64) float64 {
	x = clamp01(x)
	return 1 - math.Pow(1-x, 3)
}
