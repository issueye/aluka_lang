package interpreter

import "testing"

// 本文件覆盖 v18 修复：return/break/continue 穿出 try/finally 区域时必须
// 先运行 finally（此前字节码 VM 直接跳过 finally，导致 proper-lockfile 等
// `try { ... return ... } finally { release() }` 模式的资源永远不被释放）。
// 风格对齐 try_catch_test.go：vmEvalStr + 表驱动。

// TestReturnRunsFinally: try 内 return 时 finally 必须执行，且返回值正确。
func TestReturnRunsFinally(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		// 基础：return 穿出 finally（两次调用验证幂等）
		{`var log=[]; function f(){ try { log.push("t"); return 1 } finally { log.push("f") } } f(); log.join(",") + ":" + f()`, "t,f:1"},
		// finally 语句在 return 之前执行（顺序）
		{`var log=[]; function f(){ try { log.push("a"); return "r" } finally { log.push("b") } } var v=f(); log.join("+") + ":" + v`, "a+b:r"},
		// finally 内 return 覆盖 try 的 return（ES 语义）
		{`function f(){ try { return 1 } finally { return 2 } } f()`, "2"},
		{`function f(){ try { return "try" } finally { return "finally" } } f()`, "finally"},
		// return 无参数（undefined）+ finally
		{`var log=[]; function f(){ try { return } finally { log.push("f") } } var v=f(); log.join(",") + ":" + v`, "f:undefined"},
		// catch 内 return 也需运行 finally
		{`var log=[]; function f(){ try { throw new Error("e") } catch(e) { log.push("c"); return "caught" } finally { log.push("f") } } var v=f(); log.join(",") + ":" + v`, "c,f:caught"},
		// finally 内异常覆盖 pending return
		{`var log=[]; function f(){ try { return 1 } finally { log.push("f"); throw new Error("boom") } } var v; try { v = f() } catch(e) { v = "err:" + e.message } log.join(",") + ":" + v`, "f:err:boom"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// TestNestedTryReturnRunsFinallys: 嵌套 try/finally，return 按内→外顺序运行所有 finally。
func TestNestedTryReturnRunsFinallys(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		// 内层 finally 先于外层
		{`var log=[]; function f(){ try { try { return 1 } finally { log.push("inner") } } finally { log.push("outer") } } f(); log.join(",")`, "inner,outer"},
		// 内层 finally 的 return 覆盖外层 try 的 return，但外层 finally 仍运行
		{`var log=[]; function f(){ try { try { return "inner-try" } finally { log.push("i"); return "inner-finally" } } finally { log.push("o") } } var v=f(); log.join(",") + ":" + v`, "i,o:inner-finally"},
		// 三层嵌套
		{`var log=[]; function f(){ try { try { try { return 3 } finally { log.push("f3") } } finally { log.push("f2") } } finally { log.push("f1") } } f(); log.join(",")`, "f3,f2,f1"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// TestBreakContinueRunsFinally: break/continue 穿出 try 区域时 finally 必须执行。
func TestBreakContinueRunsFinally(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		// break 穿出（try 在循环内）
		{`var log=[]; for(var i=0;i<3;i++){ try { if(i===1) break; log.push("t"+i) } finally { log.push("f"+i) } } log.join(",")`, "t0,f0,f1"},
		// continue 穿出
		{`var log=[]; for(var i=0;i<3;i++){ try { if(i===1) continue; log.push("t"+i) } finally { log.push("f"+i) } } log.join(",")`, "t0,f0,f1,t2,f2"},
		// 嵌套 try：内层 break 穿出两层，两个 finally 都运行
		{`var log=[]; for(var i=0;i<3;i++){ try { try { if(i===1) break; log.push("t"+i) } finally { log.push("i"+i) } } finally { log.push("o"+i) } } log.join(",")`, "t0,i0,o0,i1,o1"},
		// break 目标在 try 区域内（try 在循环外、break 在循环内）：finally 在区域
		// 正常退出时运行一次，不得提前/重复运行
		{`var log=[]; try { for(var i=0;i<3;i++){ if(i===1) break; log.push("t"+i) } log.push("after-loop") } finally { log.push("f") } log.join(",")`, "t0,after-loop,f"},
		// 循环在 try 内且 break 在嵌套 try 内：最终外层 finally 仍运行
		{`var log=[]; try { for(var i=0;i<3;i++){ try { if(i===1) break; log.push("t"+i) } finally { log.push("i"+i) } } } finally { log.push("o") } log.join(",")`, "t0,i0,i1,o"},
		// 带标签的 break 同样运行 finally
		{`var log=[]; outer: for(var i=0;i<3;i++){ for(var j=0;j<3;j++){ try { if(j===1) break outer; log.push(i+"-"+j) } finally { log.push("f"+i) } } } log.join(",")`, "0-0,f0,f0"},
		// while 循环 continue
		{`var log=[]; var k=0; while(k<3){ k++; try { if(k===2) continue; log.push("t"+k) } finally { log.push("f"+k) } } log.join(",")`, "t1,f1,f2,t3,f3"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// TestTryExitJmpNoStaleHandler: break/continue 穿出 try 后，handler 不得残留，
// 区域外的异常不能被残留 handler 捕获。
func TestTryExitJmpNoStaleHandler(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		// break 穿出 try/catch 后，循环外异常不能被残留 handler 捕获
		{`var out=[]; for(var i=0;i<3;i++){ try { out.push("t"+i); break } catch(e) { out.push("caught") } } out.push("after"); try { throw new Error("x") } catch(e) { out.push("outer") } out.join(",")`, "t0,after,outer"},
		// continue 穿出后同样
		{`var out=[]; var k=0; while(k<3){ k++; try { if(k===2) continue; out.push("t"+k) } catch(e) { out.push("caught") } } try { throw new Error("y") } catch(e) { out.push("outer") } out.join(",")`, "t1,t3,outer"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// TestAsyncReturnRunsFinally: async 函数 try 内 await + return，finally 必须执行。
func TestAsyncReturnRunsFinally(t *testing.T) {
	got := vmEvalPromise(t, `
var log = [];
async function f() {
  try {
    var x = await Promise.resolve(6);
    log.push("t");
    return x;
  } finally {
    log.push("f");
  }
}
f().then(function(v) { globalThis.__r = log.join(",") + ":" + v; });
`)
	if got != "t,f:6" {
		t.Errorf("async return through finally = %q, want %q", got, "t,f:6")
	}
}

// TestAsyncFinallyAwait: finally 内的 await 完成后才返回（pi 的 withLockAsync
// `try { ... return result } finally { await release() }` 模式）。
func TestAsyncFinallyAwait(t *testing.T) {
	got := vmEvalPromise(t, `
var log = [];
function asyncRelease() { return Promise.resolve().then(function() { log.push("release"); }); }
async function f() {
  try {
    log.push("try");
    return "ok";
  } finally {
    await asyncRelease();
  }
}
f().then(function(v) { globalThis.__r = log.join(",") + ":" + v; });
`)
	if got != "try,release:ok" {
		t.Errorf("async finally await = %q, want %q", got, "try,release:ok")
	}
}

// TestThrowInFinallyOverridesPendingReturn: finally 内 throw 覆盖 try 的 return
// （与异常在 finally 中抛出的语义一致）。
func TestThrowInFinallyOverridesPendingReturn(t *testing.T) {
	got := vmEvalStr(t, `
var log=[];
function f() {
  try {
    log.push("t");
    return "try-ret";
  } finally {
    log.push("f");
    throw new Error("finally-boom");
  }
}
var result;
try { result = f(); } catch(e) { result = "err:" + e.message; }
log.join(",") + ":" + result
`)
	if got != "t,f:err:finally-boom" {
		t.Errorf("throw in finally = %q, want %q", got, "t,f:err:finally-boom")
	}
}

// TestReturnInCatchWithFinally: catch 内 return + finally 的正常路径。
func TestReturnInCatchWithFinally(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{`var log=[]; function f(){ try { throw "e" } catch(e) { log.push("c"); return "r" } finally { log.push("f") } } var v=f(); log.join(",")+":"+v`, "c,f:r"},
		// finally 内 return 覆盖 catch 的 return
		{`var log=[]; function f(){ try { throw "e" } catch(e) { return "c-r" } finally { return "f-r" } } f()`, "f-r"},
	}
	for _, c := range cases {
		got := vmEvalStr(t, c.code)
		if got != c.want {
			t.Errorf("Eval(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}
