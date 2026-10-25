package main

// screen_cfg.go —— 物理屏的尺寸与方向（面板可改，落盘到 panel 可写目录）
//
// 背景：驱动只吃 MADCTL + 偏移，UI 逻辑尺寸由「面板尺寸 + 旋转」推导。
//   · 面板尺寸 PanelW×PanelH 是玻璃的物理像素（240×240 或 240×320）。
//   · 旋转 90/270 时逻辑画布与面板互换（240×320 → 320×240），
//     否则 UI 会横躺着显示。
//   · MADCTL 只在基址上按 90° 步进异或：ST7789 的四个朝向由
//     {MX,MY,MV} 组合枚举，90° 步 = MX|MV = 0x60。
//
// 优先级：/var/lib/nokeyout/state/screen.json > SCREEN_PANEL_W/H · SCREEN_ROTATION。
// 文件由管理面 POST /api/screen/config 写入；改尺寸或方向会实时重建渲染线程。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
)

// screenCfgVer：每次应用新配置 +1；screen_run 的渲染线程据此重建画布与驱动。
var screenCfgVer atomic.Int64

type screenCfg struct {
	PanelW   int  `json:"panelW"`
	PanelH   int  `json:"panelH"`
	Rotation int  `json:"rotation"` // 0|90|180|270
	Invert   *bool `json:"invert,omitempty"`
}

func screenCfgPath() string {
	return cfgGet("SCREEN_CFG_FILE", "/var/lib/nokeyout/state/screen.json")
}

// defaultScreenCfg：环境变量是出厂/镜像默认值，screen.json 是现场覆盖。
func defaultScreenCfg() screenCfg {
	w, _ := strconv.Atoi(cfgGet("SCREEN_PANEL_W", "240"))
	h, _ := strconv.Atoi(cfgGet("SCREEN_PANEL_H", "240"))
	r, _ := strconv.Atoi(cfgGet("SCREEN_ROTATION", "0"))
	return screenCfg{PanelW: w, PanelH: h, Rotation: r}
}

func loadScreenCfg() screenCfg {
	c := defaultScreenCfg()
	if b, err := os.ReadFile(screenCfgPath()); err == nil {
		var f screenCfg
		if json.Unmarshal(b, &f) == nil {
			if f.PanelW > 0 {
				c.PanelW = f.PanelW
			}
			if f.PanelH > 0 {
				c.PanelH = f.PanelH
			}
			if f.Rotation != 0 || f.PanelW == 0 {
				c.Rotation = f.Rotation
			}
			if f.Invert != nil {
				c.Invert = f.Invert
			}
		}
	}
	c.sanitize()
	return c
}

func (c *screenCfg) sanitize() {
	if c.PanelW < 64 || c.PanelW > 480 {
		c.PanelW = 240
	}
	if c.PanelH < 64 || c.PanelH > 480 {
		c.PanelH = 240
	}
	c.Rotation = ((c.Rotation%360 + 360) % 360)
	if c.Rotation%90 != 0 {
		c.Rotation = 0
	}
}

func (c screenCfg) validate() error {
	if c.PanelW < 64 || c.PanelW > 480 || c.PanelH < 64 || c.PanelH > 480 {
		return fmt.Errorf("面板尺寸超出范围（64..480）")
	}
	if c.Rotation%90 != 0 || c.Rotation < 0 || c.Rotation > 270 {
		return fmt.Errorf("方向只能是 0/90/180/270")
	}
	return nil
}

// logical：UI 画布尺寸。旋转 90/270 时与面板长宽互换。
func (c screenCfg) logical() (int, int) {
	if c.Rotation == 90 || c.Rotation == 270 {
		return c.PanelH, c.PanelW
	}
	return c.PanelW, c.PanelH
}

// rotMask：相对基址旋转 90° 的 MADCTL 增量（MX|MV）。
func rotMask(rot int) byte {
	switch rot {
	case 90:
		return 0x60
	case 180:
		return 0xC0
	case 270:
		return 0xA0
	}
	return 0x00
}

func (c screenCfg) madctl(base int) byte { return byte(base) ^ rotMask(c.Rotation) }

// offsets：90/270 时行列互换，否则窗口起点错位。
func (c screenCfg) offsets(xoff, yoff int) (int, int) {
	if c.Rotation == 90 || c.Rotation == 270 {
		return yoff, xoff
	}
	return xoff, yoff
}

// setScreenGeometry：把逻辑尺寸发布给渲染线程/预览/Web 后端。
func setScreenGeometry(c screenCfg) {
	w, h := c.logical()
	screenW, screenH = w, h
}

// saveScreenCfg：原子落盘。目录不存在就建（panel 用户可写 /var/lib/nokeyout/state）。
func saveScreenCfg(c screenCfg) error {
	p := screenCfgPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
