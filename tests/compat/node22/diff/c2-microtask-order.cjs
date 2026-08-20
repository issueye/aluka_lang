// C2 差分：微任务顺序——unhandledRejection 检查点末尾判定（compat-boundary-closure-plan 工作流 C）。
// Node 语义：unhandledRejection 在微任务检查点末尾统一派发（rejection FIFO）；
// 同一检查点内稍后挂上 catch 的不派发。此前 aluka 在 reject 瞬间把检查任务按
// 入队位置插入微任务队列，导致事件提前/假阳性。
'use strict';
const out = [];
process.on('unhandledRejection', function (r) { out.push('u:' + r); });

// 1) 检查点末尾派发 + FIFO：三个裸 rejection 的事件必须在全部微任务之后、按 rejection 顺序。
const p1 = Promise.reject('r1');
queueMicrotask(function () { out.push('m:1'); });
const p2 = Promise.reject('r2');
Promise.resolve().then(function () { out.push('p:1'); });
const p3 = Promise.reject('r3');
queueMicrotask(function () { out.push('m:2'); });

// 2) 同检查点内稍后挂 catch：不派发（不误报）。
const late = Promise.reject('late');
queueMicrotask(function () {
  late.catch(function (e) { out.push('c:late:' + e); });
});

setTimeout(function () {
  // 3) 检查点之后（新 tick）挂 catch 太迟：仍然派发。
  const tickLate = Promise.reject('ticklate');
  Promise.resolve().then(function () {
    Promise.resolve().then(function () {
      tickLate.catch(function (e) { out.push('c:ticklate:' + e); });
    });
  });
  setTimeout(function () {
    out.push('end');
    console.log(out.join('\n'));
  }, 0);
}, 0);
