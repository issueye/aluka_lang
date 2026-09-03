// M4-1 diff：node:dns —— 主模块 API 面、错误码常量、localhost 解析、
// dns.promises 一致性、Resolver。
const dns = require('node:dns');

// 1. 模块表面。
const surface = {
  m: [
    typeof dns.lookup, typeof dns.resolve, typeof dns.resolve4, typeof dns.resolve6,
    typeof dns.resolveAny, typeof dns.resolveCname, typeof dns.resolveMx,
    typeof dns.resolveNs, typeof dns.resolvePtr, typeof dns.resolveSoa,
    typeof dns.resolveSrv, typeof dns.resolveTxt, typeof dns.lookupService,
    typeof dns.reverse, typeof dns.getServers, typeof dns.setServers,
    typeof dns.setDefaultResultOrder, typeof dns.getDefaultResultOrder,
    typeof dns.Resolver,
  ].join(','),
  // 错误码常量（Node 语义：errno 字符串）。
  codes: [
    dns.NODATA, dns.TIMEOUT, dns.NOTFOUND, dns.FORMERR, dns.REFUSED,
  ].join(','),
  // promises 一致性。
  promisesIdentity: require('node:dns/promises') === dns.promises,
  promisesSurface: [
    typeof dns.promises.lookup, typeof dns.promises.resolve,
    typeof dns.promises.resolve4, typeof dns.promises.lookupService,
    typeof dns.promises.reverse,
  ].join(','),
  order: dns.getDefaultResultOrder(),
  resolver: (() => {
    const r = new dns.Resolver();
    return [
      r instanceof dns.Resolver,
      typeof r.resolve, typeof r.resolve4, typeof r.resolveAny,
      typeof r.cancel, typeof r.setServers,
    ].join(',');
  })(),
};
process.stdout.write('SURFACE:' + JSON.stringify(surface) + '\n');

// 2. 异步解析（localhost，避免外网依赖）。
// 环境归一化：DNS 协议不可用（沙箱屏蔽 UDP-53，run-diff.sh 探测）时，
// live resolve* 断言统一输出 'skipped'（两侧一致），仅保留 lookup（系统解析）。
const NO_DNS = !!process.env.DIFF_NO_DNS;
const results = {};
let done = 0;
const after = () => {
  if (++done === 4) {
    // 键序按异步完成顺序不定，排序保证确定性输出。
    const out = Object.keys(results).sort().map((k) => k + '=' + results[k]);
    process.stdout.write('RESULT:' + JSON.stringify(out) + '\n');
  }
};

// resolve('localhost', 'A') → 仅 IPv4。
dns.resolve('localhost', 'A', (err, addrs) => {
  results.resolveA = NO_DNS ? 'skipped' : [err === null, JSON.stringify(addrs)].join(':');
  after();
});
// resolve4('localhost')。
dns.resolve4('localhost', (err, addrs) => {
  results.resolve4 = NO_DNS ? 'skipped' : [err === null, JSON.stringify(addrs)].join(':');
  after();
});
// lookup：地址为 loopback，family 4/6。
dns.lookup('localhost', (err, addr, family) => {
  const ok = err === null && (addr === '127.0.0.1' || addr === '::1') && (family === 4 || family === 6);
  results.lookup = ok ? 'ok:' + addr + ':' + family : 'bad:' + (err && err.code);
  after();
});
// promises.resolve。
dns.promises.resolve('localhost', 'A').then((addrs) => {
  results.pResolve = NO_DNS ? 'skipped' : JSON.stringify(addrs);
  after();
}).catch((e) => { results.pResolve = NO_DNS ? 'skipped' : 'err:' + e.code; after(); });
