#!/usr/bin/env node
// gen-coverage.mjs — M0：依据 manifest + aluka 探针结果，生成：
//   docs/node22-api-coverage.md   —— L0-L4 覆盖报告（可命令再生成，不手工维护数字）
//   gaps.md                        —— 缺口清单（对应计划 M1/M2 等工作项）
//   manifest/modules.json          —— 回填 status 字段（L0-L4）
//
// 用法：node tools/gen-coverage.mjs
// 前置：node tools/gen-manifest.mjs && bash run-probe.sh

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(__dirname, '..');           // tests/compat/node22
const REPO = path.resolve(ROOT, '../../..');          // 仓库根
const MANIFEST = path.join(ROOT, 'manifest');
const RESULTS = path.join(ROOT, 'results');

function readJSON(p) {
  return JSON.parse(fs.readFileSync(p, 'utf8'));
}

// ---------------------------------------------------------------------------
// L 等级判定（名称面近似；L4 需要 diff 测试，M0 一律不授予）
// ---------------------------------------------------------------------------

// 名称面：exports + events + constants + classes 的扁平名字集合。
function surfaceNames(entry) {
  const names = new Set();
  for (const e of entry.exports || []) names.add(e.name);
  for (const e of entry.events || []) names.add(e.name);
  for (const e of entry.constants || []) names.add(e.name);
  for (const c of entry.classes || []) {
    // 类名可能带模块前缀（assert.AssertionError）——用末段匹配。
    names.add(c.name);
    for (const m of c.protoMethods || []) names.add(c.name + '#' + m.name);
    for (const p of c.protoProperties || []) names.add(c.name + '#' + p.name);
    for (const e of c.events || []) names.add(c.name + '#event:' + e.name);
  }
  return [...names];
}

// 判断 aluka 探针中该名字是否存在。
function namePresent(aluka, entry, name) {
  const exports = aluka.exports || [];
  const classNames = new Set((entry.classes || []).map((c) => c.name.split('.').pop()));
  if (exports.includes(name)) return true;
  if (name.includes('.') && exports.includes(name.split('.').pop())) return true;
  if (name.includes('#')) {
    const [cls, member] = name.split('#');
    const clsShort = cls.split('.').pop();
    // 类对象在 exports 中：检查其 prototype 成员。
    for (const ex of exports) {
      const exp = aluka.items && aluka.items[ex];
      if (exp && exp.protoMethods && (ex === cls || ex === clsShort)) {
        const memberName = member.replace(/^event:/, '');
        if (exp.protoMethods.includes(memberName)) return true;
      }
    }
    void classNames;
  }
  return false;
}

function computeLevel(aluka, entry, matchedCount, totalCount) {
  if (!aluka || !aluka.loads) return 'L0';
  if (totalCount === 0) return matchedCount > 0 ? 'L3' : 'L1';
  const pct = matchedCount / totalCount;
  if (pct === 0) return 'L1';
  if (pct < 1) return 'L2';
  return 'L3'; // 名称面 100%：近似 L3，官方示例与差分在后续里程碑授 L4
}

// ---------------------------------------------------------------------------
// 生成报告
// ---------------------------------------------------------------------------

