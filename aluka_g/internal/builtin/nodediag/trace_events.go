package nodediag

// node:trace_events 内置模块——Tracing 对象面。
// 纯 Go 无 V8 追踪机制，仅提供 API 面：createTracing({categories}) 返回
// Tracing 对象（enable/disable/enabled/categories）；getEnabledCategories
// 返回 undefined（无启用分类，Node 语义——除非通过 CLI flag 启用）。

import (
	"fmt"
	"strings"

	"github.com/aluka-lang/aluka/internal/builtin/nodebase"
	"github.com/aluka-lang/aluka/internal/engine"
)

// NewTraceEvents 构造 node:trace_events 模块导出对象。
func NewTraceEvents(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// trace_events.getEnabledCategories()：无 CLI 启用分类 → undefined。
	_ = m.Set("getEnabledCategories", engine.NewFunction("getEnabledCategories", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))

	// trace_events.createTracing(options) → Tracing 对象。
	_ = m.Set("createTracing", engine.NewFunction("createTracing", func(args []engine.Value) (engine.Value, error) {
		t := engine.NewObject()
		categories := ""
		if len(args) > 0 && args[0].IsObject() {
			if o, ok := args[0].AsObject(); ok {
				if c, err := o.Get("categories"); err == nil {
					// Node 语义：categories 必须是数组且至少一项，否则抛带 code 的错误。
					arr, ok := c.(*engine.ArrayValue)
					if !ok {
						return engine.Undefined(), nodebase.NewCodedError(fmt.Errorf("%w: The \"options.categories\" property must be an instance of Array. Received type %s", engine.ErrTypeError, c.Type()), "ERR_INVALID_ARG_TYPE")
					}
					if len(arr.Elems()) == 0 {
						return engine.Undefined(), nodebase.NewCodedError(fmt.Errorf("%w: At least one category is required", engine.ErrTypeError), "ERR_TRACE_EVENTS_CATEGORY_REQUIRED")
					}
					parts := make([]string, 0, len(arr.Elems()))
					for _, e := range arr.Elems() {
						parts = append(parts, e.String())
					}
					categories = strings.Join(parts, ",")
				}
			}
		}
		_ = t.Set("categories", engine.Str(categories))
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
