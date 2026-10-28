'use strict';
/* NoKeyOut · 3D 看板渲染器（**three.js** + 自用紧凑网格；**不读 GLB、不用 GLTFLoader**）
 *
 * 数据来源：已烘焙的板件/插拔物紧凑网格。
 * 运行时只做：量化 int16/int8 → Float32 → THREE.BufferGeometry，然后按件建 Mesh。
 *
 * 世界单位 = **毫米**；坐标系与 KiCad 一致（板左上角在 (100,100)，Y 向上）。
 * 相机/轨道/拾取都是自己写的（OrbitControls 也不引，省一个文件）：
 *   * 轨道：指针拖拽（yaw/pitch）+ 滚轮/双指缩放
 *   * 拾取：THREE.Raycaster 打每件自己的 Mesh → userData.ref
 *
 * 公开 API（window.NKView3D）与上一版一致，所以两个页面不用改调用方式：
 *   mount(cont) setMesh(m) setPhoto(url, rect) setPhotoRect(r) photoRect() setMeshVisible(v)
 *   addPlug(id, m) setPlug(id, {visible, at, rotDeg}) onPick(fn)
 *   setHighlight(refs) highlight() focus(ref) frame(kind) show() hide() resize() debug()
 */
(function () {
  let renderer, scene, camera, container, canvas, root3d, meshGroup, photoMesh, plugGroup, decoGroup;
  let wiringGroup, endpointGroup, moduleGroup, headerGroup, boardSkinGroup, switchGroup, screenPinGroup = null, endpointAt = new Map(), fbxModels = [];
  let parts = [], ranges = [], byRef = new Map(), meshes = new Map();   // ref -> Mesh
  const hlPins = new Map();                                            // ref -> [{m, base}] 单针实例
  let pulsePhase = null, pulseRAF = 0, pulseWant = false, pulseLast = 0;  // 接线高亮呼吸
  let plugHlId = null;
  let plugHl = [];                             // [{m, base}] 被高亮的插拔件材质
  const hlParts = new Map();                                           // ref -> [{m, base}] FBX 主板件

  let hlRefs = [], hoverRef = null, pickFn = null, pickRay = null;
  let hlSig = '', wireSig = '';
  let wireList = [];
  let switchToggleMesh = null, switchState = false;
  const silkLabels = [];
  const camCbs = [];
  let annoAnchorsCache = null;
  let switchBaseLocalX = 0, switchBaseWorldX = 0, switchMirrorLocalX = 0, switchAxisX = 0;
  /* 排针露出黑色塑料 6.0mm；母头外壳 7.0mm，插到底后底沿正好压在塑料件顶面。 */
  const DUPONT_H = 7.0, DUPONT_W = 2.3, DUPONT_INSERT = 6.0;
  const DUPONT_TAIL = DUPONT_H - DUPONT_INSERT + 0.7;
  let sparkGroup = null, sparkBursts = [], sparkRAF = 0, sparkLast = 0, sparkGeo = null, sparkGlowGeo = null;
  let previewGroup = null, previewIds = [];
  let wireGrow = null, wireGrowRAF = 0;
  const inserting = new Set();
  const endpointRot = new Map();
  let screenDisplay = null, screenPickGroup = null, screenGlass = null;
  let screenPageName = 'main';
  let screenAdminAt = 0;    // 端口开放起点（Date.now()；0=未开放）
  let screenAdminTimer = 0; // ADMIN 页 4Hz 刷新计时/闪烁
  let screenCueOn = false, screenCueLevel = 0, cueRAF = 0, cueStart = 0;
  const screenHlParts = [];
  let screenBootOn = false, screenBootOpts = {}, screenAnimRAF = 0;
  const SCR_W = 240, SCR_H = 240;
  let screenFrameCanvas = null, screenFrameCtx = null, screenFrameTex = null;
  let screenBaseCanvas = null, screenBaseCtx = null;
  let screenWasmCanvas = null, screenWasmReady = false, screenWasmLoading = false, screenWasmPage = '';
  let screenLastAnim = null;
  let screenPageDirty = true;
  let photoRect = null, photoMat = null, meshVisible = true, ready = false;
  let renderDirty = true, rafOn = false, renderCount = 0, lastRenderMs = 0;
  let baseDpr = Math.min(window.devicePixelRatio || 1, 1.5);
  let cam = { yaw: -35, pitch: 34, dist: 130, target: [124, 6, 124] };
  let camAnim = 0;
  const T = () => window.THREE;
  const TINTS = {};
  function tintFor(hov) {
    const k = hov ? 'hover' : 'base';
    if (!TINTS[k]) TINTS[k] = hov ? new (T().Color)(1, 0.82, 0.48) : new (T().Color)(0.31, 0.56, 0.97);
    return TINTS[k];
  }
  let pickNdc = null;
  /* 屏幕物理 pin 顺序/功能映射的唯一出处：assets/board/board-frame.js */
  const SCREEN_MAP = window.NK_SCREEN_PINS || {
    phys: ['GND', 'VCC', 'SCL', 'SDA', 'RES', 'DC', 'CS', 'BLK'],
    func: { VCC: 2, GND: 1, BLK: 8, CLK: 3, MOSI: 4, CS: 7, DC: 6, RST: 5 },
  };
  const SCREEN_PHYS = SCREEN_MAP.phys;
  const SCREEN_FUNC = SCREEN_MAP.func;
  /* 所有运行时补的丝印统一字高（mm）；主板 SYS/LED1/MASK/USR 与屏幕背面 8 针都用它。 */
  const SILK_TEXT_MM = 0.9375;
  const VIEW_FOV = 38;                     // 正交视锥按原透视 38° 的取景范围换算
  function hoverRefFor(ref) {
    if (!ref) return null;
    if (/^pin:\d+$/.test(ref)) return 'CON30_PIN' + String(ref.slice(4)).padStart(2, '0');
    if (ref.startsWith('scr:')) {
      const n = SCREEN_FUNC[ref.slice(4)];
      if (n) return 'SCR_PIN' + String(n).padStart(2, '0');
    }
    if (ref.startsWith('plug:')) return null;
    return ref;
  }
  function refForMesh(name) {
    if (!name) return null;
    if (/^J3/.test(name)) return 'J3';
    if (/^USB-C1/.test(name)) return 'USB-C1';
    if (/^LAN1/.test(name)) return 'LAN1';
    if (/^USB1/.test(name)) return 'USB1';
    if (/^K1/.test(name)) return 'K1';
    if (/^K2/.test(name)) return 'K2';
    if (/^D1/.test(name)) return 'D1';
    if (/^D2/.test(name)) return 'D2';
    if (name === '=>01155') return 'neo3-plus_PCB';
    if (/^Silk_/.test(name)) return 'neo3-plus_silkscreen';
    return null;
  }

  /* ---------------- 量化数组 → three 几何 ---------------- */
  function b64To(s, Type) {
    const bin = atob(s);
    const u8 = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) u8[i] = bin.charCodeAt(i);
    return new Type(u8.buffer, 0, u8.byteLength / Type.BYTES_PER_ELEMENT);
  }

  /** 移除并释放一组对象；geometry 共享时（单针实例）只释放克隆材质。 */
  function clearGroup(g, opt) {
    const o = opt || {};
    for (let i = g.children.length - 1; i >= 0; i--) {
      const child = g.children[i];
      g.remove(child);
      child.traverse((c) => {
        if (!c.isMesh) return;
        if (o.geo !== false && c.geometry) c.geometry.dispose();
        if (o.mat !== false) {
          const mats = Array.isArray(c.material) ? c.material : [c.material];
          mats.forEach((m) => { if (m) m.dispose(); });
        }
      });
    }
  }
  /* FBX 往往是非索引三角形（switch 有 133k 顶点 / 44k 三角）。按 position+normal+uv
     去重并建索引，三角形不变，但显存和顶点着色成本大幅下降。 */
  function dedupeGeometry(g) {
    if (!g || g.index || !g.attributes.position) return g;
    const TH = T();
    const pos = g.attributes.position, nrm = g.attributes.normal, uv = g.attributes.uv;
    const count = pos.count;
    const groups = (g.groups && g.groups.length) ? g.groups : [{ start: 0, count, materialIndex: 0 }];
    const map = new Map();
    const outPos = [], outNrm = [], outUv = [], index = [];
    const outGroups = [];
    const f = (v) => (Math.round(v * 1e5) / 1e5);
    for (const grp of groups) {
      const start = index.length;
      for (let i = grp.start; i < grp.start + grp.count; i++) {
        let key = f(pos.getX(i)) + ',' + f(pos.getY(i)) + ',' + f(pos.getZ(i));
        if (nrm) key += '|' + f(nrm.getX(i)) + ',' + f(nrm.getY(i)) + ',' + f(nrm.getZ(i));
        if (uv) key += '|' + f(uv.getX(i)) + ',' + f(uv.getY(i));
        let ni = map.get(key);
        if (ni === undefined) {
          ni = outPos.length / 3;
          map.set(key, ni);
          outPos.push(pos.getX(i), pos.getY(i), pos.getZ(i));
          if (nrm) outNrm.push(nrm.getX(i), nrm.getY(i), nrm.getZ(i));
          if (uv) outUv.push(uv.getX(i), uv.getY(i));
        }
        index.push(ni);
      }
      outGroups.push({ start, count: index.length - start, materialIndex: grp.materialIndex || 0 });
    }
    const ng = new TH.BufferGeometry();
    ng.setAttribute('position', new TH.BufferAttribute(new Float32Array(outPos), 3));
    if (nrm) ng.setAttribute('normal', new TH.BufferAttribute(new Float32Array(outNrm), 3));
    if (uv) ng.setAttribute('uv', new TH.BufferAttribute(new Float32Array(outUv), 2));
    ng.setIndex(index);
    if (!nrm) ng.computeVertexNormals();
    ng.groups = outGroups;
    ng.setDrawRange(0, index.length);
    ng.computeBoundingSphere();
    g.dispose();
    return ng;
  }

  function dedupeScene(root) {
    root.traverse((o) => { if (o.isMesh && o.geometry) o.geometry = dedupeGeometry(o.geometry); });
    return root;
  }

  /* 一个网格只解码一次：所有材质 range 共享 position/normal/index 属性。
     原实现每个 range 都重新解码整份数组（CON30 两个 range、整板 86 个 range 时
     会复制几十份 119k 顶点，启动和显存都被放大几十倍）。 */
  function meshAttributes(m) {
    if (m._attrs) return m._attrs;
    const TH = T();
    const i16 = b64To(m.pos, Int16Array), n8 = b64To(m.nrm, Int8Array), i32 = b64To(m.idx, Uint32Array);
    const nv = i16.length / 3;
    const pos = new Float32Array(nv * 3), nrm = new Float32Array(nv * 3);
    for (let i = 0; i < nv * 3; i++) {
      pos[i] = i16[i] * m.scale + m.origin[i % 3];      // 反量化回 mm
      nrm[i] = n8[i] / 127;
    }
    m._attrs = {
      pos: new TH.BufferAttribute(pos, 3),
      nrm: new TH.BufferAttribute(nrm, 3),
      idx: new TH.BufferAttribute(i32, 1),
      posArray: pos,
    };
    return m._attrs;
  }

  function geometryFor(m, p) {
    const TH = T();
    const a = meshAttributes(m);
    const g = new TH.BufferGeometry();
    g.setAttribute('position', a.pos);
    g.setAttribute('normal', a.nrm);
    g.setIndex(a.idx);
    if (p && p.ref === 'CON30') {
      if (!m._pinIds) m._pinIds = pinIdsAttr(a.posArray);
      if (m._pinIds) g.setAttribute('pinId', m._pinIds);
    }
    /* 包围球按整份网格算一次；drawRange 只影响绘制范围，不影响这个保守包围球。 */
    if (!m._sphere) { g.computeBoundingSphere(); m._sphere = g.boundingSphere; }
    else g.boundingSphere = m._sphere;
    return g;
  }

  /* CON30：给每个顶点标一个 1..26 的 pin id，供 shader 单针高亮。
     坐标用板面 mm：x = 100 + pad.x，z = 100 + pad.y。 */
  function pinIdsAttr(pos) {
    const TH = T();
    const all = (window.NK_BOARD_PARTS || { parts: [] }).parts || [];
    const part = all.find((x) => x.ref === 'CON30');
    const pads = part && part.pads;
    if (!pads || !pads.length) return null;
    const ids = new Float32Array(pos.length / 3);
    for (let i = 0; i < ids.length; i++) {
      const x = pos[i * 3], z = pos[i * 3 + 2];
      let best = 0, bd = Infinity;
      for (let k = 0; k < pads.length; k++) {
        const dx = x - (100 + pads[k].x), dz = z - (100 + pads[k].y);
        const d = dx * dx + dz * dz;
        if (d < bd) { bd = d; best = parseInt(pads[k].n, 10); }
      }
      ids[i] = best;
    }
    return new TH.BufferAttribute(ids, 1);
  }

  /* 单针高亮 = **整根针（上/下段）+ 该针那一格塑料 + 整块塑料件**。
     针脚按 pinId 亮；塑料没有逐针拓扑，用板面 x/z 的 2.54mm 方格定位那一格，
     同时整块塑料件给一个较低的蓝色底（单针位置仍然是更亮的那一格）。 */
  function pinShader(mat, plastic) {
    const pads = pinPadXZ();
    const u = { uPins: { value: new Float32Array(26) }, uOn: { value: 0 }, uAmp: { value: 1 },
                uPads: { value: pads }, uPlastic: { value: plastic ? 1 : 0 } };
    mat.userData.pinUniforms = u;
    mat.customProgramCacheKey = () => 'nk_pin_shader_v4';
    mat.onBeforeCompile = (shader) => {
      mat.userData.pinCompiled = true;
      shader.uniforms.uPins = u.uPins;
      shader.uniforms.uOn = u.uOn;
      shader.uniforms.uAmp = u.uAmp;
      shader.uniforms.uPads = u.uPads;
      shader.uniforms.uPlastic = u.uPlastic;
      shader.vertexShader = 'attribute float pinId;\nvarying float vPinId;\nvarying vec2 vBoardXZ;\n' +
        shader.vertexShader.replace('#include <begin_vertex>',
          '#include <begin_vertex>\n  vPinId = pinId;\n  vBoardXZ = position.xz;');
      shader.fragmentShader = 'uniform float uPins[26];\nuniform float uOn;\nuniform float uAmp;\nuniform vec2 uPads[26];\n' +
        'uniform float uPlastic;\nvarying float vPinId;\nvarying vec2 vBoardXZ;\n' +
        shader.fragmentShader.replace('#include <dithering_fragment>',
          '#include <dithering_fragment>\n' +
          '  float pinHit = 0.0;\n' +
          '  float cellHit = 0.0;\n' +
          '  for (int i = 0; i < 26; i++) {\n' +
          '    if (uPins[i] > 0.5) {\n' +
          '      pinHit += step(abs(vPinId - float(i + 1)), 0.5);\n' +
          '      vec2 d = abs(vBoardXZ - uPads[i]);\n' +
          '      if (d.x < 1.28 && d.y < 1.28) cellHit = 1.0;\n' +
          '    }\n' +
          '  }\n' +
          '  float k = max(pinHit, cellHit) * 0.78 * uAmp;\n' +
          '  k = max(k, uPlastic * step(0.5, uOn) * 0.5 * uAmp);\n' +
          '  if (k > 0.001) gl_FragColor.rgb = mix(gl_FragColor.rgb, vec3(0.31, 0.56, 0.97), k);');
    };
    mat.needsUpdate = true;
    return u;
  }

  /* 26 个焊盘的板面坐标（世界 mm），供 shader 判断"这一格"；缺数据时全 0 → 只按 pinId 亮。 */
  function pinPadXZ() {
    const out = new Float32Array(52);
    const part = ((window.NK_BOARD_PARTS || { parts: [] }).parts || []).find((p) => p.ref === 'CON30');
    (part && part.pads || []).forEach((p) => {
      const n = parseInt(p.n, 10);
      if (n >= 1 && n <= 26) { out[(n - 1) * 2] = 100 + p.x; out[(n - 1) * 2 + 1] = 100 + p.y; }
    });
    return out;
  }

  /* ---------------- 灯光 ---------------- */
  function addLights() {
    const TH = T();
    scene.add(new TH.HemisphereLight(0xdfe8f5, 0x1a1e24, 0.55));
    const key = new TH.DirectionalLight(0xffffff, 0.95);
    key.position.set(180, 260, 120);
    key.castShadow = true;
    key.shadow.mapSize.set(2048, 2048);
    key.shadow.camera.near = 50; key.shadow.camera.far = 900;
    key.shadow.camera.left = -120; key.shadow.camera.right = 120;
    key.shadow.camera.top = 120; key.shadow.camera.bottom = -120;
    key.shadow.bias = -0.0002;
    key.shadow.normalBias = 0.6;
    key.target.position.set(124, 0, 124);
    scene.add(key.target); scene.add(key);
    const fill = new TH.DirectionalLight(0x9fb4cc, 0.32);
    fill.position.set(-160, 120, -140);
    scene.add(fill);
  }

  /** 四角安装孔的**金环**：实物照片上就是"孔内暗 + 外圈金环"（Ø6.1~6.6mm），
   *  KiCad 的 MountingHole 模型是光孔 ✗ —— 这里按量到的中心/内外半径补一圈金环（装饰层，不参与拾取）。*/
  function addHoleRings(boardTopY) {
    const TH = T(), f = window.NK_BOARD_FRAME;
    if (!f || !f.holes) return;
    const g = new TH.Group();
    const mat = new TH.MeshStandardMaterial({ color: 0xd8a93c, metalness: 0.95, roughness: 0.28,
                                              side: TH.DoubleSide });
    const geo = new TH.RingGeometry(f.holes.rIn, f.holes.rOut, 48);
    f.holes.centers.forEach(([x, z]) => {
      const m = new TH.Mesh(geo, mat);
      m.rotation.x = -Math.PI / 2;
      m.position.set(f.ORIGIN_MM + x, boardTopY + 0.04, f.ORIGIN_MM + z);
      m.userData.ref = 'holes';
      g.add(m);
    });
    decoGroup.add(g);
  }

  /* ---------------- 3D 接线板：模块盒 + 端点 + 导线 ---------------- */
  function clearWiringModules() {
    clearGroup(moduleGroup);
    requestShadow();
  }

  /** 清空单针排针实例（换语言/重建时用）。 */
  function clearPinInstances() {
    clearGroup(headerGroup, { geo: false, mat: true });
    hlPins.clear();
    requestRender();
  }

  /** 抽象主板皮：FBX 里的 PCB、USB-A、LED、按键、丝印、J3/USB-C1/LAN1。
      交互件 J3/USB-C1/LAN1/D1/D2/K1/K2 参与拾取/高亮；USB-A/PCB/丝印等外观件禁用 raycast。 */
  const PICK_REFS = new Set(['J3', 'USB-C1', 'LAN1', 'D1', 'D2', 'K1', 'K2', 'neo3-plus_PCB', 'neo3-plus_silkscreen']);
  function addBoardSkin(obj) {
    clearGroup(boardSkinGroup);
    hlParts.clear();
    if (obj) {
      boardSkinGroup.add(obj);
      obj.updateMatrixWorld(true);
      obj.traverse((o) => {
        if (!o.isMesh) return;
        if (!o.visible) { o.raycast = () => {}; return; }
        const ref = refForMesh(o.name);
        if (!ref || !PICK_REFS.has(ref)) { o.raycast = () => {}; return; }
        o.userData.ref = ref;
        /* PCB / 丝印只用于 tooltip：不克隆材质、不进高亮集合，否则悬停会把整块板染黄 */
        if (ref === 'neo3-plus_PCB' || ref === 'neo3-plus_silkscreen') return;
        const mats = Array.isArray(o.material) ? o.material : [o.material];
        const clones = mats.map((m) => m && m.clone());
        o.material = Array.isArray(o.material) ? clones : clones[0];
        let arr = hlParts.get(ref);
        if (!arr) { arr = []; hlParts.set(ref, arr); }
        clones.forEach((m) => { if (m && m.color) arr.push({ m, base: m.color.clone(), mesh: o }); });
      });
    }
    requestShadow();
  }

  /** 在主板顶面补一块丝印文字（LED/按键在 FBX 里没有独立丝印）。 */
  function addSilkLabel(text, x, z, y) {
    const TH = T();
    const cv = document.createElement('canvas');
    const LH = 1.3;                          // 标签平面高度 mm
    cv.width = 256; cv.height = 64;
    const fontPx = SILK_TEXT_MM / LH * cv.height;   // 与屏幕背部丝印同字高
    const ctx = cv.getContext('2d');
    ctx.clearRect(0, 0, cv.width, cv.height);
    ctx.fillStyle = '#e8eef5';
    ctx.font = '700 ' + fontPx + 'px ui-monospace, Consolas, monospace';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillText(text, 128, 33);
    const tex = new TH.CanvasTexture(cv);
    tex.encoding = TH.sRGBEncoding;
    tex.anisotropy = 4;
    const mat = new TH.MeshBasicMaterial({ map: tex, transparent: true, depthWrite: false,
      polygonOffset: true, polygonOffsetFactor: -1 });
    const mesh = new TH.Mesh(new TH.PlaneGeometry(5.2, LH), mat);
    mesh.rotation.x = -Math.PI / 2;
    mesh.rotation.z = 0;                   // 顶视 180° 后 +x 朝右、-z 朝上：文字保持正向
    mesh.position.set(x, y + 0.02, z);
    mesh.renderOrder = 3;
    mesh.raycast = () => {};
    boardSkinGroup.add(mesh);
    silkLabels.push({ text, x: +x.toFixed(3), z: +z.toFixed(3) });
    requestRender();
  }

  /** 屏幕背面 8 针丝印：贴在 PCB 底面、排针后方，方便翻过来接线。 */
  function addScreenBackSilk(parent) {
    const TH = T();
    const host = parent || root3d;
    const W = 1024, H = 128, LH = 2.0;
    const fontPx = Math.round(SILK_TEXT_MM / LH * H);
    const makeTex = (order, backView) => {
      const cv = document.createElement('canvas');
      cv.width = W; cv.height = H;
      const ctx = cv.getContext('2d');
      ctx.clearRect(0, 0, W, H);
      ctx.fillStyle = '#e8eef5';
      ctx.font = '700 ' + fontPx + 'px ui-monospace, Consolas, monospace';
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      order.forEach((name, i) => {
        const x = (i + 0.5) * W / 8;
        if (backView) {
          /* 从板底看过来：整字旋转 180°，上下/左右同时正过来，格子位置不变。 */
          ctx.save(); ctx.translate(x, H / 2 + 1); ctx.rotate(Math.PI);
          ctx.fillText(name, 0, 0); ctx.restore();
        } else {
          ctx.fillText(name, x, H / 2 + 1);
        }
      });
      const tex = new TH.CanvasTexture(cv);
      tex.encoding = TH.sRGBEncoding;
      tex.anisotropy = 4;
      return tex;
    };
    const mat = new TH.MeshBasicMaterial({ map: makeTex(SCREEN_PHYS, true), transparent: true, depthWrite: false,
      polygonOffset: true, polygonOffsetFactor: -1 });
    const mesh = new TH.Mesh(new TH.PlaneGeometry(8 * 2.54, 2.0), mat);
    mesh.rotation.x = Math.PI / 2;          // 朝 -Y；翻到背面朝上后朝 +Y
    mesh.position.set(170, 8.56, 99.7);     // PCB 底面，排针后方 2mm
    mesh.name = 'SCREEN_BACK_SILK';
    mesh.renderOrder = 4;
    mesh.raycast = () => {};
    host.attach(mesh);

    /* 排针塑料体的前立面：rotation.y=π 会把 world +x 映到屏幕左侧，
       所以文字顺序必须反过来，VCC 才仍然贴在 pin1 上。 */
    const faceMat = new TH.MeshBasicMaterial({ map: makeTex(SCREEN_PHYS.slice().reverse(), false), transparent: true,
      depthWrite: false, polygonOffset: true, polygonOffsetFactor: -1 });
    const face = new TH.Mesh(new TH.PlaneGeometry(8 * 2.54, 2.0), faceMat);
    face.rotation.y = Math.PI;              // 朝向 -Z
    face.position.set(170, 7.27, 96.42);
    face.name = 'SCREEN_BACK_FACE';
    face.renderOrder = 4;
    face.raycast = () => {};
    host.attach(face);
    requestRender();
  }

  /** 屏幕 PCB 正面 8 针丝印：字高与背面一致。 */
  function addScreenFrontSilk(parent) {
    const TH = T();
    const host = parent || root3d;
    const W = 1024, H = 128, LH = 2.0;
    const fontPx = Math.round(SILK_TEXT_MM / LH * H);
    const cv = document.createElement('canvas');
    cv.width = W; cv.height = H;
    const ctx = cv.getContext('2d');
    ctx.clearRect(0, 0, W, H);
    ctx.fillStyle = '#e8eef5';
    ctx.font = '700 ' + fontPx + 'px ui-monospace, Consolas, monospace';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    /* 顶视 180° 后 +x 朝右，按实物顺序绘制，GND 仍贴在 pin1。 */
    SCREEN_PHYS.forEach((name, i) => ctx.fillText(name, (i + 0.5) * W / 8, H / 2 + 1));
    const tex = new TH.CanvasTexture(cv);
    tex.flipY = true;                        // Canvas 顶部对应平面上方
    tex.encoding = TH.sRGBEncoding;
    tex.anisotropy = 4;
    const mat = new TH.MeshBasicMaterial({ map: tex, transparent: true, depthWrite: false,
      polygonOffset: true, polygonOffsetFactor: -1 });
    const mesh = new TH.Mesh(new TH.PlaneGeometry(8 * 2.54, LH), mat);
    mesh.rotation.x = -Math.PI / 2;         // 朝 +Y；翻到板上方后仍朝 +Y
    mesh.rotation.z = 0;
    mesh.position.set(170, 10.13, 99.7);   // 屏幕 PCB 顶面，排针后方
    mesh.name = 'SCREEN_FRONT_SILK';
    mesh.renderOrder = 4;
    mesh.raycast = () => {};
    host.attach(mesh);
    requestRender();
  }

  /** 小屏语言与游戏一致；ADMIN / 无密钥页也来自 panel-go 位图字体渲染的 PNG。 */
  function screenLang() { return (typeof LANG !== 'undefined' && LANG === 'en') ? 'en' : 'zh'; }

  /** 屏幕点亮：直接用 panel-go 黄金样本渲染出的 240×240 真实 UI（固定位图字体）。
      玻璃整面显示，不画黑边；只朝正面，参与深度测试。 */
  function attachScreenDisplay(parent, screen) {
    const TH = T();
    const glass = screen.getObjectByName('SCREEN_GLASS001') || screen.getObjectByName('SCREEN_ACTIVE001') || screen;
    screen.updateMatrixWorld(true);
    const bb = new TH.Box3().setFromObject(glass);
    const center = bb.getCenter(new TH.Vector3());
    const glassSize = bb.getSize(new TH.Vector3());
    parent.updateMatrixWorld(true);
    /* 挂载瞬间屏幕是「背面朝上」（pivot 绕 X 转了 π）：local +Y 对应世界 -Y。
       屏幕翻到正面后 local +Y 变成世界 +Y、也就是玻璃外表面。
       所以显示面必须贴在**翻面后朝上的那一面**（挂载时的 bb.min.y），
       放在玻璃中心会陷进玻璃里，被玻璃挡住 → 翻面后看起来全黑。 */
    const outerWorld = new TH.Vector3(center.x, bb.min.y, center.z);
    const localFace = parent.worldToLocal(outerWorld.clone());

    /* 屏幕内容是一块 240×240 的画布：页面 PNG 只做源图，上传给 GPU 的是
       逐帧像素（CRT 开关机也在这块画布上算），与 panel-go 的帧缓冲同构。 */
    screenFrameCanvas = document.createElement('canvas');
    screenFrameCanvas.width = SCR_W;
    screenFrameCanvas.height = SCR_H;
    screenFrameCtx = screenFrameCanvas.getContext('2d', { willReadFrequently: true });
    screenFrameCtx.fillStyle = '#000';
    screenFrameCtx.fillRect(0, 0, SCR_W, SCR_H);
    screenBaseCanvas = document.createElement('canvas');
    screenBaseCanvas.width = SCR_W;
    screenBaseCanvas.height = SCR_H;
    screenBaseCtx = screenBaseCanvas.getContext('2d', { willReadFrequently: true });
    screenFrameTex = new TH.CanvasTexture(screenFrameCanvas);
    screenFrameTex.encoding = TH.sRGBEncoding;
    screenFrameTex.anisotropy = 1;
    screenFrameTex.magFilter = TH.NearestFilter;
    screenFrameTex.minFilter = TH.NearestFilter;
    screenFrameTex.generateMipmaps = false;

    const mat = new TH.MeshBasicMaterial({
      map: screenFrameTex, side: TH.FrontSide, depthTest: true, depthWrite: true,
      polygonOffset: true, polygonOffsetFactor: -2
    });
    const mesh = new TH.Mesh(new TH.PlaneGeometry(glassSize.x, glassSize.z), mat);
    mesh.rotation.x = -Math.PI / 2;
    mesh.position.copy(localFace);
    mesh.position.y += 0.08;   // 玻璃外表面再抬 0.08mm，避免 z-fight 与被玻璃遮挡
    mesh.name = 'NK_SCREEN_DISPLAY';
    mesh.renderOrder = 10;
    mesh.raycast = () => {};
    mesh.visible = false;
    parent.add(mesh);
    screenDisplay = mesh;
    screenGlass = glass;
    applyScreenPage();

    /* 最后一步提示：复用元件高亮方式，让整个屏幕模型脉冲自发光。
       不再盖一张独立平面——平面会随翻屏姿态和包围盒偏移。 */
    screenHlParts.length = 0;
    screen.traverse((o) => {
      if (!o.isMesh) return;
      const mats = Array.isArray(o.material) ? o.material : [o.material];
      const clones = mats.map((m) => (m && m.color) ? m.clone() : m);
      o.material = Array.isArray(o.material) ? clones : clones[0];
      clones.forEach((m) => {
        if (!m || !m.color) return;
        screenHlParts.push({
          m,
          base: m.color.clone(),
          emissive: m.emissive ? m.emissive.clone() : null,
          intensity: m.emissiveIntensity || 0,
        });
      });
    });
    if (screenCueOn) paintScreenCue(screenCueLevel);

    /* 透明拾取面：点屏幕本体即可翻屏（玻璃整面） */
    screenPickGroup = new TH.Group();
    const pick = new TH.Mesh(
      new TH.PlaneGeometry(glassSize.x, glassSize.z),
      new TH.MeshBasicMaterial({ transparent: true, opacity: 0, depthWrite: false, colorWrite: false, side: TH.DoubleSide })
    );
    pick.rotation.x = -Math.PI / 2;
    pick.position.copy(localFace);
    pick.position.y += 0.06;
    pick.userData.ref = 'screen:flip';
    screenPickGroup.add(pick);
    parent.add(screenPickGroup);
    if (screenBootOn) {
      screenBootOn = false;   // 挂载前已处于点亮态 → 挂载后补播一次开机动画
      setScreenBoot(true, screenBootOpts);
    }
    requestRender();
    return true;
  }

  /** 整屏脉冲：与 hlParts 同一个 tint/emissive 视觉，但独立于步骤高亮。 */
  function paintScreenCue(level) {
    const tint = tintFor(false);
    screenHlParts.forEach(({ m, base, emissive, intensity }) => {
      if (m.emissive) {
        m.emissive.copy(tint);
        m.emissiveIntensity = 0.18 + 0.5 * level;
      }
      m.color.copy(base).lerp(tint, 0.16 + 0.34 * level);
    });
    requestRender();
  }

  function restoreScreenCue() {
    screenHlParts.forEach(({ m, base, emissive, intensity }) => {
      m.color.copy(base);
      if (m.emissive) {
        if (emissive) m.emissive.copy(emissive); else m.emissive.setHex(0);
        m.emissiveIntensity = intensity;
      }
    });
    requestRender();
  }

  function setScreenCue(on) {
    screenCueOn = !!on;
    if (!screenCueOn) {
      if (cueRAF) { cancelAnimationFrame(cueRAF); cueRAF = 0; }
      screenCueLevel = 0;
      restoreScreenCue();
      requestRender();
      return false;
    }
    if (!cueRAF) {
      cueStart = performance.now();
      const tick = (now) => {
        if (!screenCueOn) { cueRAF = 0; requestRender(); return; }
        const p = 0.5 + 0.5 * Math.sin((now - cueStart) / 260);
        screenCueLevel = 0.35 + 0.65 * p;
        paintScreenCue(screenCueLevel);
        cueRAF = requestAnimationFrame(tick);
      };
      cueRAF = requestAnimationFrame(tick);
    }
    paintScreenCue(screenCueLevel || 0.5);
    requestRender();
    return true;
  }

  /* ---------------- CRT 开关机：240×240 点阵帧动画 ----------------
     与 panel-go/screen_crt.go 同阶段、同算法：先把页面画进 240×240 源画布，
     再逐像素生成压扁/收线/光点/展开帧，最后整帧上传为屏幕贴图。
     这里不做任何网格缩放——屏幕尺寸始终不变，变化的只有像素。 */
  const SCREEN_OFF_MS = 320, SCREEN_ON_MS = 420;
  const SCREEN_MIN_ROWS = 2;

  const exp5 = new Uint8Array(32), exp6 = new Uint8Array(64);
  for (let i = 0; i < 32; i++) exp5[i] = Math.floor(i * 255 / 31);
  for (let i = 0; i < 64; i++) exp6[i] = Math.floor(i * 255 / 63);

  function nativeScreenPage(page) {
    return page === 'main' || page === 'locked' || page === 'admin' || page === 'nokey';
  }

  function ensureScreenWasm() {
    if (screenWasmReady || screenWasmLoading) return;
    screenWasmLoading = true;
    const load = (src) => new Promise((res, rej) => {
      const s = document.createElement('script');
      s.src = src; s.onload = res; s.onerror = rej; document.head.appendChild(s);
    });
    (async () => {
      if (!window.NKScreen) {
        await load('../assets/screen/wasm_exec.js?v=8');
        await load('../assets/screen/wasm.js?v=15');
        await load('../assets/screen/demo-states.js?v=10');
      }
      await window.NKScreen.load();
      screenWasmCanvas = document.createElement('canvas');
      screenWasmCanvas.width = SCR_W; screenWasmCanvas.height = SCR_H;
      screenWasmReady = true;
      screenWasmLoading = false;
      if (screenBootOn) {
        renderNativePage(screenPageName, screenLang() === 'en');
        screenPageDirty = true;
        if (!screenAnimRAF) applyScreenPage(true);
      }
      syncAdminScreen();
    })().catch((e) => { screenWasmLoading = false; console.warn('screen wasm unavailable', e); });
  }

  function adminSecondsNow() {
    return screenAdminAt ? Math.max(0, (Date.now() - screenAdminAt) / 1000) : 0;
  }

  function renderNativePage(page, en, force, opts) {
    if (!screenWasmReady || !nativeScreenPage(page)) return;
    const key = page + ':' + (en ? 'en' : 'zh');
    if (!force && screenWasmPage === key) return;
    screenWasmPage = key;
    const states = window.NK_SCREEN_STATES || {};
    const base = states.main;
    if (!base) return;
    const st = JSON.parse(JSON.stringify(base));
    st.lang = en ? 'en' : 'zh';
    st.switch = page === 'admin' ? 'unlocked' : 'locked';
    if (page === 'admin') st.admin_s = adminSecondsNow();
    if (!st.url) st.url = 'https://<device-ip>:8443';
    if (Array.isArray(st.pools) && st.pools.length > 2) st.pools = st.pools.slice(0, 2);
    const anim = { pulse: 0.5, ring: 0.62, slide: 1 };
    if (opts && opts.transition) anim.transition = opts.transition;
    if (opts && opts.toast) anim.toast = opts.toast;
    screenLastAnim = anim;
    window.NKScreen.render(screenWasmCanvas, st, anim).then(() => {
      screenPageDirty = true;
      if (!screenAnimRAF) applyScreenPage(true);
      requestRender();
    }).catch(() => {});
  }

  /* ADMIN 页持续推进：秒表 + 超时告警边框。 */
  function syncAdminScreen() {
    const want = screenBootOn && screenPageName === 'admin' && screenWasmReady;
    if (!want) {
      if (screenAdminTimer) { clearInterval(screenAdminTimer); screenAdminTimer = 0; }
      return;
    }
    if (screenAdminTimer) return;
    screenAdminTimer = setInterval(() => {
      if (!screenBootOn || screenPageName !== 'admin' || !screenWasmReady) {
        clearInterval(screenAdminTimer); screenAdminTimer = 0;
        return;
      }
      if (document.hidden || window.__nkScreenHold) return;
      renderNativePage('admin', screenLang() === 'en', true);
    }, 33);
  }

  function drawPageToBase() {
    if (!screenWasmReady || !nativeScreenPage(screenPageName)) return false;
    if (screenWasmPage !== screenPageName + ':' + (screenLang() === 'en' ? 'en' : 'zh')) return false;
    screenBaseCtx.imageSmoothingEnabled = false;
    screenBaseCtx.clearRect(0, 0, SCR_W, SCR_H);
    screenBaseCtx.drawImage(screenWasmCanvas, 0, 0);
    return true;
  }

  /* 页面切换/语言切换：重画源画布；亮屏且不在动画中时立即上屏。 */
  function applyScreenPage(force) {
    if (!force && !screenPageDirty) return false;
    if (!drawPageToBase()) return false;
    screenPageDirty = false;
    if (!screenAnimRAF && screenBootOn) {
      screenFrameCtx.putImageData(screenBaseCtx.getImageData(0, 0, SCR_W, SCR_H), 0, 0);
      screenFrameTex.needsUpdate = true;
      requestRender();
    }
    return true;
  }

  function scrClear(dst) {
    dst.fill(0);
    for (let i = 3; i < dst.length; i += 4) dst[i] = 255;
  }

  function scrSet(dst, x, y, rgb) {
    if (x < 0 || y < 0 || x >= SCR_W || y >= SCR_H) return;
    const i = (y * SCR_W + x) * 4;
    dst[i] = rgb[0]; dst[i + 1] = rgb[1]; dst[i + 2] = rgb[2]; dst[i + 3] = 255;
  }

  /* 束流色：与 Go 的 rgb565() 相同——先按 565 量化，再展开回 8bit。 */
  function scrBeam(i) {
    i = Math.max(0, Math.min(1, i));
    const v = 255 * i;
    return [
      exp5[((v * 0.86) | 0) >> 3],
      exp6[((v * 0.94) | 0) >> 2],
      exp5[(v | 0) >> 3],
    ];
  }

  function scrHLine(dst, cx, cy, w, rgb) {
    if (w < 1) return;
    const x0 = Math.max(0, cx - (w >> 1));
    const x1 = Math.min(SCR_W - 1, x0 + w - 1);
    for (let x = x0; x <= x1; x++) scrSet(dst, x, cy, rgb);
  }

  function scrBeamLine(dst, cx, cy, w, core, halo) {
    if (w < 1 || core <= 0.01) return;
    if (halo > 0.02) {
      for (let d = 2; d >= 1; d--) {
        const rgb = scrBeam(halo * (1 - (d - 1) * 0.45) * 0.5);
        scrHLine(dst, cx, cy - d, w, rgb);
        scrHLine(dst, cx, cy + d, w, rgb);
      }
    }
    scrHLine(dst, cx, cy, w, scrBeam(core));
  }

  function scrDot(dst, cx, cy, r, i) {
    if (i <= 0.01) return;
    if (r < 1) r = 1;
    const core = scrBeam(i), halo = scrBeam(i * 0.2);
    const R = r + 2;
    for (let dy = -R; dy <= R; dy++) {
      for (let dx = -R; dx <= R; dx++) {
        const d2 = dx * dx + dy * dy;
        if (d2 > R * R) continue;
        scrSet(dst, cx + dx, cy + dy, d2 <= r * r ? core : halo);
      }
    }
  }

  /* 整行：RGB565 解码 → 提亮 mul → 向白场混合 white → 565 量化。 */
  function scrTintRow(dst, dOff, src, sOff, n, mul, white) {
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

  function scrEaseOut(x) { return 1 - Math.pow(1 - x, 3); }

  function scrFrameOff(dst, src, t) {
    scrClear(dst);
    if (t <= 0) { dst.set(src); return; }
    if (t >= 1) return;
    const cx = SCR_W >> 1, cy = SCR_H >> 1;
    if (t < 0.62) {                       // 纵向压扁
      const u = t / 0.62;
      let h = SCR_H * Math.pow(1 - u, 1.7);
      if (h < SCREEN_MIN_ROWS) h = SCREEN_MIN_ROWS;
      const rows = Math.max(SCREEN_MIN_ROWS, Math.round(h));
      const top = cy - (rows >> 1);
      const mul = 1 + 2.4 * u, white = 0.55 * u;
      for (let y = top; y < top + rows; y++) {
        if (y < 0 || y >= SCR_H) continue;
        const sy = Math.max(0, Math.min(SCR_H - 1, Math.floor((y - top + 0.5) * SCR_H / rows)));
        scrTintRow(dst, y * SCR_W * 4, src, sy * SCR_W * 4, SCR_W, mul, white);
      }
      if (u > 0.3) {
        const k = (u - 0.3) / 0.7;
        scrBeamLine(dst, cx, cy, SCR_W - 4, 0.3 + 0.7 * k, 0.45 * k);
      }
    } else if (t < 0.88) {                // 横向收线
      const u = (t - 0.62) / 0.26;
      const w = Math.max(SCREEN_MIN_ROWS, Math.floor(SCR_W * Math.pow(1 - u, 1.6)));
      scrBeamLine(dst, cx, cy, w, 1, 0.75);
    } else {                              // 光点余辉
      const u = (t - 0.88) / 0.12;
      scrDot(dst, cx, cy, 1 + Math.round(3 * (1 - u)), Math.max(0, 1 - u));
    }
  }

  function scrFrameOn(dst, src, t) {
    scrClear(dst);
    if (t <= 0) return;
    if (t >= 1) { dst.set(src); return; }
    const cx = SCR_W >> 1, cy = SCR_H >> 1;
    if (t < 0.14) {                       // 中心光点
      const u = t / 0.14;
      scrDot(dst, cx, cy, 1 + Math.round(4 * u), 0.35 + 0.65 * u);
    } else if (t < 0.44) {                // 横向扫开
      const u = (t - 0.14) / 0.3;
      scrBeamLine(dst, cx, cy, 3 + Math.round((SCR_W - 3) * scrEaseOut(u)), 1, 0.85);
    } else {                              // 纵向展开
      const u = (t - 0.44) / 0.56;
      const rows = Math.min(SCR_H, Math.max(SCREEN_MIN_ROWS,
        Math.round(SCREEN_MIN_ROWS + (SCR_H - SCREEN_MIN_ROWS) * scrEaseOut(u))));
      const top = cy - (rows >> 1);
      const mul = 2.6 - 1.6 * u, white = 0.85 * (1 - u);
      for (let y = top; y < top + rows; y++) {
        if (y < 0 || y >= SCR_H) continue;
        const sy = Math.max(0, Math.min(SCR_H - 1, Math.floor((y - top + 0.5) * SCR_H / rows)));
        scrTintRow(dst, y * SCR_W * 4, src, sy * SCR_W * 4, SCR_W, mul, white);
      }
      const k = 1 - u;
      if (k > 0.02) scrBeamLine(dst, cx, cy, SCR_W - 4, 0.6 * k * k, 0.5 * k * k);
    }
  }

  function scrClearTexture() {
    screenFrameCtx.fillStyle = '#000';
    screenFrameCtx.fillRect(0, 0, SCR_W, SCR_H);
    screenFrameTex.needsUpdate = true;
  }

  function playScreenPower(on) {
    if (!screenDisplay) return;
    if (screenAnimRAF) { cancelAnimationFrame(screenAnimRAF); screenAnimRAF = 0; }
    if (!drawPageToBase()) {            // 源图还没解码：直接切逻辑态
      screenDisplay.visible = on;
      if (!on) scrClearTexture();
      requestRender();
      return;
    }
    const base = screenBaseCtx.getImageData(0, 0, SCR_W, SCR_H).data;
    const frame = screenFrameCtx.createImageData(SCR_W, SCR_H);
    const start = performance.now();
    const dur = on ? SCREEN_ON_MS : SCREEN_OFF_MS;
    screenDisplay.visible = true;
    const tick = (now) => {
      const t = Math.min(1, Math.max(0, (now - start) / dur));
      if (on) scrFrameOn(frame.data, base, t);
      else scrFrameOff(frame.data, base, t);
      screenFrameCtx.putImageData(frame, 0, 0);
      screenFrameTex.needsUpdate = true;
      requestRender();
      if (t < 1) { screenAnimRAF = requestAnimationFrame(tick); return; }
      screenAnimRAF = 0;
      screenDisplay.visible = on;
      if (on) applyScreenPage(true); else scrClearTexture();
      requestRender();
    };
    screenAnimRAF = requestAnimationFrame(tick);
    requestRender();
  }

  function setScreenBoot(on, opts) {
    on = !!on;
    const liveOpts = opts && (opts.transition || opts.toast) ? opts : null;
    if (opts) {
      screenBootOpts = Object.assign({}, screenBootOpts, opts);
      delete screenBootOpts.transition;
      delete screenBootOpts.toast;
    }
    if (!screenDisplay) { screenBootOn = on; requestRender(); return false; }
    const changed = on !== screenBootOn;
    screenBootOn = on;
    const prevPage = screenPageName;
    screenPageName = screenBootOpts.page || (screenBootOpts.locked ? 'locked' : 'main');
    if (screenBootOpts.adminAt != null) screenAdminAt = screenBootOpts.adminAt || 0;
    if (screenPageName === 'admin') {
      if (!screenAdminAt && prevPage !== 'admin') screenAdminAt = Date.now();
    } else {
      screenAdminAt = 0;
    }
    const en = screenLang() === 'en';
    if (on) {
      ensureScreenWasm();
      renderNativePage(screenPageName, en, !!liveOpts, liveOpts);
    }
    syncAdminScreen();
    if (changed) playScreenPower(on);
    else if (!screenAnimRAF) {
      screenDisplay.visible = on;
      if (on) applyScreenPage();
    }
    requestRender();
    return on;
  }

  /** 开关：滑柄 + 3 只焊脚可交互；塑料本体不参与。 */
  function addSwitchParts(root) {
    if (!root) return;
    switchGroup.add(root);                 // 从 root3d 移入可拾取组
    root.updateMatrixWorld(true);
    const TH0 = T();
    /* 开关本体的对称轴：滑块的两个正确停位关于本体中心镜像。
       原模型滑块所在位置就是其中一个状态，另一状态用镜像点，不另编偏移。 */
    const body = root.getObjectByName('polySurface27') || root;
    const bodyBox = new TH0.Box3().setFromObject(body);
    switchAxisX = (bodyBox.min.x + bodyBox.max.x) / 2;
    root.traverse((o) => {
      if (!o.isMesh) return;
      const ref = o.name === 'polySurface13' ? 'sw:toggle'
        : o.name === 'polySurface15' ? 'sw:A'
        : o.name === 'polySurface16' ? 'sw:C'
        : o.name === 'polySurface14' ? 'sw:B'
        : null;
      if (!ref) { o.raycast = () => {}; return; }
      o.userData.ref = ref;
      o.userData.endpoint = ref;
      if (ref === 'sw:toggle') {
        switchToggleMesh = o;
        switchBaseLocalX = o.position.x;
        o.updateMatrixWorld(true);
        const tb = new TH0.Box3().setFromObject(o);
        switchBaseWorldX = (tb.min.x + tb.max.x) / 2;
        const ws = new TH0.Vector3();
        (o.parent || root).getWorldScale(ws);
        const sx = ws.x || 1;
        switchMirrorLocalX = switchBaseLocalX + (2 * (switchAxisX - switchBaseWorldX)) / sx;
        o.position.x = switchState ? switchBaseLocalX : switchMirrorLocalX;
      }
      const mats = Array.isArray(o.material) ? o.material : [o.material];
      const clones = mats.map((m) => m && m.clone());
      o.material = Array.isArray(o.material) ? clones : clones[0];
      let arr = hlParts.get(ref);
      if (!arr) { arr = []; hlParts.set(ref, arr); }
      clones.forEach((m) => { if (m && m.color) arr.push({ m, base: m.color.clone(), mesh: o }); });
    });
    requestRender();
  }
  function addPinInstance(obj, ref, endpoint, parent) {
    obj.userData.ref = ref;
    if (endpoint) obj.userData.endpoint = endpoint;
    obj.traverse((o) => {
      if (!o.isMesh) return;
      o.castShadow = false; o.receiveShadow = true;   // 34 根小针不参与 shadow pass
      o.userData.ref = ref;
      if (endpoint) o.userData.endpoint = endpoint;
      const mats = Array.isArray(o.material) ? o.material : [o.material];
      const clones = mats.map((m) => m && m.clone());
      o.material = Array.isArray(o.material) ? clones : clones[0];
    });
    const entries = [];
    obj.traverse((o) => {
      if (!o.isMesh) return;
      (Array.isArray(o.material) ? o.material : [o.material]).forEach((m) => {
        if (m && m.color) entries.push({ m, base: m.color.clone(), mesh: o });
      });
    });
    hlPins.set(ref, entries);
    if (parent) parent.attach(obj); else headerGroup.add(obj);
    /* 记录自由针尖（长针端）在实例本地坐标：随屏幕翻转连续移动。
       选择依据是“世界坐标里离塑料件更远的那一端”，不是本地 min/max——
       翻转后本地轴会反向。 */
    obj.updateMatrixWorld(true);
    const v = new (T().Vector3)();
    const groupsBox = {};
    obj.traverse((o) => {
      if (!o.isMesh || !o.geometry) return;
      const geo = o.geometry, idx = geo.index;
      (geo.groups || []).forEach((grp) => {
        const b = groupsBox[grp.materialIndex] || (groupsBox[grp.materialIndex] = {
          minY: Infinity, maxY: -Infinity, minX: Infinity, maxX: -Infinity, minZ: Infinity, maxZ: -Infinity
        });
        for (let i = grp.start; i < grp.start + grp.count; i++) {
          const vi = idx ? idx.getX(i) : i;
          v.fromBufferAttribute(geo.attributes.position, vi).applyMatrix4(o.matrixWorld);
          if (v.y < b.minY) b.minY = v.y;
          if (v.y > b.maxY) b.maxY = v.y;
          if (v.x < b.minX) b.minX = v.x;
          if (v.x > b.maxX) b.maxX = v.x;
          if (v.z < b.minZ) b.minZ = v.z;
          if (v.z > b.maxZ) b.maxZ = v.z;
        }
      });
    });
    const plastic = groupsBox[0], metal = groupsBox[1];
    if (metal && plastic) {
      const pc = (plastic.minY + plastic.maxY) / 2;
      const tipY = Math.abs(metal.minY - pc) > Math.abs(metal.maxY - pc) ? metal.minY : metal.maxY;
      const world = new (T().Vector3)(
        (metal.minX + metal.maxX) / 2, tipY, (metal.minZ + metal.maxZ) / 2
      );
      obj.userData.tipLocal = obj.worldToLocal(world);
    }
    requestRender();
    return entries.length;
  }

  /** 去掉自产网格里被单针模型替换掉的那一件（如 CON30），连拾取一起移除。 */
  function removeBasePart(ref) {
    for (let i = meshGroup.children.length - 1; i >= 0; i--) {
      const child = meshGroup.children[i];
      if (child.userData.ref !== ref) continue;
      meshGroup.remove(child);
      child.traverse((c) => {
        if (!c.isMesh) return;
        if (c.geometry) c.geometry.dispose();
        const mats = Array.isArray(c.material) ? c.material : [c.material];
        mats.forEach((m) => { if (m) m.dispose(); });
      });
    }
    for (let i = ranges.length - 1; i >= 0; i--) if (ranges[i].ref === ref) ranges.splice(i, 1);
    requestShadow();
  }

  /** 单针实例的顶部 y（接线端点用）；实例不存在返回 null。 */
  function pinTop(ref) {
    const g = headerGroup.children.find((c) => c.userData.ref === ref)
      || (screenPinGroup && screenPinGroup.children.find((c) => c.userData.ref === ref));
    if (!g) return null;
    return new (T().Box3)().setFromObject(g).max.y;
  }
  function pinCount() { return headerGroup.children.length + (screenPinGroup ? screenPinGroup.children.length : 0); }
  /** 单针实例的自由针尖（长针端）世界坐标；随屏幕翻转连续变化。 */
  function pinTip(ref) {
    const g = headerGroup.children.find((c) => c.userData.ref === ref)
      || (screenPinGroup && screenPinGroup.children.find((c) => c.userData.ref === ref));
    if (!g || !g.userData.tipLocal) return null;
    g.updateMatrixWorld(true);
    const v = g.userData.tipLocal.clone();
    g.localToWorld(v);
    return [v.x, v.y, v.z];
  }
  function setScreenPinGroup(g) { screenPinGroup = g; }
  function clearScreenPinInstances() { if (screenPinGroup) clearGroup(screenPinGroup); requestRender(); }

  /** 只解析 FBX，不做缩放/摆位：单针排针模型要按实例逐个摆放。 */
  function loadFBXModel(url, onProgress) {
    const TH = T();
    if (!TH.FBXLoader) return Promise.reject(new Error('FBXLoader 未加载'));
    return new Promise((resolve, reject) => {
      /* file:// 下 XHR 被浏览器拦截：优先用 script 注入的 base64 FBX，直接 parse。 */
      const key = (url || '').split('/').pop().split('?')[0];
      const b64 = window.NK_FBX_DATA && window.NK_FBX_DATA[key];
      if (b64) {
        try {
          const bin = atob(b64);
          const u8 = new Uint8Array(bin.length);
          for (let i = 0; i < bin.length; i++) u8[i] = bin.charCodeAt(i);
          resolve(dedupeScene(new TH.FBXLoader().parse(u8.buffer, '')));
        } catch (e) { reject(e); }
        return;
      }
      const progress = typeof onProgress === 'function'
        ? (xhr) => onProgress(xhr ? xhr.loaded : 0, xhr ? xhr.total : 0)
        : undefined;
      new TH.FBXLoader().load(url, (obj) => resolve(dedupeScene(obj)), progress, reject);
    });
  }

  /** 载入 FBX，按包围盒缩放到目标尺寸并放到指定世界位置。 */
  function loadFBX(url, opt, onProgress) {
    const TH = T();
    opt = opt || {};
    return loadFBXModel(url, onProgress).then((obj) => {
      obj.traverse((o) => {
        if (o.isMesh) {
          o.castShadow = true;
          o.receiveShadow = true;
        }
      });
      const box = new TH.Box3().setFromObject(obj);
      const size = box.getSize(new TH.Vector3());
      const fit = opt.fit || Math.max(size.x, size.z);
      const span = Math.max(size.x, size.z);
      const scl = span > 1e-6 ? fit / span : 1;
      obj.scale.setScalar(scl);
      if (opt.rotY) obj.rotation.y = opt.rotY;
      const b2 = new TH.Box3().setFromObject(obj);
      const c2 = b2.getCenter(new TH.Vector3());
      const at = opt.at || [124, 0, 124];
      obj.position.x += at[0] - c2.x;
      obj.position.z += at[2] - c2.z;
      obj.position.y += at[1] - b2.min.y;
      root3d.add(obj);
      fbxModels.push(obj);
      window.__NK_FBX = fbxModels;
      requestShadow();
      return obj;
    });
  }

  function addWiringModule(o) {
    const TH = T();
    const w = o.w || 20, d = o.d || 20, h = o.h || 2;
    const mat = new TH.MeshStandardMaterial({ color: o.color == null ? 0x1b4a6b : o.color, roughness: 0.72, metalness: 0.05 });
    const m = new TH.Mesh(new TH.BoxGeometry(w, h, d), mat);
    m.position.set(o.x, (o.y || 0) + h / 2, o.z);
    m.castShadow = m.receiveShadow = true;
    moduleGroup.add(m);
    if (o.top) {
      const g = new TH.Mesh(new TH.BoxGeometry(o.top[0], 0.4, o.top[1]),
        new TH.MeshStandardMaterial({ color: 0x06090d, roughness: 0.25, metalness: 0.1 }));
      g.position.set(o.x, (o.y || 0) + h + 0.2, o.z);
      moduleGroup.add(g);
    }
    requestShadow();
    return m;
  }

  function addWiringEndpoints(points) {
    const TH = T();
    clearGroup(endpointGroup);
    endpointAt.clear();
    /* 端点不做可见小球：用不可见 pick proxy，点击仍然命中，画面上没有球。 */
    const geo = new TH.BoxGeometry(1.8, 1.8, 1.8);
    const mat = new TH.MeshBasicMaterial({ transparent: true, opacity: 0, depthWrite: false, colorWrite: false });
    (points || []).forEach((p) => {
      const m = new TH.Mesh(geo, mat);
      m.position.set(p.x, p.y, p.z);
      m.userData.endpoint = p.id;
      m.castShadow = false; m.receiveShadow = false;
      endpointGroup.add(m);
      endpointAt.set(p.id, [p.x, p.y, p.z]);
      endpointRot.set(p.id, p.rot || 0);
    });
    requestRender();
    wireSig = '';
    if (wireList.length) setWires(wireList);
    return endpointAt.size;
  }

  /** 端点 → 线的起点与出线方向。
   *  - 排针/屏幕针：杜邦母头从正上方套进针，线从母头尾部（顶端）出，先竖直再弯；
   *  - 开关是裸件焊脚：线直接落在焊脚末端（焊锡球），从本体外侧走线。 */
  function wireAnchor(id, p) {
    if (id.startsWith('sw:')) {
      /* 开关三脚从左到右 A-C-B；都是向下伸的焊脚，从桌面上方的引脚末端出线。
         C 在中间，从后方（−z）出线，避免穿过开关本体。 */
      const dir = id === 'sw:A' ? [-1, 0.18, 0]
        : id === 'sw:B' ? [1, 0.18, 0]
        : [0, 0.18, -1];
      return { p: [p[0], 0.30, p[2]], dir, solder: true };
    }
    const TH = T();
    const rot = endpointRot.get(id) || 0;
    const base = [p[0], p[1], p[2]];
    const q = new TH.Quaternion().setFromAxisAngle(new TH.Vector3(1, 0, 0), rot);
    const tail = new TH.Vector3(0, DUPONT_TAIL, 0).applyQuaternion(q);
    const dir = new TH.Vector3(0, 1, 0).applyQuaternion(q);
    return {
      p: [base[0] + tail.x, base[1] + tail.y, base[2] + tail.z],
      dir: [dir.x, dir.y, dir.z], solder: false, base, rot
    };
  }

  function addDupontHousing(p, TH, rot, parent) {
    const g = new TH.Group();
    g.position.set(p[0], p[1], p[2]);
    g.rotation.x = rot || 0;

    const body = new TH.Mesh(
      new TH.BoxGeometry(DUPONT_W, DUPONT_H, DUPONT_W),
      new TH.MeshStandardMaterial({ color: 0x11151a, roughness: 0.72, metalness: 0.04 })
    );
    body.position.set(0, DUPONT_H / 2 - DUPONT_INSERT, 0);
    body.castShadow = body.receiveShadow = true;
    body.userData.wirePart = 'dupont';
    body.userData.wireRole = 'body';
    g.add(body);

    const cap = new TH.Mesh(
      new TH.BoxGeometry(1.25, 0.7, 1.25),
      new TH.MeshStandardMaterial({ color: 0x8d99a6, roughness: 0.45, metalness: 0.35 })
    );
    cap.position.set(0, DUPONT_H - DUPONT_INSERT + 0.35, 0);
    cap.castShadow = true;
    cap.userData.wirePart = 'dupont';
    cap.userData.wireRole = 'cap';
    g.add(cap);

    const lip = new TH.Mesh(
      new TH.BoxGeometry(DUPONT_W + 0.18, 0.55, DUPONT_W + 0.18),
      new TH.MeshStandardMaterial({ color: 0x2b3138, roughness: 0.6, metalness: 0.08 })
    );
    lip.position.set(0, 0.30 - DUPONT_INSERT, 0);
    lip.userData.wirePart = 'dupont';
    lip.userData.wireRole = 'lip';
    g.add(lip);

    (parent || wiringGroup).add(g);
    return g;
  }


  function addSolderBlob(p, TH) {
    const blob = new TH.Mesh(
      new TH.SphereGeometry(0.78, 10, 8),
      new TH.MeshStandardMaterial({ color: 0xcbd5e1, roughness: 0.28, metalness: 0.8 })
    );
    blob.scale.set(1, 0.62, 1);
    blob.position.set(p[0], p[1] + 0.10, p[2]);
    blob.castShadow = true;
    blob.userData.wirePart = 'solder';
    wiringGroup.add(blob);
  }

  function setWires(wires, opts) {
    const list = wires || [];
    opts = opts || {};
    wireList = list;
    const sig = JSON.stringify(list);
    if (sig === wireSig) return list.length;
    wireSig = sig;
    clearPreview();
    if (wireGrowRAF) { cancelAnimationFrame(wireGrowRAF); wireGrowRAF = 0; wireGrow = null; }
    const TH = T();
    clearGroup(wiringGroup);
    inserting.clear();
    const palette = [0xf0574f, 0xf0a83c, 0x35c76d, 0x4f8ff7, 0xbc8cff, 0x39c5cf];
    let growMesh = null;
    list.forEach((pair, i) => {
      const pa = endpointAt.get(pair[0]), pb = endpointAt.get(pair[1]);
      if (!pa || !pb) return;
      const a = wireAnchor(pair[0], pa), b = wireAnchor(pair[1], pb);
      if (!a.solder) {
        const ga = addDupontHousing(a.base, TH, a.rot);
        ga.userData.endpoint = pair[0];
        if (opts.animateEndpoints && opts.animateEndpoints.indexOf(pair[0]) >= 0) animateInsertion(ga, a.base, a.rot, false);
      } else addSolderBlob(a.p, TH);
      if (!b.solder) {
        const gb = addDupontHousing(b.base, TH, b.rot);
        gb.userData.endpoint = pair[1];
        if (opts.animateEndpoints && opts.animateEndpoints.indexOf(pair[1]) >= 0) animateInsertion(gb, b.base, b.rot, false);
      } else addSolderBlob(b.p, TH);

      /* 三次贝塞尔：两端沿各自出线方向起步/到达，先直后弯，不贴针折 90°。 */
      const v0 = new TH.Vector3(...a.p), v3 = new TH.Vector3(...b.p);
      const dist = v0.distanceTo(v3);
      const lift = Math.max(7.5, Math.min(32, 6 + dist * 0.22));
      const c1 = new TH.Vector3(a.dir[0], a.dir[1], a.dir[2]).normalize().multiplyScalar(lift).add(v0);
      const c2 = new TH.Vector3(b.dir[0], b.dir[1], b.dir[2]).normalize().multiplyScalar(lift).add(v3);
      const curve = new TH.CubicBezierCurve3(v0, c1, c2, v3);
      const g = new TH.TubeGeometry(curve, 48, 0.30, 8, false);
      const m = new TH.Mesh(g, new TH.MeshStandardMaterial({ color: palette[i % palette.length], roughness: 0.55, metalness: 0.1 }));
      m.castShadow = true;
      m.userData.wirePart = 'wire';
      m.userData.wirePair = pair.join('|');
      if (opts.animatePair && pair.join('|') === opts.animatePair.join('|')) growMesh = m;
      wiringGroup.add(m);
    });
    if (growMesh) {
      const geo = growMesh.geometry;
      const count = geo.index ? geo.index.count : geo.attributes.position.count;
      geo.setDrawRange(0, 0);
      growMesh.material.transparent = true; growMesh.material.opacity = 0;
      wireGrow = { mesh: growMesh, geo, mat: growMesh.material, count, start: performance.now(), dur: 320 };
      wireGrowRAF = requestAnimationFrame(tickWireGrow);
    }
    requestRender();
    return list.length;
  }

  function tickWireGrow(now) {
    if (!wireGrow) { wireGrowRAF = 0; return; }
    const t = Math.min(1, (now - wireGrow.start) / wireGrow.dur);
    wireGrow.geo.setDrawRange(0, Math.max(1, Math.floor(wireGrow.count * t)));
    wireGrow.mat.opacity = Math.min(1, t / 0.45);
    requestRender();
    if (t < 1) wireGrowRAF = requestAnimationFrame(tickWireGrow);
    else { wireGrow.geo.setDrawRange(0, wireGrow.count); wireGrow.mat.opacity = 1; wireGrow.mat.transparent = false; wireGrow = null; wireGrowRAF = 0; }
  }

  function disposePreview() {
    for (let i = previewGroup.children.length - 1; i >= 0; i--) {
      const g = previewGroup.children[i];
      previewGroup.remove(g);
      inserting.delete(g);
      g.traverse((c) => {
        if (!c.isMesh) return;
        if (c.geometry) c.geometry.dispose();
        const mats = Array.isArray(c.material) ? c.material : [c.material];
        mats.forEach((m) => { if (m) m.dispose(); });
      });
    }
    previewIds = [];
  }

  function clearPreview() {
    disposePreview();
    requestRender();
  }

  /** 杜邦帽：先在预备位置 fade-in，再沿针轴滑入。 */
  function animateInsertion(g, p, rot, keep) {
    const TH = T();
    const q = new TH.Quaternion().setFromAxisAngle(new TH.Vector3(1, 0, 0), rot);
    const dir = new TH.Vector3(0, 1, 0).applyQuaternion(q);
    const from = [p[0] + dir.x * 3.5, p[1] + dir.y * 3.5, p[2] + dir.z * 3.5];
    g.position.set(from[0], from[1], from[2]);
    const mats = [];
    g.traverse((o) => {
      if (!o.isMesh) return;
      (Array.isArray(o.material) ? o.material : [o.material]).forEach((m) => {
        if (!m) return;
        m.transparent = true; m.opacity = 0; m.depthWrite = false;
        mats.push(m);
      });
    });
    inserting.add(g);
    const t0 = performance.now();
    const tick = (now) => {
      const ms = now - t0;
      const fade = Math.min(1, ms / 120);
      const slide = Math.max(0, Math.min(1, (ms - 180) / 280));
      const e = 1 - Math.pow(1 - slide, 3);
      mats.forEach((m) => { m.opacity = fade; });
      g.position.set(
        from[0] + (p[0] - from[0]) * e,
        from[1] + (p[1] - from[1]) * e,
        from[2] + (p[2] - from[2]) * e
      );
      requestRender();
      if (slide < 1) requestAnimationFrame(tick);
      else {
        mats.forEach((m) => { m.opacity = 1; m.transparent = false; m.depthWrite = true; });
        inserting.delete(g);
        requestRender();
        if (!keep) { /* 真实接头保留，预览由 setWires 清理 */ }
      }
    };
    requestAnimationFrame(tick);
  }

  /** 点击端点时的接头动画：杜邦端滑入，开关端溅焊锡火花。 */
  function previewJoint(id, transient) {
    const TH = T();
    const p = endpointAt.get(id);
    if (!p) return 0;
    disposePreview();
    if (id.startsWith('sw:')) {
      sparkEndpoints([id]);
      return 0;
    }
    const rot = endpointRot.get(id) || 0;
    const q = new TH.Quaternion().setFromAxisAngle(new TH.Vector3(1, 0, 0), rot);
    const dir = new TH.Vector3(0, 1, 0).applyQuaternion(q);
    const g = addDupontHousing([p[0], p[1], p[2]], TH, rot, previewGroup);
    g.userData.endpoint = id;
    previewIds = [id];
    animateInsertion(g, [p[0], p[1], p[2]], rot, true);
    return 1;
  }

  /** 焊接瞬间的火花：每个焊点一簇加色粒子 + 一个扩散光斑，约 0.5s 后自动清理。 */
  function sparkEndpoints(ids) {
    const TH = T();
    if (!TH || !sparkGroup) return 0;
    if (!sparkGeo) sparkGeo = new TH.SphereGeometry(0.22, 6, 4);
    if (!sparkGlowGeo) sparkGlowGeo = new TH.SphereGeometry(0.8, 10, 6);
    (ids || []).forEach((id) => {
      const p0 = endpointAt.get(id);
      if (!p0) return;
      const p = id.startsWith('sw:') ? [p0[0], 0.35, p0[2]] : [p0[0], p0[1] + 0.15, p0[2]];
      const mat = new TH.MeshBasicMaterial({
        color: 0xffd27a, transparent: true, opacity: 0.95,
        blending: TH.AdditiveBlending, depthWrite: false
      });
      const parts = [];
      for (let i = 0; i < 16; i++) {
        const mesh = new TH.Mesh(sparkGeo, mat);
        mesh.position.set(p[0] + (Math.random() - 0.5) * 0.35,
          p[1] + (Math.random() - 0.5) * 0.25,
          p[2] + (Math.random() - 0.5) * 0.35);
        const a = Math.random() * Math.PI * 2;
        const sp = 2.5 + Math.random() * 9;
        parts.push({
          mesh,
          vel: new TH.Vector3(Math.cos(a) * sp, 3 + Math.random() * 11, Math.sin(a) * sp)
        });
        sparkGroup.add(mesh);
      }
      const glowMat = new TH.MeshBasicMaterial({
        color: 0xfff0b0, transparent: true, opacity: 0.65,
        blending: TH.AdditiveBlending, depthWrite: false
      });
      const glow = new TH.Mesh(sparkGlowGeo, glowMat);
      glow.position.set(p[0], p[1] + 0.2, p[2]);
      sparkGroup.add(glow);
      sparkBursts.push({ parts, mat, glow, glowMat, age: 0, life: 0.5 });
    });
    if (sparkBursts.length && !sparkRAF) {
      sparkLast = 0;
      sparkRAF = requestAnimationFrame(tickSparks);
    }
    requestRender();
    return sparkBursts.length;
  }

  function tickSparks(now) {
    sparkRAF = 0;
    if (!sparkBursts.length) { sparkLast = 0; return; }
    if (!sparkLast) sparkLast = now;
    const dt = Math.min(0.05, (now - sparkLast) / 1000);
    sparkLast = now;
    const keep = [];
    sparkBursts.forEach((b) => {
      b.age += dt;
      const t = Math.min(1, b.age / b.life);
      b.mat.opacity = Math.max(0, 0.95 * (1 - t));
      b.parts.forEach((q) => {
        q.mesh.position.addScaledVector(q.vel, dt);
        q.vel.y -= 26 * dt;
        q.mesh.scale.setScalar(Math.max(0.2, 1 - 0.75 * t));
      });
      b.glow.scale.setScalar(1 + 3.2 * t);
      b.glowMat.opacity = Math.max(0, 0.65 * (1 - t * 1.6));
      if (t >= 1) {
        b.parts.forEach((q) => sparkGroup.remove(q.mesh));
        sparkGroup.remove(b.glow);
        b.mat.dispose();
        b.glowMat.dispose();
      } else keep.push(b);
    });
    sparkBursts = keep;
    if (sparkBursts.length) sparkRAF = requestAnimationFrame(tickSparks);
    else sparkLast = 0;
    requestRender();
  }

  /** 拨动开关的滑柄：ON 朝板（+x），OFF 朝外（−x）。 */
  function setSwitchState(on) {
    switchState = !!on;
    annoAnchorsCache = null;
    if (switchToggleMesh) {
      switchToggleMesh.position.x = switchState ? switchBaseLocalX : switchMirrorLocalX;
      requestShadow();
      requestRender();
    }
    return switchState;
  }

  /** 工作台：深色台面 + 防静电网格垫。纯装饰，不参与拾取。 */
  function addWorkbench() {
    const TH = T();
    scene.background = new TH.Color(0x0b0e13);
    scene.fog = new TH.Fog(0x0b0e13, 420, 1000);

    const deskCv = document.createElement('canvas');
    deskCv.width = 512; deskCv.height = 512;
    const dg = deskCv.getContext('2d');
    const grad = dg.createRadialGradient(256, 236, 40, 256, 256, 360);
    grad.addColorStop(0, '#2a3139');
    grad.addColorStop(0.55, '#1d232a');
    grad.addColorStop(1, '#11151a');
    dg.fillStyle = grad; dg.fillRect(0, 0, 512, 512);
    for (let i = 0; i < 900; i++) {
      const a = 0.012 + Math.random() * 0.02;
      dg.fillStyle = `rgba(255,255,255,${a})`;
      dg.fillRect(Math.random() * 512, Math.random() * 512, 1, 1);
    }
    const deskTex = new TH.CanvasTexture(deskCv);
    deskTex.encoding = TH.sRGBEncoding;
    deskTex.anisotropy = 4;
    const desk = new TH.Mesh(new TH.PlaneGeometry(1000, 1000),
      new TH.MeshStandardMaterial({ map: deskTex, roughness: 0.96, metalness: 0 }));
    desk.rotation.x = -Math.PI / 2;
    desk.position.set(124, -0.38, 124);
    desk.receiveShadow = true;
    desk.raycast = () => {};
    scene.add(desk);

    const W = 1024, H = 748, pad = 20, radius = 22;
    const cv = document.createElement('canvas');
    cv.width = W; cv.height = H;
    const g = cv.getContext('2d');
    g.clearRect(0, 0, W, H);
    g.beginPath();
    g.moveTo(pad + radius, pad);
    g.arcTo(W - pad, pad, W - pad, H - pad, radius);
    g.arcTo(W - pad, H - pad, pad, H - pad, radius);
    g.arcTo(pad, H - pad, pad, pad, radius);
    g.arcTo(pad, pad, W - pad, pad, radius);
    g.closePath();
    g.fillStyle = '#14292b';
    g.fill();
    g.save();
    g.clip();
    const glow = g.createRadialGradient(W * 0.45, H * 0.4, 30, W * 0.5, H * 0.5, W * 0.75);
    glow.addColorStop(0, 'rgba(58, 105, 108, 0.20)');
    glow.addColorStop(1, 'rgba(6, 18, 20, 0.35)');
    g.fillStyle = glow; g.fillRect(0, 0, W, H);
    const stepX = (W - 2 * pad) / 26;   // 260mm 垫 → 10mm 网格
    const stepZ = (H - 2 * pad) / 19;   // 190mm
    g.lineWidth = 1;
    for (let i = 0; i <= 26; i++) {
      const x = pad + i * stepX;
      g.strokeStyle = i % 5 === 0 ? 'rgba(120, 190, 190, 0.20)' : 'rgba(100, 160, 160, 0.10)';
      g.beginPath(); g.moveTo(x, pad); g.lineTo(x, H - pad); g.stroke();
    }
    for (let i = 0; i <= 19; i++) {
      const y = pad + i * stepZ;
      g.strokeStyle = i % 5 === 0 ? 'rgba(120, 190, 190, 0.20)' : 'rgba(100, 160, 160, 0.10)';
      g.beginPath(); g.moveTo(pad, y); g.lineTo(W - pad, y); g.stroke();
    }
    g.restore();
    g.strokeStyle = 'rgba(118, 190, 190, 0.42)';
    g.lineWidth = 3;
    g.stroke();
    g.fillStyle = 'rgba(126, 196, 196, 0.55)';
    g.font = '700 17px ui-monospace, Consolas, monospace';
    g.textAlign = 'left'; g.textBaseline = 'bottom';
    g.fillText('NOKEYOUT · ESD WORKBENCH', pad + 18, H - pad - 14);
    g.textAlign = 'right';
    g.fillStyle = 'rgba(126, 196, 196, 0.35)';
    g.font = '600 14px ui-monospace, Consolas, monospace';
    g.fillText('10 mm GRID', W - pad - 18, H - pad - 14);
    for (const [x, y] of [[pad + 16, pad + 16], [W - pad - 16, pad + 16], [pad + 16, H - pad - 16], [W - pad - 16, H - pad - 16]]) {
      g.beginPath(); g.arc(x, y, 5, 0, Math.PI * 2);
      g.fillStyle = 'rgba(160, 210, 210, 0.28)'; g.fill();
    }
    const matTex = new TH.CanvasTexture(cv);
    matTex.encoding = TH.sRGBEncoding;
    matTex.anisotropy = 8;
    const mat = new TH.Mesh(new TH.PlaneGeometry(260, 190),
      new TH.MeshStandardMaterial({ map: matTex, transparent: true, roughness: 0.84, metalness: 0 }));
    mat.rotation.x = -Math.PI / 2;
    mat.position.set(124, -0.08, 124);
    mat.receiveShadow = true;
    mat.raycast = () => {};
    scene.add(mat);
  }

  function init(cont) {
    const TH = T();
    if (!TH) throw new Error('three.js 未加载');
    container = cont;
    renderer = new TH.WebGLRenderer({ antialias: true, powerPreference: 'high-performance' });
    renderer.setPixelRatio(baseDpr);
    renderer.outputEncoding = TH.sRGBEncoding;
    renderer.shadowMap.enabled = true;
    renderer.shadowMap.type = TH.PCFSoftShadowMap;
    renderer.shadowMap.autoUpdate = false;      // 静态场景：只在几何/显隐变化时重烘
    renderer.shadowMap.needsUpdate = true;
    canvas = renderer.domElement;
    canvas.style.cssText = 'display:block;width:100%;height:100%;touch-action:none';
    cont.appendChild(canvas);

    scene = new TH.Scene();
    camera = new TH.OrthographicCamera(-1, 1, 1, -1, 1, 3000);
    camera.aspect = 1;                       // 自定义字段，供正交视锥用
    addLights();
    addWorkbench();

    root3d = new TH.Group(); scene.add(root3d);
    boardSkinGroup = new TH.Group(); root3d.add(boardSkinGroup);   // 抽象板皮（只有四角孔）
    switchGroup = new TH.Group(); root3d.add(switchGroup);         // 开关滑柄/焊脚
    meshGroup = new TH.Group(); root3d.add(meshGroup);
    plugGroup = new TH.Group(); root3d.add(plugGroup);
    decoGroup = new TH.Group(); root3d.add(decoGroup);
    wiringGroup = new TH.Group(); root3d.add(wiringGroup);
    previewGroup = new TH.Group(); root3d.add(previewGroup);
    sparkGroup = new TH.Group(); root3d.add(sparkGroup);
    endpointGroup = new TH.Group(); root3d.add(endpointGroup);
    endpointGroup.visible = false;          // 拾取代理不参与绘制（Raycaster 仍能命中）
    moduleGroup = new TH.Group(); root3d.add(moduleGroup);
    headerGroup = new TH.Group(); root3d.add(headerGroup);
    pickRay = new TH.Raycaster();
    bindInput();
    resize();
    ready = true;
    requestRender();
  }

  /* ---------------- 网格 ---------------- */
  const BASE_KEEP = new Set(['CON30', 'J3', 'USB-C1', 'LAN1']);
  function setMesh(m) {
    const TH = T();
    clearGroup(meshGroup);
    meshes.clear(); ranges = [];
    m.parts.forEach((p) => {
      byRef.set(p.ref, p);
      /* 自产网格只建高亮/拾取需要的四件（CON30 是单针模型失败时的回退）。
         其余件的外观由 neo3-plus-abstract.fbx 提供，不再建几何。 */
      if (!BASE_KEEP.has(p.ref)) return;
      // 每件一组 Mesh（同件不同材质各一个 Mesh），userData.ref 供拾取用
      const geoGroup = new TH.Group();
      geoGroup.userData.ref = p.ref;
      p.ranges.forEach((r, i) => {
        const geo = geometryFor(m, p);
        geo.setDrawRange(r.start, r.count);
        const mat = new TH.MeshStandardMaterial({
          color: new TH.Color(r.color[0] / 255, r.color[1] / 255, r.color[2] / 255),
          metalness: r.metal, roughness: r.rough,
          flatShading: false,
        });
        /* CON30：金色针脚 **和** 黑色塑壳都挂单针 shader。
           塑料件（黑）拿整块底色，针脚（金）只亮选中针；两者都由 pinShader 处理。 */
        const pinU = (p.ref === 'CON30') ? pinShader(mat, r.color[0] < 120) : null;
        const mesh = new TH.Mesh(geo, mat);
        mesh.userData.ref = p.ref;
        mesh.castShadow = true; mesh.receiveShadow = true;
        geoGroup.add(mesh);
        meshes.set(p.ref + '#' + i, mesh);
        ranges.push({ ref: p.ref, mesh, base: r.color.slice(), count: r.count, pinU });
      });
      meshGroup.add(geoGroup);
    });
    parts = m.parts;
    addHoleRings((m.parts.find((p) => p.ref === 'neo3-plus_PCB') || { bb: [0, 0, 0, 0, 1.2, 0] }).bb[4]);
    ready = true;
    requestShadow();
    return parts.length;
  }

  function addPlug(id, m) {
    const TH = T();
    const g = geometryFor(m, null);
    const grp = new TH.Group();
    grp.userData.plug = id;
    grp.name = 'Plug_' + id;
    let matRanges = 0, matTransparent = 0, matAlphaMin = 1;
    m.parts.forEach((p, pi) => {
      p.ranges.forEach((r, i) => {
        const geo = pi === 0 && i === 0 ? g : geometryFor(m, p);
        geo.setDrawRange(r.start, r.count);
        const alpha = r.alpha == null ? 1 : r.alpha;
        const color = r.color;
        const metal = r.metal;
        const rough = r.rough;
        matRanges++;
        if (alpha < 0.999) { matTransparent++; matAlphaMin = Math.min(matAlphaMin, alpha); }
        const mesh = new TH.Mesh(geo, new TH.MeshStandardMaterial({
          color: new TH.Color(color[0] / 255, color[1] / 255, color[2] / 255),
          metalness: metal, roughness: rough,
          transparent: alpha < 0.999, opacity: alpha,
          depthWrite: alpha >= 0.999,
          side: TH.DoubleSide }));
        mesh.castShadow = true; mesh.receiveShadow = true;
        mesh.renderOrder = alpha < 0.999 ? 5 : 0;
        mesh.userData.plug = id;
        mesh.name = 'Plug_' + id + '_' + pi + '_' + i;
        grp.add(mesh);
      });
    });
    /* 线尾：Blender 世界网格已经把朝向烘焙进顶点；沿世界 +X（右缘 USB/RJ45）
       从模型尾部接出，直线，不再走 +Z/拐弯。 */
    const pl = (window.NK_PLUG_PLACEMENT || {})[id] || {};
    if (pl.tail && pl.tailAxis === 'x') {
      const len = id === 'rj45' ? 50 : 42;
      const x0 = pl.tail[0], y0 = pl.tail[1], z0 = pl.tail[2];
      const curve = new TH.LineCurve3(
        new TH.Vector3(x0, y0, z0),
        new TH.Vector3(x0 + len, y0, z0)
      );
      const cv = document.createElement('canvas');
      cv.width = 64; cv.height = 1;
      const c2 = cv.getContext('2d');
      const grd = c2.createLinearGradient(0, 0, 64, 0);
      grd.addColorStop(0, 'rgba(18,20,24,1)');
      grd.addColorStop(0.55, 'rgba(18,20,24,0.55)');
      grd.addColorStop(1, 'rgba(18,20,24,0)');
      c2.fillStyle = grd; c2.fillRect(0, 0, 64, 1);
      const tex = new TH.CanvasTexture(cv);
      tex.encoding = TH.sRGBEncoding;
      const cable = new TH.Mesh(
        new TH.TubeGeometry(curve, 28, id === 'rj45' ? 2.4 : 1.4, 6, false),
        new TH.MeshBasicMaterial({ map: tex, transparent: true, depthWrite: false, side: TH.DoubleSide })
      );
      cable.raycast = () => {};
      cable.userData.isCable = true;
      grp.add(cable);
    }
    grp.userData.materialStats = { ranges: matRanges, transparent: matTransparent, alphaMin: matRanges ? matAlphaMin : 1 };
    grp.visible = false;
    plugGroup.add(grp);
    plugGroup.userData[id] = grp;
    requestRender();
  }

  function setPlug(id, opt) {
    const grp = plugGroup.userData[id];
    if (!grp) return false;
    const vis = !!(opt && opt.visible);
    const at = opt && opt.at;
    const rotDeg = (opt && opt.rotDeg) || 0;
    const rollDeg = (opt && opt.rollDeg) || 0;
    const insert = opt && opt.insert != null ? Math.max(0, Math.min(1, opt.insert)) : 1;
    const pushDist = opt && opt.push != null ? Math.max(0, opt.push) : 10;
    const key = vis + '|' + (at ? at.join(',') : '') + '|' + rotDeg + '|' + rollDeg + '|' + (opt && opt.out ? opt.out.join(',') : '') + '|' + insert.toFixed(3) + '|' + pushDist;
    if (grp.userData._plugKey === key) return true;
    grp.userData._plugKey = key;
    grp.visible = vis;
    if (at) {
      const TH = T();
      const rad = rotDeg * Math.PI / 180;
      const ov = opt && opt.out;
      const outX = ov ? ov[0] : Math.sin(rad), outY = ov ? ov[1] : 0, outZ = ov ? ov[2] : Math.cos(rad);
      const push = (1 - insert) * pushDist;
      grp.position.set(at[0] + outX * push, at[1] + outY * push, at[2] + outZ * push);
      /* rotDeg 绕世界 Y 转向插座；rollDeg 再绕插头自身长轴翻转（tab up ↔ tab down） */
      const yaw = new TH.Quaternion().setFromAxisAngle(new TH.Vector3(0, 1, 0), rad);
      const roll = new TH.Quaternion().setFromAxisAngle(new TH.Vector3(1, 0, 0), rollDeg * Math.PI / 180);
      grp.quaternion.copy(yaw).multiply(roll);
    }
    requestShadow();
    return true;
  }

  /* ---------------- 照片贴面 ---------------- */
  function setPhoto(url, rect) {
    const TH = T();
    photoRect = Object.assign({}, rect);
    if (photoMesh) { root3d.remove(photoMesh); photoMesh = null; }
    const tex = new TH.TextureLoader().load(url);
    tex.onLoad = () => requestRender();
    tex.encoding = TH.sRGBEncoding;
    tex.anisotropy = renderer.capabilities.getMaxAnisotropy();
    photoMat = new TH.MeshBasicMaterial({ map: tex, transparent: true, depthWrite: false, opacity: rect.alpha || 0.97 });
    photoMesh = new TH.Mesh(new TH.PlaneGeometry(rect.w, rect.h), photoMat);
    photoMesh.rotation.x = -Math.PI / 2;
    photoMesh.position.set(rect.x0 + rect.w / 2, rect.y, rect.z0 + rect.h / 2);
    root3d.add(photoMesh);
    requestRender();
  }
  function setPhotoRect(r) {
    photoRect = Object.assign({}, photoRect, r);
    if (!photoMesh) return;
    photoMat.opacity = photoRect.alpha;
    photoMesh.position.set(photoRect.x0 + photoRect.w / 2, photoRect.y, photoRect.z0 + photoRect.h / 2);
    requestRender();
  }

  /* ---------------- 相机 / 轨道（自己写，不引 OrbitControls） ---------------- */
  function orthoHeight() {
    return 2 * cam.dist * Math.tan(VIEW_FOV * Math.PI / 360);
  }
  function updateOrtho() {
    const h = orthoHeight(), w = h * (camera.aspect || 1);
    camera.left = -w / 2; camera.right = w / 2;
    camera.top = h / 2; camera.bottom = -h / 2;
    camera.updateProjectionMatrix();
  }
  function applyCam() {
    const cp = Math.cos(cam.pitch * Math.PI / 180), sp = Math.sin(cam.pitch * Math.PI / 180);
    const cy = Math.cos(cam.yaw * Math.PI / 180), sy = Math.sin(cam.yaw * Math.PI / 180);
    camera.position.set(cam.target[0] + cam.dist * cp * cy, cam.target[1] + cam.dist * sp, cam.target[2] + cam.dist * cp * sy);
    // 正俯视时视轴与世界 Y 平行，lookAt 需换一个 up，否则姿态退化。
    if (cam.pitch >= 89.5) {
      camera.up.set(Math.sin(cam.yaw * Math.PI / 180), 0, Math.cos(cam.yaw * Math.PI / 180));
    } else {
      camera.up.set(0, 1, 0);
    }
    camera.lookAt(cam.target[0], cam.target[1], cam.target[2]);
    updateOrtho();
    for (const fn of camCbs) { try { fn(); } catch (e) { /* ignore */ } }
    requestRender();
  }
  function stopCamAnim() {
    if (camAnim) { cancelAnimationFrame(camAnim); camAnim = 0; }
  }
  function animateCam(dest, ms) {
    stopCamAnim();
    const from = { yaw: cam.yaw, pitch: cam.pitch, dist: cam.dist, target: cam.target.slice() };
    const t0 = performance.now();
    const dur = Math.max(120, ms || 700);
    const tick = (now) => {
      const p = Math.min(1, (now - t0) / dur);
      const e = p * p * (3 - 2 * p);
      cam.yaw = from.yaw + (dest.yaw - from.yaw) * e;
      cam.pitch = from.pitch + (dest.pitch - from.pitch) * e;
      cam.dist = from.dist + (dest.dist - from.dist) * e;
      cam.target = [
        from.target[0] + (dest.target[0] - from.target[0]) * e,
        from.target[1] + (dest.target[1] - from.target[1]) * e,
        from.target[2] + (dest.target[2] - from.target[2]) * e,
      ];
      applyCam();
      if (p < 1) camAnim = requestAnimationFrame(tick);
      else camAnim = 0;
    };
    camAnim = requestAnimationFrame(tick);
  }
  /* 中键 / 右键 / Shift+左键拖拽 = 平移（相机目标沿屏幕平面移动）。 */
  function panBy(dx, dy) {
    const TH = T();
    camera.updateMatrixWorld();
    const right = new TH.Vector3().setFromMatrixColumn(camera.matrixWorld, 0);
    const up = new TH.Vector3().setFromMatrixColumn(camera.matrixWorld, 1);
    const h = canvas.clientHeight || 1;
    const k = orthoHeight() / h;
    cam.target[0] += (-right.x * dx + up.x * dy) * k;
    cam.target[1] += (-right.y * dx + up.y * dy) * k;
    cam.target[2] += (-right.z * dx + up.z * dy) * k;
    applyCam();
  }
  function bindInput() {
    let down = null, moved = 0, lastHover = 0, dragMode = 'orbit', dragButton = 0;
    canvas.addEventListener('pointerdown', (e) => {
      stopCamAnim();
      dragButton = e.button;
      /* 左键平移、右键转视角（中键/Shift 也平移）；左键仍负责点选元件 */
      dragMode = (e.button === 0 || e.button === 1 || e.shiftKey) ? 'pan' : 'orbit';
      e.preventDefault();
      down = [e.clientX, e.clientY]; moved = 0;
      canvas.setPointerCapture(e.pointerId);
      const tip = document.getElementById('bench3d-tip');
      if (tip) tip.hidden = true;
    });
    canvas.addEventListener('mousedown', (e) => {
      if (e.button === 1 || e.button === 2) e.preventDefault();
    });
    canvas.addEventListener('contextmenu', (e) => e.preventDefault());
    canvas.addEventListener('pointermove', (e) => {
      if (down) {
        const dx = e.clientX - down[0], dy = e.clientY - down[1];
        moved += Math.abs(dx) + Math.abs(dy);
        if (dragMode === 'pan') panBy(dx, dy);
        else if (dragMode === 'orbit') {
          cam.yaw += dx * 0.4;
          cam.pitch = Math.max(-88, Math.min(88, cam.pitch + dy * 0.4));
          applyCam();
        }
        down = [e.clientX, e.clientY];
        return;
      }
      const now = performance.now();
      if (now - lastHover < 16) return;
      lastHover = now;
      const cw = canvas.clientWidth || 1, ch = canvas.clientHeight || 1;
      const hit = pickAtLocal(e.offsetX, e.offsetY, cw, ch);
      const ref = hit && hit.ref;
      const h = hoverRefFor(ref);
      if (h !== hoverRef) { hoverRef = h; paint(); }
      const boardRef = ref === 'neo3-plus_PCB' || ref === 'neo3-plus_silkscreen' || ref === 'holes';
      canvas.style.cursor = (ref && !boardRef) ? 'pointer' : 'default';
      if (pickFn) pickFn(ref, e.clientX, e.clientY, 'hover');
    });
    canvas.addEventListener('pointerup', (e) => {
      if (dragButton === 0 && down && moved < 6) {
        const cw = canvas.clientWidth || 1, ch = canvas.clientHeight || 1;
        const hit = pickAtLocal(e.offsetX, e.offsetY, cw, ch);
        if (pickFn) pickFn(hit && hit.ref, e.clientX, e.clientY, 'click');
      }
      down = null;
      dragButton = 0;
    });
    canvas.addEventListener('pointercancel', () => { down = null; dragButton = 0; });
    canvas.addEventListener('wheel', (e) => {
      e.preventDefault();
      stopCamAnim();
      cam.dist = Math.max(18, Math.min(700, cam.dist * (1 + Math.sign(e.deltaY) * 0.1)));
      applyCam();
    }, { passive: false });
    let pinch = null;
    canvas.addEventListener('touchmove', (e) => {
      if (e.touches.length !== 2) return;
      const d = Math.hypot(e.touches[0].clientX - e.touches[1].clientX, e.touches[0].clientY - e.touches[1].clientY);
      if (pinch) { cam.dist = Math.max(18, Math.min(700, cam.dist * pinch / d)); applyCam(); }
      pinch = d;
    }, { passive: true });
    canvas.addEventListener('touchend', () => { pinch = null; });
  }

  function objVisible(o) {
    for (let p = o; p; p = p.parent) if (p.visible === false) return false;
    return true;
  }

  function pickAtLocal(x, y, w, h) {
    if (!pickNdc) pickNdc = new (T().Vector2)();
    pickNdc.set((x / w) * 2 - 1, -(y / h) * 2 + 1);
    pickRay.setFromCamera(pickNdc, camera);
    const groups = [boardSkinGroup, switchGroup, meshGroup, endpointGroup, headerGroup];
    if (screenPickGroup) groups.push(screenPickGroup);
    if (screenPinGroup && screenPinGroup.children.length) groups.push(screenPinGroup);
    if (plugGroup.children.some((c) => c.visible)) groups.push(plugGroup);
    const hits = pickRay.intersectObjects(groups, true);
    const hit = hits.find((h2) => objVisible(h2.object));
    if (!hit) return null;
    const o = hit.object;
    if (o.userData.endpoint) return { ref: o.userData.endpoint, dist: hit.distance };
    let ref = o.userData.plug ? 'plug:' + o.userData.plug : o.userData.ref;
    /* 点到 CON30 时再按落点归到具体那一根针：悬停/点击都只亮一根 */
    if (ref === 'CON30') ref = pinRefAt(hit.point) || ref;
    return { ref, dist: hit.distance };
  }

  function pickAt(cssX, cssY) {
    const r = canvas.getBoundingClientRect();
    return pickAtLocal(cssX - r.left, cssY - r.top, r.width, r.height);
  }

  function pinRefAt(p) {
    const all = (window.NK_BOARD_PARTS || { parts: [] }).parts || [];
    const part = all.find((x) => x.ref === 'CON30');
    if (!part || !part.pads) return '';
    let best = '', bd = Infinity;
    for (const pad of part.pads) {
      const dx = p.x - (100 + pad.x), dz = p.z - (100 + pad.y);
      const d = dx * dx + dz * dz;
      if (d < bd) { bd = d; best = 'CON30_PIN' + String(pad.n).padStart(2, '0'); }
    }
    return best;
  }

  /* ---------------- 高亮 ---------------- */
  function pinNo(ref) {
    if (!ref || ref.indexOf('CON30_PIN') !== 0) return 0;
    return parseInt(ref.slice(9), 10) || 0;
  }

  function paint() {
    /* 穿透遮挡用「幽灵副本」：原件保持正常深度（元件内部遮挡正常），
       另加一层不吃深度的半透明副本，被挡住时也能看到在闪。 */
    const ghostFor = (mesh, lit, amp2) => {
      if (!mesh) return;
      const TH = T();
      let g = mesh.userData.__ghost;
      if (!g) {
        const mat = new TH.MeshBasicMaterial({
          color: tintFor(false).clone(), transparent: true, opacity: 0.3,
          depthTest: false, depthWrite: false, blending: TH.AdditiveBlending, side: TH.DoubleSide,
        });
        g = new TH.Mesh(mesh.geometry, mat);
        g.raycast = () => {};
        g.frustumCulled = false;
        g.renderOrder = 26;
        mesh.add(g);
        mesh.userData.__ghost = g;
      }
      g.visible = lit;
      if (lit) g.material.opacity = 0.12 + 0.34 * (amp2 == null ? 1 : amp2);
    };
    const amp = pulsePhase;                       // null = 常亮；0..1 = 呼吸相位
    /* hover 是“鼠标指向”的轻微提示，只改基色/自发光，靠正常深度被遮挡；
       任务指示才使用穿透遮挡的 ghost + 呼吸动画。 */
    const litI = (hov) => (hov ? 0.32 : (amp == null ? 0.4 : 0.14 + 0.72 * amp));
    const litM = (hov) => (hov ? 0.38 : (amp == null ? 0.5 : 0.3 + 0.5 * amp));
    /* 单针高亮：CON30_PINxx 可以同时亮多根；CON30 仍然整件高亮 */
    const wanted = new Set();
    hlRefs.forEach((x) => { const n = pinNo(x); if (n) wanted.add(n); });
    const hn = pinNo(hoverRef); if (hn) wanted.add(hn);
    ranges.forEach((r) => {
      const on = hlRefs.indexOf(r.ref) >= 0, hov = r.ref === hoverRef;
      const m = r.mesh.material;
      if (!r.baseColor) r.baseColor = m.color.clone();
      if (r.pinU) {
        r.pinU.uPins.value.fill(0);
        wanted.forEach((n) => { if (n >= 1 && n <= 26) r.pinU.uPins.value[n - 1] = 1; });
        r.pinU.uOn.value = wanted.size ? 1 : 0;
        if (r.pinU.uAmp) r.pinU.uAmp.value = amp == null ? 1 : Math.max(0.12, amp);
      }
      if (on || hov) {
        const tint = tintFor(hov && !on);
        m.emissive.copy(tint); m.emissiveIntensity = litI(hov);
        m.color.copy(r.baseColor).lerp(tint, litM(hov));   // 只加自发光在深色件上看不出来，基色也拉过去
      } else {
        m.emissive.setHex(0); m.emissiveIntensity = 0;
        m.color.copy(r.baseColor);
      }
    });
    /* 单针排针实例：一个实例 = 一根针的塑料 + 金属，整件一起亮/灭。 */
    hlPins.forEach((entries, ref) => {
      const on = hlRefs.indexOf(ref) >= 0, hov = ref === hoverRef;
      const tint = tintFor(hov && !on);
      entries.forEach(({ m, base, mesh }) => {
        if (!m.emissive) return;
        ghostFor(mesh, on, amp);
        if (on || hov) {
          m.emissive.copy(tint); m.emissiveIntensity = litI(hov);
          m.color.copy(base).lerp(tint, litM(hov));
        } else {
          m.emissive.setHex(0); m.emissiveIntensity = 0;
          m.color.copy(base);
        }
      });
    });
    plugHl.forEach(({ m, base, mesh }) => {
      const tint = tintFor(false);
      ghostFor(mesh, true, amp);
      if (!m.emissive) return;
      /* 与 hlParts 完全同一套呼吸强度/混色，插拔件不再单独用一种闪烁 */
      m.emissive.copy(tint);
      m.emissiveIntensity = litI(false);
      m.color.copy(base).lerp(tint, litM(false));
    });
    hlParts.forEach((entries, ref) => {
      const on = hlRefs.indexOf(ref) >= 0, hov = ref === hoverRef;
      const tint = tintFor(hov && !on);
      entries.forEach(({ m, base, mesh }) => {
        if (!m.emissive) return;
        ghostFor(mesh, on, amp);
        if (on || hov) {
          m.emissive.copy(tint); m.emissiveIntensity = litI(hov);
          m.color.copy(base).lerp(tint, litM(hov));
        } else {
          m.emissive.setHex(0); m.emissiveIntensity = 0;
          m.color.copy(base);
        }
      });
    });
    requestRender();
  }

  function resize() {
    if (!renderer || !container) return;
    const r = container.getBoundingClientRect();
    if (r.width < 2 || r.height < 2) return;
    renderer.setSize(r.width, r.height, false);
    camera.aspect = r.width / r.height;
    updateOrtho();
    requestRender();
  }

  /* 只在场景变化时渲染：静止时不再每帧占用 GPU/CPU。 */
  /** 高亮某个插拔件（如停靠状态的 TF 卡）；传 null 清除。 */
  function setPlugHighlight(id) {
    if (plugHlId === id) return;
    plugHl.forEach(({ m, base, mesh }) => {
      if (m.emissive) { m.emissive.setHex(0); m.emissiveIntensity = 0; }
      m.color.copy(base);
      if (mesh && mesh.userData.__ghost) mesh.userData.__ghost.visible = false;
    });
    plugHl = [];
    plugHlId = id || null;
    if (plugHlId && plugGroup) {
      const grp = plugGroup.userData[plugHlId];
      if (grp) {
        grp.traverse((o) => {
          if (!o.isMesh) return;
          (Array.isArray(o.material) ? o.material : [o.material]).forEach((m) => {
            if (m && m.color && m.emissive) plugHl.push({ m, base: m.color.clone(), mesh: o });
          });
        });
      }
    }
    paint();
    requestRender();
  }

  function startPulse() {
    if (pulseRAF || !pulseWant) return;
    const tick = (t) => {
      pulseRAF = requestAnimationFrame(tick);
      if (t - pulseLast < 60) return;              // ~16 fps 足够看出呼吸，别烧 GPU
      pulseLast = t;
      pulsePhase = 0.5 + 0.5 * Math.sin(t / 230);
      paint();
      requestRender();
    };
    pulseRAF = requestAnimationFrame(tick);
  }
  function stopPulse() {
    if (pulseRAF) cancelAnimationFrame(pulseRAF);
    pulseRAF = 0; pulseLast = 0; pulsePhase = null;
    paint();
    requestRender();
  }
  function requestRender() {
    renderDirty = true;
    if (!rafOn) { rafOn = true; requestAnimationFrame(loop); }
  }
  function requestShadow() {
    renderer.shadowMap.needsUpdate = true;
    requestRender();
  }
  function loop() {
    rafOn = false;
    if (!renderDirty) return;
    if (!(renderer && ready && container && !container.hidden)) return;
    renderDirty = false;
    const t0 = performance.now();
    renderer.render(scene, camera);
    lastRenderMs = performance.now() - t0;
    renderCount++;
    if (renderDirty) requestRender();     // 渲染期间又被事件标脏
  }

  /* ---------------- 场景导出（构建期/人工校对用） ----------------
     把 root3d 里当前可见的网格导出成 GLB（世界坐标，单位 mm）。
     不导出贴图；材质只带 baseColor/metal/rough，够在 Blender 里对齐插头和座子。
     浏览器里返回 base64；构建脚本把它写到 web/diy/assets/board/review-scene.glb。 */
  function exportGLB(opts) {
    const TH = T();
    opts = opts || {};
    scene.updateMatrixWorld(true);
    const visible = (o) => { let p = o; while (p) { if (!p.visible) return false; p = p.parent; } return true; };
    const meshes = [];
    root3d.traverse((o) => {
      if (!o.isMesh || !o.geometry || !o.geometry.attributes.position) return;
      if (!visible(o)) return;
      if (o.userData.isCable && opts.skipCables) return;
      if (o.name === 'NK_SCREEN_DISPLAY' && opts.skipScreen) return;
      meshes.push(o);
    });

    const buffers = [], views = [], accessors = [], materials = [], matIndex = new Map(), outMeshes = [], nodes = [];
    let byteLength = 0;
    const addView = (typed, target) => {
      const bytes = new Uint8Array(typed.buffer, typed.byteOffset, typed.byteLength);
      const pad = (4 - (byteLength % 4)) % 4;
      if (pad) { buffers.push(new Uint8Array(pad)); byteLength += pad; }
      buffers.push(bytes); views.push({ buffer: 0, byteOffset: byteLength, byteLength: bytes.length, target });
      byteLength += bytes.length;
      return views.length - 1;
    };
    const addAccessor = (typed, compType, type, count, min, max, target) => {
      const vi = addView(typed, target);
      const a = { bufferView: vi, componentType: compType, count, type };
      if (min) { a.min = min; a.max = max; }
      accessors.push(a);
      return accessors.length - 1;
    };
    const addMaterial = (m) => {
      if (!m) m = { color: new TH.Color(0.7, 0.7, 0.7), metalness: 0, roughness: 0.6 };
      if (matIndex.has(m)) return matIndex.get(m);
      const pbr = { baseColorFactor: [m.color ? m.color.r : 0.7, m.color ? m.color.g : 0.7, m.color ? m.color.b : 0.7, m.opacity == null ? 1 : m.opacity] };
      if (m.metalness != null) pbr.metallicFactor = m.metalness;
      if (m.roughness != null) pbr.roughnessFactor = m.roughness;
      const mat = { name: m.name || ('mat_' + materials.length), pbrMetallicRoughness: pbr };
      if (m.transparent || (m.opacity != null && m.opacity < 1)) { mat.alphaMode = 'BLEND'; mat.doubleSided = true; }
      if (m.side === TH.DoubleSide) mat.doubleSided = true;
      if (m.emissive && (m.emissive.r || m.emissive.g || m.emissive.b)) mat.emissiveFactor = [m.emissive.r, m.emissive.g, m.emissive.b];
      materials.push(mat); matIndex.set(m, materials.length - 1);
      return materials.length - 1;
    };

    const nm = new TH.Matrix3();
    const v = new TH.Vector3();
    for (const o of meshes) {
      const g = o.geometry;
      const pa = g.attributes.position;
      const na = g.attributes.normal;
      const p = new Float32Array(pa.count * 3);
      const n = na ? new Float32Array(na.count * 3) : null;
      nm.getNormalMatrix(o.matrixWorld);
      for (let i = 0; i < pa.count; i++) {
        v.fromBufferAttribute(pa, i).applyMatrix4(o.matrixWorld);
        p[i * 3] = v.x / 1000; p[i * 3 + 1] = v.y / 1000; p[i * 3 + 2] = v.z / 1000;  // glTF 单位=m
        if (n) {
          v.fromBufferAttribute(na, i).applyMatrix3(nm).normalize();
          n[i * 3] = v.x; n[i * 3 + 1] = v.y; n[i * 3 + 2] = v.z;
        }
      }
      const min = [Infinity, Infinity, Infinity], max = [-Infinity, -Infinity, -Infinity];
      for (let i = 0; i < p.length; i += 3) for (let a = 0; a < 3; a++) {
        if (p[i + a] < min[a]) min[a] = p[i + a];
        if (p[i + a] > max[a]) max[a] = p[i + a];
      }
      const posAcc = addAccessor(p, 5126, 'VEC3', pa.count, min, max, 34962);
      const nrmAcc = n ? addAccessor(n, 5126, 'VEC3', pa.count, null, null, 34962) : null;
      const idx = g.index ? g.index.array : null;
      const draw = g.drawRange || { start: 0, count: Infinity };
      const dStart = Math.max(0, draw.start || 0);
      const dCount = draw.count === Infinity ? (idx ? idx.length : pa.count) : Math.min(draw.count, (idx ? idx.length : pa.count) - dStart);
      const groups = (g.groups && g.groups.length) ? g.groups : [{ start: dStart, count: dCount, materialIndex: 0 }];
      const prims = [];
      for (const grp of groups) {
        const s0 = Math.max(0, grp.start | 0), c0 = Math.max(0, Math.min(grp.count | 0, (idx ? idx.length : pa.count) - s0));
        const out = new Uint32Array(c0);
        for (let i = 0; i < c0; i++) out[i] = idx ? idx[s0 + i] : (s0 + i);
        const ia = addAccessor(out, 5125, 'SCALAR', out.length, null, null, 34963);
        const m = Array.isArray(o.material) ? (o.material[grp.materialIndex] || o.material[0]) : o.material;
        const prim = { attributes: { POSITION: posAcc }, indices: ia, material: addMaterial(m), mode: 4 };
        if (nrmAcc != null) prim.attributes.NORMAL = nrmAcc;
        prims.push(prim);
      }
      if (!prims.length) continue;
      outMeshes.push({ name: o.name || o.userData.ref || ('mesh_' + outMeshes.length), primitives: prims });
      nodes.push({ mesh: outMeshes.length - 1, name: o.name || o.userData.ref || ('mesh_' + nodes.length) });
    }

    const bin = new Uint8Array(byteLength);
    let off = 0;
    for (const b of buffers) { bin.set(b, off); off += b.length; }
    const json = {
      asset: { version: '2.0', generator: 'NoKeyOut exportGLB' },
      scene: 0, scenes: [{ nodes: nodes.map((_, i) => i) }], nodes,
      meshes: outMeshes, materials, accessors, bufferViews: views,
      buffers: [{ byteLength: bin.length }]
    };
    let js = new TextEncoder().encode(JSON.stringify(json));
    const jsPad = (4 - (js.length % 4)) % 4;
    if (jsPad) { const q = new Uint8Array(js.length + jsPad); q.set(js); q.fill(0x20, js.length); js = q; }
    const binPad = (4 - (bin.length % 4)) % 4;
    const bin2 = new Uint8Array(bin.length + binPad); bin2.set(bin);
    const total = 12 + 8 + js.length + 8 + bin2.length;
    const out = new Uint8Array(total);
    const dv = new DataView(out.buffer);
    dv.setUint32(0, 0x46546C67, true); dv.setUint32(4, 2, true); dv.setUint32(8, total, true);
    dv.setUint32(12, js.length, true); dv.setUint32(16, 0x4E4F534A, true); out.set(js, 20);
    const b0 = 20 + js.length;
    dv.setUint32(b0, bin2.length, true); dv.setUint32(b0 + 4, 0x004E4942, true); out.set(bin2, b0 + 8);
    let s = '';
    for (let i = 0; i < out.length; i += 0x8000) s += String.fromCharCode.apply(null, out.subarray(i, i + 0x8000));
    return btoa(s);
  }

  /* ---------------- API ---------------- */
  window.NKView3D = {
    mount(cont) { if (!renderer) init(cont); else container = cont; return true; },
    isReady: () => ready,
    setMesh, addPlug, setPlug, setPhoto, setPhotoRect, exportGLB,
    boardPartBBox(ref) {
      const TH = T();
      const b = new TH.Box3();
      boardSkinGroup.traverse((o) => {
        if (o.isMesh && o.userData.ref === ref && o.visible) b.expandByObject(o);
      });
      if (b.isEmpty()) return null;
      return { min: b.min.toArray().map((v) => +v.toFixed(2)), max: b.max.toArray().map((v) => +v.toFixed(2)) };
    },
    plugMaterialStats(id) {
      const grp = plugGroup && plugGroup.userData[id];
      return grp && grp.userData.materialStats ? grp.userData.materialStats : null;
    },
    plugBBox(id) {
      const grp = plugGroup && plugGroup.userData[id];
      if (!grp) return null;
      const TH = T();
      grp.updateMatrixWorld(true);
      const b = new TH.Box3();
      grp.traverse((o) => { if (o.isMesh && !o.userData.isCable) b.expandByObject(o); });
      if (b.isEmpty()) return null;
      return { min: b.min.toArray().map((v) => +v.toFixed(2)), max: b.max.toArray().map((v) => +v.toFixed(2)) };
    },
    addWiringModule, addWiringEndpoints, setWires, previewJoint, clearPreview, setSwitchState, sparkEndpoints, attachScreenDisplay, setScreenBoot, setScreenCue,
    loadFBX, loadFBXModel, clearWiringModules,
    clearPinInstances, clearScreenPinInstances, setScreenPinGroup, addPinInstance, removeBasePart, pinTop, pinTip, pinCount, addBoardSkin, requestShadow,
    addSwitchParts, addSilkLabel, addScreenBackSilk, addScreenFrontSilk,
    pinInstances() {
      const out = {};
      const add = (g) => {
        const b = new (T().Box3)().setFromObject(g);
        out[g.userData.ref] = {
          min: b.min.toArray().map((v) => +v.toFixed(3)),
          max: b.max.toArray().map((v) => +v.toFixed(3)),
        };
      };
      headerGroup.children.forEach(add);
      if (screenPinGroup) screenPinGroup.children.forEach(add);
      return out;
    },
    /** 单针实例内部各材质组的 y 范围（塑料/金属分界，米制 mm）。 */
    pinGroups(ref) {
      const TH = T();
      const g = headerGroup.children.find((c) => c.userData.ref === ref)
        || (screenPinGroup && screenPinGroup.children.find((c) => c.userData.ref === ref));
      if (!g) return [];
      const out = [];
      const v = new TH.Vector3();
      g.updateMatrixWorld(true);
      g.traverse((o) => {
        if (!o.isMesh || !o.geometry) return;
        const geo = o.geometry, idx = geo.index;
        (geo.groups || []).forEach((grp) => {
          let minY = Infinity, maxY = -Infinity;
          for (let i = grp.start; i < grp.start + grp.count; i++) {
            const vi = idx ? idx.getX(i) : i;
            v.fromBufferAttribute(geo.attributes.position, vi).applyMatrix4(o.matrixWorld);
            if (v.y < minY) minY = v.y;
            if (v.y > maxY) maxY = v.y;
          }
          if (minY !== Infinity) out.push({ name: o.name, materialIndex: grp.materialIndex, minY: +minY.toFixed(3), maxY: +maxY.toFixed(3) });
        });
      });
      return out;
    },
    partVisibility() {
      const out = {};
      meshGroup.children.forEach((g) => { out[g.userData.ref] = g.visible; });
      return out;
    },
    rangeInfo(ref) {
      return ranges.filter((r) => r.ref === ref).map((r) => ({
        color: r.mesh.material.color.getHexString(),
        emissive: r.mesh.material.emissive && r.mesh.material.emissive.getHexString(),
        intensity: r.mesh.material.emissiveIntensity,
        visible: r.mesh.visible,
      }));
    },
    renderOnce() {
      if (renderer) {
        const t0 = performance.now();
        renderer.render(scene, camera);
        lastRenderMs = performance.now() - t0;
        renderCount++; renderDirty = false;
      }
      return true;
    },
    project(x, y, z) {
      const TH = T();
      camera.updateMatrixWorld(true);
      const v = new TH.Vector3(x, y, z).project(camera);
      const r = canvas.getBoundingClientRect();
      return { x: r.left + (v.x + 1) / 2 * r.width, y: r.top + (1 - v.y) / 2 * r.height, ndc: [v.x, v.y, v.z] };
    },
    photoRect: () => photoRect,
    setMeshVisible(v) {
      meshVisible = !!v;
      meshGroup.visible = meshVisible;
      decoGroup.visible = meshVisible;      // 金环是板体的一部分，一起隐藏
      plugGroup.visible = meshVisible;      // 「仅 2D」时插拔物也别留在画面里
      requestShadow();
    },
    /** 抽象模式：只显示白名单里的节点（PCB + 互动接插件），其余隐藏。 */
    setPartsVisible(refs) {
      const keep = refs ? new Set(refs) : null;
      meshGroup.children.forEach((g) => { g.visible = !keep || keep.has(g.userData.ref); });
      requestShadow();
    },
    showPhoto(v) { if (photoMesh) { photoMesh.visible = !!v; requestRender(); } },
    onPick(fn) { pickFn = fn; },
    setHighlight(refs) {
      const next = refs || [];
      const sig = next.join('|');
      if (sig !== hlSig) { hlSig = sig; hlRefs = next; paint(); }
      return hlRefs.slice();
    },
    highlight: () => hlRefs.slice(),
    setHover(ref) { hoverRef = ref; paint(); },
    wireDebug() {
      wiringGroup.updateMatrixWorld(true);
      const meshes = [];
      wiringGroup.traverse((m) => { if (m.isMesh && m.userData.wirePart) meshes.push(m); });
      const count = (kind) => meshes.filter((m) => m.userData.wirePart === kind).length;
      const bodies = meshes.filter((m) => m.userData.wireRole === 'body');
      const dup = meshes.find((m) => m.userData.wirePart === 'dupont');
      const bb = dup ? new (T().Box3)().setFromObject(dup) : null;
      const groups = wiringGroup.children.filter((g) => g.type === 'Group');
      return {
        wires: wireList.length, dupont: count('dupont'), solder: count('solder'), tubes: count('wire'),
        connMinY: bb ? +bb.min.y.toFixed(2) : null, connMaxY: bb ? +bb.max.y.toFixed(2) : null,
        connMinYs: bodies.map((m) => +new (T().Box3)().setFromObject(m).min.y.toFixed(2)),
        growing: !!wireGrow, inserting: inserting.size, preview: previewIds.length,
        connRots: groups.map((g) => +g.rotation.x.toFixed(3)),
        connPositions: groups.map((g) => g.position.toArray().map((v) => +v.toFixed(2)))
      };
    },
    switchVisual() {
      if (!switchToggleMesh) return null;
      const b = new (T().Box3)().setFromObject(switchToggleMesh);
      return {
        on: switchState,
        x: +((b.min.x + b.max.x) / 2).toFixed(3),
        axis: +switchAxisX.toFixed(3),
        base: +switchBaseWorldX.toFixed(3)
      };
    },
    sparkCount: () => sparkBursts.length,
    silkDebug: () => silkLabels.slice(),
    screenDebug: () => {
      const TH = T();
      const bbox = (o) => {
        if (!o) return null;
        const b = new TH.Box3().setFromObject(o);
        return { min: b.min.toArray(), max: b.max.toArray() };
      };
      return {
        visible: !!screenBootOn, boot: !!screenBootOn,
        meshVisible: !!(screenDisplay && screenDisplay.visible), animating: !!screenAnimRAF,
        cue: screenCueOn, cueGlow: +screenCueLevel.toFixed(2), cueParts: screenHlParts.length,
        locked: screenPageName === 'locked', source: screenPageName,
        lang: screenLang(),
        wasm: screenWasmReady, native: screenWasmReady && nativeScreenPage(screenPageName), wasmPage: screenWasmPage, anim: screenLastAnim,
        map: screenFrameTex ? screenFrameTex.uuid : null,
        size: screenFrameCanvas ? [screenFrameCanvas.width, screenFrameCanvas.height] : null,
        face: screenDisplay ? screenDisplay.material.side : null,
        depthTest: screenDisplay ? screenDisplay.material.depthTest : null,
        nearest: screenFrameTex ? (screenFrameTex.magFilter === T().NearestFilter) : false,
        displayBox: bbox(screenDisplay), glassBox: bbox(screenGlass)
      };
    },
    screenFrame() {
      return screenFrameCanvas ? screenFrameCanvas.toDataURL('image/png') : null;
    },
    screenFrameStats() {
      if (!screenFrameCanvas) return null;
      const d = screenFrameCtx.getImageData(0, 0, SCR_W, SCR_H).data;
      let first = -1, last = -1, ink = 0;
      for (let y = 0; y < SCR_H; y++) {
        for (let x = 0; x < SCR_W; x++) {
          const i = (y * SCR_W + x) * 4;
          if (d[i] || d[i + 1] || d[i + 2]) { if (first < 0) first = y; last = y; ink++; break; }
        }
      }
      return { first, last, ink, w: SCR_W, h: SCR_H };
    },
    screenCenter() {
      if (!screenPickGroup) return null;
      screenPickGroup.updateMatrixWorld(true);
      const b = new (T().Box3)().setFromObject(screenPickGroup);
      return b.getCenter(new (T().Vector3)()).toArray();
    },
    annotationAnchors() {
      if (annoAnchorsCache) return annoAnchorsCache;
      const TH = T();
      const out = {};
      const kb = window.NKView3D.boardPartBBox('K2');
      if (kb) out.usr = [(kb.min[0] + kb.max[0]) / 2, kb.max[1] + 0.3, (kb.min[2] + kb.max[2]) / 2];
      if (switchToggleMesh) {
        switchToggleMesh.updateMatrixWorld(true);
        const sb = new TH.Box3().setFromObject(switchToggleMesh);
        out.switch = [(sb.min.x + sb.max.x) / 2, sb.max.y + 0.4, (sb.min.z + sb.max.z) / 2];
      }
      annoAnchorsCache = out;
      return out;
    },
    onCameraChange(fn) { if (typeof fn === 'function') camCbs.push(fn); },
    focus(ref) {
      const p = byRef.get(ref);
      if (!p) return false;
      const bb = p.bb;
      cam.target = [(bb[0] + bb[3]) / 2, (bb[1] + bb[4]) / 2, (bb[2] + bb[5]) / 2];
      cam.dist = Math.max(28, Math.max(bb[3] - bb[0], bb[4] - bb[1], bb[5] - bb[2]) * 2.2);
      applyCam();
      return true;
    },
    /** 取景到若干接线端点（pin:/scr:/sw:）的包围盒：接线步骤自动把两端都放进画面。 */
    pinSample() {
      const out = [];
      hlPins.forEach((entries, ref) => {
        if (!entries.length) return;
        const m = entries[0].m;
        out.push({ ref, ei: +(m.emissiveIntensity || 0).toFixed(3), color: m.color.getHexString(), emissive: m.emissive.getHexString(), depth: m.depthTest });
      });
      return out;
    },
    endpointPoint(id) { const p = endpointAt.get(id); return p ? [+p[0], +p[1], +p[2]] : null; },
    setPulse(on) {
      const want = !!on;
      if (want && !pulseWant) pulseLast = 0;
      pulseWant = want;
      if (pulseWant) {
        /* 标签页被冻结/恢复后 RAF 可能停住，重新进入步骤时重启一次 */
        if (pulsePhase == null && pulseRAF) { cancelAnimationFrame(pulseRAF); pulseRAF = 0; }
        startPulse();
      } else stopPulse();
    },
    setPlugHighlight(id) { setPlugHighlight(id); },
    focusEndpoints(ids, opts) {
      const pts = (ids || []).map((id) => endpointAt.get(id)).filter(Boolean);
      if (!pts.length) return false;
      let minx = Infinity, miny = Infinity, minz = Infinity;
      let maxx = -Infinity, maxy = -Infinity, maxz = -Infinity;
      pts.forEach((p) => {
        minx = Math.min(minx, p[0]); maxx = Math.max(maxx, p[0]);
        miny = Math.min(miny, p[1]); maxy = Math.max(maxy, p[1]);
        minz = Math.min(minz, p[2]); maxz = Math.max(maxz, p[2]);
      });
      cam.target = [(minx + maxx) / 2, (miny + maxy) / 2, (minz + maxz) / 2];
      const span = Math.max(maxx - minx, maxy - miny, maxz - minz, 18);
      cam.dist = Math.max(34, span * 2.4);
      if (opts && opts.yaw != null) cam.yaw = opts.yaw;
      if (opts && opts.pitch != null) cam.pitch = opts.pitch;
      applyCam();
      return true;
    },
    frame(kind) {
      if (kind === 'screen-back') { const over = window.NKBOARD3D && window.NKBOARD3D.screenPose && window.NKBOARD3D.screenPose() === 'over'; cam.target = [124, 6, over ? 122 : 76]; cam.yaw = 90; cam.pitch = 45; cam.dist = 95; }
      else if (kind === 'screen-face') {
        animateCam({ target: [124, 0, 124], yaw: 90, pitch: 88, dist: 92 }, 760);
        return;
      }
      else if (kind === 'wiring') { cam.target = [124, 5, 110]; cam.yaw = -35; cam.pitch = 34; cam.dist = 200; }
      else if (kind === 'top') { cam.target = [124, 0, 124]; cam.yaw = 90; cam.pitch = 90; cam.dist = 200; }
      else if (kind === 'plugs') { cam.target = [118, 2, 122]; cam.yaw = -90; cam.pitch = 24; cam.dist = 96; }
      // 并排对比：照片被挪到 world x0+90（屏幕左侧），板在 100..148 → 目标取两者中点、拉远到都进画面
      else if (kind === 'both') { cam.target = [178, 0, 122]; cam.yaw = -90; cam.pitch = 62; cam.dist = 185; }
      else { cam.target = [124, 6, 124]; cam.yaw = -35; cam.pitch = 34; cam.dist = 130; }
      applyCam();
    },
    show() { if (container) { container.hidden = false; resize(); } },
    hide() { if (container) container.hidden = true; },
    resize,
    debug() {
      const lit = ranges.filter((r) => hlRefs.indexOf(r.ref) >= 0);
      const litPins = Array.from(hlPins.keys()).filter((k) => hlRefs.indexOf(k) >= 0);
      return {
        lib: 'three r' + (window.THREE ? window.THREE.REVISION : '?'),
        parts: parts.length, ranges: ranges.length,
        verts: parts.reduce((a, p) => a + p.verts, 0),
        tris: parts.reduce((a, p) => a + p.tris, 0),
        hl: hlRefs.slice(), hlRanges: lit.length, hlPins: litPins, pinInstances: hlPins.size,
        hlTris: lit.reduce((a, r) => a + r.count / 3, 0),
        pinRanges: ranges.filter((r) => r.pinU).map((r) => {
          const a = r.mesh.geometry.getAttribute('pinId');
          const ids = a ? Array.from(new Set(Array.from(a.array))).sort((x, y) => x - y) : [];
          const pins = Array.from(r.pinU.uPins.value).map((v, i) => v ? i + 1 : 0).filter(Boolean);
          return { ref: r.ref, on: r.pinU.uOn.value, amp: r.pinU.uAmp ? r.pinU.uAmp.value : null, pins, compiled: !!r.mesh.material.userData.pinCompiled, hook: typeof r.mesh.material.onBeforeCompile, version: r.mesh.material.version, visible: r.mesh.visible && r.mesh.parent.visible && r.mesh.parent.parent.visible, count: r.count, ids: ids.slice(0, 30) };
        }),
        fbx: fbxModels.map((o) => {
          const b = new (T().Box3)().setFromObject(o);
          const s = b.getSize(new (T().Vector3)());
          return { name: o.name, size: [+s.x.toFixed(1), +s.y.toFixed(1), +s.z.toFixed(1)], children: o.children.map((c) => c.name) };
        }),
        gltfLoader: typeof window.THREE.GLTFLoader,
        renderer: renderer ? {
          calls: renderer.info.render.calls,
          triangles: renderer.info.render.triangles,
          geometries: renderer.info.memory.geometries,
          textures: renderer.info.memory.textures,
          renders: renderCount,
          lastMs: +lastRenderMs.toFixed(2),
          pixelRatio: renderer.getPixelRatio(),
        } : null,
        cam: { yaw: +cam.yaw.toFixed(1), pitch: +cam.pitch.toFixed(1), dist: +cam.dist.toFixed(1), target: cam.target.map((v) => +v.toFixed(1)), anim: !!camAnim },
        pulse: { phase: pulsePhase == null ? null : +pulsePhase.toFixed(3), raf: !!pulseRAF, want: pulseWant },
        plugHl: plugHlId,
        pos: camera.position.toArray().map((v) => +v.toFixed(1)),
      };
    },
    plugState() {
      const out = {};
      if (!plugGroup) return out;
      Object.keys(plugGroup.userData).forEach((id) => {
        const g = plugGroup.userData[id];
        if (!g || !g.isObject3D) return;
        out[id] = { visible: !!g.visible, pos: g.position.toArray().map((v) => +v.toFixed(2)) };
      });
      return out;
    },
    _pick: pickAt,
  };
})();
