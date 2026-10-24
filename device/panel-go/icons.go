//go:build !(js && wasm)

package main

// icons.go —— 面板图标缓存

import (
	"sync"
	"time"
)

type iconCache struct {
	mu sync.Mutex
	d  map[string]iconEntry
}

type iconEntry struct {
	body []byte
	ts   time.Time
}

func newIconCache() *iconCache { return &iconCache{d: map[string]iconEntry{}} }

var genericSVG = []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" ` +
	`stroke="currentColor" stroke-width="1.4" stroke-linecap="round" ` +
	`stroke-linejoin="round"><path d="M8 2.2 13.2 5.1v5.8L8 13.8 2.8 10.9V5.1Z ` +
	`M8 8v5.8 M2.8 5.1 8 8l5.2-2.9"/></svg>`)
