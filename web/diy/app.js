'use strict';
/* NoKeyOut 装机演练 — 单场景引擎（vanilla JS，无构建无后端）
 *
 * 一个连续流程：页面 = 3D 场景，所有任务/读数/进度以 HUD 浮在画布上。
 * 没有采购页、没有关卡图：步骤按顺序解锁，只有当前步骤可操作。
 * 数据仍来自 levels/*.js（纯内容），这里的 PLAN 把它们编排成一条步骤流。
 * 3D 渲染在 view3d.js / board3d.js（懒加载）；不存在 2D 工位。 */

/* ============ i18n ============ */
const I18N = {
  zh: {

    'hud.steps': '步骤',
    'hud.proc': '采购清单',
    'sfx.mute': '音效开（点击静音）', 'sfx.unmute': '音效关（点击开启）',
    'hud.reset': '重置', 'parts.total': '合计', 'hud.back': '← 上一步', 'back.done': '已返回上一步',
    'tut.wire': '分别点击元件进行连接',
    'cam.reset': '重置视角', 'cam.top': '俯视',
    'loading.scene': '正在载入 3D 场景…',
    'loading.meshes': '正在解析模型…',
    'loading.ready': '准备完成',
    'loading.enter': '开始装配',
    'loading.tip': '进入后按顶部步骤操作',
    'load.file': '3D 场景需要本地服务：file:// 下浏览器不让读网格文件。在 site/ 目录运行 python -m http.server 8080，再打开 http://localhost:8080/',
    'load.fail': '3D 场景加载失败：',
    'switch.on': '开关 ON（ADMIN）', 'switch.off': '开关 OFF（LOCK）',
    'switch.flip': '开关已拨到 {s}——读数会跟着变化。',
    'g.gate': '门控接线', 'g.screen': '屏幕接线',
    'g.plug': '上电与接线', 'g.final': '点亮', 'g.done': '完成',
    'wiring.guide': '点两个高亮针脚',
    'wiring.selected': '已选：', 'wiring.cancel': '再点一次取消',
    'wiring.wrongstep': '这一根属于后面的步骤，先完成当前步骤（顺序即安全）。',
    'wiring.fail': '[X] 判据失败', 'wiring.attempts': '尝试次数', 'wiring.done': '[OK] 已接',
    'plug.guide': '点对应插口。',
    'tf.kicker': '在线获取脚本与镜像',
    'tf.skip': '跳过烧录', 'tf.copy': '复制命令', 'tf.copied': '[OK] 已复制', 'tf.copyFail': '复制失败，请手动选择命令',
    'tf.skipToast': '已跳过烧录，直接点卡槽插卡',
    'tf.boardNote': '烧录脚本基于 NanoPi NEO3 Plus 编写；其他主板请直接点击“跳过烧录”。',
    'tf.termDone': '写卡完成：点 × 关闭，再点卡槽插卡；首次启动用卡内离线包安装。',
    'tf.cmdHint': '命令已带 --board neo3plus；选盘、输入 YES、输两次密码。首次启动离线安装卡内版本。',
    'tf.windows': 'Windows：以管理员运行 install-tf.ps1 -Board neo3plus（需要 WSL2）。',
    'tf.param.fetch': '用 curl 取 install-tf.sh 到 /tmp',
    'tf.param.board': 'neo3plus：按板型解析 Armbian 镜像',
    'tf.param.resolve': '只解析镜像，不写卡',
    'tf.param.nodeploy': '只写 Armbian，不注入 firstboot',
    'tf.param.more': '账号/主机名固定 nokeyout；选盘、YES 和密码都必须手动输入。',
    'tf.firstFlash': '未烧录：点 TF 卡跑完烧录',
    'tf.flashHint': '未烧录：点 TF 卡打开烧录终端',
    'tf.insertHint': '[OK] 烧录完成：点卡槽插卡',
    'tf.ready': '[OK] 烧录完成',
    'check.guide': '安全项：确认无误再继续。',
    'screen.guideOff': '点击屏幕本体，把它翻到正面。',
    'screen.guideOn': '屏幕已点亮；ON=ADMIN（显示产品标识、面板地址与开放计时，超时后边框告警），OFF=LOCK（常规面板）。端口开放时屏幕常亮，拨回 LOCK 后可用 USR 熄屏/亮屏。',
    'screen.first': '先把屏幕翻到正面。',
    'screen.done': '完成', 'screen.flipped': '[OK] 屏幕已翻到正面', 'screen.lit': '[OK] 接电完成，屏幕启动',
    'screen.adminOn': 'ADMIN 模式不允许熄屏',
    'screen.adminKeep': '端口开放：屏幕保持常亮。',
    'anno.usr': 'USR · 熄屏 / 亮屏（仅 LOCK）', 'anno.switch': '开关 · ADMIN ↔ LOCK',
    'action.confirm': '确认', 'action.submit': '提交',
    'gate.pass': '[OK] 判据通过', 'gate.fail': '[X] 与判据不符',
    'step.locked': '未解锁',
    'done.title': '这台 NoKeyOut 已按步骤装好',
    'done.scene': '回到场景', 'done.replay': '重玩', 'done.back': '返回',
    'reset.confirm': '重置全部进度？已接的线和已通过的步骤都会清空。',
    'step.notyet': '先完成当前步骤。',
    'need.scene': '这一步要在 3D 里操作。',
    'reset.done': '已重置。'
  },
  en: {

    'hud.steps': 'Steps',
    'hud.proc': 'Parts list',
    'sfx.mute': 'Sound on (click to mute)', 'sfx.unmute': 'Sound off (click to unmute)',
    'hud.reset': 'Reset', 'parts.total': 'Total', 'hud.back': '← Back', 'back.done': 'Back one step',
    'tut.wire': 'Click each part to connect',
    'cam.reset': 'Reset view', 'cam.top': 'Top',
    'loading.scene': 'Loading the 3D scene…',
    'loading.meshes': 'Parsing models…',
    'loading.ready': 'Ready',
    'loading.enter': 'Start assembly',
    'loading.tip': 'Follow the steps at the top once inside',
    'load.file': 'The 3D scene needs a local server: file:// blocks mesh reads. Run python -m http.server 8080 in site/ and open http://localhost:8080/',
    'load.fail': '3D scene failed to load: ',
    'switch.on': 'Switch ON (ADMIN)', 'switch.off': 'Switch OFF (LOCK)',
    'switch.flip': 'Switch is now {s} — readings follow it.',
    'g.gate': 'Gate wiring', 'g.screen': 'Screen wiring',
    'g.plug': 'Power & cabling', 'g.final': 'Power-on', 'g.done': 'Done',
    'wiring.guide': 'Click the two highlighted pins',
    'wiring.selected': 'Selected: ', 'wiring.cancel': 'click again to cancel',
    'wiring.wrongstep': 'That wire belongs to a later step — finish the current one first (order is safety).',
    'wiring.fail': '[X] Criterion failed', 'wiring.attempts': 'attempts', 'wiring.done': '[OK] Connected',
    'plug.guide': 'Click the matching jack.',
    'tf.kicker': 'Fetch script & image online',
    'tf.skip': 'Skip flashing', 'tf.copy': 'Copy command', 'tf.copied': '[OK] Copied', 'tf.copyFail': 'Copy failed — select the command manually',
    'tf.skipToast': 'Flashing skipped; click the slot to insert the card',
    'tf.boardNote': 'The flashing script is written for the NanoPi NEO3 Plus. For other boards, click “Skip flashing”.',
    'tf.termDone': 'Flash complete: click × to close, then insert the card; first boot installs the bundled release offline.',
    'tf.cmdHint': 'The command uses --board neo3plus; select the disk, type YES, enter the password twice. First boot installs the bundled release offline.',
    'tf.windows': 'Windows: run install-tf.ps1 -Board neo3plus as Administrator (WSL2 required).',
    'tf.param.fetch': 'Fetch install-tf.sh to /tmp with curl',
    'tf.param.board': 'neo3plus: resolves the Armbian image',
    'tf.param.resolve': 'Resolve the image only; do not write',
    'tf.param.nodeploy': 'Write Armbian only; skip firstboot injection',
    'tf.param.more': 'User/hostname are fixed to nokeyout; disk choice, YES and password are manual.',
    'tf.firstFlash': 'This card is not flashed yet: click the TF card and finish flashing first.',
    'tf.flashHint': 'Not flashed yet: click the TF card in the scene to open the flashing terminal.',
    'tf.insertHint': '[OK] Flashed — now click the board’s TF slot to insert the card.',
    'tf.ready': '[OK] Flash complete',
    'check.guide': 'Safety item: confirm before continuing.',
    'screen.guideOff': 'Click the screen itself to flip it to the front.',
    'screen.guideOn': 'Screen is lit; ON=ADMIN (product mark, panel URL and open timer, with an alert border after 15 min), OFF=LOCK (normal panel). The screen stays on while ports are open; USR works after flipping back to LOCK.',
    'screen.first': 'Flip the screen to the front first.',
    'screen.done': 'Done', 'screen.flipped': '[OK] Screen flipped', 'screen.lit': '[OK] Powered on; screen booted',
    'screen.adminOn': 'Screen stays on in ADMIN mode',
    'screen.adminKeep': 'Ports open: the screen stays on.',
    'anno.usr': 'USR · screen off / on (LOCK only)', 'anno.switch': 'Switch · ADMIN ↔ LOCK',
    'action.confirm': 'Confirm', 'action.submit': 'Submit',
    'gate.pass': '[OK] Passed', 'gate.fail': '[X] Does not match the criterion',
    'step.locked': 'Locked',
    'done.title': 'This NoKeyOut is built, step by step',
    'done.scene': 'Back to scene', 'done.replay': 'Replay', 'done.back': 'Back',
    'reset.confirm': 'Reset all progress? Wires and passed steps will be cleared.',
    'step.notyet': 'Finish the current step before touching other parts of the scene.',
    'need.scene': 'This step is done in the scene.',
    'reset.done': 'Reset.'
  }
};
function detectLang() {
  try {
    const q = new URLSearchParams(location.search).get('lang');
    if (q === 'zh' || q === 'en') return q;
  } catch (e) { /* ignore */ }
  try {
    const saved = localStorage.getItem('nokeyout-lang');
    if (saved === 'zh' || saved === 'en') return saved;
  } catch (e) { /* ignore */ }
  const langs = (navigator.languages && navigator.languages.length)
    ? navigator.languages : [navigator.language || ''];
  for (const l of langs) {
    if (/^zh\b|^zh-/i.test(l)) return 'zh';
    if (/^en\b|^en-/i.test(l)) return 'en';
  }
  return 'en';
}
let LANG = detectLang();
const t = (k) => (I18N[LANG] && I18N[LANG][k]) ?? I18N.zh[k] ?? k;
const tx = (v) => (v && typeof v === 'object' && ('zh' in v || 'en' in v)) ? (v[LANG] ?? v.zh) : v;
const fmt = (s, vars) => String(s).replace(/\{(\w+)\}/g, (_, k) => (vars && vars[k] != null ? vars[k] : ''));
const sfx = (n) => { try { if (window.NKSFX) window.NKSFX.play(n); } catch (e) { /* ignore */ } };

