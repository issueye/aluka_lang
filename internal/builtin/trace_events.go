package builtin

// node:trace_events 内置模块——Tracing 对象最小面。
// 纯 Go 无 V8 追踪机制，仅提供 API 面：createTracing({categories}) 返回
// Tracing 对象（enable/disable/enabled）；getEnabledCategories 返回
// undefined（无启用分类，Node 语义）。

import (

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewTraceEvents 构造 node:trace_events 模块导出对象。
func NewTraceEvents(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// trace_events.getEnabledCategories()：无启用分类 → undefined。
	_ = m.Set("getEnabledCategories", engine.NewFunction("getEnabledCategories", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))

	// trace_events.createTracing(options) → Tracing 对象。
	_ = m.Set("createTracing", engine.NewFunction("createTracing", func(args []engine.Value) (engine.Value, error) {
		t := engine.NewObject()
		_ = t.Set("enabled", engine.Boolean(false))
		_ = t.Set("enable", engine.NewFunction("enable", func(args []engine.Value) (engine.Value, error) {
			_ = t.Set("enabled", engine.Boolean(true))
			return engine.Undefined(), nil
		}))
		_ = t.Set("disable", engine.NewFunction("disable", func(args []engine.Value) (engine.Value, error) {
			_ = t.Set("enabled", engine.Boolean(false))
			return engine.Undefined(), nil
		}))
		return t, nil
	}))

	return m, nil
}
