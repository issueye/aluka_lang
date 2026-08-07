// 监控器与内存上限演示（--monitor / --max-memory）。
//
// 运行：
//   aluka --monitor demo/monitor-demo.js
//   aluka --monitor=500ms --monitor-format=json demo/monitor-demo.js
//   aluka --max-memory=64MB demo/monitor-demo.js      # 触发可捕获 RangeError
//   ALUKA_MAX_MEMORY=64MB aluka demo/monitor-demo.js  # 环境变量同样生效

// 1) CPU 负载：递归斐波那契（大量调用帧）。
function fib(n) {
  if (n < 2) return n;
  return fib(n - 1) + fib(n - 2);
}
console.log('fib(24) =', fib(24));

// 2) 内存负载：构造 200K 个对象并保留。
const items = [];
for (let i = 0; i < 200000; i++) {
  items.push({ idx: i, tag: 'item-' + i, data: [i, i + 1, i + 2] });
}
console.log('items =', items.length, '| last =', items[199999].tag);

// 3) 演示 --max-memory 的可捕获 OOM（仅在达到上限时触发）。
let s = '';
let i = 0;
let caught = null;
try {
  while (i < 3000000) {
    s += 'chunk-' + (i++) + '-';
    if (i % 50000 === 0) s = s.slice(0, 20000); // 保持有界（无上限时不失控）
  }
} catch (e) {
  // --max-memory 生效时走到这里（RangeError: JavaScript heap out of memory）。
  caught = e.name + ': ' + String(e.message).slice(0, 48);
  console.log('caught:', caught);
}
if (!caught) console.log('loop completed without memory limit (add --max-memory to see OOM)');
console.log('monitor demo done');
