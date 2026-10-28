package main

// screen_test.go —— 屏渲染栈回归（不需要硬件）
//   1) 与 Python 基线逐字节一致的黄金样本（testdata/screen_golden.json.gz，
//      期望值可由 go test -run TestGoldenParityWithPython -update 重算——版式不许悄悄漂移）
//   2) 帧间差分矩形（随机用例）
//   3) 字体度量与绘制推进自洽（右对齐锚点）
//   4) 驱动命令流/推送内容（MemoryBus 假件）

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"flag"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type goldenCase struct {
	Name   string         `json:"name"`
	Page   string         `json:"page"`
	Lang   string         `json:"lang"`
	Snap   map[string]any `json:"snap"`
	Prog   float64        `json:"prog"`
	Now    string         `json:"now"`
	Hist   []float64      `json:"hist"`
	Expect string         `json:"expect"`
}

func loadGolden(t *testing.T) []goldenCase {
	t.Helper()
	p := filepath.Join("testdata", "screen_golden.json.gz")
	f, err := os.Open(p)
	if err != nil {
		t.Fatalf("打开黄金样本失败（testdata/screen_golden.json.gz 缺失；版式改动用 go test -update 重算）: %v", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version int          `json:"version"`
		Cases   []goldenCase `json:"cases"`
	}
	if err := json.NewDecoder(gz).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("黄金样本为空")
	}
	return doc.Cases
}

// 1) 逐字节对齐基线（期望值由 `go test -run TestGoldenParityWithPython -update` 重算）
//
// 输入固定、期望值可用 Go 侧重算**，版式改动必须显式 -update 并 review diff，
// 不允许"悄悄漂移"。
var updateGolden = flag.Bool("update", false, "重算黄金样本的期望值（版式改动时才用）")

func TestGoldenParityWithPython(t *testing.T) {
	cases := loadGolden(t)
	if *updateGolden {
		rewriteGolden(t, cases)
		return
	}
	for _, cs := range cases {
		t.Run(cs.Name, func(t *testing.T) {
			want, err := base64.StdEncoding.DecodeString(cs.Expect)
			if err != nil {
				t.Fatal(err)
			}
			c := newCanvas(screenW, screenH)
			hist := &History{m: map[string][]float64{"traffic.tok_s": cs.Hist}}
			uiRender(cs.Page, cs.Snap, hist, c, cs.Prog, cs.Lang, cs.Now, animState{})
			if !bytes.Equal(c.Buf, want) {
				diff := -1
				for i := range c.Buf {
					if c.Buf[i] != want[i] {
						diff = i
						break
					}
				}
				t.Fatalf("与基线不一致：首个差异字节 %d (像素 %d,%d)，got=%02x want=%02x"+
					"（版式改动请显式 go test -update 并 review diff）",
					diff, (diff/2)%screenW, (diff/2)/screenW, c.Buf[diff], want[diff])
			}
		})
	}
}

