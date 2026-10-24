package main

// screen_period.go —— 用量周期语义：布局渲染与 WASM 共用，不依赖网络/代理。

import "time"

// histColCandidates：一列代表的时长候选（**整数**，按从小到大）。
// 用户要求"尽可能用整数"：10 分钟 / 60 分钟 / 4 小时这种，而不是 6.4 分钟。
var histColCandidates = []time.Duration{
	time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute,
	30 * time.Minute, time.Hour, 2 * time.Hour, 3 * time.Hour, 4 * time.Hour,
	6 * time.Hour, 8 * time.Hour, 12 * time.Hour, 24 * time.Hour,
}
// usagePeriods：屏上可选的三个用量周期。magpie 的 period 参数是 today / 7d / 30d
// （传 "week" 会被它归一成 30d，所以这里只用它认的三个）。
var usagePeriods = []string{"today", "7d", "30d"}

// usagePeriodLen：周期长度（与 magpie 的 period 语义一致）。
func usagePeriodLen(per string) time.Duration {
	switch per {
	case "today":
		return 24 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	}
	return 0
}

func columnSpec(period time.Duration, width int) (time.Duration, int) {
	if period <= 0 || width <= 0 {
		return 0, 0
	}
	for _, d := range histColCandidates {
		n := int((period + d - 1) / d)
		if n <= width {
			return d, n
		}
	}
	last := histColCandidates[len(histColCandidates)-1]
	return last, int((period + last - 1) / last)
}
