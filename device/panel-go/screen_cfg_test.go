package main

import "testing"

// 面板尺寸 × 方向 → 逻辑画布 / MADCTL / 窗口偏移。
// MADCTL 以 0x70 为基址：90° 步 = MX|MV（0x60）异或。
func TestScreenCfgGeometry(t *testing.T) {
	cases := []struct {
		pw, ph, rot int
		lw, lh      int
		mask        byte
	}{
		{240, 240, 0, 240, 240, 0x00},
		{240, 240, 90, 240, 240, 0x60},
		{240, 240, 180, 240, 240, 0xC0},
		{240, 240, 270, 240, 240, 0xA0},
		{240, 320, 0, 240, 320, 0x00},
		{240, 320, 90, 320, 240, 0x60},
		{240, 320, 180, 240, 320, 0xC0},
		{240, 320, 270, 320, 240, 0xA0},
	}
	for _, c := range cases {
		cfg := screenCfg{PanelW: c.pw, PanelH: c.ph, Rotation: c.rot}
		if lw, lh := cfg.logical(); lw != c.lw || lh != c.lh {
			t.Errorf("%dx%d rot=%d: logical = %dx%d, want %dx%d", c.pw, c.ph, c.rot, lw, lh, c.lw, c.lh)
		}
		if got, want := cfg.madctl(0x70), byte(0x70)^c.mask; got != want {
			t.Errorf("%dx%d rot=%d: madctl = 0x%02X, want 0x%02X", c.pw, c.ph, c.rot, got, want)
		}
		xo, yo := cfg.offsets(3, 7)
		if c.rot == 90 || c.rot == 270 {
			if xo != 7 || yo != 3 {
				t.Errorf("rot=%d: offsets = %d,%d, want 7,3", c.rot, xo, yo)
			}
		} else if xo != 3 || yo != 7 {
			t.Errorf("rot=%d: offsets = %d,%d, want 3,7", c.rot, xo, yo)
		}
	}
}

func TestScreenCfgValidate(t *testing.T) {
	good := []screenCfg{
		{PanelW: 240, PanelH: 240, Rotation: 0},
		{PanelW: 240, PanelH: 320, Rotation: 270},
	}
	for _, c := range good {
		if err := c.validate(); err != nil {
			t.Errorf("%+v: unexpected error %v", c, err)
		}
	}
	bad := []screenCfg{
		{PanelW: 0, PanelH: 240},
		{PanelW: 240, PanelH: 999},
		{PanelW: 240, PanelH: 240, Rotation: 45},
		{PanelW: 240, PanelH: 240, Rotation: -90},
	}
	for _, c := range bad {
		if err := c.validate(); err == nil {
			t.Errorf("%+v: expected validation error", c)
		}
	}
}

func TestScreenCfgSanitize(t *testing.T) {
	c := screenCfg{PanelW: 0, PanelH: 1000, Rotation: 450}
	c.sanitize()
	if c.PanelW != 240 || c.PanelH != 240 {
		t.Errorf("size sanitize = %dx%d, want 240x240", c.PanelW, c.PanelH)
	}
	if c.Rotation != 90 {
		t.Errorf("rotation 450 should normalize to 90, got %d", c.Rotation)
	}
	c = screenCfg{PanelW: 240, PanelH: 320, Rotation: 45}
	c.sanitize()
	if c.Rotation != 0 {
		t.Errorf("non-multiple rotation should fall back to 0, got %d", c.Rotation)
	}
}
