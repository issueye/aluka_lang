package nodediag

// node:inspector 内置模块——Chrome DevTools Protocol 会话 API 面。
// 纯 Go 无 V8，不实现真正的 CDP 通信；仅提供 API 面（Session 类、
// open/close/url/wrapper/console），供依赖方做存在性检测与轻量交互。

import (
	"github.com/aluka-lang/aluka/internal/builtin/nodeevents"
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

	// inspector.Network：CDP Network 域事件广播函数（Node 语义：均为函数）。
	network := engine.NewObject()
	for _, evt := range []string{"dataReceived", "dataSent", "requestWillBeSent", "responseReceived", "loadingFinished", "loadingFailed"} {
		evtCopy := evt
		_ = network.Set(evtCopy, engine.NewFunction(evtCopy, func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
	}
	_ = m.Set("Network", network)

	// inspector.NetworkResources：资源追踪句柄（put 方法面）。
	networkResources := engine.NewObject()
	_ = networkResources.Set("put", engine.NewFunction("put", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = m.Set("NetworkResources", networkResources)

	sessionCtor := engine.NewFunction("Session", func(args []engine.Value) (engine.Value, error) {
		inst := nodeevents.NewEmitterInstance().(engine.Object)
		connected := false

		_ = inst.Set("connect", engine.NewFunction("connect", func(args []engine.Value) (engine.Value, error) {
			connected = true
			return engine.Undefined(), nil
		}))

		_ = inst.Set("disconnect", engine.NewFunction("disconnect", func(args []engine.Value) (engine.Value, error) {
			connected = false
			return engine.Undefined(), nil
		}))

		_ = inst.Set("connectToMainThread", engine.NewFunction("connectToMainThread", func(args []engine.Value) (engine.Value, error) {
			connected = true
			return engine.Undefined(), nil
		}))

		_ = inst.Set("post", engine.NewFunction("post", func(pa []engine.Value) (engine.Value, error) {
			if !connected {
				return engine.Undefined(), inspectorNotConnected()
			}
			if len(pa) == 0 {
				return engine.Undefined(), nil
			}
			method := pa[0].String()
			var callback engine.Function

			for _, arg := range pa[1:] {
				if f, ok := arg.AsFunction(); ok {
					callback = f
					break
				}
			}

			if callback != nil {
				resObj := engine.NewObject()
				_ = resObj.Set("method", engine.Str(method))
				_ = resObj.Set("status", engine.Str("ok"))
				_, _ = callback.Call([]engine.Value{engine.Null(), resObj})
			}
			return engine.Undefined(), nil
		}))

		return inst, nil
	})
	_ = m.Set("Session", sessionCtor)

	return m, nil
}

// inspectorNotConnected 返回 ERR_INSPECTOR_NOT_CONNECTED 错误（Session.post
// 未连接时 Node 同步抛出的错误）。
func inspectorNotConnected() error {
	return &inspectorErr{code: "ERR_INSPECTOR_NOT_CONNECTED", msg: "Session is not connected"}
}

type inspectorErr struct {
	code string
	msg  string
}

func (e *inspectorErr) Error() string { return e.msg }
func (e *inspectorErr) Code() string  { return e.code }
