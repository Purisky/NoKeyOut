// 关卡数据：第一关「拨动开关门控接入 · 极简 2 线」（双语）
// 路线：SoC 内部上拉（DT bias-pull-up）+ 软件去抖（契约 3×50ms）→ 2 根线、零面包板、零电阻电容。
//       也可改用外置上拉/消抖的 RC 硬件变体（可选，不是必经步骤）。
window.registerLevel({
  id: "level-1-switch",
  order: 1,
  mapKey: "lv1",
  // 部件按顺序出现：主板+开关 → 屏幕 →（上电与门控验收）→ 插卡/上电/网线
// 本关不接串口：有屏 + 网线即可（SSH）验收；串口只是屏没起来/网络没通时的后备手段，不进教程流程。
  stages: [
    "procurement",
    { kind: "wiring", from: 0, to: 2, title: { zh: "门控接线（主板 + 开关）", en: "Gate wiring (board + switch)" } },
    { kind: "wiring", from: 2, to: 10, title: { zh: "屏接线（屏幕已出现）", en: "Screen wiring" } },
    { kind: "verify", title: { zh: "上电与门控验收", en: "Power-up & gating" } },
    { kind: "wiring", from: 10, to: 13, title: { zh: "烧录 / 上电 / 网线", en: "Flash / power / LAN" } },
    "complete"
  ],
  title: { zh: "第一关 · 门控 + 屏接线（免焊 10 线）", en: "L1 · Gating + screen wiring (10 solderless wires)" },
  brief: {
    disciplines: [
      { zh: "改线必断电（sudo poweroff + 拔 USB-C）", en: "Rewire only when powered off (poweroff + unplug USB-C)" },
      { zh: "先检查、后上电", en: "Inspect before powering on" },
      { zh: "一次只加一个器件，每步有可解释的现象再往下", en: "One device at a time; every step must have an explainable observation" },
      { zh: "不接串口：有屏 + 网线就能验收（SSH）；串口只是屏没起来/网络没通时的后备手段", en: "No serial wiring: the screen plus the network cable are enough (SSH); the UART is only the fallback when the display or the network is down" }
    ],
    irreversibles: [
      { zh: "5V（pin2/4）接到 GPIO 或 3V3 输入 → SoC 当场死，不可逆", en: "5V (pin2/4) into a GPIO or 3.3V input kills the SoC — irreversible" },
      { zh: "开关一端接 pin11、另一端必须接 GND；接到 5V = 烧 GPIO", en: "One switch lead goes to pin11; the other MUST go to GND — putting it on 5V burns the GPIO" },
      { zh: "杜邦线搭到 5V 与 GND 之间（或在排针上蹭）→ 短路烧板", en: "Jumper across 5V and GND (or brushing the header) shorts the board" },
      { zh: "带电插拔排线 → IO 瞬态击穿", en: "Hot-plugging harnesses — transient blow-through of IO" }
    ]
  },
  procurement: [
    { id: "board", qty: 1,
      spec: { zh: "ARM64 主板（≥256MB 内存 / 百兆网口 / ≥16GB 存储；教程以 NanoPi NEO3 Plus 为参考）", en: "ARM64 board (≥256MB RAM / 100M Ethernet / ≥16GB storage; NanoPi NEO3 Plus is the reference)" },
      why: { zh: "本关的主角。排针上有空闲 GPIO4_A5（pin11）与 GND（pin9）", en: "The star of this level: free GPIO4_A5 (pin11) and GND (pin9) on the header." },
      channel: { zh: "通用电子元件渠道", en: "General electronics supplier" },
      dont: { zh: "认准 Plus：别买成无排针的 NEO3 基础版", en: "Make sure it is the Plus — not the headerless base NEO3." } },
    { id: "sw", qty: 1,
      spec: { zh: "拨动开关，锁存 2 档，**推荐带 10k 上拉的开关模块**（2.54 排针）；裸开关也行（改用 SoC 内部上拉）", en: "Latching 2-position toggle; a module WITH 10k pull-up (2.54mm header) is preferred; a bare switch also works (using the SoC's internal pull-up)" },
      why: { zh: "闭合 = 把 GPIO4_A5 短到 GND = LOCKED；断开 = 内部/外置上拉把它拉高 = UNLOCKED（失效开放）", en: "Closed = shorts GPIO4_A5 to GND = LOCKED; open = pulled HIGH by internal/external pull-up = UNLOCKED (fail-open)." },
      channel: { zh: "通用电子元件渠道", en: "General electronics supplier" },
      dont: { zh: "别买内置**下拉**的模块（按下才接通 3V3）→ 恒 LOCKED 不可用；别买带 LED 的模块", en: "Do NOT buy pull-DOWN modules (press connects 3V3) — permanently LOCKED; no LED modules." } },
    { id: "wires", qty: 2,
      spec: { zh: "母-母杜邦线 20cm ×2（买 40 根套装亦可）", en: "Female-female dupont 20cm ×2 (40-pc kit fine)" },
      why: { zh: "本关只需要 2 根：pin11 与 pin9 各一根", en: "Only two wires this level: one to pin11, one to pin9." },
      channel: { zh: "通用电子元件渠道", en: "General electronics supplier" },
      dont: { zh: "别买公-公或面包板专用线", en: "No male-male or breadboard-only wires." } },
    { id: "scr", qty: 1,
      spec: { zh: "ST7789 1.3\"/1.54\" 240×240 SPI 屏模块 —— 必须选「已焊排针版」（免焊）", en: "ST7789 1.3\"/1.54\" 240×240 SPI module — must pick the “header pre-soldered” version (solderless)" },
      why: { zh: "默认装配就带屏：显示 LOCKED/统计/状态，是本产品的脸。", en: "The screen is part of the default build: LOCKED state, stats, status — the product's face." },
      channel: { zh: "通用电子元件渠道", en: "General electronics supplier" },
      dont: { zh: "别买「排针未焊版」（要动烙铁）；也别买 NEO/NEO2 的 24-pin HAT 堆叠屏（26-pin 排布不同，插上去是错位短接）。", en: "Do not buy the unsoldered-header version (needs an iron), nor NEO/NEO2 24-pin HAT stacking screens (different 26-pin layout → offset shorts)." },
      choose: {
        prompt: { zh: "选购时必须定：哪一种排针方案？（本项目全程免焊）", en: "You must decide at purchase: which header option? (this project is solderless end to end)" },
        options: [
          { zh: "已焊排针版：杜邦线直插（推荐）", en: "Header pre-soldered: plug duponts straight in (recommended)" },
          { zh: "排针未焊版：要自己焊 8 针", en: "Header not soldered: you would have to solder 8 pins" },
          { zh: "裸屏 + 自备排针", en: "Bare panel + buy a header separately" }
        ],
        expect: 0,
        hint: { zh: "本项目是免焊方案：必须选「已焊排针版」，否则你就得买烙铁并焊接。", en: "This project is solderless: pick the pre-soldered header, otherwise you need an iron and soldering." }
      } },
    { id: "psu", qty: 1,
      spec: { zh: "USB-C 电源适配器 5V/3A 及以上（配 C 口线）", en: "USB-C power adapter, 5V/3A or better (with C cable)" },
      why: { zh: "NEO3 Plus 从 USB-C 供电（官方建议 5V/3A）", en: "The NEO3 Plus is powered over USB-C (official recommendation 5V/3A)." },
      channel: { zh: "通用电子元件渠道", en: "General electronics supplier" },
      dont: { zh: "别用 PD 诱骗出 9/12V 的头（板载 OVP 阈值 6V，会烧输入级）；上电前用 USB 电压表确认 5.0–5.2V", en: "Do not use a PD charger that negotiates 9/12V (on-board OVP trips at 6V); verify 5.0–5.2V with a USB meter first." } },
    { id: "tf", qty: 1,
      spec: { zh: "microSD/TF 卡 ≥16GB，Class 10 及以上", en: "microSD/TF card ≥16GB, Class 10 or better" },
      why: { zh: "系统从 TF 卡启动（也可用 eMMC 模块）", en: "The OS boots from the TF card (or the optional eMMC module)." },
      channel: { zh: "通用电子元件渠道", en: "General electronics supplier" },
      dont: { zh: "别用杂牌低速卡（Class 4/6）——启动慢且容易写坏", en: "Avoid slow no-name cards (Class 4/6)." } },
    { id: "reader", qty: 1,
      spec: { zh: "USB 读卡器（把镜像写到 TF 卡用）", en: "USB card reader (to flash the image onto the TF card)" },
      why: { zh: "在电脑上写官方 img 镜像，再插回板子", en: "Write the official .img on your PC, then put the card back in the board." },
      channel: { zh: "通用电子元件渠道", en: "General electronics supplier" },
      dont: { zh: "别直接拿板子当读卡器刷（eMMC 才走 RKDevTool，TF 卡走读卡器）", en: "Do not treat the board as a card reader — that path is for eMMC flashing only." } },
    { id: "lan", qty: 1,
      spec: { zh: "网线（接板子的以太网口 ↔ 路由器/交换机）", en: "Ethernet cable (board Ethernet port ↔ router/switch)" },
      why: { zh: "管理面/业务面都要走网口；本关的门控验收也从另一台机器走网络验证", en: "Panel and gateway both live on the wire; the gating acceptance is tested from another machine." },
      channel: { zh: "通用电子元件渠道", en: "General electronics supplier" },
      dont: { zh: "别用只有 4 芯的劣质线（只能跑百兆，甚至 link 不上）", en: "Avoid 4-wire junk cables (100M at best, or no link)." } }
  ],
  inspection: [
    { id: "board-silkscreen", kind: "ack",
      text: { zh: "在板边找到 pin1 标记（三角或“1”丝印），并确认：奇数排 1,3,…25，偶数排 2,4,…26，n 与 n+1 同列", en: "Found the pin-1 mark (triangle / “1” silkscreen): odd pins 1..25 top row, even 2..26 bottom row, n and n+1 same column" } },
    { id: "switch-type", kind: "select",
      prompt: { zh: "手上的开关属于哪种？", en: "What kind is your switch?" },
      options: [
        { zh: "锁存 2 档（带 10k 上拉模块，或裸开关）", en: "Latching 2-position (module with 10k pull-up, or a bare switch)" },
        { zh: "带 LED 指示的模块", en: "Module with LED indicator" },
        { zh: "按下才接通 3V3 的自复位模块（内置下拉）", en: "Momentary module with internal pull-down (press = 3V3)" }
      ], expect: 0,
      failHint: { zh: "只有锁存 2 档可用；内置下拉或带 LED 的模块要换掉，别将就", en: "Only the latching 2-position type works; replace pull-down or LED modules." },
      hints: [
        { zh: "拨一次应保持位置（锁存），松手不回弹", en: "Toggle it: it should stay (latching), not spring back." },
        { zh: "看商品页：有没有“内置下拉/上拉电阻”“带灯”", en: "Check the listing: any built-in pull resistor or LED?" },
        { zh: "正确答案：锁存 2 档（带 10k 上拉模块或裸开关）", en: "Correct: latching 2-position (10k pull-up module, or bare switch)." }
      ] },
    { id: "dt-pullup", kind: "ack",
      text: { zh: "若用裸开关：已确认后面会在 DT 里给 GPIO4_A5 开 bias-pull-up；若用带 10k 模块：模块自带，DT 可不配", en: "Bare switch: you will enable &gpio4A5 bias-pull-up in DT; 10k module: it is built in, DT optional" } },
    { id: "wire-fit", kind: "ack",
      text: { zh: "每根杜邦线母头插拔 3 次不脱、内芯无松动；排针上没有明显歪针", en: "Every dupont female end holds after 3 insertions; no bent header pins" } }
  ],
  wiring: {
    intro: { zh: "本关 10 根线、全程免焊：先门控 2 根（拨动开关），再屏 8 根（BLK→CS→DC→RST→MOSI→CLK→VCC→GND）。上拉由 SoC 内部（或开关模块内置）提供，不需要面包板。",
             en: "Ten solderless wires: two for the gate (toggle switch), then eight for the screen (BLK→CS→DC→RST→MOSI→CLK→VCC→GND). The pull-up is internal (or built into the switch module); no breadboard needed." },
    nodeLabel: { zh: "（本关不需要节点条）", en: "(no node strip this level)" },
    props: [
      { id: "tfcard", name: { zh: "TF 卡（待烧录）", en: "TF card (not flashed yet)" } },
      { id: "power", name: { zh: "USB-C 电源线", en: "USB-C power lead" } },
      { id: "lan", name: { zh: "网线", en: "Ethernet cable" } }
    ],
    pins: [
      { n: 1,  label: "3V3",        cls: "power" },
      { n: 2,  label: "5V ⚠",       cls: "danger" },
      { n: 3,  label: "I2C5",       cls: "keep" },
      { n: 4,  label: "5V ⚠",       cls: "danger" },
      { n: 5,  label: "I2C5",       cls: "keep" },
      { n: 6,  label: "GND",        cls: "gnd" },
      { n: 7,  label: "PWM0_M0 ★", cls: "target" },
      { n: 8,  label: "UART1_TX（不用）", cls: "keep" },
      { n: 9,  label: "GND ★",      cls: "gnd" },
      { n: 10, label: "UART1_RX（不用）", cls: "keep" },
      { n: 11, label: "GPIO4_A5 ★", cls: "target" },
      { n: 12, label: "",           cls: "plain" },
      { n: 13, label: "屏RST",      cls: "keep" },
      { n: 14, label: "GND",        cls: "gnd" },
      { n: 15, label: "屏DC",       cls: "keep" },
      { n: 16, label: "",           cls: "plain" },
      { n: 17, label: "3V3",        cls: "power" },
      { n: 18, label: "",           cls: "plain" },
      { n: 19, label: "SPI1 MOSI",  cls: "keep" },
      { n: 20, label: "GND",        cls: "gnd" },
      { n: 21, label: "SPI1 MISO",  cls: "keep" },
      { n: 22, label: "SPI1 CS",    cls: "keep" },
      { n: 23, label: "SPI1 CLK",   cls: "keep" },
      { n: 24, label: "歧义·不用",  cls: "warn" },
      { n: 25, label: "GND",        cls: "gnd" },
      { n: 26, label: "PWM",        cls: "keep" }
    ],
    components: [
      { id: "sw", name: { zh: "拨动开关", en: "Toggle switch" },
        leads: ["A", "C", "B"],
        note: { zh: "从左到右 A-C-B：C 是公共端 → pin11(GPIO4_A5)；B → pin9(GND)；A 悬空。开关只闭合 C-A 或 C-B，两侧外脚 A/B 永不互通", en: "Left to right A-C-B: C is common → pin11 (GPIO4_A5); B → pin9 (GND); A stays open. The switch only closes C-A or C-B — the two outer pins never connect to each other." } },
      { id: "scr", name: { zh: "ST7789 屏模块（已焊排针版）", en: "ST7789 module (header pre-soldered)" },
        leads: ["VCC", "GND", "BLK", "CLK", "MOSI", "CS", "DC", "RST"],
        module: { pcb: [27.8, 27.4], glass: [26, 26], active: [23.4, 23.4] },
        note: { zh: "免焊：出厂已焊 2.54 排针，杜邦线直插；BLK 接 pin7（PWM）", en: "Solderless: header pre-soldered, plug duponts straight in; BLK goes to pin7 (PWM)." } }
    ],
    steps: [
      { edge: ["sw:C", "pin:11"],
        observe: { zh: "开关公共端 C 落到 GPIO4_A5（pin11）。断电操作，插到底。", en: "The switch common C lands on GPIO4_A5 (pin11). Powered off, fully seated." },
        typicalFail: { zh: "插到相邻孔（pin12/pin13）或偶数排——读到的就不是这个脚", en: "Plugged into a neighbour (pin12/13) or the even row — you would read a different pin." },
        hints: [
          { zh: "pin11 在单数排、左起第 6 孔（丝印 GPIO4_A5 ★）", en: "pin11 is the 6th hole of the odd row (GPIO4_A5 ★)." },
          { zh: "接中间的 C（公共端），不是两侧的 A/B——A 与 B 之间永远不通", en: "Use the middle lead C (common), not the outer A/B — A and B never connect to each other." },
          { zh: "明确动作：开关中间引线 C → pin11（蓝色高亮针）", en: "Exact action: switch middle lead C → pin11 (blue pillar)." }
        ] },
      { edge: ["sw:B", "pin:9"],
        observe: { zh: "另一根落 GND：C 与 B 闭合时把 GPIO4_A5 短到地 = LOCKED 电平", en: "The other wire lands on GND: closing C to B shorts GPIO4_A5 low = the LOCKED level." },
        typicalFail: { zh: "错接到 pin2/4（5V）→ 上电即烧 GPIO。这是本关最危险的一根", en: "Into pin2/4 (5V) burns the GPIO on power-up — the most dangerous wire here." },
        hints: [
          { zh: "GND 还有 pin6/14/20/25；pin9 与 pin11 同排好走线", en: "GND is also on 6/14/20/25; pin9 shares the row with pin11." },
          { zh: "上电前必量：开关拨到 C-B 闭合时 pin11↔pin9 ≈0Ω", en: "Measure before powering: with C-B closed, pin11↔pin9 ≈0Ω." },
          { zh: "明确动作：开关外侧引线 B → pin9（GND，灰色针）", en: "Exact action: switch outer lead B → pin9 (GND, grey pillar)." }
        ] },
      { edge: ["scr:BLK", "pin:7"],
        observe: { zh: "背光接 pin7（PWM0_M0）：先给 100% 占空比当常亮，用来确认「屏亮不亮」。", en: "Backlight goes to pin7 (PWM0_M0) at 100% duty, i.e. steady on, to answer “does it light”." },
        typicalFail: { zh: "BLK 接 5V → 背光过流，亮度衰减甚至烧背光。", en: "BLK on 5V over-drives the backlight." },
        hints: [
          { zh: "pin7 是 PWM 脚，驱动里给 100% 就是常亮；不占 3V3 那一路。", en: "pin7 is a PWM pin: 100% duty = steady on, and it leaves the 3V3 pin free." },
          { zh: "L4 会用它调亮度（占空比 10–100%）。", en: "L4 uses it for brightness control (10–100% duty)." },
          { zh: "明确动作：屏 BLK → pin7（PWM0_M0）。", en: "Exact action: screen BLK → pin7 (PWM0_M0)." }
        ] },
      { edge: ["scr:CS", "pin:22"],
        observe: { zh: "CS = pin22（SPI1_CSN1）→ 通常 /dev/spidev1.1。", en: "CS is pin22 (SPI1_CSN1) → usually /dev/spidev1.1." },
        typicalFail: { zh: "接 pin24（CSN0 歧义）→ 节点号对不上。", en: "Into pin24 (ambiguous CSN0) → wrong device node." },
        hints: [
          { zh: "pin24 本项目不用。", en: "pin24 is unused in this project." },
          { zh: "L3 回环关已经证明过总线通。", en: "The L3 loopback level already proved the bus." },
          { zh: "明确动作：屏 CS → pin22。", en: "Exact action: screen CS → pin22." }
        ] },
      { edge: ["scr:DC", "pin:15"],
        observe: { zh: "DC = pin15（GPIO4_A0）。不接 = 命令被当数据 → 花屏/不亮。", en: "DC is pin15 (GPIO4_A0); without it, commands are read as data." },
        typicalFail: { zh: "误用 pin11（拨动开关的脚）→ 门控随机触发。", en: "Using pin11 (the switch pin) makes gating fire randomly." },
        hints: [
          { zh: "pin11 已分给开关。", en: "pin11 belongs to the switch." },
          { zh: "DC/RS 是同一种信号。", en: "DC/RS are the same signal." },
          { zh: "明确动作：屏 DC → pin15。", en: "Exact action: screen DC → pin15." }
        ] },
      { edge: ["scr:RST", "pin:13"],
        observe: { zh: "RST = pin13（GPIO4_B4）。不建议悬空。", en: "RST is pin13 (GPIO4_B4); do not float it." },
        typicalFail: { zh: "悬空或接 5V → 初始化时序随机失败。", en: "Floating or on 5V → random init failures." },
        hints: [
          { zh: "低电平复位，3.3V 直驱即可。", en: "Active-low reset; 3.3V drive is fine." },
          { zh: "复位脉冲要够长（驱动里通常 ≥10ms）。", en: "The reset pulse must be long enough (drivers usually ≥10ms)." },
          { zh: "明确动作：屏 RST → pin13。", en: "Exact action: screen RST → pin13." }
        ] },
      { edge: ["scr:MOSI", "pin:19"],
        observe: { zh: "SPI1_MOSI = pin19。", en: "SPI1_MOSI is pin19." },
        typicalFail: { zh: "SDA/SCL 与 MOSI/CLK 叫法混用 → 插进 I2C 脚。", en: "SDA/SCL vs MOSI/CLK naming mix-ups land on I2C pins." },
        hints: [
          { zh: "认丝印不认线色。", en: "Trust the silkscreen, not the wire colour." },
          { zh: "pin19 在单数排。", en: "pin19 is on the odd row." },
          { zh: "明确动作：屏 MOSI → pin19。", en: "Exact action: screen MOSI → pin19." }
        ] },
      { edge: ["scr:CLK", "pin:23"],
        observe: { zh: "SPI1_CLK = pin23。", en: "SPI1_CLK is pin23." },
        typicalFail: { zh: "接成 pin21（MISO）→ 屏收不到时钟。", en: "Into pin21 (MISO) — no clock reaches the screen." },
        hints: [
          { zh: "SPI1 四兄弟：CLK=23、MOSI=19、CS=22、MISO=21（本关悬空）。", en: "SPI1: CLK=23, MOSI=19, CS=22, MISO=21 (unused here)." },
          { zh: "别与 I2C5（pin3/5）混。", en: "Do not mix with I2C5 (pin3/5)." },
          { zh: "明确动作：屏 CLK → pin23。", en: "Exact action: screen CLK → pin23." }
        ] },
      { edge: ["scr:VCC", "pin:1"],
        observe: { zh: "屏 VCC 落到 3V3（pin1 或 pin17）。断电操作。", en: "Screen VCC lands on 3V3 (pin1 or pin17). Powered off." },
        typicalFail: { zh: "接到 pin2/4（5V）：除非模块是 5V 版，否则烧屏。", en: "Into pin2/4 (5V): burns the screen unless it is a 5V-rated module." },
        hints: [
          { zh: "1.3\"/1.54\" 背光 20–60mA@3.3V，pin1/17 直取没问题。", en: "A 1.3\"/1.54\" backlight draws 20–60mA at 3.3V — pin1/17 is fine." },
          { zh: "先认丝印：模块的 VCC 与 GND 是相邻还是对角，各模块不同。", en: "Read the silkscreen first: VCC/GND order differs between modules." },
          { zh: "明确动作：屏 VCC → pin1 或 pin17（3V3，琥珀色针）。", en: "Exact action: screen VCC → pin1 or pin17 (3V3, amber pillar)." }
        ] },
      { edge: ["scr:GND", "pin:6"],
        observe: { zh: "屏 GND 落 pin6（pin9 留给开关 B；「一孔一线」是排针的硬约束）。", en: "Screen GND goes to pin6 (pin9 is kept for switch B — one wire per header pin is a hard constraint)." },
        typicalFail: { zh: "VCC/GND 反接 = 烧屏第一大原因。", en: "Reversed VCC/GND is the number-one screen killer." },
        hints: [
          { zh: "GND 在偶数排。", en: "GND lives on the even row." },
          { zh: "屏 VCC 与 GND 之间不应短路。", en: "VCC–GND on the module must not be shorted." },
          { zh: "明确动作：屏 GND → pin6。", en: "Exact action: screen GND → pin6." }
        ] },
      { plug: { id: "tfcard", label: { zh: "把镜像写进 TF 卡，再插进板子", en: "Write the image to the TF card, then insert it" } },
        observe: { zh: "镜像写入完成、卡插好——板子从此可以从这张卡启动，坏了大不了重写。", en: "Image written and card inserted — the board can now boot from it; bricking only costs a rewrite." },
        typicalFail: { zh: "忘了先写镜像就上电 → 卡在 U-Boot / 找不到系统（屏幕上也不会出现 IP）。", en: "Powering up without an image → it stalls at U-Boot / no OS found (and no IP appears on the screen)." },
        hints: [
          { zh: "到官方下载页取 .img.gz，解压后写卡（win32diskimager/balenaEtcher）。", en: "Grab the official .img.gz, extract, then flash (win32diskimager / balenaEtcher)." },
          { zh: "写完先拔卡再插回板子（板子不是读卡器）。", en: "Eject from the PC, then insert into the board (the board is not a card reader)." },
          { zh: "明确动作：点 3D 里的 TF 卡打开烧录终端，输入生成的命令；烧录完再点卡槽插卡。", en: "Exact action: click the TF card to open the flashing terminal, run the generated command, then click the slot to insert it." }
        ] },
      { plug: { id: "power", label: { zh: "接入 USB-C 供电", en: "Connect USB-C power" } },
        observe: { zh: "接入 USB-C：板上 LED 亮；随后把屏幕翻到正面点亮。", en: "Connect USB-C: the LEDs light; then flip the screen to the front to boot it." },
        typicalFail: { zh: "用 PD 诱骗出 9/12V 的头 → 板载 OVP 阈值 6V，可能烧输入级。", en: "A PD charger negotiating 9/12V can kill the input stage (on-board OVP trips at 6V)." },
        hints: [
          { zh: "确认电源是 5V，不要用会协商 9/12V 的 PD 头。", en: "Use a 5V source; do not use a PD charger that negotiates 9/12V." },
          { zh: "第一次上电只看屏上的变化（管理位应出现 IP），先别动别的。", en: "On first power-up, watch the screen (ADMIN should show the IP) and nothing else." },
          { zh: "明确动作：点 3D 里的 USB-C 电源线道具。", en: "Exact action: click the USB-C power prop." }
        ] },
      { plug: { id: "lan", label: { zh: "插上网线", en: "Plug in the Ethernet cable" } },
        observe: { zh: "网口 link/act 灯亮；从另一台机器 ping 通设备，管理面 https://<ip>:8443 可达。", en: "Link/act LEDs light; the device answers ping and https://<ip>:8443 is reachable." },
        typicalFail: { zh: "插了只有 4 芯的劣质线 → 只能跑百兆，甚至 link 不上。", en: "A 4-wire junk cable caps at 100M or never links." },
        hints: [
          { zh: "插到板子的以太网口（网口在板子一侧）。", en: "Plug into the board's Ethernet jack." },
          { zh: "路由器端口灯不亮就换口/换线。", en: "If the router port LED stays dark, change port/cable." },
          { zh: "明确动作：点 3D 里的网线道具。", en: "Exact action: click the Ethernet cable prop." }
        ] }
    ],
    mistakes: [
      { pair: ["pin:2", "sw:B"], hint: { zh: "开关接地端 B 接到了 5V —— 闭合瞬间把 5V 灌进 GPIO，烧针脚。立刻改到 GND", en: "The switch ground lead B is on 5V — closing it feeds 5V into the GPIO and burns the pin. Move it to GND." } },
      { pair: ["pin:2", "sw:C"], hint: { zh: "开关公共端 C 接到了 5V —— 拨到任一档都会把 5V 灌进 GPIO，烧针脚。立刻改到 pin11", en: "The switch common C is on 5V — either position feeds 5V into the GPIO and burns it. Move it to pin11." } },
      { pair: ["pin:2", "pin:9"], hint: { zh: "5V 直接对地 = 短路。立刻断电", en: "5V straight to GND = short. Power off." } },
      { pair: ["pin:11", "pin:9"], hint: { zh: "这等于把 GPIO 直接钉死到地：门控会恒为 LOCKED，而且拔线也不会恢复", en: "That pins the GPIO to GND permanently: gating would be stuck LOCKED and unplugging would not recover it." } },
      { pair: ["pin:17", "pin:9"], hint: { zh: "3V3 对地短路。立刻断电", en: "3V3 to GND short. Power off." } }
    ]
  },
  field: [
    { id: "dt-pullup-conf", kind: "ack",
      text: { zh: "上拉来源已确认：裸开关 → DT 里 GPIO4_A5 配 bias-pull-up；带 10k 模块 → 模块自带", en: "Pull-up source confirmed: bare switch → bias-pull-up in DT; 10k module → built in" } },
    { id: "debounce", kind: "ack",
      text: { zh: "软件去抖契约已确认：连续 3×50ms 电平一致才认可（switchd 契约）", en: "Software debounce contract confirmed: 3×50ms consistent readings required (switchd contract)" } }
  ],
  verifyTitle: { zh: "上电与门控验收", en: "Power-up & gating acceptance" },
  verify: [
    { id: "get-lock", kind: "number",
      prompt: { zh: "上电后（SSH：插网线后登进设备）gpioget gpiochip4 5：开关拨到 LOCK 位（闭合）读数", en: "After power-up, gpioget gpiochip4 5 with the switch at LOCK (closed)" },
      expect: [0, 0],
      failHint: { zh: "闭合读 0。读 1 = 信号线没接到 pin11/pin9", en: "Closed reads 0. A 1 = signal wire not on pin11/pin9." } },
    { id: "get-admin", kind: "number",
      prompt: { zh: "同一命令（SSH）：开关拨到 ADMIN 位（断开）读数", en: "Same command over SSH: switch at ADMIN (open)" },
      expect: [1, 1],
      failHint: { zh: "断开读 1。若读到 0 或乱跳：内部上拉没配（DT bias-pull-up）或模块下拉型", en: "Open reads 1. A 0 or jitter means the internal pull-up is not configured (DT bias-pull-up) or the module is pull-down type." },
      hints: [
        { zh: "确认 DT 里给该脚开了 bias-pull-up 并已重启", en: "Confirm bias-pull-up is enabled for that pin in DT and the board rebooted." },
        { zh: "用 `gpioinfo` 看你那一路的 line 名称/bias 状态", en: "Use `gpioinfo` to inspect the line's bias state." },
        { zh: "明确判据：断开必须读 1", en: "Criterion: open must read 1." }
      ] },
    { id: "init-screen", kind: "select",
      prompt: { zh: "开关拨到管理位（ADMIN，断开）但网线还没插：屏幕应显示什么？", en: "Switch at ADMIN (open) with no Ethernet cable yet: what should the screen show?" },
      options: [
        { zh: "未检测到以太网（插上网线后显示 IP）", en: "\"No Ethernet detected\" (and the IP once the cable is in)" },
        { zh: "IP 地址，照常显示", en: "The IP, as usual" },
        { zh: "全黑，必须插串口才知道状态", en: "All black — you need the serial adapter to learn the state" }
      ],
      expect: 0,
      failHint: { zh: "屏幕内容只取决于「开关状态 + 网线」：管理位必显示 IP（无网线则报未检测到以太网）；锁定位不显示 IP。不需要判断是否已初始化——串口只是屏没起来/网络没通时的后备。",
        en: "The screen depends only on the switch state + the cable: ADMIN always shows the IP (or \"no Ethernet detected\" without a cable); LOCKED shows no IP. No need to check whether it has been initialized — the serial is only the fallback when the display or the network is down." } },
    { id: "file-lock", kind: "select",
      prompt: { zh: "文件后端验证（不碰 GPIO）：curl -XPOST -d '{\「locked\":true}' http://127.0.0.1:8799/v1/state 后，https://<ip>:8443 的表现", en: "File-backend check (no GPIO): after curl -XPOST -d '{\"locked\":true}' http://127.0.0.1:8799/v1/state, what does https://<ip>:8443 do?" },
      options: [
        { zh: "变拒绝/锁定页，/healthz 仍 200，3425 网关不受影响", en: "Refused/locked page; /healthz still 200; gateway on 3425 unaffected" },
        { zh: "完全无变化", en: "No change at all" },
        { zh: "面板和 3425 一起挂了", en: "Panel and 3425 both die" }
      ], expect: 0,
      failHint: { zh: "LOCKED 只挡管理面（8443），放行 3425 与 443 出网", en: "LOCKED blocks only the panel (8443); 3425 and 443 stay open." } },
    { id: "gpio-lock", kind: "select",
      prompt: { zh: "切 GPIO 后端后拨到 LOCK 位：管理面表现", en: "With the GPIO backend: switch at LOCK — the panel" },
      options: [
        { zh: "管理面不可达（连接被拒绝）", en: "Becomes unreachable (connection refused)" },
        { zh: "管理面仍能正常登录", en: "Still logs in normally" },
        { zh: "设备整体断网", en: "Whole device is offline" }
      ], expect: 0,
      failHint: { zh: "GPIO 链路没生效：查 gpioget 读数与 switchd 后端切换", en: "GPIO chain not effective: check gpioget and switchd backend." } },
    { id: "gpio-admin", kind: "select",
      prompt: { zh: "拨回 ADMIN 位：管理面表现", en: "Switch back to ADMIN: the panel" },
      options: [
        { zh: "恢复正常登录", en: "Logs in normally again" },
        { zh: "仍然不可达", en: "Still unreachable" },
        { zh: "要重启才好", en: "Needs a reboot" }
      ], expect: 0,
      failHint: { zh: "应立即恢复", en: "Should recover immediately." } }
  ],
  complete: {
    reveal: {
      zh: "两根线就够：闭合 = LOCKED（管理面默认拒绝），断开 = 内部上拉拉高 = UNLOCKED；3425 业务面永不封。也可改用外置 10k + 100nF 的硬件变体（教程不设关）",
      en: "Two wires are enough: closed = LOCKED (panel denies), open = internal pull-up = UNLOCKED; the 3425 gateway never closes. An external 10k + 100nF variant is optional (not a game level)."
    },
    badge: {
      title: { zh: "第一关 · 通关（免焊 10 线）", en: "Level 1 · Cleared (10 solderless wires)" },
      sub: { zh: "拨动开关门控接入 —— 10 根免焊杜邦线（开关 2 + 屏 8）· 内部上拉 + 软件去抖", en: "Toggle-switch gating — 10 solderless wires (2 switch + 8 screen), internal pull-up + software debounce" },
      tag: { zh: "解锁方式：完成检查与接线", en: "Unlocked by checks and wiring" }
    }
  }
});