/* ============ 数据登记 ============ */
const LEVELS = [];
window.registerLevel = (l) => LEVELS.push(l);
const SAVE_KEY = 'nokeyout-diy-build-v2';

const GROUP_LABELS = {
  gate: 'g.gate', screen: 'g.screen',
  plug: 'g.plug', final: 'g.final', done: 'g.done'
};

const PROCUREMENT = [
  { n: { zh: 'NanoPi NEO3 Plus', en: 'NanoPi NEO3 Plus' }, qty: 1, price: 179, spec: { zh: 'RK3528A · 1GB · 千兆网口 · eMMC/microSD · 26-pin 2.54mm 排针', en: 'RK3528A · 1GB · GbE · eMMC/microSD · 26-pin 2.54mm header' } },
  { n: { zh: '拨动开关', en: 'Slide switch' }, qty: 1, price: 0.8, spec: { zh: '锁存 2 档', en: '2-position latching' } },
  { n: { zh: '杜邦线', en: 'Jumper wires' }, qty: 10, price: 1.05, spec: { zh: '母-母', en: 'female-female' } },
  { n: { zh: 'ST7789 屏模块', en: 'ST7789 display' }, qty: 1, price: 24.7, spec: { zh: '1.3 或 1.54 英寸 · 240×240 · SPI · 排针已焊', en: '1.3 or 1.54 inch · 240×240 · SPI · header pre-soldered' } },
  { n: { zh: 'USB-C 电源', en: 'USB-C power' }, qty: 1, price: 32.8, spec: { zh: '5V / 3A 及以上', en: '5V / 3A or more' } },
  { n: { zh: 'TF 卡', en: 'TF card' }, qty: 1, price: 86.9, spec: { zh: '≥16GB · Class 10 及以上', en: '≥16GB · Class 10 or faster' } },
  { n: { zh: 'USB 读卡器', en: 'USB card reader' }, qty: 1, spec: { zh: 'USB-A 或 USB-C 接口', en: 'USB-A or USB-C' } },
  { n: { zh: '网线', en: 'Ethernet cable' }, qty: 1, spec: { zh: '百兆及以上（Cat5 或更好）', en: '100M or faster (Cat5 or better)' } },
];

/* 价格：人民币标价，美元按固定汇率估算（只做展示，不参与计算精度） */
const USD_PER_CNY = 0.15;
function fmtCny(v) {
  const r = Math.round(v * 100) / 100;
  return Number.isInteger(r) ? String(r) : r.toFixed(2);
}
function usdOf(cny) {
  return Math.round(cny * USD_PER_CNY * 100 + 1e-6) / 100;   // 四舍五入到分
}
function fmtUsd(cny) {
  return usdOf(cny).toFixed(2);
}
function fmtMoney(cny) {
  return '¥' + fmtCny(cny) + '/$' + fmtUsd(cny);
}
function renderLoadParts() {
  const box = document.getElementById('load-parts');
  const ul = document.getElementById('lp-list');
  if (!box || !ul) return;
  ul.innerHTML = PROCUREMENT.map((it) => {
    const name = document.createElement('span');
    name.className = 'lp-name';
    name.textContent = tx(it.n);
    const qty = document.createElement('span');
    qty.className = 'lp-qty';
    qty.textContent = '×' + it.qty;
    const price = document.createElement('span');
    price.className = it.price == null ? 'lp-price na' : 'lp-price';
    price.textContent = it.price == null ? '—' : fmtMoney(it.price);
    const li = document.createElement('li');
    li.append(name, qty, price);
    return li.outerHTML;
  }).join('');
  const totalCny = PROCUREMENT.reduce((sum, it) => sum + (it.price == null ? 0 : it.price), 0);
  const totalUsd = PROCUREMENT.reduce((sum, it) => sum + (it.price == null ? 0 : usdOf(it.price)), 0);
  const totalEl = document.getElementById('lp-total');
  if (totalEl) totalEl.textContent = '¥' + fmtCny(totalCny) + '/$' + totalUsd.toFixed(2);
  const rate = document.getElementById('lp-rate');
  if (rate) rate.textContent = '¥1 = $' + USD_PER_CNY;
}

function renderProcurement() {
  const ul = document.getElementById('proc-list');
  if (!ul) return;
  ul.innerHTML = '';
  for (const item of PROCUREMENT) {
    const li = document.createElement('li');
    const name = document.createElement('b');
    name.textContent = tx(item.n);
    const qty = document.createElement('span');
    qty.className = 'qty';
    qty.textContent = '× ' + tx(item.qty);
    const spec = document.createElement('span');
    spec.className = 'spec';
    spec.textContent = tx(item.spec);
    if (item.price != null) {
      const price = document.createElement('span');
      price.className = 'price';
      price.textContent = fmtMoney(item.price);
      li.append(name, qty, spec, price);
    } else {
      li.append(name, qty, spec);
    }
    ul.appendChild(li);
  }
}

let G1 = null;
let PLAN = [], S = null;
let ADMIN_AT = 0; /* 端口开放计时起点（Date.now()；0=未开放） */
let sceneLoading = null, sceneMounted = false, sceneEntered = false, toastTimer = 0;

/* ============ 启动 ============ */
document.addEventListener('DOMContentLoaded', () => {
  document.body.classList.add('gate');
  const find = (id) => LEVELS.find((l) => l.id === id) || null;
  G1 = find('level-1-switch');
  if (!G1) throw new Error('missing level-1-switch data');
  buildPlan();
  bindChrome();
  S = loadState();
  ADMIN_AT = S.sw ? Date.now() : 0;
  applyLangChrome();
  render();
  bootScene();
});

function current() { return PLAN[S.idx] || null; }

/* ============ 一条步骤流（不是关卡） ============ */
function buildPlan() {
  const plan = [];
  const add = (o) => plan.push(o);
  const addWire = (i, group) => {
    const st = G1.wiring.steps[i];
    if (!st) return;
    if (st.plug) {
      add({ type: 'plug', id: 'plug:' + st.plug.id, wid: i, plug: st.plug, step: st, level: G1, group });
    } else {
      add({ type: 'wire', id: 'w' + i, wid: i, edge: st.edge, step: st, level: G1, group });
    }
  };
  /* 接线：开关端已焊，板/屏端用杜邦 */
  addWire(0, 'gate');                      // sw:C → pin11
  addWire(1, 'gate');                      // sw:B → pin9

  for (let i = 2; i <= 9; i++) addWire(i, 'screen');
  addWire(10, 'plug');                     // TF 卡
  plan[plan.length - 1].detail = {
    zh: '卡先不插：点 TF 卡打开烧录终端，跑完在线命令；输入两次密码，动画走完再点卡槽插卡。',
    en: 'Do not insert it yet: click the TF card to open the terminal, run the single online command, enter the password twice, then click the slot to insert the card after flashing.'
  };
  addWire(12, 'plug');                     // 网线
  addWire(11, 'plug');                     // USB-C 上电（普通步骤）
  plan[plan.length - 1].detail = {
    zh: '先接入 USB-C 供电，再翻屏幕。',
    en: 'Connect USB-C power first, then flip the screen.'
  };
  add({
    type: 'screen', id: 'screen-front', group: 'final', level: G1, focus: [],
    label: { zh: '屏幕翻至正面并点亮', en: 'Flip the screen to the front and light it up' },
    observe: { zh: '屏幕已翻到正面，显示 panel-go 真实主页面（池用量/流量）。', en: 'Screen is front-up, showing the real panel-go main page (pool usage / traffic).' }
  });
  add({ type: 'complete', id: 'complete', level: G1, group: 'done' });
  PLAN = plan;
}

