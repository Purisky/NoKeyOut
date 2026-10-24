//go:build !(js && wasm)

package main

// screen_ws.go —— 虚拟屏的 WebSocket 原始帧通道（/screen/ws）
//
// WS 推送线上字节序的 RGB565（115,200B/帧）：服务端零编码（一次 memcpy），
// 浏览器侧 JS 展开成 RGBA 画上 canvas——**没有压缩假纹、没有编解码延迟**。
// 30fps @ 3.5MB/s，LAN/回环都无压力。
//
// 协议：RFC6455 最小子集——握手 + 服务端二进制帧（FIN=1，opcode=2，无掩码）。

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type wsConn struct {
	conn net.Conn
	mu   sync.Mutex
	out  []byte
}

// send：推送一帧。**必须有界**——渲染线程会调用它，客户端不读时不能把屏卡死。
//
//	一个冻结的浏览器标签页足以让渲染线程永久阻塞，设备上表现为真屏冻住。）
//
// 写超时 20ms，超时/出错即关闭该连接并丢弃（客户端会重连，latest-wins）。
func (w *wsConn) send(b []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.conn == nil {
		return
	}
	hdr := make([]byte, 10)
	n := 2
	hdr[0] = 0x82 // FIN + binary
	switch {
	case len(b) < 126:
		hdr[1] = byte(len(b))
	default:
		// 16 位长度字段最大 65,535B，**装不下 115,200B 的一帧**——
		// 所以 ≥126B 一律走 64 位扩展长度。
		hdr[1] = 127
		binary.BigEndian.PutUint64(hdr[2:10], uint64(len(b)))
		n = 10
	}
	_ = w.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	if _, err := w.conn.Write(append(hdr[:n], b...)); err != nil {
		_ = w.conn.Close()
		w.conn = nil
	}
}

// wsWriteTimeout：单帧写超时。LAN 上 115KB 远低于它；拥塞客户端会在这里被丢弃。
const wsWriteTimeout = 20 * time.Millisecond

var wsMu sync.Mutex
var wsClients []*wsConn

// wsBroadcast：非阻塞发送；客户端拥塞就丢（下一帧马上又来，latest-wins）
func wsBroadcast(b []byte) {
	wsMu.Lock()
	clients := append([]*wsConn{}, wsClients...)
	wsMu.Unlock()
	for _, c := range clients {
		c.send(b)
	}
}

func (p *panel) screenWS(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") ||
		!strings.Contains(strings.ToLower(r.Header.Get("Upgrade")), "websocket") {
		p.errJSON(w, r, 400, "need websocket upgrade")
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		p.errJSON(w, r, 400, "missing Sec-WebSocket-Key")
		return
	}
	sum := sha1.Sum([]byte(key + wsGUID))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	hj, _ := w.(http.Hijacker)
	if hj == nil {
		p.errJSON(w, r, 500, "hijack unsupported")
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := conn.Write([]byte(resp)); err != nil {
		return
	}
	_ = buf // 客户端不会发数据，保留引用即可
	cl := &wsConn{conn: conn}
	wsMu.Lock()
	wsClients = append(wsClients, cl)
	wsMu.Unlock()
	defer func() {
		wsMu.Lock()
		for i, c := range wsClients {
			if c == cl {
				wsClients = append(wsClients[:i], wsClients[i+1:]...)
				break
			}
		}
		wsMu.Unlock()
	}()
	// 立刻推一帧，再保持连接（客户端关闭时 Read 返回错误）
	if p.screen != nil {
		cl.send(p.screen.frameRaw())
	}
	b := make([]byte, 512)
	for {
		if _, err := conn.Read(b); err != nil {
			if !errors.Is(err, io.EOF) {
				_ = err
			}
			return
		}
	}
}
