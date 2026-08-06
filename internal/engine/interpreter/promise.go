package interpreter

import (
	"fmt"
	"strconv"

	"github.com/aluka-lang/aluka/internal/engine"
)

// promiseState tracks the Promise lifecycle.
type promiseState int

const (
	promisePending promiseState = iota
	promiseFulfilled
	promiseRejected
)

// promiseReaction is a pending then/catch/finally callback. When the promise
// settles, each reaction is enqueued as a microtask. The reaction calls the
// appropriate handler and resolves/rejects the derived promise.
type promiseReaction struct {
	onFulfilled engine.Value  // function value or undefined (passthrough)
	onRejected  engine.Value  // function value or undefined (passthrough)
	derived     *PromiseValue // promise to settle with the handler's result
}

// run executes the reaction: calls the handler (if any) and settles the
// derived promise with the result. If the handler throws, the derived promise
// is rejected with the thrown value.
func (r *promiseReaction) run(state promiseState, result engine.Value) {
	var handler engine.Value
	if state == promiseFulfilled {
		handler = r.onFulfilled
	} else {
		handler = r.onRejected
	}

	// Passthrough: if no handler, settle derived with the same outcome.
	if !isCallable(handler) {
		if state == promiseFulfilled {
			r.derived.Fulfill(result)
		} else {
			r.derived.Reject(result)
		}
		return
	}

	// Call the handler with the result as the argument.
	callable, _ := asCallable(handler)
	retVal, err := callable.callWith(engine.Undefined(), []engine.Value{result})
	if err != nil {
		r.derived.Reject(extractThrowValue(err, r.derived.interp))
		return
	}
	// Resolve derived with the handler's return value.
	r.derived.resolve(retVal)
}

// PromiseValue is a JavaScript Promise object. It implements engine.Value and
// engine.Object, mirroring the GeneratorValue pattern.
type PromiseValue struct {
	obj       engine.Object
	interp    *Interpreter
	state     promiseState
	result    engine.Value
	reactions []promiseReaction
}

// NewPromiseValue creates a new pending Promise.
func NewPromiseValue(interp *Interpreter) *PromiseValue {
	p := &PromiseValue{
		interp: interp,
		state:  promisePending,
		result: engine.Undefined(),
	}
	obj := engine.NewObject()
	engine.SetProto(obj, interp.promiseProto)
	p.obj = obj
	return p
}

// State 返回 promise 状态（0 pending / 1 fulfilled / 2 rejected）。
// 供 Aluka.peek 等外部查询，避免直接依赖 promiseState 类型。
func (p *PromiseValue) State() int { return int(p.state) }

// Result 返回 promise 已定值结果（pending 时为 undefined）。
func (p *PromiseValue) Result() engine.Value { return p.result }

// Fulfill transitions the promise to fulfilled and schedules reactions.
// Unlike resolve, Fulfill does NOT unwrap Promise/thenable values.
func (p *PromiseValue) Fulfill(value engine.Value) {
	if p.state != promisePending {
		return
	}
	p.state = promiseFulfilled
	p.result = value
	p.triggerReactions()
}

// Reject transitions the promise to rejected and schedules reactions.
func (p *PromiseValue) Reject(reason engine.Value) {
	if p.state != promisePending {
		return
	}
	p.state = promiseRejected
	p.result = reason
	p.triggerReactions()
}

// resolve resolves the promise with the given value. If the value is a Promise,
// the promise adopts the value's state. Otherwise, the promise is fulfilled.
func (p *PromiseValue) resolve(value engine.Value) {
	if p.state != promisePending {
		return
	}
	// Resolving with self → TypeError.
	if value == p {
		p.Reject(makeTypeError(p.interp, "Cannot resolve a promise with itself"))
		return
	}
	// If value is a Promise, adopt its state.
	if pv, ok := value.(*PromiseValue); ok {
		resolveFn := p.interp.nativeMethod("resolve", func(this engine.Value, args []engine.Value) (engine.Value, error) {
			if len(args) > 0 {
				p.Fulfill(args[0])
			}
			return engine.Undefined(), nil
		})
		rejectFn := p.interp.nativeMethod("reject", func(this engine.Value, args []engine.Value) (engine.Value, error) {
			if len(args) > 0 {
				p.Reject(args[0])
			}
			return engine.Undefined(), nil
		})
		pv.Then(resolveFn, rejectFn)
		return
	}
	p.Fulfill(value)
}

