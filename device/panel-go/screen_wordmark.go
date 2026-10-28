package main

// screen_wordmark.go —— ADMIN 页字标与倒计时。

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

// drawMaskFlow：1bpp 掩码逐点取流光色（图标/字标共用同一套算法）。
func drawMaskFlow(c *Canvas, x, y int, blob []byte, w, rb, rows int, now float64) {
	for row := 0; row < rows; row++ {
		for col := 0; col < w; col++ {
			if blob[row*rb+col/8]&(0x80>>(col%8)) == 0 {
				continue
			}
			c.pixel(x+col, y+row, swirlAt(x+col, y+row, now))
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
			c.pixel(x0+dx, top+dy, swirlAt(x0+dx, top+dy, now))
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
