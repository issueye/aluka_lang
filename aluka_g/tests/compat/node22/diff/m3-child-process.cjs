// M3-5 diff：child_process spawn/exec/execFile/fork + sync 三件套。
// 子命令用 process.execPath（node 跑 node，aluka 跑 aluka），-e 兼容。
const cp = require('node:child_process');
const r = {};

// --- 表面 ---
r.surface = ['spawn', 'exec', 'execFile', 'fork', 'spawnSync', 'execFileSync', 'execSync']
  .filter((k) => typeof cp[k] === 'function').sort();

// --- spawn 事件流 ---
function spawnTest() {
  return new Promise((resolve) => {
    const child = cp.spawn(process.execPath, ['-e', 'console.log("hello from child")']);
    const evts = [];
    let data = '';
    r.spawnPid = typeof child.pid === 'number';
    r.spawnHasKill = typeof child.kill === 'function';
    r.spawnHasStdout = typeof child.stdout === 'object';
    child.stdout.on('data', (d) => { data += d.toString(); });
    child.on('spawn', () => evts.push('spawn'));
    child.on('exit', (code, sig) => evts.push('exit:' + code));
    child.on('close', (code, sig) => {
      evts.push('close:' + code);
      r.spawnData = data.trim();
      r.spawnEvents = evts;
      r.spawnStderrType = typeof child.stderr.on;
      resolve();
    });
  });
}

// --- exec / execFile 回调 ---
function execTest() {
  return new Promise((resolve) => {
    // 避免 cmd/sh 嵌套引号问题：用无引号命令；stderr 用重定向。
    cp.exec('echo hello-from-exec', (err, stdout, stderr) => {
      r.execErr = err ? String(err && err.message).slice(0, 30) : null;
      r.execStdout = stdout.trim();
      r.execStderr = stderr;
      // -e 代码用 console.log（返回 undefined，避免 aluka 的 -e 打印末表达式值）。
      cp.execFile(process.execPath, ['-e', 'console.log("file-out")'], (err2, so, se) => {
        r.execFileErr = err2 ? err2.code : null;
        r.execFileOut = so.trim();
        resolve();
      });
    });
  });
}

// --- sync 三件套 ---
function syncTests() {
  // spawnSync 基本
  const ok = cp.spawnSync(process.execPath, ['-e', 'console.log("sync-out")']);
  r.spawnSyncStatus = ok.status;
  r.spawnSyncOut = ok.stdout.toString().trim();
  r.spawnSyncHasPid = typeof ok.pid === 'number';

  // spawnSync 命令不存在 → error 属性（不抛）
  const bad = cp.spawnSync('aluka-no-such-cmd-xyz', []);
  r.spawnSyncBadErr = bad.error ? bad.error.code : null;
  r.spawnSyncBadStatus = bad.status;

  // spawnSync 非零退出 → status 反映，无 error
  const nz = cp.spawnSync(process.execPath, ['-e', 'process.exit(3)']);
  r.spawnSyncNzStatus = nz.status;
  r.spawnSyncNzError = nz.error ? nz.error.code : null;

  // execFileSync 成功（console.log 末尾，末表达式返回 undefined）
  const efs = cp.execFileSync(process.execPath, ['-e', 'console.log("efs-out")'], { encoding: 'utf8' });
  r.execFileSyncOut = efs.trim();

  // execFileSync 非零退出 → 抛错（status）
  try {
    cp.execFileSync(process.execPath, ['-e', 'process.exit(5)']);
    r.execFileSyncNz = 'no-throw';
  } catch (e) {
    r.execFileSyncNz = 'status=' + e.status;
  }

  // execSync 经 shell
  try {
    const es = cp.execSync('echo exec-sync-echo', { encoding: 'utf8' });
    r.execSyncOut = es.trim();
  } catch (e) {
    r.execSyncErr = String(e);
  }

  // execSync 非零退出 → 抛错（status）
  try {
    cp.execSync(process.execPath + ' -e "process.exit(2)"', { encoding: 'utf8' });
    r.execSyncNz = 'no-throw';
  } catch (e) {
    r.execSyncNz = 'status=' + e.status;
  }

  // spawnSync timeout
  const to = cp.spawnSync(process.execPath, ['-e', 'setTimeout(()=>{}, 5000)'], { timeout: 300 });
  r.spawnSyncTimeoutErr = to.error ? to.error.code : null;
  r.spawnSyncTimeoutStatus = to.status;
  r.spawnSyncTimeoutSignal = to.signal;
}

// --- fork（子进程打印 stdout；fork IPC 通道未实现，见 knownDifference） ---
function forkTest() {
  return new Promise((resolve) => {
    const fixture = require('node:path').join(__dirname, 'm3-fixtures', 'child-mod.cjs');
    const f = cp.fork(fixture);
    r.forkPid = typeof f.pid === 'number';
    r.forkHasKill = typeof f.kill === 'function';
    f.on('close', (code, sig) => {
      r.forkClose = code;
      resolve();
    });
  });
}

(async () => {
  try {
    await spawnTest();
    await execTest();
    syncTests();
    await forkTest();
  } catch (e) {
    r.fatal = String(e && e.message) + '|' + (e && e.code);
  }
  const out = {};
  for (const k of Object.keys(r).sort()) out[k] = r[k];
  process.stdout.write(JSON.stringify(out));
})().catch((e) => {
  process.stdout.write(JSON.stringify({ fatal: String(e && e.message) }));
});
