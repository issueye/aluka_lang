package globals

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
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

	// execArgv / features / release / config（形状与 Node 对齐；值因运行时
	// 差异为已知差异）。
	_ = proc.Set("execArgv", engine.NewArray(nil))
	features := engine.NewObject()
	for _, k := range []string{"inspector", "ipv6", "tls_alpn", "tls_sni", "tls", "uv", "minimal"} {
		_ = features.Set(k, engine.Boolean(true))
	}
	_ = proc.Set("features", features)
	release := engine.NewObject()
	_ = release.Set("name", engine.Str("aluka"))
	_ = release.Set("lts", engine.Null())
	_ = proc.Set("release", release)
	_ = proc.Set("config", engine.NewObject())

	// M9-4：process.permission——Permission Model 的 has 方法面。
	// aluka 不实现权限模型（ADR: docs/adr/permissions-report.md），has() 恒
	// 返回 false（拒绝一切，等价于 Node --permission 且未授予任何 scope）。
	// knownDifference：Node 默认（无 --permission）时 process.permission 为
	// undefined，aluka 始终暴露该对象。
	permObj := engine.NewObject()
	_ = permObj.Set("has", engine.NewFunction("has", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(false), nil
	}))
	_ = proc.Set("permission", permObj)

	// M9-4：process.report——writeReport/getReport 方法面 + 属性面。
	// 默认值与 Node 一致；getReport 返回最小但形状一致的 report 对象，
	// writeReport 将 JSON 落盘并打印 Node 相同的提示行（stderr）。
	report := engine.NewObject()
	_ = report.Set("compact", engine.Boolean(false))
	_ = report.Set("directory", engine.Str(""))
	_ = report.Set("excludeEnv", engine.Boolean(false))
	_ = report.Set("excludeNetwork", engine.Boolean(false))
	_ = report.Set("filename", engine.Str(""))
	_ = report.Set("reportOnFatalError", engine.Boolean(false))
	_ = report.Set("reportOnSignal", engine.Boolean(false))
	_ = report.Set("reportOnUncaughtException", engine.Boolean(false))
	_ = report.Set("signal", engine.Str("SIGUSR2"))
	_ = report.Set("getReport", engine.NewFunction("getReport", func(a []engine.Value) (engine.Value, error) {
		return reportToJS(buildReportMap(cfg.Argv)), nil
	}))
	_ = report.Set("writeReport", engine.NewFunction("writeReport", func(a []engine.Value) (engine.Value, error) {
		// 文件名：默认生成 report.<ts>.<pid>.0.001.json（Node 格式），
		// 或显式传入的路径。
		path := ""
		if len(a) > 0 && !a[0].IsUndefined() && a[0].String() != "" {
			path = a[0].String()
		} else {
			dir := "."
			path = filepath.Join(dir, fmt.Sprintf("report.%s.%d.0.001.json", time.Now().Format("20060102.150405"), os.Getpid()))
		}
		data, err := json.MarshalIndent(buildReportMap(cfg.Argv), "", "  ")
		if err != nil {
			return engine.Undefined(), err
		}
		if werr := os.WriteFile(path, data, 0o644); werr != nil {
			return engine.Undefined(), werr
		}
		// Node 的提示输出走 stderr（含前导空行）。
		fmt.Fprintf(os.Stderr, "\nWriting Node.js report to file: %s\nNode.js report completed\n", path)
		return engine.Str(path), nil
	}))
	_ = proc.Set("report", report)


	// abort()：终止进程（Node 语义，SIGABRT）。
	_ = proc.Set("abort", engine.NewFunction("abort", func(args []engine.Value) (engine.Value, error) {
		fmt.Fprintln(os.Stderr, "Aborted")
		os.Exit(134)
		return engine.Undefined(), nil
	}))

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
		} else {
			// 无参数：使用 process.exitCode（Node 语义）。
			if ec, err := proc.Get("exitCode"); err == nil && !ec.IsUndefined() {
				if n, ok := ec.Int(); ok {
					code = n
				}
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

	// exitCode：退出码属性（默认 undefined；process.exit() 无参数时使用）。
	_ = proc.Set("exitCode", engine.Undefined())

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
	hrtimeVal := engine.NewFunction("hrtime", func(args []engine.Value) (engine.Value, error) {
		elapsed := time.Since(startTime)
		secs := int64(elapsed.Seconds())
		nanos := int64(elapsed.Nanoseconds() - secs*1e9)
		return engine.NewArray([]engine.Value{
			engine.IntValue(int(secs)),
			engine.IntValue(int(nanos)),
		}), nil
	})
	// hrtime.bigint()：返回自引用点起的纳秒数（BigInt，Node 语义）。
	if hrtimeObj, ok := hrtimeVal.AsObject(); ok {
		_ = hrtimeObj.Set("bigint", engine.NewFunction("bigint", func(args []engine.Value) (engine.Value, error) {
			elapsed := time.Since(startTime)
			return engine.BigInt(new(big.Int).SetInt64(elapsed.Nanoseconds())), nil
		}))
	}
	_ = proc.Set("hrtime", hrtimeVal)

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
		_ = mu.Set("arrayBuffers", engine.Number(0))
		return mu, nil
	}))

	// umask([mask])：读取/设置文件权限掩码（Windows 恒 0，Node 实测）。
	_ = proc.Set("umask", engine.NewFunction("umask", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 && !args[0].IsUndefined() {
			var mask int
			if n, ok := args[0].Int(); ok {
				mask = n
			} else if s, ok := args[0].Float(); ok {
				mask = int(s)
			}
			return engine.IntValue(setUmask(mask)), nil
		}
		return engine.IntValue(getUmask()), nil
	}))

	// cpuUsage([prevValue])：{user, system} 微秒；带 prev 时返回增量。
	_ = proc.Set("cpuUsage", engine.NewFunction("cpuUsage", func(args []engine.Value) (engine.Value, error) {
		user, system := getProcessUsage()
		if len(args) > 0 {
			if po, ok := args[0].AsObject(); ok {
				if pv, err := po.Get("user"); err == nil {
					if pf, ok2 := pv.Float(); ok2 {
						user -= int64(pf)
					}
				}
				if pv, err := po.Get("system"); err == nil {
					if pf, ok2 := pv.Float(); ok2 {
						system -= int64(pf)
					}
				}
			}
		}
		cu := engine.NewObject()
		_ = cu.Set("user", engine.Number(float64(user)))
		_ = cu.Set("system", engine.Number(float64(system)))
		return cu, nil
	}))

	// emitWarning(warning[, options] | warning[, type[, code]])：
	// Node 语义的进程警告（默认输出 stderr，含 [code] 与 type；同时触发
	// 'warning' 事件，监听器收到 {message, name, code, stack} 对象）。M2 供
	// EventEmitter 的 maxListeners 警告使用。
	_ = proc.Set("emitWarning", engine.NewFunction("emitWarning", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		msg := args[0].String()
		code := ""
		typ := "Warning"
		parse := func(v engine.Value) {
			if o, ok := v.AsObject(); ok {
				if vv, err := o.Get("type"); err == nil && !vv.IsUndefined() {
					typ = vv.String()
				}
				if vv, err := o.Get("code"); err == nil && !vv.IsUndefined() {
					code = vv.String()
				}
			}
		}
		if len(args) > 1 {
			if args[1].Type() == engine.TypeObject {
				parse(args[1])
			} else {
				typ = args[1].String()
			}
		}
		if len(args) > 2 {
			code = args[2].String()
		}
		// 构造 Warning 对象并异步触发 'warning' 事件（Node 语义：nextTick
		// 投递，emitWarning 调用点监听器尚未收到）。无监听器时直接打印
		// stderr；有监听器时由默认 'warning' 监听器打印（removeAllListeners
		// ('warning') 可静默）。
		wv, err := makeWarningObject(ctx, msg, typ, code)
		task := engine.NewFunction("emitWarningTask", func(a []engine.Value) (engine.Value, error) {
			emitted := false
			if err == nil {
				if emitVal, err2 := proc.Get("emit"); err2 == nil && emitVal.IsFunction() {
					if f, ok := emitVal.AsFunction(); ok {
						if rv, cerr := f.Call([]engine.Value{engine.Str("warning"), wv}); cerr == nil {
							if b, ok2 := rv.Bool(); ok2 {
								emitted = b
							}
						}
					}
				}
			}
			if !emitted {
				if code != "" {
					fmt.Fprintf(os.Stderr, "(aluka:%d) [%s] %s: %s\n", os.Getpid(), code, typ, msg)
				} else {
					fmt.Fprintf(os.Stderr, "(aluka:%d) %s: %s\n", os.Getpid(), typ, msg)
				}
			}
			return engine.Undefined(), nil
		})
		if q, qerr := ctx.Global().Get("queueMicrotask"); qerr == nil && q.IsFunction() {
			if f, ok := q.AsFunction(); ok {
				_, _ = f.Call([]engine.Value{task})
				return engine.Undefined(), nil
			}
		}
		// 无微任务队列兜底：同步执行。
		_, _ = task.Call(nil)
		return engine.Undefined(), nil
	}))

	// on / emit / removeListener / once：SIGINT/SIGTERM/SIGHUP/SIGBREAK 等信号
	// 事件实际触发（os/signal → PostTask → JS 监听器）；其余事件为普通注册。
	// 监听器存 JS 函数值（支持 removeListener/once 的身份比较）。
	listeners := make(map[string][]engine.Value)
	sigCh := make(chan os.Signal, 8)
	go func() {
		for sig := range sigCh {
			name := sigName(sig)
			ctx.PostTask(func() {
				for _, fn := range listeners[name] {
					if f, ok := fn.AsFunction(); ok {
						_, _ = f.Call(nil)
					}
				}
			})
		}
	}()
	_ = proc.Set("on", engine.NewFunction("on", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		event := args[0].String()
		if !args[1].IsFunction() {
			return engine.Undefined(), nil
		}
		listeners[event] = append(listeners[event], args[1])
		// 注册可监听信号：启动 os/signal 通知（SIGTSTP/SIGCONT 等
		// Windows 不支持，注册不生效——与 Node 行为一致）。
		if sig, ok := goSignalByName(event); ok {
			osSignalNotify(sigCh, sig)
		}
		return engine.Undefined(), nil
	}))
	_ = proc.Set("once", engine.NewFunction("once", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		event := args[0].String()
		original := args[1]
		if !original.IsFunction() {
			return engine.Undefined(), nil
		}
		// wrapper：首次触发调用原监听器并自删。
		var wrapper engine.Value
		wrapper = engine.NewFunction("onceWrapper", func(ca []engine.Value) (engine.Value, error) {
			if f, ok := original.AsFunction(); ok {
				_, _ = f.Call(ca)
			}
			l := listeners[event]
			for i, x := range l {
				if x == wrapper {
					listeners[event] = append(append([]engine.Value{}, l[:i]...), l[i+1:]...)
					break
				}
			}
			return engine.Undefined(), nil
		})
		listeners[event] = append(listeners[event], wrapper)
		if sig, ok := goSignalByName(event); ok {
			osSignalNotify(sigCh, sig)
		}
		return engine.Undefined(), nil
	}))
	_ = proc.Set("removeListener", engine.NewFunction("removeListener", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		event := args[0].String()
		target := args[1]
		l := listeners[event]
		for i, x := range l {
			if x == target {
				listeners[event] = append(append([]engine.Value{}, l[:i]...), l[i+1:]...)
				return engine.Undefined(), nil
			}
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
			if f, ok := fn.AsFunction(); ok {
				_, _ = f.Call(args[1:])
			}
		}
		return engine.Boolean(len(fns) > 0), nil
	}))

	// removeAllListeners([event])：移除某事件全部监听器（Node 语义）。
	_ = proc.Set("removeAllListeners", engine.NewFunction("removeAllListeners", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			for e := range listeners {
				listeners[e] = nil
			}
			return proc, nil
		}
		listeners[args[0].String()] = nil
		return proc, nil
	}))

	// listenerCount([event])：返回监听器数量。
	_ = proc.Set("listenerCount", engine.NewFunction("listenerCount", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.IntValue(0), nil
		}
		return engine.IntValue(len(listeners[args[0].String()])), nil
	}))

	// 默认 'warning' 监听器：打印 stderr（Node 语义——emitWarning 经
	// 'warning' 事件输出，removeAllListeners('warning') 可静默）。
	defaultWarning := engine.NewFunction("defaultWarning", func(ca []engine.Value) (engine.Value, error) {
		var msg, typ, code string
		typ = "Warning"
		if len(ca) > 0 {
			if o, ok := ca[0].AsObject(); ok {
				if v, err := o.Get("message"); err == nil && !v.IsUndefined() {
					msg = v.String()
				}
				if v, err := o.Get("name"); err == nil && !v.IsUndefined() {
					typ = v.String()
				}
				if v, err := o.Get("code"); err == nil && !v.IsUndefined() {
					code = v.String()
				}
			}
		}
		if code != "" {
			fmt.Fprintf(os.Stderr, "(aluka:%d) [%s] %s: %s\n", os.Getpid(), code, typ, msg)
		} else {
			fmt.Fprintf(os.Stderr, "(aluka:%d) %s: %s\n", os.Getpid(), typ, msg)
		}
		return engine.Undefined(), nil
	})
	listeners["warning"] = []engine.Value{defaultWarning}

	return ctx.Global().Set("process", proc)
}

