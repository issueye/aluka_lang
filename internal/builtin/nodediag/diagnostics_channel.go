package nodediag

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/builtin/nodebase"
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// diagnosticsChannelState stores one named channel. Node returns the same
// Channel object for repeated channel(name) calls, so the state and object are
// cached together by NewDiagnosticsChannel.
type diagnosticsChannelState struct {
	name        string
	obj         engine.Object
	subscribers []engine.Value
	watchers    []func()
	stores      []engine.Value // bindStore 绑定的 AsyncLocalStorage 实例
}

// NewDiagnosticsChannel implements the subscription and publication surface
// used by observability-aware packages such as undici.
func NewDiagnosticsChannel(ctx engine.Context) (engine.Value, error) {
	mod := engine.NewObject()
	channels := make(map[string]*diagnosticsChannelState)

	// Channel.prototype：channel(name) 返回的实例共享该原型。
	channelProto := engine.NewObject()
	_ = channelProto.Set("subscribe", interpreter.NewNativeMethod("subscribe", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		st := lookupChannel(channels, this)
		if st == nil {
			return engine.Undefined(), fmt.Errorf("diagnostics_channel: not a Channel")
		}
		if len(args) == 0 || !args[0].IsFunction() {
			return engine.Undefined(), fmt.Errorf("diagnostics_channel: subscriber must be a function")
		}
		st.subscribe(args[0])
		return engine.Undefined(), nil
	}))
	_ = channelProto.Set("unsubscribe", interpreter.NewNativeMethod("unsubscribe", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		st := lookupChannel(channels, this)
		if st == nil {
			return engine.Boolean(false), nil
		}
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(st.unsubscribe(args[0])), nil
	}))
	_ = channelProto.Set("publish", interpreter.NewNativeMethod("publish", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		st := lookupChannel(channels, this)
		if st == nil {
			return engine.Undefined(), nil
		}
		message := engine.Undefined()
		if len(args) > 0 {
			message = args[0]
		}
		if err := st.publish(message); err != nil {
			return engine.Undefined(), err
		}
		return engine.Undefined(), nil
	}))
	_ = channelProto.Set("hasSubscribers", interpreter.NewNativeMethod("hasSubscribers", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		st := lookupChannel(channels, this)
		if st == nil {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(len(st.subscribers) > 0), nil
	}))
	_ = channelProto.Set("bindStore", interpreter.NewNativeMethod("bindStore", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		st := lookupChannel(channels, this)
		if st == nil {
			return engine.Undefined(), nil
		}
		if len(args) > 0 {
			st.bindStore(args[0])
		}
		return engine.Undefined(), nil
	}))
	_ = channelProto.Set("unbindStore", interpreter.NewNativeMethod("unbindStore", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		st := lookupChannel(channels, this)
		if st == nil {
			return engine.Undefined(), nil
		}
		if len(args) > 0 {
			st.unbindStore(args[0])
		}
		return engine.Undefined(), nil
	}))
	_ = channelProto.Set("runStores", interpreter.NewNativeMethod("runStores", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		st := lookupChannel(channels, this)
		if st == nil {
			return engine.Undefined(), nil
		}
		context := engine.Undefined()
		if len(args) > 0 {
			context = args[0]
		}
		if len(args) < 2 || !args[1].IsFunction() {
			return engine.Undefined(), fmt.Errorf("diagnostics_channel: runStores requires a callback")
		}
		return st.runStores(context, args[1], args[2:])
	}))

	channelCtor := engine.NewFunction("Channel", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), fmt.Errorf("diagnostics_channel: Channel constructor is not exposed; use channel(name)")
	})
	if co, ok := channelCtor.AsObject(); ok {
		_ = co.Set("prototype", channelProto)
	}
	_ = mod.Set("Channel", channelCtor)

	getChannel := func(name string) *diagnosticsChannelState {
		if existing, ok := channels[name]; ok {
			return existing
		}

		state := &diagnosticsChannelState{name: name, obj: engine.NewObject()}
		engine.SetProto(state.obj, channelProto)
		_ = state.obj.Set("name", engine.Str(name))
		_ = state.obj.Set("hasSubscribers", engine.Boolean(false))
		// 每个 channel 持有自己的方法闭包（无 this 绑定场景也能工作，且与
		// 原型方法并存以满足 instanceof Channel）。
		_ = state.obj.Set("subscribe", engine.NewFunction("subscribe", func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 || !args[0].IsFunction() {
				return engine.Undefined(), fmt.Errorf("diagnostics_channel: subscriber must be a function")
			}
			state.subscribe(args[0])
			return engine.Undefined(), nil
		}))
		_ = state.obj.Set("unsubscribe", engine.NewFunction("unsubscribe", func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 {
				return engine.Boolean(false), nil
			}
			return engine.Boolean(state.unsubscribe(args[0])), nil
		}))
		_ = state.obj.Set("publish", engine.NewFunction("publish", func(args []engine.Value) (engine.Value, error) {
			message := engine.Undefined()
			if len(args) > 0 {
				message = args[0]
			}
			if err := state.publish(message); err != nil {
				return engine.Undefined(), err
			}
			return engine.Undefined(), nil
		}))
		_ = state.obj.Set("bindStore", engine.NewFunction("bindStore", func(args []engine.Value) (engine.Value, error) {
			if len(args) > 0 {
				state.bindStore(args[0])
			}
			return engine.Undefined(), nil
		}))
		_ = state.obj.Set("unbindStore", engine.NewFunction("unbindStore", func(args []engine.Value) (engine.Value, error) {
			if len(args) > 0 {
				state.unbindStore(args[0])
			}
			return engine.Undefined(), nil
		}))
		_ = state.obj.Set("runStores", engine.NewFunction("runStores", func(args []engine.Value) (engine.Value, error) {
			context := engine.Undefined()
			if len(args) > 0 {
				context = args[0]
			}
			if len(args) < 2 || !args[1].IsFunction() {
				return engine.Undefined(), fmt.Errorf("diagnostics_channel: runStores requires a callback")
			}
			return state.runStores(context, args[1], args[2:])
		}))

		channels[name] = state
		return state
	}

	_ = mod.Set("channel", engine.NewFunction("channel", func(args []engine.Value) (engine.Value, error) {
		return getChannel(nodebase.StrArg(args, 0)).obj, nil
	}))
	_ = mod.Set("tracingChannel", engine.NewFunction("tracingChannel", func(args []engine.Value) (engine.Value, error) {
		name := nodebase.StrArg(args, 0)
		tracing := engine.NewObject()
		states := map[string]*diagnosticsChannelState{
			"start":      getChannel("tracing:" + name + ":start"),
			"end":        getChannel("tracing:" + name + ":end"),
			"asyncStart": getChannel("tracing:" + name + ":asyncStart"),
			"asyncEnd":   getChannel("tracing:" + name + ":asyncEnd"),
			"error":      getChannel("tracing:" + name + ":error"),
		}
		updateHasSubscribers := func() {
			hasSubscribers := false
			for _, state := range states {
				if len(state.subscribers) > 0 {
					hasSubscribers = true
					break
				}
			}
			_ = tracing.Set("hasSubscribers", engine.Boolean(hasSubscribers))
		}
		for key, state := range states {
			_ = tracing.Set(key, state.obj)
			state.watchers = append(state.watchers, updateHasSubscribers)
		}
		updateHasSubscribers()

		_ = tracing.Set("subscribe", engine.NewFunction("subscribe", func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 {
				return engine.Undefined(), nil
			}
			for _, state := range states {
				state.subscribe(args[0])
			}
			return engine.Undefined(), nil
		}))
		_ = tracing.Set("unsubscribe", engine.NewFunction("unsubscribe", func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 {
				return engine.Undefined(), nil
			}
			for _, state := range states {
				state.unsubscribe(args[0])
			}
			return engine.Undefined(), nil
		}))

		traceCall := func(callArgs []engine.Value) (engine.Value, error) {
			if len(callArgs) == 0 || !callArgs[0].IsFunction() {
				return engine.Undefined(), fmt.Errorf("diagnostics_channel: trace callback must be a function")
			}
			context := engine.Value(engine.NewObject())
			if len(callArgs) > 1 && !callArgs[1].IsUndefined() && !callArgs[1].IsNull() {
				context = callArgs[1]
			}
			if err := states["start"].publish(context); err != nil {
				return engine.Undefined(), err
			}
			fn, _ := callArgs[0].AsFunction()
			invokeArgs := []engine.Value{}
			if len(callArgs) > 3 {
				invokeArgs = callArgs[3:]
			}
			result, err := fn.Call(invokeArgs)
			if err != nil {
				_ = states["error"].publish(engine.Str(err.Error()))
				return engine.Undefined(), err
			}
			if err := states["end"].publish(context); err != nil {
				return engine.Undefined(), err
			}
			return result, nil
		}
		for _, method := range []string{"traceSync", "tracePromise", "traceCallback"} {
			_ = tracing.Set(method, engine.NewFunction(method, traceCall))
		}
		return tracing, nil
	}))
	_ = mod.Set("hasSubscribers", engine.NewFunction("hasSubscribers", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(len(getChannel(nodebase.StrArg(args, 0)).subscribers) > 0), nil
	}))
	_ = mod.Set("subscribe", engine.NewFunction("subscribe", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 || !args[1].IsFunction() {
			return engine.Undefined(), fmt.Errorf("diagnostics_channel: subscriber must be a function")
		}
		getChannel(args[0].String()).subscribe(args[1])
		return engine.Undefined(), nil
	}))
	_ = mod.Set("unsubscribe", engine.NewFunction("unsubscribe", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(getChannel(args[0].String()).unsubscribe(args[1])), nil
	}))

	return mod, nil
}