/* ============ 状态 / 存档 ============ */
const TF_DEFAULT_USER = 'nokeyout';
function newState() {
  return { idx: 0, wires: [], plugs: {}, sw: false, screenFlipped: false, screenOn: false, screenLit: false, pick: null, fails: {}, completeDismissed: false, tf: { flashed: false } }; // sw=true = ON = ADMIN；默认 false（OFF=LOCK，设备静止态）
}
function applySideEffect(s, c) {
  if (!c) return;
  if (c.type === 'wire' && c.edge) s.wires.push([c.edge[0], c.edge[1]]);
  else if (c.type === 'plug' && c.plug) {
    s.plugs[c.plug.id] = true;
    if (c.plug.id === 'tfcard') s.tf.flashed = true;
    if (c.plug.id === 'power') { s.screenOn = true; s.screenLit = true; }
  }
  else if (c.type === 'screen') { s.screenFlipped = true; }
}
function loadState() {
  let raw = null;
  try { raw = JSON.parse(localStorage.getItem(SAVE_KEY) || 'null'); } catch (e) { raw = null; }
  const s = newState();
  if (!raw) return s;
  s.sw = !!raw.sw;
  const idx = Math.max(0, Math.min(PLAN.length - 1, parseInt(raw.idx, 10) || 0));
  for (let i = 0; i < idx; i++) applySideEffect(s, PLAN[i]);
  s.idx = idx;
  s.screenFlipped = !!raw.screenFlipped || s.screenFlipped;
  s.screenOn = !!raw.screenOn || s.screenOn;
  s.screenLit = raw.screenLit != null ? !!raw.screenLit : s.screenLit;
  if (s.sw && s.screenOn) s.screenLit = true; /* 端口开放不允许熄屏 */
  s.completeDismissed = !!raw.completeDismissed;
  s.tf.flashed = !!raw.tfFlashed || s.tf.flashed;
  return s;
}
function saveState() {
  try {
    localStorage.setItem(SAVE_KEY, JSON.stringify({
      idx: S.idx, sw: S.sw, screenFlipped: !!S.screenFlipped, screenOn: !!S.screenOn, screenLit: !!S.screenLit, completeDismissed: !!S.completeDismissed, tfFlashed: !!S.tf.flashed, at: new Date().toISOString()
    }));
  } catch (e) { /* private mode */ }
}

/* ============ 基础查询 ============ */
function isEndpoint(id) { return /^(pin:\d+|scr:[A-Z]+|sw:[ABC])$/.test(id || ''); }
function norm(id) {
  if (typeof id === 'string' && id.startsWith('pin:')) {
    const n = +id.slice(4);
    if ([6, 9, 14, 20, 25].indexOf(n) >= 0) return 'GND';
    if ([1, 17].indexOf(n) >= 0) return '3V3';
  }
  return id;
}
const pair = (a, b) => [norm(a), norm(b)].sort().join('|');

function epName(id, level) {
  if (id === 'GND') return tx({ zh: 'GND（pin6/9/14/20/25）', en: 'GND (pin6/9/14/20/25)' });
  if (id === '3V3') return tx({ zh: '3V3（pin1/17）', en: '3V3 (pin1/17)' });
  if (typeof id !== 'string') return String(id);
  const lv = (level && level.wiring) ? level : G1;
  if (id.startsWith('pin:')) {
    const n = +id.slice(4);
    const p = (lv.wiring.pins || []).find((x) => x.n === n);
    return 'pin' + n + (p && p.label ? '（' + p.label + '）' : '');
  }
  if (id.startsWith('sw:')) {
    return (LANG === 'zh' ? '开关 ' : 'Switch ') + id.slice(3);
  }
  const [cid, lead] = id.split(':');
  const c = (lv.wiring.components || []).find((x) => x.id === cid);
  if (cid === 'scr') return (LANG === 'zh' ? '屏 ' : 'Screen ') + lead;
  if (cid === 'sw') return (LANG === 'zh' ? '开关 ' : 'Switch ') + lead;
  return (c ? tx(c.name) : cid) + ' · ' + lead;
}

/* ============ 渲染 ============ */
function render() {
  if (!S) return;
  const c = current();
  if (TF && TF.open) {
    const stillTf = c && c.type === 'plug' && c.plug.id === 'tfcard' && !S.plugs.tfcard;
    if (!stillTf) closeTfTerminal();
  }
  const total = Math.max(1, PLAN.length - 2);
  const done = S.screenFlipped ? total : Math.min(S.idx, total - 1);
  document.getElementById('hud-count').textContent = `${done} / ${total}`;
  renderProgress(done, total);
  updateBackUI();
  renderStepList();

  if (!c) return;
  syncAnnotations(c);
  if (c.type === 'complete' && !S.completeDismissed) {
    syncScene(c);
    renderComplete();
    document.getElementById('step-card').hidden = true;
    return;
  }
  document.getElementById('step-card').hidden = false;
  document.getElementById('complete').hidden = true;
  renderCard(c);
  syncScene(c);
}

function renderCard(c) {
  document.getElementById('step-group').textContent = t(GROUP_LABELS[c.group] || c.group);
  document.getElementById('step-title').textContent = stepTitle(c);
  document.getElementById('step-body').innerHTML = stepBody(c);
  document.getElementById('step-controls').innerHTML = '';
  const fb = document.getElementById('step-feedback');
  fb.hidden = true;
  fb.className = '';

  if (c.type === 'check') renderCheck(c);
  else if (c.type === 'complete') renderCompletionCard(c);
  else if (c.type === 'screen') {
    const row = document.createElement('div');
    row.className = 'row';
    if (S.screenFlipped) {
      const b = document.createElement('button');
      b.className = 'btn primary';
      b.id = 'screen-done';
      b.textContent = t('screen.done');
      b.disabled = !S.screenLit;
      b.addEventListener('click', () => nextStep(t('screen.flipped')));
      row.appendChild(b);
    }
    document.getElementById('step-controls').appendChild(row);
  }
  else if (c.type === 'plug') {
    if (c.plug.id === 'tfcard' && !S.tf.flashed) {
      const row = document.createElement('div');
      row.className = 'row';
      const b = document.createElement('button');
      b.className = 'btn primary';
      b.id = 'tf-skip-card';
      b.textContent = t('tf.skip');
      b.addEventListener('click', skipTfTerminal);
      row.appendChild(b);
      document.getElementById('step-controls').appendChild(row);
    }
  }
  else if (c.type === 'wire') {
    const row = document.createElement('div');
    row.className = 'row';
    row.id = 'wire-status';
    document.getElementById('step-controls').appendChild(row);
    refreshPickUI();
  }
}

function stepTitle(c) {
  if (!c) return '';
  if (c.type === 'wire') {
    const left = epName(c.edge[0], c.level);
    let right = epName(c.edge[1], c.level);
    /* 右端 pinN（信号）与左端信号同名时省略括注，避免"屏 DC → pin15（屏DC）"这种重复 */
    const m = right.match(/^pin(\d+)（(.+)）$/);
    if (m) {
      const norm = (v) => v.replace(/[\s★]/g, '').toLowerCase();
      const lead = left.split(' ').pop();
      if (norm(m[2]) === norm(left) || norm(m[2]).endsWith(norm(lead))) right = 'pin' + m[1];
    }
    return `${left} → ${right}`;
  }
  if (c.type === 'plug') return tx(c.plug.label);
  if (c.type === 'screen') return tx(c.label);
  if (c.type === 'check') return tx(c.item.prompt || c.item.text);
  if (c.type === 'complete') return t('done.title');
  return c.id;
}

function stepBody(c) {
  const modelHint = c.modelHint || (c.item && c.item.modelHint);
  const hint = modelHint ? `<p class="model-hint">${tx(modelHint)}</p>` : '';
  if (c.type === 'wire') return `<p>${t('wiring.guide')}</p>`;
  if (c.type === 'plug') {
    /* TF 卡这一步的首行必须是"烧录/插卡"，不能是通用的"点对应接口" */
    if (c.plug.id === 'tfcard') {
      if (S.tf.flashed) return `<p>${t('tf.insertHint')}</p>`;
      return `<p>${t('tf.flashHint')}</p>`
        + `<p class="tf-board-note">${t('tf.boardNote')}</p>`;
    }
    return `<p>${t('plug.guide')}</p>` + (c.detail ? `<p class="muted small">${tx(c.detail)}</p>` : '');
  }
  if (c.type === 'screen') return `<p>${t(S.screenFlipped ? 'screen.guideOn' : 'screen.guideOff')}</p>`;
  if (c.type === 'check') return `<p class="muted small">${t('check.guide')}</p>` + (c.item && c.item.detail ? `<p class="muted small">${tx(c.item.detail)}</p>` : '') + hint;
  return '';
}

function renderCompletionCard(c) {
  const bar = document.createElement('div');
  bar.className = 'btn-row';
  bar.innerHTML = `<button class="btn primary" id="card-replay">${t('done.replay')}</button>`;
  document.getElementById('step-controls').appendChild(bar);
  document.getElementById('card-replay').addEventListener('click', resetAll);
}

function renderProgress(done, total) {
  const track = document.getElementById('progress-track');
  if (!track || total < 1) return;
  if (track.children.length !== total) {
    track.innerHTML = Array.from({ length: total }, () => '<i aria-hidden="true"></i>').join('');
  }
  for (let i = 0; i < total; i++) {
    track.children[i].className = i < done ? 'on' : (i === done ? 'cur' : '');
  }
  track.setAttribute('aria-valuemax', String(total));
  track.setAttribute('aria-valuenow', String(done));
}