// triggerReactions enqueues all pending reactions as microtasks.
func (p *PromiseValue) triggerReactions() {
	reactions := p.reactions
	p.reactions = nil
	state := p.state
	result := p.result
	for _, r := range reactions {
		r := r // capture for closure
		p.interp.enqueueMicrotask(func() {
			r.run(state, result)
		})
	}
}

// then adds onFulfilled/onRejected handlers and returns a new derived Promise.
func (p *PromiseValue) Then(onFulfilled, onRejected engine.Value) *PromiseValue {
	derived := NewPromiseValue(p.interp)
	reaction := promiseReaction{
		onFulfilled: onFulfilled,
		onRejected:  onRejected,
		derived:     derived,
	}
	if p.state == promisePending {
		p.reactions = append(p.reactions, reaction)
	} else {
		// Already settled: enqueue immediately.
		state := p.state
		result := p.result
		p.interp.enqueueMicrotask(func() {
			reaction.run(state, result)
		})
	}
	return derived
}

// catch is shorthand for then(undefined, onRejected).
func (p *PromiseValue) Catch(onRejected engine.Value) *PromiseValue {
	return p.Then(engine.Undefined(), onRejected)
}

// finally adds a handler that runs regardless of state, passing through the
// value or reason to the derived promise.
func (p *PromiseValue) Finally(onFinally engine.Value) *PromiseValue {
	if !isCallable(onFinally) {
		return p.Then(engine.Undefined(), engine.Undefined())
	}

	// onFulfilled wrapper: call onFinally(), then pass through the value.
	onFulfilled := p.interp.nativeMethod("", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		callable, _ := asCallable(onFinally)
		_, err := callable.callWith(engine.Undefined(), nil)
		if err != nil {
			return engine.Undefined(), err
		}
		if len(args) > 0 {
			return args[0], nil
		}
		return engine.Undefined(), nil
	})

	// onRejected wrapper: call onFinally(), then re-throw the reason.
	onRejected := p.interp.nativeMethod("", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		callable, _ := asCallable(onFinally)
		_, err := callable.callWith(engine.Undefined(), nil)
		if err != nil {
			return engine.Undefined(), err
		}
		if len(args) > 0 {
			return engine.Undefined(), &jsThrow{val: args[0]}
		}
		return engine.Undefined(), nil
	})

	return p.Then(onFulfilled, onRejected)
}

// === engine.Value interface ================================================

func (p *PromiseValue) Type() engine.ValueType { return engine.TypeObject }

func (p *PromiseValue) String() string { return "[object Promise]" }

func (p *PromiseValue) Int() (int, bool)                    { return 0, false }
func (p *PromiseValue) Float() (float64, bool)              { return 0, false }
func (p *PromiseValue) Bool() (bool, bool)                  { return true, true }
func (p *PromiseValue) IsUndefined() bool                   { return false }
func (p *PromiseValue) IsNull() bool                        { return false }
func (p *PromiseValue) IsObject() bool                      { return true }
func (p *PromiseValue) IsFunction() bool                    { return false }
func (p *PromiseValue) AsObject() (engine.Object, bool)     { return p, true }
func (p *PromiseValue) AsFunction() (engine.Function, bool) { return nil, false }

func (p *PromiseValue) Get(key string) (engine.Value, error) { return p.obj.Get(key) }
func (p *PromiseValue) Set(key string, val engine.Value) error {
	return p.obj.Set(key, val)
}
func (p *PromiseValue) Keys() []string         { return p.obj.Keys() }
func (p *PromiseValue) Delete(key string) bool { return p.obj.Delete(key) }

