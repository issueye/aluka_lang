// M3-7 diff：node:tty 方法面。
// 差分环境 stdin/stdout 均为管道（非 TTY）：isatty 恒 false；
// 非 TTY fd 构造 ReadStream/WriteStream → ERR_TTY_INIT_FAILED。
const tty = require('node:tty');
const r = {};

r.surface = ['isatty', 'ReadStream', 'WriteStream']
  .filter((k) => typeof tty[k] !== 'undefined').sort();
r.isattyFn = typeof tty.isatty === 'function';

// 管道环境：isatty 全部 false。
r.isatty0 = tty.isatty(0);
r.isatty1 = tty.isatty(1);
r.isatty2 = tty.isatty(2);

// 原型方法面。
r.rsProtoKeys = Object.getOwnPropertyNames(tty.ReadStream.prototype).sort();
r.wsProtoKeys = Object.getOwnPropertyNames(tty.WriteStream.prototype).sort();
r.rsSetRawMode = typeof tty.ReadStream.prototype.setRawMode;
r.wsClearLine = typeof tty.WriteStream.prototype.clearLine;
r.wsClearScreenDown = typeof tty.WriteStream.prototype.clearScreenDown;
r.wsCursorTo = typeof tty.WriteStream.prototype.cursorTo;
r.wsMoveCursor = typeof tty.WriteStream.prototype.moveCursor;
r.wsGetColorDepth = typeof tty.WriteStream.prototype.getColorDepth;
r.wsHasColors = typeof tty.WriteStream.prototype.hasColors;
r.wsGetWindowSize = typeof tty.WriteStream.prototype.getWindowSize;

// 非 TTY fd 构造 → ERR_TTY_INIT_FAILED。
try { new tty.ReadStream(0); r.rsCtor = 'no-throw'; } catch (e) { r.rsCtor = e.code; }
try { new tty.WriteStream(1); r.wsCtor = 'no-throw'; } catch (e) { r.wsCtor = e.code; }

process.stdout.write(JSON.stringify(r));
