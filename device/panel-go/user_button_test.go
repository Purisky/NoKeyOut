package main

// user_button_test.go —— USER 按键：配置落盘、动作分发、HTTP 契约、驱动睡眠/唤醒。
// 不碰真 GPIO（sysfs 读取在硬件上验证；这里守住"默认动作/不落盘屏态"的语义）。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestUserButtonConfigStore(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("USER_BUTTON_FILE", filepath.Join(dir, "state", "user-button.json"))

	if got := currentUserButtonAction(); got != userButtonDefaultAction {
		t.Fatalf("缺文件应回默认动作 %q，got %q", userButtonDefaultAction, got)
	}
	if err := saveUserButtonAction("screen_off"); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if got := currentUserButtonAction(); got != "screen_off" {
		t.Fatalf("读回动作 = %q，want screen_off", got)
	}
	// 未知动作不能写进去
	if err := saveUserButtonAction("lock_toggle"); err == nil {
		t.Fatal("未知动作应被拒绝")
	}
	if got := currentUserButtonAction(); got != "screen_off" {
		t.Fatalf("拒绝后不应改动原动作，got %q", got)
	}
	// 文件损坏 → 回到默认动作，按键不能因此失效
	if err := os.WriteFile(filepath.Join(dir, "state", "user-button.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := currentUserButtonAction(); got != userButtonDefaultAction {
		t.Fatalf("损坏配置应回默认动作，got %q", got)
	}
}

func TestUserButtonDispatchesScreenPower(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("USER_BUTTON_FILE", filepath.Join(dir, "user-button.json"))
	p := &panel{}

	// 默认 = 熄屏/亮屏
	p.runUserButtonAction()
	if !p.screenOff.Load() {
		t.Fatal("默认动作第一次应按成熄屏")
	}
	p.runUserButtonAction()
	if p.screenOff.Load() {
		t.Fatal("默认动作第二次应恢复亮屏")
	}

	// 固定熄屏 / 固定亮屏
	if err := saveUserButtonAction("screen_off"); err != nil {
		t.Fatal(err)
	}
	p.runUserButtonAction()
	p.runUserButtonAction()
	if !p.screenOff.Load() {
		t.Fatal("screen_off 应保持熄屏")
	}
	if err := saveUserButtonAction("screen_on"); err != nil {
		t.Fatal(err)
	}
	p.runUserButtonAction()
	if p.screenOff.Load() {
		t.Fatal("screen_on 应亮屏")
	}
	// 无动作
	if err := saveUserButtonAction("none"); err != nil {
		t.Fatal(err)
	}
	p.runUserButtonAction()
	if p.screenOff.Load() {
		t.Fatal("none 不应改变屏态")
	}
}

