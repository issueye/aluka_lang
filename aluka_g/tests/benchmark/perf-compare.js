// 跨引擎性能对比基准（aluka vs Node 22）：
//   同代码分别用 aluka 与 node 运行，输出 `name: ms` 单行，供脚本对比。
//
// 用法：
//   aluka tests/benchmark/perf-compare.js
//   node  tests/benchmark/perf-compare.js
//
// 每个用例固定迭代量（避免不同引擎执行速度导致的 N 调整偏差），
// 计时用 process.hrtime.bigint()（微秒精度）。
"use strict";

function timeIt(name, iterations, fn) {
  const start = process.hrtime.bigint();
  fn(iterations);
  const ms = Number(process.hrtime.bigint() - start) / 1e6;
  console.log(`${name}: ${ms.toFixed(2)}`);
}

// 1) fib(25) 递归调用 + 算术 + 控制流
function fib(n) { return n < 2 ? n : fib(n - 1) + fib(n - 2); }
timeIt("fib25", 1, () => fib(25));

// 2) fib(30) 更重负载（单次，调用深度大）
timeIt("fib30", 1, () => fib(30));

// 3) 对象属性读（隐藏类 IC 命中）
function propAccess(iters) {
  let s = 0;
  const o = { a: 1, b: 2, c: 3 };
  for (let i = 0; i < iters; i++) { s += o.a + o.b + o.c; }
  return s;
}
timeIt("propAccess-3M", 3000000, propAccess);

// 4) 对象属性写
function propSet(iters) {
  const o = { a: 0 };
  for (let i = 0; i < iters; i++) { o.a = i; }
  return o.a;
}
timeIt("propSet-3M", 3000000, propSet);

// 5) 字符串拼接（动态长度）
function strConcat(iters) {
  let s = "";
  for (let i = 0; i < iters; i++) { s += "x" + i; }
  return s.length;
}
timeIt("strConcat-100K", 100000, strConcat);

// 6) 数组 push
function arrayPush(iters) {
  const a = [];
  for (let i = 0; i < iters; i++) { a.push(i); }
  return a.length;
}
timeIt("arrayPush-1M", 1000000, arrayPush);

// 7) 数组 map 高阶（回调调用开销）
function arrayMap(iters) {
  const a = [];
  for (let i = 0; i < 10000; i++) { a.push(i); }
  let s = 0;
  for (let j = 0; j < iters / 10000; j++) { s += a.map(x => x * 2).length; }
  return s;
}
timeIt("arrayMap-100x10K", 1000000, arrayMap);

// 8) 空函数调用开销
function callOverhead(iters) {
  function noop() {}
  let s = 0;
  for (let i = 0; i < iters; i++) { noop(); s++; }
  return s;
}
timeIt("callOverhead-1M", 1000000, callOverhead);

// 9) 闭包调用（upvalue 访问）
function closureCall(iters) {
  function make() { let n = 0; return () => ++n; }
  const f = make();
  let s = 0;
  for (let i = 0; i < iters; i++) { s += f(); }
  return s;
}
timeIt("closureCall-1M", 1000000, closureCall);

// 10) 方法调用 obj.method()
function methodCall(iters) {
  const o = { v: 1, get() { return this.v; } };
  let s = 0;
  for (let i = 0; i < iters; i++) { s += o.get(); }
  return s;
}
timeIt("methodCall-1M", 1000000, methodCall);

// 11) 短生命周期对象创建（GC 压力）
function gcPressure(iters) {
  let keep = [];
  for (let i = 0; i < iters; i++) {
    const o = { x: i, y: { z: i }, arr: [i, i + 1] };
    if (i % 100 === 0) { keep.push(o); }
  }
  return keep.length;
}
timeIt("gcPressure-500K", 500000, gcPressure);
