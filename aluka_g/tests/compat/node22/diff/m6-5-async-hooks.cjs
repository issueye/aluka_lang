// M6-5 diff：node:async_hooks —— createHook/AsyncResource/执行 id + AsyncLocalStorage。
const ah = require('node:async_hooks');
const { AsyncLocalStorage, AsyncResource, createHook, executionAsyncId, triggerAsyncId, executionAsyncResource, asyncWrapProviders } = ah;
const results = {};

// surface
results.exports = Object.keys(ah).sort();
results.execIdTop = executionAsyncId();
results.triggerIdTop = triggerAsyncId();
results.execResourceType = typeof executionAsyncResource();
results.provHasTCPWRAP = 'TCPWRAP' in asyncWrapProviders;
results.provType = typeof asyncWrapProviders.TCPWRAP;

// AsyncLocalStorage：run / 嵌套 / exit / 返回值
{
  const als = new AsyncLocalStorage();
  results.alsOutside = als.getStore() === undefined;
  als.run('A', () => {
    results.alsInside = als.getStore();
    als.run('B', () => { results.alsNested = als.getStore(); });
    results.alsAfterNested = als.getStore();
    als.exit(() => { results.alsInsideExit = als.getStore() === undefined; });
    results.alsAfterExit = als.getStore();
  });
  results.alsAfterRun = als.getStore() === undefined;
  results.alsRunReturn = als.run(42, (x) => x + 1, 0);
}

// enterWith / disable
{
  const als2 = new AsyncLocalStorage();
  als2.enterWith('E');
  results.enterWithStore = als2.getStore();
  als2.disable();
  results.afterDisable = als2.getStore() === undefined;
}

// AsyncResource + createHook 回调（同步时序；destroy 为异步触发，
// 只在末尾用布尔断言其发生）。
{
  const events = [];
  const hook = createHook({
    init(id, type, tid, res) { if (type === 'TESTRES') events.push('init'); },
    before() { events.push('before'); },
    after() { events.push('after'); },
    destroy() { events.push('destroy'); }
  });
  hook.enable();
  const r = new AsyncResource('TESTRES');
  results.resAsyncIdNum = typeof r.asyncId() === 'number';
  results.resTriggerNum = typeof r.triggerAsyncId() === 'number';
  r.runInAsyncScope(() => { events.push('scope'); });
  results.hookEventsSync = events.join(',');
  // Node：emitDestroy 触发的 destroy 回调是异步的，且 disable() 会取消
  // 尚未触发的 destroy。因此不在此处 disable，末尾用布尔断言 destroy 已发生。
  r.emitDestroy();

  // disable() 本身可调用且幂等（不触发事件）。
  const h2 = createHook({ init() { events.push('h2-init'); } });
  h2.enable();
  h2.disable();
  results.disableCallable = true;

  // AsyncLocalStorage 跨异步资源传播：setTimeout / Promise.then / async-await
  const alsP = new AsyncLocalStorage();
  let timerStore, promiseStore, awaitStore;
  alsP.run('T', () => {
    setTimeout(() => { timerStore = alsP.getStore(); }, 10);
    Promise.resolve().then(() => { promiseStore = alsP.getStore(); });
    (async () => { await Promise.resolve(); awaitStore = alsP.getStore(); })();
  });
  setTimeout(() => {
    results.timerStore = timerStore;
    results.promiseStore = promiseStore;
    results.awaitStore = awaitStore;
    results.destroyFiredLate = events.indexOf('destroy') >= 0;
    results.h2NeverFired = events.indexOf('h2-init') === -1;
    const sorted = {};
    Object.keys(results).sort().forEach((k) => { sorted[k] = results[k]; });
    process.stdout.write(JSON.stringify(sorted));
  }, 40);
}
