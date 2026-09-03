// M1-2 diff：node:inspector/promises —— surface / 成功（构造）/ 失败（post reject）。
const insp = require('node:inspector/promises');
const results = {};

// surface：只导出 Session
results.surface = Object.keys(insp).sort();
results.sessionType = typeof insp.Session;

// 成功：new Session() 可用
const s = new insp.Session();
results.hasConnect = typeof s.connect;
results.hasDisconnect = typeof s.disconnect;

// 失败：post 返回 rejected Promise
(async () => {
  try {
    await s.post('Runtime.evaluate', { expression: '1' });
    results.postOutcome = 'resolved';
  } catch (e) {
    results.postOutcome = 'rejected';
  }
  process.stdout.write(JSON.stringify(results));
})().catch((e) => {
  process.stdout.write(JSON.stringify({ fatal: String(e && e.message) }));
});
