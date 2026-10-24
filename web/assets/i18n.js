/* 中英同页：只替换文本与少量属性，不动 DOM 结构。
   因此滚动位置、供应商多选、物理开关、小屏当前页都不会被重置。
   用法：/?lang=zh 或点导航右侧按钮；切换只做 history.replaceState。 */
(function () {
  const DICT = {
    'Install': '安装',
    'open-source standalone key box': '开源独立的密钥设备',
    'Standalone hardware and a physical switch block accidental or malicious agent access to API keys.': '用独立硬件和物理开关阻断 agent 对 apikey 的意外/恶意访问',
    'Screen needs http:// — browsers block WebAssembly from file://.': '小屏需要 http:// 打开：浏览器禁止从 file:// 加载 WebAssembly。',
    'nokeyout box': 'nokeyout 盒子',
    'device A': '设备A',
    'device B': '设备B',
    'browser': '浏览器',
    'panel login': '面板登录',
    'gateway key': '网关密钥',
    'provider keys': '供应商密钥',
    'closed': '已关闭',
    'open': '开放',
    'panel': '管理面',
    'providers': '供应商',
    'Three things the box does': '这台盒子做的三件事',
    'Keys on the box': '密钥在盒子里',
    'Provider keys live only on the device. Clients get gateway keys, revocable per machine.': '供应商密钥只存在设备上。客户端只拿得到可逐台吊销的网关密钥。',
    'Physical lock': '物理锁',
    'The switch closes SSH and the panel.': '开关同时关闭 SSH 与管理面。',
    'Local screen': '本地小屏',
    '240x240: IP, lock state, usage, quota.': '240x240：IP、锁定状态、用量与配额。',
    'Reference build: NanoPi NEO3 Plus. Any 256 MB ARM64 board runs it headless.': '参考配置：NanoPi NEO3 Plus；任何 256 MB 的 ARM64 板子都能无头运行。',
    'Part': '件',
    'Qty': '数量',
    'Spec': '规格',
    'Cost': '价格',
    'RK3528A · 1GB · GbE · eMMC/microSD · 26-pin header': 'RK3528A · 1GB · 千兆网口 · eMMC/microSD · 26-pin 2.54mm 排针',
    'Slide switch': '拨动开关',
    '2-position latching': '锁存 2 档',
    'Jumper wires': '杜邦线',
    'female-female': '母-母',
    'ST7789 display': 'ST7789 屏模块',
    '1.3 or 1.54 in · 240x240 · SPI · header pre-soldered': '1.3 或 1.54 英寸 · 240×240 · SPI · 排针已焊',
    'USB-C power': 'USB-C 电源',
    '5V / 3A or more': '5V / 3A 及以上',
    'TF card': 'TF 卡',
    '≥16GB · Class 10 or faster': '≥16GB · Class 10 及以上',
    'USB card reader': 'USB 读卡器',
    'USB-A or USB-C': 'USB-A 或 USB-C 接口',
    'Ethernet cable': '网线',
    '100M or faster (Cat5 or better)': '百兆及以上（Cat5 或更好）',
    'Total': '合计',
    'priced parts only': '仅计价件',
    'Reference prices, ¥1 = $0.15.': '参考价，¥1 = $0.15。',
    'Build one': '开始组装',
    'Pinout notes': '引脚说明'
  };

  const H1 = {
    en: 'A small box that locks API key access with a <span class="k">physical switch</span>.<span class="cursor" aria-hidden="true"></span>',
    zh: '一个小盒子，用<span class="k">物理开关</span>锁定 apikey 访问权限。<span class="cursor" aria-hidden="true"></span>'
  };
  const LEGAL = {
    en: 'Gateway engine: <a href="https://github.com/yetone/magpie">yetone/magpie</a> (MIT).',
    zh: '网关引擎：<a href="https://github.com/yetone/magpie">yetone/magpie</a>(MIT)'
  };
  const TITLE = {
    en: 'NoKeyOut - a standalone box that holds your API keys',
    zh: 'NoKeyOut - 一个保管 API 密钥的小盒子'
  };
  const DESC = {
    en: 'NoKeyOut is a small ARM64 device on your LAN: provider API keys live on it, a physical switch closes SSH and the panel, and a 240x240 screen reports state locally.',
    zh: 'NoKeyOut 是局域网上一台 ARM64 小设备：供应商 API 密钥只存在它上面，物理开关关闭 SSH 与管理面，240x240 小屏在本地显示状态。'
  };
  const ATTRS = [
    ['#hero-screen', 'aria-label', { en: 'Live 240x240 device screen', zh: '实时 240x240 设备屏' }],
    ['.topo svg', 'aria-label', { en: 'A LAN with device A running codex, device B running opencode, and a browser. Both devices use gateway keys to reach magpie on the box. magpie holds the provider keys and sends requests to the providers listed on the right. The switch in the box header gates :22 and :8443. The live 240x240 screen sits beside the diagram.', zh: '一个局域网：设备 A 运行 codex，设备 B 运行 opencode，加一个浏览器。两台设备都用网关密钥访问盒子里的 magpie；magpie 保管供应商密钥，并把请求发给右侧列出的供应商。盒子标题栏里的开关门控 :22 与 :8443。实时 240x240 小屏在图旁。' }],
    ['#pswitch', 'aria-label', { en: 'Physical switch: on is LOCK (normal), off is ADMIN (ports open)', zh: '物理开关：开为 LOCK（正常），关为 ADMIN（端口开放）' }]
  ];

  const root = document.documentElement;
  const btn = document.getElementById('lang-btn');
  const h1 = document.getElementById('hero-title');
  const legal = document.querySelector('footer .legal');
  const title = document.querySelector('title');
  const desc = document.querySelector('meta[name="description"]');
  const orig = new Map();
  let lang = 'en';

  (function collect() {
    const w = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT, {
      acceptNode(n) {
        const p = n.parentElement;
        if (!p || /^(SCRIPT|STYLE|NOSCRIPT)$/.test(p.tagName)) return NodeFilter.FILTER_REJECT;
        if (p.closest('#hero-title, footer .legal')) return NodeFilter.FILTER_REJECT;
        return NodeFilter.FILTER_ACCEPT;
      }
    });
    let n;
    while ((n = w.nextNode())) orig.set(n, n.nodeValue);
  })();

  function apply(next, push) {
    lang = next;
    const zh = next === 'zh';
    for (const [node, en] of orig) {
      const key = en.trim();
      const zhText = DICT[key];
      node.nodeValue = zh && zhText != null ? en.replace(key, zhText) : en;
    }
    const diy = zh ? '/diy/?lang=zh' : '/diy/';
    document.querySelectorAll('a[href^="/diy/"]').forEach((a) => { a.href = diy; });
    if (h1) h1.innerHTML = H1[next];
    if (legal) legal.innerHTML = LEGAL[next];
    if (title) title.textContent = TITLE[next];
    if (desc) desc.setAttribute('content', DESC[next]);
    for (const [sel, attr, v] of ATTRS) {
      const el = document.querySelector(sel);
      if (el) el.setAttribute(attr, v[next]);
    }
    root.lang = zh ? 'zh-CN' : 'en';
    if (btn) btn.textContent = zh ? 'EN' : '中文';
    if (push) history.replaceState(null, '', zh ? '?lang=zh' : location.pathname);
    dispatchEvent(new CustomEvent('langchange', { detail: { lang: next } }));
  }

  const q = new URLSearchParams(location.search).get('lang');
  apply(q === 'zh' ? 'zh' : 'en', false);

  if (btn) btn.addEventListener('click', () => apply(lang === 'zh' ? 'en' : 'zh', true));
})();