// rewriteGolden：保留全部输入，仅按当前 Go 渲染重算 expect 并写回 gz。
func rewriteGolden(t *testing.T, cases []goldenCase) {
	t.Helper()
	for i := range cases {
		cs := &cases[i]
		c := newCanvas(screenW, screenH)
		hist := &History{m: map[string][]float64{"traffic.tok_s": cs.Hist}}
		uiRender(cs.Page, cs.Snap, hist, c, cs.Prog, cs.Lang, cs.Now, animState{})
		cs.Expect = base64.StdEncoding.EncodeToString(c.Buf)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	enc := json.NewEncoder(gz)
	enc.SetIndent("", " ")
	if err := enc.Encode(struct {
		Version int          `json:"version"`
		Cases   []goldenCase `json:"cases"`
	}{Version: 1, Cases: cases}); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("testdata", "screen_golden.json.gz"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("黄金样本已重算：%d 例（请 git diff 复核）", len(cases))
}

// 2) 脏矩形：随机单/多点，box 必须等于最小外接矩形
func TestDirtyRect(t *testing.T) {
	rnd := rand.New(rand.NewPCG(11, 0))
	for range 200 {
		c := newCanvas(screenW, screenH)
		c.clear(screenBG)
		c.prev = append([]byte(nil), c.Buf...)
		type pt struct{ x, y int }
		var pts []pt
		for range rnd.IntN(5) + 1 {
			pts = append(pts, pt{rnd.IntN(screenW), rnd.IntN(screenH)})
		}
		for _, p := range pts {
			c.pixel(p.x, p.y, screenWarn)
		}
		got := c.dirty()
		x0, y0, x1, y1 := pts[0].x, pts[0].y, pts[0].x, pts[0].y
		for _, p := range pts {
			x0, y0 = min(x0, p.x), min(y0, p.y)
			x1, y1 = max(x1, p.x), max(y1, p.y)
		}
		if got == nil || got.x0 != x0 || got.y0 != y0 || got.x1 != x1 || got.y1 != y1 {
			t.Fatalf("dirty=%v want=(%d,%d,%d,%d)", got, x0, y0, x1, y1)
		}
	}
	// 无变化 → nil
	c := newCanvas(screenW, screenH)
	c.clear(screenBG)
	c.prev = append([]byte(nil), c.Buf...)
	if c.dirty() != nil {
		t.Fatal("无差异却返回 box")
	}
}

// 3) 字体：宽度 == 绘制推进（右对齐锚点的前提）
func TestFontWidthMatchesAdvance(t *testing.T) {
	samples := map[*Font][]string{
		SM:      {"tok/s 42.3", "12:34", "8443"},
		MD:      {"NoKeyOut", "tokens", "1234"},
		NUM:     {"42.7", "1284553", "0.42", "99"},
		CJKFont: {"数据新鲜度", "锁定", "管理面已阻隔"},
	}
	for _, f := range []*Font{SM, MD, NUM, CJKFont} {
		for _, s := range samples[f] {
			x := 0
			for _, r := range s {
				x += f.step(r)
			}
			if x != f.width(s) {
				t.Fatalf("%s: width(%q)=%d，绘制推进=%d", f.Key, s, f.width(s), x)
			}
		}
	}
	// 混排宽度一致性
	if got := mixedWidth("输出速率 tok/s", SM, CJKFont); got != CJKFont.width("输出速率")+SM.width(" tok/s") {
		t.Fatalf("mixedWidth 不自洽: %d", got)
	}
}

// 4) 驱动：命令流 + 推送字节（MemoryBus）
type memSpi struct {
	rec []struct {
		dc  bool
		buf []byte
	}
}

func (m *memSpi) write(b []byte) {
	m.rec = append(m.rec, struct {
		dc  bool
		buf []byte
	}{dc: cur, buf: append([]byte(nil), b...)})
}

var cur bool // 记录 DC 状态（memPin 同步设置）

type memPin struct{}

func (memPin) set(on bool) { cur = on }

func TestST7789Stream(t *testing.T) {
	bus := &memSpi{}
	st := newST7789(bus, memPin{}, nil, 0x70, 0, 0, true)
	st.init()
	type wireCmd struct {
		c    byte
		args []byte
	}
	var cmds []wireCmd
	for _, r := range bus.rec {
		if !r.dc {
			cmds = append(cmds, wireCmd{c: r.buf[0]})
			continue
		}
		last := &cmds[len(cmds)-1]
		last.args = append(last.args, r.buf...)
	}
	if len(cmds) < 3 || cmds[0].c != 0x11 || cmds[1].c != 0x36 || len(cmds[1].args) != 1 || cmds[1].args[0] != 0x70 {
		t.Fatalf("init 应先 SLPOUT 再 MADCTL 透传: %+v", cmds[:2])
	}
	if cmds[2].c != 0x3A || len(cmds[2].args) != 1 || cmds[2].args[0] != 0x05 {
		t.Fatalf("COLMOD 应为 0x05（卖家序列）: %+v", cmds[2])
	}
	if last := cmds[len(cmds)-1]; last.c != 0x29 {
		t.Fatalf("结尾应为 DISPON: %+v", last)
	}

	// 渲染一页 → flush：CASET/RASET + RAMWR + 像素 == 画布差异区
	c := newCanvas(screenW, screenH)
	cs := loadGolden(t)[0] // main_zh
	hist := &History{m: map[string][]float64{"traffic.tok_s": cs.Hist}}
	uiRender(cs.Page, cs.Snap, hist, c, cs.Prog, cs.Lang, cs.Now, animState{})
	box := c.dirty()
	bus.rec = nil
	if !c.flush(st) {
		t.Fatal("首帧应推送")
	}
	var cas, ras, ram []byte
	var px [][]byte
	cur := byte(0)
	for _, r := range bus.rec {
		if r.dc {
			switch cur {
			case 0x2A:
				cas = append(cas, r.buf...)
			case 0x2B:
				ras = append(ras, r.buf...)
			case 0x2C:
				px = append(px, r.buf)
			}
			continue
		}
		cur = r.buf[0]
		if cur == 0x2C {
			ram = r.buf
		}
	}
	if !bytes.Equal(cas, []byte{byte(box.x0 >> 8), byte(box.x0), byte(box.x1 >> 8), byte(box.x1)}) {
		t.Fatalf("CASET 参数=%x box=%v", cas, box)
	}
	if !bytes.Equal(ras, []byte{byte(box.y0 >> 8), byte(box.y0), byte(box.y1 >> 8), byte(box.y1)}) {
		t.Fatalf("RASET 参数=%x box=%v", ras, box)
	}
	if ram == nil || len(px) == 0 {
		t.Fatal("缺 RAMWR/像素")
	}
	want := []byte{}
	stride := screenW * 2
	for y := box.y0; y <= box.y1; y++ {
		want = append(want, c.Buf[y*stride+box.x0*2:y*stride+(box.x1+1)*2]...)
	}
	pushed := bytes.Join(px, nil)
	if !bytes.Equal(pushed, want) {
		t.Fatalf("推送内容与画布不一致（got %d, want %d 字节）", len(pushed), len(want))
	}

	// 改一个像素 → 推送序列应为 CASET/RASET/RAMWR + 2 字节
	c.pixel(100, 100, 0xF800)
	bus.rec = nil
	c.flush(st)
	var got []byte
	for _, r := range bus.rec {
		got = append(got, r.buf...)
	}
	wantSeq := []byte{0x2A, 0, 100, 0, 100, 0x2B, 0, 100, 0, 100, 0x2C, 0xF8, 0x00}
	if !bytes.Equal(got, wantSeq) {
		t.Fatalf("单像素推送流不对:\n got %x\nwant %x", got, wantSeq)
	}
}

// 5) 页面渲染不落空：每页都要有内容（全 0 视为 bug）
func TestPagesRenderContent(t *testing.T) {
	for _, cs := range loadGolden(t) {
		c := newCanvas(screenW, screenH)
		uiRender(cs.Page, cs.Snap, histFromGolden(cs.Hist), c, cs.Prog, cs.Lang, cs.Now, animState{})
		if allZero(c.Buf) {
			t.Fatalf("%s 渲染结果为空白", cs.Name)
		}
	}
}

// ADMIN 页：产品图标 + Silkscreen 大字标 + 放大倒计时（只显示时长、居中）。
// 不再有图标外圈动画、/15:00 与分割线；超过 15 分钟转危险色。
func TestAdminPageLayout(t *testing.T) {
	renderNow := func(sec, now float64) *Canvas {
		c := newCanvas(screenW, screenH)
		uiRender("main", map[string]any{
			"switch": "unlocked", "url": "https://192.0.2.20:8443", "admin_s": sec,
		}, newHistory(), c, 0, "zh", "12:34", animState{now: now})
		return c
	}
	render := func(sec float64) *Canvas { return renderNow(sec, 0) }
	if bytes.Equal(render(0).Buf, render(65).Buf) {
		t.Fatal("计时没有随 admin_s 推进")
	}

	inkCount := func(c *Canvas, y0, y1 int) int {
		n := 0
		for y := y0; y <= y1; y++ {
			for x := 0; x < screenW; x++ {
				if pxAt(c, x, y) != uint16(screenBG) {
					n++
				}
			}
		}
		return n
	}
	// 字标区（Silkscreen，74..95）必须有大量墨迹。
	if n := inkCount(render(0), 74, 95); n < 200 {
		t.Fatalf("Silkscreen 字标墨迹过少：%d", n)
	}
	// 倒计时放大 2 倍后高度约 48px：140..188 条带里必须有墨迹，且上下都覆盖到。
	if n := inkCount(render(300), 146, 170); n < 120 {
		t.Fatalf("倒计时墨迹过少：%d", n)
	}
	if inkCount(render(300), 146, 152) == 0 || inkCount(render(300), 164, 170) == 0 {
		t.Fatal("倒计时没有铺满 24px 高度")
	}

	// 倒计时水平居中。
	lo, hi := screenW, -1
	for y := 146; y <= 170; y++ {
		for x := 0; x < screenW; x++ {
			if pxAt(render(300), x, y) != uint16(screenBG) {
				if x < lo {
					lo = x
				}
				if x > hi {
					hi = x
				}
			}
		}
	}
	if lo < 0 {
		t.Fatal("倒计时没有墨迹")
	}
	if mid := (lo + hi) / 2; mid < screenW/2-8 || mid > screenW/2+8 {
		t.Fatalf("倒计时未居中：墨迹 %d..%d（中线 %d）", lo, hi, screenW/2)
	}

	// 正常状态无危险色；达到 15 分钟阈值后倒计时转红。
	dangerInk := func(c *Canvas, y0, y1 int) int {
		n := 0
		for y := y0; y <= y1; y++ {
			for x := 0; x < screenW; x++ {
				if pxAt(c, x, y) == uint16(screenDanger) {
					n++
				}
			}
		}
		return n
	}
	if n := dangerInk(render(300), 100, 220); n != 0 {
		t.Fatalf("未超阈值不应出现危险色，got %d", n)
	}
	if n := dangerInk(render(1000), 146, 170); n == 0 {
		t.Fatal("超过 15 分钟倒计时应变红")
	}

	// 不闪：同一秒内的两个相位必须完全一致。
	if !bytes.Equal(render(899.0).Buf, render(899.5).Buf) {
		t.Fatal("同一秒内出现闪烁（不要 1Hz 闪烁）")
	}

	// 字标亮度跟随官方扫掠曲线：动画时钟下与静态帧不同。
	if bytes.Equal(renderNow(300, 0).Buf, renderNow(300, 2.5).Buf) {
		t.Fatal("字标动画没有生效")
	}
}

func TestFmtAdminDur(t *testing.T) {
	for _, c := range []struct {
		sec  float64
		want string
	}{{0, "00:00"}, {65, "01:05"}, {900, "15:00"}, {3661, "1:01:01"}} {
		if got := fmtAdminDur(c.sec); got != c.want {
			t.Fatalf("fmtAdminDur(%v) = %q，want %q", c.sec, got, c.want)
		}
	}
}

// 6) console 后端帧文案（中英都出关键行，锁态出救援提示）
func TestConsoleFrames(t *testing.T) {
	for _, lang := range []string{"zh", "en"} {
		txt := newTxt(lang)
		main := frameConsole("main", loadGolden(t)[0].Snap, txt, "12:34:56")
		// 只出今日累计量；瞬时速率（tok/s、调用/s、错误/s）与数据龄已下线
		for _, must := range []string{txt.get("calls_today"), txt.get("tokens_in"), txt.get("tokens_out")} {
			if !contains(main, must) {
				t.Fatalf("%s 主屏帧缺 %q: %s", lang, must, main)
			}
		}
		for _, banned := range []string{"tok/s", txt.get("calls_s"), txt.get("errors_s")} {
			if banned != "" && contains(main, banned) {
				t.Fatalf("%s 主屏帧不该再有瞬时量 %q: %s", lang, banned, main)
			}
		}
		admin := frameConsole("main", map[string]any{"switch": "unlocked", "url": "https://192.0.2.20:8443"}, txt, "12:34:56")
		if !contains(admin, "ADMIN") || !contains(admin, "192.0.2.20:8443") {
			t.Fatalf("%s ADMIN 帧缺地址: %s", lang, admin)
		}
	}
}

func contains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}

// histFromGolden：黄金样本里的 hist 是 traffic.tok_s 序列。
func histFromGolden(vals []float64) *History {
	return &History{m: map[string][]float64{"traffic.tok_s": vals}}
}

// TestHeaderNetIcon：屏头地球仪图标（像素级）——绿=有网络，红=其余（未插线/无网络/nil）
func TestHeaderNetIcon(t *testing.T) {
	px := func(c *Canvas, x, y int) uint16 {
		return uint16(c.Buf[(y*screenW+x)*2])<<8 | uint16(c.Buf[(y*screenW+x)*2+1])
	}
	nodeX, nodeY := 11, 10 // globeIcon(6,5) 圆心 = 赤道线像素
	cases := []struct {
		name string
		net  map[string]any
		want uint16
	}{
		{"有网络", map[string]any{"eth": "up", "inet": true}, screenOK},
		{"插线无网络", map[string]any{"eth": "up", "inet": false}, screenDanger},
		{"未插线", map[string]any{"eth": "down", "inet": false}, screenDanger},
		{"nil 网络快照", nil, screenDanger},
	}
	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			c := newCanvas(screenW, screenH)
			screenHeader(c, newTxt("zh"), "12:34", "", cs.net)
			if got := px(c, nodeX, nodeY); got != cs.want {
				t.Fatalf("图标节点像素 = %04x，want %04x", got, cs.want)
			}
			// 图标必须只占一席：左侧 20px 内不应再出现 ETH/NET 文字残留
			if pw := SM.width("ETH") + SM.width("NET"); pw == 0 {
				t.Fatal("字体度量异常")
			}
		})
	}
}