// === setupPromise: register Promise constructor and prototype ==============

func (interp *Interpreter) setupPromise() {
	// Promise.prototype
	interp.promiseProto = engine.NewObject()
	engine.SetProto(interp.promiseProto, interp.objectProto)

	// Promise.prototype.then(onFulfilled, onRejected)
	thenMethod := interp.nativeMethod("then", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		p, ok := this.(*PromiseValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: then called on non-Promise", engine.ErrTypeError)
		}
		var onFulfilled, onRejected engine.Value = engine.Undefined(), engine.Undefined()
		if len(args) > 0 {
			onFulfilled = args[0]
		}
		if len(args) > 1 {
			onRejected = args[1]
		}
		return p.Then(onFulfilled, onRejected), nil
	})
	_ = interp.promiseProto.Set("then", thenMethod)

	// Promise.prototype.catch(onRejected)
	catchMethod := interp.nativeMethod("catch", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		p, ok := this.(*PromiseValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: catch called on non-Promise", engine.ErrTypeError)
		}
		var onRejected engine.Value = engine.Undefined()
		if len(args) > 0 {
			onRejected = args[0]
		}
		return p.Catch(onRejected), nil
	})
	_ = interp.promiseProto.Set("catch", catchMethod)

	// Promise.prototype.finally(onFinally)
	finallyMethod := interp.nativeMethod("finally", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		p, ok := this.(*PromiseValue)
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: finally called on non-Promise", engine.ErrTypeError)
		}
		var onFinally engine.Value = engine.Undefined()
		if len(args) > 0 {
			onFinally = args[0]
		}
		return p.Finally(onFinally), nil
	})
	_ = interp.promiseProto.Set("finally", finallyMethod)

	// Promise constructor: new Promise(executor)
	promiseCtor := interp.makeFunc("Promise", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !isCallable(args[0]) {
			return engine.Undefined(), fmt.Errorf("%w: Promise resolver is not a function", engine.ErrTypeError)
		}
		p := NewPromiseValue(interp)

		// Create resolve/reject functions.
		resolveFn := interp.nativeMethod("resolve", func(this engine.Value, args []engine.Value) (engine.Value, error) {
			var val engine.Value = engine.Undefined()
			if len(args) > 0 {
				val = args[0]
			}
			p.resolve(val)
			return engine.Undefined(), nil
		})
		rejectFn := interp.nativeMethod("reject", func(this engine.Value, args []engine.Value) (engine.Value, error) {
			var reason engine.Value = engine.Undefined()
			if len(args) > 0 {
				reason = args[0]
			}
			p.Reject(reason)
			return engine.Undefined(), nil
		})

		// Call the executor with resolve and reject.
		executor, _ := asCallable(args[0])
		_, err := executor.callWith(engine.Undefined(), []engine.Value{resolveFn, rejectFn})
		if err != nil {
			// If executor throws, reject the promise with the thrown value.
			p.Reject(extractThrowValue(err, interp))
		}
		return p, nil
	})
	_ = promiseCtor.Set("prototype", interp.promiseProto)
	_ = interp.promiseProto.Set("constructor", promiseCtor)

	// Promise.resolve(value)
	_ = promiseCtor.Set("resolve", interp.makeFunc("resolve", func(args []engine.Value) (engine.Value, error) {
		var val engine.Value = engine.Undefined()
		if len(args) > 0 {
			val = args[0]
		}
		// If already a Promise, return it as-is.
		if pv, ok := val.(*PromiseValue); ok {
			return pv, nil
		}
		p := NewPromiseValue(interp)
		p.resolve(val)
		return p, nil
	}))

	// Promise.Reject(reason)
	_ = promiseCtor.Set("reject", interp.makeFunc("reject", func(args []engine.Value) (engine.Value, error) {
		var reason engine.Value = engine.Undefined()
		if len(args) > 0 {
			reason = args[0]
		}
		p := NewPromiseValue(interp)
		p.Reject(reason)
		return p, nil
	}))

	// Promise.all(iterable)
	_ = promiseCtor.Set("all", interp.makeFunc("all", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("%w: Promise.all requires an iterable", engine.ErrTypeError)
		}
		values, err := collectIterable(args[0])
		if err != nil {
			return engine.Undefined(), err
		}
		result := NewPromiseValue(interp)
		if len(values) == 0 {
			arr := engine.NewArray(nil)
			engine.SetProto(arr, interp.arrayProto)
			result.Fulfill(arr)
			return result, nil
		}
		results := make([]engine.Value, len(values))
		remaining := len(values)
		for i, v := range values {
			idx := i
			// Resolve v to a Promise (non-Promise values become fulfilled).
			pv := promiseResolve(interp, v)
			onFulfilled := interp.nativeMethod("", func(this engine.Value, args []engine.Value) (engine.Value, error) {
				if len(args) > 0 {
					results[idx] = args[0]
				}
				remaining--
				if remaining == 0 {
					arr := engine.NewArray(results)
					engine.SetProto(arr, interp.arrayProto)
					result.Fulfill(arr)
				}
				return engine.Undefined(), nil
			})
			onRejected := interp.nativeMethod("", func(this engine.Value, args []engine.Value) (engine.Value, error) {
				if len(args) > 0 {
					result.Reject(args[0])
				}
				return engine.Undefined(), nil
			})
			pv.Then(onFulfilled, onRejected)
		}
		return result, nil
	}))

	// Promise.race(iterable)
	_ = promiseCtor.Set("race", interp.makeFunc("race", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("%w: Promise.race requires an iterable", engine.ErrTypeError)
		}
		values, err := collectIterable(args[0])
		if err != nil {
			return engine.Undefined(), err
		}
		result := NewPromiseValue(interp)
		for _, v := range values {
			pv := promiseResolve(interp, v)
			onFulfilled := interp.nativeMethod("", func(this engine.Value, args []engine.Value) (engine.Value, error) {
				if len(args) > 0 {
					result.Fulfill(args[0])
				}
				return engine.Undefined(), nil
			})
			onRejected := interp.nativeMethod("", func(this engine.Value, args []engine.Value) (engine.Value, error) {
				if len(args) > 0 {
					result.Reject(args[0])
				}
				return engine.Undefined(), nil
			})
			pv.Then(onFulfilled, onRejected)
		}
		return result, nil
	}))

	// Promise.allSettled(iterable)
	_ = promiseCtor.Set("allSettled", interp.makeFunc("allSettled", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("%w: Promise.allSettled requires an iterable", engine.ErrTypeError)
		}
		values, err := collectIterable(args[0])
		if err != nil {
			return engine.Undefined(), err
		}
		result := NewPromiseValue(interp)
		if len(values) == 0 {
			arr := engine.NewArray(nil)
			engine.SetProto(arr, interp.arrayProto)
			result.Fulfill(arr)
			return result, nil
		}
		results := make([]engine.Value, len(values))
		remaining := len(values)
		for i, v := range values {
			idx := i
			pv := promiseResolve(interp, v)
			onFulfilled := interp.nativeMethod("", func(this engine.Value, args []engine.Value) (engine.Value, error) {
				settled := engine.NewObject()
				engine.SetProto(settled, interp.objectProto)
				_ = settled.Set("status", engine.Str("fulfilled"))
				if len(args) > 0 {
					_ = settled.Set("value", args[0])
				}
				results[idx] = settled
				remaining--
				if remaining == 0 {
					arr := engine.NewArray(results)
					engine.SetProto(arr, interp.arrayProto)
					result.Fulfill(arr)
				}
				return engine.Undefined(), nil
			})
			onRejected := interp.nativeMethod("", func(this engine.Value, args []engine.Value) (engine.Value, error) {
				settled := engine.NewObject()
				engine.SetProto(settled, interp.objectProto)
				_ = settled.Set("status", engine.Str("rejected"))
				if len(args) > 0 {
					_ = settled.Set("reason", args[0])
				}
				results[idx] = settled
				remaining--
				if remaining == 0 {
					arr := engine.NewArray(results)
					engine.SetProto(arr, interp.arrayProto)
					result.Fulfill(arr)
				}
				return engine.Undefined(), nil
			})
			pv.Then(onFulfilled, onRejected)
		}
		return result, nil
	}))

	_ = interp.globalObj.Set("Promise", promiseCtor)
	interp.constructors["Promise"] = promiseCtor

	// queueMicrotask(callback) global function.
	queueMicrotaskFn := interp.makeFunc("queueMicrotask", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !isCallable(args[0]) {
			return engine.Undefined(), fmt.Errorf("%w: queueMicrotask requires a function", engine.ErrTypeError)
		}
		cb := args[0]
		interp.enqueueMicrotask(func() {
			callable, _ := asCallable(cb)
			_, _ = callable.callWith(engine.Undefined(), nil)
		})
		return engine.Undefined(), nil
	})
	_ = interp.globalObj.Set("queueMicrotask", queueMicrotaskFn)
}

