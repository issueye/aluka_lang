package builtin

// node:child_process 内置模块（开发计划 3.8）。
// spawn/exec/execFile/fork。子进程在 Go goroutine 运行，stdout/stderr
// 经 PostTask 回 JS 线程触发 'data'；退出触发 'exit'。

import (
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

// NewChildProcess 构造 node:child_process 模块导出对象。
func NewChildProcess(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	_ = m.Set("spawn", engine.NewFunction("spawn", func(args []engine.Value) (engine.Value, error) {
		return spawnChild(ctx, args), nil
	}))
	_ = m.Set("exec", engine.NewFunction("exec", func(args []engine.Value) (engine.Value, error) {
		execChild(ctx, args)
		return engine.Undefined(), nil
	}))
	_ = m.Set("execFile", engine.NewFunction("execFile", func(args []engine.Value) (engine.Value, error) {
		execFileChild(ctx, args)
		return engine.Undefined(), nil
	}))
	_ = m.Set("fork", engine.NewFunction("fork", func(args []engine.Value) (engine.Value, error) {
		return forkChild(ctx, args), nil
	}))

	// 同步三件套（spawnSync/execFileSync/execSync）。
	registerChildProcessSync(m)

	return m, nil
}

// spawnChild 实现 child_process.spawn。
// options 支持 silent（fork 用）：silent:false 时继承 stdio（Node fork 默认）。
func spawnChild(ctx engine.Context, args []engine.Value) engine.Value {
	cp := newEmitterInstance().(engine.Object)

	command := ""
	var cmdArgs []string
	if len(args) > 0 {
		command = args[0].String()
	}
	if len(args) > 1 {
		if a, ok := args[1].(*engine.ArrayValue); ok {
			for _, e := range a.Elems() {
				cmdArgs = append(cmdArgs, e.String())
			}
		}
	}

	// fork 的 silent/env 选项（缺省 = 管道；fork 显式传 {silent:false} 继承
	// stdio，并携带 env）。
	var envList []string
	inheritStdio := false
	if len(args) > 2 && args[2].IsObject() {
		if o, ok := args[2].AsObject(); ok {
			if v, err := o.Get("silent"); err == nil && !v.IsUndefined() {
				if b, ok2 := v.Bool(); ok2 {
					inheritStdio = !b
				}
			}
			if v, err := o.Get("env"); err == nil && !v.IsUndefined() {
				if eo, ok2 := v.AsObject(); ok2 {
					for _, k := range eo.Keys() {
						if ev, err3 := eo.Get(k); err3 == nil {
							envList = append(envList, k+"="+ev.String())
						}
					}
				}
			}
		}
	}

	cmd := exec.Command(command, cmdArgs...)
	if envList != nil {
		cmd.Env = envList
	}

	// stdout/stderr 流（管道模式）。
	var stdout, stderr engine.Object
	var stdoutPipe, stderrPipe io.ReadCloser
	if inheritStdio {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cp.Set("stdout", engine.Null())
		_ = cp.Set("stderr", engine.Null())
		_ = cp.Set("stdin", engine.Null())
	} else {
		stdout = newEmitterInstance().(engine.Object)
		stderr = newEmitterInstance().(engine.Object)
		installChildReadable := func(stream engine.Object, pipe *io.ReadCloser) {
			_ = stream.Set("readable", engine.Boolean(true))
			_ = stream.Set("readableEnded", engine.Boolean(false))
			_ = stream.Set("destroyed", engine.Boolean(false))
			var destroyOnce sync.Once
			_ = stream.Set("destroy", engine.NewFunction("destroy", func(a []engine.Value) (engine.Value, error) {
				destroyOnce.Do(func() {
					_ = stream.Set("destroyed", engine.Boolean(true))
					if *pipe != nil {
						_ = (*pipe).Close()
					}
				})
				return stream, nil
			}))
		}
		installChildReadable(stdout, &stdoutPipe)
		installChildReadable(stderr, &stderrPipe)
		_ = cp.Set("stdout", stdout)
		_ = cp.Set("stderr", stderr)
		stdin := engine.NewObject()
		_ = stdin.Set("write", engine.NewFunction("write", func(a []engine.Value) (engine.Value, error) {
			return engine.Boolean(false), nil // 简化：不支持写 stdin
		}))
		_ = stdin.Set("end", engine.NewFunction("end", func(a []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
		_ = cp.Set("stdin", stdin)
	}

	if !inheritStdio {
		var err error
		stdoutPipe, err = cmd.StdoutPipe()
		if err != nil {
			emitEvent(cp, "error", engine.Str(err.Error()))
			return cp
		}
		stderrPipe, err = cmd.StderrPipe()
		if err != nil {
			emitEvent(cp, "error", engine.Str(err.Error()))
			return cp
		}

		readPipe := func(pipe io.ReadCloser, stream engine.Object) {
			defer func() {
				ctx.PostTask(func() {
					_ = stream.Set("readable", engine.Boolean(false))
					_ = stream.Set("readableEnded", engine.Boolean(true))
					emitEvent(stream, "end")
					emitEvent(stream, "close")
				})
			}()
			buf := make([]byte, 4096)
			for {
				n, rerr := pipe.Read(buf)
				if n > 0 {
					data := append([]byte(nil), buf[:n]...)
					ctx.PostTask(func() {
						emitEvent(stream, "data", globals.NewBufferInstance(data))
					})
				}
				if rerr != nil {
					break
				}
			}
		}
		go readPipe(stdoutPipe, stdout)
		go readPipe(stderrPipe, stderr)
	}

	// 子进程计入事件循环活跃度（运行期间保持进程存活）。
	release := ctx.AddRef()
	pid := 0
	spawnErr := cmd.Start()
	if spawnErr != nil {
		ctx.PostTask(func() {
			emitEvent(cp, "error", engine.Str(spawnErr.Error()))
			emitEvent(cp, "exit", engine.IntValue(-1), engine.Null())
			release()
		})
		return cp
	}
	pid = cmd.Process.Pid
	_ = cp.Set("pid", engine.IntValue(pid))
	ctx.PostTask(func() {
		emitEvent(cp, "spawn")
	})

	// 等待退出。
	go func() {
		werr := cmd.Wait()
		code := 0
		if werr != nil {
			if ee, ok := werr.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				code = -1
			}
		}
		ctx.PostTask(func() {
			emitEvent(cp, "exit", engine.IntValue(code), engine.Null())
			emitEvent(cp, "close", engine.IntValue(code), engine.Null())
			release()
		})
	}()

	// kill(signal)
	_ = cp.Set("kill", engine.NewFunction("kill", func(a []engine.Value) (engine.Value, error) {
		if cmd.Process != nil {
			return engine.Boolean(cmd.Process.Kill() == nil), nil
		}
		return engine.Boolean(false), nil
	}))

	return cp
}

// execChild 实现 child_process.exec（shell 执行 + 收集输出）。
func execChild(ctx engine.Context, args []engine.Value) {
	if len(args) == 0 {
		return
	}
	command := args[0].String()
	var cb engine.Value
	if len(args) > 1 && args[1].IsFunction() {
		cb = args[1]
	} else if len(args) > 2 && args[2].IsFunction() {
		cb = args[2]
	}

	release := ctx.AddRef()
	go func() {
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.Command("cmd", "/c", command)
		} else {
			cmd = exec.Command("sh", "-c", command)
		}
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		ctx.PostTask(func() {
			defer release()
			if cb != nil && cb.IsFunction() {
				if f, ok := cb.AsFunction(); ok {
					var errVal engine.Value = engine.Null()
					if err != nil {
						errVal = engine.Str(err.Error())
					}
					_, _ = f.Call([]engine.Value{errVal, engine.Str(stdout.String()), engine.Str(stderr.String())})
				}
			}
		})
	}()
}

// execFileChild 实现 child_process.execFile。
func execFileChild(ctx engine.Context, args []engine.Value) {
	if len(args) == 0 {
		return
	}
	file := args[0].String()
	var fileArgs []string
	argIdx := 1
	if len(args) > 1 {
		if a, ok := args[1].(*engine.ArrayValue); ok {
			for _, e := range a.Elems() {
				fileArgs = append(fileArgs, e.String())
			}
			argIdx = 2
		}
	}
	var cb engine.Value
	for i := argIdx; i < len(args); i++ {
		if args[i].IsFunction() {
			cb = args[i]
			break
		}
	}

	release := ctx.AddRef()
	go func() {
		cmd := exec.Command(file, fileArgs...)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		ctx.PostTask(func() {
			defer release()
			if cb != nil && cb.IsFunction() {
				if f, ok := cb.AsFunction(); ok {
					var errVal engine.Value = engine.Null()
					if err != nil {
						errVal = engine.Str(err.Error())
					}
					_, _ = f.Call([]engine.Value{errVal, engine.Str(stdout.String()), engine.Str(stderr.String())})
				}
			}
		})
	}()
}

// forkChild 实现 child_process.fork（spawn 当前可执行文件跑模块）。
// fork 默认 silent:false（子进程 stdout/stderr 继承，Node 语义）。
func forkChild(ctx engine.Context, args []engine.Value) engine.Value {
	modulePath := ""
	if len(args) > 0 {
		modulePath = args[0].String()
	}
	var forkArgs []string
	if len(args) > 1 {
		if a, ok := args[1].(*engine.ArrayValue); ok {
			for _, e := range a.Elems() {
				forkArgs = append(forkArgs, e.String())
			}
		}
	}
	// silent 选项（fork 支持 {silent:true} 管道输出）。
	silent := false
	var envObj engine.Value = engine.Undefined()
	if len(args) > 2 && args[2].IsObject() {
		if o, ok := args[2].AsObject(); ok {
			if v, err := o.Get("silent"); err == nil && !v.IsUndefined() {
				if b, ok2 := v.Bool(); ok2 {
					silent = b
				}
			}
			if v, err := o.Get("env"); err == nil && !v.IsUndefined() {
				envObj = v
			}
		}
	}
	exe, _ := os.Executable()
	spawnArgs := append([]string{modulePath}, forkArgs...)
	opts := engine.NewObject()
	_ = opts.Set("silent", engine.Boolean(silent))
	if !envObj.IsUndefined() {
		_ = opts.Set("env", envObj)
	}
	return spawnChild(ctx, []engine.Value{
		engine.Str(exe),
		engine.NewArray(stringsToValues(spawnArgs)),
		opts,
	})
}

// stringsToValues 把字符串切片转成 engine.Value 数组。
func stringsToValues(ss []string) []engine.Value {
	out := make([]engine.Value, len(ss))
	for i, s := range ss {
		out[i] = engine.Str(s)
	}
	return out
}
