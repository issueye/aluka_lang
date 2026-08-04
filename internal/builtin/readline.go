package builtin

// node:readline 内置模块（开发计划 3.12）。
// createInterface({input, output})：question() 打印提示并读取 stdin 一行，
// 基于 EventEmitter 触发 'line'/'close'。

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewReadline 构造 node:readline 模块导出对象。
func NewReadline(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	_ = m.Set("createInterface", engine.NewFunction("createInterface", func(args []engine.Value) (engine.Value, error) {
		rl := newEmitterInstance().(engine.Object)
		var output engine.Value
		if len(args) > 0 {
			if o, ok := args[0].AsObject(); ok {
				if v, err := o.Get("output"); err == nil && !v.IsUndefined() {
					output = v
				}
			}
		}

		// question(query, callback)：打印提示 + 读一行。
		_ = rl.Set("question", engine.NewFunction("question", func(qargs []engine.Value) (engine.Value, error) {
			query := ""
			var cb engine.Value
			if len(qargs) > 0 {
				query = qargs[0].String()
			}
			if len(qargs) > 1 && qargs[1].IsFunction() {
				cb = qargs[1]
			}
			// 输出 query（到 output 对象或 stdout）。
			if output != nil {
				if o, ok := output.AsObject(); ok {
					if w, err := o.Get("write"); err == nil && w.IsFunction() {
						if f, ok := w.AsFunction(); ok {
							_, _ = f.Call([]engine.Value{engine.Str(query)})
						}
					}
				}
			} else {
				fmt.Print(query)
			}
			reader := bufio.NewReader(os.Stdin)
			line, err := reader.ReadString('\n')
			if err != nil {
				emitEvent(rl, "close")
				return rl, nil
			}
			line = strings.TrimRight(line, "\r\n")
			if cb != nil {
				if f, ok := cb.AsFunction(); ok {
					_, _ = f.Call([]engine.Value{engine.Str(line)})
				}
			}
			emitEvent(rl, "line", engine.Str(line))
			return rl, nil
		}))

		// pause/resume/close/setPrompt：no-op 或链式。
		noop := func(args []engine.Value) (engine.Value, error) { return rl, nil }
		_ = rl.Set("pause", engine.NewFunction("pause", noop))
		_ = rl.Set("resume", engine.NewFunction("resume", noop))
		_ = rl.Set("setPrompt", engine.NewFunction("setPrompt", noop))
		_ = rl.Set("close", engine.NewFunction("close", func(a []engine.Value) (engine.Value, error) {
			emitEvent(rl, "close")
			return rl, nil
		}))
		return rl, nil
	}))

	return m, nil
}
