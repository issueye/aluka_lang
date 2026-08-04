package builtin

// node:repl 内置模块（开发计划 3.13）。
// start(options) 启动内部 REPL（读行-求值-打印循环）。

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
		if len(args) > 0 && args[0].IsObject() {
			if o, ok := args[0].AsObject(); ok {
				if v, err := o.Get("prompt"); err == nil && !v.IsUndefined() {
					prompt = v.String()
				}
			}
		}

		// 读行-求值-打印循环。
		reader := bufio.NewReader(os.Stdin)
		_ = replObj.Set("setPrompt", engine.NewFunction("setPrompt", func(a []engine.Value) (engine.Value, error) {
			if len(a) > 0 {
				prompt = a[0].String()
			}
			return replObj, nil
		}))
		_ = replObj.Set("close", engine.NewFunction("close", func(a []engine.Value) (engine.Value, error) {
			return replObj, nil
		}))

		// 主循环（阻塞；在 CLI 交互场景运行）。
		for {
			fmt.Print(prompt)
			line, err := reader.ReadString('\n')
			if err != nil {
				fmt.Println()
				break
			}
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if line == ".exit" || line == "exit" {
				break
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
