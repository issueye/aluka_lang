package builtin

// node:perf_hooks 内置模块（开发计划 3.10）。
// performance.now()/timeOrigin + mark/measure（简化）。

import (
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

// NewPerfHooks 构造 node:perf_hooks 模块导出对象。
func NewPerfHooks(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()
	performance, err := globals.EnsurePerformance(ctx)
	if err != nil {
		return engine.Undefined(), err
	}

	_ = m.Set("performance", performance)
	return m, nil
}
