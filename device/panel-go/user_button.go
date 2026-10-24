//go:build !(js && wasm)

package main

// user_button.go —— 主板 USER 按键
//
// 板载 USER 键 = K2_USR = GPIO4_B2 = 全局号 138（按下短接到 GND）。默认动作是
// **熄屏 / 亮屏**；动作映射可在 管理面 → 系统 → 用户按键 里改（落盘到
// USER_BUTTON_FILE，默认 /var/lib/nokeyout/state/user-button.json）。
//
// 屏幕开关状态本身**绝不落盘**：每次重启/上电一律以亮屏启动。
// 按键读取走 sysfs 轮询 + 去抖（不依赖 libgpiod / CGO）；导出与授权由
// nokeyout-user-button-gpio.service 在 panel 启动前完成。

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	userButtonDefaultAction = "screen_toggle"
	userButtonMaxBody       = 4 << 10
)

// buttonAction：一个可分配给 USER 键的动作。新增动作只需往 userButtonActions 里加一项，
// Web 下拉框与校验都自动跟随（当前只提供屏幕电源类动作 + 禁用）。
type buttonAction struct {
	ID      string
	LabelZh string
	LabelEn string
	Run     func(p *panel)
}

var userButtonActions = []buttonAction{
	{
		ID: "screen_toggle", LabelZh: "熄屏 / 亮屏", LabelEn: "Screen off / on",
		Run: func(p *panel) { p.setScreenOff(!p.screenOff.Load()) },
	},
	{
		ID: "screen_off", LabelZh: "熄屏", LabelEn: "Screen off",
		Run: func(p *panel) { p.setScreenOff(true) },
	},
	{
		ID: "screen_on", LabelZh: "亮屏", LabelEn: "Screen on",
		Run: func(p *panel) { p.setScreenOff(false) },
	},
	{
		ID: "none", LabelZh: "无动作", LabelEn: "Do nothing",
		Run: func(p *panel) {},
	},
}

func userButtonActionByID(id string) (buttonAction, bool) {
	for _, a := range userButtonActions {
		if a.ID == id {
			return a, true
		}
	}
	return buttonAction{}, false
}

func userButtonFile() string {
	return cfgGet("USER_BUTTON_FILE", "/var/lib/nokeyout/state/user-button.json")
}

// currentUserButtonAction：每次按键现读文件（按键频率极低，不值得做缓存）。
// 文件缺失/损坏/未知动作 → 回到默认动作，绝不因为配置坏掉让按键失效。
func currentUserButtonAction() string {
	b, err := os.ReadFile(userButtonFile())
	if err != nil {
		return userButtonDefaultAction
	}
	var doc struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return userButtonDefaultAction
	}
	if _, ok := userButtonActionByID(doc.Action); !ok {
		return userButtonDefaultAction
	}
	return doc.Action
}