function jumpToStep(i) {
  if (!S || !PLAN.length) return;
  const target = Math.max(0, Math.min(PLAN.length - 2, Number(i) || 0));
  sfx('click');
  const next = newState();
  next.sw = S.sw;
  next.fails = S.fails;
  for (let j = 0; j < target; j++) applySideEffect(next, PLAN[j]);
  next.idx = target;
  S = next;
  document.getElementById('complete').hidden = true;
  document.getElementById('step-card').hidden = false;
  const drawer = document.getElementById('step-drawer');
  if (drawer) drawer.hidden = true;
  saveState();
  render();
  if (window.NKBOARD3D && sceneMounted) frameCurrentStep();
}

function renderStepList() {
  const list = document.getElementById('step-list');
  if (!list) return;
  const total = PLAN.length - 1;
  list.innerHTML = PLAN.slice(0, total).map((c, i) => {
    const cls = i < S.idx ? 'done' : i === S.idx ? 'on' : '';
    return `<li class="${cls}" data-step="${i}" role="button" tabindex="0"><span class="no">${String(i + 1).padStart(2, '0')}</span><span>${planLabel(c)}</span></li>`;
  }).join('');
  list.querySelectorAll('[data-step]').forEach((li) => {
    const go = () => jumpToStep(li.dataset.step);
    li.addEventListener('click', go);
    li.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); go(); }
    });
  });
}

function planLabel(c) {
  if (c.type === 'wire') return `${epName(c.edge[0], c.level)} → ${epName(c.edge[1], c.level)}`;
  if (c.type === 'plug') return tx(c.plug.label);
  if (c.type === 'screen') return tx(c.label);
  if (c.item) return tx(c.item.prompt || c.item.text);
  return c.id;
}

function updateSfxUI() {
  const b = document.getElementById('sfx-toggle');
  if (!b) return;
  const on = !window.NKSFX || window.NKSFX.enabled();
  b.setAttribute('aria-pressed', on ? 'true' : 'false');
  b.title = t(on ? 'sfx.mute' : 'sfx.unmute');
  b.setAttribute('aria-label', b.title);
}

function updateBackUI() {
  const b = document.getElementById('btn-back');
  if (!b) return;
  const off = !S || S.idx <= 0 || !!(TF && TF.open);
  b.disabled = off;
  b.textContent = t('hud.back');
  const nav = document.getElementById('step-nav');
  if (nav) nav.hidden = off;              // 第一步（和烧录终端打开时）不显示「上一步」
}

function goBack() {
  if (!S || S.idx <= 0 || (TF && TF.open)) return;
  sfx('click');
  const target = S.idx - 1;
  const keepFails = S.fails, keepSw = S.sw;
  const next = newState();
  next.sw = keepSw;
  next.fails = keepFails;
  /* TF 烧录状态也由步骤回放决定：退回到烧录之前，卡就回到"未烧录" */
  for (let i = 0; i < target; i++) applySideEffect(next, PLAN[i]);
  next.idx = target;
  S = next;
  document.getElementById('complete').hidden = true;
  document.getElementById('step-card').hidden = false;
  saveState();
  render();
  toast(t('back.done'), 'ok');
}

function toast(msg, kind) {
  const el = document.getElementById('toast');
  if (!el || !msg) return;
  el.textContent = msg;
  el.className = kind || '';
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.hidden = true; }, 3600);
}

function fail(c, msg) {
  sfx('error');
  if (window.NKFX) window.NKFX.shake();
  S.fails[c.id] = (S.fails[c.id] || 0) + 1;
  const fb = document.getElementById('step-feedback');
  fb.hidden = false;
  fb.className = 'bad';
  fb.innerHTML = '<b>' + t('gate.fail') + '</b><br>' + msg;
}

function clearFail() {
  const fb = document.getElementById('step-feedback');
  if (fb) { fb.hidden = true; fb.className = ''; }
}

function hintText(it, fails) {
  if (!it) return '';
  if (fails <= 1) return tx(it.failHint) || '';
  const idx = Math.min(it.hints ? it.hints.length : 0, fails >= 7 ? 3 : fails >= 5 ? 2 : 1) - 1;
  return tx(it.hints && it.hints[idx]) || tx(it.failHint) || '';
}

/* ---------- 判据类型：ack / number / select ---------- */
function renderCheck(c) {
  const it = c.item;
  const controls = document.getElementById('step-controls');
  if (it.kind === 'ack') {
    const row = document.createElement('div');
    row.className = 'row';
    row.innerHTML = `<button class="btn primary" id="step-submit">${t('action.confirm')}</button>`;
    controls.appendChild(row);
    row.querySelector('button').addEventListener('click', () => judgeCheck(c, true));
    return;
  }
  if (it.kind === 'number') {
    const row = document.createElement('div');
    row.className = 'row';
    row.innerHTML = `<input type="number" step="any" min="0" id="step-input" placeholder="${LANG === 'zh' ? '输入数值' : 'value'}">
      <button class="btn primary" id="step-submit">${t('action.submit')}</button>`;
    controls.appendChild(row);
    const submit = () => judgeCheck(c, parseFloat(document.getElementById('step-input').value));
    row.querySelector('button').addEventListener('click', submit);
    row.querySelector('input').addEventListener('keydown', (e) => { if (e.key === 'Enter') submit(); });
    return;
  }
  const sel = document.createElement('select');
  sel.id = 'step-select';
  sel.innerHTML = (it.options || []).map((o, i) => `<option value="${i}">${tx(o)}</option>`).join('');
  const row = document.createElement('div');
  row.className = 'row';
  row.appendChild(sel);
  const btn = document.createElement('button');
  btn.className = 'btn primary';
  btn.id = 'step-submit';
  btn.textContent = t('action.submit');
  btn.addEventListener('click', () => judgeCheck(c, parseInt(sel.value, 10)));
  row.appendChild(btn);
  controls.appendChild(row);
}

function judgeCheck(c, val) {
  const it = c.item;
  let ok = false;
  if (it.kind === 'ack') ok = val === true;
  else if (it.kind === 'number') ok = Number.isFinite(val) && val >= it.expect[0] && val <= it.expect[1];
  else ok = val === it.expect;
  if (ok) {
    clearFail();
    nextStep(t('gate.pass'));
  } else {
    fail(c, hintText(it, (S.fails[c.id] || 0) + 1));
  }
}

/* ---------- 接线 ---------- */
function refreshPickUI() {
  const c = current();
  const row = document.getElementById('wire-status');
  if (row) {
    row.innerHTML = S.pick
      ? `<span class="small muted">${t('wiring.selected')}<b class="target">${epName(S.pick, c && c.level)}</b> · ${t('wiring.cancel')}</span>`
      : '';
  }
  if (c && c.type === 'wire') {
    const ids = S.pick ? c.edge.concat([S.pick]) : c.edge;
    if (window.NKBOARD3D && sceneMounted) NKBOARD3D.setStepRefs(ids);
  }
}

function pickWire(id, c) {
  if (!S.pick) {
    S.pick = id;
    sfx('select');
    refreshPickUI();
    if (window.NKBOARD3D && sceneMounted && c.edge.indexOf(id) >= 0) {
      NKBOARD3D.previewJoint(id);
    }
    return;
  }
  if (S.pick === id) {
    S.pick = null;
    if (window.NKBOARD3D && sceneMounted) NKBOARD3D.clearPreview();
    refreshPickUI();
    return;
  }
  const a = S.pick, b = id;
  S.pick = null;
  refreshPickUI();
  if (pair(a, b) === pair(c.edge[0], c.edge[1])) {
    S.wires.push([a, b]);
    sfx('wire');
    clearFail();
    if (window.NKBOARD3D && sceneMounted) {
      NKBOARD3D.setWires(S.wires, { animatePair: [a, b], animateEndpoints: [b] });
    }
    nextStep(t('wiring.done'));
    if (window.NKBOARD3D && sceneMounted && b.startsWith('sw:')) NKBOARD3D.previewJoint(b, true);
    return;
  }
  if (window.NKBOARD3D && sceneMounted) NKBOARD3D.clearPreview();
  /* 先判断是不是"以后的步骤" */
  const later = PLAN.findIndex((x, i) => i > S.idx && x.type === 'wire' && pair(x.edge[0], x.edge[1]) === pair(a, b));
  if (later > S.idx) return fail(c, t('wiring.wrongstep'));
  const mm = (G1.wiring.mistakes || []).find((x) => pair(x.pair[0], x.pair[1]) === pair(a, b));
  if (mm) return fail(c, tx(mm.hint));
  const step = c.step;
  const fails = (S.fails[c.id] || 0) + 1;
  let hint;
  if (fails <= 1) hint = tx(step.typicalFail);
  else {
    const i2 = Math.min(step.hints.length, fails >= 7 ? 3 : fails >= 5 ? 2 : 1) - 1;
    hint = tx(step.hints[i2]) || tx(step.typicalFail);
  }
  fail(c, hint);
}

function connectPower() {
  if (!S || S.plugs.power) return;
  sfx('plug');
  S.plugs.power = true;
  S.screenOn = true;
  S.screenLit = true;
  clearFail();
  saveState();
  nextStep(t('gate.pass'));
}

/* ---------- 3D 点击入口 ---------- */
const PROP_ALIAS = {
  J3: 'prop:tfcard', 'USB-C1': 'prop:power', LAN1: 'prop:lan',
  'plug:usbc': 'prop:power', 'plug:rj45': 'prop:lan'
};

