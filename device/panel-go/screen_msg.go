//go:build !(js && wasm)

package main

// screen_msg.go —— 在 panel 服务安装前接管小屏，显示安装进度。
//
//   panel screen-msg "Installing NoKeyOut" "Downloading release" 35
//
// firstboot 在 rootfs 里直接调用它；消息是英文，避免安装阶段依赖语言设置。
// 进程退出后 ST7789 保留最后一帧；背光保持点亮。

import (
	"fmt"
	"os"
	"strconv"
)

func cmdScreenMsg(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: panel screen-msg <title> [subtitle] [percent]")
		return 2
	}
	title := args[0]
	sub := ""
	if len(args) > 1 {
		sub = args[1]
	}
	pct := -1
	if len(args) > 2 {
		if n, err := strconv.Atoi(args[2]); err == nil {
			pct = n
		}
	}

	p := newPanel()
	st, err := p.openScreenHW()
	if err != nil {
		fmt.Fprintf(os.Stderr, "screen-msg: %v\n", err)
		return 1
	}
	if p.cfg.screenBL >= 0 {
		p.blOpen()
	}

	c := newCanvas(screenW, screenH)
	renderScreenMsg(c, title, sub, pct)
	if !c.flush(st) {
		fmt.Fprintln(os.Stderr, "screen-msg: flush failed")
		return 1
	}
	return 0
}

func renderScreenMsg(c *Canvas, title, sub string, pct int) {
	c.clear(screenBG)
	drawScreenCard(c, 8, 36, screenW-16, 168)

	MD.drawCenter(c, screenW/2, 66, screenTrunc(title, 200), screenFG)
	if sub != "" {
		SM.drawCenter(c, screenW/2, 96, screenTrunc(sub, 220), screenMuted)
	}

	if pct >= 0 {
		if pct > 100 {
			pct = 100
		}
		x0, y0, w, h := 24, 136, screenW-48, 8
		c.rect(x0, y0, w, h, screenBorder)
		c.fillRect(x0+2, y0+2, (w-4)*pct/100, h-4, screenAccent)
		SM.drawCenter(c, screenW/2, 152, fmt.Sprintf("%d%%", pct), screenMuted)
	}

	SM.drawCenter(c, screenW/2, 176, "Do not power off", screenWarn)
}
