// M2-2/5 diff：Error cause 与 AbortSignal（abort/timeout/any/reason/throwIfAborted）。
const results = {};

// Error cause + name/message
const e = new Error('boom', { cause: new Error('root') });
results.errCause = e.cause && e.cause.message;
results.errName = e.name;
results.errMessage = e.message;

// 内置错误类 instanceof
try { null.x; } catch (te) { results.teName = te.name; }

// 系统错误：code/errno（路径片段归一化后比较）
try { require('node:fs').readFileSync('/nonexistent/zzz'); results.fs = 'no-throw'; }
catch (fe) {
  results.fs = JSON.stringify({ code: fe.code, errno: fe.errno, hasPath: fe.path !== undefined });
}

// AbortController / AbortSignal
const ac = new AbortController();
const s = ac.signal;
results.signalAborted = s.aborted;
ac.abort(new Error('stop'));
results.signalAborted2 = s.aborted;
results.signalReason2 = s.reason && s.reason.message;

// AbortSignal 静态方法
const t = AbortSignal.timeout(1);
results.timeoutIsSignal = t instanceof AbortSignal;
results.abortReason = String(AbortSignal.abort('x').reason);
results.anyIsSignal = AbortSignal.any([s, t]) instanceof AbortSignal;

// throwIfAborted
results.throwIfAborted = (() => { try { s.throwIfAborted(); return 'no-throw'; } catch (er) { return 'threw'; } })();

process.stdout.write(JSON.stringify(results));
