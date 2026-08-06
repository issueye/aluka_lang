// M2-4 diff：Promise/microtask 语义 —— 微任务顺序、then 链、rejection 处理、Promise 状态。
const results = {};

// 1. 微任务顺序：promise then 在同步代码之后、timer 之前（setImmediate-vs-timer
//    顺序在 Node 中本就不确定，只断言微任务先于 timer）。
{
  const order = [];
  order.push('sync');
  Promise.resolve().then(() => order.push('micro'));
  queueMicrotask(() => order.push('queueMicrotask'));
  setTimeout(() => order.push('timer'), 0);
  setImmediate(() => {
    const i = order.indexOf('timer');
    results.microBeforeTimer = order[0] === 'sync' && order.indexOf('micro') < order.indexOf('queueMicrotask');
    results.microtasksBeforeTimer = i === -1 || i > 1;
  });
}

// 2. then 链顺序
{
  const chain = [];
  Promise.resolve(1)
    .then((x) => { chain.push('t1:' + x); return x + 1; })
    .then((x) => { chain.push('t2:' + x); return x * 2; })
    .then((x) => { chain.push('t3:' + x); });
  setImmediate(() => { results.chain = chain.join(','); });
}

// 3. rejection 冒泡到 catch
{
  let caught = null;
  Promise.reject(new Error('r'))
    .catch((e) => { caught = e.message; return 'ok'; })
    .then((v) => { results.rejectionCaught = caught + ':' + v; });
}

// 4. Promise 状态与 finally
{
  let fin = null;
  Promise.resolve('val')
    .finally(() => { fin = 'finally'; })
    .then((v) => { results.finallyOrder = fin + ':' + v; });
}

// 5. async/await
{
  const seq = [];
  (async () => {
    seq.push('a');
    await 1;
    seq.push('b');
  })().then(() => {
    results.asyncSeq = seq.join(',');
  });
}

// 6. unhandledRejection 事件
{
  const events = [];
  const onUR = (reason) => { events.push('unhandled:' + reason && reason.message); };
  process.on('unhandledRejection', onUR);
  Promise.reject(new Error('ur'));
  setImmediate(() => {
    process.removeListener('unhandledRejection', onUR);
    results.unhandledRejection = events.join(',');
  });
}

setTimeout(() => {
  // 键序（独立微任务链的赋值先后）不作为合同；按 key 排序后输出。
  const sorted = {};
  Object.keys(results).sort().forEach((k) => { sorted[k] = results[k]; });
  process.stdout.write(JSON.stringify(sorted));
}, 80);
