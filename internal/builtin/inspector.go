package builtin

// node:inspector 内置模块——Chrome DevTools Protocol 会话 API 面。
// 纯 Go 无 V8，不实现真正的 CDP 通信；仅提供 API 面（Session 类、
// open/close/url/wrapper/console），供依赖方做存在性检测与轻量交互。

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// NewInspector 构造 node:inspector 模块导出对象。
func NewInspector(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// inspector.open([port[, host[, wait]]])：纯 Go 无 V8，空实现（不报错）。
	_ = m.Set("open", engine.NewFunction("open", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = m.Set("close", engine.NewFunction("close", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	// inspector.url()：无活动 CDP 端点，返回 undefined（Node 语义）。
	_ = m.Set("url", engine.NewFunction("url", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	// inspector.waitForDebugger()：无 V8，立即返回。
	_ = m.Set("waitForDebugger", engine.NewFunction("waitForDebugger", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))

	// inspector.console：Console API（CDP 映射，简化为 console 对象）。
	inspConsole := engine.NewObject()
	for _, lvl := range []string{"log", "info", "warn", "error", "debug", "trace"} {
		lvlCopy := lvl
		_ = inspConsole.Set(lvlCopy, engine.NewFunction(lvlCopy, func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
	}
	_ = m.Set("console", inspConsole)

	// inspector.Session：CDP 会话类（纯 API 面，无真正通信）。
	sessionProto := engine.NewObject()
	_ = sessionProto.Set("connect", engine.NewFunction("connect", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = sessionProto.Set("disconnect", engine.NewFunction("disconnect", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	// post(method[, params], callback)：CDDP 命令——无 V8 回 "not connected" 错误。
	_ = sessionProto.Set("post", engine.NewFunction("post", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			var cb engine.Value = engine.Undefined()
			for _, a := range args[1:] {
				if a.IsFunction() {
					cb = a
				}
			}
			if f, ok := cb.AsFunction(); ok {
				_, _ = f.Call([]engine.Value{engine.Str(" inspector: not connected (pure-Go runtime, no V8)")})
			}
		}
		return engine.Undefined(), nil
	}))
	// connectToMainThread：worker 场景，主线程直接 connect。
	_ = sessionProto.Set("connectToMainThread", engine.NewFunction("connectToMainThread", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))

	sessionCtor := engine.NewFunction("Session", func(args []engine.Value) (engine.Value, error) {
		inst := newEmitterInstance().(engine.Object)
		// 继承 Session.prototype（connect/disconnect/post/on）。
		for _, k := range sessionProto.Keys() {
			if v, err := sessionProto.Get(k); err == nil {
				_ = inst.Set(k, v)
			}
		}
		return inst, nil
	})
	if co, ok := sessionCtor.AsObject(); ok {
		_ = co.Set("prototype", sessionProto)
	}
	_ = m.Set("Session", sessionCtor)

	return m, nil
}
