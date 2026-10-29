//go:build !linux

package main

// screen_spi_other.go —— 非 Linux 平台 stub：让 go vet/test 在开发机上也能编译
// （真机屏只剩 screen_spi_linux.go 的 spidev 路径）。

import (
	"fmt"
	"os"
)

const (
	spiDefaultHz  = uint32(40_000_000)
	spiDefaultDev = "/dev/spidev1.1"
)

type spiBus struct{ f *os.File }

func openSPI(dev string, hz uint32) (*spiBus, error) {
	return nil, fmt.Errorf("spidev 仅在 Linux 可用（%s）", dev)
}

func (s *spiBus) write([]byte) error { return nil }

func (s *spiBus) close() {}
