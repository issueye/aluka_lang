// M3-9 diff：node:repl 方法面。
// 差分环境 stdin 为 /dev/null：repl.start 立即 EOF 返回 REPLServer。
const repl = require('node:repl');
const r = {};

r.startFn = typeof repl.start === 'function';

const server = repl.start({ prompt: 'R> ' });
r.defineCommand = typeof server.defineCommand;
r.displayPrompt = typeof server.displayPrompt;
r.clearBufferedCommand = typeof server.clearBufferedCommand;
r.setupHistory = typeof server.setupHistory;
r.setPrompt = typeof server.setPrompt;
r.close = typeof server.close;
r.context = typeof server.context;

process.stdout.write(JSON.stringify(r));
