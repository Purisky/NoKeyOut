package main

import "math"

// flowPalette：teal → blue → violet。
var flowPalette = [3][3]float64{
	{40.0 / 255, 196.0 / 255, 178.0 / 255},
	{76.0 / 255, 141.0 / 255, 255.0 / 255},
	{177.0 / 255, 92.0 / 255, 255.0 / 255},
}

// swirlAt：屏幕像素 (x,y) 在 now 时刻的漩涡取色；now<=0 返回纯色。
func swirlAt(x, y int, now float64) uint16 {
	if now <= 0 {
		return screenFG
	}
	const spinAmount = 0.36
	const contrast = 2.0

	ux := float64(x-screenW/2) / float64(screenW)
	uy := float64(y-screenH/2) / float64(screenH)
	uvLen := math.Hypot(ux, uy)

	speed := now*1.2*0.2 + 302.2
	ang := math.Atan2(uy, ux) + speed - 20*(spinAmount*uvLen+(1-spinAmount))
	ux, uy = uvLen*math.Cos(ang), uvLen*math.Sin(ang)

	uvx, uvy := ux*30, uy*30
	uv2x, uv2y := uvx+uvy, uvx+uvy
	for i := 0; i < 5; i++ {
		s := math.Sin(math.Max(uvx, uvy))
		uv2x += s + uvx
		uv2y += s + uvy
		uvx += 0.5 * math.Cos(5.1123314+0.353*uv2y+now*0.131121)
		uvy += 0.5 * math.Sin(uv2x-0.113*now)
		d := math.Cos(uvx+uvy) - math.Sin(uvx*0.711-uvy)
		uvx -= d
		uvy -= d
	}

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
