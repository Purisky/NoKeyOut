//go:build !(js && wasm)

package main

// screen_st7789.go —— ST7789 240×240 驱动（核心逻辑）
//
// 真件依赖在平台文件里：screen_spi_linux.go（spidev ioctl）/ screen_spi_other.go（非 Linux  stub）。
// GPIO 走 sysfs（/sys/class/gpio）：CGO_ENABLED=0 可静态链接；libgpiod 需要 cgo，不选它。
//
// 接线：
//   CLK=pin23 MOSI=pin19 CS=pin22(→/dev/spidev1.1) DC=pin15(GPIO4_A0=128) RST=pin13(GPIO4_B4=140)

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type byteSink interface{ write(b []byte) }
type pinSetter interface{ set(on bool) }

type st7789 struct {
	spi    byteSink
	dc     pinSetter
	rst    pinSetter
	madctl byte
	xoff   int
	yoff   int
	invert bool
}

func newST7789(spi byteSink, dc, rst pinSetter, madctl, xoff, yoff int, invert bool) *st7789 {
	return &st7789{spi: spi, dc: dc, rst: rst, madctl: byte(madctl), xoff: xoff, yoff: yoff, invert: invert}
}

func (s *st7789) cmd(b ...byte) {
	s.dc.set(false)
	s.spi.write(b)
}

func (s *st7789) data(b []byte) {
	s.dc.set(true)
	s.spi.write(b)
}

func (s *st7789) reset() {
	if s.rst == nil {
		return
	}
	s.rst.set(false)
	time.Sleep(10 * time.Millisecond)
	s.rst.set(true)
	time.Sleep(120 * time.Millisecond)
}

// init：240×240 IPS 常用初始化序列（与 Python 版逐字节一致）。
// 花屏时调整 madctl/偏移/invert。
func (s *st7789) init() {
	s.reset()
	s.cmd(0x11) // SLPOUT
	time.Sleep(120 * time.Millisecond)
	s.cmd(0x3A, 0x55)                         // COLMOD 16bit
	s.cmd(0x36, s.madctl)                     // MADCTL
	s.cmd(0xB2, 0x0C, 0x0C, 0x00, 0x33, 0x33) // PORCTRL
	s.cmd(0xB7, 0x35)                         // GCTRL
	s.cmd(0xBB, 0x1F)                         // VCOMS
	s.cmd(0xC0, 0x2C)                         // LCMCTRL
	s.cmd(0xC2, 0x01)                         // VDVVRHEN
	s.cmd(0xC3, 0x11)                         // VRHS
	s.cmd(0xC4, 0x20)                         // VDVSET
	s.cmd(0xC6, 0x0F)                         // FRCTRL2
	s.cmd(0xD0, 0xA4, 0xA1)                   // PWCTRL1
	s.cmd(0xE0, 0xD0, 0x08, 0x0E, 0x09, 0x09, 0x05, 0x31,
		0x33, 0x48, 0x17, 0x14, 0x15, 0x31, 0x34) // PVGAMCTRL
	s.cmd(0xE1, 0xF0, 0x0E, 0x12, 0x0C, 0x0B, 0x04, 0x16,
		0x33, 0x45, 0x13, 0x13, 0x15, 0x18, 0x17) // NVGAMCTRL
	if s.invert {
		s.cmd(0x21) // INVON
	} else {
		s.cmd(0x20) // INVOFF
	}
	s.cmd(0x29) // DISPON
	time.Sleep(20 * time.Millisecond)
}

func (s *st7789) window(x0, y0, x1, y1 int) {
	xs, xe := x0+s.xoff, x1+s.xoff
	ys, ye := y0+s.yoff, y1+s.yoff
	s.cmd(0x2A, byte(xs>>8), byte(xs), byte(xe>>8), byte(xe)) // CASET
	s.cmd(0x2B, byte(ys>>8), byte(ys), byte(ye>>8), byte(ye)) // RASET
}

// pushRect：Canvas.flush() 的推送目标；data 为线上字节序矩形像素。
func (s *st7789) pushRect(x0, y0, x1, y1 int, data []byte) error {
	s.window(x0, y0, x1, y1)
	s.cmd(0x2C) // RAMWR
	s.data(data)
	return nil
}

func (s *st7789) selftest() error {
	c565 := func(r, g, b uint8) uint16 { return rgb565(r, g, b) }
	colors := []uint16{c565(0xE5, 0x53, 0x4B), c565(0x3F, 0xB2, 0x7F), c565(0x4C, 0x8D, 0xFF), c565(0xFF, 0xFF, 0xFF)}
	band := 60
	for i, col := range colors {
		s.window(0, i*band, screenW-1, (i+1)*band-1)
		s.cmd(0x2C)
		row := []byte{byte(col >> 8), byte(col)}
		s.data(repeat2(row, screenW*band))
	}
	return nil
}

// sleep：DISPOFF + SLPIN——显示关闭、控制器进睡眠。RAM 内容保留，唤醒后不必重推全屏。
func (s *st7789) sleep() error {
	s.cmd(0x28) // DISPOFF
	s.cmd(0x10) // SLPIN
	return nil
}

// wake：SLPOUT + DISPON——从睡眠恢复，准备继续推帧（SLPOUT 后给控制器 120ms 稳定）。
func (s *st7789) wake() error {
	s.cmd(0x11) // SLPOUT
	time.Sleep(120 * time.Millisecond)
	s.cmd(0x29) // DISPON
	return nil
}

func (s *st7789) close() {
	if c, ok := s.spi.(interface{ close() }); ok {
		c.close()
	}
}

// ---------------------------------------------------------------- sysfs GPIO

type gpioOut struct {
	line int
	vf   *os.File
}

// openGPIO：line 为全局 GPIO 号（bank×32+组×8+序；GPIO4_A0=128）。
func openGPIO(line int) (*gpioOut, error) {
	base := fmt.Sprintf("/sys/class/gpio/gpio%d", line)
	if _, err := os.Stat(base); err != nil {
		if err := os.WriteFile("/sys/class/gpio/export", []byte(strconv.Itoa(line)), 0o200); err != nil {
			return nil, fmt.Errorf("gpio %d export: %w", line, err)
		}
		// udev 赋权/创建节点可能有毫秒级延迟
		for range 50 {
			if _, err := os.Stat(base); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if err := os.WriteFile(base+"/direction", []byte("out"), 0o200); err != nil {
		return nil, fmt.Errorf("gpio %d direction: %w", line, err)
	}
	vf, err := os.OpenFile(base+"/value", os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("gpio %d value: %w", line, err)
	}
	return &gpioOut{line: line, vf: vf}, nil
}

func (g *gpioOut) set(on bool) {
	v := "0"
	if on {
		v = "1"
	}
	_, _ = g.vf.WriteAt([]byte(v), 0)
}

func (g *gpioOut) close() {
	if g != nil && g.vf != nil {
		_ = g.vf.Close()
		_ = os.WriteFile("/sys/class/gpio/unexport", []byte(strconv.Itoa(g.line)), 0o200)
	}
}

func repeat2(pair []byte, n int) []byte {
	out := make([]byte, 0, n*2)
	for range n {
		out = append(out, pair...)
	}
	return out
}
