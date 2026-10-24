/* CRT 开关机效果：与 site/view3d.js 的 scrFrameOn / scrFrameOff 同一套算法与时长。
 * 用法：NKCRT.play(canvas, on, done) —— 画布内容即源帧；on=false 播完留黑，on=true 播完还原源帧。 */
(function () {
  const W = 240, H = 240;
  const OFF_MS = 320, ON_MS = 420;
  const MIN_ROWS = 2;

  const exp5 = new Uint8Array(32), exp6 = new Uint8Array(64);
  for (let i = 0; i < 32; i++) exp5[i] = Math.floor((i * 255) / 31);
  for (let i = 0; i < 64; i++) exp6[i] = Math.floor((i * 255) / 63);

  let raf = 0;

  function clear(dst) {
    dst.fill(0);
    for (let i = 3; i < dst.length; i += 4) dst[i] = 255;
  }

  function set(dst, x, y, rgb) {
    if (x < 0 || y < 0 || x >= W || y >= H) return;
    const i = (y * W + x) * 4;
    dst[i] = rgb[0];
    dst[i + 1] = rgb[1];
    dst[i + 2] = rgb[2];
    dst[i + 3] = 255;
  }

  function beam(i) {
    i = Math.max(0, Math.min(1, i));
    const v = 255 * i;
    return [exp5[((v * 0.86) | 0) >> 3], exp6[((v * 0.94) | 0) >> 2], exp5[(v | 0) >> 3]];
  }

  function hline(dst, cx, cy, w, rgb) {
    if (w < 1) return;
    const x0 = Math.max(0, cx - (w >> 1));
    const x1 = Math.min(W - 1, x0 + w - 1);
    for (let x = x0; x <= x1; x++) set(dst, x, cy, rgb);
  }

  function beamLine(dst, cx, cy, w, core, halo) {
    if (w < 1 || core <= 0.01) return;
    if (halo > 0.02) {
      for (let d = 2; d >= 1; d--) {
        const rgb = beam(halo * (1 - (d - 1) * 0.45) * 0.5);
        hline(dst, cx, cy - d, w, rgb);
        hline(dst, cx, cy + d, w, rgb);
      }
    }
    hline(dst, cx, cy, w, beam(core));
  }

  function dot(dst, cx, cy, r, i) {
    if (i <= 0.01) return;
    if (r < 1) r = 1;
    const core = beam(i), halo = beam(i * 0.2);
    const R = r + 2;
    for (let dy = -R; dy <= R; dy++) {
      for (let dx = -R; dx <= R; dx++) {
        const d2 = dx * dx + dy * dy;
        if (d2 > R * R) continue;
        set(dst, cx + dx, cy + dy, d2 <= r * r ? core : halo);
      }
    }
  }

  function tintRow(dst, dOff, src, sOff, n, mul, white) {
    for (let x = 0; x < n; x++) {
      const si = sOff + x * 4;
      const v = ((src[si] >> 3) << 11) | ((src[si + 1] >> 2) << 5) | (src[si + 2] >> 3);
      let r = exp5[(v >> 11) & 31] * mul;
      let g = exp6[(v >> 5) & 63] * mul;
      let b = exp5[v & 31] * mul;
      if (r > 255) r = 255;
      if (g > 255) g = 255;
      if (b > 255) b = 255;
      r += (255 - r) * white;
      g += (255 - g) * white;
      b += (255 - b) * white;
      const di = dOff + x * 4;
      dst[di] = exp5[(r | 0) >> 3];
      dst[di + 1] = exp6[(g | 0) >> 2];
      dst[di + 2] = exp5[(b | 0) >> 3];
      dst[di + 3] = 255;
    }
  }

  function easeOut(x) {
    return 1 - Math.pow(1 - x, 3);
  }

  function frameOff(dst, src, t) {
    clear(dst);
    if (t <= 0) { dst.set(src); return; }
    if (t >= 1) return;
    const cx = W >> 1, cy = H >> 1;
    if (t < 0.62) {
      const u = t / 0.62;
      let h = H * Math.pow(1 - u, 1.7);
      if (h < MIN_ROWS) h = MIN_ROWS;
      const rows = Math.max(MIN_ROWS, Math.round(h));
      const top = cy - (rows >> 1);
      const mul = 1 + 2.4 * u, white = 0.55 * u;
      for (let y = top; y < top + rows; y++) {
        if (y < 0 || y >= H) continue;
        const sy = Math.max(0, Math.min(H - 1, Math.floor(((y - top + 0.5) * H) / rows)));
        tintRow(dst, y * W * 4, src, sy * W * 4, W, mul, white);
      }
      if (u > 0.3) {
        const k = (u - 0.3) / 0.7;
        beamLine(dst, cx, cy, W - 4, 0.3 + 0.7 * k, 0.45 * k);
      }
    } else if (t < 0.88) {
      const u = (t - 0.62) / 0.26;
      const w = Math.max(MIN_ROWS, Math.floor(W * Math.pow(1 - u, 1.6)));
      beamLine(dst, cx, cy, w, 1, 0.75);
    } else {
      const u = (t - 0.88) / 0.12;
      dot(dst, cx, cy, 1 + Math.round(3 * (1 - u)), Math.max(0, 1 - u));
    }
  }

  function frameOn(dst, src, t) {
    clear(dst);
    if (t <= 0) return;
    if (t >= 1) { dst.set(src); return; }
    const cx = W >> 1, cy = H >> 1;
    if (t < 0.14) {
      const u = t / 0.14;
      dot(dst, cx, cy, 1 + Math.round(4 * u), 0.35 + 0.65 * u);
    } else if (t < 0.44) {
      const u = (t - 0.14) / 0.3;
      beamLine(dst, cx, cy, 3 + Math.round((W - 3) * easeOut(u)), 1, 0.85);
    } else {
      const u = (t - 0.44) / 0.56;
      const rows = Math.min(H, Math.max(MIN_ROWS, Math.round(MIN_ROWS + (H - MIN_ROWS) * easeOut(u))));
      const top = cy - (rows >> 1);
      const mul = 2.6 - 1.6 * u, white = 0.85 * (1 - u);
      for (let y = top; y < top + rows; y++) {
        if (y < 0 || y >= H) continue;
        const sy = Math.max(0, Math.min(H - 1, Math.floor(((y - top + 0.5) * H) / rows)));
        tintRow(dst, y * W * 4, src, sy * W * 4, W, mul, white);
      }
      const k = 1 - u;
      if (k > 0.02) beamLine(dst, cx, cy, W - 4, 0.6 * k * k, 0.5 * k * k);
    }
  }

  function play(canvas, on, done) {
    if (!canvas) return false;
    const ctx = canvas.getContext('2d');
    if (raf) cancelAnimationFrame(raf);
    const base = new Uint8ClampedArray(ctx.getImageData(0, 0, W, H).data);
    const frame = ctx.createImageData(W, H);
    const start = performance.now();
    const dur = on ? ON_MS : OFF_MS;
    const tick = (now) => {
      const t = Math.min(1, Math.max(0, (now - start) / dur));
      if (on) frameOn(frame.data, base, t);
      else frameOff(frame.data, base, t);
      ctx.putImageData(frame, 0, 0);
      if (t < 1) {
        raf = requestAnimationFrame(tick);
        return;
      }
      raf = 0;
      if (on) {
        ctx.putImageData(new ImageData(base, W, H), 0, 0);
      } else {
        ctx.fillStyle = '#000';
        ctx.fillRect(0, 0, W, H);
      }
      if (done) done(on);
    };
    raf = requestAnimationFrame(tick);
    return true;
  }

  window.NKCRT = { play };
})();
