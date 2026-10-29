//go:build !(js && wasm)

package main

// screen_run.go —— screend Go 版：panel 进程内的屏幕渲染线程
//   · 进程内读 stats.snapshot() + switchState()——不走 HTTPS、不需要 screen token
//   · LOCKED 时 panel 监听会被门控关掉，但本线程在进程内照常渲染锁定页
//   · 任何 panic/设备错误只影响屏幕，绝不影响 panel 本体

import (
	"fmt"
	"log"
	"math"
	"os"
	"time"
)

// screenState：真机与 Web 预览共用的有效快照（含调试注入/画面模拟锁定）。
func (p *panel) screenState() map[string]any {
	snap := p.stats.snapshot()
	if screenctl.injected() {
		snap["error"] = "injected: stats error (debug)"
	}
	if screenctl.simLocked() { // 画面模拟 ADMIN（端口开放）：只改快照给渲染器，**不动真门控**
		snap["switch"] = "unlocked"
	}
	if snapS(snap, "switch") == "unlocked" {
		snap["admin_s"] = adminOpenSeconds() // ADMIN 页计时；≥15min 开始闪烁
	}
	return snap
}

// startScreen：SCREEN_BACKEND 为空时直接返回（默认不启用，可选组件）。
func (p *panel) startScreen() {
	backend := p.cfg.screenBackend
	if backend == "" {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("screen: panic recovered（panel 不受影响）: %v", r)
			}
		}()
		p.screenLoop(backend)
	}()
}

// buildScreenDev：按 backend 建一次设备（上电门 / 自检 / 打不开时退 console）。
// 返回 (dev, label, ok)。方向或面板尺寸变更后由渲染线程重新调用。
func (p *panel) buildScreenDev(backend string) (screenDev, string, bool) {
	p.screen = nil
	switch backend {
	case "st7789":
		if p.cfg.screenGate != "" {
			p.screenGateHold()
		}
		st, err := p.openScreenHW()
		if p.cfg.screenBL >= 0 {
			p.blOpen() // 门已通过（screenGateHold 返回）→ 背光才点亮
		}
		if err != nil {
			// 硬件打不开 → 退回 console：屏幕上没有，但日志里有（服务不坠落）
			log.Printf("screen: 打不开 ST7789（%v）→ 退回 console 输出", err)
			return nil, "console(fallback)", true
		}
		if p.cfg.screenSelfTest {
			log.Printf("screen: 自检（四色块：红/绿/蓝/白）…")
			if err := st.selftest(); err != nil {
				log.Printf("screen: 自检失败：%v", err)
			} else {
				log.Printf("screen: 自检完成。若花屏，调整 madctl/偏移/invert")
			}
		}
		return st, "st7789", true
	case "png":
		pd := newShadowScreen(p.cfg.screenPngDir, p.cfg.screenPngScale)
		p.screen = pd
		return pd, "png→" + pd.dir, true
	case "web":
		sc := newShadowScreen(p.cfg.screenPngDir, p.cfg.screenPngScale) // dir 可空：只 HTTP 不落盘
		p.screen = sc
		return sc, "web(/screen)", true
	case "console":
		return nil, "console", true
	}
	log.Printf("screen: 未知后端 %q（支持 console|st7789|png|web）", backend)
	return nil, backend, false
}

