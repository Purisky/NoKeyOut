'use strict';
/* NoKeyOut 装机演练 · 8-bit 合成音效（零素材、Web Audio）
 * 全部使用方波/脉冲波 + LFSR 噪声 + 8-bit 量化，不加载任何音频素材。 */
(function () {
  const KEY = 'nokeyout-sfx';
  let enabled = true;
  try { enabled = localStorage.getItem(KEY) !== '0'; } catch (e) { /* private */ }
  let ctx = null, master = null, limiter = null, crusher = null;
  const VOL = 4.0;
  const MAX_GAIN = 0.35;

  /* 8-bit 量化：把输出压到 256 个电平，保留芯片音色的颗粒感。 */
  function makeCrusher(ac) {
    const n = 256, curve = new Float32Array(n);
    for (let i = 0; i < n; i++) {
      const x = (i / (n - 1)) * 2 - 1;
      curve[i] = Math.round(x * 127) / 127;
    }
    const ws = ac.createWaveShaper();
    ws.curve = curve;
    ws.oversample = 'none';
    return ws;
  }

  /* NES 风格脉冲波：duty=0.125/0.25/0.5。 */
  function pulseWave(ac, duty) {
    const n = 32, real = new Float32Array(n), imag = new Float32Array(n);
    for (let k = 1; k < n; k++) imag[k] = (2 / (k * Math.PI)) * Math.sin(Math.PI * k * duty);
    return ac.createPeriodicWave(real, imag, { disableNormalization: false });
  }

  function ensure() {
    if (!ctx) {
      const AC = window.AudioContext || window.webkitAudioContext;
      if (!AC) return null;
      ctx = new AC();
      master = ctx.createGain();
      master.gain.value = 1.0;
      crusher = makeCrusher(ctx);
      limiter = ctx.createDynamicsCompressor();
      limiter.threshold.value = -8;
      limiter.knee.value = 6;
      limiter.ratio.value = 12;
      limiter.attack.value = 0.003;
      limiter.release.value = 0.25;
      master.connect(crusher);
      crusher.connect(limiter);
      limiter.connect(ctx.destination);
    }
    if (ctx.state === 'suspended') ctx.resume().catch(() => {});
    return ctx;
  }

  /* 8-bit 单音：默认 25% 脉冲波；不产生平滑的模拟正弦。 */
  function tone(freq, dur, type, vol, slideTo, delay) {
    const ac = ensure(); if (!ac) return;
    const t0 = ac.currentTime + (delay || 0);
    const o = ac.createOscillator(), g = ac.createGain();
    if (type === 'triangle') o.type = 'triangle';
    else if (type === 'square') o.type = 'square';
    else o.setPeriodicWave(pulseWave(ac, type === 'pulse50' ? 0.5 : 0.25));
    o.frequency.setValueAtTime(freq, t0);
    if (slideTo) o.frequency.exponentialRampToValueAtTime(Math.max(20, slideTo), t0 + dur);
    const v = Math.min(MAX_GAIN, (vol || 0.04) * VOL);
    g.gain.setValueAtTime(0.0001, t0);
    g.gain.exponentialRampToValueAtTime(v, t0 + 0.006);
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + dur);
    o.connect(g); g.connect(master);
    o.start(t0); o.stop(t0 + dur + 0.02);
  }

  /* 8-bit LFSR 噪声：8kHz 采样，产生芯片式“沙沙”而非白噪声。 */
  function noise(dur, vol, hp, delay) {
    const ac = ensure(); if (!ac) return;
    const t0 = ac.currentTime + (delay || 0);
    const rate = 8000, len = Math.max(1, Math.floor(rate * dur));
    const buf = ac.createBuffer(1, len, rate);
    const d = buf.getChannelData(0);
    let reg = 0xACE1;
    for (let i = 0; i < len; i++) {
      const bit = ((reg >> 0) ^ (reg >> 2) ^ (reg >> 3) ^ (reg >> 5)) & 1;
      reg = (reg >> 1) | (bit << 15);
      const fade = 1 - i / len;
      d[i] = ((reg & 1) ? 1 : -1) * fade;
    }
    const src = ac.createBufferSource(); src.buffer = buf;
    const f = ac.createBiquadFilter(); f.type = 'highpass'; f.frequency.value = hp || 900;
    const g = ac.createGain(); g.gain.value = Math.min(MAX_GAIN, (vol || 0.035) * VOL);
    src.connect(f); f.connect(g); g.connect(master);
    src.start(t0);
  }

  const S = {
    click() { tone(1046, 0.03, 'pulse', 0.022); },
    select() { tone(784, 0.05, 'pulse', 0.03, 1175); },
    terminal() { tone(1760, 0.02, 'square', 0.016); },
    wire() { tone(440, 0.1, 'pulse', 0.035, 880); },
    solder() { noise(0.12, 0.04, 1800); tone(2093, 0.035, 'square', 0.012); },
    plug() { tone(110, 0.09, 'pulse50', 0.06, 55); noise(0.04, 0.018, 600); },
    flip() { tone(330, 0.24, 'pulse', 0.03, 990); noise(0.2, 0.015, 500); },
    boot() {
      [523, 659, 784, 1046].forEach((f, i) => tone(f, 0.14, 'pulse', 0.04, null, i * 0.1));
    },
    switch() { tone(1175, 0.025, 'square', 0.02); tone(784, 0.025, 'square', 0.018, null, 0.05); },
    error() { tone(220, 0.18, 'square', 0.035, 110); },
    step() { tone(988, 0.07, 'pulse', 0.035); tone(1319, 0.12, 'pulse', 0.035, null, 0.06); },
    celebrate() {
      [523, 659, 784, 1046, 1318, 1568].forEach((f, i) => tone(f, 0.24, 'pulse', 0.045, null, i * 0.09));
      noise(0.35, 0.018, 2600, 0.04);
      noise(0.25, 0.014, 3400, 0.32);
    },
  };

  function play(name) {
    if (!enabled) return;
    const fn = S[name];
    if (fn) { try { fn(); } catch (e) { /* audio unavailable */ } }
  }
  function setEnabled(v) {
    enabled = !!v;
    try { localStorage.setItem(KEY, enabled ? '1' : '0'); } catch (e) { /* private */ }
    return enabled;
  }
  function toggle() { return setEnabled(!enabled); }
  function unlock() { ensure(); }

  window.addEventListener('pointerdown', unlock, { passive: true });
  window.addEventListener('keydown', unlock);
  window.NKSFX = { play, toggle, setEnabled, enabled: () => enabled, unlock };
})();
