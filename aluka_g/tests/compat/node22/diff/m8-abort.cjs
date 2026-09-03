// M8-7 diff：AbortController / AbortSignal（timeout/any/reason/throwIfAborted）。
const r = {};

// AbortController 基础
const ctrl = new AbortController();
const sig = ctrl.signal;
r.acAborted0 = sig.aborted;
r.acReason0 = sig.reason;
ctrl.abort('my-reason');
r.acAborted = sig.aborted;
r.acReason = sig.reason;
r.acIsSignal = sig instanceof AbortSignal;
r.acIsAbortCtor = new AbortController().constructor === AbortController;

// abort 事件（onabort + addEventListener 只触发一次）
r.abortEvents = (() => {
  const c = new AbortController();
  let n = 0;
  c.signal.addEventListener('abort', () => n++);
  c.signal.addEventListener('abort', () => n++);
  c.abort();
  c.abort();
  return n;
})();
r.onabortCalled = (() => {
  const c = new AbortController();
  let n = 0;
  c.signal.onabort = () => n++;
  c.abort();
  c.abort();
  return n;
})();

// AbortSignal.abort(reason)
r.staticAbort = (() => {
  const s = AbortSignal.abort('boom');
  return s.aborted + '|' + s.reason;
})();
r.staticAbortDefault = (() => {
  const s = AbortSignal.abort();
  return s.aborted + '|' + (s.reason === undefined);
})();

// AbortSignal.any
r.anyAlready = (() => {
  const a = AbortSignal.abort('first');
  const b = new AbortController().signal;
  const combined = AbortSignal.any([a, b]);
  return combined.aborted + '|' + combined.reason;
})();
r.anyLater = (() => {
  const a = new AbortController();
  const b = new AbortController();
  const combined = AbortSignal.any([a.signal, b.signal]);
  a.abort('later');
  return combined.aborted + '|' + combined.reason;
})();
r.anyEmpty = (() => {
  const s = AbortSignal.any([]);
  return s.aborted;
})();

// throwIfAborted
r.throwIfOk = (() => {
  const s = new AbortController().signal;
  try { s.throwIfAborted(); return 'no-throw'; } catch (e) { return 'threw'; }
})();
r.throwIfAbort = (() => {
  const c = new AbortController();
  c.abort('stop');
  try { c.signal.throwIfAborted(); return 'no-throw'; } catch (e) { return 'threw:' + e.message; }
})();

// AbortSignal.timeout：短超时后 aborted，reason 为 TimeoutError 语义。
(async () => {
  const timed = AbortSignal.timeout(20);
  await new Promise((res) => setTimeout(res, 60));
  r.timeoutAborted = timed.aborted;
  const s = AbortSignal.timeout(5);
  const name = await new Promise((res) => {
    s.addEventListener('abort', () => res(s.reason ? s.reason.name : 'no-name'));
  });
  r.timeoutReasonName = name;
  const sorted = {};
  Object.keys(r).sort().forEach((k) => { sorted[k] = r[k]; });
  console.log(JSON.stringify(sorted));
})();
