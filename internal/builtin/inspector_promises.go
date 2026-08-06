package builtin

// node:inspector/promises 内置模块——Promise 版 CDP 会话（API 面）。
// 纯 Go 无 V8：Session.post 返回 rejected Promise（与 node:inspector 的
// callback 版 "not connected" 错误对应）。connect/disconnect 空实现。
// Node 中该模块仅导出 Session 类。

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewInspectorPromises 构造 node:inspector/promises 模块导出对象。
// Node 22 运行时该模块除 Promise Session 外还导出与 node:inspector 相同的
// 管理 API（open/close/url/waitForDebugger/console/Network/NetworkResources）。
func NewInspectorPromises(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// 管理面（与 node:inspector 一致；纯 Go 无 V8，空实现）。
	_ = m.Set("open", engine.NewFunction("open", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = m.Set("close", engine.NewFunction("close", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = m.Set("url", engine.NewFunction("url", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = m.Set("waitForDebugger", engine.NewFunction("waitForDebugger", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	inspConsole := engine.NewObject()
	for _, lvl := range []string{"log", "info", "warn", "error", "debug", "trace"} {
		lvlCopy := lvl
		_ = inspConsole.Set(lvlCopy, engine.NewFunction(lvlCopy, func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
	}
	_ = m.Set("console", inspConsole)
	// Network/NetworkResources：CDP 资源通知对象（Node 语义：均为对象，
	// Network 含 6 个事件函数，NetworkResources 含 put 方法）。
	network := engine.NewObject()
	for _, evt := range []string{"dataReceived", "dataSent", "requestWillBeSent", "responseReceived", "loadingFinished", "loadingFailed"} {
		evtCopy := evt
		_ = network.Set(evtCopy, engine.NewFunction(evtCopy, func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
	}
	_ = m.Set("Network", network)
	networkResources := engine.NewObject()
	_ = networkResources.Set("put", engine.NewFunction("put", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = m.Set("NetworkResources", networkResources)
	sessionProto := engine.NewObject()
	_ = sessionProto.Set("connect", engine.NewFunction("connect", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = sessionProto.Set("disconnect", engine.NewFunction("disconnect", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = sessionProto.Set("connectToMainThread", engine.NewFunction("connectToMainThread", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	// post(method[, params]) → Promise：无 V8，reject。
	_ = sessionProto.Set("post", engine.NewFunction("post", func(args []engine.Value) (engine.Value, error) {
		return promiseRejected(ctx, " inspector: not connected (pure-Go runtime, no V8)")
	}))

	sessionCtor := engine.NewFunction("Session", func(args []engine.Value) (engine.Value, error) {
		inst := newEmitterInstance().(engine.Object)
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

// promiseRejected 构造一个立即 reject 的 Promise（用于无 V8 的 post 等）。
func promiseRejected(ctx engine.Context, msg string) (engine.Value, error) {
	promiseCtor, err := ctx.Global().Get("Promise")
	if err != nil || !promiseCtor.IsFunction() {
		return engine.Undefined(), fmt.Errorf("inspector/promises: global Promise not available")
	}
	executor := engine.NewFunction("executor", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 {
			if f, ok := args[1].AsFunction(); ok {
				_, _ = f.Call([]engine.Value{engine.Str(msg)})
			}
		}
		return engine.Undefined(), nil
	})
	pf, ok := promiseCtor.AsFunction()
	if !ok {
		return engine.Undefined(), fmt.Errorf("inspector/promises: Promise not callable")
	}
	return pf.Call([]engine.Value{executor})
}
