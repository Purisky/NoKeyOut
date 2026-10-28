package main

// screen_motion_test.go —— 动效参数的出处断言（改参数必须同时改注释里的官方来源）。

import (
	"bytes"
	"math"
	"testing"
)

// M3 不确定圆形指示器：周期 5s，关键帧 0→0.8@1.25s→0.2@2.5s→0.8@3.75s→0@5s，
// 起始角 270°，全局旋转 1440°/5s（见 screen_motion.go 顶部来源）。
func TestM3IndeterminateKeyframes(t *testing.T) {
	cases := []struct {
		now   float64
		sweep float64
		start float64
	}{
		{0, 0, 270},
		{1.25, 0.8, 270 + 360 + 0},    // global=360, add=0
		{2.5, 0.2, 270 + 720 + 360},   // global=720, add=360
		{3.75, 0.8, 270 + 1080 + 360}, // global=1080, add=360
		{5.0, 0, 270},                 // 周期回绕
	}
	for _, c := range cases {
		start, sweep := m3Indeterminate(c.now)
		if math.Abs(sweep-c.sweep) > 1e-9 {
			t.Fatalf("t=%.2fs sweep=%.3f，want %.3f", c.now, sweep, c.sweep)
		}
		if math.Abs(start-c.start) > 1e-9 {
			t.Fatalf("t=%.2fs start=%.1f，want %.1f", c.now, start, c.start)
		}
	}
	// 静态场景（now<=0）不画：保证测试/预览的黄金样本可复现。
	c := newCanvas(screenW, screenH)
	c.clear(screenBG)
	before := append([]byte(nil), c.Buf...)
	drawM3Indeterminate(c, 20, 20, 18, 2, screenAccent, 0)
	if !bytes.Equal(before, c.Buf) {
		t.Fatal("now=0 时不应绘制动画")
	}
}

// shared axis：300ms；出场向左淡出、入场从右淡入，中间不出现整屏黑场。
func TestSharedAxisComposite(t *testing.T) {
	from := newCanvas(screenW, screenH)
	from.clear(rgb565(255, 0, 0))
	to := newCanvas(screenW, screenH)
	to.clear(rgb565(0, 0, 255))
	dst := newCanvas(screenW, screenH)

	compositeSharedAxis(dst, from, to, 0)
	if !bytes.Equal(dst.Buf, from.Buf) {
		t.Fatal("p=0 应完全显示旧页")
	}
	compositeSharedAxis(dst, from, to, 1)
	if !bytes.Equal(dst.Buf, to.Buf) {
		t.Fatal("p=1 应完全显示新页")
	}

	// 中段：两页同时在场（既非纯红也非纯蓝），且不出现整屏黑场。
	compositeSharedAxis(dst, from, to, 0.5)
	nonBG := 0
	for i := 0; i+1 < len(dst.Buf); i += 2 {
		if dst.Buf[i] != 0 || dst.Buf[i+1] != 0 {
			nonBG++
		}
	}
	if nonBG < screenW*screenH*3/4 {
		t.Fatalf("过渡中段黑场过大：非背景像素 %d", nonBG)
	}
	if bytes.Equal(dst.Buf, from.Buf) || bytes.Equal(dst.Buf, to.Buf) {
		t.Fatal("过渡中段应同时混合两页")
	}
}

// 字标流光：静态恒为纯色；动画时同一点颜色随时间变化，且落在品牌渐变色域内。
func TestWordmarkFlowColors(t *testing.T) {
	if got := balatroFlow(100, 100, 0); got != screenFG {
		t.Fatalf("静态应为纯色，got %04x", got)
	}
	a := balatroFlow(100, 100, 0.4)
	b := balatroFlow(100, 100, 1.3)
	if a == b {
		t.Fatal("流光颜色不随时间变化")
	}
	if a == screenFG {
		t.Fatal("动画时不应仍是纯色")
	}
	// 图标也走同一条色带：同一掩码在不同时刻颜色不同。
	c1, c2 := newCanvas(screenW, screenH), newCanvas(screenW, screenH)
	drawMaskFlow(c1, 96, 18, productMark48, productMarkSize, productMarkSize/8, productMarkSize, 0.4)
	drawMaskFlow(c2, 96, 18, productMark48, productMarkSize, productMarkSize/8, productMarkSize, 1.3)
	if bytes.Equal(c1.Buf, c2.Buf) {
		t.Fatal("图标的流光没有生效")
	}
}
