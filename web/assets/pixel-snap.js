/* 像素图标吸附到设备像素网格。
   原因：16px 图标在 DPR 1.25 的屏幕上，若布局落在半个设备像素上（x=110 → 137.5），
   浏览器会把 20px 位图拉成 21px，像素被重采样。这里把盒子左上角对齐到整数设备像素。 */
(function () {
  const SEL = 'img.mark, img.brand-mark, img.load-mark, img.complete-mark';
  const imgs = () => Array.from(document.querySelectorAll(SEL));
  function snap() {
    const dpr = window.devicePixelRatio || 1;
    for (const img of imgs()) {
      img.style.transform = 'none';
      const r = img.getBoundingClientRect();
      if (!r.width || !r.height) continue;
      const dx = Math.round(r.left * dpr) / dpr - r.left;
      const dy = Math.round(r.top * dpr) / dpr - r.top;
      if (Math.abs(dx) > 0.001 || Math.abs(dy) > 0.001) {
        img.style.transform = 'translate(' + dx + 'px,' + dy + 'px)';
      }
    }
  }
  addEventListener('resize', snap);
  addEventListener('load', snap);
  if (document.fonts && document.fonts.ready) document.fonts.ready.then(snap);
  requestAnimationFrame(snap);
})();
