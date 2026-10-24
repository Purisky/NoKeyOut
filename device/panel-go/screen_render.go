package main

// screen_render.go —— 纯渲染入口：不依赖 panel 进程、HTTP 或硬件。
// 真机 screend 与 browser WASM 共用这一条路径。

import (
	"time"
)

var screenTick = 33 * time.Millisecond // 渲染节拍（30fps）：进度线/圆环/滑入都按它走

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// renderScreenPage：唯一的常态渲染入口（单屏）。锁定/网络不可用/错误由 uiRender 内部接管。
func renderScreenPage(snap map[string]any, hist *History, c *Canvas, lang string, anim animState) {
	uiRender("main", snap, hist, c, 0, lang, time.Now().Format("15:04"), anim)
}
