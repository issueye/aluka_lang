// M1-7 diff：node:test/reporters —— surface / 构造 / 流方法面（CJS 视角）。
const reporters = require('node:test/reporters');
const results = {};

// surface：CJS 视角 dot/junit/lcov/spec/tap（无 default 键）
results.surface = Object.keys(reporters).sort();
results.hasDefault = Object.prototype.hasOwnProperty.call(reporters, 'default');

// 各报告器类型：dot/junit/spec/tap 为类（function），lcov 为实例（object）
results.ctorTypes = {};
for (const n of ['dot', 'junit', 'lcov', 'spec', 'tap']) {
  results.ctorTypes[n] = typeof reporters[n];
}

// 构造 spec 报告器：有 write/end 流方法
try {
  const spec = new reporters.spec();
  results.specMethods = ['write', 'end'].filter((m) => typeof spec[m] === 'function');
} catch (e) {
  results.specMethods = 'err:' + e.message;
}

// lcov 为实例：可直接调用 write
try {
  results.lcovWritable = typeof reporters.lcov.write === 'function';
} catch (e) {
  results.lcovWritable = false;
}

process.stdout.write(JSON.stringify(results));
