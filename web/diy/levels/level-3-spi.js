// 关卡数据：第三关「SPI1 回环」（双语）
window.registerLevel({
  id: "level-3-spi",
  order: 4,
  mapKey: "lv3",
  stages: ["procurement", "wiring", "verify", "complete"],
  title: { zh: "第三关 · SPI1 回环（不接屏先证总线）", en: "L3 · SPI1 Loopback" },
  brief: {
    disciplines: [
      { zh: "改线必断电（拔 USB-C）", en: "Rewire only when powered off (unplug USB-C)" },
      { zh: "先量电、后上电", en: "Measure before powering on" }
    ],
    irreversibles: [
      { zh: "回环线只短接 pin19↔pin21；搭到 5V/GND = 烧控制器", en: "The loopback wire only bridges pin19↔pin21; touching 5V/GND kills the controller" },
      { zh: "别用 pin24（CSN0 有来源歧义）", en: "Do not use pin24 (CPN0 has conflicting sources)" }
    ]
  },
  procurement: [
    { id: "wire", qty: 1,
      spec: { zh: "母-母杜邦线 20cm ×1（回环专用）", en: "One female-female dupont 20cm (loopback only)" },
      why: { zh: "短接 MOSI↔MISO，让发送字节原样回来。", en: "Bridges MOSI↔MISO so sent bytes echo back." },
      channel: { zh: "通用电子元件渠道", en: "General electronics supplier" },
      dont: { zh: "别用会松脱的线——回环松了你会去怀疑 DT。", en: "Do not use a loose wire — a bad loopback will send you chasing DT ghosts." } }
  ],
  wiring: {
    intro: { zh: "本关只有一根线：把 SPI1 的 MOSI 与 MISO 短接。屏还没接，就能证明总线通。", en: "One wire this level: short SPI1 MOSI to MISO. No screen attached, yet the bus proves itself." },
    nodeLabel: { zh: "SPI1 回环", en: "SPI1 loopback" },
    pins: [
      { n: 1, label: "3V3", cls: "power" }, { n: 2, label: "5V ⚠", cls: "danger" },
      { n: 6, label: "GND", cls: "gnd" }, { n: 9, label: "GND", cls: "gnd" },
      { n: 11, label: "GPIO4_A5 ★", cls: "target" },
      { n: 14, label: "GND", cls: "gnd" }, { n: 15, label: "屏DC 别碰", cls: "keep" },
      { n: 17, label: "3V3", cls: "power" }, { n: 19, label: "SPI1 MOSI", cls: "target" },
      { n: 20, label: "GND", cls: "gnd" }, { n: 21, label: "SPI1 MISO", cls: "target" },
      { n: 22, label: "SPI1 CSN1", cls: "target" }, { n: 23, label: "SPI1 CLK", cls: "target" },
      { n: 24, label: "歧义·不用", cls: "warn" }, { n: 25, label: "GND", cls: "gnd" }
    ],
    components: [],
    steps: [
      { edge: ["pin:19", "pin:21"],
        observe: { zh: "一根线短接 MOSI(pin19) ↔ MISO(pin21)。断电操作，插到底。", en: "One wire bridging MOSI (pin19) ↔ MISO (pin21). Powered off, fully seated." },
        typicalFail: { zh: "插到隔壁孔（21↔22）——发送字节就对不上了。", en: "Plugged into the adjacent hole (21↔22) — the echo won't match." },
        hints: [
          { zh: "MOSI 在单数排 pin19，MISO 在单数排 pin21，中间隔 pin20(GND)。", en: "MOSI is pin19 (odd row), MISO is pin21 (odd row), with pin20 (GND) between them." },
          { zh: "确认 pin19↔pin21 短接，pin19↔pin20 不短接。", en: "Confirm pin19↔pin21 are bridged and pin19↔pin20 are not." },
          { zh: "明确动作：pin19（MOSI）→ pin21（MISO）。", en: "Exact action: pin19 (MOSI) → pin21 (MISO)." }
        ] }
    ],
    mistakes: [
      { pair: ["pin:2", "pin:19"], hint: { zh: "5V 串进 SPI 引脚 = 烧控制器。立刻断电。", en: "5V into an SPI pin burns the controller. Power off now." } },
      { pair: ["GND", "pin:19"], hint: { zh: "MOSI 对地短接 = 上电后总线异常。", en: "MOSI shorted to GND — bus faults on power-up." } }
    ]
  },
  verifyTitle: { zh: "SPI1 回路验收", en: "SPI1 loopback acceptance" },
  verify: [
    { id: "dt", kind: "ack",
      text: { zh: "设备树已打开 &spi1 + 对应 pinctrl（M0 复用）并重启", en: "Device tree has &spi1 + pinctrl (M0 mux) enabled and the board rebooted" } },
    { id: "spidev", kind: "select",
      prompt: { zh: "ls /dev/spidev* 的结果", en: "Output of ls /dev/spidev*" },
      options: [
        { zh: "出现 spidev1.x", en: "spidev1.x appears" },
        { zh: "什么都没有", en: "Nothing" },
        { zh: "只有 spidev0.x", en: "Only spidev0.x" }
      ], expect: 0,
      failHint: { zh: "只有一个可怀疑处：DT/复用（不是接线+屏三处一起猜）。", en: "Suspect exactly one place: the DT/mux — not wiring and screen together." } },
    { id: "cs", kind: "select",
      prompt: { zh: "CS 选哪个脚、对应哪个设备节点", en: "Which pin for CS, and which device node" },
      options: [
        { zh: "pin22（SPI1_CSN1）→ /dev/spidev1.1", en: "pin22 (SPI1_CSN1) → /dev/spidev1.1" },
        { zh: "pin24（CSN0）→ /dev/spidev1.0", en: "pin24 (CPN0) → /dev/spidev1.0" },
        { zh: "随便哪个都行", en: "Any of them" }
      ], expect: 0,
      failHint: { zh: "pin24/CSN0 有来源歧义，本项目先不用。", en: "pin24/CSN0 has conflicting sources — unused in this project." } },
    { id: "echo", kind: "select",
      prompt: { zh: "spidev_test -D /dev/spidev1.1 -v 的输出", en: "Output of spidev_test -D /dev/spidev1.1 -v" },
      options: [
        { zh: "发送/接收字节一致", en: "Sent and received bytes match" },
        { zh: "不一致", en: "Mismatch" },
        { zh: "命令报错", en: "Command errors" }
      ], expect: 0,
      failHint: { zh: "不一致 = 回环线没插实/插错孔，或复用没配对。", en: "Mismatch = loopback not seated / wrong hole, or mux not applied." } },
    { id: "unplug", kind: "ack",
      text: { zh: "已拔回环线（上屏前不留回环）", en: "Loopback wire removed (never leave it in before the screen goes on)" } }
  ],
  complete: {
    reveal: {
      zh: "SPI1 控制器 + 引脚复用 + spidev 全通——而且是在屏没接的情况下证明的。失败时你只需怀疑一处，而不是三处一起猜。",
      en: "SPI1 controller + pin mux + spidev all verified — without the screen attached. On failure you suspect one place, not three."
    },
    badge: {
      title: { zh: "第三关 · 通关", en: "Level 3 · Cleared" },
      sub: { zh: "SPI1 回环 —— spidev_test 收发一致", en: "SPI1 loopback — spidev_test echo matched" },
      tag: { zh: "解锁方式：一个字节的回声", en: "Unlocked by the echo of a byte" }
    }
  }
});
