// Package globals 提供 JS 全局对象的实现。
// 包括 console、process、Buffer、timers 等。
package globals

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
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

	_ = console.Set("log", engine.NewFunction("log", logLike(cfg.Stdout)))
	_ = console.Set("info", engine.NewFunction("info", logLike(cfg.Stdout)))
	_ = console.Set("debug", engine.NewFunction("debug", logLike(cfg.Stdout)))
	_ = console.Set("dir", engine.NewFunction("dir", logLike(cfg.Stdout)))
	_ = console.Set("warn", engine.NewFunction("warn", logLike(cfg.Stderr)))
	_ = console.Set("error", engine.NewFunction("error", logLike(cfg.Stderr)))

	// assert(cond, ...args)
	_ = console.Set("assert", engine.NewFunction("assert", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			fmt.Fprintln(cfg.Stderr, "Assertion failed")
			return engine.Undefined(), nil
		}
		cond, _ := args[0].Bool()
		if !cond {
			rest := args[1:]
			if len(rest) == 0 {
				fmt.Fprintln(cfg.Stderr, "Assertion failed")
			} else {
				fmt.Fprintln(cfg.Stderr, "Assertion failed:", engine.InspectValues(rest))
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
		timers[label] = cfg.Now()
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
			fmt.Fprintf(cfg.Stderr, "Warning: No such label '%s' for console.timeEnd()\n", label)
			return engine.Undefined(), nil
		}
		delete(timers, label)
		elapsed := cfg.Now().Sub(start)
		fmt.Fprintf(cfg.Stdout, "%s: %s\n", label, elapsed)
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
			fmt.Fprintf(cfg.Stderr, "Warning: No such label '%s' for console.timeLog()\n", label)
			return engine.Undefined(), nil
		}
		elapsed := cfg.Now().Sub(start)
		suffix := ""
		if len(rest) > 0 {
			suffix = " " + engine.InspectValues(rest)
		}
		fmt.Fprintf(cfg.Stdout, "%s: %s%s\n", label, elapsed, suffix)
		return engine.Undefined(), nil
	}))

	// group / groupEnd：缩进分组。
	_ = console.Set("group", engine.NewFunction("group", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			fmt.Fprintln(cfg.Stdout, strings.Repeat("  ", groupDepth)+engine.InspectValues(args))
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
		fmt.Fprintf(cfg.Stdout, "%s: %d\n", label, counters[label])
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
				fmt.Fprintln(cfg.Stdout, indent+e.String())
			}
			return engine.Undefined(), nil
		}
		if o, ok := args[0].AsObject(); ok {
			for _, k := range o.Keys() {
				if v, err := o.Get(k); err == nil {
					fmt.Fprintf(cfg.Stdout, "%s%s: %s\n", indent, k, v.String())
				}
			}
			return engine.Undefined(), nil
		}
		fmt.Fprintln(cfg.Stdout, indent+args[0].String())
		return engine.Undefined(), nil
	}))

	// trace(...args)：打印调用栈（简化）。
	_ = console.Set("trace", engine.NewFunction("trace", func(args []engine.Value) (engine.Value, error) {
		fmt.Fprintln(cfg.Stderr, "Trace: "+engine.InspectValues(args))
		fmt.Fprintln(cfg.Stderr, "    at <anonymous>")
		return engine.Undefined(), nil
	}))

	// clear()
	_ = console.Set("clear", engine.NewFunction("clear", func(args []engine.Value) (engine.Value, error) {
		// 在终端中清屏：写 ANSI 转义码
		// 不实现跨平台清屏行为，仅输出换行
		fmt.Fprintln(cfg.Stdout)
		return engine.Undefined(), nil
	}))

	return ctx.Global().Set("console", console)
}
