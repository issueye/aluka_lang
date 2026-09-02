// node:test mock 面：函数/方法/属性 spy、调用记录对象与作用域化 mock tracker。

package builtin

import (
	"fmt"
	"sync"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

type mockSpy struct {
	mu       sync.Mutex
	target   engine.Object
	method   string
	original engine.Value
	spyFn    engine.Value
	// calls 记录（Node 语义：arguments/error/result/stack/target/this）。
	calls    *engine.ArrayValue
	accesses *engine.ArrayValue // property mock（getter/setter）的访问记录
	isFn     bool               // mock.fn 创建的独立函数（无 target）
	isProp   bool               // mock.getter/setter 创建的属性 mock
	// 实现替换：mockImplementation / mockImplementationOnce。
	impl     engine.Value
	onceImpl engine.Value
	// 归属的 spy 列表（全局 mock 或 t.mock 的 per-test 列表）。
	list *[]*mockSpy
}

// mockSpies 全局 spy 列表（模块级 mock 对象持有）。
var mockSpies []*mockSpy

// newMockAccess 构造一次属性访问（get/set）记录。
func newMockAccess(kind string, value engine.Value, thisVal engine.Value, err error) engine.Value {
	acc := engine.NewObject()
	_ = acc.Set("type", engine.Str(kind))
	_ = acc.Set("result", value)
	_ = acc.Set("error", engine.Undefined())
	if err != nil {
		errObj := engine.NewObject()
		_ = errObj.Set("message", engine.Str(err.Error()))
		_ = acc.Set("error", errObj)
	}
	if thisVal != nil {
		_ = acc.Set("this", thisVal)
	} else {
		_ = acc.Set("this", engine.Undefined())
	}
	return acc
}

// newMockCall 构造一次调用的记录对象。
func newMockCall(ca []engine.Value, thisVal engine.Value, result engine.Value, err error, hasResult bool, stack engine.Value) engine.Value {
	call := engine.NewObject()
	argsArr := engine.NewArray(append([]engine.Value{}, ca...))
	_ = call.Set("arguments", argsArr)
	_ = call.Set("error", engine.Undefined())
	if hasResult {
		_ = call.Set("result", result)
	} else {
		_ = call.Set("result", engine.Undefined())
	}
	if err != nil {
		errObj := engine.NewObject()
		_ = errObj.Set("message", engine.Str(err.Error()))
		_ = call.Set("error", errObj)
	}
	_ = call.Set("stack", stack)
	_ = call.Set("target", engine.Undefined())
	// Node 语义：this 键始终存在（值为调用时的 this，可能 undefined）。
	if thisVal != nil {
		_ = call.Set("this", thisVal)
	} else {
		_ = call.Set("this", engine.Undefined())
	}
	return call
}

// detachSpy 从 spy 归属列表中移除（restore 时）。
func detachSpy(spy *mockSpy) {
	if spy.list == nil {
		return
	}
	for i, s := range *spy.list {
		if s == spy {
			*spy.list = append((*spy.list)[:i], (*spy.list)[i+1:]...)
			break
		}
	}
}

// newMockCallsObj 构造 spy 的 .mock 对象（calls + MockFunctionContext 方法）。
// Node 22 语义：MockFunctionContext 暴露 calls/restore/resetCalls/callCount/
// mockImplementation/mockImplementationOnce（MockTracker.reset 已移除）。
func newMockCallsObj(spy *mockSpy) engine.Value {
	calls := engine.NewArray(nil)
	engine.SetProto(calls, nil)
	spy.calls = calls
	mo := engine.NewObject()
	_ = mo.Set("calls", calls)
	// restore：还原为原实现（无 target 时清空调用记录）。
	_ = mo.Set("restore", engine.NewFunction("restore", func(args []engine.Value) (engine.Value, error) {
		spy.mu.Lock()
		if !spy.isFn && spy.target != nil {
			_ = spy.target.Set(spy.method, spy.original)
			detachSpy(spy)
		}
		spy.impl = engine.Undefined()
		spy.onceImpl = engine.Undefined()
		spy.mu.Unlock()
		return engine.Undefined(), nil
	}))
	// resetCalls：清空调用历史（Node 语义：原地重置数组；这里重建数组并
	// 更新 .mock.calls 引用，可观察行为一致——calls.length 归零）。
	_ = mo.Set("resetCalls", engine.NewFunction("resetCalls", func(args []engine.Value) (engine.Value, error) {
		spy.mu.Lock()
		calls := engine.NewArray(nil)
		engine.SetProto(calls, nil)
		spy.calls = calls
		_ = mo.Set("calls", calls)
		spy.mu.Unlock()
		return engine.Undefined(), nil
	}))
	// callCount：返回调用次数。
	_ = mo.Set("callCount", engine.NewFunction("callCount", func(args []engine.Value) (engine.Value, error) {
		spy.mu.Lock()
		n := len(spy.calls.Elems())
		spy.mu.Unlock()
		return engine.Number(float64(n)), nil
	}))
	// mockImplementation(impl)：替换实现（后续调用使用 impl）。
	_ = mo.Set("mockImplementation", engine.NewFunction("mockImplementation", func(args []engine.Value) (engine.Value, error) {
		spy.mu.Lock()
		spy.impl = engine.Undefined()
		if len(args) > 0 && args[0].IsFunction() {
			spy.impl = args[0]
		}
		spy.mu.Unlock()
		return engine.Undefined(), nil
	}))
	// mockImplementationOnce(impl)：单次实现（本次调用后还原）。
	_ = mo.Set("mockImplementationOnce", engine.NewFunction("mockImplementationOnce", func(args []engine.Value) (engine.Value, error) {
		spy.mu.Lock()
		spy.onceImpl = engine.Undefined()
		if len(args) > 0 && args[0].IsFunction() {
			spy.onceImpl = args[0]
		}
		spy.mu.Unlock()
		return engine.Undefined(), nil
	}))
	return mo
}

// newMockPropObj 构造属性 mock 的 .mock 对象（accesses + 方法）。
func newMockPropObj(spy *mockSpy) engine.Value {
	accesses := engine.NewArray(nil)
	engine.SetProto(accesses, nil)
	spy.accesses = accesses
	mo := engine.NewObject()
	_ = mo.Set("accesses", accesses)
	_ = mo.Set("restore", engine.NewFunction("restore", func(args []engine.Value) (engine.Value, error) {
		spy.mu.Lock()
		if spy.target != nil {
			_ = spy.target.Set(spy.method, spy.original)
			detachSpy(spy)
		}
		spy.mu.Unlock()
		return engine.Undefined(), nil
	}))
	_ = mo.Set("resetAccesses", engine.NewFunction("resetAccesses", func(args []engine.Value) (engine.Value, error) {
		spy.mu.Lock()
		accesses := engine.NewArray(nil)
		engine.SetProto(accesses, nil)
		spy.accesses = accesses
		_ = mo.Set("accesses", accesses)
		spy.mu.Unlock()
		return engine.Undefined(), nil
	}))
	_ = mo.Set("accessCount", engine.NewFunction("accessCount", func(args []engine.Value) (engine.Value, error) {
		spy.mu.Lock()
		n := len(spy.accesses.Elems())
		spy.mu.Unlock()
		return engine.Number(float64(n)), nil
	}))
	_ = mo.Set("mockImplementation", engine.NewFunction("mockImplementation", func(args []engine.Value) (engine.Value, error) {
		spy.mu.Lock()
		spy.impl = engine.Undefined()
		if len(args) > 0 {
			spy.impl = args[0]
		}
		spy.mu.Unlock()
		return engine.Undefined(), nil
	}))
	_ = mo.Set("mockImplementationOnce", engine.NewFunction("mockImplementationOnce", func(args []engine.Value) (engine.Value, error) {
		spy.mu.Lock()
		spy.onceImpl = engine.Undefined()
		if len(args) > 0 {
			spy.onceImpl = args[0]
		}
		spy.mu.Unlock()
		return engine.Undefined(), nil
	}))
	return mo
}

// mockCurrentImpl 解析当前应调用的实现：onceImpl（一次性）> impl > original。
func (spy *mockSpy) mockCurrentImpl() engine.Value {
	if spy.onceImpl != nil && !spy.onceImpl.IsUndefined() {
		impl := spy.onceImpl
		spy.onceImpl = engine.Undefined()
		return impl
	}
	if spy.impl != nil && !spy.impl.IsUndefined() {
		return spy.impl
	}
	return engine.Undefined()
}

// makeMockSpyFn 构造 spy 函数（记录调用 + 委托 impl/original）。
func makeMockSpyFn(vm *interpreter.VM, spy *mockSpy, impl engine.Value, original engine.Value, mo engine.Value) engine.Value {
	return interpreter.NewNativeMethod("mockSpy", func(this engine.Value, ca []engine.Value) (engine.Value, error) {
		spy.mu.Lock()
		call := newMockCall(ca, this, engine.Undefined(), nil, false, engine.Undefined())
		spy.calls.Append(call)
		curImpl := spy.mockCurrentImpl()
		spy.mu.Unlock()
		var result engine.Value
		var err error
		// 委托 impl/original 时保持 this 绑定（Node 语义：
		// mock 函数的 this 透传给原实现）。
		target := curImpl
		if target == nil || !target.IsFunction() {
			target = original
		}
		if target != nil && target.IsFunction() && vm != nil {
			result, err = vm.InvokeFn(target, this, ca)
		}
		// 更新调用记录（result/error）。
		spy.mu.Lock()
		elems := spy.calls.Elems()
		if len(elems) > 0 {
			if last, ok := elems[len(elems)-1].(engine.Object); ok {
				_ = last.Set("result", result)
				if err != nil {
					errObj := engine.NewObject()
					_ = errObj.Set("message", engine.Str(err.Error()))
					_ = last.Set("error", errObj)
				}
			}
		}
		spy.mu.Unlock()
		return result, err
	})
}

// newScopedTestMock 构造 per-test MockTracker（t.mock）：spy 列表独立，
// restoreFn 在测试结束时还原全部并清空列表。
func newScopedTestMock(ctx engine.Context, st *testRunState) (engine.Value, func()) {
	var spies []*mockSpy
	tracker := newMockTracker(ctx, &spies)
	restoreFn := func() {
		for _, s := range spies {
			if !s.isFn && !s.isProp && s.target != nil {
				_ = s.target.Set(s.method, s.original)
			}
			if s.isProp && s.target != nil {
				_ = s.target.Set(s.method, s.original)
			}
		}
		spies = nil
	}
	return tracker, restoreFn
}

// newMockTracker 构造 MockTracker 对象（模块级或 per-test）。
// list 指向该 tracker 的 spy 列表。
func newMockTracker(ctx engine.Context, list *[]*mockSpy) engine.Value {
	mockObj := engine.NewObject()
	vm, _ := ctx.(*interpreter.VM)

	// mock.fn([impl]) → 独立 spy 函数（Node 22 语义）。
	_ = mockObj.Set("fn", engine.NewFunction("fn", func(args []engine.Value) (engine.Value, error) {
		var impl engine.Value
		if len(args) > 0 && args[0].IsFunction() {
			impl = args[0]
		}
		spy := &mockSpy{isFn: true, impl: impl, list: list}
		mo := newMockCallsObj(spy)
		fn := makeMockSpyFn(vm, spy, impl, engine.Undefined(), mo)
		if fo, ok := fn.AsObject(); ok {
			_ = fo.Set("mock", mo)
		}
		spy.spyFn = fn
		spy.original = impl
		*list = append(*list, spy)
		return fn, nil
	}))

	// mock.method(target, name[, impl|options])：替换对象方法为 spy。
	_ = mockObj.Set("method", engine.NewFunction("method", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("%w: mock.method(target, methodName)", engine.ErrTypeError)
		}
		target, ok := args[0].AsObject()
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: mock.method target must be an object", engine.ErrTypeError)
		}
		method := args[1].String()
		original, err := target.Get(method)
		if err != nil {
			original = engine.Undefined()
		}
		var impl engine.Value
		if len(args) >= 3 && args[2].IsFunction() {
			impl = args[2]
		}
		spy := &mockSpy{target: target, method: method, original: original, impl: impl, list: list}
		mo := newMockCallsObj(spy)
		spyFn := makeMockSpyFn(vm, spy, impl, original, mo)
		if fo, ok := spyFn.AsObject(); ok {
			_ = fo.Set("mock", mo)
		}
		_ = target.Set(method, spyFn)
		spy.spyFn = spyFn
		*list = append(*list, spy)
		return spyFn, nil
	}))

	// mock.getter(target, name[, value|fn])：mock 对象属性 getter
	// （Node 语义：mock.getter = mock.method(..., { getter: true })）。
	_ = mockObj.Set("getter", engine.NewFunction("getter", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("%w: mock.getter(target, property)", engine.ErrTypeError)
		}
		target, ok := args[0].AsObject()
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: mock.getter target must be an object", engine.ErrTypeError)
		}
		name := args[1].String()
		original, err := target.Get(name)
		if err != nil {
			original = engine.Undefined()
		}
		var impl engine.Value
		if len(args) >= 3 {
			impl = args[2]
		}
		spy := &mockSpy{target: target, method: name, original: original, impl: impl, isProp: true, list: list}
		mo := newMockCallsObj(spy)
		// getter spy：调用时记录 get 访问（Node 语义：mock.getter 的
		// .mock 是 MockFunctionContext，调用记录在 calls）。
		getterFn := interpreter.NewNativeMethod("mockGetter", func(this engine.Value, ca []engine.Value) (engine.Value, error) {
			spy.mu.Lock()
			cur := spy.mockCurrentImpl()
			if cur == nil || cur.IsUndefined() {
				cur = original
			}
			var result engine.Value
			var err error
			if cur.IsFunction() {
				result, err = vm.InvokeFn(cur, this, nil)
			} else {
				result = cur
			}
			spy.calls.Append(newMockCall(nil, this, result, err, true, engine.Undefined()))
			spy.mu.Unlock()
			return result, err
		})
		if gof, ok := getterFn.AsObject(); ok {
			_ = gof.Set("mock", mo)
		}
		// getter mock 存为访问器属性：引擎读属性时调用 getter 并记录访问。
		_ = target.Set(name, engine.NewAccessor(getterFn, engine.Undefined()))
		spy.spyFn = getterFn
		*list = append(*list, spy)
		return getterFn, nil
	}))

	// mock.setter(target, name[, fn])：mock 对象属性 setter。
	_ = mockObj.Set("setter", engine.NewFunction("setter", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("%w: mock.setter(target, property)", engine.ErrTypeError)
		}
		target, ok := args[0].AsObject()
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: mock.setter target must be an object", engine.ErrTypeError)
		}
		name := args[1].String()
		original, err := target.Get(name)
		if err != nil {
			original = engine.Undefined()
		}
		var impl engine.Value
		if len(args) >= 3 {
			impl = args[2]
		}
		spy := &mockSpy{target: target, method: name, original: original, impl: impl, isProp: true, list: list}
		mo := newMockCallsObj(spy)
		// setter spy：调用时记录 set 访问（Node 语义：mock.setter 的
		// .mock 是 MockFunctionContext，调用记录在 calls，arguments=[新值]）。
		setterFn := interpreter.NewNativeMethod("mockSetter", func(this engine.Value, ca []engine.Value) (engine.Value, error) {
			spy.mu.Lock()
			cur := spy.mockCurrentImpl()
			if cur == nil || cur.IsUndefined() {
				cur = original
			}
			var result engine.Value
			var err error
			if cur.IsFunction() {
				result, err = vm.InvokeFn(cur, this, ca)
			} else {
				result = engine.Undefined()
			}
			spy.calls.Append(newMockCall(ca, this, result, err, true, engine.Undefined()))
			spy.mu.Unlock()
			return result, err
		})
		if so, ok := setterFn.AsObject(); ok {
			_ = so.Set("mock", mo)
		}
		_ = target.Set(name, engine.NewAccessor(engine.Undefined(), setterFn))
		spy.spyFn = setterFn
		*list = append(*list, spy)
		return setterFn, nil
	}))

	// mock.property(target, name, value)：mock 对象属性值（get 返回 value）。
	_ = mockObj.Set("property", engine.NewFunction("property", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return engine.Undefined(), fmt.Errorf("%w: mock.property(target, property, value)", engine.ErrTypeError)
		}
		target, ok := args[0].AsObject()
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: mock.property target must be an object", engine.ErrTypeError)
		}
		name := args[1].String()
		original, err := target.Get(name)
		if err != nil {
			original = engine.Undefined()
		}
		value := args[2]
		spy := &mockSpy{target: target, method: name, original: original, impl: value, isProp: true, list: list}
		mo := newMockPropObj(spy)
		getterFn := interpreter.NewNativeMethod("mockProperty", func(this engine.Value, ca []engine.Value) (engine.Value, error) {
			spy.mu.Lock()
			result := value
			spy.accesses.Append(newMockAccess("get", result, this, nil))
			spy.mu.Unlock()
			return result, nil
		})
		if po, ok := getterFn.AsObject(); ok {
			_ = po.Set("mock", mo)
		}
		_ = target.Set(name, engine.NewAccessor(getterFn, engine.Undefined()))
		spy.spyFn = getterFn
		*list = append(*list, spy)
		return getterFn, nil
	}))

	_ = mockObj.Set("restoreAll", engine.NewFunction("restoreAll", func(args []engine.Value) (engine.Value, error) {
		for _, s := range *list {
			if !s.isFn && s.target != nil {
				_ = s.target.Set(s.method, s.original)
			}
		}
		*list = nil
		return engine.Undefined(), nil
	}))

	// mock.reset()：恢复全部 mock 的原始实现（Node 22.23 语义；call 历史
	// 保留的细微差异记为 knownDifference）。
	_ = mockObj.Set("reset", engine.NewFunction("reset", func(args []engine.Value) (engine.Value, error) {
		for _, s := range *list {
			if !s.isFn && s.target != nil {
				_ = s.target.Set(s.method, s.original)
			}
		}
		return engine.Undefined(), nil
	}))
	return mockObj
}

func newTestMock(ctx engine.Context) engine.Value {
	return newMockTracker(ctx, &mockSpies)
}
