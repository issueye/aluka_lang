package globals

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/ipc"
)

// alukaRegisterIPC 注册 Aluka.ipc 运行时 API。
func alukaRegisterIPC(ctx engine.Context, aluka engine.Value) {
	ao, _ := aluka.AsObject()
	ipcObj := engine.NewObject()

	// Aluka.ipc.listen(address, options) → Server
	_ = ipcObj.Set("listen", engine.NewFunction("listen", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("ipc.listen requires an address argument")
		}
		addr := args[0].String()
		srv, err := ipc.NewServer(addr)
		if err != nil {
			return nil, err
		}

		serverVal := newIPCEmitter()
		so, _ := serverVal.AsObject()

		_ = so.Set("address", engine.Str(srv.Addr().String()))

		// registerMethod(name, handler)
		_ = so.Set("registerMethod", engine.NewFunction("registerMethod", func(mArgs []engine.Value) (engine.Value, error) {
			if len(mArgs) < 2 || !mArgs[1].IsFunction() {
				return nil, fmt.Errorf("registerMethod requires method name and function handler")
			}
			mName := mArgs[0].String()
			fn, _ := mArgs[1].AsFunction()

			srv.RegisterMethod(mName, func(params interface{}) (interface{}, error) {
				var pVal engine.Value = engine.Undefined()
				if params != nil {
					pBytes, _ := json.Marshal(params)
					var parsed interface{}
					_ = json.Unmarshal(pBytes, &parsed)
					pVal = jsonToEngine(parsed)
				}
				retVal, err := fn.Call([]engine.Value{pVal})
				if err != nil {
					return nil, err
				}
				return valueToJSONHelper(retVal), nil
			})
			return engine.Undefined(), nil
		}))

		// broadcast(event, data)
		_ = so.Set("broadcast", engine.NewFunction("broadcast", func(bArgs []engine.Value) (engine.Value, error) {
			if len(bArgs) == 0 {
				return nil, fmt.Errorf("broadcast requires event name")
			}
			evt := bArgs[0].String()
			var data interface{}
			if len(bArgs) > 1 {
				data = valueToJSONHelper(bArgs[1])
			}
			srv.Broadcast(evt, data)
			return engine.Undefined(), nil
		}))

		// close()
		_ = so.Set("close", engine.NewFunction("close", func(cArgs []engine.Value) (engine.Value, error) {
			_ = srv.Close()
			return engine.Undefined(), nil
		}))

		// 如果 options 中有 methods 映射，自动批量注册
		if len(args) > 1 && args[1].IsObject() {
			if optObj, ok := args[1].AsObject(); ok {
				if mVal, err := optObj.Get("methods"); err == nil && mVal.IsObject() {
					if mo, ok := mVal.AsObject(); ok {
						for _, k := range mo.Keys() {
							keyCopy := k
							if hVal, err := mo.Get(keyCopy); err == nil && hVal.IsFunction() {
								if fn, ok := hVal.AsFunction(); ok {
									srv.RegisterMethod(keyCopy, func(params interface{}) (interface{}, error) {
										var pVal engine.Value = engine.Undefined()
										if params != nil {
											pBytes, _ := json.Marshal(params)
											var parsed interface{}
											_ = json.Unmarshal(pBytes, &parsed)
											pVal = jsonToEngine(parsed)
										}
										retVal, err := fn.Call([]engine.Value{pVal})
										if err != nil {
											return nil, err
										}
										return valueToJSONHelper(retVal), nil
									})
								}
							}
						}
					}
				}
			}
		}

		return serverVal, nil
	}))

	// Aluka.ipc.connect(address) → Promise<Client>
	_ = ipcObj.Set("connect", engine.NewFunction("connect", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("ipc.connect requires an address argument")
		}
		addr := args[0].String()
		client, err := ipc.Connect(addr)
		if err != nil {
			return nil, err
		}

		return wrapIPCClientInstance(ctx, client), nil
	}))

	_ = ao.Set("ipc", ipcObj)
}

