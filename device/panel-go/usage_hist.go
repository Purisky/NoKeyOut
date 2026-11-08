//go:build !(js && wasm)

package main

//
// 背景：`/v1/magpie/usage` 只给 hour/day 聚合（`bucket` 参数无效），所以图上台阶很大。
// 做法：**每 2s 采样上游的今日累计总量，取差值得增量，写进我们自己的 1 分钟桶**，
// 落盘成一个小文件；直方图优先用这份桶，覆盖不足时退回 API 的粗桶。
//
// 存 1 分钟桶，任何窗口都能用同一份数据聚合出来，不需要保留原始请求行。
//
// 落盘：`<state>/usage-hist.bin`，格式是 (slot, tok, calls, in, out, peak) 的 int64 小端对，slot = unix/60。
// 只存非零槽，当前量级（天几百个活跃槽）文件只有几十 KB；每 5 分钟最多写一次。

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// NKH3 在 NKH2 的 5 个 int64 后追加 peak。旧面板只认识 NKH2/v1，
	// 从带 peak 的版本回滚到旧面板前请删除 usage-hist.bin；旧面板会从
	// magpie 流水重建总用量（peak 会在新版本再次启动时重建）。
	histFileMagic   = "NKH3"
	histFileMagicV2 = "NKH2"

	// 1 分钟一槽：屏上可用宽度只有 224px（240 − 两侧 8px），
	// 24h 的 5 分钟桶是 288 个 > 224 列，塞不下也无意义——所以**存得更细（1 分钟），
	// 显示时按"周期 ÷ 宽度"聚合**：today 每列 ≈ 6.4 分钟、7d ≈ 45 分钟、30d ≈ 3.2 小时。
	histSlotSec  = 60
	histKeepDays = 31
)

// histSlot：1 分钟槽的累计增量（tokens / 调用 / 输入 / 输出 / DeepSeek 峰价 tokens）。
// peak 是 tok 的子集，只统计"模型名含 deepseek 且落在高峰时段"的请求。
type histSlot struct {
	tok, calls, in, out, peak int64
}

type usageHist struct {
	path string
	mu   sync.Mutex

	slots    map[int64]histSlot // slot → 增量
	prev     histSlot           // 上次采样的今日累计
	prevDay  string             // prev 属于哪一天（本地日）
	dirty    bool
	lastSave time.Time

	// magpie 逐请求流水：启动时全量重建 peak（避免重复累加），之后从
	// logOffset 增量 tail；不落盘 offset，重启时重建一次即可。
	logPath   string
	logOffset int64
	peakReady bool

	errs errorRing // 最近调用错误（含分类），由同一条流水 tail 收集
}

func newUsageHist(path string) *usageHist {
	if path == "" || path == "off" {
		return nil
	}
	h := &usageHist{path: path, slots: map[int64]histSlot{}}
	h.load()
	return h
}

func (h *usageHist) load() {
	if h == nil {
		return
	}
	b, err := os.ReadFile(h.path)
	if err != nil || len(b) < 16 {
		return
	}
	if len(b) >= 4 {
		switch string(b[:4]) {
		case histFileMagic: // NKH3：slot, tok, calls, in, out, peak（48 字节/条）
			for i := 4; i+48 <= len(b); i += 48 {
				slot := int64(binary.LittleEndian.Uint64(b[i:]))
				v := histSlot{
					tok:   int64(binary.LittleEndian.Uint64(b[i+8:])),
					calls: int64(binary.LittleEndian.Uint64(b[i+16:])),
					in:    int64(binary.LittleEndian.Uint64(b[i+24:])),
					out:   int64(binary.LittleEndian.Uint64(b[i+32:])),
					peak:  int64(binary.LittleEndian.Uint64(b[i+40:])),
				}
				if v.tok > 0 || v.calls > 0 || v.in > 0 || v.out > 0 || v.peak > 0 {
					h.slots[slot] = v
				}
			}
			return
		case histFileMagicV2: // NKH2：slot, tok, calls, in, out（40 字节/条；无 peak）
			for i := 4; i+40 <= len(b); i += 40 {
				slot := int64(binary.LittleEndian.Uint64(b[i:]))
				v := histSlot{
					tok:   int64(binary.LittleEndian.Uint64(b[i+8:])),
					calls: int64(binary.LittleEndian.Uint64(b[i+16:])),
					in:    int64(binary.LittleEndian.Uint64(b[i+24:])),
					out:   int64(binary.LittleEndian.Uint64(b[i+32:])),
				}
				if v.tok > 0 || v.calls > 0 || v.in > 0 || v.out > 0 {
					h.slots[slot] = v
				}
			}
			return
		}
	}
	// v1：只有 tokens 的 (slot, tokens) 对
	for i := 0; i+16 <= len(b); i += 16 {
		slot := int64(binary.LittleEndian.Uint64(b[i:]))
		v := int64(binary.LittleEndian.Uint64(b[i+8:]))
		if v > 0 {
			h.slots[slot] = histSlot{tok: v}
		}
	}
}