func (p *panel) screenLoop(backend string) {
	dev, label, ok := p.buildScreenDev(backend)
	if !ok {
		return
	}
	madctl := p.cfg.screenCfg.madctl(p.cfg.screenMADCTL)
	log.Printf("screen: 后端 %s 启动（SPI=%s DC=%d RST=%d %dx%d rot=%d madctl=0x%02X）",
		label, p.cfg.screenSPI, p.cfg.screenDC, p.cfg.screenRST,
		screenW, screenH, p.cfg.screenCfg.Rotation, madctl)

	canvas := newCanvas(screenW, screenH)
	scratch := newCanvas(screenW, screenH)
	hist := newHistory()
	p.screenHist.Store(hist) // 发布给预览接口（History 自带锁）
	ring := -1.0             // 圆环扫掠当前值（-1=未初始化）
	powerOn := dev != nil    // 首帧也走一次 CRT 开机动画（物理屏 RAM 不可信）
	startedAt := time.Now()
	lastFull := startedAt // 周期性整幅重推，兜住 SPI 半帧/面板 RAM 漂移
	// LOCK ↔ ADMIN 页面切换（300ms）。
	prevUnlocked := false
	transStart := time.Time{}
	transPrevUnlocked := false
	var transPrev *Canvas
	screenInited := false
	ver := screenCfgVer.Load()
	for {
		// 面板尺寸/方向变了：关旧设备、按新几何重建画布与驱动（实时生效，不重启进程）。
		if v := screenCfgVer.Load(); v != ver {
			ver = v
			if c, okc := dev.(interface{ close() }); okc && dev != nil {
				c.close()
			}
			dev, label, ok = p.buildScreenDev(backend)
			if !ok {
				return
			}
			log.Printf("screen: 配置变更 → %s（%dx%d rot=%d madctl=0x%02X）", label,
				screenW, screenH, p.cfg.screenCfg.Rotation, p.cfg.screenCfg.madctl(p.cfg.screenMADCTL))
			canvas = newCanvas(screenW, screenH)
			scratch = newCanvas(screenW, screenH)
			hist = newHistory()
			p.screenHist.Store(hist)
			ring = -1.0
			powerOn = dev != nil
			startedAt = time.Now()
		}
		// 规则：管理面端口开放期间不允许熄屏。若熄屏发生在端口打开之前，
		// 端口一开放就强制亮屏（USER/Web 之后也无法再熄屏，见 setScreenOff）。
		if p.screenOff.Load() && p.portOpen() {
			p.setScreenOff(false)
		}
		// 熄屏（USER 键/Web）或上电门被撤销 → CRT 关机动画 + 硬件睡眠 + 关背光，
		// 阻塞到恢复；本线程是唯一碰屏的 goroutine，不做跨线程硬件竞争。
		if !p.screenWanted() {
			p.screenPause(dev, canvas)
			powerOn = dev != nil
		}
		// 250ms 节拍：圆环/脉冲/进度是时间的函数，不依赖数据变化
		tick := time.Now()
		snap := p.screenState()
		lang := screenctl.lang(snap) // 覆盖 > 快照 > zh
		hist.push(snap)

		anim := animState{
			slide: 1,
			ring:  ring,
			pulse: math.Mod(tick.Sub(startedAt).Seconds(), 1.0), // 1Hz 锁定脉冲
			now:   tick.Sub(startedAt).Seconds(),
		}
		// 圆环向目标扫掠（300ms 收敛）
		if hit, ok := snapF(snapMap(snap, "traffic"), "cache_hit_pct"); ok {
			target := clamp01(hit / 100)
			if ring < 0 {
				ring = target
			} else if math.Abs(ring-target) > 0.004 {
				step := 0.06
				if ring < target {
					ring = math.Min(target, ring+step)
				} else {
					ring = math.Max(target, ring-step)
				}
			} else {
				ring = target
			}
			anim.ring = ring
		}
		if p.screen != nil {
			p.screen.setPage("main", lang)
		}

		inTrans := false
		if dev != nil {
			renderScreenPage(snap, hist, scratch, lang, anim)
			if active, _ := chal.peek()["active"].(bool); active {
				uiChallenge(scratch, chal.peek())
			}
			if tst := p.screenToast.Load(); tst != nil {
				if time.Now().Before(tst.until) {
					drawToast(scratch, newTxt(lang).get(tst.key))
				} else {
					p.screenToast.CompareAndSwap(tst, nil)
				}
			}
			// 旧页滑出、新页滑入。
			unlocked := snapS(snap, "switch") == "unlocked"
			if !screenInited {
				prevUnlocked, screenInited = unlocked, true
			} else if unlocked != prevUnlocked && !powerOn {
				transPrevUnlocked = prevUnlocked
				prevUnlocked = unlocked
				transStart = tick
				if transPrev == nil {
					transPrev = newCanvas(screenW, screenH)
				}
				prevSnap := cloneSnap(snap)
				if transPrevUnlocked {
					prevSnap["switch"] = "unlocked"
				} else {
					prevSnap["switch"] = "locked"
				}
				renderScreenPage(prevSnap, hist, transPrev, lang, anim)
			}
			elapsed := tick.Sub(transStart)
			inTrans = !transStart.IsZero() && elapsed < pageTransDur
			switch {
			case powerOn:
				crtPlayOn(dev, canvas, scratch) // 从中心光点展开到这一帧
				powerOn = false
			case inTrans:
				compositeSharedAxis(canvas, transPrev, scratch, elapsed.Seconds()/pageTransDur.Seconds())
				canvas.invalidate()
				canvas.flush(dev)
			default:
				transStart = time.Time{}
				copy(canvas.Buf, scratch.Buf)
				// 每 60s 整幅重推一次：即使某次 SPI 写入丢失/短写，
				// 残留也不会永久留在屏上（差分 prev 已无法发现它）。
				if tick.Sub(lastFull) >= time.Minute {
					canvas.invalidate()
					lastFull = tick
				}
				canvas.flush(dev)
			}
		} else {
			fmt.Print(frameConsole("main", snap, newTxt(lang), time.Now().Format("15:04:05")))
		}

		d := screenTick - time.Since(tick)
		if d < 5*time.Millisecond {
			d = 5 * time.Millisecond
		}
		// 过渡期间不按常规节拍等待：尽可能连续推帧，减小滑动/淡入的台阶感。
		if dev != nil && inTrans {
			d = 2 * time.Millisecond
		}
		time.Sleep(d)
	}
}

