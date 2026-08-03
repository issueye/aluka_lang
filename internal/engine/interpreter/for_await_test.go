package interpreter

import "testing"

// 本文件覆盖 ES2018 for await...of（1D.10）。
// 风格对齐 async_smoke_test.go：通过 vmEvalPromise 观察 async 函数最终结果。
//
// 测试场景：
//  1. 手写 async iterable（带 [Symbol.asyncIterator] 的对象，next 返回 Promise）
//  2. 回退到同步迭代器（数组：无 asyncIterator，走 Symbol.iterator + OpAwait 包装）
//  3. Promise.reject 经 await 抛入循环体被 try/catch 捕获
//  4. break 提前退出
//  5. 解构绑定 + 累加
//  6. 非异步上下文使用 for await 应报语法错误

// TestForAwaitAsyncIterable: 手写 async iterable。
//
//	asyncRange(1,4) 产出 1,2,3（done 后停止）。
//	[Symbol.asyncIterator] 返回自身，next() 返回 Promise<{value,done}>。
func TestForAwaitAsyncIterable(t *testing.T) {
	got := vmEvalPromise(t, `
function asyncRange(start, end) {
  var i = start;
  return {
    [Symbol.asyncIterator]() {
      return {
        next: function() {
          if (i < end) {
            return Promise.resolve({ value: i++, done: false });
          }
          return Promise.resolve({ value: undefined, done: true });
        }
      };
    }
  };
}
async function collect() {
  var sum = 0;
  for await (var n of asyncRange(1, 4)) {
    sum += n;
  }
  return sum;
}
collect().then(function(v) { globalThis.__r = v });
`)
	if got != "6" { // 1+2+3
		t.Errorf("for await asyncIterable sum = %q, want 6", got)
	}
}

// TestForAwaitFallbackToSyncIterator: 数组无 asyncIterator，回退到
// Symbol.iterator；每次 next() 返回普通对象，由 OpAwait 经 promiseResolve 包装。
func TestForAwaitFallbackToSyncIterator(t *testing.T) {
	got := vmEvalPromise(t, `
async function foo() {
  var parts = [];
  for await (var x of [10, 20, 30]) {
    parts.push(x);
  }
  return parts.join(",");
}
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "10,20,30" {
		t.Errorf("for await over array = %q, want 10,20,30", got)
	}
}

// TestForAwaitStringFallback: 字符串同样回退到同步迭代器。
func TestForAwaitStringFallback(t *testing.T) {
	got := vmEvalPromise(t, `
async function foo() {
  var s = "";
  for await (var c of "abc") {
    s += c;
  }
  return s;
}
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "abc" {
		t.Errorf("for await over string = %q, want abc", got)
	}
}

// TestForAwaitRejectCaught: 异步迭代器 next() 返回 rejected Promise，
// 经 await 抛出后应能被循环体外层的 try/catch 捕获。
func TestForAwaitRejectCaught(t *testing.T) {
	got := vmEvalPromise(t, `
var it = {
  [Symbol.asyncIterator]() {
    var i = 0;
    return {
      next: function() {
        i++;
        if (i === 2) {
          return Promise.reject("boom");
        }
        if (i > 3) {
          return Promise.resolve({ value: undefined, done: true });
        }
        return Promise.resolve({ value: i, done: false });
      }
    };
  }
};
async function foo() {
  var collected = [];
  try {
    for await (var v of it) {
      collected.push(v);
    }
  } catch (e) {
    collected.push("err:" + e);
  }
  return collected.join(",");
}
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "1,err:boom" {
		t.Errorf("for await reject = %q, want 1,err:boom", got)
	}
}

// TestForAwaitBreak: break 提前终止循环。
func TestForAwaitBreak(t *testing.T) {
	got := vmEvalPromise(t, `
function asyncOf(arr) {
  return {
    [Symbol.asyncIterator]() {
      var i = 0;
      return {
        next: function() {
          if (i < arr.length) {
            return Promise.resolve({ value: arr[i++], done: false });
          }
          return Promise.resolve({ value: undefined, done: true });
        }
      };
    }
  };
}
async function foo() {
  var got = [];
  for await (var x of asyncOf([1,2,3,4,5])) {
    if (x === 3) break;
    got.push(x);
  }
  return got.join(",");
}
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "1,2" {
		t.Errorf("for await break = %q, want 1,2", got)
	}
}

// TestForAwaitDestructuring: 解构绑定循环变量。
func TestForAwaitDestructuring(t *testing.T) {
	got := vmEvalPromise(t, `
async function foo() {
  var sum = 0;
  for await (var {a, b} of [{a:1,b:2}, {a:3,b:4}]) {
    sum += a + b;
  }
  return sum;
}
foo().then(function(v) { globalThis.__r = v });
`)
	if got != "10" { // (1+2)+(3+4)
		t.Errorf("for await destructuring = %q, want 10", got)
	}
}

// TestForAwaitOutsideAsync: 在非 async 函数内使用 for await 应报语法错误。
func TestForAwaitOutsideAsync(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}
	_, err = vm.Eval(`function foo() { for await (var x of [1,2,3]) x; }`, "test.js")
	if err == nil {
		t.Errorf("for await outside async should be a syntax error")
	}
}
