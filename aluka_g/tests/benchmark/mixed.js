// 混合负载（pprof --profile / --monitor 用）：
//   覆盖调用（fib 递归）、属性读/写、字符串拼接、数组 push、
//   数组高阶回调（map/filter/reduce 简单回调 → O-6 原生路径）、GC 压力。
// 与 v1/v2 报告口径一致（fib22 + 100万属性读 + 3万拼接 + 30万 push + 300×10K map），
// 另加 20×10K filter/reduce 使 O-6 覆盖面可测。
"use strict";

// 1) fib(22) 递归调用 + 算术
function fib(n) { return n < 2 ? n : fib(n - 1) + fib(n - 2); }
fib(22);

// 2) 100 万对象属性读（隐藏类 IC）
{
  const o = { a: 1, b: 2, c: 3 };
  let s = 0;
  for (let i = 0; i < 1000000; i++) { s += o.a + o.b + o.c; }
}

// 3) 3 万字符串拼接（rope）
{
  let s = "";
  for (let i = 0; i < 30000; i++) { s += "x" + i; }
}

// 4) 30 万数组 push
{
  const a = [];
  for (let i = 0; i < 300000; i++) { a.push(i); }
}

// 5) 300×10K map（简单箭头回调 → O-6 原生路径）
{
  const a = [];
  for (let i = 0; i < 10000; i++) { a.push(i); }
  let s = 0;
  for (let j = 0; j < 300; j++) { s += a.map(x => x * 2).length; }
}

// 6) 50×10K filter + reduce（简单回调）
{
  const a = [];
  for (let i = 0; i < 10000; i++) { a.push(i); }
  let s = 0;
  for (let j = 0; j < 50; j++) {
    const f = a.filter(x => x % 2 === 0);
    s += f.reduce((acc, x) => acc + x, 0);
  }
}

// 7) 5 万短生命周期对象（GC 压力）
{
  let keep = [];
  for (let i = 0; i < 50000; i++) {
    const o = { x: i, y: { z: i }, arr: [i, i + 1] };
    if (i % 100 === 0) { keep.push(o); }
  }
}