function onScenePick(raw) {
  if (!S) return;
  /* 板体只用于悬停提示，点它不算操作 */
  if (raw === 'neo3-plus_PCB' || raw === 'neo3-plus_silkscreen' || raw === 'holes') return;
  if (raw === 'screen:flip') {
    if (current() && current().type === 'screen' && !S.screenFlipped) flipScreen();
    else if (!S.screenFlipped) toast(t('screen.first'));
    return;
  }
  if (raw === 'btn:usr') { toggleScreenPower(); return; }
  let id = raw;
  if (id === 'sw:toggle') { toggleSwitch(); return; }
  /* TF 卡本体：先弹烧录终端；烧录完后再点的是卡槽，不是这里。 */
  if (id === 'tfcard:card' || id === 'plug:microsd') {
    const c = current();
    if (c && c.type === 'plug' && c.plug.id === 'tfcard' && !S.plugs.tfcard) {
      if (S.tf.flashed) toast(t('tf.insertHint'));
      else openTfTerminal();
    } else if (!(TF && TF.open)) toast(t('step.notyet'));
    return;
  }
  if (PROP_ALIAS[id]) id = PROP_ALIAS[id];
  const c = current();
  if (!c || c.type === 'complete') return;

  if (c.type === 'wire') {
    if (isEndpoint(id)) pickWire(id, c);
    else toast(t('wiring.wrongstep'));
    return;
  }
  if (c.type === 'plug') {
    if (id === 'prop:' + c.plug.id) {
      if (c.plug.id === 'power') connectPower();
      else if (c.plug.id === 'tfcard') {
        if (!S.tf.flashed) { openTfTerminal(); toast(t('tf.firstFlash'), 'bad'); }
        else { S.plugs.tfcard = true; clearFail(); nextStep(t('gate.pass')); }
      }
      else {
        S.plugs[c.plug.id] = true;
        sfx('plug');
        clearFail();
        nextStep(t('gate.pass'));
      }
    } else {
      fail(c, hintText(c.step, (S.fails[c.id] || 0) + 1) || t('step.notyet'));
    }
    return;
  }
  toast(t('step.notyet'));
}

function screenPageFor() {
  if (S.sw) return 'admin';    // ON（闭合）= ADMIN：显示访问页
  return 'nokey';              // OFF（断路）= LOCK：常规面板
}

let screenTrans = null;      // { start, fromUnlocked }
let screenToast = null;      // { text, until }
let screenTransRAF = 0;

function refreshScreen() {
  if (!window.NKBOARD3D || !sceneMounted) return;
  const opts = { page: screenPageFor(), adminAt: ADMIN_AT };
  if (screenTrans) {
    opts.transition = {
      p: Math.min(1, (performance.now() - screenTrans.start) / 300),
      fromUnlocked: screenTrans.fromUnlocked
    };
  }
  if (screenToast && performance.now() < screenToast.until) opts.toast = screenToast.text;
  window.__nkScreenHold = !!screenTrans || !!screenToast;
  NKBOARD3D.setScreenBoot(!!S.screenOn && !!S.screenLit && !!S.screenFlipped, opts);
}

function playScreenTransition(fromUnlocked) {
  if (screenTransRAF) clearInterval(screenTransRAF);
  screenTrans = { start: performance.now(), fromUnlocked };
  screenTransRAF = setInterval(() => {
    refreshScreen();
    if (performance.now() - screenTrans.start >= 300) {
      clearInterval(screenTransRAF);
      screenTransRAF = 0;
      screenTrans = null;
      refreshScreen();
    }
  }, 16);
}

function flashScreenToast(text) {
  screenToast = { text, until: performance.now() + 2500 };
  refreshScreen();
  setTimeout(() => { screenToast = null; refreshScreen(); }, 2600);
}

function flipScreen() {
  if (!S || S.screenFlipped) return;
  sfx('flip');
  S.screenFlipped = true;
  if (S.plugs.power) { S.screenOn = true; S.screenLit = true; }
  saveState();
  if (window.NKBOARD3D && sceneMounted) {
    NKBOARD3D.setScreenPose('over', true);
    NKBOARD3D.frame('screen-face');
  }
  refreshScreen();
  render();
  setTimeout(() => {
    sfx('celebrate');
    if (window.NKFX) window.NKFX.celebrate();
  }, 650);
  if (S.screenLit) setTimeout(() => sfx('boot'), 700);
}

function toggleScreenPower() {
  if (!S || !S.screenFlipped || !S.screenOn) { toast(t('screen.first')); return; }
  if (S.sw) { flashScreenToast(t('screen.adminOn')); toast(t('screen.adminOn'), 'bad'); return; }
  sfx('click');
  S.screenLit = !S.screenLit;
  saveState();
  refreshScreen();
  render();
}


function toggleSwitch() {
  sfx('switch');
  const fromUnlocked = S.sw;
  S.sw = !S.sw;
  screenToast = null;
  if (S.sw) {           /* ON（闭合）= ADMIN */
    ADMIN_AT = Date.now();
    if (S.screenOn) S.screenLit = true; /* 端口开放：不允许熄屏（含原本熄灭的屏） */
  } else {              /* OFF（断路）= LOCK */
    ADMIN_AT = 0;
  }
  saveState();
  if (window.NKBOARD3D && sceneMounted) NKBOARD3D.setSwitchState(S.sw);
  if (S.screenFlipped) {
    playScreenTransition(fromUnlocked);
  }
  if (S.sw && S.screenOn) toast(t('screen.adminKeep'));
  else toast(fmt(t('switch.flip'), { s: S.sw ? 'ON' : 'OFF' }));
}

/* ---------- TF 卡烧录伪终端 ---------- */
let TF = null;   // { open, busy, done, token, mode, pass, board }

const TF_SCRIPT_URL = 'https://nokeyout.com/tools/install-tf.sh';
const TF_BOARD = 'neo3plus';
const TF_IMAGE_HINT = 'latest Armbian community image (resolved at run time)';
const TF_PROMPTS = {
  shell: 'user@pc:~$',
  disk: 'Select target disk [1-1]:',
  format: 'All partitions and data on this disk will be lost. Type YES to continue:',
  pass: 'Password for nokeyout:',
  confirm: 'Confirm password:',
};
function tfCommand() {
  return `curl -fsSL ${TF_SCRIPT_URL} -o /tmp/install-tf.sh && sudo bash /tmp/install-tf.sh --board ${TF_BOARD}`;
}
const tfNorm = (s) => String(s || '').replace(/\s+/g, ' ').trim();
const TF_PARAMS = [
  ['curl … install-tf.sh', 'tf.param.fetch'],
  ['--board neo3plus', 'tf.param.board'],
  ['--resolve-only', 'tf.param.resolve'],
  ['--no-deploy', 'tf.param.nodeploy'],
];

function renderTfParams() {
  const ul = document.getElementById('tf-term-params');
  if (!ul) return;
  ul.innerHTML = '';
  const add = (flag, key) => {
    const li = document.createElement('li');
    const b = document.createElement('b');
    const s = document.createElement('span');
    b.textContent = flag;
    s.textContent = t(key);
    li.title = t(key);
    li.appendChild(b);
    li.appendChild(s);
    ul.appendChild(li);
  };
  TF_PARAMS.forEach(([flag, key]) => add(flag, key));
  const more = document.createElement('li');
  const span = document.createElement('span');
  span.textContent = t('tf.param.more');
  more.title = t('tf.param.more');
  more.appendChild(span);
  ul.appendChild(more);
  const cmd = document.getElementById('tf-term-cmd');
  if (cmd) cmd.title = t('tf.cmdHint');
  const board = document.getElementById('tf-term-board');
  if (board) board.textContent = t('tf.boardNote');
}

function termOut() { return document.getElementById('tf-term-out'); }
function setTfPrompt(text) {
  const p = document.getElementById('tf-term-prompt');
  if (p) p.textContent = text;
}
function termLine(text, cls) {
  const out = termOut();
  if (!out) return null;
  const d = document.createElement('div');
  d.className = 'term-line' + (cls ? ' ' + cls : '');
  d.textContent = text;
  out.appendChild(d);
  out.scrollTop = out.scrollHeight;
  return d;
}

function openTfTerminal() {
  const el = document.getElementById('tf-term');
  if (!el || !S) return;
  sfx('click');
  if (TF && TF.open) {
    const input = document.getElementById('tf-term-input');
    if (input && !input.disabled) input.focus();
    return;
  }
  closeTfTerminal();
  TF = { open: true, busy: false, done: !!S.tf.flashed, token: {}, mode: 'command', pass: '', board: '' };
  el.hidden = false;
  el.dataset.state = S.tf.flashed ? 'ready' : 'idle';
  document.getElementById('tf-term-cmd').textContent = tfCommand();
  const copyBtn = document.getElementById('tf-copy-cmd');
  if (copyBtn) copyBtn.classList.add('attention');
  const closeBtn = document.getElementById('tf-term-close');
  if (closeBtn) closeBtn.classList.toggle('attention', !!S.tf.flashed);
  const skipBtn = document.getElementById('tf-term-skip');
  if (skipBtn) skipBtn.hidden = !!S.tf.flashed;
  renderTfParams();
  updateBackUI();
  const hint = document.getElementById('tf-term-hint');
  if (hint) { hint.textContent = t('tf.cmdHint'); hint.classList.remove('ok'); }
  termOut().innerHTML = '';
  const input = document.getElementById('tf-term-input');
  const form = document.getElementById('tf-term-form');
  if (form) form.hidden = false;
  if (input) {
    input.disabled = false;
    input.value = '';
    input.type = 'text';
    input.placeholder = '';
    setTfPrompt(TF_PROMPTS.shell);
    setTimeout(() => { if (TF && TF.open && !TF.busy) input.focus(); }, 60);
  }
}

function skipTfTerminal() {
  if (!S) return;
  sfx('step');
  closeTfTerminal();
  S.tf.flashed = true;
  saveState();
  render();
  toast(t('tf.skipToast'), 'ok');
}