// saveUserButtonAction：原子写（临时文件 + rename），调用方负责会话鉴权与校验。
func saveUserButtonAction(action string) error {
	if _, ok := userButtonActionByID(action); !ok {
		return fmt.Errorf("未知动作 %q", action)
	}
	b, err := json.MarshalIndent(struct {
		Action string `json:"action"`
	}{Action: action}, "", "  ")
	if err != nil {
		return err
	}
	p := userButtonFile()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (p *panel) setScreenOff(off bool) {
	// 规则：管理面端口开放（ADMIN）期间屏幕必须常亮。拨回 LOCK 后才能熄屏。
	if off && p.portOpen() {
		log.Printf("screen: 管理面端口开放中——拒绝熄屏（拨回 LOCK 后才能熄屏）")
		return
	}
	was := p.screenOff.Swap(off)
	if was == off {
		return
	}
	if off {
		log.Printf("screen: USER/Web 熄屏（仅本次运行；重启/上电默认亮屏）")
	} else {
		log.Printf("screen: USER/Web 亮屏")
	}
}

// readUserButtonPressed：sysfs 读值。activeLow=按下短接 GND（板载 K2_USR 的极性）。
func readUserButtonPressed(line int, activeLow bool) (bool, error) {
	b, err := os.ReadFile(fmt.Sprintf("/sys/class/gpio/gpio%d/value", line))
	if err != nil {
		return false, err
	}
	v := strings.TrimSpace(string(b))
	if v != "0" && v != "1" {
		return false, fmt.Errorf("gpio %d value=%q", line, v)
	}
	return (v == "0") == activeLow, nil
}

// startUserButton：GPIO 不可读时只关闭按键功能并记一条原因，不影响 panel/屏幕。
func (p *panel) startUserButton() {
	line := p.cfg.userButtonGPIO
	if line < 0 {
		log.Printf("button: USER 键未启用（USER_BUTTON_GPIO=-1）")
		return
	}
	if _, err := readUserButtonPressed(line, p.cfg.userButtonActiveLow); err != nil {
		log.Printf("button: USER 键 GPIO %d 不可读（%v）——按键功能关闭"+
			"（确认 nokeyout-user-button-gpio.service 已导出并授权）", line, err)
		return
	}
	poll := time.Duration(max(p.cfg.userButtonPollMs, 10)) * time.Millisecond
	debounce := time.Duration(max(p.cfg.userButtonDebounce, 0)) * time.Millisecond
	go p.userButtonLoop(line, p.cfg.userButtonActiveLow, poll, debounce)
	log.Printf("button: USER 键就绪（GPIO %d，动作=%s）", line, currentUserButtonAction())
}

// userButtonLoop：轮询 + 去抖。只有"稳定按下"的边沿触发一次动作；
// 按住不动不会连发。读失败只记一次并退出（面板其余功能不受影响）。
func (p *panel) userButtonLoop(line int, activeLow bool, poll, debounce time.Duration) {
	stable, err := readUserButtonPressed(line, activeLow)
	if err != nil {
		log.Printf("button: USER 键读取失败（%v）——按键功能关闭", err)
		return
	}
	raw := stable
	changed := time.Now()
	for {
		time.Sleep(poll)
		cur, err := readUserButtonPressed(line, activeLow)
		if err != nil {
			log.Printf("button: USER 键读取失败（%v）——按键功能关闭", err)
			return
		}
		if cur != raw {
			raw, changed = cur, time.Now()
			continue
		}
		if raw == stable {
			continue
		}
		if debounce > 0 && time.Since(changed) < debounce {
			continue
		}
		stable = raw
		if stable {
			p.runUserButtonAction()
		}
	}
}

func (p *panel) runUserButtonAction() {
	id := currentUserButtonAction()
	a, ok := userButtonActionByID(id)
	if !ok {
		a, _ = userButtonActionByID(userButtonDefaultAction)
	}
	log.Printf("button: USER 键按下 → %s", a.ID)
	if a.Run != nil {
		a.Run(p)
	}
}

// userButtonHandler：GET 读配置/当前屏态；POST 改动作映射。
func (p *panel) userButtonHandler(w http.ResponseWriter, r *http.Request) {
	if p.user(r) == "" {
		p.errJSON(w, r, 401, "no session")
		return
	}
	switch r.Method {
	case http.MethodGet:
		available := make([]map[string]any, 0, len(userButtonActions))
		for _, a := range userButtonActions {
			available = append(available, map[string]any{
				"id": a.ID,
				"label": map[string]string{
					"zh": a.LabelZh,
					"en": a.LabelEn,
				},
			})
		}
		p.json(w, r, 200, map[string]any{
			"action":         currentUserButtonAction(),
			"default":        userButtonDefaultAction,
			"available":      available,
			"gpio":           p.cfg.userButtonGPIO,
			"enabled":        p.cfg.userButtonGPIO >= 0,
			"active_low":     p.cfg.userButtonActiveLow,
			"screen_off":     p.screenOff.Load(),
			"screen_backend": p.cfg.screenBackend,
		}, nil)
	case http.MethodPost:
		var body struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, userButtonMaxBody)).Decode(&body); err != nil {
			p.errJSON(w, r, 400, "请求体不是合法 JSON")
			return
		}
		if _, ok := userButtonActionByID(body.Action); !ok {
			p.errJSON(w, r, 400, "未知动作")
			return
		}
		if err := saveUserButtonAction(body.Action); err != nil {
			p.errJSON(w, r, 500, "保存失败: "+err.Error())
			return
		}
		log.Printf("button: USER 键动作已更新 → %s", body.Action)
		p.json(w, r, 200, map[string]any{"ok": true, "action": body.Action}, nil)
	default:
		p.errJSON(w, r, 405, "只支持 GET / POST")
	}
}