// blitSlide：把 src 的**中间内容带**按 dx=slide（0..1）滑入 dst（右侧进场，左侧露底）。
// **页头/页脚不跟着滑**：设备状态、时钟、网络图标、轮播进度线、页码点都是常驻元素，
// 翻页时保持不动——只有 body（页头下沿与页脚上沿之间）整体位移。
func blitSlide(dst, src *Canvas, slide float64) {
	dst.clear(screenBG)
	dx := int(float64(screenW) * (1 - slide))
	if dx <= 0 {
		copy(dst.Buf, src.Buf)
		return
	}
	stride := screenW * 2
	top, bot := screenHeadH, screenH-screenFootH
	// 头/尾原样贴 src（当拍就是新页的常驻元素：进度线已归零、页码点已切到新页）
	copy(dst.Buf[:top*stride], src.Buf[:top*stride])
	copy(dst.Buf[bot*stride:], src.Buf[bot*stride:])
	if dx >= screenW {
		return
	}
	for y := top; y < bot; y++ {
		copy(dst.Buf[y*stride+dx*2:], src.Buf[y*stride:y*stride+(screenW-dx)*2])
	}
}

// uiChallenge：升级态下在屏上盖一层"当次挑战码"横幅。
// 字体走既有 SM/NUM 与 CJKFont（screen_font.go），不引入新依赖。
func uiChallenge(c *Canvas, info map[string]any) {
	code, _ := info["code"].(string)
	if code == "" {
		return
	}
	c.fillRect(0, screenH-52, screenW, 52, screenBG)
	drawMixed(c, 8, screenH-46, "登录挑战码（一次性）", screenWarn, SM, CJKFont)
	NUM.draw(c, 8, screenH-26, code, screenWarn)
}

func (p *panel) openScreenHW() (*st7789, error) {
	spi, err := openSPI(p.cfg.screenSPI, p.cfg.screenHz)
	if err != nil {
		return nil, err
	}
	dc, err := openGPIO(p.cfg.screenDC)
	if err != nil {
		spi.close()
		return nil, err
	}
	var rstpin pinSetter // 接口 nil 判定：RST=-1 时保持真 nil（驱动里跳过硬件复位）
	if p.cfg.screenRST >= 0 {
		rst, err := openGPIO(p.cfg.screenRST)
		if err != nil {
			dc.close()
			spi.close()
			return nil, err
		}
		rstpin = rst
	}
	madctl := p.cfg.screenCfg.madctl(p.cfg.screenMADCTL)
	xoff, yoff := p.cfg.screenCfg.offsets(p.cfg.screenXOff, p.cfg.screenYOff)
	st := newST7789(spi, dc, rstpin, int(madctl), xoff, yoff, p.cfg.screenInvert)
	st.init()
	return st, nil
}

