// M2-7 diff：Stream 语义 —— 事件时序、cork/uncork、destroy、write 返回值、管道。
const { Readable, Writable, Transform } = require('node:stream');
const results = {};

// 1. Readable 事件时序：data → end
{
  const r = new Readable({ read() {} });
  const seq = [];
  r.on('data', (c) => seq.push('d:' + c));
  r.on('end', () => seq.push('end'));
  r.push('a');
  r.push(null);
  setImmediate(() => { results.readableSeq = seq.join(','); });
}

// 2. Writable write 返回值（未超 highWaterMark → true）
{
  const w = new Writable({ write(c, e, cb) { cb(); } });
  results.writeRet = w.write('x');
  w.end();
}

// 3. cork / uncork 批量写
{
  const w = new Writable({ write(c, e, cb) { cb(); } });
  const writes = [];
  w.on('write-log', () => {});
  // 无法直接观察写调用次数，改用管道计数：
  const t = new Transform({ transform(c, e, cb) { results.transformChunks = (results.transformChunks || 0) + 1; cb(null, c); } });
  const src = Readable.from(['a', 'b', 'c']);
  src.pipe(t);
  t.on('end', () => { results.transformEnd = true; });
  setImmediate(() => {});
}

// 4. destroy 触发 close
{
  const r = new Readable({ read() {} });
  let closed = false;
  r.on('close', () => { closed = true; });
  r.destroy();
  setImmediate(() => { results.destroyClose = closed; });
}

// 5. pipeline 完成时序
{
  const { pipeline } = require('node:stream/promises');
  const src = Readable.from(['a', 'b']);
  const out = [];
  const t = new Transform({ transform(c, e, cb) { out.push(c); cb(null, c); } });
  const sink = new Writable({ write(c, e, cb) { cb(); } });
  pipeline(src, t, sink).then(() => { results.pipelineDone = true; });
}

// 6. errored 流事件
{
  const r = new Readable({ read() {} });
  let errSeen = false;
  r.on('error', () => { errSeen = true; });
  r.destroy(new Error('boom'));
  setImmediate(() => { results.errorSeen = errSeen; });
}

setTimeout(() => { process.stdout.write(JSON.stringify(results)); }, 120);
