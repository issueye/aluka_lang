package globals

// Aluka.spawn / spawnSync（Phase 4 WBS 4.15）。
//
//   - Aluka.spawn(cmd, opts?) → Subprocess（pid/stdout/stderr/exited/kill）
//     cmd 为字符串（按空格拆分）或字符串数组；stdout/stderr 是 ReadableStream。
//   - Aluka.spawnSync(cmd, opts?) → {stdout, stderr, exitCode}

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// alukaRegisterSpawn 注册进程相关 API。
func alukaRegisterSpawn(ctx engine.Context, aluka engine.Value) {
	ao, _ := aluka.AsObject()

	_ = ao.Set("spawn", engine.NewFunction("spawn", func(args []engine.Value) (engine.Value, error) {
		argv, env, dir := alukaSpawnArgs(args)
		if len(argv) == 0 {
			return engine.Undefined(), nil
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Env = env
		cmd.Dir = dir
		stdoutPipe, err := cmd.StdoutPipe()
		if err != nil {
			return engine.Undefined(), err
		}
		stderrPipe, err := cmd.StderrPipe()
		if err != nil {
			return engine.Undefined(), err
		}
		if err := cmd.Start(); err != nil {
			return engine.Undefined(), err
		}

		proc := engine.NewObject()
		_ = proc.Set("pid", engine.IntValue(cmd.Process.Pid))
		_ = proc.Set("stdout", alukaPipeStream(ctx, stdoutPipe))
		_ = proc.Set("stderr", alukaPipeStream(ctx, stderrPipe))
		_ = proc.Set("exited", alukaExitedPromise(ctx, cmd))
		_ = proc.Set("kill", engine.NewFunction("kill", func(a []engine.Value) (engine.Value, error) {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			return engine.Undefined(), nil
		}))
		return proc, nil
	}))

	_ = ao.Set("spawnSync", engine.NewFunction("spawnSync", func(args []engine.Value) (engine.Value, error) {
		argv, env, dir := alukaSpawnArgs(args)
		if len(argv) == 0 {
			return engine.Undefined(), nil
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Env = env
		cmd.Dir = dir
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		exitCode := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				exitCode = ee.ExitCode()
			}
		}
		out := engine.NewObject()
		_ = out.Set("stdout", NewBufferInstance(stdout.Bytes()))
		_ = out.Set("stderr", NewBufferInstance(stderr.Bytes()))
		_ = out.Set("exitCode", engine.IntValue(exitCode))
		return out, nil
	}))
}

// alukaSpawnArgs 解析 cmd 与 opts：argv/env/dir。
func alukaSpawnArgs(args []engine.Value) ([]string, []string, string) {
	var argv []string
	if len(args) > 0 && args[0] != nil {
		if a, ok := args[0].(*engine.ArrayValue); ok {
			for _, e := range a.Elems() {
				argv = append(argv, e.String())
			}
		} else {
			s := strings.TrimSpace(args[0].String())
			if s != "" {
				argv = strings.Fields(s)
			}
		}
	}
	env := osEnvironSlice()
	dir := ""
	if len(args) > 1 && args[1].IsObject() {
		if o, ok := args[1].AsObject(); ok {
			if v, err := o.Get("cwd"); err == nil && !v.IsUndefined() && v.String() != "" {
				dir = v.String()
			}
			if v, err := o.Get("env"); err == nil && v.IsObject() {
				if eo, ok := v.AsObject(); ok {
					merged := make([]string, 0, len(env)+len(eo.Keys()))
					// 保留当前环境并覆盖传入项。
					used := map[string]bool{}
					for _, kv := range env {
						if i := strings.IndexByte(kv, '='); i > 0 {
							if _, present := eo.Get(kv[:i]); present == nil {
								merged = append(merged, kv)
								used[kv[:i]] = true
							}
						}
					}
					for _, k := range eo.Keys() {
						if !used[k] {
							if v, err := eo.Get(k); err == nil {
								merged = append(merged, k+"="+v.String())
							}
						}
					}
					env = merged
				}
			}
		}
	}
	return argv, env, dir
}

// osEnvironSlice 返回当前环境变量切片。
func osEnvironSlice() []string {
	return append([]string(nil), os.Environ()...)
}

// alukaPipeStream 把 io.Reader 包装为 ReadableStream（数据经 PostTask 推入）。
func alukaPipeStream(ctx engine.Context, r io.Reader) engine.Value {
	var controller engine.Value
	stream, err := newReadableStream(ctx, []engine.Value{engine.NewObjectFrom(map[string]engine.Value{
		"start": engine.NewFunction("start", func(a []engine.Value) (engine.Value, error) {
			if len(a) > 0 {
				controller = a[0]
			}
			return engine.Undefined(), nil
		}),
	})})
	if err != nil {
		return engine.NewObject()
	}
	release := ctx.AddRef()
	go func() {
		defer release()
		buf := make([]byte, 16*1024)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				ctx.PostTask(func() {
					if c, ok := controller.AsObject(); ok {
						if e, err := c.Get("enqueue"); err == nil && e.IsFunction() {
							if f, ok := e.AsFunction(); ok {
								_, _ = f.Call([]engine.Value{NewBufferInstance(chunk)})
							}
						}
					}
				})
			}
			if err != nil {
				ctx.PostTask(func() {
					if c, ok := controller.AsObject(); ok {
						if cl, err := c.Get("close"); err == nil && cl.IsFunction() {
							if f, ok := cl.AsFunction(); ok {
								_, _ = f.Call(nil)
							}
						}
					}
				})
				return
			}
		}
	}()
	return stream
}

// alukaExitedPromise 构造进程退出 Promise。
func alukaExitedPromise(ctx engine.Context, cmd *exec.Cmd) engine.Value {
	executor := engine.NewFunction("executor", func(ea []engine.Value) (engine.Value, error) {
		if len(ea) < 2 {
			return engine.Undefined(), nil
		}
		resolve, reject := ea[0], ea[1]
		release := ctx.AddRef()
		go func() {
			defer release()
			err := cmd.Wait()
			exitCode := 0
			if err != nil {
				if ee, ok := err.(*exec.ExitError); ok {
					exitCode = ee.ExitCode()
				} else {
					ctx.PostTask(func() {
						callResolve(reject, engine.Str("Aluka.spawn: "+err.Error()))
					})
					return
				}
			}
			ctx.PostTask(func() {
				callResolve(resolve, engine.IntValue(exitCode))
			})
		}()
		return engine.Undefined(), nil
	})
	p, _ := newPromise(ctx, executor)
	return p
}
