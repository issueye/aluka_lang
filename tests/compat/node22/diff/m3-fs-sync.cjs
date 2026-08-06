// M3-1a diff：node:fs 同步面 + 错误码 + Stats/Dirent。
// 输出归一化 JSON 单行；路径经 fs.realpathSync 归一化避免 Windows/Linux 差异。
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const results = {};

// surface：关键 API 面（完整导出集合存在大量未实现项，由 gaps.md 跟踪）。
const keyAPIs = ['readFileSync', 'writeFileSync', 'appendFileSync', 'statSync', 'lstatSync',
  'mkdirSync', 'rmdirSync', 'rmSync', 'readdirSync', 'unlinkSync', 'renameSync',
  'copyFileSync', 'realpathSync', 'mkdtempSync', 'globSync', 'cpSync', 'truncateSync',
  'readFile', 'writeFile', 'appendFile', 'stat', 'lstat', 'mkdir', 'readdir', 'unlink',
  'rmdir', 'rm', 'rename', 'copyFile', 'realpath', 'mkdtemp', 'access', 'exists',
  'truncate', 'watch', 'glob', 'cp', 'createReadStream', 'createWriteStream'];
results.surface = keyAPIs.filter((k) => typeof fs[k] === 'function');
results.missing = keyAPIs.filter((k) => typeof fs[k] !== 'function');
results.promisesPresent = typeof fs.promises === 'object' && typeof fs.promises.readFile === 'function';

// 用临时目录做文件操作（输出只保留相对路径基名，避免平台差异）。
const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'm3-fs-'));
const file = path.join(dir, 'a.txt');
const sub = path.join(dir, 'sub');
const relFile = path.basename(file);

try {
  // 写读
  fs.writeFileSync(file, 'hello');
  results.read = fs.readFileSync(file, 'utf8');
  results.readBuffer = Buffer.isBuffer(fs.readFileSync(file));
  fs.appendFileSync(file, ' world');
  results.append = fs.readFileSync(file, 'utf8');

  // stat
  const st = fs.statSync(file);
  results.statKeys = ['size', 'mode', 'uid', 'gid', 'atimeMs', 'mtimeMs', 'ctimeMs', 'birthtimeMs']
    .filter((k) => typeof st[k] !== 'undefined').sort();
  results.statSize = st.size;
  results.statIsFile = st.isFile();
  results.statIsDir = st.isDirectory();
  results.statIsSymlink = st.isSymbolicLink();
  results.statModeType = (st.mode & 0o170000) >>> 0;

  // lstat 同文件
  const ls = fs.lstatSync(file);
  results.lstatIsFile = ls.isFile();

  // mkdir / rmdir / readdir
  fs.mkdirSync(sub);
  fs.mkdirSync(path.join(sub, 'nested'), { recursive: true });
  fs.writeFileSync(path.join(sub, 'x.txt'), 'x');
  const entries = fs.readdirSync(sub);
  results.readdir = entries.sort();
  const dirents = fs.readdirSync(sub, { withFileTypes: true });
  results.dirent = dirents.map((d) => d.name + ':' + d.isFile() + ':' + d.isDirectory()).sort();

  // rename / copyFile / unlink
  const moved = path.join(dir, 'b.txt');
  fs.renameSync(file, moved);
  fs.copyFileSync(moved, file);
  fs.unlinkSync(moved);
  results.afterRenameCopy = fs.readFileSync(file, 'utf8');

  // existsSync
  results.exists = fs.existsSync(file);
  results.existsMissing = fs.existsSync(path.join(dir, 'nope.txt'));

  // realpathSync 返回绝对路径
  const rp = fs.realpathSync(file);
  results.realpathAbs = path.isAbsolute(rp) && path.basename(rp) === 'a.txt';

  // 错误码：ENOENT
  try { fs.readFileSync(path.join(dir, 'missing.txt')); results.errNoent = 'no-throw'; }
  catch (e) { results.errNoent = e.code + '|' + (e.errno < 0) + '|' + e.syscall; }
  // 错误码：EISDIR（读目录）
  try { fs.readFileSync(sub); results.errIsdir = 'no-throw'; }
  catch (e) { results.errIsdir = e.code; }
  // 错误码：EEXIST（mkdir 已存在）
  try { fs.mkdirSync(sub); results.errExist = 'no-throw'; }
  catch (e) { results.errExist = e.code; }
  // 错误码：ENOTEMPTY（rmdir 非空目录）
  fs.mkdirSync(path.join(dir, 'full'));
  fs.writeFileSync(path.join(dir, 'full', 'f.txt'), '');
  try { fs.rmdirSync(path.join(dir, 'full')); results.errNotempty = 'no-throw'; }
  catch (e) { results.errNotempty = e.code; }
  // rmSync recursive
  fs.rmSync(path.join(dir, 'full'), { recursive: true });
  results.rmRecursive = !fs.existsSync(path.join(dir, 'full'));

  // globSync
  fs.mkdirSync(path.join(dir, 'globd'));
  fs.writeFileSync(path.join(dir, 'globd', 'one.js'), '');
  fs.writeFileSync(path.join(dir, 'globd', 'two.ts'), '');
  fs.writeFileSync(path.join(dir, 'globd', 'three.txt'), '');
  const globRes = fs.globSync('*.js', { cwd: path.join(dir, 'globd') });
  results.glob = globRes.sort();

  // cpSync（递归目录）
  fs.cpSync(path.join(dir, 'globd'), path.join(dir, 'copyd'), { recursive: true });
  results.cpSync = fs.readdirSync(path.join(dir, 'copyd')).sort();
  // cpSync 非递归目录 → EISDIR
  try { fs.cpSync(path.join(dir, 'globd'), path.join(dir, 'copyd2')); results.cpNoRec = 'no-throw'; }
  catch (e) { results.cpNoRec = e.code; }
} catch (e) {
  results.fatal = String(e && e.message);
} finally {
  try { fs.rmSync(dir, { recursive: true, force: true }); } catch (e) {}
}

process.stdout.write(JSON.stringify(results));
