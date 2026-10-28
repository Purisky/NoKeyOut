//go:build !(js && wasm)

package main

// screen_layout_http.go —— 布局编辑器的 HTTP 接口 + PNG 预览；
// 与纯渲染/布局存储分开，WASM 构建不链入 net/http 和 image/png。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"net/http"
	"time"
)

// screenModules：模块布局的 HTTP 接口（需会话；LOCKED 时监听已被门控关掉，天然不可达）。
//
//	GET  → {"modules":[…],"max":3,"budget":240,"available":[{id,label,height}…]}
//	POST {"modules":[…]} → 校验并原子落盘；渲染线程按 mtime 热加载
func (p *panel) screenModules(w http.ResponseWriter, r *http.Request) {
	if p.user(r) == "" {
		p.errJSON(w, r, 401, "no session")
		return
	}
	switch r.Method {
	case http.MethodGet:
		doc := layout.current()
		if doc.Modules == nil {
			doc.Modules = []string{}
		}
		p.json(w, r, 200, map[string]any{
			"modules": doc.Modules, "budget": screenH,
			"available": moduleInfoList(),
			"quotas":    doc.Quotas, "usage": doc.Usage, "compact": doc.Compact,
			"usage_periods": usagePeriods,
			// 可选配额行（供应商/周期/形态），来自实时快照：编辑器据此生成下拉，
			// 前端不写死任何供应商或窗口名。
			"quota_choices": quotaChoices(p.stats.snapshot()),
			"module_gap":    screenModuleGap,
		}, nil)
	case http.MethodPost:
		var body layoutDoc
		if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&body); err != nil {
			p.errJSON(w, r, 400, "请求体不是合法 JSON")
			return
		}
		if err := layout.save(body); err != nil {
			p.errJSON(w, r, 400, err.Error())
			return
		}
		log.Printf("screen: 布局已更新 → %v（配额自定义 %d 条，用量 %q）", body.Modules, len(body.Quotas), body.Usage)
		out := body.Modules
		if out == nil {
			out = []string{}
		}
		p.json(w, r, 200, map[string]any{"ok": true, "modules": out, "quotas": body.Quotas,
			"usage": body.Usage, "compact": body.Compact}, nil)
	default:
		p.errJSON(w, r, 405, "只支持 GET / POST")
	}
}

// quotaChoices：屏上可选的配额条目（编辑器下拉用）。
// window 为空 = 固定余额行；label 给人看（"OpenCode Go · 7 days"）。
func quotaChoices(snap map[string]any) []map[string]any {
	out := []map[string]any{}
	for _, m := range singleRows(snap) {
		kind := snapS(m, "mode")
		if kind == "" {
			kind = "period"
		}
		label := snapS(m, "name")
		if acc := snapS(m, "account"); acc != "" {
			label += " · " + acc // 多账号时这一节是唯一的区分点
		}
		if w := snapS(m, "window"); w != "" {
			label += " · " + w
		} else if kind == "balance" {
			if u := snapS(m, "unit"); u != "" {
				label += " · " + u // 余额行：用币种当后缀（原文金额会变，不进标签）
			} else {
				label += " · balance"
			}
		} else if snapS(m, "err") != "" {
			label += " · error" // 取不到窗口的供应商也留在下拉里，标出原因
		}
		out = append(out, map[string]any{
			"provider": snapS(m, "provider"), "window": snapS(m, "window"),
			"account": snapS(m, "account"), "name": snapS(m, "name"),
			"mode": kind, "label": label, "error": snapS(m, "err") != "",
		})
	}
	return out
}

