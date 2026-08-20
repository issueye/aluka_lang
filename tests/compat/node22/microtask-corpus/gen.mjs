// gen.mjs — 工作流 C1 微任务顺序语料生成器（compat-boundary-closure-plan）。
// 参数化生成"批量 await"程序：维度 = async 函数数 × 每函数 await 数 ×
// Promise/nextTick/queueMicrotask 混合 × rejection 比例 × TLA 模块数 × 定时器交错。
// 每个任务回调立即 console.log 一行 `标签`（stdout 顺序即调度顺序证据，
// 双跑 diff 序列即偏差证据）。种子化 PRNG，同 --seed 输出确定。
// 用法：node gen.mjs --seed 42 --count 60 --outdir cases
'use strict';
import { mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { join } from 'node:path';

const args = process.argv.slice(2);
const opt = (name, dflt) => {
  const i = args.indexOf('--' + name);
  return i >= 0 ? args[i + 1] : dflt;
};
const seed = Number(opt('seed', 42));
const count = Number(opt('count', 60));
const outdir = opt('outdir', 'cases');

// mulberry32：确定性 PRNG。
function prng(s) {
  return function () {
    s |= 0; s = (s + 0x6D2B79F5) | 0;
    let t = Math.imul(s ^ (s >>> 15), 1 | s);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

rmSync(outdir, { recursive: true, force: true });
mkdirSync(outdir, { recursive: true });

const HEAD = `'use strict';
process.on('unhandledRejection', function (r) { console.log('u:' + r); });
console.log('s:start');
`;

// genCase 生成一个非 TLA 用例源码。
function genCase(rnd, idx) {
  const nFns = 2 + Math.floor(rnd() * 4);      // 2..5 个 async 函数
  const lines = [HEAD];
  const timerBits = [];
  let microSeq = 0, tickSeq = 0, thenSeq = 0, timerSeq = 0;

  // await 目标形状：已决 promise / queueMicrotask 包装 / nextTick 包装 /
  // rejected（部分被 catch、部分裸奔产生 unhandled）。
  function awaitTarget(fi, ai) {
    const roll = rnd();
    const tag = 'f' + fi + '.' + ai;
    if (roll < 0.35) {
      return `await Promise.resolve().then(function () { console.log('p:then-${thenSeq++}'); });`;
    }
    if (roll < 0.55) {
      return `await new Promise(function (res) { queueMicrotask(function () { console.log('m:micro-${microSeq++}'); res(); }); });`;
    }
    if (roll < 0.75) {
      return `await new Promise(function (res) { process.nextTick(function () { console.log('t:tick-${tickSeq++}'); res(); }); });`;
    }
    if (roll < 0.9) {
      // rejected：随机是否在 async 函数外 catch（不 catch → unhandled）。
      return `await Promise.reject('rej-${tag}');`;
    }
    return `await new Promise(function (res) { setTimeout(function () { console.log('ti:timer-${timerSeq++}'); res(); }, 0); });`;
  }

  for (let fi = 1; fi <= nFns; fi++) {
    const nAwaits = 1 + Math.floor(rnd() * 3);
    const caught = rnd() < 0.6; // 该函数是否 .catch
    const body = [];
    body.push(`  console.log('a:f${fi}.0');`);
    for (let ai = 1; ai <= nAwaits; ai++) {
      body.push('  ' + awaitTarget(fi, ai));
      body.push(`  console.log('a:f${fi}.${ai}');`);
    }
    body.push(`  return 'ret-f${fi}';`);
    lines.push(`async function f${fi}() {\n${body.join('\n')}\n}`);
    if (caught) {
      lines.push(`f${fi}().catch(function (e) { console.log('c:catch-f${fi}:' + e); });`);
    } else if (rnd() < 0.5) {
      lines.push(`f${fi}().then(function (v) { console.log('p:then-ret-' + v); });`);
    } else {
      lines.push(`f${fi}();`);
    }
  }

  // 顶层交错：微任务/nextTick/已决 then 混合（入队顺序即证据）。
  const nTop = 1 + Math.floor(rnd() * 4);
  for (let i = 0; i < nTop; i++) {
    const roll = rnd();
    if (roll < 0.4) {
      lines.push(`queueMicrotask(function () { console.log('m:top-micro-${i}'); });`);
    } else if (roll < 0.7) {
      lines.push(`process.nextTick(function () { console.log('t:top-tick-${i}'); });`);
    } else {
      lines.push(`Promise.resolve().then(function () { console.log('p:top-then-${i}'); });`);
    }
  }

  // 尾部定时器收口（保证 unhandledRejection 在末尾检查点前都已判定）。
  timerBits.push(`setTimeout(function () { console.log('ti:final'); }, 0);`);
  lines.push(...timerBits);
  return lines.join('\n') + '\n';
}

// genTlaCase 生成一个 TLA 用例目录：main.mjs + N 个含顶层 await 的模块。
// 模块间用静态 import 形成 ESM 图；每模块 TLA 前后打点。
function genTlaCase(rnd, idx) {
  const nMods = 1 + Math.floor(rnd() * 3);
  const dir = join(outdir, 'tla-' + String(idx).padStart(4, '0'));
  mkdirSync(dir, { recursive: true });
  let main = `'use strict';\nconsole.log('s:main-start');\n`;
  for (let m = 1; m <= nMods; m++) {
    main += `import { v as v${m} } from './mod${m}.mjs';\nconsole.log('s:main-loaded-${m}:' + v${m});\n`;
    const nAwaits = 1 + Math.floor(rnd() * 2);
    const mod = [`console.log('a:mod${m}.0');`];
    for (let ai = 1; ai <= nAwaits; ai++) {
      const roll = rnd();
      if (roll < 0.5) {
        mod.push(`await Promise.resolve().then(function () { console.log('p:mod${m}-${ai}'); });`);
      } else if (roll < 0.75) {
        mod.push(`await new Promise(function (res) { queueMicrotask(function () { console.log('m:mod${m}-${ai}'); res(); }); });`);
      } else {
        mod.push(`await new Promise(function (res) { process.nextTick(function () { console.log('t:mod${m}-${ai}'); res(); }); });`);
      }
      mod.push(`console.log('a:mod${m}.${ai}');`);
    }
    mod.push(`export const v = 'mod${m}';`);
    writeFileSync(join(dir, 'mod' + m + '.mjs'), mod.join('\n') + '\n');
  }
  main += `console.log('s:main-end');\nsetTimeout(function () { console.log('ti:final'); }, 0);\n`;
  writeFileSync(join(dir, 'main.mjs'), main);
}

for (let i = 0; i < count; i++) {
  const rnd = prng(seed + i * 7919);
  if (rnd() < 0.7) {
    writeFileSync(join(outdir, 'case-' + String(i).padStart(4, '0') + '.cjs'), genCase(rnd, i));
  } else {
    genTlaCase(rnd, i);
  }
}
console.log(`generated ${count} cases into ${outdir}`);