// lookupChannel 根据 channel 实例对象反查状态。
func lookupChannel(channels map[string]*diagnosticsChannelState, inst engine.Value) *diagnosticsChannelState {
	if inst == nil || !inst.IsObject() {
		return nil
	}
	o, _ := inst.AsObject()
	for _, st := range channels {
		if st.obj == o {
			return st
		}
	}
	return nil
}

func (s *diagnosticsChannelState) subscribe(callback engine.Value) {
	s.subscribers = append(s.subscribers, callback)
	_ = s.obj.Set("hasSubscribers", engine.Boolean(true))
	s.notifyWatchers()
}

func (s *diagnosticsChannelState) unsubscribe(callback engine.Value) bool {
	for i, subscriber := range s.subscribers {
		if subscriber == callback {
			s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
			_ = s.obj.Set("hasSubscribers", engine.Boolean(len(s.subscribers) > 0))
			s.notifyWatchers()
			return true
		}
	}
	return false
}

func (s *diagnosticsChannelState) notifyWatchers() {
	for _, watcher := range s.watchers {
		watcher()
	}
}

// bindStore 绑定 AsyncLocalStorage：publish 时订阅者运行在 store.run(message)
// 的上下文中（Node 语义）。
func (s *diagnosticsChannelState) bindStore(store engine.Value) {
	for i, st := range s.stores {
		if st == store {
			// 重新绑定：移除旧位置后追加到末尾。
			s.stores = append(s.stores[:i], s.stores[i+1:]...)
			break
		}
	}
	s.stores = append(s.stores, store)
}

