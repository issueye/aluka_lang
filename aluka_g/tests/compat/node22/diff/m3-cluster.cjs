// M3-10 diff：node:cluster 方法面 + fork 子进程往返。
// 子进程经 ALUKA_WORKER_ID/NODE_CLUSTER env 标记为 worker（isWorker 分支）。
const cluster = require('node:cluster');
const r = {};

if (cluster.isPrimary || cluster.isMaster) {
  r.isPrimary = cluster.isPrimary;
  r.isMaster = cluster.isMaster;
  r.isWorker = cluster.isWorker;
  r.surface = ['fork', 'disconnect', 'setupMaster', 'setupPrimary', 'isPrimary', 'isMaster', 'isWorker', 'workers', 'settings', 'schedulingPolicy', 'SCHED_RR', 'SCHED_NONE', 'Worker']
    .filter((k) => typeof cluster[k] !== 'undefined').sort();
  r.schedulingPolicy = cluster.schedulingPolicy;
  r.settingsKeys = cluster.settings ? Object.keys(cluster.settings).sort() : [];
  r.setupPrimaryFn = typeof cluster.setupPrimary;
  r.setupMasterFn = typeof cluster.setupMaster;
  r.disconnectFn = typeof cluster.disconnect;
  r.workersType = typeof cluster.workers;
  r.WorkerType = typeof cluster.Worker;

  const w = cluster.fork();
  r.workerHasProcess = typeof w.process === 'object';
  r.workerHasSend = typeof w.send;
  r.workerHasKill = typeof w.kill;
  r.workerHasIsConnected = typeof w.isConnected;
  r.workerHasIsDead = typeof w.isDead;
  r.workerId = w.id;
  w.on('exit', (code, signal) => {
    r.workerExit = code;
    r.workersAfterExit = Object.keys(cluster.workers).length;
    process.stdout.write(JSON.stringify(r));
    process.exit(0);
  });
} else {
  // worker：打印 id 后退出。
  const id = cluster.worker && cluster.worker.id;
  process.stdout.write('worker-id:' + id);
  process.exit(0);
}