// === Helpers ================================================================

// isCallable returns true if v is a callable function (not undefined/null).
func isCallable(v engine.Value) bool {
	if v == nil || v.IsUndefined() || v.IsNull() {
		return false
	}
	_, err := asCallable(v)
	return err == nil
}

// promiseResolve wraps a value into a Promise. If the value is already a
// Promise, it is returned as-is. Otherwise, a new fulfilled Promise is created.
func promiseResolve(interp *Interpreter, value engine.Value) *PromiseValue {
	if pv, ok := value.(*PromiseValue); ok {
		return pv
	}
	p := NewPromiseValue(interp)
	p.resolve(value)
	return p
}

// collectIterable extracts values from an iterable. Supports arrays and
// array-like objects (with a numeric length property).
func collectIterable(v engine.Value) ([]engine.Value, error) {
	if arr, ok := v.(*engine.ArrayValue); ok {
		return arr.Elems(), nil
	}
	if v.IsObject() {
		obj, _ := v.AsObject()
		lengthVal, err := obj.Get("length")
		if err != nil || lengthVal.IsUndefined() {
			return nil, fmt.Errorf("%w: value is not iterable", engine.ErrTypeError)
		}
		length, _ := lengthVal.Int()
		result := make([]engine.Value, 0, length)
		for i := 0; i < length; i++ {
			elem, _ := obj.Get(strconv.Itoa(i))
			result = append(result, elem)
		}
		return result, nil
	}
	return nil, fmt.Errorf("%w: value is not iterable", engine.ErrTypeError)
}