// wrapIPCClientInstance 把 ipc.Client 包装为 JavaScript 客户端对象。
// wrapIPCClientInstance 把 ipc.Client 包装为 JavaScript 客户端对象。
func wrapIPCClientInstance(ctx engine.Context, client *ipc.Client) engine.Value {
	clientVal := newIPCEmitter()
	co, _ := clientVal.AsObject()

	// 1. 异步调用：client.call(method, params[, timeoutMs]) → Promise<result>
	_ = co.Set("call", engine.NewFunction("call", func(cArgs []engine.Value) (engine.Value, error) {
		if len(cArgs) == 0 {
			return nil, fmt.Errorf("client.call requires a method name")
		}
		method := cArgs[0].String()
		var params interface{}
		if len(cArgs) > 1 {
			params = valueToJSONHelper(cArgs[1])
		}
		timeout := 30 * time.Second
		if len(cArgs) > 2 {
			if ms, ok := cArgs[2].Float(); ok && ms > 0 {
				timeout = time.Duration(ms) * time.Millisecond
			}
		}

		executor := engine.NewFunction("executor", func(execArgs []engine.Value) (engine.Value, error) {
			if len(execArgs) < 2 {
				return engine.Undefined(), nil
			}
			resolve := execArgs[0]
			reject := execArgs[1]

			release := ctx.AddRef()
			go func() {
				res, err := client.Call(method, params, timeout)
				ctx.PostTask(func() {
					defer release()
					if err != nil {
						if rf, ok := reject.AsFunction(); ok {
							errObj := engine.NewObject()
							_ = errObj.Set("message", engine.Str(err.Error()))
							_, _ = rf.Call([]engine.Value{errObj})
						}
					} else {
						if rf, ok := resolve.AsFunction(); ok {
							_, _ = rf.Call([]engine.Value{jsonToEngine(res)})
						}
					}
				})
			}()
			return engine.Undefined(), nil
		})

		return newPromise(ctx, executor)
	}))

	// 2. 同步调用：client.callSync(method, params[, timeoutMs]) → result
	_ = co.Set("callSync", engine.NewFunction("callSync", func(cArgs []engine.Value) (engine.Value, error) {
		if len(cArgs) == 0 {
			return nil, fmt.Errorf("client.callSync requires a method name")
		}
		method := cArgs[0].String()
		var params interface{}
		if len(cArgs) > 1 {
			params = valueToJSONHelper(cArgs[1])
		}
		timeout := 30 * time.Second
		if len(cArgs) > 2 {
			if ms, ok := cArgs[2].Float(); ok && ms > 0 {
				timeout = time.Duration(ms) * time.Millisecond
			}
		}

		res, err := client.Call(method, params, timeout)
		if err != nil {
			return nil, err
		}
		return jsonToEngine(res), nil
	}))

	// client.emit(event, data)
	_ = co.Set("emit", engine.NewFunction("emit", func(eArgs []engine.Value) (engine.Value, error) {
		if len(eArgs) == 0 {
			return nil, fmt.Errorf("client.emit requires an event name")
		}
		evt := eArgs[0].String()
		var data interface{}
		if len(eArgs) > 1 {
			data = valueToJSONHelper(eArgs[1])
		}
		if err := client.Emit(evt, data); err != nil {
			return nil, err
		}
		return engine.Undefined(), nil
	}))

	// client.close()
	_ = co.Set("close", engine.NewFunction("close", func(clArgs []engine.Value) (engine.Value, error) {
		_ = client.Close()
		return engine.Undefined(), nil
	}))

	return clientVal
}

// CreatePluginProxyModule 为 aluka:plugin:<name> 创建透明 RPC 代理模块。
func CreatePluginProxyModule(ctx engine.Context, pluginName string) (engine.Value, error) {
	client, err := ipc.Connect(pluginName)
	if err != nil {
		return createLazyPluginProxy(ctx, pluginName), nil
	}

	proxyObj := engine.NewObject()
	_ = proxyObj.Set("__pluginName", engine.Str(pluginName))
	_ = proxyObj.Set("__client", wrapIPCClientInstance(ctx, client))

	// 异步 Promise 调用
	_ = proxyObj.Set("call", engine.NewFunction("call", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("plugin.call requires method name")
		}
		method := args[0].String()
		var params interface{}
		if len(args) > 1 {
			params = valueToJSONHelper(args[1])
		}

		executor := engine.NewFunction("executor", func(execArgs []engine.Value) (engine.Value, error) {
			if len(execArgs) < 2 {
				return engine.Undefined(), nil
			}
			resolve := execArgs[0]
			reject := execArgs[1]

			release := ctx.AddRef()
			go func() {
				res, err := client.Call(method, params, 30*time.Second)
				ctx.PostTask(func() {
					defer release()
					if err != nil {
						if rf, ok := reject.AsFunction(); ok {
							errObj := engine.NewObject()
							_ = errObj.Set("message", engine.Str(err.Error()))
							_, _ = rf.Call([]engine.Value{errObj})
						}
					} else {
						if rf, ok := resolve.AsFunction(); ok {
							_, _ = rf.Call([]engine.Value{jsonToEngine(res)})
						}
					}
				})
			}()
			return engine.Undefined(), nil
		})

		return newPromise(ctx, executor)
	}))

	// 同步调用
	_ = proxyObj.Set("callSync", engine.NewFunction("callSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("plugin.callSync requires method name")
		}
		method := args[0].String()
		var params interface{}
		if len(args) > 1 {
			params = valueToJSONHelper(args[1])
		}
		res, err := client.Call(method, params, 30*time.Second)
		if err != nil {
			return nil, err
		}
		return jsonToEngine(res), nil
	}))

	// emit 方法
	_ = proxyObj.Set("emit", engine.NewFunction("emit", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("plugin.emit requires event name")
		}
		evt := args[0].String()
		var data interface{}
		if len(args) > 1 {
			data = valueToJSONHelper(args[1])
		}
		if err := client.Emit(evt, data); err != nil {
			return nil, err
		}
		return engine.Undefined(), nil
	}))

	return proxyObj, nil
}