async function copyTfCommand() {
  const copyBtn = document.getElementById('tf-copy-cmd');
  if (copyBtn) copyBtn.classList.remove('attention');
  const cmd = tfCommand();
  try {
    await navigator.clipboard.writeText(cmd);
    sfx('click');
    toast(t('tf.copied'), 'ok');
    return;
  } catch (e) { /* fall through to selection */ }
  const el = document.getElementById('tf-term-cmd');
  if (el) {
    const r = document.createRange();
    r.selectNodeContents(el);
    const sel = window.getSelection();
    if (sel) { sel.removeAllRanges(); sel.addRange(r); }
  }
  toast(t('tf.copyFail'), 'bad');
}

function closeTfTerminal() {
  if (!TF) {
    const el = document.getElementById('tf-term');
    if (el) el.hidden = true;
    updateBackUI();
    return;
  }
  sfx('click');
  TF.open = false;
  TF.token = null;
  TF = null;
  const el = document.getElementById('tf-term');
  if (el) el.hidden = true;
  const copyBtn = document.getElementById('tf-copy-cmd');
  if (copyBtn) copyBtn.classList.remove('attention');
  const closeBtn = document.getElementById('tf-term-close');
  if (closeBtn) closeBtn.classList.remove('attention');
  updateBackUI();
}

function onTfSubmit(e) {
  if (e) e.preventDefault();
  if (!TF || !TF.open || TF.busy) return;
  sfx('terminal');
  const input = document.getElementById('tf-term-input');
  const v = input ? input.value : '';
  if (TF.mode === 'password') { tfPasswordStep(v); return; }
  if (TF.mode === 'confirm') { tfConfirmStep(v); return; }
  if (TF.mode === 'disk') { tfDiskStep(v); return; }
  if (TF.mode === 'format') { tfFormatStep(v); return; }
  if (!v.trim()) return;
  if (input) input.value = '';
  termLine(`${TF_PROMPTS.shell} ${v}`, 'cmd');
  const norm = tfNorm(v);
  if (norm !== tfNorm(tfCommand())) {
    termLine('ERROR: command mismatch (expected install-tf.sh --board neo3plus)', 'bad');
    return;
  }
  TF.board = TF_BOARD;
  termLine('==> 1/3 Resolving board and official image', 'dim');
  termLine('  board:   neo3plus (NanoPi NEO3 Plus)', 'dim');
  termLine(`  image:   ${TF_IMAGE_HINT}`, 'dim');
  termLine('  variant: minimal · support: community', 'dim');
  termLine('==> 2/3 Preparing first-boot install', 'dim');
  termLine('==> 3/3 Writing card', 'dim');
  termLine('Select the target TF card (all data will be erased):', 'dim');
  termLine('  [1] /dev/sda     29.7G   USB Card Reader', 'dim');
  TF.mode = 'disk';
  tfSetInputType('text', TF_PROMPTS.disk, '', '');
}

function tfSetInputType(type, prompt, placeholder, value) {
  setTfPrompt(prompt);
  const input = document.getElementById('tf-term-input');
  if (input) {
    input.type = type;
    input.value = value || '';
    input.placeholder = placeholder || '';
    input.focus();
  }
}

function tfDiskStep(v) {
  const input = document.getElementById('tf-term-input');
  if (input) input.value = '';
  termLine(`${TF_PROMPTS.disk} ${v}`, 'cmd');
  if (v.trim() !== '1') {
    termLine(`ERROR: invalid selection: ${v}`, 'bad');
    return;
  }
  termLine('WARNING: /dev/sda (29.7G USB Card Reader) will be ERASED and reformatted.', 'hint');
  TF.mode = 'format';
  tfSetInputType('text', TF_PROMPTS.format, '', '');
}

function tfFormatStep(v) {
  const input = document.getElementById('tf-term-input');
  if (input) input.value = '';
  termLine(`${TF_PROMPTS.format} ${v}`, 'cmd');
  if (v !== 'YES') {
    termLine('ERROR: cancelled', 'bad');
    return;
  }
  termLine('==> Target:   /dev/sda (29.7G USB Card Reader)', 'dim');
  termLine(`==> Board:    ${TF.board}`, 'dim');
  termLine('==> User:     nokeyout', 'dim');
  termLine('==> Hostname: nokeyout', 'dim');
  startTfPassword();
}

/* 真机脚本不接收默认密码：命令不带 --password 时强制交互输入。
   游戏里同样要求输两次，输入不回显、不落盘、不写进任何状态。 */
function startTfPassword() {
  if (!TF) return;
  TF.mode = 'password';
  TF.pass = '';
  tfSetInputType('password', TF_PROMPTS.pass, '', '');
}

function tfPasswordStep(v) {
  if (!v) { termLine('ERROR: empty password', 'bad'); return; }
  TF.pass = v;
  TF.mode = 'confirm';
  tfSetInputType('password', TF_PROMPTS.confirm, '', '');
}

function tfConfirmStep(v) {
  const input = document.getElementById('tf-term-input');
  if (v !== TF.pass) {
    termLine('ERROR: passwords do not match', 'bad');
    startTfPassword();
    return;
  }
  TF.pass = '';
  TF.mode = 'flash';
  setTfPrompt(TF_PROMPTS.shell);
  if (input) { input.type = 'text'; input.value = ''; }
  runFakeFlash();
}

function runFakeFlash() {
  if (!TF || !TF.open) return;
  sfx('plug');
  const token = TF.token;
  const el = document.getElementById('tf-term');
  TF.busy = true;
  TF.mode = 'flash';
  if (el) el.dataset.state = 'running';
  const input = document.getElementById('tf-term-input');
  const form = document.getElementById('tf-term-form');
  if (input) input.disabled = true;
  if (form) form.hidden = true;
  const alive = () => !!(TF && TF.open && TF.token === token);
  const wait = (ms) => new Promise((r) => setTimeout(r, ms));

  (async () => {
    termLine(`==> Image:    ${TF_IMAGE_HINT}`, 'dim');
    await wait(120); if (!alive()) return;
    termLine('==> Downloading the resolved image', 'dim');
    await wait(200); if (!alive()) return;
    termLine('==> Writing /root/.cache/nokeyout-images/<resolved>.img.xz -> /dev/sda', 'dim');
    await wait(150); if (!alive()) return;

    const prog = termLine('0 MiB / 1.8 GiB copied, 0.0 MB/s', 'dim');
    const total = 1843;   // MiB
    for (let p = 0; p <= 100; p += 4) {
      if (!alive()) return;
      prog.textContent = `${Math.round(total * p / 100)} MiB / 1.8 GiB copied, ${(17 + Math.random() * 4).toFixed(1)} MB/s`;
      await wait(62);
    }
    if (!alive()) return;
    prog.textContent = '1843+0 records in';
    termLine('1843+0 records out', 'dim');
    termLine('==> Rootfs: /dev/sda1', 'dim');
    await wait(120); if (!alive()) return;
    termLine('==> Created user nokeyout (uid=1000 gid=1000)', 'dim');
    await wait(220); if (!alive()) return;
    termLine('==> Done.', 'dim');
    termLine('==> User: nokeyout', 'dim');
    termLine('==> SSH:  enabled (PasswordAuthentication yes)', 'dim');
    termLine('==> First boot: nokeyout-firstboot.service', 'dim');
    termLine('==> Eject the card, insert it into the board, power on, then: ssh nokeyout@<device-ip>', 'dim');
    termLine('Done. First boot installs the bundled release offline; update from the panel.', 'ok');

    S.tf.flashed = true;
    sfx('step');
    saveState();
    if (el) el.dataset.state = 'ready';
    const hint = document.getElementById('tf-term-hint');
    if (hint) { hint.textContent = t('tf.termDone'); hint.classList.add('ok'); }
    const copyBtn = document.getElementById('tf-copy-cmd');
    if (copyBtn) copyBtn.classList.remove('attention');
    const skipBtn = document.getElementById('tf-term-skip');
    if (skipBtn) skipBtn.hidden = true;
    const closeBtn = document.getElementById('tf-term-close');
    if (closeBtn) closeBtn.classList.add('attention');
    if (TF) { TF.busy = false; TF.done = true; }
    render();                       // 步骤卡改成「点卡槽插入」
    toast(t('tf.ready'), 'ok');
    // 不自动关闭：保留完整安装输出，由用户点 × 退出。
  })().catch((err) => {
    console.error('flash terminal', err);
    TF = null;
  });
}


function edgeRefs(c) {
  if (!c) return [];
  if (c.type === 'wire') return c.edge.slice();
  if (c.type === 'plug') return ['prop:' + c.plug.id];
  if (c.type === 'screen') return (c.focus || []).slice();
  if (c.type === 'check') return (c.focus || []).slice();
  return [];
}

function syncScene(c) {
  if (!window.NKBOARD3D || !sceneMounted) return;
  NKBOARD3D.setSwitchState(S.sw);
  const wantPose = S.screenFlipped ? 'over' : 'north';
  if (NKBOARD3D.screenPose && NKBOARD3D.screenPose() !== wantPose) NKBOARD3D.setScreenPose(wantPose, false);
  NKBOARD3D.setScreenBoot(!!S.screenOn && !!S.screenLit && !!S.screenFlipped, { page: screenPageFor() });
  if (NKBOARD3D.setScreenCue) NKBOARD3D.setScreenCue(!!(c && c.type === 'screen' && !S.screenFlipped));
  if (NKBOARD3D.setTfCard) {
    NKBOARD3D.setTfCard({
      parked: !!(c && c.type === 'plug' && c.plug.id === 'tfcard' && !S.plugs.tfcard),
      flashed: !!S.tf.flashed
    });
  }
  NKBOARD3D.setStepRefs(edgeRefs(c));
  if (NKBOARD3D.setPulse) NKBOARD3D.setPulse(!!(c && c.type !== 'complete'));
  syncWireTut(c);
  NKBOARD3D.setWires(S.wires);
  NKBOARD3D.setPlugged(S.plugs);
}

