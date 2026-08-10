package builtin

// node:readline 内置模块（开发计划 3.12）。
// createInterface({input, output})：question() 打印提示并读取 stdin 一行，
// 基于 EventEmitter 触发 'line'/'close'；补齐 Interface 方法面
// （prompt/getPrompt/write/getCursorPos）与顶层工具函数。

import (
	"bufio"
	"fmt"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"os"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewReadline 构造 node:readline 模块导出对象。
func NewReadline(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// 顶层工具函数（终端控制，差分环境无 TTY，no-op）。
	mkNoop := func(name string) engine.Value {
		return engine.NewFunction(name, func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		})
	}
	_ = m.Set("emitKeypressEvents", mkNoop("emitKeypressEvents"))
	_ = m.Set("clearLine", mkNoop("clearLine"))
	_ = m.Set("clearScreenDown", mkNoop("clearScreenDown"))
	_ = m.Set("cursorTo", mkNoop("cursorTo"))
	_ = m.Set("moveCursor", mkNoop("moveCursor"))

	_ = m.Set("createInterface", engine.NewFunction("createInterface", func(args []engine.Value) (engine.Value, error) {
		rl := newEmitterInstance().(engine.Object)
		var output engine.Value
		terminal := true
		if len(args) > 0 {
			if o, ok := args[0].AsObject(); ok {
				if v, err := o.Get("output"); err == nil && !v.IsUndefined() {
					output = v
				}
				if v, err := o.Get("terminal"); err == nil && !v.IsUndefined() {
					if b, ok2 := v.Bool(); ok2 {
						terminal = b
					}
				}
			}
		}
		_ = rl.Set("terminal", engine.Boolean(terminal))
		_ = rl.Set("line", engine.Str(""))

		// writePrompt 把提示符写到 output（Node 语义：prompt()/question()）。
		writePrompt := func(s string) {
			if output != nil {
				if o, ok := output.AsObject(); ok {
					if w, err := o.Get("write"); err == nil && w.IsFunction() {
						if f, ok := w.AsFunction(); ok {
							if _, err := f.Call([]engine.Value{engine.Str(s)}); err != nil {
								interpreter.ReportUncaught(nil, err)
							}
							return
						}
					}
				}
			}
			fmt.Print(s)
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
			writePrompt(query)
			reader := bufio.NewReader(os.Stdin)
			line, err := reader.ReadString('\n')
			if err != nil {
				emitEvent(rl, "close")
				return rl, nil
			}
			line = strings.TrimRight(line, "\r\n")
			_ = rl.Set("line", engine.Str(line))
			if cb != nil {
				if f, ok := cb.AsFunction(); ok {
					if _, err := f.Call([]engine.Value{engine.Str(line)}); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				}
			}
			emitEvent(rl, "line", engine.Str(line))
			return rl, nil
		}))

		// prompt()：输出提示符（不读行）。
		prompt := ""
		_ = rl.Set("setPrompt", engine.NewFunction("setPrompt", func(a []engine.Value) (engine.Value, error) {
			if len(a) > 0 {
				prompt = a[0].String()
			}
			return rl, nil
		}))
		_ = rl.Set("getPrompt", engine.NewFunction("getPrompt", func(a []engine.Value) (engine.Value, error) {
			return engine.Str(prompt), nil
		}))
		_ = rl.Set("prompt", engine.NewFunction("prompt", func(a []engine.Value) (engine.Value, error) {
			writePrompt(prompt)
			return engine.Undefined(), nil
		}))
		_ = rl.Set("write", engine.NewFunction("write", func(a []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
		_ = rl.Set("getCursorPos", engine.NewFunction("getCursorPos", func(a []engine.Value) (engine.Value, error) {
			pos := engine.NewObject()
			_ = pos.Set("rows", engine.IntValue(0))
			_ = pos.Set("cols", engine.IntValue(0))
			return pos, nil
		}))

		// pause/resume/close：链式或触发 close。
		noop := func(args []engine.Value) (engine.Value, error) { return rl, nil }
		_ = rl.Set("pause", engine.NewFunction("pause", noop))
		_ = rl.Set("resume", engine.NewFunction("resume", noop))
		_ = rl.Set("close", engine.NewFunction("close", func(a []engine.Value) (engine.Value, error) {
			emitEvent(rl, "close")
			return rl, nil
		}))
		return rl, nil
	}))

	return m, nil
}
