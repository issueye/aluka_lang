package interpreter

// 未捕获 JS 异常上报（Node 'uncaughtException' 语义）。
//
// 定时器（setTimeout/setInterval/setImmediate）、process.nextTick、
// queueMicrotask 以及 IO 监听器等"已调度回调"抛出的 JS 异常没有天然的
// 上层 try/catch 可捕获。Node 会将其作为 uncaughtException 派发给
// process 的 'uncaughtException' 事件（有监听器则交给监听器，无则打印并
// 退出）。aluka 此前在各回调处用 `_, _ = f.Call(...)` 静默丢弃——pi 等
// TUI 应用因此会在渲染出错时"界面冻结且无任何提示"。
//
// 本文件提供包级钩子 UncaughtExceptionHandler：runtime/globals 的
// NewProcess 注册它来派发 'uncaughtException'；未注册时回退到 stderr 打印
// （与 unhandledRejection 的既有兜底一致，不退出进程）。

import (
	"fmt"
	"os"

	"github.com/aluka-lang/aluka/internal/engine"
)

// UncaughtExceptionHandler 处理未捕获的 JS 回调异常。reason 是抛出/拒绝的
// JS 值（Error 对象）。nil 时 ReportUncaught 回退到 stderr 打印。
// 由 runtime/gproc.NewProcess 注册为 process 'uncaughtException' 派发。
var UncaughtExceptionHandler func(reason engine.Value)

// ReportUncaught 报告一次未捕获的 JS 回调异常（Node uncaughtException 语义）。
// ctx 用于从 VM 取解释器（把普通 Go 错误包装成 JS Error）；*jsThrow 直接
// 透传，不需要 ctx。ctx 可为 nil。
func ReportUncaught(ctx engine.Context, err error) {
	var interp *Interpreter
	if vm, ok := ctx.(*VM); ok {
		interp = vm.interp
	}
	reportUncaught(interp, err)
}

// reportUncaught 内部实现：把 err 还原成 JS 值，交给 UncaughtExceptionHandler
// 或回退到 stderr 打印。interp 可为 nil（此时普通 Go 错误以字符串呈现，
// 避免 extractThrowValue 在无 Error 构造器时解引用）。
func reportUncaught(interp *Interpreter, err error) {
	if err == nil {
		return
	}
	var reason engine.Value
	if jt, ok := err.(*jsThrow); ok {
		reason = jt.val
	} else if interp != nil {
		reason = extractThrowValue(err, interp)
	} else {
		reason = engine.Str(err.Error())
	}
	if UncaughtExceptionHandler != nil {
		UncaughtExceptionHandler(reason)
		return
	}
	PrintUncaught(reason)
}

// PrintUncaught 把未捕获异常值打印到 stderr（Node 风格：首行摘要 + 错误
// 对象的 .stack）。供 ReportUncaught 兜底与 globals 的无监听器路径复用。
func PrintUncaught(reason engine.Value) {
	fmt.Fprintf(os.Stderr, "Uncaught %s\n", reason.String())
	if obj, ok := reason.AsObject(); ok {
		if stack, err := obj.Get("stack"); err == nil && !stack.IsUndefined() && stack.String() != "" {
			fmt.Fprintln(os.Stderr, stack.String())
		}
	}
}
