package main

import (
	"testing"
	"time"
)

// 屏宽 240、内容宽 224：柱状图必须铺满可用宽度，不能只在左边画几条粗柱。
func TestSparkBarsUsesFullWidth(t *testing.T) {
	c := newCanvas(screenW, screenH)
	vs := make([]float64, 240) // 240 个采样 > 224 列 → 先降采样
	for i := range vs {
		vs[i] = 10
	}
	sparkBars(c, 0, 0, 224, 12, vs, screenAccent)
	lit := 0
	for x := range 224 {
		i := (11*screenW + x) * 2
		if uint16(c.Buf[i])<<8|uint16(c.Buf[i+1]) == screenAccent {
			lit++
		}
	}
	if lit < 220 {
		t.Fatalf("柱状图只覆盖 %d/224 列", lit)
	}
}

// 列数少于宽度时柱宽应随宽度缩放（不是固定的 3px 台阶）。
func TestSparkBarsScalesWithWidth(t *testing.T) {
	c := newCanvas(screenW, screenH)
	vs := make([]float64, 28) // 24h 的粗桶（每桶约 1 小时）
	for i := range vs {
		vs[i] = 10
	}
	sparkBars(c, 0, 0, 224, 12, vs, screenAccent)
	lit := 0
	for x := range 224 {
		i := (11*screenW + x) * 2
		if uint16(c.Buf[i])<<8|uint16(c.Buf[i+1]) == screenAccent {
			lit++
		}
	}
	if lit < 180 {
		t.Fatalf("28 桶只覆盖 %d/224 列，柱宽没有随宽度缩放", lit)
	}
}

// 时间轴标签必须跟本地时钟走：滚动窗口起点 18:09 时，6 小时后应显示 00，而不是 6。
func TestAxisLabelUsesLocalClock(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	since := time.Date(2026, 10, 23, 18, 9, 0, 0, loc)
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "18"},
		{6 * time.Hour, "00"},
		{12 * time.Hour, "06"},
		{18 * time.Hour, "12"},
	}
	for _, c := range cases {
		if got := axisLabel(since.Add(c.d), "today"); got != c.want {
			t.Errorf("+%v → %q，want %q", c.d, got, c.want)
		}
	}
	if got := axisLabel(since.AddDate(0, 0, 1), "7d"); got != "24" {
		t.Errorf("7d 日号 = %q，want 24", got)
	}
}