// ExtractThrowValue 从 Go error 中提取 JS 抛出的值（供外部包如 builtin
// 的 node:test 运行器使用）。
func ExtractThrowValue(err error, interp *Interpreter) engine.Value {
	return extractThrowValue(err, interp)
}

// extractThrowValue extracts the JS value from a Go error. Handles *jsThrow
// (VM), *jsError (AST interpreter), and plain Go errors (wrapped as JS Error).
func extractThrowValue(err error, interp *Interpreter) engine.Value {
	if jt, ok := err.(*jsThrow); ok {
		return jt.val
	}
	if je, ok := err.(*jsError); ok {
		return je.value
	}
	// Wrap as a JS Error object if possible.
	if ctor, ok := interp.constructors["Error"]; ok {
		if f, ok := ctor.AsFunction(); ok {
			result, callErr := f.Call([]engine.Value{engine.Str(err.Error())})
			if callErr == nil && result.IsObject() {
				return result
			}
		}
	}
	return engine.Str(err.Error())
}

// makeTypeError creates a JS TypeError object with the given message.
func makeTypeError(interp *Interpreter, msg string) engine.Value {
	if ctor, ok := interp.constructors["TypeError"]; ok {
		if f, ok := ctor.AsFunction(); ok {
			result, _ := f.Call([]engine.Value{engine.Str(msg)})
			if result.IsObject() {
				return result
			}
		}
	}
	return engine.Str(msg)
}
