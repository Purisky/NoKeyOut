// 关卡数据：第二关「门控全链路验收」（双语）
window.registerLevel({
  id: "level-2-gating",
  order: 3,
  mapKey: "lv2",
  stages: ["verify", "complete"],
  title: { zh: "第二关 · 门控全链路验收", en: "L2 · Full Gating Acceptance" },
  brief: {
    disciplines: [
      { zh: "本关多数命令在**另一台机器**上执行（证明“从外面真的进不来”）", en: "Most checks run from a SECOND machine — proving outsiders really cannot get in" },
      { zh: "LOCK 位下排障只走物理/串口，不走 SSH", en: "While LOCKED, troubleshoot physically or via serial — never via SSH" },
      { zh: "全程串口日志开着（PuTTY session logging）", en: "Keep serial logging on throughout (PuTTY session logging)" }
    ],
    irreversibles: [
      { zh: "本关无不可逆操作；但如果改 nftables 规则表，先备份（LOCKED = 入站默认拒绝由它实施）", en: "No irreversible actions here — but back up before touching nftables rules (they implement the inbound-default-deny)" }
    ]
  },
  verifyTitle: { zh: "门控全链路验收", en: "Full gating acceptance" },
  verify: [
    { id: "ssh-lock", kind: "select",
      prompt: { zh: "LOCK 位时，从另一台机器 ssh <设备IP>", en: "While LOCKED, from another machine: ssh <deviceIP>" },
      options: [
        { zh: "连不上（拒绝/超时）", en: "Unreachable (refused/timeout)" },
        { zh: "能登进去", en: "Logs in fine" },
        { zh: "时通时断", en: "Intermittent" }
      ], expect: 0,
      failHint: { zh: "LOCKED 应默认拒绝所有入站（nftables），只放行 3425 与 443 出网口。", en: "LOCKED must deny all inbound by default; only 3425 and the 443 egress stay open." } },
    { id: "nc-8443", kind: "select",
      prompt: { zh: "同一状态下 nc -z <设备IP> 8443", en: "Same state: nc -z <deviceIP> 8443" },
      options: [
        { zh: "失败（连不上）", en: "Fails (unreachable)" },
        { zh: "成功", en: "Succeeds" }
      ], expect: 0,
      failHint: { zh: "8443 是管理面，LOCKED 必须挡。", en: "8443 is the panel — LOCKED must block it." } },
    { id: "nc-8080", kind: "select",
      prompt: { zh: "同一状态下 nc -z <设备IP> 8080", en: "Same state: nc -z <deviceIP> 8080" },
      options: [
        { zh: "失败（连不上）", en: "Fails (unreachable)" },
        { zh: "成功", en: "Succeeds" }
      ], expect: 0,
      failHint: { zh: "8080 同样在封禁之列——管理面端口一个都不留。", en: "8080 is on the deny list too — no panel port survives LOCKED." } },
    { id: "gw-3425", kind: "select",
      prompt: { zh: "LOCK 位时，另一台机器 curl http://<设备IP>:3425/v1/models", en: "While LOCKED, from another machine: curl http://<deviceIP>:3425/v1/models" },
      options: [
        { zh: "正常（200）", en: "Works (200)" },
        { zh: "连不上", en: "Unreachable" }
      ], expect: 0,
      failHint: { zh: "业务面永不封——两个开关位置下 3425 都必须正常。", en: "The gateway never closes: 3425 must work in both switch positions." } },
    { id: "admin-restore", kind: "select",
      prompt: { zh: "拨回 ADMIN 位后，从另一台机器 ssh / nc 8443", en: "Switch back to ADMIN, then ssh / nc 8443 from the other machine" },
      options: [
        { zh: "全部恢复", en: "All restored" },
        { zh: "仍然不可达", en: "Still unreachable" }
      ], expect: 0,
      failHint: { zh: "ADMIN 位应立即恢复管理面。", en: "ADMIN must restore the panel immediately." } },
    { id: "fail-open", kind: "select",
      prompt: { zh: "失效方向验证：拔掉开关信号线", en: "Fail direction: unplug the switch signal wire" },
      options: [
        { zh: "面板回到 UNLOCKED（可用），不是锁死", en: "Panel returns to UNLOCKED (usable), not bricked" },
        { zh: "仍然 LOCKED 进不去", en: "Stays LOCKED and unusable" }
      ], expect: 0,
      failHint: { zh: "本项目失效方向 = 开放（救援走物理）。若你要 fail-closed，须显式设 PANEL_DEFAULT_LOCKED=1 并取反读法逻辑。", en: "This project fails OPEN (rescue is physical). For fail-closed you must explicitly set PANEL_DEFAULT_LOCKED=1 and invert the read logic." } },
    { id: "rescue-drill", kind: "ack",
      text: { zh: "救援演练完成：LOCK 位下 ① 拨到 ADMIN 即恢复 ② 拔掉信号线也恢复 ③ 串口能登 shell", en: "Rescue drill done: while LOCKED — ① toggling ADMIN restores ② unplugging the wire restores ③ serial shell works" } }
  ],
  complete: {
    reveal: {
      zh: "门控链路全通：LOCKED = 入站默认拒绝（只放行 3425/443），断开 = 开放，救援只走物理/串口。这套行为由你装的 5 根线 + switchd 8799 + nftables 共同兑现。",
      en: "Gating chain verified: LOCKED = inbound default-deny (only 3425/443), open = fail-open, rescue is physical/serial only. Delivered by your 5 wires + switchd 8799 + nftables together."
    },
    badge: {
      title: { zh: "第二关 · 通关", en: "Level 2 · Cleared" },
      sub: { zh: "门控全链路验收 —— 入站全封 / 失效方向 / 救援演练", en: "Full gating acceptance — lockdown / fail direction / rescue drill" },
      tag: { zh: "解锁方式：从另一台机器验证，不是“我信”", en: "Unlocked by testing from a second machine, not by faith" }
    }
  }
});
