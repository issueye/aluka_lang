package noderepl

// node:readline/promises 内置模块——Promise 版 readline。
// createInterface/Readline.question 返回 Promise（Node ≥ 17）。
// 从 options.input 流读取一行（'data'/'end' 事件）；未提供 input 时回退 os.Stdin。
// Readline 类提供 question/commit/rollback API 面。

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/aluka-lang/aluka/internal/builtin/nodebase"
	"github.com/aluka-lang/aluka/internal/builtin/nodeevents"
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// NewReadlinePromises 构造 node:readline/promises 模块导出对象。
func NewReadlinePromises(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// createInterface(options) → Interface（promise question）。
	_ = m.Set("createInterface", engine.NewFunction("createInterface", func(args []engine.Value) (engine.Value, error) {
		return makePromiseReadline(ctx, args)
	}))

	// Interface 类：createInterface 返回的对象类型（可 new Interface(options)）。
	interfaceCtor := engine.NewFunction("Interface", func(args []engine.Value) (engine.Value, error) {
		return makePromiseReadline(ctx, args)
	})
	if co, ok := interfaceCtor.AsObject(); ok {
		_ = co.Set("prototype", engine.NewObject())
	}
	_ = m.Set("Interface", interfaceCtor)

	// Readline 类：question/commit/rollback。
	readlineProto := engine.NewObject()
	_ = readlineProto.Set("commit", engine.NewFunction("commit", func(a []engine.Value) (engine.Value, error) {
		return nodebase.PromiseResolved(ctx, engine.Undefined())
	}))
	_ = readlineProto.Set("rollback", engine.NewFunction("rollback", func(a []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = readlineProto.Set("clearLine", engine.NewFunction("clearLine", func(a []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = readlineProto.Set("clearScreenDown", engine.NewFunction("clearScreenDown", func(a []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = readlineProto.Set("cursorTo", engine.NewFunction("cursorTo", func(a []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = readlineProto.Set("moveCursor", engine.NewFunction("moveCursor", func(a []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))

	readlineCtor := engine.NewFunction("Readline", func(args []engine.Value) (engine.Value, error) {
		inst := nodeevents.NewEmitterInstance().(engine.Object)
		// Readline 类第一个参数是流（输入与输出共用）：按实例闭包捕获。
		var input, output engine.Value
		if len(args) > 0 && args[0].IsObject() {
			input, output = args[0], args[0]
		}
		_ = inst.Set("question", engine.NewFunction("question", func(qargs []engine.Value) (engine.Value, error) {
			query := ""
			if len(qargs) > 0 {
				query = qargs[0].String()
			}
			return promiseReadLine(ctx, query, input, output)
		}))
		for _, k := range readlineProto.Keys() {
			if v, err := readlineProto.Get(k); err == nil {
				_ = inst.Set(k, v)
			}
		}
		return inst, nil
	})
	if co, ok := readlineCtor.AsObject(); ok {
		_ = co.Set("prototype", readlineProto)
	}
	_ = m.Set("Readline", readlineCtor)

	return m, nil
}

// rlInputKey / rlOutputKey：Interface/Readline 实例上保存的流引用。
const rlInputKey = "__aluka_rl_input"
const rlOutputKey = "__aluka_rl_output"

// makePromiseReadline 构造 createInterface 返回的 Interface（含 promise question）。
func makePromiseReadline(ctx engine.Context, args []engine.Value) (engine.Value, error) {
	rl := nodeevents.NewEmitterInstance().(engine.Object)
	// 解析 options：{ input, output }，按实例闭包捕获。
	var input, output engine.Value
	if len(args) > 0 {
		if o, ok := args[0].AsObject(); ok {
			if v, err := o.Get("input"); err == nil && !v.IsUndefined() {
				input = v
			}
			if v, err := o.Get("output"); err == nil && !v.IsUndefined() {
				output = v
			}
		}
	}
	_ = rl.Set(rlInputKey, input)
	_ = rl.Set(rlOutputKey, output)
	_ = rl.Set("question", engine.NewFunction("question", func(qargs []engine.Value) (engine.Value, error) {
		query := ""
		if len(qargs) > 0 {
			query = qargs[0].String()
		}
		return promiseReadLine(ctx, query, input, output)
	}))
	noop := func(args []engine.Value) (engine.Value, error) { return rl, nil }
	_ = rl.Set("pause", engine.NewFunction("pause", noop))
	_ = rl.Set("resume", engine.NewFunction("resume", noop))
	_ = rl.Set("close", engine.NewFunction("close", func(a []engine.Value) (engine.Value, error) {
		nodebase.EmitEvent(rl, "close")
		return rl, nil
	}))
	_ = rl.Set("setPrompt", engine.NewFunction("setPrompt", noop))
	return rl, nil
}

// promiseReadLine 打印 query 并异步读取一行，返回 Promise<string>。
// input 为流对象时经 'data'/'end' 事件消费；否则回退 os.Stdin。
func promiseReadLine(ctx engine.Context, query string, input, output engine.Value) (engine.Value, error) {
	promiseCtor, err := ctx.Global().Get("Promise")
	if err != nil || !promiseCtor.IsFunction() {
		return engine.Undefined(), fmt.Errorf("readline/promises: global Promise not available")
	}
	executor := engine.NewFunction("executor", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		resolve, reject := args[0], args[1]
		// 输出 query 到 output 流或 stdout。
		if output != nil {
			if o, ok := output.AsObject(); ok {
				if w, err := o.Get("write"); err == nil && w.IsFunction() {
					if f, ok := w.AsFunction(); ok {
						if _, err := f.Call([]engine.Value{engine.Str(query)}); err != nil {
							interpreter.ReportUncaught(nil, err)
						}
					}
				}
			}
		} else if query != "" {
			fmt.Print(query)
		}

		if input != nil && hasOnMethod(input) {
			consumeStreamLine(ctx, input, resolve, reject)
			return engine.Undefined(), nil
		}
		// 回退：读 os.Stdin。
		release := ctx.AddRef()
		go func() {
			reader := bufio.NewReader(os.Stdin)
			line, rerr := reader.ReadString('\n')
			ctx.PostTask(func() {
				defer release()
				if rerr != nil && line == "" {
					if f, ok := reject.AsFunction(); ok {
						if _, err := f.Call([]engine.Value{engine.Str("readline/promises: input closed")}); err != nil {
							interpreter.ReportUncaught(nil, err)
						}
					}
					return
				}
				line = strings.TrimRight(line, "\r\n")
				if f, ok := resolve.AsFunction(); ok {
					if _, err := f.Call([]engine.Value{engine.Str(line)}); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				}
			})
		}()
		return engine.Undefined(), nil
	})
	pf, ok := promiseCtor.AsFunction()
	if !ok {
		return engine.Undefined(), fmt.Errorf("readline/promises: Promise not callable")
	}
	return pf.Call([]engine.Value{executor})
}

// hasOnMethod 判断值是否为带 on 方法的流对象。
func hasOnMethod(v engine.Value) bool {
	if o, ok := v.AsObject(); ok {
		if fn, err := o.Get("on"); err == nil && fn.IsFunction() {
			return true
		}
	}
	return false
}

// consumeStreamLine 从流消费第一个换行（'data'/'end' 事件）。先注册 end 再 data
// （data 注册触发缓冲排空，同步结束的流能收到 end）。
func consumeStreamLine(ctx engine.Context, input engine.Value, resolve, reject engine.Value) {
	var buf strings.Builder
	settled := false
	release := ctx.AddRef()

	settle := func(fn engine.Value, arg engine.Value) {
		if settled {
			return
		}
		settled = true
		release()
		if f, ok := fn.AsFunction(); ok {
			if _, err := f.Call([]engine.Value{arg}); err != nil {
				interpreter.ReportUncaught(nil, err)
			}
		}
	}
	dataCb := engine.NewFunction("lineData", func(ca []engine.Value) (engine.Value, error) {
		if len(ca) > 0 && !ca[0].IsNull() {
			buf.WriteString(ca[0].String())
			if idx := strings.IndexByte(buf.String(), '\n'); idx >= 0 {
				line := strings.TrimRight(buf.String()[:idx], "\r")
				settle(resolve, engine.Str(line))
			}
		}
		return engine.Undefined(), nil
	})
	endCb := engine.NewFunction("lineEnd", func(ca []engine.Value) (engine.Value, error) {
		line := strings.TrimRight(buf.String(), "\r\n")
		settle(resolve, engine.Str(line))
		return engine.Undefined(), nil
	})
	errCb := engine.NewFunction("lineErr", func(ca []engine.Value) (engine.Value, error) {
		msg := "readline error"
		if len(ca) > 0 {
			msg = ca[0].String()
		}
		settle(reject, engine.Str(msg))
		return engine.Undefined(), nil
	})
	_, _ = nodeevents.CallEmitterMethod(input, "on", []engine.Value{engine.Str("end"), endCb})
	_, _ = nodeevents.CallEmitterMethod(input, "on", []engine.Value{engine.Str("error"), errCb})
	_, _ = nodeevents.CallEmitterMethod(input, "on", []engine.Value{engine.Str("data"), dataCb})
}
