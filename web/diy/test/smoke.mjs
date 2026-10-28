#!/usr/bin/env node
/**
 * NoKeyOut 单场景装机演练 — 烟雾/回归测试（零依赖，Node >= 22）
 *
 * 用法：  node web/diy/test/smoke.mjs
 * 环境：  CHROME_PATH 可指定 Chromium 可执行文件；默认探测 Playwright 缓存里的 Chromium。
 *
 * 断言的是消费者可见行为：
 *   S1 直接进入 3D 场景（没有着陆页/采购页/关卡图/检查模式）
 *   S2 点击顺序动画：先点板端 fade+插入预览；先点开关端火花；第二端后线从第一端生长
 *   S3 门控 2 根线按顺序推进，母头插到底
 *   S4 屏幕 8 根线按顺序推进
 *   S5 最后翻屏/点亮/USR/开关联动
 *   S6 全部 14 步后出现完成面板 + 徽章，进度可恢复
 *   S7 全程零 pageerror
 */
import { spawn } from 'node:child_process';
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { homedir, tmpdir } from 'node:os';
import { extname, join } from 'node:path';

const SITE_DIR = join(process.cwd(), 'site');
const PASS = [], FAIL = [];
const ok = (name, cond, extra = '') => (cond ? PASS : FAIL).push(name + (extra ? ` — ${extra}` : ''));
const log = (s) => console.log(s);

/* ---------- 静态服务器 ---------- */
const MIME = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.jpg': 'image/jpeg', '.png': 'image/png', '.json': 'application/json', '.wasm': 'application/wasm', '.fbx': 'application/octet-stream' };
function serveStatic() {
  const srv = createServer((req, res) => {
    let rel = decodeURIComponent(req.url.split('?')[0]).replace(/^\/+/, '') || 'index.html';
    if (rel.endsWith('/')) rel += 'index.html';
    try {
      const buf = readFileSync(join(SITE_DIR, rel));
      res.writeHead(200, { 'content-type': MIME[extname(rel)] || 'application/octet-stream' });
      res.end(buf);
    } catch {
      res.writeHead(404); res.end('not found');
    }
  });
  return new Promise((resolve) => srv.listen(0, '127.0.0.1', () => resolve({ srv, port: srv.address().port })));
}

/* ---------- 找 Chromium ---------- */
function findChrome() {
  if (process.env.CHROME_PATH && existsSync(process.env.CHROME_PATH)) return process.env.CHROME_PATH;
  const root = join(homedir(), 'AppData', 'Local', 'ms-playwright');
  if (!existsSync(root)) return null;
  const revs = readdirSync(root).filter((d) => /^chromium-\d+$/.test(d)).sort().reverse();
  for (const r of revs) {
    const p = join(root, r, 'chrome-win64', 'chrome.exe');
    if (existsSync(p)) return p;
  }
  return null;
}

/* ---------- 极简 CDP ---------- */
class CDP {
  constructor(ws) { this.ws = ws; this.id = 0; this.pending = new Map(); this.pageerrors = []; }
  static async attach(wsUrl) {
    const ws = new WebSocket(wsUrl);
    await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
    const c = new CDP(ws);
    ws.onmessage = (ev) => {
      const m = JSON.parse(ev.data);
      if (m.id && c.pending.has(m.id)) {
        const { res, rej } = c.pending.get(m.id); c.pending.delete(m.id);
        m.error ? rej(new Error(m.error.message)) : res(m.result);
      } else if (m.method === 'Runtime.exceptionThrown') {
        c.pageerrors.push(m.params.exceptionDetails.exception?.description || m.params.exceptionDetails.text);
      } else if (m.method === 'Log.entryAdded' && m.params.entry.level === 'error') {
        c.pageerrors.push(m.params.entry.text);
      }
    };
    await c.send('Page.enable'); await c.send('Runtime.enable'); await c.send('Log.enable');
    return c;
  }
  send(method, params = {}) {
    const id = ++this.id;
    this.ws.send(JSON.stringify({ id, method, params }));
    return new Promise((res, rej) => this.pending.set(id, { res, rej }));
  }
  async eval(expression) {
    const r = await this.send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
    if (r.exceptionDetails) throw new Error('eval: ' + (r.exceptionDetails.exception?.description || r.exceptionDetails.text));
    return r.result.value;
  }
  async goto(url) {
    await this.send('Page.navigate', { url });
    await this.waitFor(`location.href.startsWith('${url.split('#')[0]}')`, 20000);
  }
  async waitFor(expr, timeout = 10000, gap = 100) {
    const t0 = Date.now();
    for (;;) {
      try { if (await this.eval(`!!(${expr})`)) return true; } catch { /* navigating */ }
      if (Date.now() - t0 > timeout) throw new Error('waitFor timeout: ' + expr.slice(0, 100));
      await new Promise((r) => setTimeout(r, gap));
    }
  }
  async click(sel) { await this.eval(`document.querySelector(${JSON.stringify(sel)})?.click()`); }
  pick(id) { return this.eval(`window.__nkPick(${JSON.stringify(id)})`); }
  step() { return this.eval(`window.__nkStep()`); }
  state() { return this.eval(`window.__nkState()`); }
  async submit() { await this.click('#step-submit'); }
  async select(v) {
    await this.eval(`(() => { const s = document.getElementById('step-select'); s.value = '${v}'; document.getElementById('step-submit').click(); })()`);
  }
  async fill(v) {
    await this.eval(`(() => { const i = document.getElementById('step-input'); i.value = '${v}'; document.getElementById('step-submit').click(); })()`);
  }
  async mode(m) { await this.click(`[data-range="${m}"]`); }
  async feedback() { return this.eval(`(document.getElementById('step-feedback')?.hidden === false ? document.getElementById('step-feedback').textContent : '')`); }
}

