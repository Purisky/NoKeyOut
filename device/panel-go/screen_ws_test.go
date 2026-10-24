package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// wsHijackRec：实现 Hijacker 的伪造 ResponseWriter，捕获握手字节与后续帧
type wsHijackRec struct {
	*httptest.ResponseRecorder
	conn *fakeWSConn
}

func newWSHijackRecorder() (*wsHijackRec, net.Conn) {
	rec := &wsHijackRec{ResponseRecorder: httptest.NewRecorder()}
	fc := &fakeWSConn{buf: &bytes.Buffer{}}
	rec.conn = fc
	return rec, fc
}

func (w *wsHijackRec) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, nil, nil
}

// fakeWSConn：只写不读（读时立即 EOF）
type fakeWSConn struct{ buf *bytes.Buffer }

func (f *fakeWSConn) Read(b []byte) (int, error)         { return 0, io.EOF }
func (f *fakeWSConn) Write(b []byte) (int, error)        { return f.buf.Write(b) }
func (f *fakeWSConn) Close() error                       { return nil }
func (f *fakeWSConn) LocalAddr() net.Addr                { return nil }
func (f *fakeWSConn) RemoteAddr() net.Addr               { return nil }
func (f *fakeWSConn) SetDeadline(t time.Time) error      { return nil }
func (f *fakeWSConn) SetReadDeadline(t time.Time) error  { return nil }
func (f *fakeWSConn) SetWriteDeadline(t time.Time) error { return nil }

func TestWSHandshakeAccept(t *testing.T) {
	req := httptest.NewRequest("GET", "/screen/ws", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==") // RFC6455 例
	req.Header.Set("Sec-WebSocket-Version", "13")
	rec := &wsHijackRec{ResponseRecorder: httptest.NewRecorder(), conn: &fakeWSConn{buf: &bytes.Buffer{}}}
	p := &panel{screen: newShadowScreen(t.TempDir(), 1)}
	p.screenWS(rec, req)
	out := rec.conn.buf.String()
	if !strings.Contains(out, "101 Switching Protocols") {
		t.Fatalf("握手响应缺 101: %q", out[:80])
	}
	// RFC6455 §1.3 官方例：key 经 GUID 的 SHA1/base64
	if want := "Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo="; !strings.Contains(out, want) {
		t.Fatalf("Accept 错：%q", out)
	}
}

// TestWSFrameLength64bit：≥126B 的帧必须走 64 位扩展长度——
// 拥塞/冻结的 WS 客户端不得阻塞渲染线程。
// （设备上表现为真屏冻住）。超时即丢弃该连接。
func TestWSSendDoesNotBlockOnStalledClient(t *testing.T) {
	srv, cli := net.Pipe()
	defer cli.Close()
	w := &wsConn{conn: srv}
	done := make(chan struct{})
	go func() {
		w.send(make([]byte, screenW*screenH*2)) // 115,200B：一帧
		close(done)
	}()
	select {
	case <-done: // 期望：超时后放弃并关闭连接
	case <-time.After(2 * time.Second):
		t.Fatal("send 阻塞超过 2s：拥塞客户端会把渲染线程卡死")
	}
	if w.conn != nil {
		t.Fatal("超时后应关闭并置空连接")
	}
}

// 正常客户端仍然收得到整帧（不能被上面的超时误伤）。
func TestWSSendDeliversFullFrame(t *testing.T) {
	srv, cli := net.Pipe()
	defer srv.Close()
	defer cli.Close()
	w := &wsConn{conn: srv}
	frame := make([]byte, 115200)
	for i := range frame {
		frame[i] = byte(i)
	}
	go w.send(frame)
	hdr := make([]byte, 10)
	if _, err := io.ReadFull(cli, hdr); err != nil {
		t.Fatal(err)
	}
	if hdr[0] != 0x82 || hdr[1] != 127 {
		t.Fatalf("帧头不对：FIN/opcode=%02x len=%d（≥126B 必须走 64 位长度）", hdr[0], hdr[1])
	}
	if got := binary.BigEndian.Uint64(hdr[2:10]); got != uint64(len(frame)) {
		t.Fatalf("长度字段 %d，want %d", got, len(frame))
	}
	body := make([]byte, len(frame))
	if _, err := io.ReadFull(cli, body); err != nil {
		t.Fatal(err)
	}
	if body[len(body)-1] != byte(len(frame)-1) {
		t.Fatal("帧体内容不完整")
	}
}
func TestWSFrameLength64bit(t *testing.T) {
	big := make([]byte, 115200)
	for i := range big {
		big[i] = byte(i)
	}
	rec := &wsHijackRec{ResponseRecorder: httptest.NewRecorder(), conn: &fakeWSConn{buf: &bytes.Buffer{}}}
	c := &wsConn{conn: rec.conn}
	c.send(big)
	data := rec.conn.buf.Bytes()
	if len(data) == 0 || data[0] != 0x82 {
		t.Fatal("首字节应为 FIN+binary (0x82)")
	}
	if data[1] != 127 {
		t.Fatalf("115200B 帧的长度字段 = 0x%02x，应走 64 位扩展 (127)", data[1])
	}
	if n := binary.BigEndian.Uint64(data[2:10]); n != 115200 {
		t.Fatalf("64 位长度 = %d，want 115200", n)
	}
	body := data[10:]
	if len(body) != 115200 {
		t.Fatalf("载荷长 %d，want 115200（16 位截断会变 49664）", len(body))
	}
	for i := range big {
		if body[i] != big[i] {
			t.Fatalf("载荷第 %d 字节不符", i)
		}
	}
	small := []byte("hi")
	c.send(small)
	data = rec.conn.buf.Bytes()[10+len(big):]
	if data[1] != 2 {
		t.Fatalf("小帧长度字段 = 0x%02x，want 2", data[1])
	}
}
