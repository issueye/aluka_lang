// M7-1 diff：node:test 模块面 —— 别名、标记注册、hooks、顶层 shorthand、
// run()/snapshot/mock。纯行为探针（无需测试运行器执行）。
const t = require('node:test');
const results = {};

results.suiteAlias = t.suite === t.describe;
results.testAlias = t.it === t.test;
results.hooks = ['before', 'after', 'beforeEach', 'afterEach'].filter((k) => typeof t[k] === 'function').sort().join(',');
results.mockFns = ['fn', 'method', 'getter', 'setter', 'property', 'restoreAll', 'reset'].filter((k) => typeof t.mock[k] === 'function').sort().join(',');
results.testMarkers = ['skip', 'todo', 'only'].map((k) => typeof t.test[k]).join(',');
results.descMarkers = ['skip', 'todo', 'only'].map((k) => typeof t.describe[k]).join(',');
results.itMarkers = ['skip', 'todo', 'only'].map((k) => typeof t.it[k]).join(',');
results.topLevel = ['assert', 'mock', 'skip', 'todo', 'only', 'run', 'snapshot'].map((k) => typeof t[k]).join(',');
results.snapshotKeys = Object.keys(t.snapshot || {}).sort().join(',');
results.runStream = (() => {
  const s = t.run({ files: [] });
  return [typeof s, typeof s.on, typeof s.pipe].join(',');
})();

process.stdout.write(JSON.stringify(results));