// makeWarningObject 构造 process 'warning' 事件负载对象（Node 语义：
// {message, name, code, stack}；name = type）。
func makeWarningObject(ctx engine.Context, msg, typ, code string) (engine.Value, error) {
	var obj engine.Object
	if ctor, err := ctx.Global().Get("Error"); err == nil && ctor.IsFunction() {
		if f, ok := ctor.AsFunction(); ok {
			if v, cerr := f.Call([]engine.Value{engine.Str(msg)}); cerr == nil {
				obj, _ = v.AsObject()
			}
		}
	}
	if obj == nil {
		obj = engine.NewObject()
		_ = obj.Set("message", engine.Str(msg))
	}
	_ = obj.Set("name", engine.Str(typ))
	_ = obj.Set("type", engine.Str(typ))
	if code != "" {
		_ = obj.Set("code", engine.Str(code))
	}
	if _, err := obj.Get("stack"); err != nil {
		_ = obj.Set("stack", engine.Str(typ+": "+msg))
	}
	return obj, nil
}

// buildReportMap 构造 process.report.getReport() / writeReport() 使用的
// report 数据（形状对齐 Node 22：header + 各 section 键）。动态值仅用于
// 展示，不承诺与 Node 逐字段一致。
func buildReportMap(argv []string) map[string]interface{} {
	cwd, _ := os.Getwd()
	cmdLine := make([]string, len(argv))
	copy(cmdLine, argv)
	host, _ := os.Hostname()
	header := map[string]interface{}{
		"reportVersion":   5,
		"event":           "JavaScript API",
		"trigger":         "getReport",
		"filename":        "",
		"dumpEventTime":   time.Now().Format(time.RFC3339),
		"dumpEventTimeStamp": time.Now().UnixMilli(),
		"processId":       os.Getpid(),
		"threadId":        0,
		"cwd":             cwd,
		"commandLine":     cmdLine,
		"nodejsVersion":   "v0.1.0-aluka",
		"wordSize":        64,
		"arch":            archName(),
		"platform":        platformName(),
		"componentVersions": map[string]interface{}{
			"aluka": "0.1.0",
			"go":    runtime.Version(),
		},
		"release": map[string]interface{}{
			"name": "aluka",
			"lts":  nil,
		},
		"osName":     runtime.GOOS,
		"osRelease":  runtime.GOOS,
		"osVersion":  runtime.GOOS,
		"osMachine":  runtime.GOARCH,
		"cpus":       []interface{}{},
		"networkInterfaces": map[string]interface{}{},
		"host":       host,
	}
	return map[string]interface{}{
		"header":               header,
		"javascriptStack":      map[string]interface{}{"message": "No stack trace was produced", "stack": []interface{}{}},
		"javascriptHeap":       map[string]interface{}{},
		"nativeStack":          []interface{}{},
		"resourceUsage":        map[string]interface{}{},
		"uvthreadResourceUsage": map[string]interface{}{},
		"libuv":                []interface{}{},
		"workers":              []interface{}{},
		"environmentVariables": map[string]interface{}{},
		"sharedObjects":        []interface{}{},
	}
}

// reportToJS 将 report 数据（map/切片/标量）转换为 engine.Value。
func reportToJS(v interface{}) engine.Value {
	switch t := v.(type) {
	case map[string]interface{}:
		obj := engine.NewObject()
		for k, val := range t {
			_ = obj.Set(k, reportToJS(val))
		}
		return obj
	case []interface{}:
		vals := make([]engine.Value, len(t))
		for i, e := range t {
			vals[i] = reportToJS(e)
		}
		return engine.NewArray(vals)
	case string:
		return engine.Str(t)
	case bool:
		return engine.Boolean(t)
	case int:
		return engine.IntValue(t)
	case int64:
		return engine.IntValue(int(t))
	case float64:
		return engine.Number(t)
	case nil:
		return engine.Null()
	default:
		return engine.Undefined()
	}
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
