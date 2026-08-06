// M3-1b diff：node:fs 回调版 + node:fs/promises 面。
// 输出归一化 JSON 单行；路径归一化避免平台差异。
const fs = require('node:fs');
const fsp = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const results = {};

// 1. 回调版 API
function cbTest() {
  return new Promise((resolve) => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'm3-cb-'));
    const file = path.join(dir, 'cb.txt');
    let done = 0;
    const maybe = () => { if (++done >= 6) resolve(); };
    fs.writeFile(file, 'cb-data', (err) => {
      results.cbWriteErr = err ? err.code : null;
      fs.readFile(file, 'utf8', (err2, data) => { results.cbRead = err2 ? 'err' : data; maybe(); });
      fs.stat(file, (err2, st) => { results.cbStat = err2 ? 'err' : (st.isFile() + '|' + st.size); maybe(); });
      maybe();
    });
    fs.mkdir(path.join(dir, 'd'), (err) => {
      results.cbMkdirErr = err ? err.code : null;
      fs.readdir(path.join(dir, 'd'), (err2, entries) => { results.cbReaddir = err2 ? 'err' : JSON.stringify(entries); maybe(); });
      maybe();
    });
    fs.readFile(path.join(dir, 'missing.txt'), (err) => { results.cbReadErr = err ? err.code : null; maybe(); });
  });
}

// 2. promises API
async function pTest() {
  const dir = await fsp.mkdtemp(path.join(os.tmpdir(), 'm3-pr-'));
  const file = path.join(dir, 'p.txt');
  await fsp.writeFile(file, 'promise-data');
  const data = await fsp.readFile(file, 'utf8');
  results.pRead = data;
  const st = await fsp.stat(file);
  results.pStat = st.isFile() + '|' + st.size;
  const buf = await fsp.readFile(file);
  results.pBuf = Buffer.isBuffer(buf);
  results.pAppend = (await (async () => {
    await fsp.appendFile(file, '-x');
    return fsp.readFile(file, 'utf8');
  })());
  const sub = path.join(dir, 'sub');
  await fsp.mkdir(sub);
  await fsp.writeFile(path.join(sub, 'a.txt'), 'a');
  const entries = await fsp.readdir(sub);
  results.pReaddir = entries.sort();
  await fsp.rm(dir, { recursive: true, force: true });
  results.pRm = !fs.existsSync(dir);
  // 错误 reject：ENOENT
  try { await fsp.readFile(path.join(dir, 'nope')); results.pErr = 'no-reject'; }
  catch (e) { results.pErr = e.code; }
  // identity：fs.promises === require('node:fs/promises')
  results.identity = fs.promises === fsp;
  return;
}

(async () => {
  await cbTest();
  await pTest();
  const out = {};
  for (const k of Object.keys(results).sort()) out[k] = results[k];
  process.stdout.write(JSON.stringify(out));
})().catch((e) => {
  process.stdout.write(JSON.stringify({ fatal: String(e && e.message) }));
});
