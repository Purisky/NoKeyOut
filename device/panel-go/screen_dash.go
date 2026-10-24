package main

// screen_dash.go —— 配额数据层：把 magpie 真实响应的一条窗口变成屏上的一条结论。
//
//
//	{"data":[{"provider":"opencode-go","name":"OpenCode Go","kind":"plan",
//	          "windows":[{"name":"5 hours","used":0,"remaining":100},
//	          "lastServedAt":"...","last":true}],"object":"list"}
//
// 要点：**used/remaining 是百分比（0–100，两者相加=100），没有 total/unit**；
// resetsAt 只在部分窗口出现（RFC3339）。因此进度条直接画 remaining。

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

type poolState int

const (
	poolOK poolState = iota
	poolWarn
	poolCrit
	poolUnknown
)

const (
	critBelow = 15.0 // 剩余 <15% 判危急
	warnBelow = 40.0 // 剩余 <40% 判警告
	// staleAfter：usage 数据超过此时长未更新即视为过期。
	staleAfter = 60 * time.Second
)

// Pool：屏上一条配额窗口。
type Pool struct {
	Name     string // 供应商显示名，如 "OpenCode Go"
	Provider string // 供应商 id，如 "opencode-go"
	Window   string // 窗口名，如 "7 days"（上游给什么就显示什么）
	Kind     string // "plan" 等
	UsedPct     float64 // 已用 %（上游 used）
	RemainPct   float64 // 剩余 %（上游 remaining）
	HasVal      bool    // 上游给了百分比才有值
	State       poolState
	Reset       time.Time // 下次重置时刻
	HasReset    bool
	Period      time.Duration // 窗口周期（由窗口名解析；0=未知）

	//   "period"  = 周期窗口（used/remaining 是百分比，可能有 resetsAt）
	//   "balance" = **固定余额**（key 型供应商账上还有多少钱；没有周期、没有重置，百分比无意义）
	// 账号：magpie 的订阅/额度是按**账号**给的（provider.AccountRef / json:"accountNames"），
	// 同一 provider 可能有多个账号、窗口名完全相同 → 必须带上账号才能区分。
	// 上游没给就空（key 型供应商通常一个账号一个 provider id，天然可区分）。
	Account string

	Mode      string  // "period" | "balance"
	// Err：**上游明确报错**（magpie 的 data[].error，如密钥过期 401）。
	// 有它的时候这一行照常显示，错误写在条里——绝不能因为取不到数就把整条抹掉。
	Err       string
	Amount    float64 // 余额金额（Mode=balance）
	Unit      string  // 币种/单位（"USD" 等；上游没给就空）
	HasAmount bool
}

// remainState：剩余 % → 告警级别。
func remainState(remain float64) poolState {
	switch {
	case remain < critBelow:
		return poolCrit
	case remain < warnBelow:
		return poolWarn
	default:
		return poolOK
	}
}

// buildPool：一条上游窗口 → Pool。used/remaining 是百分比，直接采用。
func buildPool(provider, name, kind, window string, usedPct, remainPct float64, hasVal bool) Pool {
	p := Pool{Provider: provider, Name: name, Kind: kind, Window: window, Mode: "period",
		State: poolUnknown, Period: windowPeriod(window, time.Now())}
	if hasVal {
		p.UsedPct = math.Max(0, math.Min(100, usedPct))
		p.RemainPct = math.Max(0, math.Min(100, remainPct))
		p.HasVal = true
		p.State = remainState(p.RemainPct)
	}
	return p
}

// buildBalancePool：**固定余额**配额。上游只给"账上还剩多少钱"，没有周期/总量，
// 所以不画百分比、不算倒计时——直接报金额（百分比对余额没有意义）。
func buildBalancePool(provider, name string, amount float64, unit string) Pool {
	st := poolOK
	if amount <= 0 {
		st = poolCrit
	}
	return Pool{Provider: provider, Name: name, Kind: "balance", Mode: "balance",
		Window: "", State: st, Amount: amount, Unit: unit, HasAmount: true}
}

// sortPools：剩余升序（最紧的在最前）；无数据沉底；同档按名称稳定排序。
func sortPools(ps []Pool) {
	sort.SliceStable(ps, func(i, j int) bool {
		a, b := ps[i], ps[j]
		if a.HasVal != b.HasVal {
			return a.HasVal
		}
		if a.RemainPct != b.RemainPct {
			return a.RemainPct < b.RemainPct
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Window < b.Window
	})
}

// windowPeriod：由窗口名解析周期。上游用「5 hours」「7 days」「Month」这类名字，
// 名字本身就写着窗口长度，所以这是有据可依的解析（不是猜）：
//   - "30 minutes" / "5 hours" / "7 days" / "2 weeks" → 数值 × 单位
//   - "Month"（无数字）→ 当前自然月长度
//
// 识别不了返回 0（调用方据此不画时间标记）。
func windowPeriod(name string, now time.Time) time.Duration {
	s := strings.ToLower(strings.TrimSpace(name))
	fields := strings.Fields(s)
	if len(fields) == 2 {
		if n, err := strconv.Atoi(fields[0]); err == nil && n > 0 {
			unit := strings.TrimSuffix(fields[1], "s") // hours→hour, days→day
			switch unit {
			case "minute":
				return time.Duration(n) * time.Minute
			case "hour":
				return time.Duration(n) * time.Hour
			case "day":
				return time.Duration(n) * 24 * time.Hour
			case "week":
				return time.Duration(n) * 7 * 24 * time.Hour
			case "month":
				return monthLength(now) * time.Duration(n)
			}
		}
	}
	if strings.Contains(s, "month") {
		return monthLength(now)
	}
	return 0
}

// monthLength：当前自然月的实际长度（28–31 天）。
func monthLength(now time.Time) time.Duration {
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	return start.AddDate(0, 1, 0).Sub(start)
}

// timeFrac：窗口已过时间比例 [0,1]。需要重置时刻 + 周期；缺一不可（否则不画时间标记）。
func timeFrac(reset time.Time, period time.Duration, now time.Time) float64 {
	if period <= 0 || reset.IsZero() {
		return -1
	}
	left := reset.Sub(now)
	if left <= 0 {
		return 1 // 已经到点（上游值可能滞后），视为周期走完
	}
	if left >= period {
		return 0
	}
	return float64(period-left) / float64(period)
}
