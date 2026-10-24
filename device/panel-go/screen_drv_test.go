package main

// screen_drv_test.go —— 直操纵像素 API 回归

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDrawOpsApply(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SWITCHD_STATE_FILE", filepath.Join(dir, "sw"))
	_ = os.WriteFile(filepath.Join(dir, "sw"), []byte("unlocked"), 0o644)
	p := &panel{screen: newShadowScreen(dir, 1)}
	post := func(body map[string]any) (int, string) {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/v1/screen/draw", bytes.NewReader(b))
		rec := httptest.NewRecorder()
		p.screenDrvHandler(rec, req, "/draw")
		return rec.Code, rec.Body.String()
	}
	// 内置字体（cjk/md）+ 用户字体 + 原语全覆盖
	code, body := post(map[string]any{"ops": []map[string]any{
		{"op": "clear", "color": "#101820"},
		{"op": "px", "x": 1, "y": 1, "color": "#FF0000"},
		{"op": "rect", "x": 8, "y": 8, "w": 32, "h": 16, "color": "#4C8DFF", "fill": true},
		{"op": "line", "x": 0, "y": 0, "x2": 239, "y2": 239, "color": "#00FF00"},
		{"op": "circle", "x": 200, "y": 40, "r": 20, "color": "#E5A33C"},
		{"op": "arc", "x": 120, "y": 200, "r": 18, "a0": -90, "a1": 90, "color": "#E5534B"},
		{"op": "text", "x": 16, "y": 54, "text": "自由画屏", "font": "cjk", "color": "#3FB27F"},
		{"op": "text", "x": 16, "y": 14, "text": "DIRECT", "font": "md", "color": "#FFFFFF"},
		{"op": "bitmap", "x": 40, "y": 120, "w": 8, "h": 8, "data": "ff818199a58181ff", "color": "#4C8DFF"},
	}})
	if code != 200 {
		t.Fatalf("draw code=%d %s", code, body)
	}
	// 帧里有内容（非全底色）
	raw := scr.frameRaw()
	if len(raw) != screenW*screenH*2 {
		t.Fatalf("raw frame %d bytes", len(raw))
	}
	same := true
	for _, b := range raw {
		if b != raw[len(raw)/2] {
			same = false
			break
		}
	}
	if same {
		t.Fatal("draw 后仍是纯色")
	}
	// 字体上传 + 引用
	code, body = post(map[string]any{
		"font": map[string]any{"name": "t8", "w": 8, "h": 8, "adv": 9,
			"charset": "A", "data": "ff00ff00ff00ff00"},
		"ops": []map[string]any{{"op": "text", "x": 0, "y": 0, "text": "A", "font": "t8", "color": "#FFF"}},
	})
	if code != 200 {
		t.Fatalf("font+draw code=%d %s", code, body)
	}
	if _, ok := scr.fonts["t8"]; !ok {
		t.Fatal("用户字体未注册")
	}
	// 坏 font：长度不符
	if code, _ := post(map[string]any{"font": map[string]any{"name": "bad", "charset": "AB",
		"w": 8, "h": 8, "data": "ff"}}); code != 400 {
		t.Fatalf("坏 font 应 400，got %d", code)
	}
	// 坏 op
	if code, _ := post(map[string]any{"ops": []map[string]any{{"op": "nope"}}}); code != 400 {
		t.Fatalf("坏 op 应 400，got %d", code)
	}
	// 内置字体名保留（用户不能用内置名覆盖）
	if code, _ := post(map[string]any{"font": map[string]any{"name": "sm", "charset": "A",
		"w": 8, "h": 8, "data": "ff00ff00ff00ff00"}}); code != 400 {
		t.Fatalf("覆盖内置字体名应 400，got %d", code)
	}
	scr.fonts = map[string]*Font{}
	scr.manual.Store(false)
}

func TestParseColor(t *testing.T) {
	cases := []struct {
		in   any
		want uint16
		ok   bool
	}{
		{"#FF0000", 0xF800, true},
		{"00FF00", 0x07E0, true},
		{[]any{0, 0, 255}, 0x001F, true},
		{float64(0xABCD), 0xABCD, true},
		{"xyz", 0, false},
		{[]any{300, 0, 0}, 0, false},
	}
	for _, c := range cases {
		got, ok := parseColor(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Fatalf("parseColor(%v)=%04x,%v want %04x,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}