// screenPreview：把**候选布局**用真实渲染器 + 实时数据画出来给编辑器看（不落盘、不改配置）。
// best-effort：配置不完整/非法也返回图（编辑过程中本来就常常是中间状态）。
func (p *panel) screenPreview(w http.ResponseWriter, r *http.Request) {
	if p.user(r) == "" {
		p.errJSON(w, r, 401, "no session")
		return
	}
	if r.Method != http.MethodPost {
		p.errJSON(w, r, 405, "只支持 POST")
		return
	}
	var body struct {
		layoutDoc
		Scale int `json:"scale"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&body); err != nil {
		p.errJSON(w, r, 400, "请求体不是合法 JSON")
		return
	}
	scale := body.Scale
	if scale < 1 || scale > 4 {
		scale = 2
	}
	c := newCanvas(screenW, screenH)
	hist := p.screenHist.Load()
	if hist == nil {
		hist = newHistory() // 渲染线程未起（console/png 后端或尚未启动）时给个空的
	}
	// 和真机 / 首页 WASM 同一个入口与同一份有效快照：锁定、断网、统计异常、模块栈全部一致。
	snap := p.screenState()
	snap["switch"] = "locked" // 预览的是版面配置；ADMIN 页与版面无关
	uiRenderWith("main", snap, hist, c, 0, screenctl.lang(snap),
		time.Now().Format("15:04"), animState{}, body.layoutDoc)
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(canvasPNG(c, scale))
}

// canvasPNG：画布 → PNG，整数倍最近邻放大（预览要看清像素，不能平滑）。
func canvasPNG(c *Canvas, scale int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, screenW*scale, screenH*scale))
	for y := 0; y < screenH*scale; y++ {
		for x := 0; x < screenW*scale; x++ {
			i := ((y/scale)*screenW + x/scale) * 2
			rr, gg, bb := rgb565expand(uint16(c.Buf[i])<<8 | uint16(c.Buf[i+1]))
			img.SetRGBA(x, y, color.RGBA{rr, gg, bb, 0xFF})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// screenCfgHandler：物理屏尺寸与方向（GET 读 / POST 写）。
//
//	GET  → {panelW,panelH,rotation,logicalW,logicalH,backend,madctl,presets}
//	POST {"panelW":240,"panelH":320,"rotation":0} → 校验、落盘、实时重建渲染线程
func (p *panel) screenCfgHandler(w http.ResponseWriter, r *http.Request) {
	if p.user(r) == "" {
		p.errJSON(w, r, 401, "no session")
		return
	}
	view := func(c screenCfg) map[string]any {
		lw, lh := c.logical()
		return map[string]any{
			"panelW": c.PanelW, "panelH": c.PanelH, "rotation": c.Rotation,
			"logicalW": lw, "logicalH": lh,
			"backend": p.cfg.screenBackend,
			"madctl":  fmt.Sprintf("0x%02X", c.madctl(p.cfg.screenMADCTL)),
			"presets": []map[string]any{
				{"w": 240, "h": 240, "label": "240×240"},
				{"w": 240, "h": 320, "label": "240×320"},
			},
		}
	}
	switch r.Method {
	case http.MethodGet:
		p.json(w, r, 200, view(p.cfg.screenCfg), nil)
	case http.MethodPost:
		var body struct {
			PanelW   int `json:"panelW"`
			PanelH   int `json:"panelH"`
			Rotation int `json:"rotation"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
			p.errJSON(w, r, 400, "请求体不是合法 JSON")
			return
		}
		c := screenCfg{PanelW: body.PanelW, PanelH: body.PanelH, Rotation: body.Rotation}
		if err := c.validate(); err != nil {
			p.errJSON(w, r, 400, err.Error())
			return
		}
		if err := saveScreenCfg(c); err != nil {
			p.errJSON(w, r, 500, "写入配置失败："+err.Error())
			return
		}
		p.cfg.screenCfg = c
		setScreenGeometry(c)
		screenCfgVer.Add(1) // 渲染线程下一拍重建画布与驱动
		lw, lh := c.logical()
		log.Printf("screen: 面板 %d×%d rot=%d → 逻辑 %d×%d（madctl=0x%02X）",
			c.PanelW, c.PanelH, c.Rotation, lw, lh, c.madctl(p.cfg.screenMADCTL))
		p.json(w, r, 200, view(c), nil)
	default:
		p.errJSON(w, r, 405, "只支持 GET / POST")
	}
}
