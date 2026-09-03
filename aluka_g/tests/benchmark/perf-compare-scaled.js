// 跨引擎对比（放大迭代量版）：
//
// 相对 perf-compare.js 的两点修正：
//   1. 迭代量放大到读数 >50ms。aluka 的 process.hrtime/Date.now 建在 Go
//      time.Since 上，本机 Windows 分辨率实测约 546µs（Node 用 QPC，约
//      100ns）；perf-compare 的 callOverhead/methodCall/fib25 落在 1-5ms，
//      被时钟地板淹没，甚至偶现 0.00ms。
//   2. 返回值存入 sink 数组。aluka 的 JIT trace 会把结果未被消费的循环判为
//      无副作用整体跳过，导致首次调用出现虚假的 30 倍加速。
//
// 用法：
//   node  tests/benchmark/perf-compare-scaled.js [caseName]
//   aluka tests/benchmark/perf-compare-scaled.js [caseName]
// 只传单个用例名时仅跑该用例——供 compare-run.sh 逐用例交替执行，避免
// 同进程内先跑的用例把 CPU 烤热、后跑的用例被降频惩罚。
"use strict";
const REPS = Number(process.env.REPS || 3);
const only = process.argv[2] || null;

const sink = [];
function bench(name, fn) {
  if (only && name !== only) return;
  let best = Infinity;
  for (let r = 0; r < REPS; r++) {
    const t0 = Date.now();
    // 返回值存入 sink：aluka 的 JIT trace 会把结果未被消费的循环判为
    // 无副作用并整体跳过（perf-compare 的 callOverhead 因此偶现 0.00ms）。
    sink.push(fn());
    const ms = Date.now() - t0;
    if (ms < best) best = ms;
  }
  console.log(`${name}: ${best}`);
}

function fib(n) { return n < 2 ? n : fib(n - 1) + fib(n - 2); }
bench("fib32", () => fib(32));

bench("propAccess-60M", () => {
  const o = { a: 1, b: 2, c: 3 };
  let s = 0;
  for (let i = 0; i < 60000000; i++) s += o.a + o.b + o.c;
  return s;
});

bench("propSet-60M", () => {
  const o = { a: 0 };
  for (let i = 0; i < 60000000; i++) o.a = i;
  return o.a;
});

bench("strConcat-1M", () => {
  let s = "";
  for (let i = 0; i < 1000000; i++) s += "x" + i;
  return s.length;
});

bench("arrayPush-10M", () => {
  const a = [];
  for (let i = 0; i < 10000000; i++) a.push(i);
  return a.length;
});

bench("arrayMap-100x100K", () => {
  const a = [];
  for (let i = 0; i < 100000; i++) a.push(i);
  let s = 0;
  for (let j = 0; j < 100; j++) s += a.map(x => x * 2).length;
  return s;
});

bench("callOverhead-30M", () => {
  function noop() {}
  let s = 0;
  for (let i = 0; i < 30000000; i++) { noop(); s++; }
  return s;
});

bench("closureCall-30M", () => {
  function make() { let n = 0; return () => ++n; }
  const f = make();
  let s = 0;
  for (let i = 0; i < 30000000; i++) s += f();
  return s;
});

bench("methodCall-30M", () => {
  const o = { v: 1, get() { return this.v; } };
  let s = 0;
  for (let i = 0; i < 30000000; i++) s += o.get();
  return s;
});

bench("elemRead-6M", () => {
  // 模块作用域数组 + 下标读 + 属性读（trace tier 的 GetElem/对象 upvalue 路径）
  const nums = []; for (let i = 0; i < 2000000; i++) nums.push(i);
  const objs = []; for (let i = 0; i < 2000000; i++) objs.push({ v: i });
  let s1 = 0;
  for (let i = 0; i < 2000000; i++) s1 += nums[i];
  let s2 = 0;
  for (let i = 0; i < 2000000; i++) s2 += objs[i].v;
  return s1 + s2;
});

bench("gcPressure-3M", () => {
  let keep = [];
  for (let i = 0; i < 3000000; i++) {
    const o = { x: i, y: { z: i }, arr: [i, i + 1] };
    if (i % 100 === 0) keep.push(o);
  }
  return keep.length;
});
