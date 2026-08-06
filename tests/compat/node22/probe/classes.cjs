// probe/classes.cjs — M0 类/原型/Symbol 探针：
// 关键类构造器身份、静态方法、原型方法、Symbol 协议（toStringTag/iterator/asyncIterator/dispose）。
// 双跑输出归一化 JSON 单行。
'use strict';

const CLASSES = [
  'EventEmitter', 'Buffer', 'URL', 'URLSearchParams', 'URLPattern',
  'AbortController', 'AbortSignal',
  'Blob', 'File', 'TextEncoder', 'TextDecoder',
  'ReadableStream', 'WritableStream', 'TransformStream',
  'Event', 'EventTarget', 'CustomEvent', 'MessageEvent', 'CloseEvent',
  'MessageChannel', 'MessagePort', 'BroadcastChannel',
  'Headers', 'Request', 'Response', 'FormData',
  'DOMException', 'WebSocket', 'Performance', 'PerformanceObserver',
  'Navigator', 'Crypto', 'SubtleCrypto', 'CryptoKey', 'AssertionError',
];

const SYMBOLS = ['toStringTag', 'iterator', 'asyncIterator', 'dispose', 'hasInstance'];

function ownNames(obj) {
  return Object.getOwnPropertyNames(obj).filter((n) => !n.startsWith('_'));
}

function probeSymbols(proto) {
  const out = {};
  for (const s of SYMBOLS) {
    const key = Symbol[s];
    if (!key) continue;
    try {
      out[s] = proto != null && key in Object(proto);
    } catch (e) {
      out[s] = false;
    }
  }
  return out;
}

function probeClass(name) {
  const out = { present: name in globalThis };
  if (!out.present) return out;
  let ctor;
  try { ctor = globalThis[name]; } catch (e) { out.error = String(e); return out; }
  out.type = typeof ctor;
  if (typeof ctor !== 'function') return out;
  out.name = ctor.name;
  out.length = ctor.length;
  out.statics = ownNames(ctor);
  const proto = ctor.prototype;
  if (proto && typeof proto === 'object') {
    out.protoMethods = ownNames(proto).filter((n) => n !== 'constructor');
    out.symbols = probeSymbols(proto);
    try {
      out.toStringTag = proto[Symbol.toStringTag] !== undefined ? String(proto[Symbol.toStringTag]) : null;
    } catch (e) { out.toStringTag = null; }
  }
  return out;
}

function main() {
  const classes = {};
  for (const name of CLASSES) {
    classes[name] = probeClass(name);
  }
  process.stdout.write(JSON.stringify({ probe: 'classes', classes }));
}

main();
