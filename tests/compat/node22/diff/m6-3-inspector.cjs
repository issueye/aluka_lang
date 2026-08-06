// M6-3 diff：node:inspector / node:inspector/promises —— surface 核对补全。
const insp = require('node:inspector');
const inspP = require('node:inspector/promises');
const results = {};

// node:inspector surface
results.exports = Object.keys(insp).sort();
results.sessionType = typeof insp.Session;
results.networkType = typeof insp.Network;
results.networkKeys = Object.keys(insp.Network).sort();
results.networkEventsAreFns = ['dataReceived','dataSent','requestWillBeSent','responseReceived','loadingFinished','loadingFailed']
  .every((k) => typeof insp.Network[k] === 'function');
results.networkResourcesType = typeof insp.NetworkResources;
results.networkResourcesPut = typeof insp.NetworkResources.put;
results.consoleType = typeof insp.console.log;
results.waitForDebuggerFn = typeof insp.waitForDebugger;
results.urlFn = typeof insp.url;
results.openFn = typeof insp.open;
results.closeFn = typeof insp.close;

// Session 方法面
{
  const s = new insp.Session();
  results.sessionProto = ['connect','disconnect','post','connectToMainThread']
    .map((k) => k + ':' + typeof s[k]).join(',');
  results.sessionEmitter = typeof s.on === 'function' && typeof s.emit === 'function';
  // post(method, cb)：未连接时同步抛 ERR_INSPECTOR_NOT_CONNECTED（Node 语义）。
  let postErr = null;
  try {
    s.post('Runtime.evaluate', { expression: '1' }, (err) => {});
  } catch (e) {
    postErr = e && e.code;
  }
  results.postThrowsCode = postErr;
}

// node:inspector/promises surface
results.promisesExports = Object.keys(inspP).sort();
results.promisesNetworkKeys = Object.keys(inspP.Network).sort();
results.promisesNetworkResourcesPut = typeof inspP.NetworkResources.put;
{
  const s2 = new inspP.Session();
  results.promisesPostFn = typeof s2.post;
  // post 返回 rejected Promise（无 V8）。
  (async () => {
    try {
      await s2.post('Runtime.evaluate', { expression: '1' });
      results.promisesPostOutcome = 'resolved';
    } catch (e) {
      results.promisesPostOutcome = 'rejected';
    }
    const sorted = {};
    Object.keys(results).sort().forEach((k) => { sorted[k] = results[k]; });
    process.stdout.write(JSON.stringify(sorted));
  })().catch((e) => {
    process.stdout.write(JSON.stringify({ fatal: String(e && e.message) }));
  });
}
