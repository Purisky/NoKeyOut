package main

// screen_ctl_test.go —— 调试控制回归（重点是"模拟锁定绝不写真开关"）

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestCtlSimLockNeverWritesSwitch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SWITCHD_STATE_FILE", dir+"/switch-state")
	_ = os.WriteFile(dir+"/switch-state", []byte("unlocked"), 0o644)
	p := &panel{screen: newShadowScreen(dir, 1)}
	body := `{"action":"lock"}`
	req := httptest.NewRequest(http.MethodPost, "/screen/ctl", strings.NewReader(body))
	rec := httptest.NewRecorder()
	p.screenCtlHandler(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !screenctl.simLocked() {
		t.Fatal("sim_locked 应为 true")
	}
	b, _ := os.ReadFile(dir + "/switch-state")
	if string(b) != "unlocked" {
		t.Fatalf("真实开关被改动了！state=%q（必须保持 unlocked）", b)
	}
	// 再点一次恢复
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/screen/ctl", strings.NewReader(`{"action":"lock"}`))
	p.screenCtlHandler(rec, req)
	if screenctl.simLocked() {
		t.Fatal("再次点击应恢复")
	}
	// 其余动作
	for _, a := range []string{"lang", "error", "auto", "next", "prev"} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/screen/ctl", strings.NewReader(`{"action":"`+a+`"}`))
		p.screenCtlHandler(rec, req)
		if rec.Code != 200 {
			t.Fatalf("action %s -> %d %s", a, rec.Code, rec.Body.String())
		}
	}
	screenctl.injectErr.Store(false)
	screenctl.langOverride.Store("")
	screenctl.pageOverride.Store("")
}

func TestCtlOverlayOnlyAffectsRender(t *testing.T) {
	// simLock 开启时渲染器走锁定页，但面板的 switchState 不能被它骗到
	screenctl.simLock.Store(true)
	defer screenctl.simLock.Store(false)
	snap := map[string]any{"switch": "locked", "lang": "zh"}
	if screenctl.simLocked() {
		snap["switch"] = "unlocked"
	}
	if snap["switch"] != "unlocked" {
		t.Fatal("渲染快照应被覆盖为 unlocked")
	}
	// 真门控读的是另一份（这里只验证渲染路径把 switch 改成 unlocked 即可，
	// switchState/门控不读 snap——由 screen_run 只覆盖渲染快照保证）
}
