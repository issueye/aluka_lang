// M9-3: node:wasi —— 实验性（stability 1）WASI preview1 方法面。
// aluka 无 WebAssembly 运行时，仅校验类/方法面与错误路径（与 Node 对齐）。
// Node 加载 wasi 时输出 ExperimentalWarning（含 --trace-warnings 提示行，
// run-diff.sh 只过滤 DeprecationWarning），先移除 'warning' 默认监听器静默之。
process.removeAllListeners('warning');
const wasi = require('wasi');
const { WASI } = wasi;
const out = [];

out.push('result: keys:' + Object.keys(wasi).sort().join(','));
out.push('result: type:' + typeof WASI);
const w = new WASI({ version: 'preview1' });
out.push('result: instanceOf:' + (w instanceof WASI));
out.push('result: methods:' + ['start', 'initialize', 'getImportObject'].map(m => typeof w[m]).join(','));
out.push('result: wasiImport-type:' + typeof w.wasiImport);
const io = w.getImportObject();
out.push('result: io-keys:' + Object.keys(io).sort().join(','));
const pv = io.wasi_snapshot_preview1;
out.push('result: pv-count:' + Object.keys(pv).length);
out.push('result: pv-fn:' + typeof pv.args_get + ',' + typeof pv.fd_write + ',' + typeof pv.proc_exit + ',' + typeof pv.random_get);

// unstable 版本：同一函数面，不同导入键。
const wu = new WASI({ version: 'unstable' });
out.push('result: unstable-key:' + Object.keys(wu.getImportObject()).join(','));
out.push('result: unstable-count:' + Object.keys(wu.getImportObject().wasi_unstable).length);

// 失败用例：错误码与消息对齐 Node。
function tryErr(name, fn) {
  try { fn(); out.push('result: ' + name + ':NO-THROW'); }
  catch (e) { out.push('result: ' + name + ':' + e.name + ':' + e.code + ':' + e.message); }
}
tryErr('bad-version', () => new WASI({ version: 'preview2' }));
tryErr('no-version', () => new WASI({}));
tryErr('bad-args', () => new WASI({ version: 'preview1', args: 'x' }));
tryErr('bad-env', () => new WASI({ version: 'preview1', env: 'x' }));
tryErr('bad-preopens', () => new WASI({ version: 'preview1', preopens: 'x' }));
tryErr('bad-stdin', () => new WASI({ version: 'preview1', stdin: -1 }));
tryErr('start-empty', () => w.start({}));
tryErr('start-exports-empty', () => new WASI({ version: 'preview1' }).start({ exports: {} }));
tryErr('init-exports-empty', () => new WASI({ version: 'preview1' }).initialize({ exports: {} }));
tryErr('call-before-start', () => w.getImportObject().wasi_snapshot_preview1.fd_write(1, 0, 1, 0));
tryErr('call-args-before-start', () => w.getImportObject().wasi_snapshot_preview1.args_get(0, 0));

// start 标记：第一次 start 校验失败后，第二次调用抛 ERR_WASI_ALREADY_STARTED。
const w2 = new WASI({ version: 'preview1' });
tryErr('start-first', () => w2.start({}));
tryErr('start-second', () => w2.start({ exports: { _start() {} } }));

console.log(out.join('\n'));
