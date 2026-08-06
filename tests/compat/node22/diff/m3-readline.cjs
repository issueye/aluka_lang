// M3-8 diff：node:readline 方法面。
// 差分环境 stdin 为 /dev/null（EOF），不做交互读取，只验证方法面。
const readline = require('node:readline');
const r = {};

r.surface = ['createInterface', 'emitKeypressEvents', 'clearLine', 'clearScreenDown', 'cursorTo', 'moveCursor']
  .filter((k) => typeof readline[k] !== 'undefined').sort();
r.createInterfaceFn = typeof readline.createInterface === 'function';
r.emitKeypressEvents = typeof readline.emitKeypressEvents;
r.clearLine = typeof readline.clearLine;
r.clearScreenDown = typeof readline.clearScreenDown;
r.cursorTo = typeof readline.cursorTo;
r.moveCursor = typeof readline.moveCursor;

// createInterface 返回的 Interface 实例方法面。
const rl = readline.createInterface({ input: process.stdin, output: process.stdout });
r.ifaceMethods = ['question', 'prompt', 'pause', 'resume', 'close', 'setPrompt', 'getPrompt', 'write', 'getCursorPos']
  .filter((k) => typeof rl[k] === 'function').sort();
r.ifaceMissing = ['question', 'prompt', 'pause', 'resume', 'close', 'setPrompt', 'getPrompt', 'write', 'getCursorPos']
  .filter((k) => typeof rl[k] !== 'function').sort();
r.ifaceTerminal = typeof rl.terminal;
r.ifaceLine = typeof rl.line;

// 基本行为：setPrompt/getPrompt/close（同步，确定性）。
rl.setPrompt('p> ');
r.getPrompt = rl.getPrompt();
r.promptReturns = typeof rl.prompt();
r.getCursorPosType = typeof rl.getCursorPos;
r.writeReturns = rl.write('abc');

rl.close();

process.stdout.write(JSON.stringify(r));