function genCoverage() {
  const snapshot = readJSON(path.join(ROOT, 'data', 'snapshot.json'));
  const modules = readJSON(path.join(MANIFEST, 'modules.json'));
  const globals = readJSON(path.join(MANIFEST, 'globals.json'));
  const errors = readJSON(path.join(MANIFEST, 'errors.json'));
  const cli = readJSON(path.join(MANIFEST, 'cli.json'));

  let alukaMod = null;
  let alukaGlob = null;
  try {
    alukaMod = readJSON(path.join(RESULTS, 'probe-aluka-modules.json'));
    alukaGlob = readJSON(path.join(RESULTS, 'probe-aluka-globals.json'));
  } catch (e) {
    console.error('缺少探针结果，请先执行: bash run-probe.sh');
    process.exit(1);
  }
  const nodeMod = readJSON(path.join(RESULTS, 'probe-node-modules.json'));

  // --- 模块级评估 ---
  const rows = [];
  const gaps = [];
  const levelCount = { L0: 0, L1: 0, L2: 0, L3: 0, L4: 0 };
  let totalSurface = 0;
  let matchedSurface = 0;

  for (const entry of modules.entries) {
    const alu = (alukaMod.modules || {})[entry.name] || { loads: false };
    const names = surfaceNames(entry);
    const missing = [];
    let matched = 0;
    for (const n of names) {
      if (namePresent(alu, entry, n)) matched++;
      else missing.push(n);
    }
    const total = names.length;
    totalSurface += total;
    matchedSurface += matched;
    const level = computeLevel(alu, entry, matched, total);
    levelCount[level]++;
    entry.status = level;
    if (missing.length > 0) {
      gaps.push({
        module: entry.name,
        level,
        missing: missing.length,
        total,
        samples: missing.slice(0, 12),
      });
    }
    rows.push({
      name: entry.name,
      level,
      docPage: entry.docPage,
      surface: total,
      matched,
      missingCount: missing.length,
      pct: total === 0 ? '-' : Math.round((matched / total) * 100) + '%',
    });
  }

  // --- 全局评估 ---
  const globalsReport = [];
  for (const g of globals.globals) {
    const alu = alukaGlob && alukaGlob.globals ? alukaGlob.globals[g.name] : null;
    const present = !!(alu && alu.present);
    globalsReport.push({ name: g.name, present, kind: g.kind });
    if (!present) gaps.push({ global: g.name, kind: g.kind, missing: 'global missing' });
  }
  // 全局类评估（classes 存在性）。
  const globalClassReport = [];
  for (const c of globals.classes) {
    const short = c.name.split('.').pop();
    const alu = alukaGlob && alukaGlob.globals ? alukaGlob.globals[short] : null;
    const present = !!(alu && alu.present);
    globalClassReport.push({ name: c.name, present });
    if (!present) gaps.push({ global: c.name, kind: 'class', missing: 'class missing' });
  }
  // 全局方法评估。
  const globalMethodReport = [];
  for (const m of globals.methods) {
    const alu = alukaGlob && alukaGlob.globals ? alukaGlob.globals[m.name] : null;
    const present = !!(alu && alu.present);
    globalMethodReport.push({ name: m.name, present });
    if (!present) gaps.push({ global: m.name, kind: 'method', missing: 'method missing' });
  }

  // --- 事件/错误/CLI 摘要 ---
  const eventsDiff = (() => {
    try { return fs.readFileSync(path.join(RESULTS, 'diff-events.txt'), 'utf8').split('\n').filter(Boolean); } catch { return []; }
  })();

  const modPct = modules.entryCount === 0 ? 0 : Math.round((matchedSurface / totalSurface) * 100);

  // -------------------------------------------------------------------------
  // 写 docs/node22-api-coverage.md
  // -------------------------------------------------------------------------
  const md = [];
  md.push('# Aluka × Node 22 完整公开 API 覆盖报告');
  md.push('');
  md.push(`> 自动生成：\`tests/compat/node22/gen-all.sh\`（gen-manifest → run-probe → gen-coverage），禁止手工修改。`);
  md.push(`> 数据源：官方 API JSON ${snapshot.nodeDocVersion}（sha256 \`${snapshot.allJsonSha256}\`）、本机 Node ${snapshot.nodeRuntimeVersion}、Aluka 探针实测。`);
  md.push(`> 平台：${snapshot.platform}/${snapshot.arch} ｜ 生成时间：${new Date().toISOString()}`);
  md.push('');
  md.push('## 1. 总体结论');
  md.push('');
  md.push(`- 入口：${modules.entryCount}/57 有 manifest；名称面覆盖 ${matchedSurface}/${totalSurface}（${modPct}%）。`);
  md.push(`- 等级分布：${Object.entries(levelCount).map(([k, v]) => `${k}=${v}`).join('，')}。`);
  md.push('- **L0-L4 判定口径**：本报告为探针初始分级（名称面近似）。L3 表示 manifest 名称面 100% 存在；' +
    'L4 只在对应模块差分/语义测试通过后授予（M10 全量认证）。');
  md.push('- 已知差异与缺口见 [gaps.md](./node22/../tests/compat/node22/gaps.md) 与下文章节。');
  md.push('');
  md.push('## 2. 模块清单');
  md.push('');
  md.push('| 模块 | L 级 | 文档页 | 名称面 | 覆盖 | 缺口样例 |');
  md.push('|------|------|--------|--------|------|----------|');
  for (const r of rows) {
    const gapSample = gaps.find((g) => g.module === r.name);
    const sample = gapSample ? gapSample.samples.slice(0, 5).join('、') : '-';
    md.push(`| \`${r.name}\` | ${r.level} | ${r.docPage || '-'} | ${r.surface} | ${r.pct} | ${sample || '-'} |`);
  }
  md.push('');
  md.push('## 3. 全局与 Web API');
  md.push('');
  md.push('### 3.1 全局对象');
  md.push('');
  md.push('| 全局 | 状态 |');
  md.push('|------|------|');
  for (const g of globalsReport) md.push(`| \`${g.name}\` | ${g.present ? '✅' : '❌ 缺失'} |`);
  md.push('');
  md.push('### 3.2 全局类');
  md.push('');
  md.push('| 类 | 状态 |');
  md.push('|----|------|');
  for (const c of globalClassReport) md.push(`| \`${c.name}\` | ${c.present ? '✅' : '❌ 缺失'} |`);
  md.push('');
  md.push('### 3.3 全局方法');
  md.push('');
  md.push('| 方法 | 状态 |');
  md.push('|------|------|');
  for (const m of globalMethodReport) md.push(`| \`${m.name}\` | ${m.present ? '✅' : '❌ 缺失'} |`);
  md.push('');
  md.push('## 4. 事件语义探针差异（EventEmitter 合同）');
  md.push('');
  if (eventsDiff.length === 0) {
    md.push('（无差异或探针未生成）');
  } else {
    md.push('```text');
    md.push(...eventsDiff.slice(0, 50));
    md.push('```');
  }
  md.push('');
  md.push('## 5. 错误与 CLI 清单规模');
  md.push('');
  md.push(`- errors.json：${errors.codes.length} 个错误码（ERR_* 为主），${errors.classes.length} 个错误类。`);
  md.push(`- cli.json：${cli.flags.length} 个 CLI flags，${cli.environmentVariables.length} 个环境变量，${cli.exitCodes.length} 个退出码项。`);
  md.push('');
  md.push('## 6. 再生成命令');
  md.push('');
  md.push('```bash');
  md.push('cd tests/compat/node22');
  md.push('bash gen-all.sh            # 一键重建 manifest + 探针 + 本报告 + gaps.md');
  md.push('# 分步：');
  md.push('node tools/gen-manifest.mjs   # 1) 从 data/all.json 生成四类 manifest');
  md.push('bash run-probe.sh             # 2) node 与 aluka 双跑探针');
  md.push('node tools/gen-coverage.mjs   # 3) 生成覆盖报告与 gaps.md');
  md.push('```');
  md.push('');
  fs.writeFileSync(path.join(REPO, 'docs', 'node22-api-coverage.md'), md.join('\n'), 'utf8');

  // -------------------------------------------------------------------------
  // 写 gaps.md
  // -------------------------------------------------------------------------
  const gm = [];
  gm.push('# Node 22 兼容缺口清单（M0 探针实测）');
  gm.push('');
  gm.push(`> 自动生成于 ${new Date().toISOString()}，对应冻结快照 ${snapshot.freezeVersion}。`);
  gm.push('> 缺口 = aluka 探针相对官方 manifest 缺失的条目；L 级判定见覆盖报告。');
  gm.push('');
  gm.push('## 1. 缺失模块（加载失败 → L0，对应 M1）');
  gm.push('');
  const l0 = rows.filter((r) => r.level === 'L0');
  gm.push(`共 ${l0.length} 个：${l0.map((r) => `\`${r.name}\``).join('、')}。`);
  gm.push('');
  gm.push('## 2. 已有模块的名称面缺口（对应 M2-M6 逐模块审计）');
  gm.push('');
  gm.push('| 模块 | 缺口数/总数 | 缺口样例 |');
  gm.push('|------|-------------|----------|');
  for (const g of gaps.filter((x) => x.module)) {
    gm.push(`| \`${g.module}\` | ${g.missing}/${g.total} | ${g.samples.slice(0, 8).join('、')} |`);
  }
  gm.push('');
  gm.push('## 3. 全局对象缺口');
  gm.push('');
  const gmiss = gaps.filter((x) => x.global);
  gm.push(gmiss.map((g) => `- \`${g.global}\`（${g.kind}）`).join('\n') || '（无）');
  gm.push('');
  gm.push('## 4. 事件语义缺口（EventEmitter 合同差异样例）');
  gm.push('');
  gm.push(eventsDiff.slice(0, 30).map((d) => `- ${d}`).join('\n') || '（无）');
  gm.push('');
  fs.writeFileSync(path.join(ROOT, 'gaps.md'), gm.join('\n'), 'utf8');

  // -------------------------------------------------------------------------
  // 回填 modules.json status
  // -------------------------------------------------------------------------
  fs.writeFileSync(path.join(MANIFEST, 'modules.json'), JSON.stringify(modules, null, 2) + '\n', 'utf8');

  console.log('written: docs/node22-api-coverage.md');
  console.log('written: tests/compat/node22/gaps.md');
  console.log('updated: manifest/modules.json status');
  console.log(`summary: entries=${modules.entryCount} levels=${JSON.stringify(levelCount)} surface=${matchedSurface}/${totalSurface}`);
}

genCoverage();
