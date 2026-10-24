'use strict';
/* NoKeyOut · 游戏内 3D 看板（薄适配层：把游戏状态接到自写渲染器 NKView3D 上）
 *
 * 渲染器在 site/view3d.js（零依赖自写 WebGL）；这里只做三件"游戏侧"的事：
 *   1. 载入 FBX 模型（neo3-plus-abstract.fbx / 屏幕 / 开关 / 排针）与插拔物网格
 *   2. 当前步骤 → 高亮哪些 refdes（pin:* → 26 针排针；插座类 → 对应 refdes）
 *   3. 插拔步骤完成 → 把对应插拔物摆到座子口
 *
 * 坐标：一律走 assets/board/board-frame.js（唯一出处）。世界单位 = mm，且用 KiCad 的绝对坐标
 * （板左上角在 (100,100)），这样网格顶点不用再平移。
 */
(function () {
  const FRAME = window.NK_BOARD_FRAME;
  const REF_FOR_PROP = { tfcard: 'J3', power: 'USB-C1', lan: 'LAN1' };
  const PROP_OF_REF = { J3: 'prop:tfcard', 'USB-C1': 'prop:power', LAN1: 'prop:lan' };
  const PLUG_OF = { tfcard: 'microsd', power: 'usbc', lan: 'rj45' };
  const PLUG_OF_PROP = { microsd: 'prop:tfcard', usbc: 'prop:power', rj45: 'prop:lan' };
  // 插头插上后摆在哪：座子中心 + 朝向（插头原点=插入端面中心、沿 +Z 向外）
  //   microsd 在板左缘 → 尖朝 +X（绕 Y -90°）；usbc/rj45 在板右缘 → 尖朝 −X（绕 Y +90°）
  // 主板外观全部由 neo3-plus-abstract.fbx 提供；不再加载自产整板网格。
  let abstract = true;
  const PLUG_AT = {
    microsd: { rot: -90, x: 0.6, dz: 0 },
    usbc: { rot: 90, x: 47.6, dz: 0 },   // Type-C 插头壳长 6.65mm；插头尖在局部 -Z，插入约 3.7mm
    rj45: { rot: 90, roll: 180, x: 47.6, dz: 0 },
  };
  const RJ45_PLUG_DY = -1.6;             // 新网口开口略低，网线整体下移 1.6mm
  let parts = [], byRef = new Map(), holesY = 1.2, inited = false, container = null, mounted = false;
  let screenPivot = null, screenPinsGroup = null, screenObj = null, screenProto = null, screenHeight = 11.54;
  let screenPhase = 0, screenTarget = 0, screenAnim = 0, screenAnimLast = 0;
  let screenHingeZ = 95.7;
  let screenPose = 'north';             // north = 屏在板北、背面朝上；over = 翻到板上方
  let wantScreenPose = 'north';
  const SWITCH_Z = 124;                 // 开关在板左缘，纵向对准板心
  const SCREEN_OVER_Y = 20.0;           // 正面态再抬高，避开板载排针（针顶≈10.05）
  const SCREEN_OVER_Z = 102.7;          // 正面态整体向南（+Z）挪 7mm

  // 主板 PCB 顶面（neo3-plus-abstract.fbx 的 `=>01155`：0..1.51mm）
  const BOARD_TOP_Y = 1.51;
  function boardTopY() { return BOARD_TOP_Y; }

  function plugMeshKey(id) { return PLUG_OF[id]; }

  /** 悬停只显示名称（不再带型号/说明/位置来源）。 */
  function showTip(ref, cx, cy) {
    const tip = document.getElementById('bench3d-tip');
    if (!tip) return;
    if (!ref) { tip.hidden = true; return; }
    if (/^pin:\d+$/.test(ref)) ref = 'CON30_PIN' + String(ref.slice(4)).padStart(2, '0');
    else if (ref.startsWith('scr:')) {
      const n = SCREEN_FUNC[ref.slice(4)];
      if (n) ref = 'SCR_PIN' + String(n).padStart(2, '0');
    }
    let title = ref;
    if (ref.startsWith('plug:')) {
      const id = ref.slice(5);
      title = PLUG_NAMES[id] ? nameOf(PLUG_NAMES[id]) : id;
    } else if (ref.startsWith('CON30_PIN')) {
      title = 'PIN ' + parseInt(ref.slice(9), 10);
    } else if (ref.startsWith('SCR_PIN')) {
      const name = SCREEN_PHYS[parseInt(ref.slice(7), 10) - 1] || ref;
      const alias = SCREEN_ALIAS[name];
      title = name + (alias ? ' (' + alias + ')' : '');
    } else if (TIP_NAMES[ref]) {
      title = nameOf(TIP_NAMES[ref]);
    }
    if (tip.dataset.key !== title) {
      tip.innerHTML = '<b>' + title + '</b>';
      tip.dataset.key = title;
    }
    tip.hidden = false;
    const x = Math.min(window.innerWidth - 300, cx + 14);
    tip.style.transform = `translate3d(${x}px,${cy + 16}px,0)`;
  }

  const SCREEN_MAP = window.NK_SCREEN_PINS || {
    phys: ['GND', 'VCC', 'SCL', 'SDA', 'RES', 'DC', 'CS', 'BLK'],
    func: { VCC: 2, GND: 1, BLK: 8, CLK: 3, MOSI: 4, CS: 7, DC: 6, RST: 5 },
  };
  const SCREEN_FUNCS = ['VCC', 'GND', 'BLK', 'CLK', 'MOSI', 'CS', 'DC', 'RST'];
  const SCREEN_PHYS = SCREEN_MAP.phys;
  const SCREEN_FUNC = SCREEN_MAP.func;
  const SCREEN_ALIAS = { SCL: 'CLK', SDA: 'MOSI', RES: 'RST' };
  const SCREEN_BY_PIN = [];
  Object.keys(SCREEN_FUNC).forEach((name) => { SCREEN_BY_PIN[SCREEN_FUNC[name]] = name; });
  /* 统一 tooltip 规则：只显示「名称」；丝印名不同时用括号带功能别名（如 SCL (CLK)）。
     不显示 kind/ref/型号/尺寸。 */
  const TIP_NAMES = {
    J3: { zh: 'TF 卡槽', en: 'TF card slot' },
    'USB-C1': { zh: 'Type-C', en: 'USB-C' },
    LAN1: { zh: '网口', en: 'Ethernet' },
    D1: { zh: 'SYS', en: 'SYS' },
    D2: { zh: 'LED1', en: 'LED1' },
    K1: { zh: 'MASK', en: 'MASK' },
    K2: { zh: 'USR', en: 'USR' },
    'sw:toggle': { zh: '拨动开关', en: 'Toggle switch' },
    'sw:A': { zh: '开关焊脚 A', en: 'Switch pin A' },
    'sw:B': { zh: '开关焊脚 B', en: 'Switch pin B' },
    'sw:C': { zh: '开关焊脚 C', en: 'Switch pin C' },
    'screen:flip': { zh: "1.54'IPS ST7789", en: "1.54'IPS ST7789" },
    'neo3-plus_PCB': { zh: 'NanoPi NEO3 Plus', en: 'NanoPi NEO3 Plus' },
    'neo3-plus_silkscreen': { zh: 'NanoPi NEO3 Plus', en: 'NanoPi NEO3 Plus' },
    holes: { zh: 'NanoPi NEO3 Plus', en: 'NanoPi NEO3 Plus' },
  };
  const PLUG_NAMES = { microsd: 'microSD', rj45: 'RJ45', usba: 'USB-A', usbc: 'USB-C' };
  function tipLang() {
    return (typeof LANG !== 'undefined' && LANG === 'en') ? 'en' : 'zh';
  }
  function nameOf(v) {
    return typeof v === 'string' ? v : (v[tipLang()] || v.zh);
  }
  const HEADER_PIN_MODEL = 'assets/board/models/排针.fbx';
  /** 单针排针模型的单位：几何包围盒 254×1154×254 = 2.54×11.54×2.54mm → 0.01mm/单位。 */
  const HEADER_PIN_SCALE = 0.01;

  /** 屏幕 / 开关两件 FBX + 单针排针原型：加载后替换抽象占位盒。
   *  主板外观由 neo3-plus-abstract.fbx 提供；
   *  主板/屏幕排针隐藏，改用单针模型逐针重建（可单针高亮）。 */
  const SCREEN_FIT = 28.2 * 2.54 / 1.8557;
  function loadFBXModels(V, onProgress) {
    const base = 'assets/board/models/';
    /* 五个模型并行加载：按「已完成文件数 + 当前文件字节比」汇总成 0..1 */
    const total = 5;
    const track = (i) => (loaded, size) => {
      if (typeof onProgress !== 'function') return;
      const frac = size > 0 ? Math.min(1, loaded / size) : 0;
      onProgress((i + frac) / total);
    };
    return Promise.all([
      V.loadFBX(base + 'board-screen-pins.fbx', { fit: SCREEN_FIT, at: [170, 0, 115] }, track(0)),
      V.loadFBX(base + 'switch_减面.fbx', { fit: 10, at: [90, 0, 115] }, track(1)),
      V.loadFBXModel(base + '排针.fbx', track(2)),
      V.loadFBXModel(base + 'neo3-plus-abstract.fbx', track(3)),
      V.loadFBXModel(base + 'rj45.fbx', track(4)).catch((e) => { console.warn('rj45.fbx load failed, keep original LAN1', e); return null; }),
    ]).then(([screen, sw, pinProto, boardFbx, rj45]) => {
      checkSwitchSize(sw);
      fixScreenMaterials(screen);
      fixHeaderBackface(screen);
      hideScreenSilk(screen);
      const TH = window.THREE;
      screen.updateMatrixWorld(true);
      const sb = new TH.Box3().setFromObject(screen);
      const hingeZ = sb.min.z;                 // 8 针排针所在边
      screenHeight = sb.max.y - sb.min.y;
      screenHingeZ = hingeZ;
      const pivot = new TH.Group();
      pivot.position.set(170, 0, hingeZ);
      screen.parent.add(pivot);
      pivot.attach(screen);                    // 保持原世界位姿，再绕排针边翻
      screenPinsGroup = new TH.Group();
      pivot.add(screenPinsGroup);
      V.setScreenPinGroup(screenPinsGroup);
      V.addScreenBackSilk(pivot);
      V.addScreenFrontSilk(pivot);
      pivot.rotation.x = Math.PI;              // 背面朝上、翻到板北
      pivot.position.set(124, screenHeight, hingeZ);
      pivot.updateMatrixWorld(true);
      screenPivot = pivot; screenObj = screen; screenProto = pinProto; screenPose = 'north';
      syncScreenSilk();
      V.attachScreenDisplay(pivot, screen);
      if (wantScreenPose === 'over') setScreenPose('over', false);

      const swBox = new TH.Box3().setFromObject(sw);
      const swCenter = swBox.getCenter(new TH.Vector3());
      sw.position.z += SWITCH_Z - swCenter.z;  // 开关移到板左缘中线

      const boardSkin = makeBoardSkin(boardFbx);   // 48×48、四角孔的抽象板
      const lanPort = buildLanPort(boardFbx, rj45);
      if (boardSkin) {
        const skin = new TH.Group();
        skin.add(boardSkin);
        if (lanPort) skin.add(lanPort);
        V.addBoardSkin(skin);
      } else {
        V.addBoardSkin(null);
      }
      V.addSwitchParts(sw);                      // 滑柄 + 3 个焊脚可交互
      const silkAt = (text, ref, side) => {
        const b = V.boardPartBBox(ref);
        if (!b) return;
        const cx = (b.min[0] + b.max[0]) / 2;
        const cz = (b.min[2] + b.max[2]) / 2;
        let x = cx, z = cz;
        if (side === 'right') { x = b.max[0] + 2.8; z = cz; }
        else if (side === 'north') { z = b.min[2] - 1.5; }
        else if (side === 'south') { z = b.max[2] + 1.5; }
        V.addSilkLabel(text, x, z, BOARD_TOP_Y);
      };
      silkAt('SYS', 'D1', 'right');
      silkAt('LED1', 'D2', 'right');
      silkAt('MASK', 'K1', 'north');
      silkAt('USR', 'K2', 'north');
      silkAt('J3', 'J3', 'south');
      silkAt('USB-C1', 'USB-C1', 'south');
      silkAt('LAN1', 'LAN1', 'north');
      V.addSilkLabel('CON30', 123.5, 106.8, BOARD_TOP_Y);
      const made = buildPinHeaders(V, screen, pinProto);
      V.clearWiringModules();
      V.requestShadow();
      return { screen, sw, made, pivot };
    }).catch((e) => { console.warn('FBX load failed, keep mesh placeholders', e); return null; });
  }

  /** 减面版 switch 必须以 fit:10 落在和旧模型相同的尺寸（10 × 7.31 × 2.80mm）。 */
  function checkSwitchSize(sw) {
    const TH = window.THREE;
    if (!sw) return;
    const s = new TH.Box3().setFromObject(sw).getSize(new TH.Vector3());
    const ok = Math.abs(s.x - 10) < 0.06 && Math.abs(s.y - 7.31) < 0.06 && Math.abs(s.z - 2.80) < 0.06;
    if (!ok) console.warn('switch size mismatch', s.toArray().map((v) => +v.toFixed(3)));
  }

  /** 抽象板视觉：保留 FBX 里的 PCB、USB-A、LED、按键、丝印、
      J3 / USB-C1 / LAN1 和四角金环；只隐藏旧的 2×13 排针。 */
  function makeBoardSkin(root) {
    const TH = window.THREE;
    if (!root) return null;
    let pcb = null, best = 0;
    root.traverse((o) => {
      if (!o.isMesh) return;
      const b = new TH.Box3().setFromObject(o);
      const s = b.getSize(new TH.Vector3());
      const area = s.x * s.z;
      if (o.name === '=>01155' || (s.y < 3 && area > best)) { best = area; pcb = o; }
    });
    if (!pcb) return null;
    const replaced = /^(CON30|Silk_)/;
    root.traverse((o) => {
      if (!o.isMesh) return;
      o.visible = !replaced.test(o.name);
    });
    fixUsbA(root);
    root.scale.setScalar(0.01);
    root.updateMatrixWorld(true);
    const b = new TH.Box3().setFromObject(pcb);
    root.position.x += 100 - b.min.x;
    root.position.y += 0 - b.min.y;
    root.position.z += 100 - b.min.z;
    root.updateMatrixWorld(true);
    root.traverse((o) => { if (o.isMesh && o.visible) { o.castShadow = true; o.receiveShadow = true; } });
    window.__NK_BOARD_VISUAL = root;
    return root;
  }

  /** 取指定材质所在平面的世界 Y（rj45.fbx 的 PCB_Dark 是贴合 PCB 的平面）。 */
  function materialPlaneY(obj, name) {
    const TH = window.THREE;
    let y = null;
    obj.updateMatrixWorld(true);
    obj.traverse((o) => {
      if (!o.isMesh || !o.geometry) return;
      const mats = Array.isArray(o.material) ? o.material : [o.material];
      const want = new Set();
      mats.forEach((m, i) => { if (m && m.name === name) want.add(i); });
      if (!want.size) return;
      const geo = o.geometry, pos = geo.attributes.position, idx = geo.index;
      const groups = (geo.groups && geo.groups.length)
        ? geo.groups
        : [{ start: 0, count: idx ? idx.count : pos.count, materialIndex: 0 }];
      const v = new TH.Vector3();
      groups.forEach((g) => {
        if (!want.has(g.materialIndex)) return;
        for (let i = g.start; i < g.start + g.count; i++) {
          const vi = idx ? idx.getX(i) : i;
          v.fromBufferAttribute(pos, vi).applyMatrix4(o.matrixWorld);
          if (y === null || v.y < y) y = v.y;
        }
      });
    });
    return y;
  }

  /** 用 rj45.fbx 替换抽象板里的 LAN1：
   *  - 隐藏原 LAN1 的 4 个网格
   *  - 新模型开口朝 +X，开口面/宽度对齐旧 LAN1
   *  - 深度保留真实 21.4mm；PCB_Dark 平面贴板顶，针脚穿到板下
   */
  function buildLanPort(board, rj45) {
    if (!board || !rj45) return null;
    const TH = window.THREE;
    board.updateMatrixWorld(true);
    const old = [];
    board.traverse((o) => {
      if (o.isMesh && /^LAN1/.test(o.name || '')) old.push(o);
    });
    if (!old.length) return null;
    const oldBox = new TH.Box3();
    old.forEach((o) => { o.updateMatrixWorld(true); oldBox.expandByObject(o); });
    old.forEach((o) => { o.visible = false; o.raycast = () => {}; });

    rj45.updateMatrixWorld(true);
    const nb = new TH.Box3().setFromObject(rj45);
    const ns = nb.getSize(new TH.Vector3());
    const os = oldBox.getSize(new TH.Vector3());
    if (!ns.x || !ns.y || !ns.z) return null;

    const wrap = new TH.Group();
    wrap.name = 'LAN1';
    wrap.add(rj45);
    /* 深度保留真实 21.4mm（不压到旧 LAN1 的 16.05），宽度对齐旧 LAN1，高度 16.5mm */
    wrap.scale.set(21.4 / ns.x, 16.5 / ns.y, os.z / ns.z);
    wrap.updateMatrixWorld(true);
    const nb2 = new TH.Box3().setFromObject(wrap);
    const oc = oldBox.getCenter(new TH.Vector3());
    const nc = nb2.getCenter(new TH.Vector3());
    const seat = materialPlaneY(wrap, 'PCB_Dark');
    wrap.position.x += oldBox.max.x - nb2.max.x;       // 开口面贴旧 LAN1 外缘
    /* PCB_Dark 平面贴板顶；针脚/屏蔽脚自然穿到板下（3mm） */
    wrap.position.y += BOARD_TOP_Y - (seat == null ? nb2.min.y + 3.0 : seat);
    wrap.position.z += oc.z - nc.z;                    // 宽度居中
    wrap.updateMatrixWorld(true);
    wrap.traverse((o) => {
      if (!o.isMesh) return;
      o.name = 'LAN1';
      o.castShadow = true;
      o.receiveShadow = true;
    });
    return wrap;
  }

  /** 用单针模型重建两处排针：主板 2×13、屏幕 1×8。
   *  - 缩放固定 0.01（模型单位 0.01mm），所以针距 2.54、针体 0.64、塑料体 2.54×2.54。
   *  - **主板排针要翻 180°**：模型的塑料体偏向针尾一侧，不翻会让长针朝下、只露出 3mm；
   *    翻过来后长针朝上（针顶 10.05mm ≈ 原模型 10.135mm），短尾穿过 PCB。
   *  - 屏幕保持原方向（排针在 PCB 背面，长针朝下），与屏幕原模型一致。
   *  - 建好后隐藏自产 CON30 与屏幕 SCREEN_PIN/HEADER_BODY。 */
  function buildPinHeaders(V, screen, proto) {
    const TH = window.THREE;
    if (!proto) return 0;
    V.clearPinInstances();
    V.clearScreenPinInstances();
    proto.updateMatrixWorld(true);
    const flipped = proto.clone(true);
    flipped.rotation.x = Math.PI;
    flipped.updateMatrixWorld(true);
    const fb = new TH.Box3().setFromObject(flipped);
    const fpc = fb.getCenter(new TH.Vector3());
    const flipPlasticBottom = protoPlasticBottom(flipped) * HEADER_PIN_SCALE;
    const flipCenterY = fpc.y * HEADER_PIN_SCALE;
    const place = (target, flip) => {
      const inst = proto.clone(true);
      if (flip) inst.rotation.x = Math.PI;
      inst.scale.setScalar(HEADER_PIN_SCALE);
      inst.updateMatrixWorld(true);
      const bb = new TH.Box3().setFromObject(inst);
      const c = bb.getCenter(new TH.Vector3());
      inst.position.x += target.x - c.x;
      inst.position.y += target.y - c.y;
      inst.position.z += target.z - c.z;
      inst.updateMatrixWorld(true);
      return inst;
    };
    const pads = ((window.NK_BOARD_PARTS || {}).parts || []).find((p) => p.ref === 'CON30');
    /* 主板：翻转后塑料体底面坐在 PCB 顶面。 */
    const boardCenterY = boardTopY() - (flipPlasticBottom - flipCenterY);
    let made = 0;
    ((pads && pads.pads) || []).forEach((pad) => {
      const n = parseInt(pad.n, 10);
      const inst = place({ x: 100 + pad.x, y: boardCenterY, z: 100 + pad.y }, true);
      V.addPinInstance(inst, 'CON30_PIN' + String(n).padStart(2, '0'), 'pin:' + n);
      made++;
    });
    const sp = screenPinPoints(screen);
    SCREEN_PHYS.forEach((phys, i) => {
      const n = i + 1;
      const p = sp[n];
      if (!p) return;
      const inst = place(p, true);    // 屏幕背面朝上时 8 针长针朝上；翻面由 screenPivot 带动
      V.addPinInstance(inst, 'SCR_PIN' + String(n).padStart(2, '0'), 'scr:' + SCREEN_BY_PIN[n], screenPinsGroup);
      made++;
    });
    hideScreenHeader(screen);
    return made;
  }

  /** 单针模型里黑色塑料组（materialIndex 0）的最低 y（模型单位）。 */
  function protoPlasticBottom(proto) {
    let min = Infinity;
    const v = new window.THREE.Vector3();
    proto.traverse((o) => {
      if (!o.isMesh || !o.geometry) return;
      const g = o.geometry, pos = g.attributes.position, idx = g.index;
      const grp = (g.groups || []).find((x) => x.materialIndex === 0) || (g.groups || [])[0];
      if (!grp) return;
      for (let i = grp.start; i < grp.start + grp.count; i++) {
        const vi = idx ? idx.getX(i) : i;
        v.fromBufferAttribute(pos, vi).applyMatrix4(o.matrixWorld);
        if (v.y < min) min = v.y;
      }
    });
    return min === Infinity ? 0 : min;
  }

  /** 屏幕 8 针 + 塑料体让位给单针模型；PCB/玻璃/丝印/焊盘都保留。 */
  function hideScreenHeader(screen) {
    if (!screen) return;
    screen.traverse((o) => {
      if (o.isMesh && (o.name === 'SCREEN_HEADER_BODY' || /^SCREEN_PIN\d+$/.test(o.name))) o.visible = false;
    });
  }

  /** USB-A 的 FBX 导出成多材质但 groups 为空 → Three 会漏画成破面；
   *  收成第一个（黑塑料）材质并开双面，保持双面材质。 */
  function fixUsbA(root) {
    const TH = window.THREE;
    root.traverse((o) => {
      if (!o.isMesh || !/USB1/i.test(o.name)) return;
      o.frustumCulled = false;
      o.visible = true;
      if (o.geometry) o.geometry.computeBoundingSphere();
      const mats = Array.isArray(o.material) ? o.material : [o.material];
      const black = mats[0] && mats[0].clone();
      if (black) {
        black.side = TH.DoubleSide;
        black.needsUpdate = true;
        o.material = black;
      }
    });
  }

  /** 背面看排针塑料体时，FBX 法线朝正面会被背面剔除；只给这个件开双面。 */
  function fixHeaderBackface(screen) {
    const TH = window.THREE;
    screen.traverse((o) => {
      if (o.isMesh && /HEADER_BODY/i.test(o.name)) {
        o.frustumCulled = false;
        o.visible = true;
        if (o.geometry) o.geometry.computeBoundingSphere();
        /* FBX 导出成多材质但 groups 为空 → Three 不画；原 body 本来主要是黑塑料，收成单材质即可。 */
        const mats = Array.isArray(o.material) ? o.material : [o.material];
        const black = mats[0];
        if (black) { black.side = TH.DoubleSide; black.needsUpdate = true; o.material = black; }
      }
    });
  }

  /** 屏幕 PCB 上色（统一丝印平面）。 */
  function fixScreenMaterials(screen) {
    screen.traverse((o) => {
      if (!o.isMesh) return;
      const n = (o.name || '').toUpperCase();
      const mats = Array.isArray(o.material) ? o.material : [o.material];
      mats.forEach((m) => {
        if (!m) return;
        if (n.includes('PCB')) {
          m.color.setHex(0x02294b); m.map = null; m.needsUpdate = true;
        } else if (n.includes('LABEL')) {
          m.color.setHex(0xe8e8e2); m.needsUpdate = true;
        }
      });
    });
  }

  /** 隐藏 FBX 自带的正面丝印贴图，统一用运行时丝印平面。 */
  function hideScreenSilk(screen) {
    if (!screen) return;
    screen.traverse((o) => {
      if (o.isMesh && o.name === 'SCREEN_SILK_TEX') {
        o.visible = false;
        o.raycast = () => {};
      }
    });
  }

  /** 占位盒：FBX 加载失败时仍能看见屏幕/开关的位置；加载成功后由 clearWiringModules 清掉。 */
  function addPlaceholders(V) {
    V.addWiringModule({ x: 170, z: 115, w: 28, d: 38, h: 1.6, color: 0x12384f, top: [26, 26] });
    V.addWiringModule({ x: 90, z: 115, w: 10, d: 10, h: 6, color: 0x22262c });
  }

  /** 屏幕 8 针的真实世界坐标：按 FBX 节点名取包围盒中心，改缩放/摆位后自动跟着走。 */
  function screenPinPoints(screen) {
    const out = [];
    if (!screen) return out;
    screen.updateMatrixWorld(true);
    screen.traverse((o) => {
      const m = /^SCREEN_PIN(\d+)$/.exec(o.name || '');
      if (!m || !o.isMesh) return;
      const b = new window.THREE.Box3().setFromObject(o);
      const c = b.getCenter(new window.THREE.Vector3());
      out[parseInt(m[1], 10)] = { x: +c.x.toFixed(3), y: +c.y.toFixed(3), z: +c.z.toFixed(3) };
    });
    return out;
  }

  /** 3D 接线板：把 2D 里的端点搬到 3D。
   *  - 主板 26 针：pin:1..26
   *  - 屏幕 8 针：scr:VCC/GND/BLK/CLK/MOSI/CS/DC/RST（有 FBX 时取它的真实针位）
   *  - 开关 3 脚（从左到右 A-C-B）：中间 C 是公共端
   */
  function setupWiring(V, screen) {
    const top = boardTopY();
    const boardPinY = (V.pinTop && V.pinTop('CON30_PIN01')) || (top + 5.54);
    const pts = [];
    const part = ((window.NK_BOARD_PARTS || {}).parts || []).find((p) => p.ref === 'CON30');
    (part && part.pads ? part.pads : []).forEach((pad) => {
      pts.push({ id: 'pin:' + pad.n, x: 100 + pad.x, y: boardPinY, z: 100 + pad.y });
    });
    const sp = screenPinPoints(screen);
    SCREEN_FUNCS.forEach((name) => {
      const n = SCREEN_FUNC[name];
      const p = sp[n];
      /* 自由针尖（长针端）随屏幕旋转连续移动；不用会随翻转变错端的 pinTop */
      const tip = V.pinTip ? V.pinTip('SCR_PIN' + String(n).padStart(2, '0')) : null;
      pts.push({ id: 'scr:' + name,
        x: tip ? tip[0] : (p ? p.x : 161.11 + (n - 1) * 2.54),
        y: tip ? tip[1] : (p ? p.y + 5.77 : 11.54),
        z: tip ? tip[2] : (p ? p.z : 97.7),
        rot: screenPivot ? (screenPivot.rotation.x - Math.PI) : 0 });
    });
    pts.push({ id: 'sw:A', x: 88, y: 4.0, z: SWITCH_Z });
    pts.push({ id: 'sw:C', x: 90, y: 4.0, z: SWITCH_Z });
    pts.push({ id: 'sw:B', x: 92, y: 4.0, z: SWITCH_Z });
    V.addWiringEndpoints(pts);
  }

  function syncScreenSilk() {
    if (!screenPivot) return;
    const back = screenPivot.getObjectByName('SCREEN_BACK_SILK');
    const front = screenPivot.getObjectByName('SCREEN_FRONT_SILK');
    const face = screenPivot.getObjectByName('SCREEN_BACK_FACE');
    const north = screenPose === 'north';
    if (back) back.visible = north;
    if (face) face.visible = north;
    if (front) front.visible = !north;
  }

  /* 单条轨迹：直接绕排针边轴翻过去；反向 phase 递减，沿原轨迹返回。 */
  function applyScreenPhase(p) {
    if (!screenPivot) return;
    const e = p * p * (3 - 2 * p);             // easeInOut
    screenPivot.rotation.x = Math.PI * (1 + e); // π→2π：与上一版相反方向
    const y = screenHeight + (SCREEN_OVER_Y - screenHeight) * e;
    const z = screenHingeZ + (SCREEN_OVER_Z - screenHingeZ) * e;
    screenPivot.position.set(124, y, z);
    if (window.NKView3D && window.NKView3D.requestShadow) window.NKView3D.requestShadow();
    screenPivot.updateMatrixWorld(true);
  }
  function setScreenPose(pose, animate) {
    const target = pose === 'over' ? 'over' : 'north';
    wantScreenPose = target;
    if (!screenPivot || !screenObj) return screenPose;
    const targetPhase = target === 'over' ? 1 : 0;
    if (target === screenPose && (screenAnim || screenPhase === targetPhase)) return screenPose;
    screenPose = target;
    screenTarget = targetPhase;
    syncScreenSilk();
    if (animate === false) {
      if (screenAnim) { cancelAnimationFrame(screenAnim); screenAnim = 0; }
      screenPhase = targetPhase;
      applyScreenPhase(screenPhase);
      if (window.NKView3D && window.NKView3D.requestShadow) window.NKView3D.requestShadow();
      if (window.NKView3D && screenObj) setupWiring(window.NKView3D, screenObj);
      return screenPose;
    }
    if (screenAnim) return screenPose;         // 正在走同一条轨迹，只改目标方向
    screenAnimLast = performance.now();
    const tick = (now) => {
      const dt = Math.min(64, Math.max(1, now - screenAnimLast));
      screenAnimLast = now;
      const step = dt / 650;
      if (screenPhase < screenTarget) screenPhase = Math.min(screenTarget, screenPhase + step);
      else if (screenPhase > screenTarget) screenPhase = Math.max(screenTarget, screenPhase - step);
      applyScreenPhase(screenPhase);
      if (window.NKView3D) {
        /* 端点/线帽/线束跟着屏幕的当前角度逐帧更新，而不是只在动画结束跳一次 */
        setupWiring(window.NKView3D, screenObj);
        if (window.NKView3D.requestShadow) window.NKView3D.requestShadow();
        window.NKView3D.renderOnce();
      }
      if (screenPhase !== screenTarget) {
        screenAnim = requestAnimationFrame(tick);
      } else {
        screenAnim = 0;
        if (window.NKView3D && screenObj) setupWiring(window.NKView3D, screenObj);
      }
    };
    screenAnim = requestAnimationFrame(tick);
    return screenPose;
  }
  function toggleScreenPose() {
    return setScreenPose(screenPose === 'north' ? 'over' : 'north', true);
  }

  const plugTargets = { tfcard: false, power: false, lan: false };
  const plugProgress = { tfcard: 0, power: 0, lan: 0 };
  let plugRAF = 0, plugLast = 0;
  /* TF 卡"待烧录"态：卡停在卡槽外，可点击打开烧录终端；烧录完才允许插进卡槽。 */
  let tfParked = false, tfFlashed = false;
  const TF_PARK_PUSH = 25;              // 插入全过程从槽外 25mm 滑到槽底

  function plugAt(prop) {
    const pid = plugMeshKey(prop), cfg = PLUG_AT[pid];
    const placed = (window.NK_PLUG_PLACEMENT || {})[pid];
    if (placed) {
      const at = placed.at.slice();
      if (pid === 'rj45') at[1] += RJ45_PLUG_DY;
      return { pid, cfg: { rot: placed.rot == null ? cfg.rot : placed.rot, roll: placed.roll == null ? (cfg.roll || 0) : placed.roll, out: placed.out }, at };
    }
    const ref = REF_FOR_PROP[prop];
    const host = byRef.get(ref);
    const hb = window.NKView3D.boardPartBBox(ref);
    if (hb) {
      /* 用**插座真实包围盒**对齐，而不是零件位姿里的 y：卧式座子的开口在板外，
         插头中心必须落在座子高度/深度中心，否则会浮在板下或插进板里。 */
      const cy = (hb.min[1] + hb.max[1]) / 2;
      const cz = (hb.min[2] + hb.max[2]) / 2;
      let x;
      if (pid === 'microsd') x = hb.max[0] - 7.5;          // 卡宽 15，插到槽底
      else if (pid === 'usbc') x = hb.max[0] - 4.5 + 3.325; // Type-C 长 6.65，插入 4.5
      else x = hb.max[0] - 6.0 + 4.5;                      // RJ45 长 9，插入 6
      return { pid, cfg, at: [x, cy, cz] };
    }
    const y = host ? host.y : 24;
    return { pid, cfg, at: [FRAME.ORIGIN_MM + cfg.x, holesY + 0.4, FRAME.ORIGIN_MM + y + cfg.dz] };
  }

  function applyPlug(prop) {
    const { pid, cfg, at } = plugAt(prop);
    const t = plugProgress[prop];
    const parked = pid === 'microsd' && tfParked && !plugTargets[prop] && t < 0.001;
    window.NKView3D.setPlug(pid, {
      visible: t > 0.001 || plugTargets[prop] || parked,
      rotDeg: cfg.rot, rollDeg: cfg.roll || 0, out: cfg.out, at, insert: parked ? 0 : t,
      push: pid === 'microsd' ? TF_PARK_PUSH : undefined,
    });
  }

  function tickPlugs(now) {
    if (!plugLast) plugLast = now - 16;
    const dt = Math.min(0.05, (now - plugLast) / 1000);
    plugLast = now;
    const step = dt / 0.38;
    let moving = false;
    Object.keys(plugTargets).forEach((prop) => {
      const target = plugTargets[prop] ? 1 : 0;
      const cur = plugProgress[prop];
      let next = cur;
      if (cur < target) next = Math.min(target, cur + step);
      else if (cur > target) next = Math.max(target, cur - step);
      if (next !== cur) { plugProgress[prop] = next; moving = true; }
      applyPlug(prop);
    });
    if (moving) plugRAF = requestAnimationFrame(tickPlugs);
    else { plugRAF = 0; plugLast = 0; }
  }

  function setTfCard(opts) {
    const parked = !!(opts && opts.parked);
    tfFlashed = !!(opts && opts.flashed);
    if (parked !== tfParked) tfParked = parked;
    if (plugRAF) return;              // 插入动画中由 tick 每帧重算
    applyPlug('tfcard');
  }

  function setPlugged(map) {
    map = map || {};
    let changed = false;
    Object.keys(plugTargets).forEach((prop) => {
      const t = !!map[prop];
      if (t !== plugTargets[prop]) { plugTargets[prop] = t; changed = true; }
    });
    if (changed && !plugRAF) { plugLast = 0; plugRAF = requestAnimationFrame(tickPlugs); }
    else if (!plugRAF) Object.keys(plugTargets).forEach(applyPlug);
  }

  window.NKBOARD3D = {
    mounted: () => mounted,
    parts: () => parts.slice(),
    screenPose: () => screenPose,
    screenAngle: () => screenPivot ? screenPivot.rotation.x : null,
    setScreenPose,
    toggleScreenPose,
    setScreenBoot(on, opts) { return window.NKView3D.setScreenBoot(on, opts); },
    setScreenCue(on) { return window.NKView3D.setScreenCue(on); },

    async mount(cont, partList, lang) {
      container = cont;
      parts = partList || [];
      byRef = new Map(parts.map((p) => [p.ref, p]));
      if (!inited) {
        const V = window.NKView3D;
        V.mount(cont);
        const pm = window.NK_PLUG_MESHES || {};
        Object.keys(pm).forEach((k) => V.addPlug(k, pm[k]));
        V.onPick((ref, cx, cy, kind) => {
          /* 3D 接线板：端点（板针/屏排针/开关脚/插座）直接交给接线状态机 */
          if (kind === 'click' && typeof window.__nk3dEndpoint === 'function') {
            if (/^(pin:|scr:|sw:)/.test(ref || '')) window.__nk3dEndpoint(ref);
            else if (/^CON30_PIN\d+/.test(ref || '')) window.__nk3dEndpoint('pin:' + ref.slice(9));
            else if (PROP_OF_REF[ref]) window.__nk3dEndpoint(PROP_OF_REF[ref]);
            else if (ref === 'K2') window.__nk3dEndpoint('btn:usr');
            else if (ref === 'screen:flip') window.__nk3dEndpoint('screen:flip');
            else if (ref === 'plug:microsd') window.__nk3dEndpoint('tfcard:card');
            else if (/^plug:/.test(ref || '')) {
              const p = PLUG_OF_PROP[ref.slice(5)];
              if (p) window.__nk3dEndpoint(p);
            }
          }
          showTip(ref, cx, cy);
          if (kind === 'click') window.__nk3dPick = ref;
        });
        addPlaceholders(V);
        setupWiring(V);                       // 先给端点，FBX 未到也能接线
        const onP = typeof window.__nkLoadAssets === 'function' ? window.__nkLoadAssets : null;
        const models = await loadFBXModels(V, onP);
        if (models && models.screen) setupWiring(V, models.screen);   // 用屏幕 FBX 的真实针位重算屏幕端点
        V.frame('wiring');
        inited = true;
      }
      mounted = true;
      window.__nk3dReady = true;
      return true;
    },
    show() { window.NKView3D.show(); },
    hide() { window.NKView3D.hide(); },
    resize() { window.NKView3D.resize(); },
    abstract() { return abstract; },
    /** 外观已经由抽象 FBX 提供；自产网格只负责 J3/USB-C1/LAN1 的高亮，
        不再用「完整」把自产整板亮出来（会与 FBX 的 USB-A/LED/按键 重影）。 */
    toggleAbstract() {
      abstract = !abstract;
      return abstract;
    },

    /** 当前步骤的端点 → 板载 refdes（pin:* 全在 26 针排针上；插座走 prop:） */
    setStepRefs(edges) {
      const set = [];
      let plugHl = null;
      (edges || []).forEach((ep) => {
        if (!ep) return;
        if (ep.startsWith('pin:')) {
          const n = parseInt(ep.slice(4), 10);
          /* 不再回退到整块 CON30：无效 pin 就不高亮，避免“整排亮” */
          if (n >= 1 && n <= 26) set.push('CON30_PIN' + String(n).padStart(2, '0'));
        }
        else if (ep.startsWith('scr:')) {
          const n = SCREEN_FUNC[ep.slice(4)];
          if (n) set.push('SCR_PIN' + String(n).padStart(2, '0'));
        }
        else if (ep.startsWith('prop:')) {
          const prop = ep.slice(5);
          /* 烧录环节：停靠在外面的 TF 卡才是要点的东西，卡槽此时不亮 */
          if (prop === 'tfcard' && tfParked && !tfFlashed) { plugHl = 'microsd'; return; }
          const r = REF_FOR_PROP[prop];
          if (r) set.push(r);
        }
        else if (/^sw:[ABC]$/.test(ep)) set.push(ep);
      });
      if (window.NKView3D.setPlugHighlight) window.NKView3D.setPlugHighlight(plugHl);
      return window.NKView3D.setHighlight(Array.from(new Set(set)));
    },
    setHighlight(refs) { return window.NKView3D.setHighlight(refs || []); },
    setWires(wires, opts) { return window.NKView3D.setWires(wires || [], opts); },
    previewJoint(id, transient) { return window.NKView3D.previewJoint(id, transient); },
    clearPreview() { return window.NKView3D.clearPreview(); },
    setSwitchState(on) { return window.NKView3D.setSwitchState(on); },
    sparkEndpoints(ids) { return window.NKView3D.sparkEndpoints(ids); },
    highlight() { return window.NKView3D.highlight(); },

    /** 插拔步骤完成 → 摆上/拿掉插头 */
    setPlugged(map) { setPlugged(map); },
    /** TF 卡待烧录：显示在卡槽外，点击打开烧录终端 */
    setTfCard(opts) { setTfCard(opts); },
    plugDebug() {
      const bounds = {}, hosts = {}, materials = {};
      Object.keys(PLUG_OF).forEach((prop) => {
        bounds[prop] = window.NKView3D.plugBBox(PLUG_OF[prop]);
        hosts[prop] = window.NKView3D.boardPartBBox(REF_FOR_PROP[prop]);
        materials[prop] = window.NKView3D.plugMaterialStats(PLUG_OF[prop]);
      });
      return { targets: { ...plugTargets }, progress: { ...plugProgress }, animating: !!plugRAF, parked: tfParked, bounds, hosts, materials };
    },

    focus(ref) { return window.NKView3D.focus(ref); },
    focusEndpoints(ids, opts) { return window.NKView3D.focusEndpoints(ids || [], opts); },
    endpointPoint(id) { return window.NKView3D.endpointPoint(id); },
    setPulse(on) { return window.NKView3D.setPulse(on); },
    setPlugHighlight(id) { return window.NKView3D.setPlugHighlight(id); },
    annotationAnchors() { return window.NKView3D.annotationAnchors(); },
    onViewChange(fn) { return window.NKView3D.onCameraChange(fn); },
    frame(kind) { window.NKView3D.frame(kind); },
    debug() {
      const d = window.NKView3D.debug();
      return Object.assign(d, { meshParts: 0, abstract, lib: 'NKView3D' });
    },
  };
})();