// ---------------------------------------------------------------- 屏幕上电门

// gateOpen：门文件存在 = 允许点亮屏幕。空路径 = 不设门（始终放行）。
func gateOpen(path string) bool {
	if path == "" {
		return true
	}
	_, err := os.Stat(path)
	return err == nil
}

// screenGateHold：进 init 前先顶住。未通过期间：
//   - 打开一次硬件并 SLPIN+DISPOFF（**背光硬接 3V3 也保证屏面全黑**），随后 500ms 轮询
//   - 门文件出现才返回，由调用方 init/自检/首帧（init 幂等，重复无妨）
func (p *panel) screenGateHold() {
	gate := p.cfg.screenGate
	if gateOpen(gate) {
		return
	}
	log.Printf("screen: 上电门未开（%s 不存在）——屏幕熄灭（SPI 睡眠）。写入门文件后执行: panel-go screen-gate pass", gate)
	for {
		st, hwErr := p.openScreenHW()
		if hwErr == nil {
			if serr := st.sleep(); serr != nil {
				log.Printf("screen: 进睡眠失败（%v）——仍保持熄屏等待", serr)
			}
			st.close()
			break
		}
		log.Printf("screen: 门控期间硬件暂不可用（%v）——500ms 后重试", hwErr)
		time.Sleep(500 * time.Millisecond)
		if gateOpen(gate) {
			break
		}
	}
	for !gateOpen(gate) {
		time.Sleep(500 * time.Millisecond)
	}
	log.Printf("screen: 上电门已开——初始化屏幕")
}

// screenWanted：屏幕应该亮着 = 没有被 USER/Web 熄屏，且上电门已放行。
func (p *panel) screenWanted() bool {
	if p.screenOff.Load() {
		return false
	}
	return gateOpen(p.cfg.screenGate)
}

// screenPause：熄屏（USER 键/Web 或上电门撤销）→ CRT 关机动画（纵向压扁成线、
// 收成光点、余辉熄灭）→ 显示睡眠 + 关背光，阻塞等恢复后再唤醒。开屏动画由主循环
// 在第一帧渲染完成后播放（crtPlayOn），这样展开的是最新画面而不是旧帧。
func (p *panel) screenPause(dev screenDev, c *Canvas) {
	reason := "上电门已撤销"
	if p.screenOff.Load() {
		reason = "USER/Web 熄屏"
	}
	log.Printf("screen: %s——熄屏", reason)
	crtPlayOff(dev, c) // dev==nil（console 后端）时直接返回
	if st, ok := dev.(*st7789); ok {
		_ = st.sleep()
	}
	if p.bl != nil {
		p.bl.set(false)
	}
	// 等恢复期间同样守着"端口开放不允许熄屏"：熄屏状态每 ~500ms 查一次端口，
	// 端口一开放立即强制亮屏（上电门未放行时不查，避免无意义的状态轮询）。
	n := 0
	for !p.screenWanted() {
		if p.screenOff.Load() && n%10 == 0 && p.portOpen() {
			p.setScreenOff(false)
			continue
		}
		n++
		time.Sleep(50 * time.Millisecond)
	}
	if st, ok := dev.(*st7789); ok {
		_ = st.wake()
	}
	if c != nil {
		c.invalidate() // 休眠期间面板 RAM 未必还等于 prev；醒来先整幅重推
	}
	if p.bl != nil {
		p.bl.set(true)
	}
	log.Printf("screen: 恢复渲染（亮屏）")
}

// blOpen：上电门放行时点亮背光（SCREEN_BL≥0 才有效；-1=不控）。
func (p *panel) blOpen() {
	if p.cfg.screenBL < 0 || p.bl != nil {
		return
	}
	if b, err := openGPIO(p.cfg.screenBL); err == nil {
		p.bl = b
		p.bl.set(true)
	} else {
		log.Printf("screen: 背光 GPIO %d 打不开(%v)——不控背光", p.cfg.screenBL, err)
	}
}
