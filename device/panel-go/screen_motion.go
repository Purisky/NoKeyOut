package main

// screen_motion.go —— 有出处的动效参数（不自行发明）。
//
// 1) 不确定圆形进度指示器（供应商正在被调用时，围绕其图标）：
//    Material Design 3 / AndroidX Compose Material3 的官方实现参数
//    androidx.wear.compose.material3.CircularProgressIndicator.kt：
//      · 全局旋转 1440° / 5000ms（Linear）
//      · 附加旋转 0@1250ms、360@2500ms、360@3750ms、720@5000ms
//      · 扫掠长度 0→0.8@1250ms→0.2@2500ms→0.8@3750ms→0@5000ms
//      · 起始角 270°，段间缓动 = MotionTokens.EasingStandard (0.2, 0, 0, 1)
//    来源：https://android.googlesource.com/platform/frameworks/support/+/HEAD/wear/compose/compose-material3/src/main/java/androidx/wear/compose/material3/CircularProgressIndicator.kt
//
// 2) 顶层页面切换（LOCK ↔ ADMIN 打开）：
//    Material Design 的 fade through：总 300ms，出场 90ms，入场 210ms
//    （outgoing 完全淡出后 incoming 再淡入）。
//    来源：https://m2.material.io/design/motion/the-motion-system.html

import (
	"math"
	"time"
)

// ---- 缓动

// easingStandard：Material MotionTokens.EasingStandard = cubic-bezier(0.2, 0, 0, 1)。
func easingStandard(x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	// 二分求参数 t 使 x(t)=目标值，再算 y(t)。
	lo, hi := 0.0, 1.0
	for i := 0; i < 24; i++ {
		t := (lo + hi) / 2
		xt := 3*(1-t)*(1-t)*t*0.2 + t*t*t
		if xt < x {
			lo = t
		} else {
			hi = t
		}
	}
	t := (lo + hi) / 2
	return 3*(1-t)*t*t + t*t*t
}

// keyframe：时间点（秒）+ 目标值，段内用 easingStandard 过渡。
type keyframe struct {
	t float64
	v float64
}

func keyframesAt(sec float64, kf []keyframe) float64 {
	if sec <= kf[0].t {
		return kf[0].v
	}
	for i := 1; i < len(kf); i++ {
		if sec <= kf[i].t {
			span := kf[i].t - kf[i-1].t
			if span <= 0 {
				return kf[i].v
			}
			u := easingStandard((sec - kf[i-1].t) / span)
			return kf[i-1].v + (kf[i].v-kf[i-1].v)*u
		}
	}
	return kf[len(kf)-1].v
}

// ---- M3 不确定圆形指示器

var (
	m3RotationKF = []keyframe{{0, 0}, {1.25, 0}, {2.5, 360}, {3.75, 360}, {5, 720}}
	m3SweepKF    = []keyframe{{0, 0}, {1.25, 0.8}, {2.5, 0.2}, {3.75, 0.8}, {5, 0}}
)

// m3Indeterminate：返回（起始角 度，扫掠占比 0..1）。周期 5s。
func m3Indeterminate(now float64) (float64, float64) {
	t := math.Mod(now, 5)
	if t < 0 {
		t += 5
	}
	global := 1440 * t / 5
	add := keyframesAt(t, m3RotationKF)
	sweep := keyframesAt(t, m3SweepKF)
	return 270 + global + add, clamp01(sweep)
}

// drawM3Indeterminate：围绕 (cx,cy)、半径 r 画一段官方参数驱动的圆弧。
// now<=0 时不画（静态场景不引入噪声，黄金样本可复现）。
func drawM3Indeterminate(c *Canvas, cx, cy, r, thick int, color uint16, now float64) {
	if now <= 0 || r <= 1 || thick <= 0 {
		return
	}
	start, sweep := m3Indeterminate(now)
	if sweep <= 0.001 {
		return
	}
	a0 := start * math.Pi / 180
	a1 := (start + 360*sweep) * math.Pi / 180
	c.arc(cx, cy, r, a0, a1, color, thick)
}

// ---- Material shared axis（页面切换）

// shared axis：两个页面共享同一空间关系，出场向左滑出并淡出、入场从右滑入并淡入，
// 中间不出现整屏黑场，因此比 fade through 更连贯。
// 来源：https://m2.material.io/design/motion/the-motion-system.html（shared axis，300ms）

const (
	pageTransDur = 300 * time.Millisecond // 官方 300ms
	pageSlidePx  = 24                     // 240px 屏上约 10% 宽的位移，避免过度运动
)

// cloneSnap：浅拷贝快照（只改顶层 switch 键，不污染渲染线程持有的原 map）。
func cloneSnap(snap map[string]any) map[string]any {
	out := make(map[string]any, len(snap)+1)
	for k, v := range snap {
		out[k] = v
	}
	return out
}

// blendShift：把 src 整体平移 dx 后，以不透明度 a 叠到 dst（整帧，RGB565）。
func blendShift(dst, src *Canvas, dx int, a float64) {
	a = clamp01(a)
	if a <= 0 {
		return
	}
	for y := 0; y < screenH; y++ {
		row := y * screenW * 2
		for x := 0; x < screenW; x++ {
			sx := x - dx
			if sx < 0 || sx >= screenW {
				continue
			}
			di := row + x*2
			si := row + sx*2
			ss := uint16(src.Buf[si])<<8 | uint16(src.Buf[si+1])
			if a >= 1 {
				dst.Buf[di], dst.Buf[di+1] = src.Buf[si], src.Buf[si+1]
				continue
			}
			ds := uint16(dst.Buf[di])<<8 | uint16(dst.Buf[di+1])
			dr, dg, db := rgb565expand(ds)
			sr, sg, sb := rgb565expand(ss)
			r := uint16(float64(dr) + (float64(sr)-float64(dr))*a)
			g := uint16(float64(dg) + (float64(sg)-float64(dg))*a)
			b := uint16(float64(db) + (float64(sb)-float64(db))*a)
			v := rgb565(uint8(r), uint8(g), uint8(b))
			dst.Buf[di], dst.Buf[di+1] = byte(v>>8), byte(v)
		}
	}
}

// compositeSharedAxis：p∈[0,1]。旧页向左滑出并淡出，新页从右滑入并淡入。
func compositeSharedAxis(dst, from, to *Canvas, p float64) {
	p = clamp01(p)
	e := easingStandard(p)
	dst.clear(screenBG)
	blendShift(dst, from, -int(float64(pageSlidePx)*e), 1-e)
	blendShift(dst, to, pageSlidePx-int(float64(pageSlidePx)*e), e)
}
