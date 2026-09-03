// M7-2 diff：node:test mock —— getter/setter/property、MockFunctionContext
// （callCount/resetCalls/mockImplementationOnce）与 calls 语义。
// 纯行为探针（无需测试运行器执行），单行 JSON 输出。
const { mock } = require('node:test');
const results = {};

// mock.getter / mock.setter（基于访问器属性；mock 对象为 MockFunctionContext）。
const backing = { _v: 5 };
const obj = {};
Object.defineProperty(obj, 'v', {
  get() { return backing._v; },
  set(x) { backing._v = x; },
  configurable: true,
});
const g = mock.getter(obj, 'v', () => 99);
results.getterVal = obj.v;
results.gCalls = g.mock.calls.length;
results.gCallResult = g.mock.calls[0].result;
results.gCallArgs = g.mock.calls[0].arguments.length;
results.gMockKeys = ['callCount', 'calls', 'mockImplementation', 'mockImplementationOnce', 'resetCalls', 'restore']
  .filter((k) => typeof g.mock[k] !== 'undefined').sort().join(',');
g.mock.restore();
results.afterGetterRestore = obj.v;

const s = mock.setter(obj, 'v', (v) => { results.setVal = v; });
obj.v = 42;
results.setVal = results.setVal;
results.sCalls = s.mock.calls.length;
results.sCallArg = s.mock.calls[0].arguments[0];
s.mock.restore();
obj.v = 7;
results.afterSetterRestore = obj.v;

// mock.property（v22.20：accesses 记录 get/set）。
const obj3 = { p: 'orig' };
const pr = mock.property(obj3, 'p', 'mocked');
results.propVal = obj3.p;
results.propAccesses = pr.mock.accesses.length;
results.propAccessType = pr.mock.accesses[0].type;
pr.mock.restore();
results.propRestore = obj3.p;

// mock.fn + MockFunctionContext 方法。
const fn = mock.fn((a) => a * 2);
fn(1); fn(2); fn(3);
results.fnCallCount = fn.mock.callCount();
results.fnCalls = fn.mock.calls.length;
results.fnResult = fn.mock.calls[2].result;
fn.mock.resetCalls();
results.fnAfterReset = fn.mock.calls.length;
fn.mock.mockImplementationOnce((a) => a * 10);
results.fnOnce = fn(5);
results.fnAfterOnce = fn(5);

// mock.method 的 restore 行为。
const objm = { add(a, b) { return a + b; } };
const spy = mock.method(objm, 'add');
results.m1 = objm.add(2, 3);
results.m2 = spy.mock.calls.length;
spy.mock.restore();
results.m3 = objm.add(5, 6);

process.stdout.write(JSON.stringify(results));
