// M2-3 diff：EventEmitter 语义合同 —— error 抛出/errorMonitor、newListener/removeListener、
// Symbol 事件名、captureRejections、statics、maxListeners 警告。
const { EventEmitter } = require('node:events');
const results = {};

// 1. 静态导出集合（Node 插入顺序是内部细节，排序比较）
results.statics = Object.getOwnPropertyNames(EventEmitter)
  .filter((n) => !n.startsWith('_') && typeof EventEmitter[n] === 'function')
  .sort();

// 2. emit('error') 无监听器时抛出原值；errorMonitor 先被调用
{
  const ee = new EventEmitter();
  let uncaught = null;
  const mon = [];
  ee.on(EventEmitter.errorMonitor, (e) => mon.push(e.message));
  try { ee.emit('error', new Error('boom')); } catch (e) { uncaught = e.message; }
  results.errorUncaught = uncaught;
  results.errorMonitor = mon;
}

// 3. newListener / removeListener 事件
{
  const ee = new EventEmitter();
  const log = [];
  ee.on('newListener', (ev) => log.push('nl:' + String(ev)));
  ee.on('removeListener', (ev) => log.push('rl:' + String(ev)));
  const fn = () => {};
  ee.on('z', fn);
  ee.removeListener('z', fn);
  results.newRemove = log;
}

// 4. Symbol 事件名（计数 + eventNames 类型）
{
  const ee = new EventEmitter();
  const s = Symbol('k');
  ee.on(s, () => {});
  results.symbolCount = ee.listenerCount(s);
  results.symbolNames = ee.eventNames().map((n) => (typeof n === 'symbol' ? 'symbol' : n));
}

// 5. captureRejections：async 监听器 rejection → 'error'
{
  const ee = new EventEmitter({ captureRejections: true });
  let caught = null;
  ee.on('async', async () => { throw new Error('rej'); });
  ee.on('error', (e) => { caught = e.message; });
  ee.emit('async');
  setTimeout(() => {
    results.captureRejections = caught;
    // 6. maxListeners 警告（覆盖 process.emitWarning 捕获）
    const ee2 = new EventEmitter();
    let warned = null;
    if (process.emitWarning) {
      const orig = process.emitWarning;
      process.emitWarning = (msg) => { if (String(msg).includes('Possible EventEmitter memory leak')) warned = 'leak-warn'; };
      for (let i = 0; i < 12; i++) ee2.on('m', () => {});
      process.emitWarning = orig;
    }
    results.maxListenersWarn = warned;
    process.stdout.write(JSON.stringify(results));
  }, 50);
}
