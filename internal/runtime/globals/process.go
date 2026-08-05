package globals

import (
	"fmt"
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
// 实现的 API：
//   - process.argv             string[]
//   - process.argv0            string
//   - process.execPath         string
//   - process.title            string
//   - process.env              object
//   - process.platform         string  ("linux" | "darwin" | "win32")
//   - process.arch              string  ("x64" | "arm64" | ...)
//   - process.pid               number
//   - process.ppid              number
//   - process.cwd()             string
//   - process.chdir(dir)        void
//   - process.exit(code)        never (触发 'exit' 监听器后调用 os.Exit)
//   - process.nextTick(fn)      void（复用全局 queueMicrotask）
//   - process.kill(pid[, sig])  void
//   - process.stdout            { write, writeSync }
//   - process.stderr            { write, writeSync }
//   - process.stdin             { readable, on }
//   - process.versions           object  ({ aluka, go, v8 })
//   - process.version            string
//   - process.hrtime()          [number, number]
//   - process.uptime()           number
//   - process.memoryUsage()     { rss, heapTotal, heapUsed, external }
//   - process.on(event, fn)     暂存回调；'exit' 在 process.exit 时触发
//   - process.emit(event, ...args)
func NewProcess(ctx engine.Context, cfg ProcessConfig) error {
	proc := engine.NewObject()

	// argv / argv0 / execPath
	argv := cfg.Argv
	if argv == nil {
		argv = os.Args
	}
	argvVals := make([]engine.Value, len(argv))
	for i, a := range argv {
		argvVals[i] = engine.Str(a)
	}
	_ = proc.Set("argv", engine.NewArray(argvVals))
	argv0 := ""
	if len(argv) > 0 {
		argv0 = argv[0]
	}
	_ = proc.Set("argv0", engine.Str(argv0))
	if exe, err := os.Executable(); err == nil {
		_ = proc.Set("execPath", engine.Str(exe))
	}
	_ = proc.Set("title", engine.Str("aluka"))

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

	// exit：触发 'exit' 监听器后退出进程。
	_ = proc.Set("exit", engine.NewFunction("exit", func(args []engine.Value) (engine.Value, error) {
		code := 0
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				code = n
			}
		}
		// 触发 'exit' 事件（同步，带退出码）。
		if emitVal, err := proc.Get("emit"); err == nil && emitVal.IsFunction() {
			if f, ok := emitVal.AsFunction(); ok {
				_, _ = f.Call([]engine.Value{engine.Str("exit"), engine.IntValue(code)})
			}
		}
		os.Exit(code)
		return engine.Undefined(), nil
	}))

	// nextTick(fn)：复用全局 queueMicrotask（engine 层已注册）。
	_ = proc.Set("nextTick", engine.NewFunction("nextTick", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !args[0].IsFunction() {
			return engine.Undefined(), nil
		}
		if q, err := ctx.Global().Get("queueMicrotask"); err == nil && q.IsFunction() {
			if f, ok := q.AsFunction(); ok {
				_, _ = f.Call([]engine.Value{args[0]})
			}
		}
		return engine.Undefined(), nil
	}))

	// kill(pid[, signal])：发信号（默认 SIGTERM；Windows 仅支持终止）。
	_ = proc.Set("kill", engine.NewFunction("kill", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		pid := argInt(args, 0, -1)
		if pid <= 0 {
			return engine.Undefined(), fmt.Errorf("%w: invalid pid %d", engine.ErrRangeError, pid)
		}
		sigName := "SIGTERM"
		if len(args) > 1 && !args[1].IsUndefined() {
			sigName = args[1].String()
		}
		p, err := os.FindProcess(pid)
		if err != nil {
			return engine.Undefined(), err
		}
		// 跨平台简化：Windows 仅支持 Kill（SIGTERM/SIGKILL 等价）；其他
		// 平台按信号名发送（SIGTERM/SIGKILL/SIGINT 等）。
		if sig, ok := goSignalByName(sigName); ok && runtime.GOOS != "windows" {
			return engine.Undefined(), p.Signal(sig)
		}
		return engine.Undefined(), p.Kill()
	}))

	// stdin（简化只读流）。
	stdin := engine.NewObject()
	_ = stdin.Set("readable", engine.Boolean(true))
	_ = stdin.Set("isTTY", engine.Undefined())
	_ = stdin.Set("on", engine.NewFunction("on", func(args []engine.Value) (engine.Value, error) {
		// 简化：不读取输入，仅返回自身（链式兼容）。
		return stdin, nil
	}))
	_ = stdin.Set("setEncoding", engine.NewFunction("setEncoding", func(args []engine.Value) (engine.Value, error) {
		return stdin, nil
	}))
	_ = proc.Set("stdin", stdin)

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

	// on / emit：SIGINT/SIGTERM/SIGHUP/SIGBREAK 等信号事件实际触发
	// （os/signal → PostTask → JS 监听器）；其余事件为普通注册。
	listeners := make(map[string][]engine.Func)
	sigCh := make(chan os.Signal, 8)
	go func() {
		for sig := range sigCh {
			name := sigName(sig)
			ctx.PostTask(func() {
				fns := listeners[name]
				for _, fn := range fns {
					_, _ = fn(nil)
				}
			})
		}
	}()
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
		// 注册可监听信号：启动 os/signal 通知（SIGTSTP/SIGCONT 等
		// Windows 不支持，注册不生效——与 Node 行为一致）。
		if sig, ok := goSignalByName(event); ok {
			osSignalNotify(sigCh, sig)
		}
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

