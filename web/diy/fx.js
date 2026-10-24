'use strict';
/* NoKeyOut 装机演练 · 完成庆祝视觉效果（canvas 彩带 + HUD 脉冲） */
(function () {
  const cv = document.getElementById('fx-canvas');
  if (!cv) return;
  const g = cv.getContext('2d');
  const reduce = !!(window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches);
  let W = 0, H = 0, dpr = 1;
  let parts = [], raf = 0, until = 0;

  function resize() {
    dpr = Math.min(window.devicePixelRatio || 1, 2);
    W = window.innerWidth; H = window.innerHeight;
    cv.width = Math.round(W * dpr); cv.height = Math.round(H * dpr);
    cv.style.width = W + 'px'; cv.style.height = H + 'px';
    g.setTransform(dpr, 0, 0, dpr, 0, 0);
  }
  window.addEventListener('resize', resize);
  resize();

  const COLORS = ['#4f8ff7', '#35c76d', '#f0a83c', '#f0574f', '#bc8cff', '#39c5cf', '#e8eef5'];

  function spawn(x, y, n, power) {
    for (let i = 0; i < n; i++) {
      const a = -Math.PI / 2 + (Math.random() - 0.5) * Math.PI * 1.5;
      const sp = (4 + Math.random() * 7) * (power || 1);
      parts.push({
        x, y,
        vx: Math.cos(a) * sp,
        vy: Math.sin(a) * sp,
        w: 3 + Math.random() * 5,
        h: 4 + Math.random() * 7,
        rot: Math.random() * Math.PI,
        vr: (Math.random() - 0.5) * 0.35,
        life: 0,
        ttl: 170 + Math.random() * 110,
        color: COLORS[Math.floor(Math.random() * COLORS.length)],
      });
    }
  }

  function tick(now) {
    g.clearRect(0, 0, W, H);
    for (let i = parts.length - 1; i >= 0; i--) {
      const p = parts[i];
      p.life++;
      p.vy += 0.22;
      p.vx *= 0.992;
      p.vy *= 0.992;
      p.x += p.vx; p.y += p.vy; p.rot += p.vr;
      if (p.life > p.ttl || p.y > H + 40) { parts.splice(i, 1); continue; }
      g.save();
      g.translate(p.x, p.y);
      g.rotate(p.rot);
      g.globalAlpha = Math.max(0, 1 - p.life / p.ttl);
      g.fillStyle = p.color;
      g.fillRect(-p.w / 2, -p.h / 2, p.w, p.h);
      g.restore();
    }
    if (parts.length) { raf = requestAnimationFrame(tick); }
    else { raf = 0; g.clearRect(0, 0, W, H); }
  }

  function run() {
    if (!raf) raf = requestAnimationFrame(tick);
  }

  let lastCelebrate = 0;
  function celebrate() {
    const now = performance.now();
    if (now - lastCelebrate < 2500) return;
    lastCelebrate = now;
    const x = W / 2, y = H * 0.38;
    spawn(x, y, reduce ? 50 : 170, 1.2);
    setTimeout(() => spawn(W * 0.24, H * 0.45, reduce ? 20 : 80, 0.9), 180);
    setTimeout(() => spawn(W * 0.76, H * 0.45, reduce ? 20 : 80, 0.9), 300);
    setTimeout(() => spawn(x, y - 30, reduce ? 30 : 110, 1), 620);
    const scene = document.getElementById('scene');
    if (scene) {
      scene.classList.remove('fx-celebrate'); void scene.offsetWidth; scene.classList.add('fx-celebrate');
      setTimeout(() => scene.classList.remove('fx-celebrate'), 2200);
    }
    run();
  }

  function pop() {
    if (reduce) return;
    const card = document.getElementById('step-card');
    const bar = document.getElementById('progress-fill');
    if (card) {
      card.classList.remove('fx-pop'); void card.offsetWidth; card.classList.add('fx-pop');
      setTimeout(() => card.classList.remove('fx-pop'), 520);
    }
    if (bar) {
      bar.classList.remove('fx-glow'); void bar.offsetWidth; bar.classList.add('fx-glow');
      setTimeout(() => bar.classList.remove('fx-glow'), 700);
    }
  }

  function shake() {
    if (reduce) return;
    const card = document.getElementById('step-card');
    if (!card) return;
    card.classList.remove('fx-shake'); void card.offsetWidth; card.classList.add('fx-shake');
    setTimeout(() => card.classList.remove('fx-shake'), 420);
  }

  window.NKFX = { celebrate, pop, shake, spawn };
})();
