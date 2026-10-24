//go:build !(js && wasm)

package main

// screen_ctl.go —— 虚拟屏调试控制（/screen/ctl）
// 无硬件调屏时用来切语言/切页/注入统计异常；影响仅限屏渲染（不动真开关、不动数据）。

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
)

type screenCtl struct {
	pageOverride atomic.Value // string：空=跟随轮播
	langOverride atomic.Value // string：空=跟随快照
	injectErr    atomic.Bool
	simLock      atomic.Bool // 仅画面模拟锁定（**不写真实开关**）
}

var screenctl = &screenCtl{}

func init() {
	screenctl.pageOverride.Store("")
	screenctl.langOverride.Store("")
}

func (c *screenCtl) page() string {
	v, _ := c.pageOverride.Load().(string)
	return v
}

func (c *screenCtl) lang(snap map[string]any) string {
	if v, _ := c.langOverride.Load().(string); v != "" {
		return v
	}
	return snapS(snap, "lang")
}

func (c *screenCtl) injected() bool { return c.injectErr.Load() }

func (c *screenCtl) simLocked() bool { return c.simLock.Load() }

func (c *screenCtl) apply(action string) string {
	switch action {
	case "auto":
		c.pageOverride.Store("")
	case "lang":
		cur, _ := c.langOverride.Load().(string)
		if cur == "en" {
			c.langOverride.Store("zh")
		} else {
			c.langOverride.Store("en")
		}
	case "error":
		c.injectErr.Store(!c.injectErr.Load())
	case "lock":
		// 只切"画面锁定"：真锁一旦写下，持有解锁页的 8443 就被门控关掉，
		// POST 127.0.0.1:8799/v1/state 或物理开关；这里绝不碰。
		c.simLock.Store(!c.simLock.Load())
	}
	return c.page()
}

// cfgScreensDir：ctl 需要知道用户页目录（配置在 panel 上，这里取进程级单例）
var screensDirFn = func() string { return "/var/lib/nokeyout/screens" }

func cfgScreensDir() string {
	if p := panelInstance; p != nil {
		return p.cfg.screensDir
	}
	return screensDirFn()
}

var panelInstance *panel

func (p *panel) screenCtlHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Action == "" {
		p.errJSON(w, r, 400, "missing action")
		return
	}
	p.json(w, r, 200, map[string]any{
		"ok": true, "page": screenctl.apply(body.Action),
		"lang": func() string {
			if v, _ := screenctl.langOverride.Load().(string); v != "" {
				return v
			}
			return "auto"
		}(),
		"error_injected": screenctl.injected(),
		"sim_locked":     screenctl.simLocked(),
	}, nil)
}
