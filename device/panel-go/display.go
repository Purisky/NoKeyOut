//go:build !(js && wasm)

package main

// display.go —— 管理面/小屏共用的显示偏好：账号 ID 脱敏 + 时区。
//
// 一个文件、一份真相：Web 保存后写 /var/lib/nokeyout/state/display.json，
// 快照（/api/stats 与 /v1/stats/snapshot）把结果带给小屏渲染线程，
// 两边不需要各自再读一次文件。

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type displayCfg struct {
	MaskAccounts bool   `json:"mask_accounts"`
	Timezone     string `json:"timezone"` // IANA 名称；空 = 跟随系统
	// DeepSeekPeak：可选的 DeepSeek 峰价提醒。nil = 默认开启；显式 false = 关闭。
	DeepSeekPeak *bool `json:"deepseek_peak,omitempty"`
}

func (d displayCfg) deepseekPeakOn() bool {
	return d.DeepSeekPeak == nil || *d.DeepSeekPeak
}

func displayCfgPath() string {
	return cfgGet("DISPLAY_CFG_FILE", "/var/lib/nokeyout/state/display.json")
}

func (d displayCfg) sanitize() displayCfg {
	d.Timezone = strings.TrimSpace(d.Timezone)
	if d.Timezone == "Local" || d.Timezone == "system" {
		d.Timezone = ""
	}
	return d
}

func (d displayCfg) validate() error {
	if d.Timezone == "" {
		return nil
	}
	_, err := time.LoadLocation(d.Timezone)
	return err
}

func loadDisplayCfg() displayCfg {
	b, err := os.ReadFile(displayCfgPath())
	if err != nil {
		return displayCfg{}
	}
	var d displayCfg
	if json.Unmarshal(b, &d) != nil {
		return displayCfg{}
	}
	d = d.sanitize()
	if d.validate() != nil {
		d.Timezone = ""
	}
	return d
}

func saveDisplayCfg(d displayCfg) error {
	d = d.sanitize()
	if err := d.validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	p := displayCfgPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// tzCache：渲染每帧都会问一次时区，按名字缓存 *time.Location。
var tzCache struct {
	sync.Mutex
	m map[string]*time.Location
}

func loadLocation(name string) *time.Location {
	if name == "" {
		return time.Local
	}
	tzCache.Lock()
	defer tzCache.Unlock()
	if tzCache.m == nil {
		tzCache.m = map[string]*time.Location{}
	}
	if loc, ok := tzCache.m[name]; ok {
		return loc
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		loc = time.Local
	}
	tzCache.m[name] = loc
	return loc
}

// displayFromSnap：渲染线程从快照取显示偏好（预览/测试缺字段时退回默认）。
func displayFromSnap(snap map[string]any) displayCfg {
	m, _ := snap["display"].(map[string]any)
	if m == nil {
		return displayCfg{}
	}
	d := displayCfg{MaskAccounts: snapB(m, "mask_accounts")}
	d.Timezone = snapS(m, "timezone")
	if v, ok := m["deepseek_peak"].(bool); ok {
		d.DeepSeekPeak = &v
	}
	return d
}

func (p *panel) displayHandler(w http.ResponseWriter, r *http.Request) {
	if p.user(r) == "" {
		p.errJSON(w, r, 401, "no session")
		return
	}
	switch r.Method {
	case http.MethodGet:
		d := loadDisplayCfg()
		p.json(w, r, 200, map[string]any{
			"mask_accounts": d.MaskAccounts,
			"timezone":      d.Timezone,
			"tz":            loadLocation(d.Timezone).String(),
			"deepseek_peak": d.deepseekPeakOn(),
			"zones":         displayZones,
		}, nil)
	case http.MethodPost:
		var body displayCfg
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			p.errJSON(w, r, 400, "请求体不是合法 JSON")
			return
		}
		if err := saveDisplayCfg(body); err != nil {
			p.errJSON(w, r, 400, "时区无效: "+err.Error())
			return
		}
		d := loadDisplayCfg()
		p.json(w, r, 200, map[string]any{
			"ok":            true,
			"mask_accounts": d.MaskAccounts,
			"timezone":      d.Timezone,
			"tz":            loadLocation(d.Timezone).String(),
			"deepseek_peak": d.deepseekPeakOn(),
		}, nil)
	default:
		p.errJSON(w, r, 405, "只支持 GET / POST")
	}
}

// displayZones：设置下拉里给出的常用时区（空值 = 跟随系统，由前端补）。
var displayZones = []map[string]string{
	{"id": "Asia/Shanghai", "label": "Asia/Shanghai (CST)"},
	{"id": "Asia/Tokyo", "label": "Asia/Tokyo (JST)"},
	{"id": "Asia/Singapore", "label": "Asia/Singapore (SGT)"},
	{"id": "Europe/London", "label": "Europe/London (GMT/BST)"},
	{"id": "Europe/Berlin", "label": "Europe/Berlin (CET)"},
	{"id": "America/New_York", "label": "America/New_York (ET)"},
	{"id": "America/Los_Angeles", "label": "America/Los_Angeles (PT)"},
	{"id": "UTC", "label": "UTC"},
}
