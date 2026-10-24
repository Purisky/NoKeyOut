package main

// screen_brand.go —— 供应商徽标：20×20 品牌色块 + 单字母。
// 设备侧不带 SVG 光栅器，真 logo 需离线烘焙成位图；
// 匹配对 provider 与显示名做小写子串匹配，兼容 "Anthropic Claude" 这类名字。

import (
	"strings"
)

type providerMark struct {
	color uint16
	mark  string // 字母兜底（没有图标时用）
	logo  string // 图标掩码 key（screen_logo_gen.go），空 = 无图标
}

var providerMarkRules = []struct {
	match []string
	mark  providerMark
}{
	{[]string{"opencode"}, providerMark{rgb565(120, 134, 168), "O", "opencode"}},
	{[]string{"openai"}, providerMark{rgb565(16, 163, 127), "O", "openai"}},
	{[]string{"anthropic", "claude"}, providerMark{rgb565(204, 120, 92), "A", "anthropic"}},
	{[]string{"gemini", "google"}, providerMark{rgb565(66, 133, 244), "G", "googlegemini"}},
	{[]string{"deepseek"}, providerMark{rgb565(77, 107, 254), "D", "deepseek"}},
	{[]string{"moonshot", "kimi"}, providerMark{rgb565(96, 96, 96), "K", "moonshot"}},
	{[]string{"zhipu", "glm"}, providerMark{rgb565(90, 70, 230), "Z", "zhipu"}},
	{[]string{"minimax"}, providerMark{rgb565(232, 64, 126), "M", "minimax"}},
	{[]string{"stepfun"}, providerMark{rgb565(255, 140, 0), "S", "stepfun"}},
	{[]string{"siliconflow"}, providerMark{rgb565(110, 80, 220), "F", "siliconflow"}},
	{[]string{"qwen", "dashscope", "tongyi"}, providerMark{rgb565(106, 77, 255), "Q", "qwen"}},
	{[]string{"openrouter"}, providerMark{rgb565(100, 100, 100), "R", "openrouter"}},
	{[]string{"mistral"}, providerMark{rgb565(255, 105, 40), "M", "mistralai"}},
	{[]string{"ollama"}, providerMark{rgb565(150, 150, 150), "L", "ollama"}},
	{[]string{"meta", "llama"}, providerMark{rgb565(60, 120, 240), "M", "meta"}},
	{[]string{"xai", "grok"}, providerMark{rgb565(160, 160, 170), "X", "xai"}},
	{[]string{"groq"}, providerMark{rgb565(245, 90, 60), "G", "groq"}},
	{[]string{"bytedance", "doubao", "volcengine"}, providerMark{rgb565(50, 110, 240), "B", "bytedance"}},
	{[]string{"tencent", "hunyuan"}, providerMark{rgb565(60, 130, 250), "T", "hunyuan"}},
	{[]string{"baidu", "ernie", "wenxin"}, providerMark{rgb565(41, 121, 255), "B", "baidu"}},
	{[]string{"cohere"}, providerMark{rgb565(57, 93, 148), "C", "cohere"}},
	{[]string{"ai21"}, providerMark{rgb565(230, 130, 150), "A", "ai21"}},
	{[]string{"perplexity"}, providerMark{rgb565(32, 139, 141), "P", "perplexity"}},
}

// providerBrand：未知供应商 → 中性灰 + 显示名首字母（不编造品牌色/图标）。
func providerBrand(provider, name string) providerMark {
	key := strings.ToLower(provider + " " + name)
	for _, r := range providerMarkRules {
		for _, m := range r.match {
			if strings.Contains(key, m) {
				return r.mark
			}
		}
	}
	mark := "?"
	for _, r := range name {
		mark = strings.ToUpper(string(r))
		break
	}
	return providerMark{rgb565(74, 85, 104), mark, ""}
}

// inkOnDark：把品牌色抬到**深色底上可读**的亮度（保色相，向白混合；阈值 110 只救很暗的品牌色，
// opencode 这类中亮度色保持原样，不会被抬成白色）。
// 源 SVG 全是 currentColor 单色字形，所以图标只能是「单色字形」——
// 早期用「白字形压品牌色块」，淡品牌色（如 opencode 的 120,134,168）会糊成一片灰，
func inkOnDark(c uint16) uint16 {
	r, g, b := rgb565expand(c)
	lum := func(r, g, b float64) float64 { return 0.2126*r + 0.7152*g + 0.0722*b }
	if lum(float64(r), float64(g), float64(b)) >= 110 {
		return rgb565(r, g, b)
	}
	for k := 0.1; k <= 1.001; k += 0.1 {
		nr := uint8(float64(r) + (255-float64(r))*k)
		ng := uint8(float64(g) + (255-float64(g))*k)
		nb := uint8(float64(b) + (255-float64(b))*k)
		if lum(float64(nr), float64(ng), float64(nb)) >= 110 {
			return rgb565(nr, ng, nb)
		}
	}
	return screenFG
}

// logoMask：取指定尺寸的供应商图标掩码（1bpp，硬像素）；没有就返回 ok=false（调用方退回字母）。
func logoMask(mk providerMark, size int) ([]byte, bool) {
	if mk.logo == "" {
		return nil, false
	}
	switch size {
	case 18:
		b, ok := logoSprites18[mk.logo]
		return b, ok
	}
	b, ok := logoSprites32[mk.logo]
	return b, ok
}
