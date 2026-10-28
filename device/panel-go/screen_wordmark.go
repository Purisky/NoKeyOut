package main

import "math"

// screen_wordmark.go —— ADMIN 页的大字标与倒计时。
//
// 字标用 Silkscreen（仓库 web/assets/fonts，OFL）34px 光栅化的 1bpp 字模，
// 与正文的 JetBrains Mono 区分开；倒计时用 NUM 数字字体（24px）。
//
// 字标颜色是流动的多段渐变（teal→blue→violet）＋ 一道高光扫过，
// 参考用户指定的 Balatro 背景流光效果；静态（now<=0）退回纯色，保证黄金样本稳定。

// wordmarkWidth：字标像素宽（按 Silkscreen 前进宽度累加）。
func wordmarkWidth(s string) int {
	w := 0
	for _, r := range s {
		w += wordmarkAdv[r]
	}
	return w
}

// wordGlyphAt：按基准 x 找到落在这个像素列上的字形（返回字形与字内列偏移）。
func wordGlyphAt(s string, x int) (wordGlyph, int, bool) {
	off := 0
	for _, r := range s {
		adv := wordmarkAdv[r]
		if x >= off && x < off+adv {
			g, ok := wordmarkGlyphs[r]
			return g, x - off, ok
		}
		off += adv
	}
	return wordGlyph{}, 0, false
}

// 流光渐变色标（RGB565）。三段循环，避免整屏彩虹抢走状态色语义。
var wordmarkStops = [3]uint16{
	rgb565(40, 196, 178), // teal
	rgb565(76, 141, 255), // blue
	rgb565(177, 92, 255), // violet
}

// mix565：两色线性混合（t=0 → a，t=1 → b）。
func mix565(a, b uint16, t float64) uint16 {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	ar, ag, ab := rgb565expand(a)
	br, bg, bb := rgb565expand(b)
	r := uint8(float64(ar) + (float64(br)-float64(ar))*t)
	g := uint8(float64(ag) + (float64(bg)-float64(ag))*t)
	bl := uint8(float64(ab) + (float64(bb)-float64(ab))*t)
	return rgb565(r, g, bl)
}

// flowColorAt：按**屏幕 x** 取流光颜色（图标与字标共用同一条色带，视觉连续）。
// now<=0 时恒为纯色，保证黄金样本可复现。
func flowColorAt(x int, now float64) uint16 {
	if now <= 0 {
		return screenFG
	}
	nx := float64(x) / float64(screenW)
	// 三段渐变循环平移：约 2.9s 走完一轮。
	t := math.Mod(nx*1.15+now*0.35, 1)
	if t < 0 {
		t += 1
	}
	seg := t * 3
	i := int(seg) % 3
	return mix565(wordmarkStops[i], wordmarkStops[(i+1)%3], seg-float64(int(seg)))
}

// drawMaskFlow：1bpp 掩码按 x 取流光色逐点绘制（图标/字标共用）。
func drawMaskFlow(c *Canvas, x, y int, blob []byte, w, rb, rows int, now float64) {
	for row := 0; row < rows; row++ {
		for col := 0; col < w; col++ {
			if blob[row*rb+col/8]&(0x80>>(col%8)) == 0 {
				continue
			}
			c.pixel(x+col, y+row, flowColorAt(x+col, now))
		}
	}
}

// drawWordmarkFlow：以 cx 为中心、top 为顶部绘制流光字标。
func drawWordmarkFlow(c *Canvas, cx, top int, text string, now float64) int {
	w := wordmarkWidth(text)
	x0 := cx - w/2
	for dy := 0; dy < wordmarkFontH; dy++ {
		for dx := 0; dx < w; dx++ {
			g, gx, ok := wordGlyphAt(text, dx)
			if !ok || gx >= g.w || dy >= len(g.rows) {
				continue
			}
			if g.rows[dy]&(1<<uint(g.w-1-gx)) == 0 {
				continue
			}
			c.pixel(x0+dx, top+dy, flowColorAt(x0+dx, now))
		}
	}
	return w
}

// countdownWidth：倒计时像素宽（数字用 NUM，冒号按 scale 倍固定宽）。
func countdownWidth(s string, scale int) int {
	w := 0
	for _, r := range s {
		if r == ':' {
			w += 6 * scale
			continue
		}
		w += NUM.step(r) * scale
	}
	return w
}

// drawCountdown：以 cx 为中心、top 为数字顶部绘制 mm:ss。
func drawCountdown(c *Canvas, cx, top int, s string, color uint16, scale int) int {
	w := countdownWidth(s, scale)
	x := cx - w/2
	for _, r := range s {
		if r == ':' {
			d := 3 * scale
			c.fillRect(x+scale, top+6*scale, d, d, color)
			c.fillRect(x+scale, top+16*scale, d, d, color)
			x += 6 * scale
			continue
		}
		if blob, gw, rb, ok := NUM.glyph(r); ok {
			c.bitmapScale(x, top, blob, gw, rb, NUM.H, color, scale)
		}
		x += NUM.step(r) * scale
	}
	return w
}
