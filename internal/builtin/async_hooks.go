package builtin

import (
	"fmt"
	"sync/atomic"

	"github.com/aluka-lang/aluka/internal/engine"
)

type functionInvoker interface {
	InvokeFn(fn, this engine.Value, args []engine.Value) (engine.Value, error)
}

// NewAsyncHooks provides AsyncResource's callback-scope API. Aluka currently
// has a single JavaScript execution context, so async IDs are informational;
// callback invocation and explicit this binding retain Node-compatible behavior.
func NewAsyncHooks(ctx engine.Context) (engine.Value, error) {
	mod := engine.NewObject()
	var nextID int64 = 1

	invoke := func(fn, thisArg engine.Value, args []engine.Value) (engine.Value, error) {
		if invoker, ok := ctx.(functionInvoker); ok {
			return invoker.InvokeFn(fn, thisArg, args)
		}
		callable, ok := fn.AsFunction()
		if !ok {
			return engine.Undefined(), fmt.Errorf("async_hooks: callback must be a function")
		}
		return callable.Call(args)
	}

	proto := engine.NewObject()
	_ = proto.Set("runInAsyncScope", engine.NewFunction("runInAsyncScope", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !args[0].IsFunction() {
			return engine.Undefined(), fmt.Errorf("async_hooks: callback must be a function")
		}
		thisArg := engine.Undefined()
		if len(args) > 1 {
			thisArg = args[1]
		}
		return invoke(args[0], thisArg, args[2:])
	}))
	_ = proto.Set("emitDestroy", engine.NewFunction("emitDestroy", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = proto.Set("asyncId", engine.NewFunction("asyncId", func(args []engine.Value) (engine.Value, error) {
		return engine.IntValue(0), nil
	}))
	_ = proto.Set("triggerAsyncId", engine.NewFunction("triggerAsyncId", func(args []engine.Value) (engine.Value, error) {
		return engine.IntValue(0), nil
	}))
	_ = proto.Set("bind", engine.NewFunction("bind", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !args[0].IsFunction() {
			return engine.Undefined(), fmt.Errorf("async_hooks: callback must be a function")
		}
		fn := args[0]
		thisArg := engine.Undefined()
		if len(args) > 1 {
			thisArg = args[1]
		}
		return engine.NewFunction("bound", func(callArgs []engine.Value) (engine.Value, error) {
			return invoke(fn, thisArg, callArgs)
		}), nil
	}))

	ctor := engine.NewFunction("AsyncResource", func(args []engine.Value) (engine.Value, error) {
		atomic.AddInt64(&nextID, 1)
		return engine.Undefined(), nil
	})
	if ctorObj, ok := ctor.AsObject(); ok {
		_ = proto.Set("constructor", ctor)
		_ = ctorObj.Set("prototype", proto)
		_ = ctorObj.Set("bind", engine.NewFunction("bind", func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 || !args[0].IsFunction() {
				return engine.Undefined(), fmt.Errorf("async_hooks: callback must be a function")
			}
			fn := args[0]
			thisArg := engine.Undefined()
			if len(args) > 2 {
				thisArg = args[2]
			}
			return engine.NewFunction("bound", func(callArgs []engine.Value) (engine.Value, error) {
				return invoke(fn, thisArg, callArgs)
			}), nil
		}))
	}

	_ = mod.Set("AsyncResource", ctor)
	_ = mod.Set("executionAsyncId", engine.NewFunction("executionAsyncId", func(args []engine.Value) (engine.Value, error) {
		return engine.IntValue(0), nil
	}))
	_ = mod.Set("triggerAsyncId", engine.NewFunction("triggerAsyncId", func(args []engine.Value) (engine.Value, error) {
		return engine.IntValue(0), nil
	}))
	return mod, nil
}
