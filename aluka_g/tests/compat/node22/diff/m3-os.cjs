// M3-3 diff：node:os 全方法面。
// 输出归一化 JSON 单行。只断言确定性值（架构/版本/常量表等）；
// 运行时内存/负载等用布尔/长度断言避免两次运行间的自然漂移。
const os = require('node:os');
const r = {};

r.surface = ['arch', 'platform', 'type', 'release', 'version', 'machine',
  'availableParallelism', 'cpus', 'loadavg', 'totalmem', 'freemem',
  'uptime', 'networkInterfaces', 'userInfo', 'homedir', 'tmpdir',
  'hostname', 'getPriority', 'setPriority', 'endianness']
  .filter((k) => typeof os[k] === 'function').sort();
r.missing = ['arch', 'platform', 'type', 'release', 'version', 'machine',
  'availableParallelism', 'cpus', 'loadavg', 'totalmem', 'freemem',
  'uptime', 'networkInterfaces', 'userInfo', 'homedir', 'tmpdir',
  'hostname', 'getPriority', 'setPriority', 'endianness']
  .filter((k) => typeof os[k] !== 'function').sort();

r.arch = os.arch();
r.platform = os.platform();
r.type = os.type();
r.release = os.release();
r.version = os.version();
r.machine = os.machine();
r.endianness = os.endianness();
r.devNull = os.devNull;
r.EOLIsStr = typeof os.EOL === 'string';

r.availableParallelism = os.availableParallelism();
r.loadavgLen = os.loadavg().length;
r.loadavg = JSON.stringify(os.loadavg()); // Windows 恒 [0,0,0]，确定性
r.totalmem = os.totalmem();
r.freememGt0 = os.freemem() > 0;
r.uptimeGt0 = os.uptime() > 0;
r.hostnameType = typeof os.hostname() === 'string';
r.homedirType = typeof os.homedir() === 'string';
r.tmpdirType = typeof os.tmpdir() === 'string';

const cpus = os.cpus();
r.cpusLen = cpus.length;
r.cpuKeys = cpus.length ? Object.keys(cpus[0]).sort() : [];
r.cpuTimesKeys = cpus.length ? Object.keys(cpus[0].times).sort() : [];
r.cpuModelType = cpus.length ? typeof cpus[0].model : 'none';
r.cpuSpeedType = cpus.length ? typeof cpus[0].speed : 'none';

const u = os.userInfo();
r.userInfoKeys = Object.keys(u).sort();
r.uidGid = u.uid + '|' + u.gid;
r.shellNull = u.shell === null;
r.usernameType = typeof u.username === 'string';
r.homedirType2 = typeof u.homedir === 'string';

// networkInterfaces：只断言结构（接口集合/地址数值随主机网络变化，不比对值）。
const ni = os.networkInterfaces();
r.niIsObject = typeof ni === 'object';
const niArr = Object.values(ni)[0];
r.niAddrKeys = niArr && niArr.length ? Object.keys(niArr[0]).sort() : [];

// 优先级：读写 self（返回 0）。
r.getPriority = os.getPriority();
r.setPriorityRet = os.setPriority(0, 0);
r.getPriorityAfter = os.getPriority();

// constants
r.constKeys = Object.keys(os.constants).sort();
r.signalKeys = Object.keys(os.constants.signals).sort();
r.sigint = os.constants.signals.SIGINT;
r.priorityKeys = Object.keys(os.constants.priority).sort();
r.prioNormal = os.constants.priority.PRIORITY_NORMAL;
r.errnoCount = Object.keys(os.constants.errno).length;
r.errnoEnoent = os.constants.errno.ENOENT;
r.dlopenKeys = os.constants.dlopen ? JSON.stringify(Object.keys(os.constants.dlopen).sort()) : 'missing';

process.stdout.write(JSON.stringify(r));
