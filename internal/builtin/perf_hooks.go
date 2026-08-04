package builtin

// node:perf_hooks 内置模块（开发计划 3.10）。
// performance.now()/timeOrigin + mark/measure（简化）。

import (
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewPerfHooks 构造 node:perf_hooks 模块导出对象。
func NewPerfHooks(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()
	performance := engine.NewObject()
	start := time.Now()

	_ = performance.Set("timeOrigin", engine.Number(float64(start.UnixNano())/1e6))
	_ = performance.Set("now", engine.NewFunction("now", func(args []engine.Value) (engine.Value, error) {
		return engine.Number(float64(time.Since(start).Nanoseconds()) / 1e6), nil
	}))
	_ = performance.Set("mark", engine.NewFunction("mark", func(args []engine.Value) (engine.Value, error) {
		obj := engine.NewObject()
		name := ""
		if len(args) > 0 {
			name = args[0].String()
		}
		_ = obj.Set("name", engine.Str(name))
		_ = obj.Set("startTime", engine.Number(float64(time.Since(start).Nanoseconds())/1e6))
		return obj, nil
	}))
	_ = performance.Set("measure", engine.NewFunction("measure", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))

	_ = m.Set("performance", performance)
	return m, nil
}