// save：原子替换；只在 dirty 且距上次 ≥5 分钟时写，避免磨 SD 卡。
func (h *usageHist) save(force bool) {
	if h == nil {
		return
	}
	h.mu.Lock()
	if !h.dirty || (!force && time.Since(h.lastSave) < 5*time.Minute) {
		h.mu.Unlock()
		return
	}
	cut := time.Now().Add(-histKeepDays*24*time.Hour).Unix() / histSlotSec
	keys := make([]int64, 0, len(h.slots))
	for k := range h.slots {
		if k >= cut {
			keys = append(keys, k)
		} else {
			delete(h.slots, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	buf := make([]byte, 0, 4+len(keys)*48)
	buf = append(buf, histFileMagic...)
	for _, k := range keys {
		var tmp [48]byte
		v := h.slots[k]
		binary.LittleEndian.PutUint64(tmp[0:], uint64(k))
		binary.LittleEndian.PutUint64(tmp[8:], uint64(v.tok))
		binary.LittleEndian.PutUint64(tmp[16:], uint64(v.calls))
		binary.LittleEndian.PutUint64(tmp[24:], uint64(v.in))
		binary.LittleEndian.PutUint64(tmp[32:], uint64(v.out))
		binary.LittleEndian.PutUint64(tmp[40:], uint64(v.peak))
		buf = append(buf, tmp[:]...)
	}
	h.dirty = false
	h.lastSave = time.Now()
	path := h.path
	h.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// observe：把"今日累计总量"的增量记到当前 1 分钟槽。
// 跨日（累计归零/变小）只重置基准，不记负数；上游修正导致的回退同样只重置基准。
func (h *usageHist) observe(now time.Time, cur histSlot) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	day := now.Format("2006-01-02")
	// 跨日（累计归零/变小）或上游回退：只重置基准，不记负数。
	if h.prevDay != day || cur.tok < h.prev.tok || cur.calls < h.prev.calls ||
		cur.in < h.prev.in || cur.out < h.prev.out {
		h.prevDay, h.prev = day, cur
		return
	}
	d := histSlot{
		tok:   cur.tok - h.prev.tok,
		calls: cur.calls - h.prev.calls,
		in:    cur.in - h.prev.in,
		out:   cur.out - h.prev.out,
	}
	if d.tok > 0 || d.calls > 0 || d.in > 0 || d.out > 0 {
		s := h.slots[now.Unix()/histSlotSec]
		s.tok += d.tok
		s.calls += d.calls
		s.in += d.in
		s.out += d.out
		h.slots[now.Unix()/histSlotSec] = s
		h.dirty = true
	}
	h.prev = cur
}

// window：把 since..since+period 的增量求和（不含缓存读，缓存读没有逐分钟来源）。
func (h *usageHist) window(since time.Time, period time.Duration) histSlot {
	var sum histSlot
	if h == nil || period <= 0 {
		return sum
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	start := since.Unix() / histSlotSec
	end := since.Add(period).Unix() / histSlotSec
	for slot, v := range h.slots {
		if slot < start || slot >= end {
			continue
		}
		sum.tok += v.tok
		sum.calls += v.calls
		sum.in += v.in
		sum.out += v.out
		sum.peak += v.peak
	}
	return sum
}

// 只在"还没导入过"时调用（判据：槽表为空）。导入后置 imported（落盘靠"有数据"体现），
// 所以重启不会重复导入、不会重复累加。文件读不到就静默跳过，靠 API 粗桶兜底。
// 行格式：{"t":"RFC3339", "in":N, "out":N, ...}；只取最近 histKeepDays 天。
func (h *usageHist) importJSONL(path string) (int, error) {
	if h == nil || path == "" || path == "off" {
		return 0, nil
	}
	h.mu.Lock()
	if len(h.slots) > 0 { // 已有数据（导入过或自己累积过）→ 不再导入
		h.mu.Unlock()
		return 0, nil
	}
	h.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	cut := time.Now().Add(-histKeepDays * 24 * time.Hour)
	n := 0
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, line := range bytesSplitLines(data) {
		if len(line) < 2 {
			continue
		}
		row, ts, ok := parseUsageRow(line)
		if !ok || ts.Before(cut) {
			continue
		}
		if tok := int64(row.In + row.Out); tok > 0 {
			slot := h.slots[ts.Unix()/histSlotSec]
			slot.tok += tok
			slot.in += int64(row.In)
			slot.out += int64(row.Out)
			h.slots[ts.Unix()/histSlotSec] = slot
			n++
		}
	}
	if n > 0 {
		h.dirty = true
	}
	return n, nil
}

// usageLogRow：magpie 逐请求流水的字段子集。provider 可能是转售商，
// 所以 DeepSeek 判定要看 model/req/served（如 opencode-go/deepseek-v4.1-flash）。
type usageLogRow struct {
	T        string  `json:"t"`
	In       float64 `json:"in"`
	Out      float64 `json:"out"`
	Provider string  `json:"provider"`
	Model    string  `json:"model"`
	Req      string  `json:"req"`
	Served   string  `json:"served"`
	Agent    string  `json:"agent"`
	Caller   string  `json:"callerKeyName"`
	EP       string  `json:"ep"`
	Kind     string  `json:"kind"`
	Source   string  `json:"source"`
	Status   int     `json:"status"`
	Err      string  `json:"err"`
	ErrType  string  `json:"err_type"`
	Rejected bool    `json:"rejected"`
}

func parseUsageRow(line []byte) (usageLogRow, time.Time, bool) {
	var row usageLogRow
	if json.Unmarshal(line, &row) != nil {
		return row, time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, row.T)
	if err != nil {
		return row, time.Time{}, false
	}
	return row, ts, true
}

func providerPricedForDeepSeekPeak(row usageLogRow) bool {
	p := row.Provider
	if strings.TrimSpace(p) == "" {
		p = row.Req
	}
	return isPeakPricedProvider(p)
}

func usagePeakTokens(row usageLogRow, ts time.Time) int64 {
	if !mentionsDeepSeek(row.Provider, row.Model, row.Req, row.Served) {
		return 0
	}
	if !providerPricedForDeepSeekPeak(row) {
		return 0
	}
	if !isDeepSeekPeak(ts) {
		return 0
	}
	return int64(row.In + row.Out)
}

// initPeak：绑定 magpie 流水并从全量流水重建 peak。
// 注意：不能"在已有 peak 上加"——重启会重复；所以启动时先清零再重建。
func (h *usageHist) initPeak(logPath string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.logPath = logPath
	h.logOffset = 0
	h.peakReady = false
	h.mu.Unlock()
	h.rebuildPeak()
}

func (h *usageHist) rebuildPeak() {
	if h == nil {
		return
	}
	h.mu.Lock()
	path := h.logPath
	h.mu.Unlock()
	if path == "" || path == "off" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	// 只处理到最后一个完整行；半行留给下一次 tail。
	complete := data
	if i := bytes.LastIndexByte(data, '\n'); i >= 0 {
		complete = data[:i+1]
	} else {
		complete = nil
	}
	peaks := make(map[int64]int64)
	cut := time.Now().Add(-histKeepDays * 24 * time.Hour)
	h.errs.reset()
	for _, line := range bytesSplitLines(complete) {
		row, ts, ok := parseUsageRow(line)
		if !ok {
			continue
		}
		h.errs.add(row, ts)
		if ts.Before(cut) {
			continue
		}
		if tok := usagePeakTokens(row, ts); tok > 0 {
			peaks[ts.Unix()/histSlotSec] += tok
		}
	}
	h.mu.Lock()
	// 已有槽按流水结果重设 peak（流水里没有的归零）。
	for slot, s := range h.slots {
		s.peak = peaks[slot]
		h.slots[slot] = s
		delete(peaks, slot)
	}
	// 流水里有、但聚合采样还没建槽的分钟：保留 peak，等 tok 采样到位。
	for slot, p := range peaks {
		h.slots[slot] = histSlot{peak: p}
	}
	h.logOffset = int64(len(complete))
	h.peakReady = true
	h.dirty = true
	h.mu.Unlock()
	h.save(true)
}

// syncPeak：增量读取 magpie 流水，只累加 peak（tok 由 2s 聚合采样负责）。
func (h *usageHist) syncPeak() {
	if h == nil {
		return
	}
	h.mu.Lock()
	path, off, ready := h.logPath, h.logOffset, h.peakReady
	h.mu.Unlock()
	if path == "" || path == "off" {
		return
	}
	if !ready {
		h.rebuildPeak()
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return
	}
	if st.Size() < off {
		h.rebuildPeak()
		return
	}
	if st.Size() == off {
		return
	}
	if _, err := f.Seek(off, 0); err != nil {
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, st.Size()-off))
	if err != nil || len(data) == 0 {
		return
	}
	complete := data
	if i := bytes.LastIndexByte(data, '\n'); i >= 0 {
		complete = data[:i+1]
	} else {
		return // 还没写完整行
	}
	added := false
	h.mu.Lock()
	for _, line := range bytesSplitLines(complete) {
		row, ts, ok := parseUsageRow(line)
		if !ok {
			continue
		}
		h.errs.add(row, ts)
		tok := usagePeakTokens(row, ts)
		if tok <= 0 {
			continue
		}
		slot := ts.Unix() / histSlotSec
		s := h.slots[slot]
		s.peak += tok
		h.slots[slot] = s
		added = true
	}
	h.logOffset = off + int64(len(complete))
	if added {
		h.dirty = true
	}
	h.mu.Unlock()
}

// bytesSplitLines：按 '\n' 切行（不额外分配切片头以外的东西）。
func bytesSplitLines(b []byte) [][]byte {
	out := make([][]byte, 0, 1024)
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

// columnSpec：给周期挑一个**整数**列宽，使列数不超过可用像素宽。
// 返回 (每列时长, 列数)；列数 × 列宽 == 周期（今天 144×10m、7d 168×1h、30d 180×4h）。
// 列数通常小于像素宽，绘制时按最近邻把每列铺成 1~2px，**不留空白、也不额外扩时间范围**。

// series：把 1 分钟槽聚合成 n 个桶（1px 一桶）。
func (h *usageHist) series(since time.Time, period time.Duration, n int) []int64 {
	return h.seriesField(since, period, n, false)
}

// seriesPeak：同 series，但取每个桶的 DeepSeek 峰价 tokens（tok 的子集）。
func (h *usageHist) seriesPeak(since time.Time, period time.Duration, n int) []int64 {
	return h.seriesField(since, period, n, true)
}

func (h *usageHist) seriesField(since time.Time, period time.Duration, n int, peak bool) []int64 {
	if h == nil || n <= 0 || period <= 0 {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.slots) == 0 {
		return nil
	}
	out := make([]int64, n)
	start := since.Unix() / histSlotSec
	end := (since.Add(period)).Unix() / histSlotSec
	if end <= start {
		return nil
	}
	for slot, v := range h.slots {
		if slot < start || slot >= end {
			continue
		}
		idx := int(float64(slot-start) / float64(end-start) * float64(n))
		if idx >= n {
			idx = n - 1
		}
		if peak {
			out[idx] += v.peak
		} else {
			out[idx] += v.tok
		}
	}
	return out
}

func (h *usageHist) ready() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.slots) > 0
}
