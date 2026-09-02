// Package globals 提供 JS 全局对象的实现。
// 包括 console、process、Buffer、timers 等。
package gconsole

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// ConsoleConfig 配置 console 输出目标。
type ConsoleConfig struct {
	Stdout io.Writer
	Stderr io.Writer
	// Now 返回当前时间（用于 time/timeEnd）。
	Now func() time.Time
}

// NewConsole 创建 console 全局对象并注册到 ctx。
// 实现的 API：
//   - console.log(...args)
//   - console.info(...args)
//   - console.warn(...args)
//   - console.error(...args)
//   - console.debug(...args)   // 等同于 log
//   - console.dir(value)       // 同 log
//   - console.table(data)      // 简单表格输出
//   - console.trace(...args)   // 打印调用栈
//   - console.time(label)
//   - console.timeEnd(label)
//   - console.timeLog(label, ...args)
//   - console.group(...args)   // 缩进分组
//   - console.groupEnd()       // 退出分组
//   - console.count(label) / console.countReset(label)
//   - console.assert(cond, ...args)
//   - console.clear()
//   - console.profile/profileEnd/timeStamp（Node 中为 no-op）
//   - console.Console 构造器：new Console({stdout, stderr}) 或旧式
//     new Console(stdout, stderr)；实例方法为自有属性闭包（与 Node 22
//     实例自有方法一致）。
func NewConsole(ctx engine.Context, cfg ConsoleConfig) error {
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	// Console 构造器 + prototype。stdout 必填（Node 抛
	// ERR_CONSOLE_WRITABLE_STREAM 同型 TypeError）；stderr 缺省回退 stdout。
	proto := engine.NewObject()
	ctor := engine.NewFunction("Console", func(args []engine.Value) (engine.Value, error) {
		var stdout, stderr io.Writer
		if len(args) > 0 && args[0].IsObject() {
			o, _ := args[0].AsObject()
			if wv, err := o.Get("write"); err == nil && wv.IsFunction() {
				// 位置参数形式：Console(stdout[, stderr])——首参为可写流
				// （有 write 方法，Node isStream 判别）。
				if w, werr := streamWriter(args[0]); werr == nil {
					stdout = w
				}
				if len(args) > 1 {
					if w, werr := streamWriter(args[1]); werr == nil {
						stderr = w
					}
				}
			} else {
				// 配置对象形式：{stdout, stderr}。
				if v, err := o.Get("stdout"); err == nil && !v.IsUndefined() && !v.IsNull() {
					if w, werr := streamWriter(v); werr == nil {
						stdout = w
					}
				}
				if v, err := o.Get("stderr"); err == nil && !v.IsUndefined() && !v.IsNull() {
					if w, werr := streamWriter(v); werr == nil {
						stderr = w
					}
				}
			}
		}
		if stdout == nil {
			return engine.Undefined(), fmt.Errorf("%w: Console expects a writable stream instance", engine.ErrTypeError)
		}
		if stderr == nil {
			stderr = stdout
		}
		inst := newConsoleInstance(stdout, stderr, time.Now)
		engine.SetProto(inst, proto)
		return inst, nil
	})
	_ = proto.Set("constructor", ctor)
	if ctorObj, ok := ctor.AsObject(); ok {
		_ = ctorObj.Set("prototype", proto)
	}

	// 全局 console 实例。
	console := newConsoleInstance(cfg.Stdout, cfg.Stderr, cfg.Now)
	engine.SetProto(console, proto)
	_ = console.Set("Console", ctor)
	// prototype 镜像方法（Node 的 Console.prototype.log 等存在，供
	// 'assert' in console.Console.prototype 等形状检查；实例自有属性优先）。
	for _, k := range console.Keys() {
		if v, err := console.Get(k); err == nil && v.IsFunction() {
			_ = proto.Set(k, v)
		}
	}

	return ctx.Global().Set("console", console)
}