/* ---------- 常驻注记：最后一步完成后标出 USR / 开关 ---------- */
let annoBuilt = false, annoRAF = 0;
const annoSizes = {};

function buildAnnotations() {
  const layer = document.getElementById('anno-layer');
  if (!layer || annoBuilt) return layer;
  layer.innerHTML = ['usr', 'switch'].map((k) =>
    `<div class="anno" data-k="${k}"><span class="anno-dot"></span><span class="anno-line"></span><div class="anno-box"></div></div>`
  ).join('');
  annoBuilt = true;
  return layer;
}

function annotationText() {
  const layer = document.getElementById('anno-layer');
  if (!layer) return;
  const usr = layer.querySelector('[data-k="usr"] .anno-box');
  const sw = layer.querySelector('[data-k="switch"] .anno-box');
  if (usr) usr.textContent = t('anno.usr');
  if (sw) sw.textContent = t('anno.switch');
  for (const k of Object.keys(annoSizes)) delete annoSizes[k];
  scheduleAnnotations();
}

function scheduleAnnotations() {
  if (annoRAF) return;
  annoRAF = requestAnimationFrame(() => { annoRAF = 0; positionAnnotations(); });
}

function positionAnnotations() {
  const layer = document.getElementById('anno-layer');
  if (!layer || layer.hidden || !window.NKBOARD3D || !sceneMounted) return;
  const anchors = NKBOARD3D.annotationAnchors ? NKBOARD3D.annotationAnchors() : null;
  if (!anchors) return;
  const clamp = (v, lo, hi) => Math.max(lo, Math.min(hi, v));
  const place = (key, p, dx, dy) => {
    if (!p) return;
    const at = window.NKView3D.project(p[0], p[1], p[2]);
    const node = layer.querySelector(`[data-k="${key}"]`);
    if (!node) return;
    const dot = node.querySelector('.anno-dot');
    const line = node.querySelector('.anno-line');
    const box = node.querySelector('.anno-box');
    if (!annoSizes[key]) annoSizes[key] = { w: box.offsetWidth || 120, h: box.offsetHeight || 26 };
    const bw = annoSizes[key].w, bh = annoSizes[key].h;
    const bx = clamp(at.x + dx, bw / 2 + 14, window.innerWidth - bw / 2 - 14);
    const by = clamp(at.y + dy, bh / 2 + 14, window.innerHeight - bh / 2 - 14);
    dot.style.left = at.x + 'px';
    dot.style.top = at.y + 'px';
    const vx = bx - at.x, vy = by - at.y;
    line.style.left = at.x + 'px';
    line.style.top = at.y + 'px';
    line.style.width = Math.hypot(vx, vy) + 'px';
    line.style.transform = `rotate(${Math.atan2(vy, vx)}rad)`;
    box.style.left = bx + 'px';
    box.style.top = by + 'px';
  };
  place('usr', anchors.usr, 165, 45);
  place('switch', anchors.switch, -165, -80);
}

/* ---------- 首次接线引导：针脚 -- 虚线 -- 说明 -- 虚线 -- 排针 ---------- */
let wireTutRAF = 0;

function scheduleWireTut() {
  if (wireTutRAF) return;
  wireTutRAF = requestAnimationFrame(() => { wireTutRAF = 0; positionWireTut(); });
}

function syncWireTut(c) {
  const el = document.getElementById('wire-tut');
  if (!el) return;
  const want = !!(sceneEntered && c && c.type === 'wire' && S && S.idx === 0);
  el.hidden = !want;
  if (!want) return;
  const label = el.querySelector('.wt-box span');
  if (label) label.textContent = t('tut.wire');
  scheduleWireTut();
}

function positionWireTut() {
  const el = document.getElementById('wire-tut');
  if (!el || el.hidden || !window.NKBOARD3D || !window.NKView3D) return;
  const c = current();
  if (!c || !c.edge) return;
  const wa = NKBOARD3D.endpointPoint(c.edge[0]);
  const wb = NKBOARD3D.endpointPoint(c.edge[1]);
  if (!wa || !wb) return;
  const pa = NKView3D.project(wa[0], wa[1], wa[2]);
  const pb = NKView3D.project(wb[0], wb[1], wb[2]);
  const box = el.querySelector('.wt-box');
  const dotA = el.querySelector('.wt-dot.a'), dotB = el.querySelector('.wt-dot.b');
  const lineA = el.querySelector('.wt-line.a'), lineB = el.querySelector('.wt-line.b');
  if (!box || !dotA || !dotB || !lineA || !lineB) return;
  const clamp = (v, lo, hi) => Math.max(lo, Math.min(hi, v));
  const bw = box.offsetWidth || 160, bh = box.offsetHeight || 30;
  /* 说明框放在两端连线的垂直方向外侧，两条虚线才能分别指到两个针脚 */
  const dx = pb.x - pa.x, dy = pb.y - pa.y;
  const len = Math.hypot(dx, dy) || 1;
  let nx = -dy / len, ny = dx / len;
  if (ny > 0) { nx = -nx; ny = -ny; }            // 优先朝上，避免压住底部
  const off = Math.min(110, Math.max(64, len * 0.34));
  const mx = (pa.x + pb.x) / 2 + nx * off;
  const my = (pa.y + pb.y) / 2 + ny * off;
  const bx = clamp(mx, bw / 2 + 16, window.innerWidth - bw / 2 - 16);
  const by = clamp(my, bh / 2 + 96, window.innerHeight - bh / 2 - 16);
  box.style.left = bx + 'px';
  box.style.top = by + 'px';
  const drawLine = (dot, line, p) => {
    dot.style.left = p.x + 'px';
    dot.style.top = p.y + 'px';
    const vx = bx - p.x, vy = by - p.y;
    line.style.left = p.x + 'px';
    line.style.top = p.y + 'px';
    line.style.width = Math.hypot(vx, vy) + 'px';
    line.style.transform = 'rotate(' + Math.atan2(vy, vx) + 'rad)';
  };
  drawLine(dotA, lineA, pa);
  drawLine(dotB, lineB, pb);
}

function syncAnnotations(c) {
  const layer = document.getElementById('anno-layer');
  if (!layer) return;
  const show = !!(S && S.screenFlipped && c && (c.type === 'screen' || c.type === 'complete'));
  layer.hidden = !show;
  if (!show) return;
  buildAnnotations();
  annotationText();
}

/* ---------- 步骤推进 ---------- */
function nextStep(msg) {
  /* 副作用由完成该步骤的 handler 写入（线/插头），这里只推进指针。 */
  sfx('step');
  if (window.NKFX) window.NKFX.pop();
  S.idx = Math.min(S.idx + 1, PLAN.length - 1);
  S.pick = null;
  saveState();
  render();
  if (msg) toast(msg, 'ok');
}

/* ---------- 完成 ---------- */
function renderComplete() {
  if (window.NKFX) window.NKFX.celebrate();
  S.completeDismissed = false;
  const el = document.getElementById('complete');
  el.hidden = false;
  document.getElementById('done-title').textContent = t('done.title');
  document.getElementById('view-scene').onclick = () => {
    S.completeDismissed = true;
    saveState();
    render();
  };
  document.getElementById('replay').onclick = resetAll;
}

/* ============ 3D 场景（懒加载） ============ */
function loadScript(src) {
  return new Promise((res, rej) => {
    if (document.querySelector(`script[src="${src}"]`)) return res();
    const s = document.createElement('script');
    s.src = src;
    s.onload = res;
    s.onerror = () => rej(new Error('加载失败 ' + src));
    document.head.appendChild(s);
  });
}

let loadProgress = 0, loadPhase = 'scene';

function setLoad(p, label, phase) {
  loadProgress = Math.max(loadProgress, Math.min(1, p));
  if (phase) loadPhase = phase;
  const fill = document.getElementById('load-fill');
  const pct = document.getElementById('load-pct');
  const lab = document.getElementById('load-label');
  if (fill) fill.style.width = (loadProgress * 100).toFixed(1) + '%';
  if (pct) pct.textContent = Math.round(loadProgress * 100) + '%';
  if (lab) lab.textContent = label || t('loading.' + loadPhase);
  const box = document.getElementById('loading');
  if (box) box.style.setProperty('--gate-shade', (1 - 0.72 * loadProgress).toFixed(3));
}

async function ensure3D() {
  if (!sceneLoading) {
    sceneLoading = (async () => {
      const scripts = [
        'assets/board/board-frame.js',
        'assets/board/neo3-plus-parts.js',
        'assets/board/plug-meshes.js?v=20261023-33',
        'assets/board/plug-models.js?v=20261023-33',
        'assets/board/plug-placement.js?v=20261023-33',
        'vendor/three/three.min.js',
        'vendor/three/fflate.min.js?v=20261020-1',
        'vendor/three/FBXLoader.js?v=20261020-1',
      ];
      if (location.protocol === 'file:') scripts.push('assets/board/models/fbx-data.js?v=20261023-1');
      scripts.push('assets/screen/screen-data.js?v=20261022-4');
      scripts.push('view3d.js?v=20261028-2');
      scripts.push('board3d.js?v=20261023-34');
      for (let i = 0; i < scripts.length; i++) {
        await loadScript(scripts[i]);
        setLoad(((i + 1) / scripts.length) * 0.4, null, 'scene');
      }
      window.__nk3dEndpoint = onScenePick;
      window.__nkLoadAssets = (f) => setLoad(0.4 + f * 0.55, null, 'meshes');
      const parts = (window.NK_BOARD_PARTS || { parts: [] }).parts;
      await window.NKBOARD3D.mount(document.getElementById('bench3d'), parts, LANG);
      sceneMounted = true;
      window.NKBOARD3D.onViewChange(() => { scheduleAnnotations(); scheduleWireTut(); });
      setLoad(1, null, 'ready');
    })();
  }
  return sceneLoading;
}

