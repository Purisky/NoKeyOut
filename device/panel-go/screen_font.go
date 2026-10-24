package main

// screen_font.go —— fontdata（screen_gen.go 生成）之上的排版层
//   定宽字 y = 盒顶；num（变宽大数字）y = 基线；缺字退化成 □（cjk 已烘焙）。

const screenGap = 1 // 字间距（像素）
const screenTofu = "□"

type fontDef struct { // screen_gen.go 生成的结构
	key      string
	h, adv   int
	ascent   int // 0 = y 传盒顶；>0 = y 传基线（num）
	variable bool
	chars    string
	widths   []int
	offsets  []int
	data     []byte
	cov      []byte
	covOffs  []int
}

type Font struct {
	Key      string
	H        int
	Adv      int
	Ascent   int
	Variable bool
	idx      map[rune]int
	data     []byte
	cov      []byte
	widths   []int
	offs     []int
	coffs    []int
	rowbytes []int
	covrb    []int
	tofu     int
}

var (
	fonts   = map[string]*Font{}
	SM, MD  *Font
	NUM     *Font
	CJKFont *Font
)

func init() {
	for _, def := range fontDefs {
		f := newScreenFont(def)
		fonts[def.key] = f
	}
	SM, MD, NUM, CJKFont = fonts["sm"], fonts["md"], fonts["num"], fonts["cjk"]
	for _, f := range []*Font{SM, MD, NUM, CJKFont} {
		if f == nil {
			panic("screen: 字体表缺字（screen_gen.go 与 screen_font.go 不同步）")
		}
	}
}

func newScreenFont(d fontDef) *Font {
	f := &Font{
		Key: d.key, H: d.h, Adv: d.adv, Ascent: d.ascent, Variable: d.variable,
		data: d.data, cov: d.cov, tofu: -1,
	}
	n := len([]rune(d.chars))
	f.idx = make(map[rune]int, n)
	f.widths = make([]int, n)
	f.offs = make([]int, n)
	f.coffs = make([]int, n)
	f.rowbytes = make([]int, n)
	f.covrb = make([]int, n)
	ri := 0
	for _, r := range d.chars { // range 给字节偏移；字模索引按**字符**计数
		f.idx[r] = ri
		ri++
	}
	if d.variable {
		for i := range f.widths {
			f.widths[i] = d.widths[i]
			f.offs[i] = d.offsets[i]
			if len(d.covOffs) > i {
				f.coffs[i] = d.covOffs[i]
			}
			rb := 0
			if i+1 < len(d.offsets) {
				rb = (d.offsets[i+1] - d.offsets[i]) / d.h
			} else {
				rb = (len(d.data) - d.offsets[i]) / d.h
			}
			f.rowbytes[i] = rb
			f.covrb[i] = (f.widths[i] + 3) / 4
		}
	} else {
		w := d.adv - 1
		rows := (w + 7) / 8
		stride := rows * d.h
		crows := (w + 3) / 4 // 2bpp：每字节 4 像素
		cstride := crows * d.h
		for i := range f.widths {
			f.widths[i] = w
			f.offs[i] = i * stride
			f.rowbytes[i] = rows
			f.coffs[i] = i * cstride
			f.covrb[i] = crows
		}
	}
	if i, ok := f.idx[[]rune(screenTofu)[0]]; ok {
		f.tofu = i
	}
	return f
}

func (f *Font) width(s string) int {
	if s == "" {
		return 0
	}
	if f.Variable {
		n, w := 0, 0
		for _, r := range s {
			if i, ok := f.idx[r]; ok {
				w += f.widths[i]
				n++
			}
		}
		if n == 0 {
			return 0
		}
		return w + screenGap*n // 含尾部 1px 字距：与 draw() 推进一致，右对齐不越锚点
	}
	return len([]rune(s)) * f.Adv
}