// TestScreenGate：屏幕上电门——CLI 放行/撤销/查询 + 循环等待语义
func TestScreenGate(t *testing.T) {
	dir := t.TempDir()
	gate := filepath.Join(dir, "state", "screen.gate")
	t.Setenv("SCREEN_GATE_FILE", gate)

	if gateOpen(gate) {
		t.Fatal("新门应为关")
	}
	if code := cmdScreenGate([]string{"status"}); code != 1 {
		t.Fatalf("CLOSED 状态码 = %d，want 1", code)
	}
	if code := cmdScreenGate([]string{"pass"}); code != 0 {
		t.Fatal("pass 应写门文件")
	}
	if !gateOpen(gate) {
		t.Fatal("pass 后门应打开")
	}
	if code := cmdScreenGate([]string{"status"}); code != 0 {
		t.Fatalf("OPEN 状态码 = %d，want 0", code)
	}
	if code := cmdScreenGate([]string{"revoke"}); code != 0 {
		t.Fatal("revoke 应删门文件")
	}
	if gateOpen(gate) {
		t.Fatal("revoke 后门应关闭")
	}
	// 空路径 = 不设门（永远放行）
	if !gateOpen("") {
		t.Fatal("空门路径应恒放行")
	}
	if code := cmdScreenGate(nil); code != 2 {
		t.Fatalf("无子命令状态码 = %d，want 2", code)
	}
	// 撤销一个不存在的门不算错误
	if code := cmdScreenGate([]string{"revoke"}); code != 0 {
		t.Fatal("revoke 不存在的门应幂等成功")
	}
}

