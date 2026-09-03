// M2-8 diff：CJS/ESM loader —— 循环依赖、缓存身份、require(esm)、import(CJS) 互操作。
const results = {};

// 1. CJS 循环依赖 + 缓存身份
{
  const a = require('./m28-fixtures/a.cjs');
  const b = require('./m28-fixtures/b.cjs');
  results.cycle = JSON.stringify({ a: a.a, aFromB: a.fromB, b: b.b, bFromA: b.fromA });
  results.cacheIdentity = require('./m28-fixtures/a.cjs') === a;
}

// 2. require(esm)：命名导出 + default + __esModule
{
  const esm = require('./m28-fixtures/esm.mjs');
  results.requireEsm = JSON.stringify({
    named: esm.named,
    hasDefault: typeof esm.default === 'function',
    keys: Object.keys(esm).sort(),
  });
}

// 3. import(CJS)：default + 命名导出
import('./m28-fixtures/cjs.cjs').then((cjs) => {
  results.importCjs = JSON.stringify({ hasDefault: typeof cjs.default === 'object', named: cjs.cjs, keys: Object.keys(cjs).sort() });
  process.stdout.write(JSON.stringify(results));
}).catch((e) => {
  results.fatal = String(e && e.message);
  process.stdout.write(JSON.stringify(results));
});