func (f *Font) glyph(r rune) ([]byte, int, int, bool) {
	i, ok := f.idx[r]
	if !ok {
		i = f.tofu
	}
	if i < 0 || i >= len(f.offs) { // 字模没有 → 跳过（与 Python 一致）
		return nil, 0, 0, false
	}
	start := f.offs[i]
	return f.data[start : start+f.rowbytes[i]*f.H], f.widths[i], f.rowbytes[i], true
}

func (f *Font) glyphAA(r rune) ([]byte, int, int, bool) {
	i, ok := f.idx[r]
	if !ok {
		i = f.tofu
	}
	if i < 0 || i >= len(f.coffs) {
		return nil, 0, 0, false
	}
	start := f.coffs[i]
	rb := f.covrb[i]
	if start < 0 || start+rb*f.H > len(f.cov) {
		return nil, 0, 0, false
	}
	return f.cov[start : start+rb*f.H], f.widths[i], rb, true
}

func (f *Font) step(r rune) int {
	if f.Variable {
		if i, ok := f.idx[r]; ok {
			return f.widths[i] + screenGap
		}
		return screenGap
	}
	return f.Adv // 定宽 adv = w+1 已含 1px 字距
}

// screenAA：全局开关（A/B 用；默认开）。关掉回 1bpp 硬边。
var screenAA = true

// draw：画文本。y = 盒顶（定宽）或基线（num）。screenAA 时走 4 级覆盖合成。
func (f *Font) draw(c *Canvas, x, y int, s string, color uint16) int {
	if s == "" {
		return 0
	}
	top := y
	if f.Ascent > 0 {
		top = y - f.Ascent
	}
	if screenAA && len(f.cov) > 0 {
		for _, r := range s {
			if cov, w, rb, ok := f.glyphAA(r); ok {
				c.bitmapAA(x, top, cov, w, rb, f.H, color)
			}
			x += f.step(r)
		}
		return f.width(s)
	}
	for _, r := range s {
		blob, w, rb, ok := f.glyph(r)
		if ok {
			c.bitmap(x, top, blob, w, rb, f.H, color)
		}
		x += f.step(r)
	}
	return f.width(s)
}

func (f *Font) drawRight(c *Canvas, xRight, y int, s string, color uint16) {
	f.draw(c, xRight-f.width(s), y, s, color)
}

// drawAA：抗锯齿文本（4 级覆盖）。y 语义与 draw 相同。
func (f *Font) drawAA(c *Canvas, x, y int, s string, color uint16) int {
	if s == "" {
		return 0
	}
	top := y
	if f.Ascent > 0 {
		top = y - f.Ascent
	}
	for _, r := range s {
		if cov, w, rb, ok := f.glyphAA(r); ok {
			c.bitmapAA(x, top, cov, w, rb, f.H, color)
		}
		x += f.step(r)
	}
	return f.width(s)
}

func (f *Font) drawCenter(c *Canvas, cx, y int, s string, color uint16) {
	f.draw(c, cx-f.width(s)/2, y, s, color)
}

func (f *Font) truncate(s string, maxW int) string {
	if f.width(s) <= maxW {
		return s
	}
	out := []rune(s)
	for len(out) > 0 && f.width(string(out)+"…") > maxW {
		out = out[:len(out)-1]
	}
	return string(out) + "…"
}

// ---------------------------------------------------------------- 中英混排

func drawMixed(c *Canvas, x, y int, s string, color uint16, ascii, cjk *Font) int {
	for _, r := range s {
		f := cjk
		if r < 0x2E80 {
			f = ascii
		}
		f.draw(c, x, y, string(r), color)
		x += f.step(r)
	}
	return x
}

func drawMixedRight(c *Canvas, xRight, y int, s string, color uint16, ascii, cjk *Font) {
	drawMixed(c, xRight-mixedWidth(s, ascii, cjk), y, s, color, ascii, cjk)
}

func mixedWidth(s string, ascii, cjk *Font) int {
	w := 0
	for _, r := range s {
		if r < 0x2E80 {
			w += ascii.step(r)
		} else {
			w += cjk.step(r)
		}
	}
	return w
}