func createLazyPluginProxy(ctx engine.Context, pluginName string) engine.Value {
	proxyObj := engine.NewObject()
	_ = proxyObj.Set("__pluginName", engine.Str(pluginName))

	_ = proxyObj.Set("call", engine.NewFunction("call", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("plugin.call requires method name")
		}
		method := args[0].String()
		var params interface{}
		if len(args) > 1 {
			params = valueToJSONHelper(args[1])
		}

		executor := engine.NewFunction("executor", func(execArgs []engine.Value) (engine.Value, error) {
			if len(execArgs) < 2 {
				return engine.Undefined(), nil
			}
			resolve := execArgs[0]
			reject := execArgs[1]

			release := ctx.AddRef()
			go func() {
				client, err := ipc.Connect(pluginName)
				if err != nil {
					ctx.PostTask(func() {
						defer release()
						if rf, ok := reject.AsFunction(); ok {
							errObj := engine.NewObject()
							_ = errObj.Set("message", engine.Str(fmt.Sprintf("plugin %q not available: %v", pluginName, err)))
							_, _ = rf.Call([]engine.Value{errObj})
						}
					})
					return
				}
				res, err := client.Call(method, params, 30*time.Second)
				ctx.PostTask(func() {
					defer release()
					if err != nil {
						if rf, ok := reject.AsFunction(); ok {
							errObj := engine.NewObject()
							_ = errObj.Set("message", engine.Str(err.Error()))
							_, _ = rf.Call([]engine.Value{errObj})
						}
					} else {
						if rf, ok := resolve.AsFunction(); ok {
							_, _ = rf.Call([]engine.Value{jsonToEngine(res)})
						}
					}
				})
			}()
			return engine.Undefined(), nil
		})

		return newPromise(ctx, executor)
	}))

	_ = proxyObj.Set("callSync", engine.NewFunction("callSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("plugin.callSync requires method name")
		}
		method := args[0].String()
		client, err := ipc.Connect(pluginName)
		if err != nil {
			return nil, fmt.Errorf("plugin %q not available: %w", pluginName, err)
		}
		var params interface{}
		if len(args) > 1 {
			params = valueToJSONHelper(args[1])
		}
		res, err := client.Call(method, params, 30*time.Second)
		if err != nil {
			return nil, err
		}
		return jsonToEngine(res), nil
	}))

	return proxyObj
}

// 辅助序列化
func valueToJSONHelper(v engine.Value) interface{} {
	switch {
	case v.IsUndefined() || v.IsNull():
		return nil
	case v.Type() == engine.TypeString:
		return v.String()
	case v.Type() == engine.TypeBoolean:
		b, _ := v.Bool()
		return b
	case v.Type() == engine.TypeNumber:
		f, _ := v.Float()
		return f
	default:
		if a, ok := v.(*engine.ArrayValue); ok {
			out := make([]interface{}, 0, len(a.Elems()))
			for _, e := range a.Elems() {
				out = append(out, valueToJSONHelper(e))
			}
			return out
		}
		if o, ok := v.AsObject(); ok {
			obj := make(map[string]interface{})
			for _, k := range o.Keys() {
				if val, err := o.Get(k); err == nil {
					obj[k] = valueToJSONHelper(val)
				}
			}
			return obj
		}
	}
	return nil
}

// newIPCEmitter 构造一个简单的 EventEmitter 实例对象。
func newIPCEmitter() engine.Value {
	inst := engine.NewObject()
	subs := make(map[string][]engine.Function)

	_ = inst.Set("on", engine.NewFunction("on", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 && args[1].IsFunction() {
			evt := args[0].String()
			fn, _ := args[1].AsFunction()
			subs[evt] = append(subs[evt], fn)
		}
		return inst, nil
	}))

	_ = inst.Set("emit", engine.NewFunction("emit", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 1 {
			evt := args[0].String()
			var emitArgs []engine.Value
			if len(args) > 1 {
				emitArgs = args[1:]
			}
			for _, fn := range subs[evt] {
				_, _ = fn.Call(emitArgs)
			}
		}
		return engine.Boolean(true), nil
	}))

	return inst
}

// jsonToEngine 将 Go 解码的 JSON 接口转换为 engine.Value。
func jsonToEngine(v interface{}) engine.Value {
	switch val := v.(type) {
	case nil:
		return engine.Null()
	case bool:
		return engine.Boolean(val)
	case float64:
		return engine.Number(val)
	case string:
		return engine.Str(val)
	case []interface{}:
		elems := make([]engine.Value, len(val))
		for i, e := range val {
			elems[i] = jsonToEngine(e)
		}
		return engine.NewArray(elems)
	case map[string]interface{}:
		obj := engine.NewObject()
		for k, e := range val {
			_ = obj.Set(k, jsonToEngine(e))
		}
		return obj
	default:
		return engine.Undefined()
	}
}
