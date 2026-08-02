package globals

import (
	"os"
	"runtime"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
)

// ProcessConfig 配置 process 全局对象。
type ProcessConfig struct {
	// Argv 显式覆盖 process.argv（用于测试）。
	// 若为 nil，使用 os.Args。
	Argv []string
	// Env 显式覆盖 process.env。
	// 若为 nil，使用 os.Environ()。
	Env map[string]string
}

// NewProcess 创建 process 全局对象并注册到 ctx。
// 实现的 API（Phase 0 子集）：
//   - process.argv             string[]
//   - process.env              object
//   - process.platform         string  ("linux" | "darwin" | "win32")
//   - process.arch              string  ("x64" | "arm64" | ...)
//   - process.pid               number
//   - process.ppid              number  (暂固定为 0)
//   - process.cwd()             string
//   - process.chdir(dir)        void
//   - process.exit(code)        never (调用 os.Exit)
//   - process.stdout            { write, writeSync }
//   - process.stderr            { write, writeSync }
//   - process.versions           object  ({ aluka, go, v8 })
//   - process.version            string
//   - process.hrtime()          [number, number]
//   - process.uptime()           number
//   - process.memoryUsage()     { rss, heapTotal, heapUsed, external }
//   - process.on(event, fn)     (暂存回调不触发)
//   - process.emit(event, ...args)
func NewProcess(ctx engine.Context, cfg ProcessConfig) error {
	proc := engine.NewObject()

	// argv
	argv := cfg.Argv
	if argv == nil {
		argv = os.Args
	}
	argvVals := make([]engine.Value, len(argv))
	for i, a := range argv {
		argvVals[i] = engine.Str(a)
	}
	_ = proc.Set("argv", engine.NewArray(argvVals))

	// env
	env := cfg.Env
	if env == nil {
		env = make(map[string]string)
		for _, kv := range os.Environ() {
			for i := 0; i < len(kv); i++ {
				if kv[i] == '=' {
					env[kv[:i]] = kv[i+1:]
					break
				}
			}
		}
	}
	envObj := engine.NewObject()
	for k, v := range env {
		_ = envObj.Set(k, engine.Str(v))
	}
	_ = proc.Set("env", envObj)

	// platform / arch
	_ = proc.Set("platform", engine.Str(platformName()))
	_ = proc.Set("arch", engine.Str(archName()))

	// pid / ppid
	_ = proc.Set("pid", engine.IntValue(os.Getpid()))
	_ = proc.Set("ppid", engine.IntValue(os.Getppid()))

	// version / versions
	_ = proc.Set("version", engine.Str("v0.1.0-aluka"))
	versions := engine.NewObject()
	_ = versions.Set("aluka", engine.Str("0.1.0"))
	_ = versions.Set("go", engine.Str(runtime.Version()))
	_ = versions.Set("v8", engine.Str("n/a"))
	_ = versions.Set("typescript", engine.Str("n/a"))
	_ = proc.Set("versions", versions)

	// cwd / chdir
	_ = proc.Set("cwd", engine.NewFunction("cwd", func(args []engine.Value) (engine.Value, error) {
		wd, err := os.Getwd()
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Str(wd), nil
	}))
	_ = proc.Set("chdir", engine.NewFunction("chdir", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		return engine.Undefined(), os.Chdir(args[0].String())
	}))

	// exit
	_ = proc.Set("exit", engine.NewFunction("exit", func(args []engine.Value) (engine.Value, error) {
		code := 0
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				code = n
			}
		}
		os.Exit(code)
		return engine.Undefined(), nil
	}))

	// stdout / stderr（提供 write/writeSync）
	makeStream := func(w *os.File) engine.Object {
		obj := engine.NewObject()
		writeFn := func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 {
				return engine.Boolean(false), nil
			}
			data := args[0].String()
			_, err := w.WriteString(data)
			if err != nil {
				return engine.Boolean(false), err
			}
			return engine.Boolean(true), nil
		}
		_ = obj.Set("write", engine.NewFunction("write", func(args []engine.Value) (engine.Value, error) {
			// Phase 0 简化为同步写入
			return writeFn(args)
		}))
		_ = obj.Set("writeSync", engine.NewFunction("writeSync", writeFn))
		_ = obj.Set("fd", engine.IntValue(int(w.Fd())))
		return obj
	}
	_ = proc.Set("stdout", makeStream(os.Stdout))
	_ = proc.Set("stderr", makeStream(os.Stderr))

	// hrtime
	startTime := time.Now()
	_ = proc.Set("hrtime", engine.NewFunction("hrtime", func(args []engine.Value) (engine.Value, error) {
		elapsed := time.Since(startTime)
		secs := int64(elapsed.Seconds())
		nanos := int64(elapsed.Nanoseconds() - secs*1e9)
		return engine.NewArray([]engine.Value{
			engine.IntValue(int(secs)),
			engine.IntValue(int(nanos)),
		}), nil
	}))

	// uptime
	_ = proc.Set("uptime", engine.NewFunction("uptime", func(args []engine.Value) (engine.Value, error) {
		return engine.Number(time.Since(startTime).Seconds()), nil
	}))

	// memoryUsage（返回 Go runtime 的近似值）
	_ = proc.Set("memoryUsage", engine.NewFunction("memoryUsage", func(args []engine.Value) (engine.Value, error) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		mu := engine.NewObject()
		_ = mu.Set("rss", engine.Number(float64(m.Sys)))
		_ = mu.Set("heapTotal", engine.Number(float64(m.HeapAlloc)))
		_ = mu.Set("heapUsed", engine.Number(float64(m.HeapInuse)))
		_ = mu.Set("external", engine.Number(float64(m.HeapSys-m.HeapInuse)))
		return mu, nil
	}))

	// on / emit（暂为空 stub，不实际触发）
	listeners := make(map[string][]engine.Func)
	_ = proc.Set("on", engine.NewFunction("on", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		event := args[0].String()
		fn, ok := args[1].AsFunction()
		if !ok {
			return engine.Undefined(), nil
		}
		listeners[event] = append(listeners[event], fn.Call)
		return engine.Undefined(), nil
	}))
	_ = proc.Set("emit", engine.NewFunction("emit", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		event := args[0].String()
		fns := listeners[event]
		for _, fn := range fns {
			_, _ = fn(args[1:])
		}
		return engine.Boolean(len(fns) > 0), nil
	}))

	return ctx.Global().Set("process", proc)
}

// platformName 返回 Node.js 风格的平台名。
func platformName() string {
	switch runtime.GOOS {
	case "linux":
		return "linux"
	case "darwin":
		return "darwin"
	case "windows":
		return "win32"
	case "freebsd":
		return "freebsd"
	default:
		return runtime.GOOS
	}
}

// archName 返回 Node.js 风格的架构名。
func archName() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	case "386":
		return "ia32"
	default:
		return runtime.GOARCH
	}
}
