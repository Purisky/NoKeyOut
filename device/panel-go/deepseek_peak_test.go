package main

import (
	"testing"
	"time"
)

func beijing(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, deepseekPeakZone)
}

func TestIsDeepSeekPeak(t *testing.T) {
	cases := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"周一早高峰起点", beijing(2026, 11, 9, 9, 0), true},
		{"周一早高峰中段", beijing(2026, 11, 9, 10, 30), true},
		{"午休", beijing(2026, 11, 9, 12, 0), false},
		{"午休末", beijing(2026, 11, 9, 13, 59), false},
		{"下午高峰起点", beijing(2026, 11, 9, 14, 0), true},
		{"下午高峰末", beijing(2026, 11, 9, 17, 59), true},
		{"晚高峰结束", beijing(2026, 11, 9, 18, 0), false},
		{"周六", beijing(2026, 11, 7, 10, 0), false},
		{"周日", beijing(2026, 11, 8, 10, 0), false},
		{"国庆周四", beijing(2026, 10, 1, 10, 0), false},
		{"春节周二", beijing(2026, 2, 17, 10, 0), false},
		{"国庆后首日", beijing(2026, 10, 8, 10, 0), true},
		{"春节后首日", beijing(2026, 2, 24, 10, 0), true},
		{"2027 端午", beijing(2027, 6, 9, 10, 0), false},
		{"2027 春节后", beijing(2027, 2, 15, 10, 0), true},
		{"UTC 01:00 = 北京 09:00", time.Date(2026, 11, 9, 1, 0, 0, 0, time.UTC), true},
		{"UTC 04:00 = 北京 12:00", time.Date(2026, 11, 9, 4, 0, 0, 0, time.UTC), false},
	}
	for _, c := range cases {
		if got := isDeepSeekPeak(c.at); got != c.want {
			t.Errorf("%s: isDeepSeekPeak(%s) = %v, want %v", c.name, c.at, got, c.want)
		}
	}
}

func TestChinaHolidaySet(t *testing.T) {
	// 区间首尾与中间日期都必须在表里。
	for _, day := range []string{
		"2026-01-01", "2026-01-03",
		"2026-02-15", "2026-02-23",
		"2026-04-06",
		"2026-05-05",
		"2026-06-21",
		"2026-09-25",
		"2026-10-07",
		"2027-02-05", "2027-02-13",
		"2027-06-09", "2027-09-15", "2027-10-07",
	} {
		d, err := time.ParseInLocation("2006-01-02", day, deepseekPeakZone)
		if err != nil {
			t.Fatal(err)
		}
		if !isChinaHoliday(d) {
			t.Errorf("%s 应在节假日表里", day)
		}
	}
	for _, day := range []string{"2026-01-04", "2026-02-14", "2026-10-08", "2027-02-14"} {
		d, err := time.ParseInLocation("2006-01-02", day, deepseekPeakZone)
		if err != nil {
			t.Fatal(err)
		}
		if isChinaHoliday(d) {
			t.Errorf("%s 不应在节假日表里（调休上班日按周末/工作日规则处理）", day)
		}
	}
}

func TestIsPeakPricedProvider(t *testing.T) {
	yes := []string{
		"deepseek", "deepseek/deepseek-flash",
		"opencode-go", "opencode-go/deepseek-v4.1-flash",
		"commandcode", "command-code", "command_code", "commandcode.ai",
	}
	for _, p := range yes {
		if !isPeakPricedProvider(p) {
			t.Errorf("%q 应按峰谷计价", p)
		}
	}
	no := []string{
		"opendesign", "opendesign/deepseek-v4.1-flash",
		"openai", "zhipu", "glm", "",
	}
	for _, p := range no {
		if isPeakPricedProvider(p) {
			t.Errorf("%q 不应按峰谷计价", p)
		}
	}
}

func TestMentionsDeepSeek(t *testing.T) {
	cases := []struct {
		parts []string
		want  bool
	}{
		{[]string{"opendesign", "deepseek-v4.1-flash", "opendesign/deepseek-v4.1-flash", ""}, true},
		{[]string{"opencode-go", "deepseek-v4-flash"}, true},
		{[]string{"deepseek", "deepseek-v4-pro"}, true},
		{[]string{"openai", "gpt-6", "openai/gpt-6", ""}, false},
		{[]string{"", ""}, false},
	}
	for _, c := range cases {
		if got := mentionsDeepSeek(c.parts...); got != c.want {
			t.Errorf("mentionsDeepSeek(%q) = %v, want %v", c.parts, got, c.want)
		}
	}
}
