// M8-3 diff：Blob / File / 对象 URL。
const r = {};

const b = new Blob(['hello', ' ', 'world'], { type: 'text/plain' });
r.bSize = b.size;
r.bType = b.type;
r.bIsInstance = b instanceof Blob;
r.bCtor = b.constructor === Blob;
r.bEmpty = new Blob().size;
r.bFromBuffer = new Blob([Buffer.from('ab')]).size;

// slice（含负数语义）
const s = b.slice(0, 5, 'text/x');
r.sliceSize = s.size;
r.sliceType = s.type;
r.sliceNeg = b.slice(-6).size;
r.sliceAll = b.slice().size;

// File
const f = new File(['abc'], 'name.txt', { type: 'text/plain', lastModified: 1234567 });
r.fName = f.name;
r.fSize = f.size;
r.fType = f.type;
r.fLastModified = f.lastModified;
r.fIsBlob = f instanceof Blob;
r.fIsFile = f instanceof File;
r.fDefaultLMType = typeof new File(['x'], 'n').lastModified;

// 对象 URL
r.createObjectURL = typeof URL.createObjectURL;
r.revokeObjectURL = typeof URL.revokeObjectURL;
if (typeof URL.createObjectURL === 'function') {
  const u = URL.createObjectURL(b);
  r.objectURLPrefix = u.slice(0, 5);
  r.objectURLForm = /^blob:nodedata:/.test(u);
  URL.revokeObjectURL(u);
  r.revokeNoThrow = true;
}

// async：text / arrayBuffer / bytes / stream
Promise.all([b.text(), b.arrayBuffer(), b.bytes()]).then(([t, ab, by]) => {
  r.asyncText = t;
  r.asyncABLen = ab.byteLength;
  r.asyncBytesLen = by.length;
  return b.stream().getReader().read();
}).then(({ value, done }) => {
  r.streamDone = done;
  r.streamChunkLen = value.length;
  const sorted = {};
  Object.keys(r).sort().forEach((k) => { sorted[k] = r[k]; });
  console.log(JSON.stringify(sorted));
});
