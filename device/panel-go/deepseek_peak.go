package main

// deepseek_peak.go —— DeepSeek 峰谷定价时间表（内部维护）。
//
// 官方口径（https://api-docs.deepseek.com/zh-cn/quick_start/pricing）：
//   · 北京时间周一至周五 09:00-12:00、14:00-18:00 为高峰时段；
//   · 不含中国法定节假日；
//   · 其余时段（含周末、法定节假日全天）为空闲时段；
//   · 空闲价为高峰价的一半。
//
// 节假日表来自国务院办公厅 2026/2027 年放假通知：
//   · 2026：国办发明电〔2025〕7号（2025-11-04）
//   · 2027：国办发明电〔2026〕10号（2026-11-05）
// 每年国务院公布下一年安排后，在 chinaHolidayRanges 里追加即可；周末已由
// isDeepSeekPeak 直接判为空闲，表里保留完整假期区间是为了可读性。

import (
	"strings"
	"time"
)

// DeepSeek 计费按北京时间（CST，UTC+8，无夏令时）。
var deepseekPeakZone = time.FixedZone("CST", 8*3600)

// chinaHolidayRanges：闭区间，含首尾日期。
var chinaHolidayRanges = [][2]string{
	// 2026
	{"2026-01-01", "2026-01-03"}, // 元旦
	{"2026-02-15", "2026-02-23"}, // 春节
	{"2026-04-04", "2026-04-06"}, // 清明
	{"2026-05-01", "2026-05-05"}, // 劳动节
	{"2026-06-19", "2026-06-21"}, // 端午
	{"2026-09-25", "2026-09-27"}, // 中秋
	{"2026-10-01", "2026-10-07"}, // 国庆
	// 2027
	{"2027-01-01", "2027-01-03"}, // 元旦
	{"2027-02-05", "2027-02-13"}, // 春节
	{"2027-04-03", "2027-04-05"}, // 清明
	{"2027-05-01", "2027-05-05"}, // 劳动节
	{"2027-06-09", "2027-06-09"}, // 端午
	{"2027-09-15", "2027-09-15"}, // 中秋
	{"2027-10-01", "2027-10-07"}, // 国庆
}

var chinaHolidays = buildChinaHolidays()

func buildChinaHolidays() map[string]struct{} {
	set := make(map[string]struct{}, 64)
	for _, r := range chinaHolidayRanges {
		from, err := time.ParseInLocation("2006-01-02", r[0], deepseekPeakZone)
		if err != nil {
			panic("deepseek peak: bad holiday start " + r[0])
		}
		to, err := time.ParseInLocation("2006-01-02", r[1], deepseekPeakZone)
		if err != nil {
			panic("deepseek peak: bad holiday end " + r[1])
		}
		for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
			set[d.Format("2006-01-02")] = struct{}{}
		}
	}
	return set
}

// isChinaHoliday：t 所在的北京日期是否为中国法定节假日。
func isChinaHoliday(t time.Time) bool {
	_, ok := chinaHolidays[t.In(deepseekPeakZone).Format("2006-01-02")]
	return ok
}

// isDeepSeekPeak：t 是否落在 DeepSeek 高峰时段。
// 高峰：周一至周五 09:00-12:00、14:00-18:00（北京时间），法定节假日除外。
func isDeepSeekPeak(t time.Time) bool {
	lt := t.In(deepseekPeakZone)
	switch lt.Weekday() {
	case time.Saturday, time.Sunday:
		return false
	}
	if isChinaHoliday(lt) {
		return false
	}
	h := lt.Hour()
	return (h >= 9 && h < 12) || (h >= 14 && h < 18)
}

// mentionsDeepSeek：供应商/模型/请求名任一命中 deepseek 即视为 DeepSeek 调用。
// magpie 流水的 provider 可能是 opencode-go、opendesign 等转售商，模型名仍是
// deepseek-v4.1-flash；所以不能只看 provider。
func mentionsDeepSeek(parts ...string) bool {
	for _, p := range parts {
		if strings.Contains(strings.ToLower(p), "deepseek") {
			return true
		}
	}
	return false
}

// isPeakPricedProvider：该 provider 是否真的按 DeepSeek 官方峰谷价计费。
//
//	· DeepSeek 官方、OpenCode Go、Command Code 的 DeepSeek 模型：峰谷计价；
//	· OpenDesign 订阅套餐：固定额度倍率（不是每日峰谷），不参与；
//	· GLM 等其它模型：没有峰谷计价，不参与。
//
// provider 也可能是 "opencode-go/deepseek-v4.1-flash" 这类带前缀的 req。
func isPeakPricedProvider(provider string) bool {
	p := strings.ToLower(strings.TrimSpace(provider))
	switch {
	case p == "deepseek" || strings.HasPrefix(p, "deepseek"):
		return true
	case strings.Contains(p, "opencode-go"):
		return true
	case strings.Contains(p, "commandcode") || strings.Contains(p, "command-code") || strings.Contains(p, "command_code"):
		return true
	default:
		return false
	}
}
