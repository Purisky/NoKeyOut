//go:build linux

package main

// screen_spi_linux.go —— spidev 打开与写入（仅 Linux；CGO_ENABLED=0 静态可用）

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	spiIOCWrMode     = 0x40016B01
	spiIOCWrMaxSpeed = 0x40046B04
	spiDefaultHz     = uint32(40_000_000)
	spiDefaultDev    = "/dev/spidev1.1"
)

type spiBus struct{ f *os.File }

func openSPI(dev string, hz uint32) (*spiBus, error) {
	f, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	var mode byte
	if err := ioctl(f.Fd(), spiIOCWrMode, unsafe.Pointer(&mode)); err != nil {
		f.Close()
		return nil, fmt.Errorf("SPI mode: %w", err)
	}
	speed := hz
	if err := ioctl(f.Fd(), spiIOCWrMaxSpeed, unsafe.Pointer(&speed)); err != nil {
		f.Close()
		return nil, fmt.Errorf("SPI speed: %w", err)
	}
	return &spiBus{f: f}, nil
}

// spidev 默认 bufsiz=4096：单次 write 超过会 EMSGSIZE，按 4096 分块写。
const spiChunk = 4096

func (s *spiBus) write(b []byte) {
	for len(b) > 0 {
		n := len(b)
		if n > spiChunk {
			n = spiChunk
		}
		if _, err := s.f.Write(b[:n]); err != nil {
			return
		}
		b = b[n:]
	}
}

func (s *spiBus) close() { _ = s.f.Close() }

func ioctl(fd, req uintptr, arg unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}
