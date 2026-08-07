// M1-1 diff：node:dns/promises —— surface / 成功 / 失败 / 身份。
// 输出归一化 JSON 单行；网络用例只比较结构（类型/数量），不比较具体 IP。
const dnsP = require('node:dns/promises');
const dns = require('node:dns');
const results = {};

// surface：导出函数集合
results.surface = Object.keys(dnsP).sort();
results.hasResolver = typeof dnsP.Resolver;

// 身份：require('node:dns/promises') === require('node:dns').promises
results.identity = dnsP === dns.promises;

// 成功：lookup('localhost') 解析非空
(async () => {
  try {
    const addr = await dnsP.lookup('localhost');
    results.lookupLocalhost = typeof addr === 'string' && addr.length > 0;
  } catch (e) {
    results.lookupLocalhost = 'err:' + e.code;
  }
  // 成功：resolve('localhost') 数组非空。
  // 环境归一化：DNS 协议不可用（沙箱屏蔽 UDP-53，run-diff.sh 探测）时跳过 live 断言。
  if (process.env.DIFF_NO_DNS) {
    results.resolveLocalhost = 'skipped';
  } else {
    try {
      const addrs = await dnsP.resolve('localhost');
      results.resolveLocalhost = Array.isArray(addrs) && addrs.length > 0;
    } catch (e) {
      results.resolveLocalhost = 'err:' + e.code;
    }
  }
  // 失败：不存在域名 reject
  try {
    await dnsP.lookup('nonexistent-host-zzz.invalid');
    results.lookupMissing = 'resolved';
  } catch (e) {
    results.lookupMissing = 'rejected';
  }
  // Resolver 实例：resolve 方法存在
  const r = new dnsP.Resolver();
  results.resolverHasResolve = typeof r.resolve;
  results.resolverHasSetServers = typeof r.setServers;
  process.stdout.write(JSON.stringify(results));
})().catch((e) => {
  process.stdout.write(JSON.stringify({ fatal: String(e && e.message) }));
});
