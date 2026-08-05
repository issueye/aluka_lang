package builtin

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
)

// diagnosticsChannelState stores one named channel. Node returns the same
// Channel object for repeated channel(name) calls, so the state and object are
// cached together by NewDiagnosticsChannel.
type diagnosticsChannelState struct {
	name        string
	obj         engine.Object
	subscribers []engine.Value
	watchers    []func()
}

// NewDiagnosticsChannel implements the subscription and publication surface
// used by observability-aware packages such as undici.
func NewDiagnosticsChannel(ctx engine.Context) (engine.Value, error) {
	mod := engine.NewObject()
	channels := make(map[string]*diagnosticsChannelState)

	getChannel := func(name string) *diagnosticsChannelState {
		if existing, ok := channels[name]; ok {
			return existing
		}

		state := &diagnosticsChannelState{name: name, obj: engine.NewObject()}
		_ = state.obj.Set("name", engine.Str(name))
		_ = state.obj.Set("hasSubscribers", engine.Boolean(false))
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

		channels[name] = state
		return state
	}

	_ = mod.Set("channel", engine.NewFunction("channel", func(args []engine.Value) (engine.Value, error) {
		return getChannel(strArg(args, 0)).obj, nil
	}))
	_ = mod.Set("tracingChannel", engine.NewFunction("tracingChannel", func(args []engine.Value) (engine.Value, error) {
		name := strArg(args, 0)
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
		return engine.Boolean(len(getChannel(strArg(args, 0)).subscribers) > 0), nil
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

func (s *diagnosticsChannelState) publish(message engine.Value) error {
	// Subscribers may unsubscribe while publishing. Snapshot the list to match
	// Node's stable traversal behavior for the current publication.
	subscribers := append([]engine.Value(nil), s.subscribers...)
	for _, subscriber := range subscribers {
		fn, ok := subscriber.AsFunction()
		if !ok {
			continue
		}
		if _, err := fn.Call([]engine.Value{message, engine.Str(s.name)}); err != nil {
			return err
		}
	}
	return nil
}