async function bootScene() {
  const box = document.getElementById('loading');
  let enter = null;
  try {
    await ensure3D();
    /* 先把场景按当前步骤摆好机位，再让用户点“开始装配”——避免点完再跳镜头 */
    window.NKBOARD3D.show();
    window.NKBOARD3D.resize();
    frameCurrentStep();
    render();
    box.classList.add('ready');
    const parts = document.getElementById('load-parts');
    if (parts) { renderLoadParts(); parts.hidden = false; }
    enter = document.getElementById('load-enter');
    if (enter) {
      enter.hidden = false;
      enter.addEventListener('click', enterScene, { once: true });
      enter.focus({ preventScroll: true });
    }
  } catch (e) {
    box.classList.add('error');
    const lab = document.getElementById('load-label');
    if (lab) lab.textContent = (location.protocol === 'file:' ? t('load.file') : t('load.fail') + (e && e.message ? e.message : e));
    const pct = document.getElementById('load-pct');
    if (pct) pct.textContent = '!';
    console.error(e);
  }
}

function enterScene() {
  const box = document.getElementById('loading');
  if (box) box.hidden = true;
  document.body.classList.remove('gate');
  if (!window.NKBOARD3D || !sceneMounted) return;
  sceneEntered = true;
  window.NKBOARD3D.resize();
  render();
}

/* 接线步骤对准两个端点（起始机位比默认机位再向左转 90°）；其余步骤回到总览 */
const START_YAW = 55, START_PITCH = 44;
function frameCurrentStep() {
  if (!window.NKBOARD3D || !sceneMounted) return;
  const c = current();
  if (c && c.type === 'wire' && c.edge && c.edge.length === 2 && NKBOARD3D.focusEndpoints) {
    NKBOARD3D.focusEndpoints(c.edge, { yaw: START_YAW, pitch: START_PITCH });
    return;
  }
  NKBOARD3D.frame('wiring');
}

/* ============ 界面绑定 ============ */
function bindChrome() {
  document.getElementById('lang-toggle').addEventListener('click', toggleLang);
  const loadLang = document.getElementById('load-lang');
  if (loadLang) loadLang.addEventListener('click', toggleLang);
  const sfxBtn = document.getElementById('sfx-toggle');
  if (sfxBtn) {
    updateSfxUI();
    sfxBtn.addEventListener('click', () => {
      const on = window.NKSFX ? window.NKSFX.toggle() : false;
      updateSfxUI();
      if (on) sfx('click');
    });
  }
  document.getElementById('btn-back').addEventListener('click', goBack);
  const tfForm = document.getElementById('tf-term-form');
  if (tfForm) tfForm.addEventListener('submit', onTfSubmit);
  const tfClose = document.getElementById('tf-term-close');
  if (tfClose) tfClose.addEventListener('click', closeTfTerminal);
  const tfSkip = document.getElementById('tf-term-skip');
  if (tfSkip) tfSkip.addEventListener('click', skipTfTerminal);
  const tfCopy = document.getElementById('tf-copy-cmd');
  if (tfCopy) tfCopy.addEventListener('click', copyTfCommand);
  const toggle = document.getElementById('hud-steps-toggle');
  const drawer = document.getElementById('step-drawer');
  const placeDrawer = () => {
    if (!drawer || drawer.hidden) return;
    const r = toggle.getBoundingClientRect();
    const width = Math.min(340, innerWidth - 20);
    const left = Math.min(innerWidth - width - 10, Math.max(10, r.right - width));
    const top = Math.round(r.bottom + 8);
    drawer.style.left = left + 'px';
    drawer.style.top = top + 'px';
    drawer.style.right = 'auto';
    drawer.style.width = width + 'px';
    drawer.style.maxHeight = Math.max(160, Math.min(innerHeight - top - 10, 520)) + 'px';
  };
  toggle.addEventListener('click', () => {
    drawer.hidden = !drawer.hidden;
    if (!drawer.hidden) placeDrawer();
  });
  document.getElementById('drawer-close').addEventListener('click', () => { drawer.hidden = true; });
  const proc = document.getElementById('procurement');
  renderProcurement();
  const procBtn = document.getElementById('hud-proc');
  if (procBtn && proc) {
    procBtn.addEventListener('click', () => {
      proc.hidden = !proc.hidden;
      drawer.hidden = true;
    });
    document.getElementById('proc-close').addEventListener('click', () => { proc.hidden = true; });
    toggle.addEventListener('click', () => { proc.hidden = true; });
  }
  document.getElementById('cam-reset').addEventListener('click', frameCurrentStep);
  document.getElementById('cam-top').addEventListener('click', () => window.NKBOARD3D && NKBOARD3D.frame('top'));
  window.addEventListener('resize', () => {
    if (drawer && !drawer.hidden) placeDrawer();
    if (window.NKBOARD3D && sceneMounted) NKBOARD3D.resize();
    scheduleAnnotations();
    scheduleWireTut();
  });
  window.__nk3dEndpoint = onScenePick;
  window.__nkPick = onScenePick;
  window.__nkState = () => ({
    idx: S.idx, total: PLAN.length - 1,
    current: current() ? current().id : null,
    type: current() ? current().type : null, wires: S.wires.length, plugs: { ...S.plugs }, sw: S.sw, screenFlipped: !!S.screenFlipped, screenOn: !!S.screenOn, screenLit: !!S.screenLit, tfFlashed: !!S.tf.flashed
  });
  window.__nkTf = () => ({
    open: !!(TF && TF.open), busy: !!(TF && TF.busy), done: !!(TF && TF.done),
    mode: TF ? TF.mode : null,
    board: TF ? TF.board : null,
    flashed: !!S.tf.flashed, command: tfCommand(),
  });
  window.__nkStep = () => {
    const c = current();
    return c ? { id: c.id, type: c.type, title: stepTitle(c), group: c.group } : null;
  };
  window.__nkPlan = () => PLAN.map((c) => ({ id: c.id, type: c.type, title: stepTitle(c), group: c.group }));
}

function resetAll() {
  if (!confirm(t('reset.confirm'))) return;
  sfx('click');
  try { localStorage.removeItem(SAVE_KEY); } catch (e) { /* ignore */ }
  closeTfTerminal();
  S = newState();
  ADMIN_AT = S.sw ? Date.now() : 0; /* 默认 ON（断路）= LOCK */
  document.getElementById('complete').hidden = true;
  document.getElementById('step-card').hidden = false;
  const drawer = document.getElementById('step-drawer');
  if (drawer) drawer.hidden = true;
  render();
  if (window.NKBOARD3D && sceneMounted) {
    NKBOARD3D.setWires([]);
    NKBOARD3D.setPlugged({});
    if (NKBOARD3D.setTfCard) NKBOARD3D.setTfCard({ parked: false, flashed: false });
    if (NKBOARD3D.setScreenBoot) NKBOARD3D.setScreenBoot(false, {});
    frameCurrentStep();
  }
  toast(t('reset.done'));
}

function toggleLang() {
  LANG = LANG === 'zh' ? 'en' : 'zh';
  try { localStorage.setItem('nokeyout-lang', LANG); } catch (e) { /* ignore */ }
  applyLangChrome();
  render();
}

function applyLangChrome() {
  document.documentElement.lang = LANG === 'zh' ? 'zh-CN' : 'en';
  const lt = document.getElementById('lang-toggle');
  if (lt) lt.textContent = LANG === 'zh' ? 'EN' : '中';
  const ll = document.getElementById('load-lang');
  if (ll) ll.textContent = LANG === 'zh' ? 'EN' : '中';
  document.querySelectorAll('[data-i18n]').forEach((el) => { el.textContent = t(el.dataset.i18n); });
  const loadBox = document.getElementById('loading');
  if (loadBox && !loadBox.hidden && !loadBox.classList.contains('error')) setLoad(loadProgress, t('loading.' + loadPhase), loadPhase);
  if (loadBox && loadBox.classList.contains('ready')) renderLoadParts();
  renderProcurement();
  updateSfxUI();
  syncWireTut(current());
  if (annoBuilt) annotationText();
  if (TF && TF.open) {
    renderTfParams();
    const hint = document.getElementById('tf-term-hint');
    if (hint) hint.textContent = t(TF.done ? 'tf.insertHint' : 'tf.cmdHint');
    if (TF.mode === 'password') {
      setTfPrompt(TF_PROMPTS.pass);
    } else if (TF.mode === 'confirm') {
      setTfPrompt(TF_PROMPTS.confirm);
    } else if (TF.mode === 'disk') {
      setTfPrompt(TF_PROMPTS.disk);
    } else if (TF.mode === 'format') {
      setTfPrompt(TF_PROMPTS.format);
    } else if (TF.mode === 'command') {
      setTfPrompt(TF_PROMPTS.shell);
    }
  }
}