// TestSlideKeepsChrome：翻页滑入只移动 body——页头/页脚像素与终帧完全一致
func TestSlideKeepsChrome(t *testing.T) {
	snap := testSnap()
	src := newCanvas(screenW, screenH)
	uiRender("pools", snap, histFromGolden(nil), src, 0.0, "zh", "12:34", animState{slide: 1})
	full := newCanvas(screenW, screenH)
	full.clear(screenBG)
	copy(full.Buf, src.Buf) // 终帧基准
	// 半程帧：slide=0.5
	dst := newCanvas(screenW, screenH)
	blitSlide(dst, src, 0.5)
	stride := screenW * 2
	rowsOf := func(c *Canvas) []byte {
		return append(append([]byte{}, c.Buf[:screenHeadH*stride]...), c.Buf[(screenH-screenFootH)*stride:]...)
	}
	if !bytes.Equal(rowsOf(dst), rowsOf(full)) {
		t.Fatal("页头/页脚不应随翻页位移")
	}
	// body 至少有一行真的左移了（否则测试是空的）
	moved := false
	for y := screenHeadH; y < screenH-screenFootH; y++ {
		if !bytes.Equal(dst.Buf[y*stride:y*stride+40], full.Buf[y*stride:y*stride+40]) {
			moved = true
			break
		}
	}
	if !moved {
		t.Fatal("body 应发生水平位移")
	}
}