/* ---------- 驱动 ---------- */
const chrome = findChrome();
if (!chrome) { console.error('找不到 Chromium：设置 CHROME_PATH'); process.exit(2); }
const { srv, port: webPort } = await serveStatic();
const siteUrl = `http://127.0.0.1:${webPort}/diy/`;
const port = 9400 + Math.floor(Math.random() * 500);
const profile = join(tmpdir(), 'nk-smoke-scene-profile');
const child = spawn(chrome, [
  '--headless=new', '--lang=zh-CN', `--remote-debugging-port=${port}`, `--user-data-dir=${profile}`,
  '--no-first-run', '--no-default-browser-check', '--disable-extensions', '--disable-gpu',
  '--enable-unsafe-swiftshader', '--use-angle=swiftshader', '--allow-file-access-from-files',
  '--memory-pressure-off', 'about:blank'
], { stdio: 'ignore' });
process.on('exit', () => { child.kill(); srv.close(); });

const wait = (ms) => new Promise((r) => setTimeout(r, ms));
const expectStep = async (cdp, id, label) => {
  const s = await cdp.step();
  ok(label || id, !!s && s.id === id, s ? `got ${s.id}` : 'no step');
};

let cdp;
try {
  let version = null;
  for (let i = 0; i < 40 && !version; i++) {
    try { version = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json(); } catch { await wait(300); }
  }
  if (!version) throw new Error('chromium 未就绪');
  const list = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
  const page = list.find((t) => t.type === 'page');
  cdp = await CDP.attach(page.webSocketDebuggerUrl);
  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 900, deviceScaleFactor: 1, mobile: false });
  await cdp.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'no-preference' }] });
  await cdp.goto(siteUrl);
  await cdp.waitFor(`window.__nkStep && window.__nkStep()`);
  await cdp.waitFor(`window.NKBOARD3D && window.NKBOARD3D.mounted()`, 25000);
  await cdp.eval(`try { localStorage.clear() } catch (e) {}`);
  await cdp.goto(siteUrl + '?r=1');
  await cdp.waitFor(`window.__nkStep && window.__nkStep()`);
  await cdp.waitFor(`window.NKBOARD3D && window.NKBOARD3D.mounted()`, 25000);

  /* S1 直接进入场景 */
  const boot = await cdp.eval(`({
    landing: !!document.getElementById('landing'),
    procurement: document.querySelectorAll('[data-proc]').length,
    levelCards: document.querySelectorAll('.levelcard').length,
    modeChips: document.querySelectorAll('.mode-chip').length,
    scene: !!(window.NKBOARD3D && window.NKBOARD3D.mounted()),
    canvas: !!document.querySelector('#bench3d canvas'),
    title: document.title,
    brand: document.querySelector('#hud-top .brand')?.textContent,
    github: (() => { const a = document.getElementById('hud-github'); const r = a && a.getBoundingClientRect(); return a ? { label: a.getAttribute('aria-label'), href: a.href, visible: !!(r && r.width > 0 && r.height > 0) } : null; })(),
    step: window.__nkStep()
  })`);
  ok('S1a 没有着陆页 / 采购 / 关卡图', !boot.landing && boot.procurement === 0 && boot.levelCards === 0);
  ok('S1b 页面就是 3D 场景', boot.scene && boot.canvas, JSON.stringify(boot));
  ok('S1b1 标题/品牌为 nokeyout，顶部常驻 GitHub 地址',
    boot.title === 'nokeyout' && boot.brand === 'nokeyout' && boot.github && boot.github.visible &&
    boot.github.label === 'GitHub' &&
    /github\.com\/Purisky\/NoKeyOut/.test(boot.github.href), JSON.stringify(boot));
  ok('S1c 第一步直接是开关到主板排针', boot.step && boot.step.id === 'w0', JSON.stringify(boot.step));
  ok('S1d 不含检查/安全步骤', boot.modeChips === 0 && !boot.step.id.startsWith('s:'));
  const st0 = await cdp.state();
  ok('S1e 简单流程共 14 步', st0.total === 14, 'total=' + st0.total);
  ok('S1f 初始进度 0 / 13', (await cdp.eval(`document.getElementById('hud-count').textContent`)) === '0 / 13');
  ok('B0 起始步骤的上一步按钮禁用', await cdp.eval(`document.getElementById('btn-back').disabled`));
  await cdp.click('#hud-proc');
  const proc = await cdp.eval(`({ visible: !document.getElementById('procurement').hidden,
    items: [...document.querySelectorAll('#proc-list li')].map((li) => li.innerText),
    note: !!document.querySelector('#procurement [data-i18n="proc.note"]'),
    button: document.getElementById('hud-proc').textContent })`);
  await cdp.click('#proc-close');
  ok('S1j 顶部采购清单列出 8 项且首项为参考主板', proc.visible && proc.items.length === 8 &&
    /NanoPi NEO3 Plus/.test(proc.items[0]) && /RK3528A/.test(proc.items[0]) &&
    /拨动开关/.test(proc.items[1]) && proc.note === false &&
    (await cdp.eval(`document.getElementById('procurement').hidden`)) === true,
    JSON.stringify({ n: proc.items.length, button: proc.button }));
  const silk = await cdp.eval(`(() => {
    const L = {}; window.NKView3D.silkDebug().forEach((x) => { L[x.text] = x; });
    const B = {}; ['D1','D2','K1','K2'].forEach((r) => { B[r] = window.NKView3D.boardPartBBox(r); });
    return { L, B };
  })()`);
  const near = (a, b) => Math.abs(a - b) < 0.02;
  ok('S1g 丝印与 LED/按键实际包围盒对齐', (() => {
    const { L, B } = silk;
    const cx = (b) => (b.min[0] + b.max[0]) / 2;
    const cz = (b) => (b.min[2] + b.max[2]) / 2;
    return L.SYS && L.LED1 && L.MASK && L.USR &&
      near(L.SYS.x, B.D1.max[0] + 2.8) && near(L.SYS.z, cz(B.D1)) &&
      near(L.LED1.x, B.D2.max[0] + 2.8) && near(L.LED1.z, cz(B.D2)) &&
      near(L.MASK.x, cx(B.K1)) && near(L.MASK.z, B.K1.min[2] - 1.5) &&
      near(L.USR.x, cx(B.K2)) && near(L.USR.z, B.K2.min[2] - 1.5);
  })(), JSON.stringify(silk));
  ok('S1h 音效与特效模块已加载', await cdp.eval(`typeof window.NKSFX?.play === 'function' && typeof window.NKFX?.celebrate === 'function'`));
  const sfx0 = await cdp.eval(`document.getElementById('sfx-toggle').getAttribute('aria-pressed')`);
  await cdp.click('#sfx-toggle');
  const sfx1 = await cdp.eval(`document.getElementById('sfx-toggle').getAttribute('aria-pressed')`);
  await cdp.click('#sfx-toggle');
  ok('S1i 音效可静音/恢复', sfx0 !== sfx1, JSON.stringify({ sfx0, sfx1 }));

  /* S2 门控接线：5V 误接 / 外脚错误 / 点击顺序动画 */
  await cdp.pick('pin:2'); await cdp.pick('sw:B');
  ok('W0 5V→开关 GND 端被判为危险', /5V|GPIO|烧/i.test(await cdp.feedback()) && (await cdp.state()).idx === 0);
  await cdp.pick('sw:A'); await cdp.pick('pin:11');
  ok('W0b 外脚 A 不能当门控信号线', (await cdp.state()).idx === 0);

  const cam0 = await cdp.eval(`window.NKView3D.debug().cam`);
  await cdp.click('#lang-toggle');
  const cam1 = await cdp.eval(`window.NKView3D.debug().cam`);
  ok('C0 切换语言不改变视角', JSON.stringify(cam0) === JSON.stringify(cam1), JSON.stringify({ cam0, cam1 }));
  await cdp.click('#lang-toggle');

  await cdp.pick('pin:11');
  const prev = await cdp.eval(`window.NKView3D.wireDebug()`);
  ok('W1 先点板端出现杜邦帽 fade+插入预览、还没有线', prev.preview === 1 && prev.inserting === 1 && prev.wires === 0, JSON.stringify(prev));
  const sp0 = await cdp.eval(`window.NKView3D.sparkCount()`);
  const camBefore = await cdp.eval(`window.NKView3D.debug().cam`);
  await cdp.pick('sw:C');
  const camAfter = await cdp.eval(`window.NKView3D.debug().cam`);
  ok('C1 推进步骤不改变视角', JSON.stringify(camBefore) === JSON.stringify(camAfter), JSON.stringify({ camBefore, camAfter }));
  const dpr0 = await cdp.eval(`window.NKView3D.debug().renderer.pixelRatio`);
  await cdp.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: 1150, y: 430, button: 'left', buttons: 1, clickCount: 1 });
  await cdp.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 1210, y: 470, button: 'left', buttons: 1 });
  await wait(80);
  const dprDrag = await cdp.eval(`window.NKView3D.debug().renderer.pixelRatio`);
  await cdp.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: 1210, y: 470, button: 'left', buttons: 0, clickCount: 1 });
  ok('C2 拖拽旋转时保持全分辨率（不降 DPR）', Math.abs(dprDrag - dpr0) < 0.001,
    JSON.stringify({ dpr0, dprDrag }));
  const sp1 = await cdp.eval(`window.NKView3D.sparkCount()`);
  ok('W2 第二端是开关时触发焊接火花', sp1 > sp0, `sparks=${sp1}`);
  const wd0 = await cdp.eval(`window.NKView3D.wireDebug()`);
  ok('W3 线从第一端生长，开关端是焊点、板端是杜邦', wd0.tubes === 1 && wd0.solder === 1 && wd0.dupont === 3,
    JSON.stringify(wd0));
  await expectStep(cdp, 'w1', 'W4 接好 C 后进入 B');

  const sp2 = await cdp.eval(`window.NKView3D.sparkCount()`);
  await cdp.pick('sw:B');
  const sp3 = await cdp.eval(`window.NKView3D.sparkCount()`);
  ok('W5 先点开关端触发火花', sp3 > sp2, `sparks=${sp3}`);
  await cdp.pick('pin:9');
  const wd1 = await cdp.eval(`window.NKView3D.wireDebug()`);
  ok('W6 第二端是板端时真实母头 fade+插入（不再是重复预览）', wd1.preview === 0 && wd1.inserting === 1, JSON.stringify(wd1));
  ok('W7 两根线：开关端焊点 + 板端杜邦', wd1.tubes === 2 && wd1.solder === 2 && wd1.dupont === 6, JSON.stringify(wd1));
  await cdp.waitFor(`window.NKView3D.wireDebug().inserting === 0`, 2500);
  const wdDone = await cdp.eval(`window.NKView3D.wireDebug()`);
  const pinTop = await cdp.eval(`window.NKView3D.pinTop('CON30_PIN01')`);
  const pinGroups = await cdp.eval(`window.NKView3D.pinGroups('CON30_PIN01')`);
  const plasticTop = (pinGroups.find((g) => g.materialIndex === 0) || {}).maxY;
  ok('W8 杜邦母头插到底并与黑色塑料接触', wdDone.connMinYs.every((y) => Math.abs(y - plasticTop) < 0.25),
    JSON.stringify({ mins: wdDone.connMinYs, plasticTop }));
  ok('W9 母头比外露针长（针完全在帽内）', wdDone.connMaxY > pinTop, JSON.stringify({ max: wdDone.connMaxY, pinTop }));

  const sw0 = await cdp.eval(`window.NKView3D.switchVisual()`);
  await cdp.pick('sw:toggle');
  const sw1 = await cdp.eval(`window.NKView3D.switchVisual()`);
  await cdp.pick('sw:toggle');
  const sw2 = await cdp.eval(`window.NKView3D.switchVisual()`);
  ok('S4g 滑块在 ON 位与镜像位（OFF）之间移动',
    sw0 && sw1 && sw2 && sw0.on && !sw1.on && sw2.on &&
      Math.abs(sw0.x - sw0.base) < 0.05 &&
      Math.abs((sw0.x + sw1.x) / 2 - sw0.axis) < 0.05 &&
      Math.abs(sw1.x - sw0.x) > 0.3 &&
      Math.abs(sw2.x - sw0.x) < 0.05,
    JSON.stringify({ sw0, sw1, sw2 }));

  /* S5 屏幕 8 根 */
  const SCREEN = [['scr:BLK', 'pin:17'], ['scr:CS', 'pin:22'], ['scr:DC', 'pin:15'], ['scr:RST', 'pin:13'],
    ['scr:MOSI', 'pin:19'], ['scr:CLK', 'pin:23'], ['scr:VCC', 'pin:1'], ['scr:GND', 'pin:6']];
  for (let i = 0; i < SCREEN.length; i++) {
    const [a, b] = SCREEN[i];
    await cdp.pick(a); await cdp.pick(b);
    const s = await cdp.state();
    ok(`S5 屏幕第 ${i + 1} 根线推进`, s.idx === 3 + i, 'idx=' + s.idx);
  }

  /* S6 插卡 / 上电 / 网线 → 完成 */
  await expectStep(cdp, 'plug:tfcard', 'S6a 接完屏先烧录 TF 卡');
  const cx = (b) => b ? (b.min[0] + b.max[0]) / 2 : 0;
  const pPark = await cdp.eval(`window.NKBOARD3D.plugDebug()`);
  ok('P0 TF 卡先渲染在卡槽外、不插入', pPark.parked && pPark.progress.tfcard < 0.001 &&
    !pPark.targets.tfcard && pPark.bounds.tfcard && pPark.hosts.tfcard &&
    cx(pPark.bounds.tfcard) < pPark.hosts.tfcard.min[0], JSON.stringify(pPark));
  await cdp.pick('tfcard:card');
  const term = await cdp.eval(`(() => {
    const b = document.getElementById('tf-term').getBoundingClientRect();
    return {
      open: !document.getElementById('tf-term').hidden,
      cmd: document.getElementById('tf-term-cmd').textContent,
      inputDisabled: document.getElementById('tf-term-input').disabled,
      out: document.getElementById('tf-term-out').innerText,
      rect: { cx: b.left + b.width / 2, cy: b.top + b.height / 2, w: b.width, h: b.height },
      vw: innerWidth, vh: innerHeight
    };
  })()`);
  ok('P0b 点 TF 卡弹出伪终端并生成固定命令', term.open &&
    /^curl -fsSL https:\/\/nokeyout\.com\/tools\/install-tf\.sh -o \/tmp\/install-tf\.sh && sudo bash \/tmp\/install-tf\.sh --board neo3plus$/.test(term.cmd) &&
    term.inputDisabled === false, JSON.stringify(term));
  ok('P0b1 终端居中显示', Math.abs(term.rect.cx - term.vw / 2) < 2 &&
    Math.abs(term.rect.cy - term.vh / 2) < 2, JSON.stringify({ rect: term.rect, vw: term.vw, vh: term.vh }));
  const guide = await cdp.eval(`({
    params: [...document.querySelectorAll('#tf-term-params li')].map((li) => li.innerText),
    target: document.getElementById('tf-term-out').innerText
  })`);
  ok('P0b2 终端参数', guide.params.length >= 5 &&
    guide.params.join(' ').includes('--board') && guide.params.join(' ').includes('--resolve-only'),
    JSON.stringify({ params: guide.params.length }));
  const pNo = await cdp.eval(`window.NKBOARD3D.plugDebug()`);
  ok('P0c 打开终端不等于插卡', pNo.progress.tfcard < 0.001 && !pNo.targets.tfcard, JSON.stringify(pNo));
  const controls = await cdp.eval(`({ noBack: !document.getElementById('tf-term-back'),
    cardBack: document.getElementById('btn-back').disabled,
    copy: !!document.getElementById('tf-copy-cmd'),
    skip: !!document.getElementById('tf-term-skip') })`);
  ok('P0c1 终端不能回退；有复制/跳过按钮', controls.noBack && controls.cardBack && controls.copy && controls.skip,
    JSON.stringify(controls));
  const submitTf = (v) => cdp.eval(`(() => { const i = document.getElementById('tf-term-input');
    i.value = ${JSON.stringify(v)}; document.getElementById('tf-term-form').requestSubmit(); })()`);
  await submitTf('lsblk');
  const wrongOut = await cdp.eval(`document.getElementById('tf-term-out').innerText`);
  ok('P0d 输错命令不会烧录', /command mismatch/.test(wrongOut) &&
    !(await cdp.eval(`window.__nkTf().flashed`)), wrongOut.split('\n').slice(-1)[0]);
  const cmd = await cdp.eval(`document.getElementById('tf-term-cmd').textContent`);
  await submitTf(cmd.replace('--board neo3plus', '--board opi5max'));
  const wrongBoard = await cdp.eval(`document.getElementById('tf-term-out').innerText`);
  ok('P0d1 只接受 --board neo3plus', /command mismatch/.test(wrongBoard), wrongBoard.split('\n').slice(-1)[0]);
  await submitTf(cmd);
  await cdp.waitFor(`window.__nkTf().mode === 'disk'`, 3000);
  const diskUi = await cdp.eval(`({ mode: window.__nkTf().mode,
    out: document.getElementById('tf-term-out').innerText,
    prompt: document.getElementById('tf-term-prompt').textContent })`);
  ok('P0e1 列盘后必须手动选择（不自动操作）',
    diskUi.mode === 'disk' && /\[1\] \/dev\/sda/.test(diskUi.out) &&
    /Select target disk/.test(diskUi.prompt), JSON.stringify({ mode: diskUi.mode }));
  await submitTf('2');
  await submitTf('1');
  await cdp.waitFor(`window.__nkTf().mode === 'format'`, 3000);
  const formatUi = await cdp.eval(`({ mode: window.__nkTf().mode,
    out: document.getElementById('tf-term-out').innerText,
    prompt: document.getElementById('tf-term-prompt').textContent })`);
  ok('P0e2 格式化警告必须输入 YES', formatUi.mode === 'format' &&
    /WARNING: \/dev\/sda .*ERASED/.test(formatUi.out) && /Type YES/.test(formatUi.prompt),
    JSON.stringify({ mode: formatUi.mode }));
  await submitTf('no');
  await submitTf('YES');
  await cdp.waitFor(`window.__nkTf().mode === 'password'`, 3000);
  const pwUi = await cdp.eval(`({ mode: window.__nkTf().mode,
    type: document.getElementById('tf-term-input').type,
    prompt: document.getElementById('tf-term-prompt').textContent,
    board: window.__nkTf().board })`);
  ok('P0e3 确认格式化后强制进入密码输入（无默认、输入隐藏）',
    pwUi.mode === 'password' && pwUi.type === 'password' && /Password for nokeyout/.test(pwUi.prompt) &&
    pwUi.board === 'neo3plus', JSON.stringify(pwUi));
  await submitTf('');
  await submitTf('testpass');
  await cdp.waitFor(`window.__nkTf().mode === 'confirm'`, 3000);
  await submitTf('mismatch');
  const mismatch = await cdp.eval(`({ mode: window.__nkTf().mode, out: document.getElementById('tf-term-out').innerText })`);
  ok('P0e2 两次密码不一致会重来', mismatch.mode === 'password' &&
    /不一致|do not match/i.test(mismatch.out), JSON.stringify({ mode: mismatch.mode }));
  await submitTf('testpass');
  await cdp.waitFor(`window.__nkTf().mode === 'confirm'`, 3000);
  await submitTf('testpass');
  await cdp.waitFor(`window.__nkTf().busy`, 3000);
  await cdp.waitFor(`/\\d+ MiB \\/ 1\\.8 GiB copied/.test(document.getElementById('tf-term-out').innerText)`, 3000);
  const tfBusy = await cdp.eval(`({ state: document.getElementById('tf-term').dataset.state,
    out: document.getElementById('tf-term-out').innerText,
    bars: document.querySelectorAll('#tf-term-out .term-progress').length })`);
  ok('P0f 终端只显示字符进度（无进度条组件）', tfBusy.state === 'running' && tfBusy.bars === 0 &&
    /==> 1\/3 Resolving board and official image/.test(tfBusy.out) &&
    /image:   latest Armbian community image \(resolved at run time\)/.test(tfBusy.out) &&
    /\d+ MiB \/ 1\.8 GiB copied/.test(tfBusy.out), JSON.stringify({ state: tfBusy.state, bars: tfBusy.bars, out: tfBusy.out.slice(-200) }));
  ok('P0g 密码不回显、不写入终端', !tfBusy.out.includes('testpass') && !tfBusy.out.includes('123456'),
    JSON.stringify(tfBusy.out.slice(-120)));
  await cdp.waitFor(`window.__nkTf().flashed`, 15000);
  const tfOut = await cdp.eval(`document.getElementById('tf-term-out').innerText`);
  ok('P0h 结束输出与真实脚本成功输出一致',
    /==> Done\./.test(tfOut) &&
    /==> User: nokeyout/.test(tfOut) &&
    /==> SSH: {2}enabled \(PasswordAuthentication yes\)/.test(tfOut) &&
    /==> First boot: nokeyout-firstboot\.service/.test(tfOut) &&
    /==> Eject the card, insert it into the board, power on, then: ssh nokeyout@<device-ip>/.test(tfOut),
    tfOut.split('\n').slice(-6).join(' | '));
  await wait(2000);
  const stillOpen = await cdp.eval(`({ hidden: document.getElementById('tf-term').hidden,
    hint: document.getElementById('tf-term-hint').textContent })`);
  ok('P0i 安装完成后终端不自动关闭，等待用户退出', stillOpen.hidden === false &&
    /写卡完成|Flash complete/.test(stillOpen.hint), JSON.stringify(stillOpen));
  await cdp.click('#tf-term-close');
  await cdp.waitFor(`document.getElementById('tf-term').hidden`, 3000);
  const tfDone = await cdp.eval(`({ st: window.__nkState(), tf: window.__nkTf(),
    card: window.NKBOARD3D.plugDebug() })`);
  ok('P0f 烧录完成：状态落盘、卡仍在槽外等插入', tfDone.st.tfFlashed === true &&
    tfDone.card.parked && tfDone.card.progress.tfcard < 0.001, JSON.stringify(tfDone.st));
  await cdp.pick('J3');
  const pTf = await cdp.eval(`window.NKBOARD3D.plugDebug()`);
  ok('P1 烧录后点卡槽才有插入滑动动画', pTf.animating && pTf.progress.tfcard < 1, JSON.stringify(pTf));
  await cdp.waitFor(`window.NKBOARD3D.plugDebug().progress.tfcard > 0.99`, 3000);
  const pTfDone = await cdp.eval(`window.NKBOARD3D.plugDebug()`);
  const tfB = pTfDone.bounds && pTfDone.bounds.tfcard;
  const tfW = tfB ? tfB.max[0] - tfB.min[0] : 0, tfD = tfB ? tfB.max[2] - tfB.min[2] : 0;
  ok('P1b microSD 真实网格是 15×11mm（不是微米级）', Math.abs(tfW - 15) < 0.5 && Math.abs(tfD - 11) < 0.5,
    JSON.stringify(tfB));
  ok('P1c TF 卡沿 -X 从板外插入', cx(pTf.bounds.tfcard) < cx(tfB) - 1,
    JSON.stringify({ start: pTf.bounds.tfcard, end: tfB }));
  await cdp.click('#btn-back');
  const pTfOut = await cdp.eval(`window.NKBOARD3D.plugDebug()`);
  const backTf = await cdp.state();
  ok('B2 返回上一步撤销刚插的卡（烧录也回到未烧录）', backTf.idx === 10 && backTf.current === 'plug:tfcard' &&
    !backTf.plugs.tfcard && backTf.tfFlashed === false &&
    pTfOut.animating && pTfOut.progress.tfcard > 0, JSON.stringify({ backTf, pTfOut }));
  await cdp.waitFor(`window.NKBOARD3D.plugDebug().progress.tfcard < 0.01`, 3000);
  const pBackPark = await cdp.eval(`window.NKBOARD3D.plugDebug()`);
  ok('B2b 返回后卡回到槽外待插入态', pBackPark.parked === true, JSON.stringify(pBackPark));
  await cdp.pick('J3');
  await cdp.waitFor(`window.__nkTf().open`, 4000);
  await cdp.click('#tf-term-skip');
  await cdp.waitFor(`!window.__nkTf().open`, 3000);
  await cdp.pick('J3');
  await cdp.waitFor(`window.NKBOARD3D.plugDebug().progress.tfcard > 0.99`, 3000);
  await expectStep(cdp, 'plug:lan', 'S6b 插完卡后先接网线');
  await cdp.pick('LAN1');
  const pLan = await cdp.eval(`window.NKBOARD3D.plugDebug()`);
  ok('P3 网线插入有滑动动画', pLan.animating && pLan.progress.lan < 1, JSON.stringify(pLan));
  await cdp.waitFor(`window.NKBOARD3D.plugDebug().progress.lan > 0.99`, 3000);
  const pLanDone = await cdp.eval(`window.NKBOARD3D.plugDebug()`);
  const lanB = pLanDone.bounds && pLanDone.bounds.lan;
  const lanS = lanB ? lanB.max.map((v, i) => v - lanB.min[i]) : [0, 0, 0];
  ok('P3b RJ45 是完整模制插头（44×14.3×13.6mm，含护套/线缆）',
    Math.abs(lanS[0] - 44.1) < 1.0 && Math.abs(lanS[1] - 14.3) < 0.8 && Math.abs(lanS[2] - 13.6) < 0.8,
    JSON.stringify({ lanB, lanS }));
  const lanStart = pLan.bounds && pLan.bounds.lan;
  ok('P3c RJ45 沿 +X 从板外插入', cx(lanStart) > cx(lanB) + 1,
    JSON.stringify({ start: lanStart, end: lanB }));
  await expectStep(cdp, 'plug:power', 'S6c 网线之后接电');
  await cdp.pick('USB-C1');
  const pPower = await cdp.eval(`window.NKBOARD3D.plugDebug()`);
  ok('P2 USB-C 插电有滑动动画', pPower.animating && pPower.progress.power < 1, JSON.stringify(pPower));
  await cdp.waitFor(`window.NKBOARD3D.plugDebug().progress.power > 0.99`, 3000);
  const pPowerDone = await cdp.eval(`window.NKBOARD3D.plugDebug()`);
  const usbB = pPowerDone.bounds && pPowerDone.bounds.power;
  const usbS = usbB ? usbB.max.map((v, i) => v - usbB.min[i]) : [0, 0, 0];
  ok('P2b USB-C 是 8.3×2.4×6.65mm 的 Type-C 头（不是 USB-A）',
    Math.abs(usbS[0] - 6.65) < 0.4 && Math.abs(usbS[1] - 2.4) < 0.2 && Math.abs(usbS[2] - 8.3) < 0.4,
    JSON.stringify({ usbB, usbS }));
  const usbStart = pPower.bounds && pPower.bounds.power;
  ok('P2c Type-C 沿 +X 从板外插入', cx(usbStart) > cx(usbB) + 1,
    JSON.stringify({ start: usbStart, end: usbB }));
  const plugged = await cdp.state();
  ok('S6b TF/电源/网线都插上', plugged.plugs.tfcard && plugged.plugs.power && plugged.plugs.lan, JSON.stringify(plugged.plugs));

  await expectStep(cdp, 'screen-front', 'S6d 接电后最后翻屏');
  const sc0 = await cdp.eval(`({ pose: window.NKBOARD3D.screenPose(), dbg: window.NKView3D.screenDebug() })`);
  const camScreen0 = await cdp.eval(`window.NKView3D.debug().cam`);
  ok('S6d1 接电后屏幕仍在背面且未亮', sc0.pose === 'north' && sc0.dbg.visible === false, JSON.stringify(sc0));
  ok('S6d1b 翻屏前进度为 12 / 13', (await cdp.eval(`document.getElementById('hud-count').textContent`)) === '12 / 13');
  const cue0 = await cdp.eval(`window.NKView3D.screenDebug()`);
  await wait(220);
  const cue1 = await cdp.eval(`window.NKView3D.screenDebug()`);
  ok('S6d1c 翻屏前整屏高亮并脉冲', cue0.cue === true && cue1.cue === true && cue0.cueParts > 0 &&
    cue0.cueGlow !== cue1.cueGlow, JSON.stringify({ a: cue0.cueGlow, b: cue1.cueGlow, parts: cue0.cueParts }));
  const scCenter = await cdp.eval(`window.NKView3D.screenCenter()`);
  const scPoint = await cdp.eval(`window.NKView3D.project(${scCenter[0]},${scCenter[1]},${scCenter[2]})`);
  const scPick = await cdp.eval(`window.NKView3D._pick(${scPoint.x},${scPoint.y})`);
  ok('S6d2 屏幕本体可点击（拾取面命中 screen:flip）', scPick && scPick.ref === 'screen:flip', JSON.stringify(scPick));
  await cdp.pick('screen:flip');
  const camAnimOn = await cdp.eval(`window.NKView3D.debug().cam.anim`);
  ok('S6e1b 翻屏镜头切换使用动画', camAnimOn === true);
  await cdp.waitFor(`window.NKBOARD3D.screenPose() === 'over' && Math.abs(window.NKBOARD3D.screenAngle() - 2 * Math.PI) < 0.02`, 20000);
  await cdp.waitFor(`!window.NKView3D.debug().cam.anim`, 4000);
  await cdp.waitFor(`window.NKView3D.screenDebug().visible`, 3000);
  const sc1 = await cdp.eval(`({ pose: window.NKBOARD3D.screenPose(), dbg: window.NKView3D.screenDebug() })`);
  const camScreen1 = await cdp.eval(`window.NKView3D.debug().cam`);
  ok('S6e 翻屏后点亮：OFF（断路）=LOCK 显示常规页', sc1.pose === 'over' && sc1.dbg.visible === true &&
    sc1.dbg.source === 'nokey', JSON.stringify(sc1));
  ok('S6e2 翻屏时镜头正对屏幕并纳入整板', JSON.stringify(camScreen0) !== JSON.stringify(camScreen1) &&
    Math.abs(camScreen1.pitch - 88) < 0.5 && Math.abs(camScreen1.target[2] - 124) < 0.5 &&
    Math.abs(camScreen1.dist - 92) < 0.5, JSON.stringify({ camScreen0, camScreen1 }));
  ok('S6e2b 屏幕是原生 240×240、只朝正面、参与深度遮挡、nearest 采样',
    Array.isArray(sc1.dbg.size) && sc1.dbg.size[0] === 240 && sc1.dbg.size[1] === 240 &&
    sc1.dbg.face === 0 && sc1.dbg.depthTest === true && sc1.dbg.nearest === true,
    JSON.stringify(sc1.dbg));
  ok('S6e2c 显示面贴在玻璃外表面且与玻璃同尺寸（无黑边）',
    sc1.dbg.displayBox && sc1.dbg.glassBox &&
    sc1.dbg.displayBox.min[1] > sc1.dbg.glassBox.max[1] &&
    Math.abs((sc1.dbg.displayBox.max[0] - sc1.dbg.displayBox.min[0]) -
      (sc1.dbg.glassBox.max[0] - sc1.dbg.glassBox.min[0])) < 0.05 &&
    Math.abs((sc1.dbg.displayBox.max[2] - sc1.dbg.displayBox.min[2]) -
      (sc1.dbg.glassBox.max[2] - sc1.dbg.glassBox.min[2])) < 0.05,
    JSON.stringify({ display: sc1.dbg.displayBox, glass: sc1.dbg.glassBox }));
  await wait(820);
  const flipDone = await cdp.eval(`(() => {
    const c = document.getElementById('fx-canvas');
    const d = c.getContext('2d').getImageData(0, 0, c.width, c.height).data;
    let n = 0;
    for (let i = 3; i < d.length; i += 16) if (d[i] > 0) n++;
    return { hud: document.getElementById('hud-count').textContent, fx: n,
      cue: window.NKView3D.screenDebug().cue,
      flash: document.getElementById('scene').classList.contains('fx-celebrate'),
      fxZ: +getComputedStyle(c).zIndex, doneZ: +getComputedStyle(document.getElementById('complete')).zIndex };
  })()`);
  ok('S6e2f 翻屏完成即 13 / 13、关闭高亮并播放庆祝', flipDone.hud === '13 / 13' &&
    flipDone.cue === false && flipDone.fx > 200 && flipDone.fxZ > flipDone.doneZ, JSON.stringify(flipDone));
  const screenImg = await cdp.eval(`({ main: !!(window.NK_SCREEN_IMG && /^data:image\\/png;base64,/.test(window.NK_SCREEN_IMG.main)), locked: !!(window.NK_SCREEN_IMG && /^data:image\\/png;base64,/.test(window.NK_SCREEN_IMG.locked)), en: !!(window.NK_SCREEN_IMG && /^data:image\\/png;base64,/.test(window.NK_SCREEN_IMG.mainEn)), admin: !!(window.NK_SCREEN_IMG && /^data:image\\/png;base64,/.test(window.NK_SCREEN_IMG.admin)), adminEn: !!(window.NK_SCREEN_IMG && /^data:image\\/png;base64,/.test(window.NK_SCREEN_IMG.adminEn)), nokey: !!(window.NK_SCREEN_IMG && /^data:image\\/png;base64,/.test(window.NK_SCREEN_IMG.nokey)), nokeyEn: !!(window.NK_SCREEN_IMG && /^data:image\\/png;base64,/.test(window.NK_SCREEN_IMG.nokeyEn)) })`);
  ok('S6e2d 屏幕贴图全部内嵌（file:// 下也能显示）', Object.values(screenImg).every(Boolean), JSON.stringify(screenImg));
  const texZh = await cdp.eval(`window.NKView3D.screenDebug().wasmPage`);
  await cdp.click('#lang-toggle');
  await cdp.waitFor(`window.NKView3D.screenDebug().lang === 'en'`, 3000);
  const texEn = await cdp.eval(`window.NKView3D.screenDebug()`);
  ok('S6e2e 屏幕语言跟随游戏语言（切换对应页面资源）',
    texEn.lang === 'en' && texEn.source === 'nokey' && texEn.wasmPage !== texZh && /:en$/.test(texEn.wasmPage),
    JSON.stringify({ before: texZh, after: texEn.wasmPage, lang: texEn.lang }));
  await cdp.click('#lang-toggle');
  await cdp.waitFor(`window.NKView3D.screenDebug().lang === 'zh'`, 3000);
  const doneBtn = await cdp.eval(`!!document.getElementById('screen-done')`);
  ok('S6e3 翻屏后不自动结束，出现完成按钮', doneBtn === true);
  await wait(180);
  const anno = await cdp.eval(`(() => {
    const layer = document.getElementById('anno-layer');
    const items = [...layer.querySelectorAll('.anno')].map((n) => {
      const dot = n.querySelector('.anno-dot'), line = n.querySelector('.anno-line'), box = n.querySelector('.anno-box');
      const d = dot.getBoundingClientRect(), b = box.getBoundingClientRect();
      return { text: box.textContent, line: parseFloat(line.style.width) || 0,
        dist: Math.hypot((b.left + b.right) / 2 - (d.left + d.right) / 2, (b.top + b.bottom) / 2 - (d.top + d.bottom) / 2) };
    });
    return { hidden: layer.hidden, items };
  })()`);
  ok('S6e3a 最后一步后 USR/开关常驻注记（虚线引出、不压按钮）',
    anno.hidden === false && anno.items.length === 2 &&
    anno.items.every((x) => x.line > 60 && x.dist > 60 && x.text.length > 2),
    JSON.stringify(anno));
  await cdp.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: 1120, y: 420, button: 'left', buttons: 1, clickCount: 1 });
  await cdp.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 1180, y: 470, button: 'left', buttons: 1 });
  await wait(80);
  const annoTrack = await cdp.eval(`(() => {
    const a = window.NKBOARD3D.annotationAnchors().usr;
    const p = window.NKView3D.project(a[0], a[1], a[2]);
    const d = document.querySelector('#anno-layer [data-k="usr"] .anno-dot').getBoundingClientRect();
    return { p, dot: { x: (d.left + d.right) / 2, y: (d.top + d.bottom) / 2 } };
  })()`);
  await cdp.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: 1180, y: 470, button: 'left', buttons: 0, clickCount: 1 });
  ok('S6e3b 拖拽视角时注记同帧跟随锚点',
    Math.abs(annoTrack.p.x - annoTrack.dot.x) < 2 && Math.abs(annoTrack.p.y - annoTrack.dot.y) < 2,
    JSON.stringify(annoTrack));
  const wdFlip = await cdp.eval(`window.NKView3D.wireDebug()`);
  const rotatedCaps = (wdFlip.connRots || []).filter((r) => Math.abs(r - Math.PI) < 0.05).length;
  ok('S6e7 翻面后 8 个屏端母头跟着旋转 180°', rotatedCaps === 8, JSON.stringify(wdFlip.connRots));
  const screenCaps = (wdFlip.connPositions || []).filter((p, i) => Math.abs((wdFlip.connRots || [])[i] - Math.PI) < 0.05);
  ok('S6e8 翻面后屏端母头落在自由针尖上（不是屏幕侧的高位）', screenCaps.length === 8 &&
    screenCaps.every((p) => p[1] > 15 && p[1] < 25), JSON.stringify(screenCaps));

  await cdp.pick('btn:usr');
  const offScreen = await cdp.eval(`window.NKView3D.screenDebug()`);
  const doneOff = await cdp.eval(`document.getElementById('screen-done').disabled`);
  ok('S6e4 USR 是熄屏按钮', offScreen.visible === false && doneOff === true, JSON.stringify({ offScreen, doneOff }));
  await cdp.pick('btn:usr');
  const onScreen = await cdp.eval(`window.NKView3D.screenDebug()`);
  const doneOn = await cdp.eval(`document.getElementById('screen-done').disabled`);
  ok('S6e5 USR 再按亮屏、完成按钮恢复', onScreen.visible === true && doneOn === false, JSON.stringify({ onScreen, doneOn }));
  await cdp.pick('sw:toggle');
  const scAdmin = await cdp.eval(`({ st: window.__nkState(), dbg: window.NKView3D.screenDebug() })`);
  ok('S6e6 ON（闭合）=ADMIN 显示 IP 页',
    scAdmin.st.sw === true && scAdmin.dbg.source === 'admin', JSON.stringify(scAdmin));
  await cdp.pick('sw:toggle');
  const scNoKey = await cdp.eval(`window.NKView3D.screenDebug()`);
  ok('S6e6d OFF（断路）=LOCK 显示常规页', scNoKey.source === 'nokey', JSON.stringify(scNoKey));

  await cdp.click('#screen-done');
  await cdp.waitFor(`!document.getElementById('complete').hidden`, 5000);

  const done = await cdp.eval(`({
    complete: !document.getElementById('complete').hidden,
    saved: JSON.parse(localStorage.getItem('nokeyout-diy-build-v2') || '{}').idx
  })`);
  ok('S6f 完成面板出现', done.complete);
  ok('S6f1 完成面板进度仍为 13 / 13', (await cdp.eval(`document.getElementById('hud-count').textContent`)) === '13 / 13');
  const noShare = await cdp.eval(`({ badge: !!document.getElementById('badge'),
    dl: !!document.getElementById('dl-badge'), copy: !!document.getElementById('copy-share') })`);
  ok('S6g 最终界面不含徽章/分享/下载 PNG', !noShare.badge && !noShare.dl && !noShare.copy,
    JSON.stringify(noShare));
  ok('S6h 进度写入 localStorage', done.saved === 14, 'idx=' + done.saved);

  await cdp.goto(siteUrl + '?r=2');
  await cdp.waitFor(`window.__nkStep && window.__nkStep()`);
  await cdp.waitFor(`!document.getElementById('complete').hidden`);
  ok('S6i 重载后恢复到完成状态', (await cdp.state()).idx === 14);
  await cdp.waitFor(`window.NKView3D && window.NKView3D.switchVisual()`, 30000);
  const swAfter = await cdp.eval(`window.NKView3D.switchVisual()`);
  ok('S6j 重载后开关模型仍在 ON 位（LOCK）', swAfter && swAfter.on === true, JSON.stringify(swAfter));
  await cdp.waitFor(`window.NKView3D.screenDebug && window.NKView3D.screenDebug().visible`, 30000);
  const scAfter = await cdp.eval(`({ pose: window.NKBOARD3D.screenPose(), dbg: window.NKView3D.screenDebug() })`);
  ok('S6k 重载后屏幕仍在正面且点亮', scAfter.pose === 'over' && scAfter.dbg.visible === true, JSON.stringify(scAfter));

  ok('S7 全程零 pageerror', cdp.pageerrors.length === 0, cdp.pageerrors.slice(0, 3).join(' | '));
} catch (e) {
  const errs = cdp && cdp.pageerrors.length ? ' | pageerrors: ' + cdp.pageerrors.slice(0, 3).join(' || ') : '';
  FAIL.push('EXCEPTION — ' + (e && e.stack ? e.stack.split('\n')[0] : e) + errs);
} finally {
  log('\n=== NoKeyOut scene smoke ===');
  PASS.forEach((p) => log('  ✓ ' + p));
  FAIL.forEach((f) => log('  ✗ ' + f));
  log(`\n${PASS.length} passed, ${FAIL.length} failed`);
  child.kill();
  srv.close();
  process.exit(FAIL.length ? 1 : 0);
}
