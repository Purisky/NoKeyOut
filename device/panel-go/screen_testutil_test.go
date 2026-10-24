package main

// screen_testutil_test.go —— 测试夹具：一份**符合真实契约**的快照。
//
// 契约来自设备行为（magpie /v1/magpie/quotas）：配额窗口给 used/remaining 百分比
// （used/total 绝对量）导致真实数据下配额行永远显示不出来——所以夹具必须照真契约写。

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 固定时钟：testSnap() 和黄金样本都用它。
var now = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

// TestMain：测试期一律用**默认模块布局 + 固定时钟**。
// 否则黄金样本会随开发机/设备上的 screen-layout.json 与墙上时钟变化——版式回归就失去意义了。
func TestMain(m *testing.M) {
	p := filepath.Join(os.TempDir(), "nokeyout-test-screen-layout.json")
	_ = os.Remove(p)
	os.Setenv(screenLayoutEnvKey, p)
	layout = &layoutStore{doc: layoutDoc{Modules: screenDefaultModules}}
	nowFn = func() time.Time { return time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC) }
	os.Exit(m.Run())
}

// hourlyBuckets / dailyBuckets：固定的分桶形状（黄金样本必须稳定，不能用随机数）。
func hourlyBuckets() []any {
	w := []int64{0, 0, 0, 0, 0, 0, 1200, 8200, 41000, 96000, 88000, 152000}
	out := make([]any, 0, 24)
	for i := 0; i < 24; i++ {
		v := int64(0)
		if i < len(w) {
			v = w[i]
		} else if i == 23 {
			v = 96000
		} else {
			v = int64(52000 + (i-12)*3000)
		}
		out = append(out, map[string]any{"label": fmt.Sprintf("%02d", i), "tokens": v})
	}
	return out
}

func dailyBuckets(n int, scale float64) []any {
	out := make([]any, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, map[string]any{
			"label":  fmt.Sprintf("d%d", i),
			"tokens": int64(scale * float64(180000+((i*7919)%260000))),
		})
	}
	return out
}

func testSnap() map[string]any {
	return map[string]any{
		"generated_at": now.Unix(),
		"switch":       "locked",
		"lang":         "zh",
		"error":        "",
		"usage_age_s":  1.8,
		"dash":         map[string]any{"age_s": 1.8, "stale": false},
		"net":          map[string]any{"eth": "up", "inet": true, "age_s": 1.0},
		"traffic": map[string]any{
			"tok_s": 42.7, "calls_s": 0.42, "errors_s": 0.01, "cache_hit_pct": 91.0,
			"tokens_today": map[string]any{"input": 1284553.0, "output": 891200.0},
			"calls_today":  3120.0, "errors_today": 2.0,
		},
		// 三周期用量（直方图来源）：桶是上游分桶，这里用固定形状保证黄金样本稳定。
		"usage": map[string]any{
			"today": map[string]any{"period": "today", "since": now.Truncate(24 * time.Hour).Format(time.RFC3339), "tokens": 2175753, "input": 1284553, "output": 891200,
				"calls": 3120, "cache_hit_pct": 91.0, "buckets": hourlyBuckets()},
			"7d": map[string]any{"period": "7d", "since": now.Add(-7 * 24 * time.Hour).Format(time.RFC3339), "tokens": 8451233, "input": 5120000, "output": 3331233,
				"calls": 18400, "cache_hit_pct": 88.5, "buckets": dailyBuckets(7, 1.0)},
			"30d": map[string]any{"period": "30d", "since": now.Add(-30 * 24 * time.Hour).Format(time.RFC3339), "tokens": 33810335, "input": 21000000, "output": 12810335,
				"calls": 73500, "cache_hit_pct": 86.2, "buckets": dailyBuckets(30, 1.6)},
		},
		"pools": []any{
			map[string]any{"name": "OpenCode Go", "provider": "opencode-go", "window": "Month",
				"kind": "plan", "state": int(poolCrit), "has_val": true,
				"used_pct": 100.0, "remain_pct": 0.0,
				"has_reset": true, "reset_at": float64(now.Add(6 * 24 * time.Hour).Unix()),
				"period_s": int64(31 * 24 * 3600)},
			map[string]any{"name": "OpenCode Go", "provider": "opencode-go", "window": "7 days",
				"kind": "plan", "state": int(poolOK), "has_val": true,
				"used_pct": 22.0, "remain_pct": 78.0,
				"has_reset": true, "reset_at": float64(now.Add(3 * 24 * time.Hour).Unix()),
				"period_s": int64(7 * 24 * 3600)},
			map[string]any{"name": "DeepSeek", "provider": "deepseek", "window": "Month",
				"kind": "plan", "state": int(poolWarn), "has_val": true,
				"used_pct": 68.0, "remain_pct": 32.0,
				"has_reset": true, "reset_at": float64(now.Add(11 * 24 * time.Hour).Unix()),
				"period_s": int64(31 * 24 * 3600)},
		},
	}
}

// ---- 像素断言小工具（多处测试共用） ----

// maxRunColor：rows 0..h-1 内某颜色的最长连续横段。
func maxRunColor(c *Canvas, h int, col uint16) int {
	best := 0
	for y := 0; y < h; y++ {
		run := 0
		for x := 0; x < screenW; x++ {
			if pxAt(c, x, y) == col {
				run++
				if run > best {
					best = run
				}
			} else {
				run = 0
			}
		}
	}
	return best
}

// inkCount：矩形内非底色的像素数。
func inkCount(c *Canvas, x0, x1, y0, y1 int) int {
	n := 0
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if pxAt(c, x, y) != uint16(screenBG) {
				n++
			}
		}
	}
	return n
}

// inkCountColor：矩形内某特定颜色的像素数。
func inkCountColor(c *Canvas, x0, x1, y0, y1 int, col uint16) int {
	n := 0
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if pxAt(c, x, y) == col {
				n++
			}
		}
	}
	return n
}
