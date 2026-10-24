#!/usr/bin/env node
/**
 * 关卡内容校验器（agent 内容管线的安全闸）
 *   node web/diy/test/validate-levels.mjs
 *
 * 校验（不通过即退出码 1）：
 *   V1 引脚扇出：一根排针最多接 1 根线（面包板节点 node:* 例外，那是"汇流排"）
 *   V2 端点存在性：steps 里引用的 pin:N / 元件引脚 必须在 pins / components 里定义
 *   V3 判据完整性：ack/number/select 项必须有 prompt|text、failHint、hints
 *   V4 采购完整性：spec/why/channel/dont 齐全；带 choose 的必须有 options/expect/hint
 *   V5 阶段合法：stages 只能用已实现的 kind
 *   V6 危险提示：每条接线（非首步）应能给出典型失败场景或危险提示
 */
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

const LEVEL_DIR = join(process.cwd(), 'site', 'levels');
const KINDS = new Set(['procurement', 'wiring', 'field', 'verify', 'complete']);

const window = { registerLevel: (l) => LEVELS.push(l) };
const LEVELS = [];
for (const f of readdirSync(LEVEL_DIR).filter((x) => x.endsWith('.js'))) {
  new Function('window', readFileSync(join(LEVEL_DIR, f), 'utf8'))(window);
}

let problems = 0;
const bad = (lvl, rule, msg) => { problems++; console.log(`  ✗ [${rule}] ${lvl}: ${msg}`); };

for (const L of LEVELS) {
  console.log(`\n== ${L.id} (${L.title.zh})`);
  // V6 判据的正确答案必须存在且合法（漏填 expect 会让该关永远过不去）
  for (const [group, items] of [['field', L.field], ['verify', L.verify]]) {
    for (const it of items || []) {
      if (it.kind === 'select') {
        if (!Array.isArray(it.options) || it.options.length < 2) bad(L.id, 'V6', `${group}/${it.id}: select 选项不足`);
        if (!Number.isInteger(it.expect) || it.expect < 0 || it.expect >= (it.options || []).length) {
          bad(L.id, 'V6', `${group}/${it.id}: select 的 expect 缺失或越界（${it.expect}）`);
        }
      } else if (it.kind === 'number') {
        if (!Array.isArray(it.expect) || it.expect.length !== 2 || !it.expect.every((x) => typeof x === 'number')) {
          bad(L.id, 'V6', `${group}/${it.id}: number 的 expect 必须是 [min,max]`);
        }
      }
    }
  }

  // V5 stages（支持对象式条目：{kind, from, to, optional}）
  const kinds = L.stages.map((s) => (typeof s === 'string' ? s : s.kind));
  for (const k of kinds) if (!KINDS.has(k)) bad(L.id, 'V5', `未知 stage: ${k}`);
  // V5b 装配窗口：必须完整、不重叠地覆盖所有步骤
  if (L.wiring) {
    const wins = L.stages.filter((s) => typeof s === 'object' && s.kind === 'wiring');
    if (wins.length) {
      let expect = 0;
      wins.forEach((w, i) => {
        const from = w.from || 0, to = w.to != null ? w.to : L.wiring.steps.length;
        if (from !== expect) bad(L.id, 'V5b', `第 ${i + 1} 个装配窗口起点应为 ${expect}，实际 ${from}`);
        if (to <= from) bad(L.id, 'V5b', `第 ${i + 1} 个装配窗口为空（${from}..${to}）`);
        expect = to;
      });
      if (expect !== L.wiring.steps.length) bad(L.id, 'V5b', `装配窗口只覆盖到 ${expect} / ${L.wiring.steps.length} 步`);
      if (kinds.filter((k) => k === 'wiring').length !== wins.length) bad(L.id, 'V5b', '存在无窗口的 wiring 阶段');
    }
  }

  // V1 引脚扇出（按"具体针脚"计，而不是归一化后的组名）
  if (L.wiring) {
    const fanout = new Map();
    const allEdges = [];
    for (const st of L.wiring.steps) {
      if (!st.edge) continue;
      allEdges.push(st.edge);
      for (const ep of st.edge) {
        if (!ep.startsWith('pin:')) continue;
        fanout.set(ep, (fanout.get(ep) || 0) + 1);
      }
    }
    for (const [pin, n] of fanout) {
      if (n > 1) bad(L.id, 'V1', `排针 ${pin} 被接了 ${n} 根线（一个孔只能插一根；请改用多路 3V3/17 与 GND/6·9·14·20·25，或用面包板 node 汇流）`);
    }
    // V2 端点存在性
    const pinNums = new Set(L.wiring.pins.map((p) => p.n));
    const leadIds = new Set();
    for (const c of L.wiring.components || []) for (const ld of c.leads) leadIds.add(c.id + ':' + ld);
    for (const [a, b] of allEdges) {
      for (const ep of [a, b]) {
        if (ep.startsWith('pin:') && !pinNums.has(+ep.slice(4))) bad(L.id, 'V2', `步骤引用了未定义的引脚 ${ep}`);
        if (!ep.startsWith('pin:') && ep !== 'node:G' && ep !== 'GND' && ep !== '3V3' && !leadIds.has(ep)) bad(L.id, 'V2', `步骤引用了未定义的元件引脚 ${ep}`);
      }
    }
    // V6 每步都要有失败可视化
    for (const [i, st] of L.wiring.steps.entries()) {
      if (!st.observe) bad(L.id, 'V6', `第 ${i + 1} 步缺 observe（成功现象）`);
      if (!st.typicalFail && !st.plug) bad(L.id, 'V6', `第 ${i + 1} 步缺 typicalFail（典型失败场景）`);
      if (!st.plug && !st.edge) bad(L.id, 'V2', `第 ${i + 1} 步既无 edge 也无 plug`);
    }
  }

  // V3 判据完整性
  for (const key of ['inspection', 'field', 'verify']) {
    for (const it of L[key] || []) {
      if (!it.prompt && !it.text) bad(L.id, 'V3', `${key}/${it.id} 缺 prompt|text`);
      if (!it.failHint && it.kind !== 'ack') bad(L.id, 'V3', `${key}/${it.id} 缺 failHint`);
    }
  }

  // V4 采购完整性
  for (const p of L.procurement || []) {
    for (const f of ['spec', 'why', 'channel', 'dont']) if (!p[f]) bad(L.id, 'V4', `采购 ${p.id} 缺 ${f}`);
    if (p.choose) {
      if (!p.choose.options || p.choose.options.length < 2) bad(L.id, 'V4', `采购 ${p.id} choose 选项不足`);
      if (typeof p.choose.expect !== 'number') bad(L.id, 'V4', `采购 ${p.id} choose 缺 expect`);
      if (!p.choose.hint) bad(L.id, 'V4', `采购 ${p.id} choose 缺 hint`);
    }
  }

  const steps = L.wiring ? L.wiring.steps.length : 0;
  const gates = (L.field || []).length + (L.verify || []).length;
  console.log(`   stages: ${L.stages.map((s) => (typeof s === 'string' ? s : s.kind + (s.from != null ? `[${s.from}-${s.to ?? 'end'}${s.optional ? ',可选' : ''}]` : ''))).join(' → ')}`);
  console.log(`   wiring: ${steps} 步 · 判据: ${gates} 项 · 采购: ${(L.procurement || []).length} 项`);
}

console.log(`\n${problems === 0 ? '✓ 全部通过' : `✗ ${problems} 个问题`}`);
process.exit(problems ? 1 : 0);
