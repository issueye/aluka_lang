package builtin

// node:repl 内置模块（开发计划 3.13）。
// start(options) 启动内部 REPL（读行-求值-打印循环）。
// REPLServer 方法面：defineCommand/displayPrompt/clearBufferedCommand/
// setupHistory/setPrompt/close/context。

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewReplModule 构造 node:repl 模块导出对象。
func NewReplModule(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	_ = m.Set("start", engine.NewFunction("start", func(args []engine.Value) (engine.Value, error) {
		replObj := engine.NewObject()
		prompt := "> "
		var evalFn engine.Value
		if len(args) > 0 && args[0].IsObject() {
			if o, ok := args[0].AsObject(); ok {
				if v, err := o.Get("prompt"); err == nil && !v.IsUndefined() {
					prompt = v.String()
				}
				if v, err := o.Get("eval"); err == nil && v.IsFunction() {
					evalFn = v
				}
			}
		}

		// REPLServer 方法面。
		_ = replObj.Set("setPrompt", engine.NewFunction("setPrompt", func(a []engine.Value) (engine.Value, error) {
			if len(a) > 0 {
				prompt = a[0].String()
			}
			return replObj, nil
		}))
		_ = replObj.Set("getPrompt", engine.NewFunction("getPrompt", func(a []engine.Value) (engine.Value, error) {
			return engine.Str(prompt), nil
		}))
		_ = replObj.Set("displayPrompt", engine.NewFunction("displayPrompt", func(a []engine.Value) (engine.Value, error) {
			fmt.Print(prompt)
			return replObj, nil
		}))
		_ = replObj.Set("defineCommand", engine.NewFunction("defineCommand", func(a []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
		_ = replObj.Set("clearBufferedCommand", engine.NewFunction("clearBufferedCommand", func(a []engine.Value) (engine.Value, error) {
			return replObj, nil
		}))
		_ = replObj.Set("setupHistory", engine.NewFunction("setupHistory", func(a []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
		_ = replObj.Set("close", engine.NewFunction("close", func(a []engine.Value) (engine.Value, error) {
			return replObj, nil
		}))
		_ = replObj.Set("context", ctx.Global())

		// 主循环（阻塞；CLI 交互场景运行）。stdin EOF 时退出（不额外换行，
		// Node 行为：提示符后直接结束）。
		reader := bufio.NewReader(os.Stdin)
		for {
			fmt.Print(prompt)
			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if line == ".exit" || line == "exit" {
				break
			}
			if evalFn.IsFunction() {
				// 自定义 eval：cb(null, result)。
				if f, ok := evalFn.AsFunction(); ok {
					cb := engine.NewFunction("replCallback", func(ca []engine.Value) (engine.Value, error) {
						if len(ca) > 1 && !ca[1].IsUndefined() {
							fmt.Println(ca[1].String())
						}
						return engine.Undefined(), nil
					})
					_, _ = f.Call([]engine.Value{engine.Str(line), ctx.Global(), engine.Str("[repl]"), cb})
				}
				continue
			}
			result, evalErr := ctx.Eval(line, "[repl]")
			if evalErr != nil {
				fmt.Println("Error:", evalErr)
				continue
			}
			if !result.IsUndefined() && !result.IsNull() {
				fmt.Println(result.String())
			}
		}
		return replObj, nil
	}))

	return m, nil
}
