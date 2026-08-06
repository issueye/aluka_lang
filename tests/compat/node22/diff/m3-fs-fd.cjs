// M3-1c diff：fs fd 操作 + FileHandle + link/symlink/readlink + Stats 时间对象。
const fs = require('node:fs');
const fsp = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const results = {};

const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'm3-fd-'));
const file = path.join(dir, 'a.txt');
const sub = path.join(dir, 'sub');

fs.writeFileSync(file, 'hello world');

try {
  // --- fd 三件套 ---
  const fd = fs.openSync(file, 'r');
  results.openFdType = typeof fd;
  const buf = Buffer.alloc(16);
  const n = fs.readSync(fd, buf, 0, 5, 0);
  results.readSync = n + ':' + buf.toString('utf8', 0, n);
  results.fstatIsFile = fs.fstatSync(fd).isFile();
  fs.closeSync(fd);

  // 写模式
  const wfd = fs.openSync(file, 'w');
  results.writeSync = fs.writeSync(wfd, Buffer.from('xyz'));
  fs.writeSync(wfd, 'AB', 1); // position=1 处写
  fs.closeSync(wfd);
  results.afterWrite = fs.readFileSync(file, 'utf8');

  // ftruncate + truncate
  const tfd = fs.openSync(file, 'r+');
  fs.ftruncateSync(tfd, 2);
  fs.closeSync(tfd);
  results.afterTruncate = fs.readFileSync(file, 'utf8') + '|' + fs.statSync(file).size;
  fs.truncateSync(file, 1);
  results.afterTruncate2 = fs.readFileSync(file, 'utf8');

  // 错误码：EBADF
  const cfd = fs.openSync(file, 'r');
  fs.closeSync(cfd);
  try { fs.readSync(cfd, Buffer.alloc(4), 0, 4, 0); results.errBadf = 'no-throw'; }
  catch (e) { results.errBadf = e.code; }

  // --- link / symlink / readlink ---
  fs.linkSync(file, path.join(dir, 'hard.txt'));
  results.hardExists = fs.existsSync(path.join(dir, 'hard.txt'));
  const target = path.join(dir, 'symtarget.txt');
  fs.writeFileSync(target, 't');
  fs.symlinkSync(target, path.join(dir, 'sym.txt'));
  results.readlinkAbs = path.isAbsolute(fs.readlinkSync(path.join(dir, 'sym.txt')));
  results.lstatSymlink = fs.lstatSync(path.join(dir, 'sym.txt')).isSymbolicLink();

  // --- Stats 时间字段 ---
  const st = fs.statSync(file);
  results.mtimeIsDate = st.mtime instanceof Date;
  results.birthtimeIsDate = st.birthtime instanceof Date;
  results.mtimeMsNum = typeof st.mtimeMs === 'number';
  // node 在 Windows 上 mtime.getTime()（整毫秒）与 mtimeMs（含小数）略有差异。
  results.mtimeClose = Math.abs(st.mtime.getTime() - st.mtimeMs) < 2;
  results.uidGid = st.uid + '|' + st.gid;
  results.size = st.size;
  results.modeType = (st.mode & 0o170000) >>> 0;

  // utimes
  fs.utimesSync(file, new Date(0), new Date(0));
  results.utimesMtime0 = fs.statSync(file).mtimeMs === 0;

  // --- opendir / Dir ---
  fs.mkdirSync(sub);
  fs.writeFileSync(path.join(sub, 'x.txt'), 'x');
  fs.writeFileSync(path.join(sub, 'y.txt'), 'y');
  const d = fs.opendirSync(sub);
  const names = [];
  let ent;
  while ((ent = d.readSync()) !== null) names.push(ent.name + ':' + ent.isFile());
  results.dirReadSync = names.sort();
  results.dirPath = path.basename(d.path);
  d.closeSync();

  // --- statfs ---
  const sf = fs.statfsSync(file);
  results.statfsKeys = Object.keys(sf).sort();
  results.statfsBsize = sf.bsize;
} catch (e) {
  results.fatal = String(e && e.message) + '|' + (e && e.code);
}

function awaitCb(file, results) {
  return new Promise((resolve) => {
    const fd = fs.openSync(file, 'w');
    fs.write(fd, Buffer.from('callback-write'), 0, 14, 0, (err, written, wbuf) => {
      results.cbWriteErr = err ? err.code : null;
      results.cbWritten = written;
      results.cbWrittenBuf = Buffer.isBuffer(wbuf) ? wbuf.length : typeof wbuf;
      fs.close(fd, (err2) => {
        results.cbCloseErr = err2 ? err2.code : null;
        results.cbFinal = fs.readFileSync(file, 'utf8');
        resolve();
      });
    });
  });
}

async function runFileHandle(file, results) {
  const fh = await fsp.open(file, 'r');
  results.fhFd = typeof fh.fd;
  results.fhStatIsFile = (await fh.stat()).isFile();
  const r = await fh.read(Buffer.alloc(100), 0, 100, 0);
  results.fhRead = r.bytesRead + ':' + r.buffer.toString('utf8', 0, r.bytesRead);
  results.fhReadFile = (await fh.readFile('utf8')).length > 0;
  await fh.close();
  results.fhClosed = true;
}

(async () => {
  try {
    await awaitCb(file, results);
    await runFileHandle(file, results);
  } catch (e) {
    results.asyncFatal = String(e && e.message) + '|' + (e && e.code);
  }
  try { fs.rmSync(dir, { recursive: true, force: true }); } catch (e) {}
  const out = {};
  for (const k of Object.keys(results).sort()) out[k] = results[k];
  process.stdout.write(JSON.stringify(out));
})().catch((e) => {
  process.stdout.write(JSON.stringify({ fatal: String(e && e.message) + '|' + (e && e.code) }));
});
