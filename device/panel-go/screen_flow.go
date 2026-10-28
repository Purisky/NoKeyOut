package main

// screen_flow.go —— 字标/图标的流光取色。
//
// 算法来自 Balatro 的 paint-swirl 背景着色器（社区从游戏源码移植的 GLSL 版本）：
//   https://github.com/xxidbr9/balatro-effect-recreate/blob/master/background.gdshader
// 步骤：极坐标旋转 → uv*30 → 5 次正弦域扭曲 → 三段调色板 + 高光。
// 这里只在**墨迹像素**上取样（约 1–2k 像素），因此可以逐帧跑而不影响 30fps。
// now<=0 时退回纯色，保证黄金样本可复现。

import "math"

// 三段调色板（0..1 RGB）：沿用页面的 teal → blue → violet。
var flowPalette = [3][3]float64{
	{40.0 / 255, 196.0 / 255, 178.0 / 255},
	{76.0 / 255, 141.0 / 255, 255.0 / 255},
	{177.0 / 255, 92.0 / 255, 255.0 / 255},
}

// balatroFlow：屏幕坐标 (x,y) 在 now 时刻的流光颜色。
func balatroFlow(x, y int, now float64) uint16 {
	if now <= 0 {
		return screenFG
	}
	const spinAmount = 0.36 // 游戏默认
	const contrast = 2.0
	// 归一化到以屏幕中心为原点的 [-0.5,0.5]。
	ux := float64(x-screenW/2) / float64(screenW)
	uy := float64(y-screenH/2) / float64(screenH)
	uvLen := math.Hypot(ux, uy)

	// 极坐标旋转：中心转得快、边缘慢，形成涡旋。
	speed := now*1.2*0.2 + 302.2
	ang := math.Atan2(uy, ux) + speed - 20*(spinAmount*uvLen+(1-spinAmount))
	ux, uy = uvLen*math.Cos(ang), uvLen*math.Sin(ang)

	uvx, uvy := ux*30, uy*30
	uv2x, uv2y := uvx+uvy, uvx+uvy
	ts := now
	for i := 0; i < 5; i++ {
		s := math.Sin(math.Max(uvx, uvy))
		uv2x += s + uvx
		uv2y += s + uvy
		uvx += 0.5 * math.Cos(5.1123314+0.353*uv2y+ts*0.131121)
		uvy += 0.5 * math.Sin(uv2x-0.113*ts)
		d := math.Cos(uvx+uvy) - math.Sin(uvx*0.711-uvy)
		uvx -= d
		uvy -= d
	}

	// 三段调色板混合 + 高光（与 shader 的 c1p/c2p/c3p 一致）。
	cm := 0.25*contrast + 0.5*spinAmount + 1.2
	res := math.Min(2, math.Max(0, math.Hypot(uvx, uvy)*0.035*cm))
	c1p := math.Max(0, 1-cm*math.Abs(1-res))
	c2p := math.Max(0, 1-cm*math.Abs(res))
	c3p := 1 - math.Min(1, c1p+c2p)
	light := (0.4-0.2)*math.Max(c1p*5-4, 0) + 0.4*math.Max(c2p*5-4, 0)

	var rgb [3]uint8
	for ch := 0; ch < 3; ch++ {
		v := (0.3/contrast)*flowPalette[0][ch] +
			(1-0.3/contrast)*(flowPalette[0][ch]*c1p+flowPalette[1][ch]*c2p+flowPalette[2][ch]*c3p) +
			light
		rgb[ch] = uint8(math.Max(0, math.Min(1, v)) * 255)
	}
	return rgb565(rgb[0], rgb[1], rgb[2])
}
