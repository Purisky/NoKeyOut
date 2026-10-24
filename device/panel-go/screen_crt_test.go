package main

// screen_crt_test.go —— CRT 开关机动画的几何/颜色回归：
// 压扁帧必须只剩中心横带、收线帧必须变窄、光点不能铺开、开屏最后一帧
// 必须逐字节回到目标画面；顺带走一遍真实 pushRect 路径。

import (
	"bytes"
	"testing"
	"time"
)

func crtTestPage() *Canvas {
	src := newCanvas(screenW, screenH)
	src.fillRect(0, 0, screenW, screenH, screenBG)
	src.fillRect(0, 0, screenW, 2, screenAccent)
	src.fillRect(16, 16, 80, 24, screenFG)
	src.fillRect(40, 120, 160, 40, screenCard2)
	return src
}

func crtTestPixel(c *Canvas, x, y int) uint16 {
	i := (y*c.W + x) * 2
	return uint16(c.Buf[i])<<8 | uint16(c.Buf[i+1])
}

// crtTestInkRows：非黑像素覆盖的行范围与总数。
func crtTestInkRows(c *Canvas) (first, last, ink int) {
	first, last = -1, -1
	for y := range c.H {
		for x := range c.W {
			if crtTestPixel(c, x, y) != 0 {
				if first < 0 {
					first = y
				}
				last = y
				ink++
				break
			}
		}
	}
	return first, last, ink
}

func crtTestRowInk(c *Canvas, y int) int {
	n := 0
	for x := range c.W {
		if crtTestPixel(c, x, y) != 0 {
			n++
		}
	}
	return n
}

func crtTestAllBlack(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

func TestCRTOffSquashesToCenterBand(t *testing.T) {
	dst := newCanvas(screenW, screenH)
	crtFrameOff(dst, crtTestPage(), 0.45)
	first, last, ink := crtTestInkRows(dst)
	if ink == 0 {
		t.Fatal("压扁帧全黑")
	}
	h := last - first + 1
	// h≈27（240·(1-0.45/0.62)^1.7），允许采样/辉光的几行误差
	if h < 10 || h > 40 {
		t.Fatalf("压扁后亮带高 %d 行，want ≈27", h)
	}
	if first > screenH/2 || last < screenH/2 {
		t.Fatalf("亮带没落在中心：%d..%d", first, last)
	}
	if n := crtTestRowInk(dst, 4); n != 0 {
		t.Fatalf("画面上缘应已压掉，仍有 %d 个亮像素", n)
	}
}

func TestCRTOffShrinksToLineThenDot(t *testing.T) {
	dst := newCanvas(screenW, screenH)
	src := crtTestPage()

	crtFrameOff(dst, src, 0.75) // 横向收线中段
	first, last, _ := crtTestInkRows(dst)
	if last-first+1 > 8 {
		t.Fatalf("收线帧不应纵向铺开：%d 行", last-first+1)
	}
	if n := crtTestRowInk(dst, screenH/2); n < 50 || n > 110 {
		t.Fatalf("收线中段线宽 %d px，want ≈79", n)
	}
	if r, g, b := rgb565expand(crtTestPixel(dst, screenW/2, screenH/2)); r < 180 || g < 200 || b < 240 {
		t.Fatalf("收线应为冷白亮线，中心像素 (%d,%d,%d)", r, g, b)
	}

	crtFrameOff(dst, src, 0.94) // 余辉光点
	first, last, ink := crtTestInkRows(dst)
	if ink == 0 {
		t.Fatal("余辉帧全黑")
	}
	if first < screenH/2-8 || last > screenH/2+8 {
		t.Fatalf("光点扩散过大：%d..%d", first, last)
	}

	crtFrameOff(dst, src, 1) // 关机完成
	if _, _, ink := crtTestInkRows(dst); ink != 0 {
		t.Fatalf("关机完成帧应全黑，仍有 %d 个亮像素", ink)
	}
}

func TestCRTOnDotLineExpand(t *testing.T) {
	dst := newCanvas(screenW, screenH)
	src := crtTestPage()

	crtFrameOn(dst, src, 0.05) // 光点
	first, last, ink := crtTestInkRows(dst)
	if ink == 0 {
		t.Fatal("开屏光点帧全黑")
	}
	if first < screenH/2-8 || last > screenH/2+8 {
		t.Fatalf("开屏光点应只在中心：%d..%d", first, last)
	}

	crtFrameOn(dst, src, 0.30) // 横向扫开、尚未纵向展开
	first, last, _ = crtTestInkRows(dst)
	if last-first+1 > 8 {
		t.Fatalf("横向阶段不该纵向展开：%d 行", last-first+1)
	}
	if n := crtTestRowInk(dst, screenH/2); n < 180 {
		t.Fatalf("横向阶段线宽 %d px，want 接近全宽", n)
	}

	crtFrameOn(dst, src, 0.70) // 纵向展开
	first, last, _ = crtTestInkRows(dst)
	if h := last - first + 1; h < 150 || h > screenH {
		t.Fatalf("纵向展开高度 %d 行，want ≈203", h)
	}
	if first > 30 || last < screenH-30 {
		t.Fatalf("展开后应接近铺满：%d..%d", first, last)
	}

	crtFrameOn(dst, src, 1) // 收尾必须与目标帧逐字节一致
	if !bytes.Equal(dst.Buf, src.Buf) {
		t.Fatal("开屏最后一帧与目标画面不一致")
	}
}

// crtRecDev：把 pushRect 贴回一块帧缓冲，模拟真实设备收到的画面。
type crtRecDev struct {
	fb     []byte
	pushes int
}

func (d *crtRecDev) pushRect(x0, y0, x1, y1 int, data []byte) error {
	w := x1 - x0 + 1
	for y := y0; y <= y1; y++ {
		di := (y*screenW + x0) * 2
		si := (y - y0) * w * 2
		copy(d.fb[di:di+w*2], data[si:si+w*2])
	}
	d.pushes++
	return nil
}

func TestCRTPlayOffOnReachesDevice(t *testing.T) {
	old := screenTick
	screenTick = 40 * time.Millisecond // 压缩测试时长（动画帧间隔上限）
	defer func() { screenTick = old }()

	src := crtTestPage()
	c := newCanvas(screenW, screenH)
	copy(c.Buf, src.Buf)
	dev := &crtRecDev{fb: make([]byte, screenW*screenH*2)}

	crtPlayOff(dev, c)
	if dev.pushes < 6 {
		t.Fatalf("关机动画只推了 %d 帧", dev.pushes)
	}
	if !crtTestAllBlack(dev.fb) {
		t.Fatal("关机动画结束后设备画面应全黑")
	}
	if !crtTestAllBlack(c.Buf) {
		t.Fatal("关机动画结束后画布应全黑（prev 已同步）")
	}

	crtPlayOn(dev, c, src)
	if dev.pushes < 16 {
		t.Fatalf("开关机动画合计只推了 %d 帧", dev.pushes)
	}
	if !bytes.Equal(dev.fb, src.Buf) {
		t.Fatal("开屏动画结束后设备画面与目标帧不一致（差分漏推）")
	}
}
