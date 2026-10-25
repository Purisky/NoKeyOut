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
      { zh: "本关无不可逆操作；但如果改 ssh.service 的 drop-in 或 nokeyout-switchd，先备份（LOCKED 的 SSH 门控 = ExecStartPre nokeyout-gate-check；面板门控 = 关闭监听）", en: "No irreversible actions here — but back up before touching the ssh.service drop-in or nokeyout-switchd (LOCKED SSH gate = ExecStartPre nokeyout-gate-check; panel gate = listener closed)" }
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
      failHint: { zh: "LOCKED 时 ssh.service 被 nokeyout-gate-check 拒绝启动，且 switchd 会停掉已建立的会话；22 必须连不上。", en: "Under LOCKED, nokeyout-gate-check refuses to start ssh.service and switchd stops live sessions; 22 must be unreachable." } },
    { id: "nc-8443", kind: "select",
      prompt: { zh: "同一状态下 nc -z <设备IP> 8443", en: "Same state: nc -z <deviceIP> 8443" },
      options: [
        { zh: "失败（连不上）", en: "Fails (unreachable)" },
        { zh: "成功", en: "Succeeds" }
      ], expect: 0,
      failHint: { zh: "8443 是管理面，LOCKED 时监听关闭，必须连不上。", en: "8443 is the panel — under LOCKED its listener is closed, so it must be unreachable." } },
    { id: "nc-8080", kind: "select",
      prompt: { zh: "同一状态下 nc -z <设备IP> 8080", en: "Same state: nc -z <deviceIP> 8080" },
      options: [
        { zh: "失败（连不上）", en: "Fails (unreachable)" },
        { zh: "成功", en: "Succeeds" }
      ], expect: 0,
      failHint: { zh: "设备只监听 22、8443、3425；8080 没有服务，应当连不上。", en: "The device listens only on 22, 8443 and 3425; nothing serves 8080, so it must be unreachable." } },
    { id: "gw-3425", kind: "select",
      prompt: { zh: "LOCK 位时，另一台机器 curl http://<设备IP>:3425/v1/models", en: "While LOCKED, from another machine: curl http://<deviceIP>:3425/v1/models" },
      options: [
        { zh: "正常（200）", en: "Works (200)" },
        { zh: "连不上", en: "Unreachable" }
      ], expect: 0,
      failHint: { zh: "网关 3425 在两个开关位置下都可用（只拒绝无有效网关密钥的请求）。", en: "The gateway 3425 stays up in both switch positions (it refuses only requests without a valid gateway key)." } },
    { id: "admin-restore", kind: "select",
      prompt: { zh: "拨回 ADMIN 位后，从另一台机器 ssh / nc 8443", en: "Switch back to ADMIN, then ssh / nc 8443 from the other machine" },
      options: [
        { zh: "全部恢复", en: "All restored" },
        { zh: "仍然不可达", en: "Still unreachable" }
      ], expect: 0,
      failHint: { zh: "ADMIN 位应立即恢复管理面。", en: "ADMIN must restore the panel immediately." } },
    { id: "fail-closed", kind: "select",
      prompt: { zh: "失效方向验证：拔掉开关信号线", en: "Fail direction: unplug the switch signal wire" },
      options: [
        { zh: "仍然 LOCKED（拔线即锁定，fail-closed）", en: "Stays LOCKED (a pulled wire locks; fail-closed)" },
        { zh: "回到 ADMIN（fail-open，管理面重新开放）", en: "Returns to ADMIN (fail-open, the panel reopens)" }
      ], expect: 0,
      failHint: { zh: "断路 = LOCKED：拔线/断线自动锁死，恢复只能把线接回（或走串口）；SSH 侧同样失效关闭。", en: "Open circuit = LOCKED: a pulled or broken wire locks the box; recovery means reconnecting it (or serial). The SSH side fails closed too." } },
    { id: "rescue-drill", kind: "ack",
      text: { zh: "救援演练完成：LOCK 位下 ① 拨到 ADMIN（闭合）即恢复 ② 拔掉信号线会锁死，需接回或走串口 ③ 串口能登 shell", en: "Rescue drill done: while LOCKED — ① closing to ADMIN restores it ② a pulled wire locks the box, so reconnect it or use serial ③ the serial shell works" } }
  ],
  complete: {
    reveal: {
      zh: "门控链路全通：LOCKED = 22 与 8443 关闭，3425 网关始终可用（仅持有效网关密钥者）。断路即 LOCKED，拔线也锁定（fail-closed）；救援走物理开关或串口。门控由 ssh.service 的 ExecStartPre、nokeyout-switchd 与 switchd 8799 共同兑现。",
      en: "Gating chain verified: LOCKED = 22 and 8443 closed; the 3425 gateway stays up for valid gateway keys only. Open circuit = LOCKED, and a pulled wire locks too (fail-closed); rescue is the physical switch or serial. Delivered by ssh.service's ExecStartPre, nokeyout-switchd and switchd 8799 together."
    },
    badge: {
      title: { zh: "第二关 · 通关", en: "Level 2 · Cleared" },
      sub: { zh: "门控全链路验收 —— 入站全封 / 失效方向 / 救援演练", en: "Full gating acceptance — lockdown / fail direction / rescue drill" },
      tag: { zh: "解锁方式：从另一台机器验证，不是“我信”", en: "Unlocked by testing from a second machine, not by faith" }
    }
  }
});
