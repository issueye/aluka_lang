// M3-6 diff：worker_threads 方法面 + Worker eval 消息往返 + env data。
const wt = require('node:worker_threads');
const r = {};

r.surface = Object.keys(wt).sort();
r.isMain = wt.isMainThread;
r.threadId = wt.threadId;
r.isInternalThread = wt.isInternalThread;
r.threadName = wt.threadName;
r.resourceLimitsType = typeof wt.resourceLimits;
r.shareEnvType = typeof wt.SHARE_ENV;
r.parentPortNull = wt.parentPort === null;
r.workerDataUndef = typeof wt.workerData === 'undefined';

// Worker eval 消息往返 + 事件（worker 收 'ping' 回 'pong' 后 close 退出）。
const worker = new wt.Worker(
  `const { parentPort, workerData } = require('node:worker_threads');
   parentPort.postMessage('echo:' + workerData.v);
   parentPort.on('message', (m) => {
     if (m === 'ping') {
       parentPort.postMessage('pong:' + workerData.v);
       parentPort.close();
     }
   });`,
  { eval: true, workerData: { v: 42 } }
);
let got = null;
worker.on('message', (m) => {
  if (typeof m === 'string' && m.startsWith('echo')) {
    worker.postMessage('ping');
  } else {
    got = m;
  }
});
worker.on('exit', (code) => {
  r.workerMsg = got;
  r.workerExit = code;
  r.workerThreadIdNum = typeof worker.threadId === 'number';
  r.workerHasPost = typeof worker.postMessage === 'function';
  r.workerHasTerminate = typeof worker.terminate === 'function';
  r.workerStdoutNull = worker.stdout === null;

  // env data（跨线程共享存储）。
  wt.setEnvironmentData('k1', 'v1');
  r.envGet = wt.getEnvironmentData('k1');
  wt.setEnvironmentData('k1');
  r.envAfterDelete = typeof wt.getEnvironmentData('k1');
  r.envMissing = typeof wt.getEnvironmentData('nope');

  // MessageChannel + receiveMessageOnPort（确定性同步取）。
  const { port1: a, port2: b } = new wt.MessageChannel();
  a.postMessage('buffered-msg');
  const recv = wt.receiveMessageOnPort(b);
  r.receiveMsg = recv ? recv.message : null;
  r.receiveEmpty = wt.receiveMessageOnPort(b);
  a.close();
  b.close();

  // BroadcastChannel 表面。
  const bc = new wt.BroadcastChannel('chan-1');
  r.bcName = bc.name;
  r.bcHasPost = typeof bc.postMessage;
  r.bcHasClose = typeof bc.close;
  bc.close();

  // 其余方法面。
  r.postToThreadType = typeof wt.postMessageToThread;
  r.movePortType = typeof wt.moveMessagePortToContext;
  r.markType = typeof wt.markAsUntransferable;
  r.isMarkedType = typeof wt.isMarkedAsUntransferable;
  r.receiveFnType = typeof wt.receiveMessageOnPort;
  r.envDataKeyType = typeof wt.setEnvironmentData;

  const out = {};
  for (const k of Object.keys(r).sort()) out[k] = r[k];
  process.stdout.write(JSON.stringify(out));
  process.exit(0);
});
