// M6-6 diff：node:diagnostics_channel —— Channel / subscribe / publish /
// bindStore+runStores / tracingChannel。
const dc = require('node:diagnostics_channel');
const { AsyncLocalStorage } = require('node:async_hooks');
const results = {};

// surface
results.exports = Object.keys(dc).sort();
results.ChannelFn = typeof dc.Channel;
results.tracingChannelFn = typeof dc.tracingChannel;

// channel：同一 name 返回同一实例，instanceof Channel
{
  const a = dc.channel('m6:test');
  const b = dc.channel('m6:test');
  results.sameChannel = a === b;
  results.channelName = a.name;
  results.instanceofChannel = a instanceof dc.Channel;
  results.hasSubscribersInitial = a.hasSubscribers;
  results.methods = ['subscribe','unsubscribe','publish','bindStore','unbindStore','runStores']
    .map((k) => k + ':' + typeof a[k]).join(',');
}

// subscribe / publish / unsubscribe
{
  const ch = dc.channel('m6:pub');
  const got = [];
  const sub = (message, name) => { got.push(message + '@' + name); };
  results.hasBefore = dc.hasSubscribers('m6:pub');
  ch.subscribe(sub);
  results.hasAfter = dc.hasSubscribers('m6:pub');
  ch.publish('hello');
  ch.unsubscribe(sub);
  results.hasAfterUnsub = dc.hasSubscribers('m6:pub');
  ch.publish('ignored');
  results.messages = got.join(',');
}

// bindStore + publish：Node 22 中 publish 不改变 store 同步值；订阅者看到的是
// 调用方当前的 ALS 上下文（bindStore 影响异步资源传播）。
{
  const als = new AsyncLocalStorage();
  const ch = dc.channel('m6:store');
  ch.bindStore(als);
  const got = [];
  ch.subscribe(() => { got.push(als.getStore()); });
  ch.publish('S1');
  const outerGot = [];
  const ch2 = dc.channel('m6:store-outer');
  ch2.bindStore(als);
  ch2.subscribe(() => { outerGot.push(als.getStore()); });
  als.run('OUTER', () => { ch2.publish('V'); });
  results.bindStorePublish = got.join(',');
  results.bindStoreOuterContext = outerGot.join(',');
}

// runStores(context, fn)：fn 在绑定 store 的上下文内执行
{
  const als = new AsyncLocalStorage();
  const ch = dc.channel('m6:store2');
  ch.bindStore(als);
  let inside = null;
  ch.runStores('CTX', () => { inside = als.getStore(); });
  results.runStoresContext = inside;
  results.runStoresAfter = als.getStore() === undefined;
}

// unbindStore
{
  const als = new AsyncLocalStorage();
  const ch = dc.channel('m6:store3');
  ch.bindStore(als);
  ch.unbindStore(als);
  let inside = null;
  ch.runStores('CTX', () => { inside = als.getStore(); });
  results.unboundNoContext = inside === undefined;
}

// tracingChannel：子 channel 命名 + traceSync 调用 start/end
{
  const t = dc.tracingChannel('m6:trace');
  const events = [];
  t.start.subscribe(() => events.push('start'));
  t.end.subscribe(() => events.push('end'));
  results.traceStartName = t.start.name;
  results.traceSyncResult = t.traceSync(() => 'rv', { ctx: 1 });
  results.traceEvents = events.join(',');
  results.tracingHasSubscribers = t.hasSubscribers;
}

setTimeout(() => {
  const sorted = {};
  Object.keys(results).sort().forEach((k) => { sorted[k] = results[k]; });
  process.stdout.write(JSON.stringify(sorted));
}, 20);
