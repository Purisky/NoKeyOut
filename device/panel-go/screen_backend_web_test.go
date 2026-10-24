package main

// screen_backend_web_test.go —— 虚拟屏（无硬件 PC 调试）回归

import (
	"bytes"
	"image"
	_ "image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestShadowScreenPushAndState(t *testing.T) {
	sc := newShadowScreen(t.TempDir(), 2)
	if st := sc.state(); st["version"].(int64) != 0 {
		t.Fatal("新虚拟屏版本应为 0")
	}
	c := newCanvas(screenW, screenH)
	uiRender("main", testSnap(), histFromGolden(nil), c, 0.5, "zh", "12:34", animState{})
	sc.setPage("main", "zh")
	if !c.flush(sc) {
		t.Fatal("首帧应推送")
	}
	st := sc.state()
	if st["pushes"].(int64) != 1 || st["version"].(int64) != 1 || st["page"].(string) != "main" {
		t.Fatalf("state 不对: %v", st)
	}
	if st["last_push_bytes"].(int64) != 115200 {
		t.Fatalf("首帧应整幅 115200B，got %v", st["last_push_bytes"])
	}
	// 只改一个像素 → 只推 2 字节（差分语义与真机一致）
	c.pixel(100, 100, screenDanger)
	if !c.flush(sc) {
		t.Fatal("像素变更应推送")
	}
	if got := sc.state()["last_push_bytes"].(int64); got != 2 {
		t.Fatalf("单像素应推 2B，got %d", got)
	}
	// PNG 尺寸 = scale
	pngBytes := sc.framePNG()
	img, _, err := image.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("framePNG 不可解码: %v", err)
	}
	if img.Bounds().Dx() != screenW*2 || img.Bounds().Dy() != screenH*2 {
		t.Fatalf("PNG 尺寸应为 %dx%d，got %dx%d", screenW*2, screenH*2, img.Bounds().Dx(), img.Bounds().Dy())
	}
	if len(sc.frameJPEG()) == 0 {
		t.Fatal("frameJPEG 为空")
	}
}

func TestShadowScreenHandlers(t *testing.T) {
	sc := newShadowScreen(t.TempDir(), 1)
	c := newCanvas(screenW, screenH)
	uiRender("main", testSnap(), histFromGolden(nil), c, 0, "zh", "12:34", animState{})
	_ = c.flush(sc)
	p := &panel{screen: sc}

	// /screen/frame.png
	rec := httptest.NewRecorder()
	p.screenHandlers(rec, httptest.NewRequest(http.MethodGet, "/screen/frame.png", nil), "/screen/frame.png")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("frame.png: code=%d ct=%s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if img, _, err := image.Decode(bytes.NewReader(rec.Body.Bytes())); err != nil || img.Bounds().Dx() != screenW {
		t.Fatalf("frame.png 内容不对: %v", err)
	}

	// /screen/state
	rec = httptest.NewRecorder()
	p.screenHandlers(rec, httptest.NewRequest(http.MethodGet, "/screen/state", nil), "/screen/state")
	if rec.Code != 200 || !bytes.Contains(rec.Body.Bytes(), []byte(`"pushes":1`)) {
		t.Fatalf("state 内容不对: %s", rec.Body.String())
	}

	// /screen 页面含 img 直出流
	rec = httptest.NewRecorder()
	p.screenHandlers(rec, httptest.NewRequest(http.MethodGet, "/screen", nil), "/screen")
	if rec.Code != 200 || !bytes.Contains(rec.Body.Bytes(), []byte(`/screen/ws`)) {
		t.Fatal("调试页应内嵌 WebSocket 帧通道")
	}

	// 未启用 → 404
	rec = httptest.NewRecorder()
	(&panel{}).screenHandlers(rec, httptest.NewRequest(http.MethodGet, "/screen", nil), "/screen")
	if rec.Code != 404 {
		t.Fatalf("未启用应 404，got %d", rec.Code)
	}
}

