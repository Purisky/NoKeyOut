/* 主页 240×240 屏：开关 ON（断路）= LOCK（正常，显示所选供应商配额）；OFF（闭合）= ADMIN（渲染地址页）。 */
(function () {
  const canvas = document.getElementById('hero-screen');
  if (!canvas || !window.NK_SCREEN_STATES) return;
  const off = document.getElementById('screen-off');
  const hint = document.querySelector('.screen-hint');
  const sw = document.getElementById('pswitch');
  let zh = document.documentElement.lang.toLowerCase().startsWith('zh');
  const base = window.NK_SCREEN_STATES.main;
  const pools = Array.isArray(base.pools) ? base.pools : [];
  const label = { off: zh ? '熄屏' : 'screen off', on: zh ? '亮屏' : 'screen on' };
  function setLang(next) {
    zh = next === 'zh';
    label.off = zh ? '熄屏' : 'screen off';
    label.on = zh ? '亮屏' : 'screen on';
    sync();
    if (powered && ready) paint();
  }
  const selected = new Set(pools.length ? [pools[0].provider] : []);
  let powered = true;
  let animating = false;
  let ready = false;
  let raf = 0;
  let last = 0;
  let loading = false;

  /* 开关 ON（断路）= LOCK = 正常运行 */
  const isLocked = () => !sw || !!sw.checked;
  /* 端口开放（ADMIN）计时起点：屏上显示，15 分钟阈值内进度条填充，超时转红。 */
  let adminAt = isLocked() ? 0 : Date.now();

  function snapshot() {
    const s = JSON.parse(JSON.stringify(base));
    s.lang = zh ? 'zh' : 'en';
    s.switch = isLocked() ? 'locked' : 'unlocked';
    if (!isLocked()) {
      if (!adminAt) adminAt = Date.now();
      s.admin_s = Math.max(0, (Date.now() - adminAt) / 1000);
    }
    const one = pools.filter((p) => selected.has(p.provider));
    if (one.length) s.pools = one;
    return s;
  }

  function sync() {
    canvas.hidden = !ready;
    if (off) {
      off.textContent = powered ? label.off : label.on;
      /* 端口开放时不允许熄屏：按钮禁用，拨回 LOCK 后恢复。 */
      off.disabled = !isLocked();
      off.title = isLocked() ? '' : (zh ? '端口开放时不能熄屏' : 'Screen stays on while ports are open');
    }
  }

  function paint(anim) {
    if (!window.NKScreenWasm) return;
    window.NKScreen.render(canvas, snapshot(), anim || { pulse: 0.5, ring: 0.62, slide: 1 }).then(() => {
      ready = true;
      sync();
    }).catch(() => {});
  }

  function draw(t) {
    if (!powered || animating) { raf = requestAnimationFrame(draw); return; }
    /* 两种页面都 30fps：ADMIN 的图标外圈是常驻动画（M3 不确定圆形指示器）。 */
    const every = 33;
    if (t - last >= every) {
      last = t;
      const pulse = (Math.sin(t / 430) + 1) / 2;
      window.NKScreen.render(canvas, snapshot(), { pulse, ring: 0.62, slide: 1 }).then(() => {
        ready = true;
        sync();
      }).catch(() => {});
    }
    raf = requestAnimationFrame(draw);
  }

  function start() {
    if (loading) return;
    loading = true;
    window.NKScreen.load().then(async () => {
      await window.NKScreen.render(canvas, snapshot(), { pulse: 0.5, ring: 0.62, slide: 1 });
      ready = true;
      loading = false;
      sync();
      if (!raf) raf = requestAnimationFrame(draw);
    }).catch((e) => {
      loading = false;
      if (hint) hint.hidden = false;
      console.warn('screen wasm unavailable; keep static image', e);
    });
  }

  const provs = [...document.querySelectorAll('.prov')];
  function select(id) {
    if (!id) return;
    if (selected.has(id)) {
      if (selected.size > 1) selected.delete(id);   // 至少保留一个
    } else {
      selected.add(id);
    }
    provs.forEach((g) => g.setAttribute('aria-pressed', String(selected.has(g.dataset.prov))));
    if (isLocked()) paint();
  }
  provs.forEach((g) => {
    g.addEventListener('click', () => select(g.dataset.prov));
    g.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); select(g.dataset.prov); }
    });
  });
  provs.forEach((g) => g.setAttribute('aria-pressed', String(selected.has(g.dataset.prov))));

  if (sw) sw.addEventListener('change', () => {
    if (isLocked()) {
      adminAt = 0;
    } else {
      adminAt = Date.now();
      powered = true; /* 端口开放期间屏幕必须常亮 */
    }
    paint();
    sync();
  });

  if (off) {
    off.addEventListener('click', async () => {
      if (!isLocked()) { sync(); return; } /* 端口开放时不允许熄屏 */
      powered = !powered;
      sync();
      if (!powered) {
        animating = true;
        if (window.NKCRT) window.NKCRT.play(canvas, false, () => { animating = false; });
        else { const ctx = canvas.getContext('2d'); ctx.fillStyle = '#000'; ctx.fillRect(0, 0, 240, 240); animating = false; }
        return;
      }
      if (!window.NKScreenWasm) { animating = false; return; }
      await window.NKScreen.render(canvas, snapshot(), { pulse: 0.5, ring: 0.62, slide: 1 });
      ready = true;
      sync();
      animating = true;
      if (window.NKCRT) window.NKCRT.play(canvas, true, () => { animating = false; });
      else animating = false;
    });
  }

  window.addEventListener('langchange', (e) => setLang(e.detail.lang));

  sync();
  if ('requestIdleCallback' in window) requestIdleCallback(start, { timeout: 1200 });
  else setTimeout(start, 300);

  document.addEventListener('visibilitychange', () => {
    if (document.hidden) { if (raf) cancelAnimationFrame(raf); raf = 0; }
    else if (window.NKScreenWasm) { if (!raf) raf = requestAnimationFrame(draw); }
    else if (loading) start();
  });
})();
