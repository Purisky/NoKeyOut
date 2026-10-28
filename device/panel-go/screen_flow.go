package main

import "math"

// flowPalette：青绿 → 靛蓝 → 品红。
var flowPalette = [3][3]float64{
	{0.0, 0.78, 0.66},
	{0.24, 0.35, 1.0},
	{1.0, 0.24, 0.60},
}

// rotateHue：绕灰轴旋转色相（弧度），用于拉开色域。
func rotateHue(r, g, b, a float64) (float64, float64, float64) {
	c, s := math.Cos(a), math.Sin(a)
	return (0.299+0.701*c+0.168*s)*r + (0.587-0.587*c+0.330*s)*g + (0.114-0.114*c-0.497*s)*b,
		(0.299-0.299*c-0.328*s)*r + (0.587+0.413*c+0.035*s)*g + (0.114-0.114*c+0.292*s)*b,
		(0.299-0.300*c+1.250*s)*r + (0.587-0.588*c-1.050*s)*g + (0.114+0.886*c-0.203*s)*b
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

	var out [3]float64
	for ch := 0; ch < 3; ch++ {
		out[ch] = (0.3/contrast)*flowPalette[0][ch] +
			(1-0.3/contrast)*(flowPalette[0][ch]*c1p+flowPalette[1][ch]*c2p+flowPalette[2][ch]*c3p) +
			light
	}
	out[0], out[1], out[2] = rotateHue(out[0], out[1], out[2], 0.70*math.Sin(now*0.4)+0.35*math.Sin(ang*2))
	var rgb [3]uint8
	for ch := 0; ch < 3; ch++ {
		rgb[ch] = uint8(math.Max(0, math.Min(1, out[ch])) * 255)
	}
	return rgb565(rgb[0], rgb[1], rgb[2])
}
