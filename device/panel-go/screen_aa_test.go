package main

import "testing"

// AA 合成必须**还原原色**：100% 覆盖的像素要等于请求的颜色。
// 绿色被压到 1/4，白字渲染成洋红（115,57,123）。这条测试就是守住它。
func TestAAColorRoundTrip(t *testing.T) {
	c := newCanvas(screenW, screenH)
	c.clear(screenBG)
	cov := []byte{0xFF} // 4 个像素、级别 3（100%）
	palette := map[string]uint16{
		"FG": screenFG, "Muted": screenMuted, "OK": screenOK,
		"Warn": screenWarn, "Danger": screenDanger, "Accent": screenAccent,
	}
	for name, col := range palette {
		c.bitmapAA(10, 10, cov, 4, 1, 1, col)
		got := uint16(c.Buf[(10*screenW+10)*2])<<8 | uint16(c.Buf[(10*screenW+10)*2+1])
		if got != col {
			t.Fatalf("%s：100%% 覆盖应还原原色，got=%04x want=%04x", name, got, col)
		}
	}
}

// 半透明级别必须落在前景与背景之间（各通道单调），且不能把绿色算丢。
func TestAAHalfCoverageStaysBetween(t *testing.T) {
	c := newCanvas(screenW, screenH)
	c.clear(screenBG)
	c.bitmapAA(20, 20, []byte{0x80, 0x00, 0x00, 0x00}, 1, 1, 1, screenFG) // 像素 0 = 级别 2（50%）
	got := uint16(c.Buf[(20*screenW+20)*2])<<8 | uint16(c.Buf[(20*screenW+20)*2+1])
	gf := int((screenFG >> 5) & 0x3F)
	gb := int((screenBG >> 5) & 0x3F)
	gg := int((got >> 5) & 0x3F)
	if gg <= gb || gg > gf {
		t.Fatalf("50%% 覆盖的绿色分量应在 bg(%d) 与 fg(%d) 之间，got=%d", gb, gf, gg)
	}
}

// 点阵字体必须是**硬像素**：烘焙产物不带覆盖度，且渲染只出现底色与字色两种颜色。
// （点阵字体的全部价值就在于此；一旦有人按老办法烘了带 AA 的字模，屏上就又是一片糊。）
func TestPixelFontIsCrisp(t *testing.T) {
	for _, f := range []*Font{SM, MD, NUM, CJKFont} {
		if len(f.cov) != 0 {
			t.Errorf("字体 %s 带覆盖度数据（%d 字节）——点阵字体不该有抗锯齿层；重烘请带 --no-aa", f.Key, len(f.cov))
		}
	}
	c := newCanvas(screenW, screenH)
	c.clear(screenBG)
	drawMixed(c, 8, 8, "42.8 tok/s 今日调用 OpenCode Go", screenFG, SM, CJKFont)
	NUM.draw(c, 8, 60, "42.8", screenFG)
	seen := map[uint16]int{}
	for y := 0; y < 80; y++ {
		for x := 0; x < 230; x++ {
			seen[uint16(c.Buf[(y*screenW+x)*2])<<8|uint16(c.Buf[(y*screenW+x)*2+1])]++
		}
	}
	if len(seen) != 2 {
		t.Fatalf("文字区域出现 %d 种颜色（应恰好 2 种：底色 + 字色）——抗锯齿回来了：%v", len(seen), seen)
	}
	if seen[uint16(screenBG)] == 0 {
		t.Fatal("没有底色像素？画布没清干净或文字没画出来")
	}
}