func TestUserButtonHTTP(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("USER_BUTTON_FILE", filepath.Join(dir, "user-button.json"))
	p := &panel{
		cfg:  &cfg{userButtonGPIO: 138, userButtonActiveLow: true, screenBackend: "st7789"},
		sess: newSessions(60),
	}
	sid := p.sess.create("dev", p.cfg.getSecret())
	cookie := &http.Cookie{Name: "nk_sid", Value: sid}

	// 无会话 → 401
	rec := httptest.NewRecorder()
	p.userButtonHandler(rec, httptest.NewRequest(http.MethodGet, "/api/user-button", nil))
	if rec.Code != 401 {
		t.Fatalf("无会话 code=%d，want 401", rec.Code)
	}

	// GET：默认动作 + 可选项
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/user-button", nil)
	req.AddCookie(cookie)
	p.userButtonHandler(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET code=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Action    string           `json:"action"`
		Default   string           `json:"default"`
		Available []map[string]any `json:"available"`
		Enabled   bool             `json:"enabled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Action != userButtonDefaultAction || got.Default != userButtonDefaultAction {
		t.Fatalf("GET action=%q default=%q", got.Action, got.Default)
	}
	if len(got.Available) != len(userButtonActions) || !got.Enabled {
		t.Fatalf("GET available=%d enabled=%v", len(got.Available), got.Enabled)
	}

	// POST：改动作并落盘
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/user-button",
		jsonBody(t, map[string]any{"action": "screen_toggle"}))
	req.AddCookie(cookie)
	p.userButtonHandler(rec, req)
	if rec.Code != 200 {
		t.Fatalf("POST code=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := currentUserButtonAction(); got != "screen_toggle" {
		t.Fatalf("落盘动作 = %q", got)
	}

	// POST 未知动作 → 400，且原文件不变
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/user-button",
		jsonBody(t, map[string]any{"action": "nope"}))
	req.AddCookie(cookie)
	p.userButtonHandler(rec, req)
	if rec.Code != 400 {
		t.Fatalf("未知动作 code=%d，want 400", rec.Code)
	}
	if got := currentUserButtonAction(); got != "screen_toggle" {
		t.Fatalf("未知动作不应落盘，got %q", got)
	}
}

// 端口开放（ADMIN）期间不允许熄屏；端口在熄屏期间打开则强制亮屏。
func TestScreenOffRuleWhilePortOpen(t *testing.T) {
	var unlocked atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]bool{"locked": !unlocked.Load()})
	}))
	defer srv.Close()
	p := &panel{cfg: &cfg{switchdURL: srv.URL}}

	unlocked.Store(true)
	p.setScreenOff(true)
	if p.screenOff.Load() {
		t.Fatal("端口开放时熄屏应被拒绝")
	}

	unlocked.Store(false)
	p.setScreenOff(true)
	if !p.screenOff.Load() {
		t.Fatal("LOCK 时仍应允许熄屏")
	}

	// 熄屏后端口开放：阻塞中的熄屏等待必须自己醒来。
	done := make(chan struct{})
	go func() {
		p.screenPause(nil, newCanvas(screenW, screenH))
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	unlocked.Store(true)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("端口开放后屏幕没有强制亮屏")
	}
	if p.screenOff.Load() {
		t.Fatal("端口开放后 screenOff 应被清除")
	}
}

// 渲染快照必须带上端口开放计时；LOCK 时不带。
func TestScreenStateIncludesAdminTimer(t *testing.T) {
	var unlocked atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]bool{"locked": !unlocked.Load()})
	}))
	defer srv.Close()
	c := &cfg{switchdURL: srv.URL}
	p := &panel{cfg: c, stats: &stats{cfg: c}}

	unlocked.Store(true)
	snap := p.screenState()
	if _, ok := snapF(snap, "admin_s"); !ok {
		t.Fatal("端口开放时快照缺 admin_s")
	}
	unlocked.Store(false)
	if _, ok := snapF(p.screenState(), "admin_s"); ok {
		t.Fatal("LOCK 时不应带 admin_s")
	}
}

// ST7789 熄屏/亮屏的命令流（真屏上就是 DISPOFF+SLPIN / SLPOUT+DISPON）。
func TestST7789SleepWakeStream(t *testing.T) {
	bus := &memSpi{}
	st := newST7789(bus, memPin{}, nil, 0x70, 0, 0, false)
	if err := st.sleep(); err != nil {
		t.Fatal(err)
	}
	if err := st.wake(); err != nil {
		t.Fatal(err)
	}
	var cmds [][]byte
	for _, r := range bus.rec {
		if !r.dc {
			cmds = append(cmds, r.buf)
		}
	}
	want := [][]byte{{0x28}, {0x10}, {0x11}, {0x29}}
	if len(cmds) != len(want) {
		t.Fatalf("命令数 = %d，want %d: %x", len(cmds), len(want), cmds)
	}
	for i := range want {
		if len(cmds[i]) != 1 || cmds[i][0] != want[i][0] {
			t.Fatalf("命令[%d] = %x，want %x", i, cmds[i], want[i])
		}
	}
}

// screenPause：熄屏 -> CRT 关机动画 -> SPI 睡眠；亮屏 -> 唤醒。用假总线验证命令顺序。
func TestScreenPauseSleepsAndWakes(t *testing.T) {
	bus := &memSpi{}
	st := newST7789(bus, memPin{}, nil, 0x70, 0, 0, false)
	p := &panel{cfg: &cfg{}}
	c := newCanvas(screenW, screenH)
	c.fillRect(0, 0, screenW, screenH, screenBG)
	p.screenOff.Store(true)
	done := make(chan struct{})
	go func() {
		p.screenPause(st, c)
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	p.setScreenOff(false)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("亮屏后 screenPause 未退出")
	}
	var cmds [][]byte
	for _, r := range bus.rec {
		if !r.dc && len(r.buf) == 1 {
			switch r.buf[0] {
			case 0x28, 0x10, 0x11, 0x29: // DISPOFF / SLPIN / SLPOUT / DISPON
				cmds = append(cmds, r.buf)
			}
		}
	}
	want := [][]byte{{0x28}, {0x10}, {0x11}, {0x29}}
	if len(cmds) != len(want) {
		t.Fatalf("命令数 = %d，want %d: %x", len(cmds), len(want), cmds)
	}
	for i := range want {
		if len(cmds[i]) != 1 || cmds[i][0] != want[i][0] {
			t.Fatalf("命令[%d] = %x，want %x", i, cmds[i], want[i])
		}
	}
	// 关机动画必须真的推过帧（不是直接 sleep）：至少推满一屏像素。
	total := 0
	for _, r := range bus.rec {
		total += len(r.buf)
	}
	if total < screenW*screenH*2 {
		t.Fatalf("熄屏动画没有推帧：总线只写了 %d 字节", total)
	}
}

func jsonBody(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b)
}
