//go:build !(js && wasm)

package main

// screen_msg.go —— 在 panel 服务安装前接管小屏，显示下载/安装进度。
//
//   panel screen-msg <stage> <percent> [zh|en]
//
// stage: start verify download extract gateway panel done error
// The firstboot/install scripts call this repeatedly; the process exits after
// one frame, so the ST7789 keeps the last image and the backlight stays on.

import (
	"fmt"
	"os"
	"strconv"
)

type screenStageText struct {
	title string
	sub   string
	warn  bool
}

var screenStageTexts = map[string]map[string]screenStageText{
	"zh": {
		"start":    {"正在安装 NoKeyOut", "请勿断开网络或断电", true},
		"verify":   {"正在校验安装包", "请勿断开网络或断电", true},
		"prepare":  {"正在准备安装包", "请勿断电", true},
		"download": {"正在下载 NoKeyOut", "请勿断开网络或断电", true},
		"extract":  {"正在解压安装包", "请勿断电", true},
		"gateway":  {"正在安装网关", "请勿断电", true},
		"panel":    {"正在配置管理面", "请勿断电", true},
		"done":     {"安装完成", "管理面 :8443", false},
		"error":    {"安装失败", "查看控制台日志", false},
	},
	"en": {
		"start":    {"Installing NoKeyOut", "Keep network connected, do not power off", true},
		"verify":   {"Verifying release", "Keep network connected, do not power off", true},
		"prepare":  {"Preparing release", "Do not power off", true},
		"download": {"Downloading NoKeyOut", "Keep network connected, do not power off", true},
		"extract":  {"Extracting release", "Do not power off", true},
		"gateway":  {"Installing gateway", "Do not power off", true},
		"panel":    {"Configuring panel", "Do not power off", true},
		"done":     {"Install complete", "Panel :8443", false},
		"error":    {"Install failed", "Check console logs", false},
	},
}

func cmdScreenMsg(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: panel screen-msg <stage> [percent] [zh|en]")
		return 2
	}
	stage := args[0]
	lang := langGet()
	if len(args) > 2 {
		if args[2] == "zh" || args[2] == "en" {
			lang = args[2]
		}
	}
	pct := -1
	if len(args) > 1 {
		if n, err := strconv.Atoi(args[1]); err == nil {
			pct = n
		}
	}
	if pct < 0 {
		pct = -1
	}
	if pct > 100 {
		pct = 100
	}

	text, ok := screenStageTexts[lang][stage]
	if !ok {
		text = screenStageTexts["en"][stage]
	}
	if text.title == "" {
		text = screenStageText{title: stage, sub: "", warn: false}
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
	renderScreenStage(c, text, pct, lang)
	if !c.flush(st) {
		fmt.Fprintln(os.Stderr, "screen-msg: flush failed")
		return 1
	}
	return 0
}

func renderScreenStage(c *Canvas, text screenStageText, pct int, lang string) {
	c.clear(screenBG)
	drawScreenCard(c, 8, 36, screenW-16, 168)

	drawMixedCenter(c, screenW/2, 60, screenTrunc(text.title, 200), screenFG, MD, CJKFont)
	if text.sub != "" {
		drawMixedCenter(c, screenW/2, 88, screenTrunc(text.sub, 210), screenMuted, SM, CJKFont)
	}

	if pct >= 0 {
		x0, y0, w, h := 24, 118, screenW-48, 8
		c.rect(x0, y0, w, h, screenBorder)
		c.fillRect(x0+2, y0+2, (w-4)*pct/100, h-4, screenAccent)
		SM.drawCenter(c, screenW/2, 132, fmt.Sprintf("%d%%", pct), screenMuted)
	}

	if text.warn {
		if lang == "zh" {
			drawMixedCenter(c, screenW/2, 158, "请勿断开网络", screenWarn, SM, CJKFont)
			drawMixedCenter(c, screenW/2, 176, "请勿断电", screenWarn, SM, CJKFont)
		} else {
			SM.drawCenter(c, screenW/2, 158, "Do not unplug network", screenWarn)
			SM.drawCenter(c, screenW/2, 176, "Do not power off", screenWarn)
		}
	}
}

func drawMixedCenter(c *Canvas, cx, y int, s string, color uint16, ascii, cjk *Font) {
	drawMixed(c, cx-mixedWidth(s, ascii, cjk)/2, y, s, color, ascii, cjk)
}
