package interpreter

import (
	"testing"
)

// === Async/await (1D.9) ====================================================

// TestAsyncBasicReturn: async function returns a value (wrapped in Promise).
func TestAsyncBasicReturn(t *testing.T) {
	got := vmEvalPromise(t, `
async function foo() { return 42 }
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "42" {
		t.Errorf("async return = %q, want 42", got)
	}
}

// TestAsyncReturnsPromise: async function returning a Promise adopts it.
func TestAsyncReturnsPromise(t *testing.T) {
	got := vmEvalPromise(t, `
async function foo() { return Promise.resolve(99) }
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "99" {
		t.Errorf("async returns promise = %q, want 99", got)
	}
}

// TestAsyncAwaitValue: await on a non-Promise value wraps it via resolve.
func TestAsyncAwaitValue(t *testing.T) {
	got := vmEvalPromise(t, `
async function foo() { var x = await 10; return x + 5 }
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "15" {
		t.Errorf("await value = %q, want 15", got)
	}
}

// TestAsyncAwaitPromise: await on a Promise suspends until it settles.
func TestAsyncAwaitPromise(t *testing.T) {
	got := vmEvalPromise(t, `
async function foo() {
  var x = await Promise.resolve(20);
  return x * 2;
}
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "40" {
		t.Errorf("await promise = %q, want 40", got)
	}
}

func TestAsyncAwaitOptionalMethodCall(t *testing.T) {
	got := vmEvalPromise(t, `
async function resolve(ctx) {
  return (await ctx.env("X"))?.trim();
}
resolve({ env: async () => undefined }).then(function(v) { globalThis.__r = v ?? "none"; });
`)
	if got != "none" {
		t.Errorf("await optional method = %q, want none", got)
	}
}

// TestAsyncMultipleAwaits: multiple sequential awaits in one function.
func TestAsyncMultipleAwaits(t *testing.T) {
	got := vmEvalPromise(t, `
async function foo() {
  var a = await Promise.resolve(1);
  var b = await Promise.resolve(2);
  var c = await Promise.resolve(3);
  return a + b + c;
}
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "6" {
		t.Errorf("multiple awaits = %q, want 6", got)
	}
}

func TestAsyncMultipleAwaitsPreserveOperandStack(t *testing.T) {
	got := vmEvalPromise(t, `
async function foo() {
  return [await Promise.resolve("a"), await Promise.resolve("b")].join(",");
}
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "a,b" {
		t.Errorf("multiple await operands = %q, want a,b", got)
	}
}

func TestAsyncMultipleAwaitedMethodsPreserveThisAndOperands(t *testing.T) {
	got := vmEvalPromise(t, `
class Client {
  constructor(value) { this.value = value; }
  async first() { return { value: this.value }; }
  async second() { return undefined; }
  async both() { return [await this.first(), await this.second()]; }
}
new Client("secret").both().then(function(v) {
  globalThis.__r = v[0].value + ":" + String(v[1]);
});
`)
	if got != "secret:undefined" {
		t.Errorf("multiple awaited methods = %q, want secret:undefined", got)
	}
}

// TestAsyncChainedAwaits: await chains where each depends on the previous.
func TestAsyncChainedAwaits(t *testing.T) {
	got := vmEvalPromise(t, `
async function double(v) { return v * 2 }
async function foo() {
  var a = await double(1);
  var b = await double(a);
  var c = await double(b);
  return c;
}
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "8" {
		t.Errorf("chained awaits = %q, want 8", got)
	}
}

// TestAsyncThrowRejects: throwing in an async function rejects the promise.
func TestAsyncThrowRejects(t *testing.T) {
	got := vmEvalPromise(t, `
async function foo() { throw "boom" }
foo().catch(function(e) { globalThis.__r = e });
`)
	if got != "boom" {
		t.Errorf("async throw = %q, want boom", got)
	}
}

// TestAsyncAwaitRejectThrows: awaiting a rejected promise throws in the body.
func TestAsyncAwaitRejectThrows(t *testing.T) {
	got := vmEvalPromise(t, `
async function foo() {
  try {
    await Promise.reject("nope");
  } catch(e) {
    return "caught:" + e;
  }
}
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "caught:nope" {
		t.Errorf("await reject = %q, want caught:nope", got)
	}
}

// TestAsyncAwaitRejectUncaught: uncaught await rejection rejects the promise.
func TestAsyncAwaitRejectUncaught(t *testing.T) {
	got := vmEvalPromise(t, `
async function foo() { return await Promise.reject("fail") }
foo().catch(function(e) { globalThis.__r = e });
`)
	if got != "fail" {
		t.Errorf("await reject uncaught = %q, want fail", got)
	}
}

// TestAsyncTryCatch: try/catch around an await that throws.
func TestAsyncTryCatch(t *testing.T) {
	got := vmEvalPromise(t, `
async function foo() {
  try {
    await Promise.resolve(1);
    throw "mid";
  } catch(e) {
    return "got:" + e;
  }
}
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "got:mid" {
		t.Errorf("async try/catch = %q, want got:mid", got)
	}
}

// TestAsyncArrow: async arrow function expression.
func TestAsyncArrow(t *testing.T) {
	got := vmEvalPromise(t, `
var foo = async () => { return 7 }
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "7" {
		t.Errorf("async arrow = %q, want 7", got)
	}
}

// TestAsyncArrowAwait: async arrow with await.
func TestAsyncArrowAwait(t *testing.T) {
	got := vmEvalPromise(t, `
var foo = async () => { return await Promise.resolve(11) }
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "11" {
		t.Errorf("async arrow await = %q, want 11", got)
	}
}

// TestAsyncArrowSingleParam: async arrow with single param (no parens).
func TestAsyncArrowSingleParam(t *testing.T) {
	got := vmEvalPromise(t, `
var foo = async x => x * 3
foo(4).then(function(v) { globalThis.__r = v });
`)
	if got != "12" {
		t.Errorf("async arrow single param = %q, want 12", got)
	}
}

// TestAsyncFunctionExpr: async function expression (anonymous).
func TestAsyncFunctionExpr(t *testing.T) {
	got := vmEvalPromise(t, `
var foo = async function() { return 33 }
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "33" {
		t.Errorf("async function expr = %q, want 33", got)
	}
}

// TestAsyncReturnsUndefined: async function with no return resolves with undefined.
func TestAsyncReturnsUndefined(t *testing.T) {
	got := vmEvalPromise(t, `
async function foo() { }
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "undefined" {
		t.Errorf("async undefined = %q, want undefined", got)
	}
}

// TestAsyncInstanceOf: async function result is a Promise.
func TestAsyncInstanceOf(t *testing.T) {
	got := vmEvalStr(t, `
async function foo() { return 1 }
foo() instanceof Promise
`)
	if got != "true" {
		t.Errorf("async instanceof Promise = %q, want true", got)
	}
}

// TestAsyncClassMethod: async method in a class.
func TestAsyncClassMethod(t *testing.T) {
	got := vmEvalPromise(t, `
class Foo {
  async bar() { return await Promise.resolve(55) }
}
new Foo().bar().then(function(v) { globalThis.__r = v });
`)
	if got != "55" {
		t.Errorf("async class method = %q, want 55", got)
	}
}

// TestAsyncClosureCapture: async function capturing closure variables.
func TestAsyncClosureCapture(t *testing.T) {
	got := vmEvalPromise(t, `
function makeCounter() {
  var n = 0;
  return async function() { n++; return n }
}
var c = makeCounter();
c().then(function() {
  c().then(function(v) { globalThis.__r = v });
});
`)
	if got != "2" {
		t.Errorf("async closure capture = %q, want 2", got)
	}
}

// TestAsyncAwaitNested: nested async function calls with await.
func TestAsyncAwaitNested(t *testing.T) {
	got := vmEvalPromise(t, `
async function inner() { return await Promise.resolve(10) }
async function outer() { return await inner() + 5 }
outer().then(function(v) { globalThis.__r = v });
`)
	if got != "15" {
		t.Errorf("async nested = %q, want 15", got)
	}
}

// TestAsyncAwaitInExpression: await in a larger expression.
func TestAsyncAwaitInExpression(t *testing.T) {
	got := vmEvalPromise(t, `
async function foo() {
  var a = await Promise.resolve(3);
  var b = await Promise.resolve(4);
  return a * a + b * b;
}
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "25" {
		t.Errorf("await in expression = %q, want 25", got)
	}
}

// TestAsyncAwaitFinally: try/finally around await.
func TestAsyncAwaitFinally(t *testing.T) {
	got := vmEvalPromise(t, `
var log = [];
async function foo() {
  try {
    await Promise.resolve(1);
    log.push("body");
  } finally {
    log.push("finally");
  }
  return log.join(",");
}
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "body,finally" {
		t.Errorf("await finally = %q, want body,finally", got)
	}
}

// TestAsyncAwaitRejectFinally: finally runs even when await rejects.
func TestAsyncAwaitRejectFinally(t *testing.T) {
	got := vmEvalPromise(t, `
var log = [];
async function foo() {
  try {
    await Promise.reject("err");
  } finally {
    log.push("finally");
  }
}
foo().catch(function(e) {
  globalThis.__r = e + "|" + log.join(",");
});
`)
	if got != "err|finally" {
		t.Errorf("await reject finally = %q, want err|finally", got)
	}
}

// TestAsyncPreservesThis: async function preserves `this` across awaits.
func TestAsyncPreservesThis(t *testing.T) {
	got := vmEvalPromise(t, `
var obj = {
  x: 100,
  async getX() {
    await Promise.resolve(1);
    return this.x;
  }
};
obj.getX().then(function(v) { globalThis.__r = v });
`)
	if got != "100" {
		t.Errorf("async this = %q, want 100", got)
	}
}

// TestAsyncParamsAndDefaults: async function with default params.
func TestAsyncParamsAndDefaults(t *testing.T) {
	got := vmEvalPromise(t, `
async function foo(a, b = 10) { return a + b }
foo(5).then(function(v) { globalThis.__r = v });
`)
	if got != "15" {
		t.Errorf("async defaults = %q, want 15", got)
	}
}

// TestAsyncRestParam: async function with rest parameter.
func TestAsyncRestParam(t *testing.T) {
	got := vmEvalPromise(t, `
async function foo(...args) { return args.join("+") }
foo(1, 2, 3).then(function(v) { globalThis.__r = v });
`)
	if got != "1+2+3" {
		t.Errorf("async rest = %q, want 1+2+3", got)
	}
}

// TestAsyncAwaitNullUndefined: await on null and undefined.
func TestAsyncAwaitNullUndefined(t *testing.T) {
	got := vmEvalPromise(t, `
async function foo() {
  var a = await null;
  var b = await undefined;
  return String(a) + "|" + String(b);
}
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "null|undefined" {
		t.Errorf("await null/undefined = %q, want null|undefined", got)
	}
}

// TestAsyncClosureMutationAcrossAwait：async 函数内 let 变量被异步回调
// （微任务）修改后，await 恢复时必须读到修改后的值。
// 回归：挂起帧的栈段在外层函数返回时被截断，恢复时 base 错位导致
// closedUps 写回失败（变量恢复为挂起前快照）。
func TestAsyncClosureMutationAcrossAwait(t *testing.T) {
	got := vmEvalPromise(t, `
async function main() {
  let out = '';
  const p = new Promise(function(resolve) {
    Promise.resolve().then(function() { out += 'x'; resolve(); });
  });
  await p;
  globalThis.__r = out;
}
main();
`)
	if got != "x" {
		t.Errorf("closure mutation across await = %q, want x", got)
	}
}

// TestAsyncClosureMutationMulti：异步回调多次修改 + await 恢复。
func TestAsyncClosureMutationMulti(t *testing.T) {
	got := vmEvalPromise(t, `
async function main() {
  let out = '';
  const p = new Promise(function(resolve) {
    Promise.resolve().then(function() { out += 'a'; });
    Promise.resolve().then(function() { out += 'b'; resolve(); });
  });
  await p;
  globalThis.__r = out;
}
main();
`)
	if got != "ab" {
		t.Errorf("multi closure mutation = %q, want ab", got)
	}
}

// TestAsyncClosureMutationAfterResume verifies that captures are reopened
// after await. A closure invoked while the frame is running must update the
// local value observed after the next suspension.
func TestAsyncClosureMutationAfterResume(t *testing.T) {
	got := vmEvalPromise(t, `
async function main() {
  let terminal = false;
  function finalize() { terminal = true; }
  await Promise.resolve();
  finalize();
  await Promise.resolve();
  return terminal;
}
main().then(function(value) { globalThis.__r = String(value); });
`)
	if got != "true" {
		t.Errorf("closure mutation after resume = %q, want true", got)
	}
}
