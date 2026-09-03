// probe/events.cjs — M0 事件语义探针（EventEmitter 合同）：
// 方法面、error 特殊路径、once/prepend、newListener/removeListener、maxListeners、
// errorMonitor、captureRejections、Symbol 监听器。输出归一化 JSON 单行。
'use strict';

const { EventEmitter } = require('node:events');

const results = {};

function record(name, value) {
  results[name] = value;
}

// 1. 方法面
const emitterMethods = [
  'addListener', 'on', 'once', 'off', 'removeListener', 'removeAllListeners',
  'emit', 'listeners', 'rawListeners', 'eventNames', 'prependListener',
  'prependOnceListener', 'setMaxListeners', 'getMaxListeners', 'listenerCount',
];
record('methodSurface', emitterMethods.filter((m) => typeof EventEmitter.prototype[m] === 'function'));

// 2. 静态导出（Node 插入顺序是内部实现细节，比较时排序）。
record('statics', Object.getOwnPropertyNames(EventEmitter)
  .filter((n) => !n.startsWith('_') && typeof EventEmitter[n] === 'function')
  .sort());

// 3. 基本 emit + on
{
  const ee = new EventEmitter();
  let got = [];
  ee.on('x', (a, b) => got.push(a + b));
  ee.emit('x', 1, 2);
  ee.emit('x', 10, 20);
  record('basic', got);
}

// 4. once
{
  const ee = new EventEmitter();
  let n = 0;
  ee.once('x', () => n++);
  ee.emit('x');
  ee.emit('x');
  record('once', n);
}

// 5. prependListener 顺序
{
  const ee = new EventEmitter();
  const order = [];
  ee.on('x', () => order.push('late'));
  ee.prependListener('x', () => order.push('pre'));
  ee.emit('x');
  record('prepend', order);
}

// 6. off / removeListener
{
  const ee = new EventEmitter();
  const fn = () => {};
  ee.on('x', fn);
  ee.off('x', fn);
  record('off', ee.listenerCount('x'));
}

// 7. listenerCount / eventNames / rawListeners
{
  const ee = new EventEmitter();
  ee.on('a', () => {});
  ee.on('b', () => {});
  ee.on('b', () => {});
  record('eventNames', ee.eventNames().map(String));
  record('listenerCount', [ee.listenerCount('a'), ee.listenerCount('b')]);
  record('rawListenersType', typeof ee.rawListeners('a')[0]);
}

// 8. error 事件：无监听器时 emit('error') 抛错；errorMonitor 收到但不吞错。
{
  const ee = new EventEmitter();
  let uncaught = null;
  const mon = [];
  ee.on(EventEmitter.errorMonitor, (e) => mon.push(e.message));
  try {
    ee.emit('error', new Error('boom'));
  } catch (e) {
    uncaught = e.message;
  }
  record('errorUncaught', uncaught);
  record('errorMonitor', mon);
}

// 9. newListener / removeListener 事件
{
  const ee = new EventEmitter();
  const log = [];
  ee.on('newListener', (ev) => log.push('nl:' + ev));
  ee.on('removeListener', (ev) => log.push('rl:' + ev));
  const fn = () => {};
  ee.on('z', fn);
  ee.removeListener('z', fn);
  record('newRemove', log);
}

// 10. maxListeners：>10 时默认警告
{
  const ee = new EventEmitter();
  let warned = null;
  const origWarn = process.emitWarning ? process.emitWarning.bind(process) : null;
  if (process.emitWarning) {
    process.emitWarning = (msg) => { warned = String(msg).includes('Possible EventEmitter memory leak') ? 'leak-warn' : warned; };
  }
  for (let i = 0; i < 12; i++) ee.on('m', () => {});
  if (process.emitWarning) process.emitWarning = origWarn;
  record('maxListenersDefault', ee.getMaxListeners());
  record('maxListenersWarn', warned);
}

// 11. captureRejections
{
  const ee = new EventEmitter({ captureRejections: true });
  let caught = null;
  ee.on('async', async () => { throw new Error('rej'); });
  ee.on('error', (e) => { caught = e.message; });
  ee.emit('async');
  setImmediate(() => {
    record('captureRejections', caught);
    finish();
  });
}

// 12. Symbol 监听器（events.on 的名字带 Symbol 不影响计数）
{
  const ee = new EventEmitter();
  const s = Symbol('k');
  ee.on(s, () => {});
  record('symbolListenerCount', ee.listenerCount(s));
  record('symbolEventNames', ee.eventNames().map((n) => (typeof n === 'symbol' ? 'symbol' : n)));
}

let done = false;
function finish() {
  if (done) return;
  done = true;
  process.stdout.write(JSON.stringify({ probe: 'events', results }));
}

// setImmediate 回调后的最终输出；若无 setImmediate 支持则直接输出。
if (typeof setImmediate !== 'function') {
  results.captureRejections = 'no-setImmediate';
  finish();
}
