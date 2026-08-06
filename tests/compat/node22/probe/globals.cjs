// probe/globals.cjs — M0 全局对象探针：计划 §4.1 所列全局/Web API 的存在性与身份。
// 双跑输出归一化 JSON 单行。键固定、逐项独立探测，避免一个异常中断全部。
'use strict';

// 计划 §4.1 的全局与 Web API 清单。
const GLOBALS = [
  // 基础全局
  'globalThis', 'global', 'process', 'console', 'Buffer',
  '__dirname', '__filename', 'require', 'module', 'exports',
  // 计时与任务
  'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval',
  'setImmediate', 'clearImmediate', 'queueMicrotask',
  // Abort
  'AbortController', 'AbortSignal',
  // URL/编码
  'URL', 'URLSearchParams', 'URLPattern',
  'TextEncoder', 'TextDecoder', 'TextEncoderStream', 'TextDecoderStream',
  'CompressionStream', 'DecompressionStream', 'atob', 'btoa',
  // Fetch
  'fetch', 'Headers', 'Request', 'Response', 'FormData',
  // Blob/File
  'Blob', 'File',
  // Web Crypto
  'crypto', 'Crypto', 'SubtleCrypto', 'CryptoKey',
  // Web Streams
  'ReadableStream', 'WritableStream', 'TransformStream',
  'ReadableStreamDefaultReader', 'ReadableStreamDefaultController',
  'ReadableByteStreamController', 'ReadableStreamBYOBReader',
  'WritableStreamDefaultWriter', 'WritableStreamDefaultController',
  'TransformStreamDefaultController',
  'ByteLengthQueuingStrategy', 'CountQueuingStrategy',
  // Events/Messaging
  'Event', 'EventTarget', 'CustomEvent', 'MessageEvent', 'CloseEvent',
  'MessageChannel', 'MessagePort', 'BroadcastChannel', 'structuredClone',
  // Performance
  'performance', 'Performance', 'PerformanceObserver', 'PerformanceMark',
  'PerformanceMeasure', 'PerformanceResourceTiming',
  // Networking / 环境
  'WebSocket', 'navigator',
  // DOM 错误
  'DOMException',
  // 实验
  'scheduler', 'sessionstorage', 'localstorage',
];

// CJS 包装器作用域变量（不在 globalThis 上，但属于"基础全局"合同）。
function cjsScope() {
  return {
    __dirname: typeof __dirname !== 'undefined' ? __dirname : undefined,
    __filename: typeof __filename !== 'undefined' ? __filename : undefined,
    require: typeof require !== 'undefined' ? require : undefined,
    module: typeof module !== 'undefined' ? module : undefined,
    exports: typeof exports !== 'undefined' ? exports : undefined,
  };
}

function probeGlobal(name, scope) {
  let v = scope[name];
  let present = v !== undefined;
  if (!present && Object.prototype.hasOwnProperty.call(globalThis, name)) {
    present = true;
    v = globalThis[name];
  }
  const out = { present };
  if (!out.present) return out;
  out.type = typeof v;
  if (typeof v === 'function') {
    out.length = v.length;
    out.name = v.name;
  }
  return out;
}

function main() {
  const scope = cjsScope();
  const globals = {};
  for (const name of GLOBALS) {
    globals[name] = probeGlobal(name, scope);
  }
  process.stdout.write(JSON.stringify({ probe: 'globals', globals }));
}

main();
