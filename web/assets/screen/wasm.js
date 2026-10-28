/* 240×240 原生 Go 屏幕渲染器的浏览器封装。
 * 页面只传 snapshot + 动画相位，像素全部由 nkscreen.wasm 里的
 * renderScreenPage / screen_*.go 生成。 */
(function () {
  const script = document.currentScript;
  const base = script && script.src ? script.src.replace(/\/[^/]*(\?.*)?$/, '/') : 'assets/screen/';
  let ready = null;
  let wasm = null;

  async function load() {
    if (wasm) return wasm;
    if (ready) return ready;
    ready = (async () => {
      if (typeof Go === 'undefined') throw new Error('wasm_exec.js not loaded');
      const go = new Go();
      let instance;
      try {
        const r = await fetch(base + 'nkscreen.wasm?v=19');
        instance = await WebAssembly.instantiateStreaming(r, go.importObject);
      } catch (e) {
        const buf = await (await fetch(base + 'nkscreen.wasm?v=19')).arrayBuffer();
        instance = await WebAssembly.instantiate(buf, go.importObject);
      }
      go.run(instance.instance); // 永不 resolve，不要 await
      for (let i = 0; i < 400 && !window.NKScreenWasm; i++) {
        await new Promise((r) => setTimeout(r, 10));
      }
      if (!window.NKScreenWasm) throw new Error('NKScreenWasm not ready');
      wasm = window.NKScreenWasm;
      return wasm;
    })();
    return ready;
  }

  const imgs = new WeakMap();

  async function render(canvas, state, anim) {
    const m = await load();
    const ctx = canvas.getContext('2d', { alpha: false });
    let img = imgs.get(canvas);
    if (!img) {
      img = ctx.createImageData(m.width, m.height);
      imgs.set(canvas, img);
    }
    const a = anim || {};
    const pulse = a.pulse == null ? 0 : Number(a.pulse);
    const ring = a.ring == null ? 0.62 : Number(a.ring);
    const slide = a.slide == null ? 1 : Number(a.slide);
    // now：动画时钟（秒）。M3 不确定圆形指示器的 5s 周期以它为相位基准。
    const now = a.now == null ? performance.now() / 1000 : Number(a.now);
    m.render(JSON.stringify(state), img.data, pulse, ring, slide, now);
    ctx.putImageData(img, 0, 0);
  }

  window.NKScreen = { load, render, width: 240, height: 240 };
})();
