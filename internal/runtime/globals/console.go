// Package globals 提供 JS 全局对象的实现。
// 包括 console、process、Buffer、timers 等。
package globals

import (
	"fmt"
	"io"
	"os"
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
//   - console.time(label)
//   - console.timeEnd(label)
//   - console.group(...args)   // 暂不实现缩进，等同 log
//   - console.groupEnd()       // no-op
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

	// timers 用于 time/timeEnd。
	timers := make(map[string]time.Time)

	// logLike 返回一个写到指定 writer 的函数。
	logLike := func(w io.Writer) engine.Func {
		return func(args []engine.Value) (engine.Value, error) {
			fmt.Fprintln(w, engine.InspectValues(args))
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

	// group / groupEnd（简化：group 等同 log，groupEnd 为 no-op）
	_ = console.Set("group", engine.NewFunction("group", logLike(cfg.Stdout)))
	_ = console.Set("groupEnd", engine.NewFunction("groupEnd", func(args []engine.Value) (engine.Value, error) {
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
