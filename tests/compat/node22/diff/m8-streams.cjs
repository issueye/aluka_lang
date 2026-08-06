// M8-5 diff：ReadableStream / WritableStream / TransformStream / queuing
// strategies / 关联类全局 / Symbol.asyncIterator / 压缩流。
const r = {};

// ReadableStream 基础
const rs = new ReadableStream({ start(c) { c.enqueue('a'); c.enqueue('b'); c.close(); } });
r.rsInstance = rs instanceof ReadableStream;
const reader = rs.getReader();
r.readerHasRead = typeof reader.read;
Promise.all([reader.read(), reader.read(), reader.read()]).then(([a, b, c]) => {
  r.reads = [a.value, a.done, b.value, b.done, c.value, c.done].join('|');
});

// locked 面（node 语义：getReader 后 locked=true）
const rsLocked = new ReadableStream({ start(c) { c.enqueue(1); c.close(); } });
r.lockedBefore = rsLocked.locked;
const rl = rsLocked.getReader();
r.lockedAfter = rsLocked.locked;
rl.releaseLock();
r.lockedAfterRelease = rsLocked.locked;

// asyncIterator（for await）
const rs2 = new ReadableStream({ start(c) { c.enqueue('x'); c.enqueue('y'); c.close(); } });
(async () => {
  const out = [];
  for await (const ch of rs2) out.push(ch);
  r.forAwait = out.join(',');
})();

// WritableStream writer
const wChunks = [];
const ws = new WritableStream({
  write(chunk) { wChunks.push(chunk); },
  close() { r.writerCloseCalled = true; },
});
const writer = ws.getWriter();
writer.write('p1').then(() => writer.write('p2')).then(() => writer.close()).then(() => {
  r.writerChunks = wChunks.join(',');
});

// TransformStream
const ts = new TransformStream({ transform(chunk, c) { c.enqueue(chunk.toUpperCase()); } });
const tw = ts.writable.getWriter();
tw.write('ab').then(() => tw.close());
ts.readable.getReader().read().then(({ value }) => { r.transform = value; });

// Queuing strategies
const bqs = new ByteLengthQueuingStrategy({ highWaterMark: 10 });
r.bqsHwm = bqs.highWaterMark;
r.bqsSizeFn = typeof bqs.size;
r.bqsSize = bqs.size(new Uint8Array([1, 2, 3]));
const cqs = new CountQueuingStrategy({ highWaterMark: 5 });
r.cqsHwm = cqs.highWaterMark;
r.cqsSize = cqs.size();

// 关联类全局
['ReadableStreamDefaultReader', 'ReadableStreamBYOBReader', 'ReadableByteStreamController',
  'ReadableStreamDefaultController', 'TransformStreamDefaultController',
  'WritableStreamDefaultWriter', 'WritableStreamDefaultController',
  'ByteLengthQueuingStrategy', 'CountQueuingStrategy', 'TextEncoderStream',
  'TextDecoderStream', 'CompressionStream', 'DecompressionStream'].forEach((n) => {
  r[n] = typeof globalThis[n];
});

// 压缩流往返（gzip）
async function drainAll(reader) {
  const parts = [];
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    parts.push(value);
  }
  return Buffer.concat(parts.map((v) => Buffer.from(v)));
}
(async () => {
  const cs = new CompressionStream('gzip');
  const cw = cs.writable.getWriter();
  await cw.write('hello hello hello');
  await cw.close();
  const compressed = await drainAll(cs.readable.getReader());
  r.compressedLenGT10 = compressed.length > 10;
  r.gzipMagic = compressed[0] === 0x1f && compressed[1] === 0x8b;

  const ds = new DecompressionStream('gzip');
  const dw = ds.writable.getWriter();
  await dw.write(compressed);
  await dw.close();
  const decompressed = await drainAll(ds.readable.getReader());
  r.decompressed = Buffer.from(decompressed).toString();
})();

setTimeout(() => {
  const sorted = {};
  Object.keys(r).sort().forEach((k) => { sorted[k] = r[k]; });
  console.log(JSON.stringify(sorted));
}, 150);
