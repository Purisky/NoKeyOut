/* 主页互动屏的演示快照。字段与 panel 的 stats.snapshot() 一致；
 * 渲染仍由 nkscreen.wasm 的原生 Go 代码完成。 */
(function () {
  const now = Math.floor(Date.now() / 1000);
  const day = 86400;
  /* 确定性伪随机 + 昼夜曲线：凌晨低谷、下午峰值、晚间次峰。 */
  /* RFC3339，带浏览器本地时区偏移（不是 Z）——屏上的时间轴按它取本地时间。 */
  const rfc3339Local = (ms) => {
    const d = new Date(ms);
    const pad = (n) => String(n).padStart(2, '0');
    const off = -d.getTimezoneOffset(); // 东为正（分钟）
    const sign = off >= 0 ? '+' : '-';
    return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) +
      'T' + pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds()) +
      sign + pad(Math.floor(Math.abs(off) / 60)) + ':' + pad(Math.abs(off) % 60);
  };
  const hash01 = (i, seed) => {
    const x = Math.sin((i + 1) * 12.9898 + seed * 78.233) * 43758.5453;
    return x - Math.floor(x);
  };
  const diurnal = (sec) => {
    const d = new Date(sec * 1000);
    const h = d.getHours() + d.getMinutes() / 60;
    return 0.08 + Math.exp(-Math.pow(h - 14.5, 2) / 16) + 0.55 * Math.exp(-Math.pow(h - 21, 2) / 6);
  };
  const buckets = (n, seed, periodSec) =>
    Array.from({ length: n }, (_, i) => {
      const t = now - periodSec + ((i + 0.5) * periodSec) / n;
      const jitter = 0.6 + 0.8 * hash01(i, seed);
      return { label: '', tokens: Math.round(9200 * (periodSec / n / 384) * diurnal(t) * jitter) };
    });

  const usageOf = (period, periodSeconds, seed, n) => {
    const b = buckets(n, seed, periodSeconds);
    const tokens = b.reduce((sum, x) => sum + x.tokens, 0);
    const input = Math.round(tokens * 0.72);
    const output = tokens - input;
    const cacheRead = Math.round(input * 1.55);
    const calls = Math.max(1, Math.round(tokens / 820));
    return {
      period,
      since: rfc3339Local((now - periodSeconds) * 1000),
      calls,
      errors: Math.max(1, Math.round(calls * 0.002)),
      input, output, cache_read: cacheRead, tokens,
      cache_hit_pct: Math.round((1000 * cacheRead) / (cacheRead + input)) / 10,
      buckets: b,
    };
  };

  const pools = [
    {
      name: 'DeepSeek', provider: 'deepseek', kind: 'balance', window: '',
      account: '', state: 0, has_val: false, used_pct: null,
      has_reset: false, mode: 'balance', amount: 12.34, unit: 'USD',
    },
    {
      name: 'GPT', provider: 'openai', kind: 'period', window: '7 days',
      account: '', state: 1, has_val: true, used_pct: 63, remain_pct: 37,
      has_reset: true, reset_at: now + 3 * day + 6 * 3600, period_s: 7 * day,
      mode: 'period',
    },
    {
      name: 'Claude', provider: 'anthropic', kind: 'period', window: '5 hours',
      account: '', state: 1, has_val: true, used_pct: 42, remain_pct: 58,
      has_reset: true, reset_at: now + 3 * 3600 + 2400, period_s: 5 * 3600,
      mode: 'period',
    },
    {
      name: 'OpenCode Go', provider: 'opencode', kind: 'period', window: '5 hours',
      account: '', state: 1, has_val: true, used_pct: 22, remain_pct: 78,
      has_reset: true, reset_at: now + 2 * 3600 + 900, period_s: 5 * 3600,
      mode: 'period',
    },
  ];

  /* 滚动窗口：today = now-24h，7d = now-7d，30d = now-30d；列数 224（每列一像素）。 */
  const usage = {
    today: usageOf('today', day, 1, 224),
    '7d': usageOf('7d', 7 * day, 2, 224),
    '30d': usageOf('30d', 30 * day, 3, 224),
  };
  const t24 = usage.today;

  const main = {
    lang: 'zh',
    switch: 'locked',
    url: 'https://<device-ip>:8443',
    net: { eth: 'up', inet: 'up' },
    generated_at: now,
    traffic: {
      calls_today: t24.calls,
      errors_today: t24.errors,
      tokens_today: { input: t24.input, output: t24.output },
      cache_hit_pct: t24.cache_hit_pct,
    },
    pools,
    usage,
  };

  /* 只保留正常运行态：LOCK 是设备的默认状态，屏上显示配额。 */
  window.NK_SCREEN_STATES = { main };
})();
