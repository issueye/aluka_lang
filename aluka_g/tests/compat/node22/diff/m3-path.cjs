// M3-2 diff：node:path 身份（posix/win32 子对象 === 独立模块）+ 行为。
const path = require('node:path');
const r = {};

// 身份：path.posix === require('node:path/posix')（Node 语义）。
r.posixIdent = path.posix === require('node:path/posix');
r.win32Ident = path.win32 === require('node:path/win32');
r.delimiter = path.delimiter;
r.sep = path.sep;

// 方法面。
const methods = ['resolve', 'normalize', 'isAbsolute', 'join', 'relative',
  'parse', 'format', 'basename', 'dirname', 'extname', 'matchesGlob', 'toNamespacedPath'];
r.surface = methods.filter((k) => typeof path[k] === 'function').sort();
r.posixSurface = methods.filter((k) => typeof path.posix[k] === 'function').sort();
r.win32Surface = methods.filter((k) => typeof path.win32[k] === 'function').sort();

// POSIX 行为（固定，平台无关）。
const p = path.posix;
r.pSep = p.sep;
r.pDelimiter = p.delimiter;
r.pJoin = p.join('a', 'b', 'c');
r.pJoinAbs = p.join('/a', '/b', '../c');
r.pNormalize = p.normalize('/a//b/../c/./d');
r.pResolve = p.resolve('/foo/bar', './baz');
r.pIsAbs = p.isAbsolute('/a') + '|' + p.isAbsolute('a/');
r.pDirname = p.dirname('/a/b/c.txt');
r.pBasename = p.basename('/a/b/c.txt');
r.pBasenameExt = p.basename('/a/b/c.txt', '.txt');
r.pExtname = p.extname('/a/b/c.txt');
r.pExtnameNoExt = p.extname('/a/b/c');
r.pExtnameDotfile = p.extname('/a/b/.bashrc');
r.pExtnameMulti = p.extname('/a/b/c.tar.gz');
r.pRel = p.relative('/data/orandea/test/aaa', '/data/orandea/impl/bbb');
r.pParse = JSON.stringify(p.parse('/home/user/dir/file.txt'));
r.pFormat = p.format({ root: '/', dir: '/home/user', base: 'file.txt' });
r.pFormat2 = p.format({ dir: '/a/b', name: 'x', ext: '.js' });
r.pMatchesGlob = p.matchesGlob('/foo/bar.js', '/foo/*.js');
r.pMatchesGlobNo = p.matchesGlob('/foo/bar.js', '*.ts');
r.pExtnameWinSep = p.extname('C:\\a\\b.txt'); // posix 不认反斜杠

// Win32 行为（固定）。
const w = path.win32;
r.wSep = w.sep;
r.wDelimiter = w.delimiter;
r.wJoin = w.join('a', 'b', 'c');
r.wJoinAbs = w.join('C:\\a', 'b', '..\\c');
r.wNormalize = w.normalize('C:\\\\a\\\\b\\\\..\\\\c');
r.wResolve = w.resolve('C:\\foo\\bar', '.\\baz');
r.wIsAbs = w.isAbsolute('C:\\a') + '|' + w.isAbsolute('a\\');
r.wDirname = w.dirname('C:\\a\\b\\c.txt');
r.wBasename = w.basename('C:\\a\\b\\c.txt');
r.wExtname = w.extname('C:\\a\\b\\c.txt');
r.wRel = w.relative('C:\\data\\a', 'C:\\data\\b\\c');
r.wParse = JSON.stringify(w.parse('C:\\home\\user\\dir\\file.txt'));
r.wFormat = w.format({ root: 'C:\\', dir: 'C:\\home\\user', base: 'file.txt' });
r.wMatchesGlob = w.matchesGlob('C:\\foo\\bar.js', 'C:\\foo\\*.js');
r.wMatchesGlobCase = w.matchesGlob('C:\\foo\\BAR.js', 'C:\\foo\\bar.js');

process.stdout.write(JSON.stringify(r));
