package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// peakTestMonday：找一个最近的非节假日周一 10:00（北京时间），保证落在
// 用量历史保留期内，测试不依赖运行日期。
func peakTestMonday(t *testing.T) time.Time {
	t.Helper()
	now := time.Now().In(deepseekPeakZone)
	d := time.Date(now.Year(), now.Month(), now.Day(), 10, 0, 0, 0, deepseekPeakZone)
	for d.Weekday() != time.Monday || isChinaHoliday(d) {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

func writeUsageLog(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%s\n", joinLines(lines))), 0o644); err != nil {
		t.Fatal(err)
	}
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}

func TestRebuildPeakFromJSONL(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "usage.jsonl")
	monday := peakTestMonday(t)
	offPeak := monday.Add(2*time.Hour + 30*time.Minute) // 12:30
	saturday := monday.AddDate(0, 0, 5)                 // 周六 10:00
	lines := []string{
		fmt.Sprintf(`{"t":%q,"provider":"opendesign","model":"deepseek-v4.1-flash","in":10,"out":5}`, monday.Format(time.RFC3339)),
		fmt.Sprintf(`{"t":%q,"provider":"openai","model":"gpt-6","in":9,"out":1}`, monday.Format(time.RFC3339)),
		fmt.Sprintf(`{"t":%q,"provider":"opendesign","model":"deepseek-v4.1-flash","in":7,"out":3}`, offPeak.Format(time.RFC3339)),
		fmt.Sprintf(`{"t":%q,"provider":"opencode-go","model":"deepseek-v4-flash","in":8,"out":2}`, saturday.Format(time.RFC3339)),
	}
	writeUsageLog(t, logPath, lines...)

	peakSlot := monday.Unix() / histSlotSec
	offSlot := offPeak.Unix() / histSlotSec
	satSlot := saturday.Unix() / histSlotSec
	h := &usageHist{
		path:  filepath.Join(dir, "hist.bin"),
		slots: map[int64]histSlot{peakSlot: {tok: 100}, offSlot: {tok: 20}, satSlot: {tok: 20}},
	}
	h.initPeak(logPath)
	if got := h.slots[peakSlot].peak; got != 15 {
		t.Fatalf("周一高峰 DeepSeek 应记 15，got %d", got)
	}
	if got := h.slots[offSlot].peak; got != 0 {
		t.Fatalf("12:30 应为低谷，got %d", got)
	}
	if got := h.slots[satSlot].peak; got != 0 {
		t.Fatalf("周六应为低谷，got %d", got)
	}
}

func TestSyncPeakTailsNewRows(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "usage.jsonl")
	at := peakTestMonday(t)
	writeUsageLog(t, logPath,
		fmt.Sprintf(`{"t":%q,"provider":"opendesign","model":"deepseek-v4.1-flash","in":10,"out":5}`, at.Format(time.RFC3339)),
	)
	h := &usageHist{path: filepath.Join(dir, "hist.bin"), slots: map[int64]histSlot{}}
	h.initPeak(logPath)
	if got := h.slots[at.Unix()/histSlotSec].peak; got != 15 {
		t.Fatalf("首次重建 = %d，want 15", got)
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(f, `{"t":%q,"provider":"opencode-go","model":"deepseek-v4-flash","in":4,"out":6}`+"\n",
		at.Add(time.Minute).Format(time.RFC3339))
	_ = f.Close()

	h.syncPeak()
	if got := h.slots[at.Unix()/histSlotSec].peak; got != 15 {
		t.Fatalf("旧槽不应重复累加，got %d", got)
	}
	if got := h.slots[at.Add(time.Minute).Unix()/histSlotSec].peak; got != 10 {
		t.Fatalf("增量行应记 10，got %d", got)
	}
	// 再同步一次：没有新行，不应重复
	h.syncPeak()
	if got := h.slots[at.Add(time.Minute).Unix()/histSlotSec].peak; got != 10 {
		t.Fatalf("重复 sync 不应累加，got %d", got)
	}
}

func TestUsageHistPeakPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.bin")
	slot := time.Now().Unix() / histSlotSec
	h := &usageHist{path: path, slots: map[int64]histSlot{slot: {tok: 100, peak: 40}}}
	h.dirty = true
	h.save(true)
	h2 := newUsageHist(path)
	if got := h2.slots[slot].peak; got != 40 {
		t.Fatalf("peak 落盘往返 = %d，want 40", got)
	}
}

func TestUsageHistNKH2Compat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.bin")
	slot := time.Now().Unix() / histSlotSec
	buf := make([]byte, 4+40)
	copy(buf[:4], histFileMagicV2)
	binary.LittleEndian.PutUint64(buf[4:], uint64(slot))
	binary.LittleEndian.PutUint64(buf[12:], 100)
	binary.LittleEndian.PutUint64(buf[20:], 3)
	binary.LittleEndian.PutUint64(buf[28:], 80)
	binary.LittleEndian.PutUint64(buf[36:], 20)
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	h := newUsageHist(path)
	if got := h.slots[slot]; got.tok != 100 || got.calls != 3 || got.in != 80 || got.out != 20 || got.peak != 0 {
		t.Fatalf("NKH2 兼容读取 = %+v", got)
	}
}

func TestPeakSeries(t *testing.T) {
	h := &usageHist{slots: map[int64]histSlot{}}
	since := time.Now().Add(-24 * time.Hour)
	h.slots[since.Unix()/histSlotSec] = histSlot{tok: 100, peak: 40}
	n := 24
	total := h.series(since, 24*time.Hour, n)
	peak := h.seriesPeak(since, 24*time.Hour, n)
	if total[0] != 100 || peak[0] != 40 {
		t.Fatalf("series = %d/%d，want 100/40", total[0], peak[0])
	}
}
