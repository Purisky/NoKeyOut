// 关卡数据：第四关「ST7789 三步点亮」（双语）
window.registerLevel({
  id: "level-4-screen",
  order: 5,
  mapKey: "lv4",
  stages: ["verify", "complete"],
  title: { zh: "第四关 · ST7789 三步点亮", en: "L4 · ST7789 Three-Step Bring-Up" },
  brief: {
    disciplines: [
      { zh: "不要一次全接：Step1 只电源与背光 → Step2 SPI+DC+RST → Step3 PWM 背光", en: "Never wire it all at once: Step1 power+backlight → Step2 SPI+DC+RST → Step3 PWM backlight" },
      { zh: "每步都要有可解释的现象再往下", en: "Every step must have an explainable observation before continuing" },
      { zh: "认丝印，不认线色（SDA/SCL 与 MOSI/CLK 叫法混用是常态）", en: "Trust silkscreen, not wire color (SDA/SCL vs MOSI/CLK naming is routinely mixed up)" }
    ],
    irreversibles: [
      { zh: "VCC 误接 5V 且模块逻辑电平 5V → 5V 灌进 3.3V 的 SoC 脚，不可逆", en: "VCC at 5V with 5V logic levels back-feeds 5V into 3.3V SoC pins — irreversible" },
      { zh: "VCC/GND 反接 = 烧屏第一大原因（模块丝印逐脚核对）", en: "Reversed VCC/GND is the #1 screen-killer (verify pinout against silkscreen)" },
      { zh: "pin24 歧义不用；pin11 已分给拨动开关，别抢", en: "pin24 ambiguous — unused; pin11 belongs to the toggle switch, do not steal it" }
    ]
  },
  verifyTitle: { zh: "上屏验收", en: "Screen acceptance" },
  verify: [
    { id: "vcc33", kind: "ack",
      text: { zh: "VCC 是 3V3（不是 pin2/4）——除非已确认模块 5V 版且逻辑电平仍 3.3V", en: "VCC is 3V3 (not pin2/4) — unless the module is 5V-rated with 3.3V logic" } },
    { id: "noshort", kind: "select",
      prompt: { zh: "断电电阻档：屏 VCC–GND", en: "Powered off, resistance range: screen VCC–GND" },
      options: [
        { zh: "不通（OL）", en: "Open (OL)" },
        { zh: "通（有阻值或 0Ω）", en: "Closed (any resistance or 0Ω)" }
      ], expect: 0,
      failHint: { zh: "短路就别上电——多半是 VCC/GND 反接", en: "A short means do not power up — usually reversed VCC/GND." } },
    { id: "silkscreen", kind: "ack",
      text: { zh: "模块丝印逐脚核对：4/7/8 pin 顺序各模块不同（VCC/GND 反接是烧屏第一大原因）", en: "Verified every pin against silkscreen: 4/7/8-pin orders differ per module (reversed VCC/GND is the #1 killer)" } },
    { id: "pin24", kind: "ack",
      text: { zh: "pin24 不用；CS 用 pin22；pin11 未被动过", en: "pin24 unused; CS on pin22; pin11 untouched" } },
    { id: "nosolder", kind: "ack",
      text: { zh: "免焊确认：屏模块是「已焊排针版」，8 根杜邦线直插到位、无松动（本项目全程不动烙铁）", en: "Solderless check: the module has its header pre-soldered; all eight duponts are fully seated (no iron used anywhere in this project)" } },
    { id: "step1-obs", kind: "select",
      prompt: { zh: "Step1 上电后现象", en: "Observation after Step1 power-up" },
      options: [
        { zh: "整屏发白/发亮（无内容）", en: "Full white/glow (no content)" },
        { zh: "全黑", en: "All black" },
        { zh: "花屏", en: "Garbage/static" }
      ], expect: 0,
      failHint: { zh: "全黑 → 停，查背光极性与供电（回 Step1），别继续往下接", en: "All black → stop: check backlight polarity and supply (back to Step1); do not wire further." } },
    { id: "step2-obs", kind: "select",
      prompt: { zh: "Step2 加载驱动后现象", en: "Observation after Step2 driver load" },
      options: [
        { zh: "红/绿/蓝/白纯色块", en: "Solid red/green/blue/white blocks" },
        { zh: "全黑", en: "All black" },
        { zh: "花屏/随机噪点", en: "Static/random noise" }
      ], expect: 0,
      failHint: { zh: "花屏 = DC/CS 接反、速率过高（先降 10MHz）、线过长", en: "Static = reversed DC/CS, rate too high (try 10MHz first), long wires." } },
    { id: "color-order", kind: "select",
      prompt: { zh: "若红蓝互换，根因是", en: "If red and blue are swapped, the cause is" },
      options: [
        { zh: "RGB/BGR 位配错", en: "Wrong RGB/BGR bit" },
        { zh: "DC 接反", en: "Reversed DC" },
        { zh: "背光太暗", en: "Dim backlight" }
      ], expect: 0,
      failHint: { zh: "改驱动 rgb/inv 参数，不用动线", en: "Flip the driver rgb/inv parameter — no rewiring." } },
    { id: "selftest", kind: "ack",
      text: { zh: "SCREEN_BACKEND=st7789 SCREEN_SELFTEST=1 重启 → 四色块通过；去掉 SELFTEST 进业务轮播", en: "SCREEN_BACKEND=st7789 SCREEN_SELFTEST=1 reboot → four color blocks passed; removing SELFTEST enters the business carousel" } },
    { id: "tidy", kind: "ack",
      text: { zh: "收尾：线束扎带/热缩固定；屏不长期受力于杜邦线；装壳后再拨一次开关确认门控仍成立", en: "Cleanup: bundle/heat-shrink wires; no long-term strain on dupont; after casing, toggle the switch once more to confirm gating still works" } }
  ],
  complete: {
    reveal: {
      zh: "灯亮了：NoKeyOut 有了自己的脸。四色块 → 业务轮播（tok/s 统计、LOCKED 锁定页），同一份 snapshot 数据，Go 进程内渲染",
      en: "It lives: NoKeyOut has a face now. Color blocks → business carousel (tok/s stats, LOCKED lock page) — same snapshot data, rendered in-process by Go."
    },
    badge: {
      title: { zh: "第四关 · 通关", en: "Level 4 · Cleared" },
      sub: { zh: "ST7789 三步点亮 —— 白屏→色块→PWM", en: "ST7789 bring-up — white→color blocks→PWM" },
      tag: { zh: "解锁方式：亲眼看见，不是“应该亮了”", en: "Unlocked by seeing it, not by “it should be on”" }
    }
  }
});
