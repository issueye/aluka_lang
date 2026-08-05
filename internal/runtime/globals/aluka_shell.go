package globals

// Aluka.$ 跨平台 shell（Phase 4 WBS 4.9）。
//
// 简化实现：不经 mvdan.cc/sh 解析，而是按平台选择 shell 直接执行：
//   - Windows：cmd /C <script>
//   - 其他：sh -c <script>
//
// `Aluka.$`("cmd") 返回 Promise<ShellOutput>：
//   { stdout, stderr, exitCode, text(), json() }

import (
	"bytes"
	"os/exec"
	"runtime"

	"github.com/aluka-lang/aluka/internal/engine"
)

// alukaRegisterShell 注册 Aluka.$。
func alukaRegisterShell(ctx engine.Context, aluka engine.Value) {
	ao, _ := aluka.AsObject()
	_ = ao.Set("$", engine.NewFunction("$", func(args []engine.Value) (engine.Value, error) {
		// 同时支持两种形式（P1-2）：
		//   Aluka.$("cmd arg")          函数调用形式
		//   Aluka.$`cmd arg`            标记模板形式（args[0] 为 TemplateStringsArray）
		script := ""
		if len(args) > 0 {
			if arr, ok := args[0].(*engine.ArrayValue); ok {
				// 模板数组：取第一个 quasis 字符串（无插值时）。
				elems := arr.Elems()
				if len(elems) > 0 {
					script = elems[0].String()
				}
			} else {
				script = args[0].String()
			}
		}
		executor := engine.NewFunction("executor", func(ea []engine.Value) (engine.Value, error) {
			if len(ea) == 0 {
				return engine.Undefined(), nil
			}
			resolve := ea[0]
			release := ctx.AddRef()
			go func() {
				var stdout, stderr bytes.Buffer
				var cmd *exec.Cmd
				if runtime.GOOS == "windows" {
					cmd = exec.Command("cmd", "/C", script)
				} else {
					cmd = exec.Command("sh", "-c", script)
				}
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				err := cmd.Run()
				exitCode := 0
				if err != nil {
					if ee, ok := err.(*exec.ExitError); ok {
						exitCode = ee.ExitCode()
					}
				}
				outText := stdout.String()
				errText := stderr.String()
				ctx.PostTask(func() {
					defer release()
					out := engine.NewObject()
					_ = out.Set("stdout", engine.Str(outText))
					_ = out.Set("stderr", engine.Str(errText))
					_ = out.Set("exitCode", engine.IntValue(exitCode))
					_ = out.Set("text", engine.NewFunction("text", func(a []engine.Value) (engine.Value, error) {
						return promiseResolveValue(ctx, engine.Str(outText))
					}))
					_ = out.Set("json", engine.NewFunction("json", func(a []engine.Value) (engine.Value, error) {
						jsonGlobal, err := ctx.Global().Get("JSON")
						if err != nil || !jsonGlobal.IsObject() {
							return promiseRejectValue(ctx, "JSON not available")
						}
						jo, _ := jsonGlobal.AsObject()
						if pf, err := jo.Get("parse"); err == nil && pf.IsFunction() {
							if f, ok := pf.AsFunction(); ok {
								v, perr := f.Call([]engine.Value{engine.Str(outText)})
								if perr != nil {
									return promiseRejectValue(ctx, perr.Error())
								}
								return promiseResolveValue(ctx, v)
							}
						}
						return promiseRejectValue(ctx, "JSON.parse failed")
					}))
					callResolve(resolve, out)
				})
			}()
			return engine.Undefined(), nil
		})
		return newPromise(ctx, executor)
	}))
}