func (s *diagnosticsChannelState) unbindStore(store engine.Value) {
	for i, st := range s.stores {
		if st == store {
			s.stores = append(s.stores[:i], s.stores[i+1:]...)
			return
		}
	}
}

// runStores 在绑定 store 的上下文（context 作为 store 值）中调用 callback。
func (s *diagnosticsChannelState) runStores(context engine.Value, callback engine.Value, args []engine.Value) (engine.Value, error) {
	return invokeWithBoundStores(s.stores, context, callback, args)
}

// invokeWithBoundStores 逐层包 store.run(context, ...)（可嵌套多个绑定 store）。
func invokeWithBoundStores(stores []engine.Value, context, callback engine.Value, args []engine.Value) (engine.Value, error) {
	if len(stores) == 0 {
		f, ok := callback.AsFunction()
		if !ok {
			return engine.Undefined(), fmt.Errorf("diagnostics_channel: callback must be a function")
		}
		return f.Call(args)
	}
	store := stores[0]
	sObj, ok := store.AsObject()
	if !ok {
		return invokeWithBoundStores(stores[1:], context, callback, args)
	}
	runFn, err := sObj.Get("run")
	if err != nil || !runFn.IsFunction() {
		return invokeWithBoundStores(stores[1:], context, callback, args)
	}
	wrapped := engine.NewFunction("bound", func(wa []engine.Value) (engine.Value, error) {
		return invokeWithBoundStores(stores[1:], context, callback, wa)
	})
	rf, _ := runFn.AsFunction()
	callArgs := []engine.Value{context, wrapped}
	callArgs = append(callArgs, args...)
	return rf.Call(callArgs)
}

func (s *diagnosticsChannelState) publish(message engine.Value) error {
	// Subscribers may unsubscribe while publishing. Snapshot the list to match
	// Node's stable traversal behavior for the current publication.
	subscribers := append([]engine.Value(nil), s.subscribers...)
	for _, subscriber := range subscribers {
		fn, ok := subscriber.AsFunction()
		if !ok {
			continue
		}
		// Node 22 语义：bindStore 绑定的 store 不影响 publish 时的同步 store
		// 值（订阅者在当前异步上下文内执行，getStore() 返回调用方上下文的值）。
		if _, err := fn.Call([]engine.Value{message, engine.Str(s.name)}); err != nil {
			return err
		}
	}
	return nil
}
