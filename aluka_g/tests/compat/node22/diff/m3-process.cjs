// M3-4 diff：process 方法面与事件。
// 注意：process.exit(0) 在末尾触发 'exit' 事件；emitWarning 使用
// DeprecationWarning 类型（stderr 行被 run-diff 归一化过滤）。
const r = {};

// --- 表面 ---
r.surface = ['argv', 'argv0', 'env', 'execPath', 'pid', 'ppid', 'platform',
  'arch', 'version', 'versions', 'cwd', 'chdir', 'exit', 'nextTick',
  'hrtime', 'uptime', 'memoryUsage', 'cpuUsage', 'umask', 'kill',
  'stdin', 'stdout', 'stderr', 'on', 'once', 'emit', 'removeListener',
  'emitWarning', 'exitCode', 'abort', 'execArgv', 'features', 'release', 'config']
  .filter((k) => typeof process[k] !== 'undefined').sort();
r.missing = ['argv', 'argv0', 'env', 'execPath', 'pid', 'ppid', 'platform',
  'arch', 'version', 'versions', 'cwd', 'chdir', 'exit', 'nextTick',
  'hrtime', 'uptime', 'memoryUsage', 'cpuUsage', 'umask', 'kill',
  'stdin', 'stdout', 'stderr', 'on', 'once', 'emit', 'removeListener',
  'emitWarning', 'exitCode', 'abort', 'execArgv', 'features', 'release', 'config']
  .filter((k) => typeof process[k] === 'undefined').sort();

r.platform = process.platform;
r.arch = process.arch;
r.pidNum = typeof process.pid === 'number';
r.ppidNum = typeof process.ppid === 'number';
r.argvLen = process.argv.length;
r.argv1type = typeof process.argv[1];
r.cwdType = typeof process.cwd() === 'string';
r.envIsObj = typeof process.env === 'object';
r.envHasPath = typeof process.env.PATH === 'string';
process.env.ALUKA_TEST = 'set';
r.envSet = process.env.ALUKA_TEST;

// --- exitCode ---
r.exitCodeDefault = process.exitCode;
process.exitCode = 7;
r.exitCodeGet = process.exitCode;
process.exitCode = undefined;

// --- memoryUsage / cpuUsage / hrtime / umask ---
r.memKeys = Object.keys(process.memoryUsage()).sort();
r.cpuKeys = Object.keys(process.cpuUsage()).sort();
r.hrtimeLen = process.hrtime().length;
r.hrtimeNum = typeof process.hrtime()[0] === 'number';
r.hrtimeBigint = typeof process.hrtime.bigint === 'function';
r.umask = process.umask();

// --- nextTick / microtask / setImmediate 顺序 ---
const order = [];
process.nextTick(() => order.push('nexttick'));
Promise.resolve().then(() => order.push('promise'));
setImmediate(() => order.push('immediate'));

// --- warning 事件（removeAllListeners 移除默认 stderr 监听器后捕获） ---
let warned = null;
process.removeAllListeners('warning');
process.on('warning', (w) => {
  warned = { message: w.message, name: w.name, code: w.code };
});
process.emitWarning('my warning message', 'MyType', 'MYCODE1');
r.warned = warned;

// --- unhandledRejection 事件 ---
let unhandled = null;
process.on('unhandledRejection', (reason) => {
  unhandled = { msg: String(reason && reason.message), isErr: reason instanceof Error };
});
Promise.reject(new Error('boom'));

// --- 'exit' 事件（process.exit 触发） ---
process.on('exit', (code) => {
  process.stdout.write('EXIT-EVENT:' + code + '\n');
});

setImmediate(() => {
  r.nextTickOrder = JSON.stringify(order); // ['nexttick','promise','immediate']
  r.unhandled = unhandled;
  const out = {};
  for (const k of Object.keys(r).sort()) out[k] = r[k];
  process.stdout.write(JSON.stringify(out) + '\n');
  process.exit(0);
});