// newConsoleInstance 构建一个 console 实例对象：全部方法为自有属性闭包，
// 捕获该实例的输出流与计时/计数状态（多实例互不干扰）。
func newConsoleInstance(stdout, stderr io.Writer, now func() time.Time) engine.Object {
	console := engine.NewObject()

	// timers 用于 time/timeEnd；counters 用于 count/countReset。
	timers := make(map[string]time.Time)
	counters := make(map[string]int)
	// groupDepth 跟踪 group 缩进层级。
	groupDepth := 0

	// logLike 返回一个写到指定 writer 的函数（带 group 缩进）。
	logLike := func(w io.Writer) engine.Func {
		return func(args []engine.Value) (engine.Value, error) {
			indent := strings.Repeat("  ", groupDepth)
			fmt.Fprintln(w, indent+engine.InspectValues(args))
			return engine.Undefined(), nil
		}
	}

	_ = console.Set("log", engine.NewFunction("log", logLike(stdout)))
	_ = console.Set("info", engine.NewFunction("info", logLike(stdout)))
	_ = console.Set("debug", engine.NewFunction("debug", logLike(stdout)))
	_ = console.Set("dir", engine.NewFunction("dir", logLike(stdout)))
	_ = console.Set("warn", engine.NewFunction("warn", logLike(stderr)))
	_ = console.Set("error", engine.NewFunction("error", logLike(stderr)))

	// assert(cond, ...args)
	_ = console.Set("assert", engine.NewFunction("assert", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			fmt.Fprintln(stderr, "Assertion failed")
			return engine.Undefined(), nil
		}
		cond, _ := args[0].Bool()
		if !cond {
			rest := args[1:]
			if len(rest) == 0 {
				fmt.Fprintln(stderr, "Assertion failed")
			} else {
				fmt.Fprintln(stderr, "Assertion failed:", engine.InspectValues(rest))
			}
		}
		return engine.Undefined(), nil
	}))

	// time(label)
	_ = console.Set("time", engine.NewFunction("time", func(args []engine.Value) (engine.Value, error) {
		label := "default"
		if len(args) > 0 {
			label = args[0].String()
		}
		timers[label] = now()
		return engine.Undefined(), nil
	}))

	// timeEnd(label)
	_ = console.Set("timeEnd", engine.NewFunction("timeEnd", func(args []engine.Value) (engine.Value, error) {
		label := "default"
		if len(args) > 0 {
			label = args[0].String()
		}
		start, ok := timers[label]
		if !ok {
			fmt.Fprintf(stderr, "Warning: No such label '%s' for console.timeEnd()\n", label)
			return engine.Undefined(), nil
		}
		delete(timers, label)
		elapsed := now().Sub(start)
		fmt.Fprintf(stdout, "%s: %s\n", label, elapsed)
		return engine.Undefined(), nil
	}))

	// timeLog(label, ...args)：打印经过时间（不删除计时器）。
	_ = console.Set("timeLog", engine.NewFunction("timeLog", func(args []engine.Value) (engine.Value, error) {
		label := "default"
		rest := args
		if len(args) > 0 {
			label = args[0].String()
			rest = args[1:]
		}
		start, ok := timers[label]
		if !ok {
			fmt.Fprintf(stderr, "Warning: No such label '%s' for console.timeLog()\n", label)
			return engine.Undefined(), nil
		}
		elapsed := now().Sub(start)
		suffix := ""
		if len(rest) > 0 {
			suffix = " " + engine.InspectValues(rest)
		}
		fmt.Fprintf(stdout, "%s: %s%s\n", label, elapsed, suffix)
		return engine.Undefined(), nil
	}))

	// group / groupEnd：缩进分组。
	_ = console.Set("group", engine.NewFunction("group", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			fmt.Fprintln(stdout, strings.Repeat("  ", groupDepth)+engine.InspectValues(args))
		}
		groupDepth++
		return engine.Undefined(), nil
	}))
	_ = console.Set("groupEnd", engine.NewFunction("groupEnd", func(args []engine.Value) (engine.Value, error) {
		if groupDepth > 0 {
			groupDepth--
		}
		return engine.Undefined(), nil
	}))
	// groupCollapsed 同 group。
	if g, err := console.Get("group"); err == nil {
		_ = console.Set("groupCollapsed", g)
	}

	// count(label) / countReset(label)
	_ = console.Set("count", engine.NewFunction("count", func(args []engine.Value) (engine.Value, error) {
		label := "default"
		if len(args) > 0 {
			label = args[0].String()
		}
		counters[label]++
		fmt.Fprintf(stdout, "%s: %d\n", label, counters[label])
		return engine.Undefined(), nil
	}))
	_ = console.Set("countReset", engine.NewFunction("countReset", func(args []engine.Value) (engine.Value, error) {
		label := "default"
		if len(args) > 0 {
			label = args[0].String()
		}
		delete(counters, label)
		return engine.Undefined(), nil
	}))

	// table(data)：简化表格输出——数组逐行，对象逐键。
	_ = console.Set("table", engine.NewFunction("table", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		indent := strings.Repeat("  ", groupDepth)
		if a, ok := args[0].(*engine.ArrayValue); ok {
			for _, e := range a.Elems() {
				fmt.Fprintln(stdout, indent+e.String())
			}
			return engine.Undefined(), nil
		}
		if o, ok := args[0].AsObject(); ok {
			for _, k := range o.Keys() {
				if v, err := o.Get(k); err == nil {
					fmt.Fprintf(stdout, "%s%s: %s\n", indent, k, v.String())
				}
			}
			return engine.Undefined(), nil
		}
		fmt.Fprintln(stdout, indent+args[0].String())
		return engine.Undefined(), nil
	}))

	// trace(...args)：打印调用栈（简化）。
	_ = console.Set("trace", engine.NewFunction("trace", func(args []engine.Value) (engine.Value, error) {
		fmt.Fprintln(stderr, "Trace: "+engine.InspectValues(args))
		fmt.Fprintln(stderr, "    at <anonymous>")
		return engine.Undefined(), nil
	}))

	// clear()
	_ = console.Set("clear", engine.NewFunction("clear", func(args []engine.Value) (engine.Value, error) {
		// 在终端中清屏：写 ANSI 转义码
		// 不实现跨平台清屏行为，仅输出换行
		fmt.Fprintln(stdout)
		return engine.Undefined(), nil
	}))

	// profile/profileEnd/timeStamp：Node 22 中均为静默 no-op，仅保证存在。
	noop := func(name string) {
		_ = console.Set(name, engine.NewFunction(name, func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
	}
	noop("profile")
	noop("profileEnd")
	noop("timeStamp")

	return console
}

// streamWriter 把 Node 风格可写流对象（带 write 方法，如 process.stdout）
// 适配为 io.Writer，供 new Console({stdout}) 使用。
func streamWriter(v engine.Value) (io.Writer, error) {
	o, ok := v.AsObject()
	if !ok {
		return nil, fmt.Errorf("not an object")
	}
	write, err := o.Get("write")
	if err != nil || !write.IsFunction() {
		return nil, fmt.Errorf("not a writable stream")
	}
	return jsStreamWriter{obj: o}, nil
}

// jsStreamWriter 经流对象的 write 方法同步写入（Write 在 JS 线程被调用，
// 与 request.write 等现有用法一致）。
type jsStreamWriter struct {
	obj engine.Object
}

func (w jsStreamWriter) Write(p []byte) (int, error) {
	write, _ := w.obj.Get("write")
	if _, err := interpreter.CallWithThis(write, w.obj, []engine.Value{engine.Str(string(p))}); err != nil {
		return 0, err
	}
	return len(p), nil
}
