// M7-1 diff：node:test 模块面 —— 别名、标记注册、hooks、顶层 shorthand。
// 纯行为探针（无需测试运行器执行）。注意：node 的 run()/snapshot 未在
// 此用例比较（aluka knownDifference）。
const t = require('node:test');
const results = {};

results.suiteAlias = t.suite === t.describe;
results.testAlias = t.it === t.test;
results.hooks = ['before', 'after', 'beforeEach', 'afterEach'].filter((k) => typeof t[k] === 'function').sort().join(',');
results.mockFns = ['fn', 'method', 'getter', 'setter', 'property', 'restoreAll'].filter((k) => typeof t.mock[k] === 'function').sort().join(',');
results.testMarkers = ['skip', 'todo', 'only'].map((k) => typeof t.test[k]).join(',');
results.descMarkers = ['skip', 'todo', 'only'].map((k) => typeof t.describe[k]).join(',');
results.itMarkers = ['skip', 'todo', 'only'].map((k) => typeof t.it[k]).join(',');
results.topLevel = ['assert', 'mock', 'skip', 'todo', 'only'].map((k) => typeof t[k]).join(',');

process.stdout.write(JSON.stringify(results));