// ADMIN 页文案必须放得下（英文版更宽）。
func TestAdminTextFits(t *testing.T) {
	for _, lang := range []string{"zh", "en"} {
		txt := newTxt(lang)
		for _, k := range []string{"admin_hint", "admin_over"} {
			if w := mixedWidth(txt.get(k), SM, CJKFont); w > screenW-16 {
				t.Fatalf("%s/%s 超宽：%d > %d", lang, k, w, screenW-16)
			}
		}
	}
}

func TestAdminToast(t *testing.T) {
	p := &panel{}
	p.showToast("toast_admin_off", time.Second)
	got := p.screenToast.Load()
	if got == nil || time.Until(got.until) <= 0 {
		t.Fatal("toast 未设置或已过期")
	}
	for _, lang := range []string{"zh", "en"} {
		msg := newTxt(lang).get("toast_admin_off")
		if w := mixedWidth(msg, SM, CJKFont); w > screenW-12 {
			t.Fatalf("%s toast 超宽：%d", lang, w)
		}
		c := newCanvas(screenW, screenH)
		c.clear(screenBG)
		drawToast(c, msg)
		lit := 0
		for y := screenH - 22; y < screenH; y++ {
			for x := 0; x < screenW; x++ {
				if pxAt(c, x, y) != uint16(screenBG) {
					lit++
				}
			}
		}
		if lit < 100 {
			t.Fatalf("%s toast 没画出来：%d", lang, lit)
		}
	}
}
