package main

// screen_axis.go —— 用量直方图与时间轴：**按时间戳取样**，随时间连续左移。
//
// 旧实现先按桶铺像素，再在轴上画固定的整点/零点；一到两次轮询之间轴不动，
// 换时区也没有出口。这里改成：窗口 = [now-period, now]，每个像素问一次
// "这个时刻属于哪个桶"，柱与刻度共用同一套时间→像素映射，因此：
//   · 时间流逝时整张图按像素连续左移（刻度跟着柱子走，不会错位）；
//   · 时区只影响刻度对齐与标签文字，不影响取样。

import (
	"sort"
	"strconv"
	"time"
)

type usagePoint struct {
	ts   int64
	v    float64
	peak float64 // DeepSeek 峰价 tokens（v 的子集；缺省 0）
}

// usagePoints：快照分桶 → 带起点时间戳的序列（按时间升序）。
// 缺 ts 的旧数据按 since + i*col 补齐，保证老快照也能画。
func usagePoints(u map[string]any, since time.Time, col time.Duration) []usagePoint {
	arr, _ := u["buckets"].([]any)
	pts := make([]usagePoint, 0, len(arr))
	for i, raw := range arr {
		m, _ := raw.(map[string]any)
		v, ok := snapF(m, "tokens")
		if !ok {
			continue
		}
		ts, ok := snapF(m, "ts")
		if !ok || ts <= 0 {
			ts = float64(since.Add(time.Duration(i) * col).Unix())
		}
		p := usagePoint{ts: int64(ts), v: v}
		if peak, ok := snapF(m, "peak"); ok {
			p.peak = peak
		}
		pts = append(pts, p)
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].ts < pts[j].ts })
	return pts
}

// usageColSeconds：桶宽（秒）。优先快照的 col_seconds，其次相邻桶差的中位数。
func usageColSeconds(u map[string]any) int64 {
	if v, ok := snapF(u, "col_seconds"); ok && v > 0 {
		return int64(v)
	}
	arr, _ := u["buckets"].([]any)
	var prev int64
	diffs := make([]int64, 0, len(arr))
	for _, raw := range arr {
		m, _ := raw.(map[string]any)
		ts, ok := snapF(m, "ts")
		if !ok || ts <= 0 {
			continue
		}
		if prev > 0 && int64(ts) > prev {
			diffs = append(diffs, int64(ts)-prev)
		}
		prev = int64(ts)
	}
	if len(diffs) == 0 {
		return 0
	}
	sort.Slice(diffs, func(i, j int) bool { return diffs[i] < diffs[j] })
	return diffs[len(diffs)/2]
}

// bucketAt：二分找到 t 所属的桶；t 落在桶外返回 0。
func bucketAt(pts []usagePoint, col int64, t int64) (float64, int) {
	i := sort.Search(len(pts), func(i int) bool { return pts[i].ts > t }) - 1
	if i < 0 {
		return 0, -1
	}
	if col > 0 && t >= pts[i].ts+col {
		return 0, -1
	}
	return pts[i].v, i
}

// drawUsageChart：在 [start,end] 窗口上按像素取样画柱。
// showPeak：是否把 DeepSeek 峰价 tokens 叠加为红色柱段（蓝色总量保持不变）。
func drawUsageChart(c *Canvas, x, y, w, h int, pts []usagePoint, col int64, start, end int64, showPeak bool) {
	if len(pts) == 0 || w < 8 || h < 4 || end <= start {
		return
	}
	mx := 0.0
	for _, p := range pts {
		if p.v > mx {
			mx = p.v
		}
	}
	span := end - start
	last := len(pts) - 1
	for i := range w {
		t := start + span*int64(2*i+1)/(2*int64(w))
		v, bi := bucketAt(pts, col, t)
		bh := 1
		if mx > 0 && v > 0 {
			bh = int(float64(h) * v / mx)
			if bh < 1 {
				bh = 1
			}
		}
		colr := screenAccentDim
		if bi == last {
			colr = screenAccent
		}
		c.fillRect(x+i, y+h-bh, 1, bh, colr)
		if !showPeak || bi < 0 || v <= 0 {
			continue
		}
		peak := pts[bi].peak
		if peak > v {
			peak = v
		}
		if peak > 0 {
			ph := int(float64(h) * peak / mx)
			if ph < 1 {
				ph = 1
			}
			c.fillRect(x+i, y+h-ph, 1, ph, peakInk)
		}
	}
}

// axisLabelIn：轴文字按指定时区输出——today 是 2 位小时，其余是日号。
func axisLabelIn(at time.Time, per string, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	t := at.In(loc)
	if per == "today" {
		return t.Format("15")
	}
	return strconv.Itoa(t.Day())
}

// drawTimeAxisTZ：窗口下沿刻度 + 少量标签；刻度在指定时区的整点/零点上。
func drawTimeAxisTZ(c *Canvas, x, yb, w int, start, end time.Time, per string, loc *time.Location) {
	if w <= 0 || !end.After(start) {
		return
	}
	if loc == nil {
		loc = time.Local
	}
	c.fillRect(x, yb, w, 1, screenBorder)
	px := func(at time.Time) int {
		p := x + int(float64(at.Sub(start))/float64(end.Sub(start))*float64(w))
		if p < x {
			p = x
		}
		if p > x+w-1 {
			p = x + w - 1
		}
		return p
	}
	ls := start.In(loc)
	var tick, label time.Duration
	var align time.Time
	if per == "today" {
		tick, label = time.Hour, 6*time.Hour
		align = time.Date(ls.Year(), ls.Month(), ls.Day(), ls.Hour(), 0, 0, 0, loc)
		if align.Before(start) {
			align = align.Add(time.Hour)
		}
	} else {
		tick = 24 * time.Hour
		if per == "30d" {
			label = 7 * 24 * time.Hour
		} else {
			label = 24 * time.Hour
		}
		align = time.Date(ls.Year(), ls.Month(), ls.Day(), 0, 0, 0, 0, loc)
		if align.Before(start) {
			align = align.AddDate(0, 0, 1)
		}
	}
	for at := align; at.Before(end); at = at.Add(tick) {
		c.fillRect(px(at), yb+1, 1, 2, screenBorder)
	}
	if per == "today" {
		// 标签落在 0/6/12/18 点，而不是"第一个整点往后 6 小时"。
		h := align.Hour() / 6 * 6
		align = time.Date(align.Year(), align.Month(), align.Day(), h, 0, 0, 0, loc)
		if align.Before(start) {
			align = align.Add(6 * time.Hour)
		}
	}
	for at := align; at.Before(end); at = at.Add(label) {
		lbl := axisLabelIn(at, per, loc)
		tw := SM.width(lbl)
		lx := px(at) - tw/2
		if lx < x {
			lx = x
		}
		if lx+tw > x+w {
			lx = x + w - tw
		}
		SM.draw(c, lx, yb+4, lbl, screenMuted)
	}
}
