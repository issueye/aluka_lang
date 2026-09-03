// M1-5 diff：node:readline/promises —— surface / 构造 / 方法面 / 成功（流输入）/ 取消(rollback)。
const rlp = require('node:readline/promises');
const { Readable, Transform } = require('node:stream');
const results = {};

results.surface = Object.keys(rlp).sort();
results.hasCreateInterface = typeof rlp.createInterface;
results.hasReadlineClass = typeof rlp.Readline;

// Readline 原型方法面（避免构造参数校验差异）
const proto = rlp.Readline.prototype || {};
results.readlineMethods = ['question', 'commit', 'rollback', 'clearLine', 'clearScreenDown', 'cursorTo', 'moveCursor']
  .filter((m) => typeof proto[m] === 'function');

// 构造 Readline：Node 要求 Duplex 流
try {
  const t = new Transform({ transform(c, e, cb) { cb(null, c); } });
  const rl = new rlp.Readline(t);
  results.constructReadline = true;
} catch (e) {
  results.constructReadline = 'err';
}

(async () => {
  // 成功：createInterface 从 input 流读一行
  const rl2 = rlp.createInterface({ input: Readable.from(['hello\n']), output: process.stdout });
  const ans = await rl2.question('');
  results.questionResolved = ans;
  // 取消：Interface 的 rollback/commit 面
  results.interfaceHasRollback = typeof rl2.rollback;
  results.interfaceHasCommit = typeof rl2.commit;
  process.stdout.write(JSON.stringify(results));
})();
