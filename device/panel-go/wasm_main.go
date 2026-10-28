//go:build js && wasm

package main

// wasm_main.go —— 把硬件屏同一套 Go 渲染器编成 WebAssembly。
//
// 构建（TinyGo 0.35 + Go 1.19–1.23）：
//   tinygo build -target=wasm -o ../../web/assets/screen/nkscreen.wasm .
//   cp "$(tinygo env TINYGOROOT)/targets/wasm_exec.js" ../../web/assets/screen/
//
// JS 侧只做三件事：准备 snapshot JSON、传入 RGBA 目标缓冲、把结果贴到 canvas。
// 页面内容、字体、配额条、时间追赶标记、CRT/翻页/圆环等全部仍由原生代码生成。

import (
	"encoding/json"
	"syscall/js"
)

var (
	wasmCanvas = newCanvas(screenW, screenH)
	wasmHist   = newHistory()
	wasmRGBA   = make([]byte, screenW*screenH*4)
)

func main() {
	layout.doc = layoutDoc{Modules: screenDefaultModules}
	js.Global().Set("NKScreenWasm", js.ValueOf(map[string]any{
		"render": js.FuncOf(wasmRender),
		"width":  screenW,
		"height": screenH,
	}))
	select {}
}

// wasmRender(stateJSON, targetUint8Array, pulse?, ring?, slide?)
func wasmRender(this js.Value, args []js.Value) any {
	if len(args) < 2 {
		return nil
	}
	var snap map[string]any
	if err := json.Unmarshal([]byte(args[0].String()), &snap); err != nil {
		return nil
	}
	lang := "zh"
	if v, ok := snap["lang"].(string); ok && (v == "zh" || v == "en") {
		lang = v
	}
	anim := animState{slide: 1, ring: 0, pulse: 0}
	if len(args) > 2 {
		anim.pulse = args[2].Float()
	}
	if len(args) > 3 {
		anim.ring = args[3].Float()
	}
	if len(args) > 4 {
		anim.slide = args[4].Float()
	}
	if len(args) > 5 {
		anim.now = args[5].Float()
	}
	if anim.slide <= 0 {
		anim.slide = 1
	}
	wasmHist.push(snap)
	renderScreenPage(snap, wasmHist, wasmCanvas, lang, anim)
	for i := range screenW * screenH {
		j := i * 2
		r, g, b := rgb565expand(uint16(wasmCanvas.Buf[j])<<8 | uint16(wasmCanvas.Buf[j+1]))
		k := i * 4
		wasmRGBA[k], wasmRGBA[k+1], wasmRGBA[k+2], wasmRGBA[k+3] = r, g, b, 255
	}
	target := args[1]
	if target.Length() >= len(wasmRGBA) {
		js.CopyBytesToJS(target, wasmRGBA)
	}
	return nil
}
