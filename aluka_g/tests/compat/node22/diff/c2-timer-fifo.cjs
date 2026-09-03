// C2 差分：微任务顺序——同刻到期定时器 FIFO（compat-boundary-closure-plan 工作流 C）。
// Node 语义：同一时刻到期的 setTimeout 按注册顺序执行（timer list）。
// 此前 aluka 每个定时器独立 time.AfterFunc，多个 0ms 定时器并发竞争投递
// 导致偶发乱序（实测 20 次出现 2 种序列）。集中式到期队列修复后必须稳定 FIFO。
'use strict';
const out = [];

// 顶层同步注册 t0..t2（0ms）。
let i = 0;
for (; i < 3; i++) {
  const n = i;
  setTimeout(function () { out.push('t:' + n); }, 0);
}

// 微任务期间再注册 t3..t5：必须在 t0..t2 之后。
queueMicrotask(function () {
  for (let k = 3; k < 6; k++) {
    setTimeout(function () { out.push('t:' + k); }, 0);
  }
});

// 首个定时器回调内注册 t6：排在其后。
setTimeout(function () {
  setTimeout(function () { out.push('t:6'); }, 0);
  out.push('t:inner-6');
}, 0);

setTimeout(function () {
  out.push('end');
  console.log(out.join('\n'));
}, 0);
