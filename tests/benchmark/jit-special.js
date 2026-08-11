// JIT 专项基准（R0-5 冻结快照）。
// 覆盖 J2/J3 里程碑报告的专项形态，与 perf-compare.js 相同输出格式 `name: ms`，
// 供 bench/cmd/jitbench 统一采集（off/quick/auto 三 tier、5 次中位数）。
//
//   1) jitNumericLoop-3M   纯数值循环（J2 专项 sum(3_000_000)）
//   2) jitCalleeInline-1M  单态 callee 内联调用（J3 专项）
//   3) jitExternalProps-3M 外部对象三属性累加（J3 专项）
//   4) jitPropWrite-3M     外部对象已有 own Number 属性写（J3 专项）
"use strict";

function timeIt(name, iterations, fn) {
  const start = process.hrtime.bigint();
  fn(iterations);
  const ms = Number(process.hrtime.bigint() - start) / 1e6;
  console.log(`${name}: ${ms.toFixed(2)}`);
}

// 1) 纯数值循环：自递归以外的数值热点，回边 trace / Native 目标形态。
function jitNumericLoop(n) {
  let s = 0;
  for (let i = 0; i < n; i++) { s += i; }
  return s;
}
timeIt("jitNumericLoop-3M", 1, () => jitNumericLoop(3000000));

// 2) 单态 callee 内联调用：稳定叶子目标，调用点以 callee identity guard。
function leaf(x) { return x + 1; }
function jitCalleeInline(n) {
  let s = 0;
  for (let i = 0; i < n; i++) { s += leaf(i); }
  return s;
}
timeIt("jitCalleeInline-1M", 1, () => jitCalleeInline(1000000));

// 3) 外部对象三属性累加：对象由外部传入，trace 以 shape/slot guard 提升属性值。
function jitExternalProps(o, n) {
  let s = 0;
  for (let i = 0; i < n; i++) { s += o.a + o.b + o.c; }
  return s;
}
timeIt("jitExternalProps-3M", 1, () => jitExternalProps({ a: 1, b: 2, c: 3 }, 3000000));

// 4) 已有 own Number 属性写：写操作只改 Frame 标量，语义出口/预算 yield 后由 Go 写回。
function jitPropWrite(o, n) {
  for (let i = 0; i < n; i++) { o.a = i; }
  return o.a;
}
timeIt("jitPropWrite-3M", 1, () => jitPropWrite({ a: 0 }, 3000000));
