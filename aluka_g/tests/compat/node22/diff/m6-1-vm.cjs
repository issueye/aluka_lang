// M6-1 diff：node:vm —— runIn*/createContext/context 隔离/Script/compileFunction。
const vm = require('node:vm');
const results = {};

// surface
results.exports = Object.keys(vm).sort();
results.scriptType = typeof vm.Script;

// runInThisContext / runInNewContext 基本求值
results.runInThisContext = vm.runInThisContext('1 + 2 * 3');
results.runInNewContext = vm.runInNewContext('(function(){ return "hi"; })()');
results.thisContextGlobalWrite = (vm.runInThisContext('globalThis.__m6_vm_flag = 1'), globalThis.__m6_vm_flag);

// sandbox：createContext + runInContext（写入同步回 sandbox，再次 run 持久化）
{
  const sandbox = { x: 1 };
  vm.createContext(sandbox);
  results.isContext = vm.isContext(sandbox);
  vm.runInContext('x = x + 1; y = x * 10;', sandbox);
  results.sandboxX = sandbox.x;
  results.sandboxY = sandbox.y;
  vm.runInContext('z = 42;', sandbox);
  results.sandboxZ = sandbox.z;
}

// runInNewContext 携带 sandbox：多次调用共享上下文状态
{
  const box = { n: 0 };
  vm.runInNewContext('n = n + 1;', box);
  vm.runInNewContext('n = n + 1;', box);
  results.sharedSandbox = box.n;
}

// context 隔离：A 的全局不泄漏到 B，也不泄漏到宿主
{
  const a = vm.createContext({});
  const b = vm.createContext({});
  vm.runInContext('globalThis.sec = "a-secret";', a);
  results.leakToHost = typeof globalThis.sec;
  results.leakToB = vm.runInContext('typeof sec', b);
  results.inA = vm.runInContext('sec', a);
}

// Script：构造期语法错误、runInThisContext、runInNewContext、runInContext、cachedData
{
  let ctorThrows = false;
  try { new vm.Script('var = ;'); } catch (e) { ctorThrows = true; }
  results.scriptSyntaxErrorThrows = ctorThrows;

  // runInThisContext 在当前 context 运行：先在宿主全局定义 a/b。
  globalThis.a = 1;
  globalThis.b = 2;
  const script = new vm.Script('a + b');
  results.scriptRun = script.runInThisContext({});
  delete globalThis.a;
  delete globalThis.b;

  const fresh = vm.createContext({});
  vm.runInContext('a = 1; b = 2;', fresh);
  results.scriptRunInContext = script.runInContext(fresh);

  const mul = new vm.Script('m * 2');
  results.scriptRunInNew = mul.runInNewContext({ m: 10 });
  results.scriptCachedDataIsBuffer = Buffer.isBuffer(script.createCachedData());

  const rej = new vm.Script('1', { cachedData: Buffer.from('stub') });
  results.cachedDataRejected = rej.cachedDataRejected === true;
}

// compileFunction：参数、返回值、options.name / filename
{
  const add = vm.compileFunction('return x + y;', ['x', 'y']);
  results.compileFunction = add(2, 5);
  const named = vm.compileFunction('return this === undefined;', []);
  results.compileFnThis = named.call({});
  results.compileFnName = vm.compileFunction('return 1', []).name;
}

// measureMemory：仅验证方法存在（Node 22 中调用会触发 experimental 警告
// 且 Promise 永不 settle，故不实际调用）。
results.measureMemoryIsFn = typeof vm.measureMemory === 'function';

setTimeout(() => {
  const sorted = {};
  Object.keys(results).sort().forEach((k) => { sorted[k] = results[k]; });
  process.stdout.write(JSON.stringify(sorted));
}, 80);
