// node:test TestContext：t.* 方法面（assert/plan/diagnostic/todo/skip）与单次运行状态。

package builtin

import (
	"fmt"
	"sync"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// testRunState 记录单个用例的运行状态（Node 语义）。
type testRunState struct {
	vm       *interpreter.VM
	name     string
	full     string
	fn       engine.Value
	filePath string

	mu            sync.Mutex
	plan          int  // 0 = 未设置；>0 = 期望断言数
	asserts       int  // t.assert 调用计数
	skipRequested bool // t.skip() 已调用
	todo          bool // t.todo() 已调用
	subtests      []*testRunState
	subtestsRun   int                       // 已启动执行的子测试数（t.before/t.after 首末判定）
	subResults    []TestResult              // 子测试执行结果（失败传播给父）
	cancelled     bool                      // 子测试被取消（父未 await——Node 语义）
	promise       *interpreter.PromiseValue // t.test 返回的 promise

	// 子测试钩子（t.beforeEach/t.afterEach/t.before/t.after——Node 语义：
	// 在父测试的子测试间生效）。
	beforeHooks   []engine.Value
	afterHooks    []engine.Value
	beforeEachArr []engine.Value
	afterEachArr  []engine.Value

	// per-test MockTracker 的 spy 列表（测试结束时自动还原——Node 语义）。
	mockSpies   []*mockSpy
	mockRestore func() // t.mock 的还原函数（测试结束时调用）

	signal engine.Value // t.signal（测试取消时中断）
}

// currentTestFilePath 记录当前测试文件绝对路径（t.filePath 用）。
var currentTestFilePath string

// newTestRunState 构造用例运行状态。
func newTestRunState(vm *interpreter.VM, name, full string, fn engine.Value) *testRunState {
	filePath := ""
	snapshotMu.Lock()
	filePath = currentTestFilePath
	snapshotMu.Unlock()
	return &testRunState{vm: vm, name: name, full: full, fn: fn, filePath: filePath}
}

// addAssert 由 t.assert 方法调用（t.plan 计数只含 t.assert——Node 语义）。
func (st *testRunState) addAssert() {
	if st == nil {
		return
	}
	st.mu.Lock()
	st.asserts++
	st.mu.Unlock()
}

// planError 校验 t.plan(n)：用例结束时断言数必须等于 n。
func (st *testRunState) planError() error {
	if st == nil || st.plan <= 0 {
		return nil
	}
	if st.asserts != st.plan {
		return fmt.Errorf("expected %d assertion calls, but received %d", st.plan, st.asserts)
	}
	return nil
}

// invokeTestFnWithState 调用测试函数（t 参数携带运行状态）。
// Node 22 语义：
//   - 父测试返回 promise（async 父）：AwaitPromise 驱动微任务，期间
//     await 的子测试经微任务执行；全部子测试完成后父测试结束。
//   - 父测试同步返回但注册了子测试（未 await）：子测试被取消
//     （cancelledByParent），父测试失败（subtestsFailed）。
func invokeTestFnWithState(vm *interpreter.VM, fn engine.Value, st *testRunState) error {
	if fn == nil || !fn.IsFunction() {
		return nil // skip/todo 无 fn 形态
	}
	t := newTestContext(vm, st)
	result, err := vm.InvokeFn(fn, engine.Undefined(), []engine.Value{t})
	if err != nil {
		return err
	}
	if pv, ok := result.(*interpreter.PromiseValue); ok {
		_, err = vm.AwaitPromise(pv)
		if err != nil {
			return err
		}
		// 父测试结束：等待未 await 的子测试完成（如父 fn 里 t.test() 未 await）。
		st.mu.Lock()
		pending := append([]*testRunState(nil), st.subtests...)
		st.mu.Unlock()
		for _, sub := range pending {
			if sub.promise != nil && sub.promise.State() != 1 { // 1 = fulfilled
				_, _ = vm.AwaitPromise(sub.promise)
			}
		}
		return nil
	}
	// 同步父测试 + 子测试 → 子测试取消、父失败（Node 22 实测语义）。
	st.mu.Lock()
	pending := len(st.subtests)
	for _, sub := range st.subtests {
		sub.mu.Lock()
		sub.cancelled = true
		sub.mu.Unlock()
	}
	st.mu.Unlock()
	if pending > 0 {
		return errSubtestsFailed
	}
	return nil
}

// errSubtestsFailed 子测试未完成即父测试结束（Node: '1 subtest failed'）。
var errSubtestsFailed = fmt.Errorf("1 subtest failed")

// invokeTestFn 调用钩子函数（before/after/beforeEach/afterEach）。
// Node 语义：钩子也接收 TestContext（独立状态，不参与 plan 校验）。
func invokeTestFn(vm *interpreter.VM, fn engine.Value) error {
	if fn == nil || !fn.IsFunction() {
		return nil
	}
	st := newTestRunState(vm, "", "", fn)
	t := newTestContext(vm, st)
	result, err := vm.InvokeFn(fn, engine.Undefined(), []engine.Value{t})
	if err != nil {
		return err
	}
	if pv, ok := result.(*interpreter.PromiseValue); ok {
		_, err = vm.AwaitPromise(pv)
		return err
	}
	return nil
}

// newTestContext 构造 TestContext 对象（状态绑定 st）。
func newTestContext(vm *interpreter.VM, st *testRunState) engine.Value {
	t := engine.NewObject()

	// 只读属性：name / fullName / filePath / signal（Node 22 语义）。
	_ = t.Set("name", engine.Str(st.name))
	_ = t.Set("fullName", engine.Str(st.full))
	_ = t.Set("filePath", engine.Str(st.filePath))
	// t.signal：AbortSignal（Node 语义：测试超时/取消时中断）。复用全局
	// AbortSignal 构造器创建独立信号；测试被父取消时置为中断。
	signal := newAbortSignal(vm)
	_ = t.Set("signal", signal)
	st.mu.Lock()
	st.signal = signal
	st.mu.Unlock()

	// t.assert：断言对象（复用 assert 模块 + snapshot）。
	// 所有断言递增计数（t.plan 只计 t.assert——Node 语义）。
	assertObj := engine.NewObject()
	_ = assertObj.Set("ok", engine.NewFunction("ok", func(args []engine.Value) (engine.Value, error) {
		st.addAssert()
		if len(args) == 0 || !truthy(args[0]) {
			return engine.Undefined(), fmt.Errorf("%w: expected value to be truthy", engine.ErrAssertion)
		}
		return engine.Undefined(), nil
	}))
	_ = assertObj.Set("strictEqual", engine.NewFunction("strictEqual", func(args []engine.Value) (engine.Value, error) {
		st.addAssert()
		if len(args) < 2 || !strictEqual(args[0], args[1]) {
			return engine.Undefined(), fmt.Errorf("%w: expected %s but got %s", engine.ErrAssertion, argString(args, 1), argString(args, 0))
		}
		return engine.Undefined(), nil
	}))
	_ = assertObj.Set("equal", engine.NewFunction("equal", func(args []engine.Value) (engine.Value, error) {
		st.addAssert()
		if len(args) < 2 || !looseEqual(args[0], args[1]) {
			return engine.Undefined(), fmt.Errorf("%w: expected %s but got %s", engine.ErrAssertion, argString(args, 1), argString(args, 0))
		}
		return engine.Undefined(), nil
	}))
	_ = assertObj.Set("deepStrictEqual", engine.NewFunction("deepStrictEqual", func(args []engine.Value) (engine.Value, error) {
		st.addAssert()
		if len(args) < 2 || !testDeepStrictEqual(args[0], args[1]) {
			return engine.Undefined(), fmt.Errorf("%w: expected %s but got %s", engine.ErrAssertion, argString(args, 1), argString(args, 0))
		}
		return engine.Undefined(), nil
	}))
	_ = assertObj.Set("deepEqual", engine.NewFunction("deepEqual", func(args []engine.Value) (engine.Value, error) {
		st.addAssert()
		if len(args) < 2 || !testDeepLooseEqual(args[0], args[1]) {
			return engine.Undefined(), fmt.Errorf("%w: expected %s but got %s", engine.ErrAssertion, argString(args, 1), argString(args, 0))
		}
		return engine.Undefined(), nil
	}))
	_ = assertObj.Set("notStrictEqual", engine.NewFunction("notStrictEqual", func(args []engine.Value) (engine.Value, error) {
		st.addAssert()
		if len(args) >= 2 && strictEqual(args[0], args[1]) {
			return engine.Undefined(), fmt.Errorf("%w: values should not be strictly equal", engine.ErrAssertion)
		}
		return engine.Undefined(), nil
	}))
	_ = assertObj.Set("notEqual", engine.NewFunction("notEqual", func(args []engine.Value) (engine.Value, error) {
		st.addAssert()
		if len(args) >= 2 && looseEqual(args[0], args[1]) {
			return engine.Undefined(), fmt.Errorf("%w: values should not be loosely equal", engine.ErrAssertion)
		}
		return engine.Undefined(), nil
	}))
	_ = assertObj.Set("notDeepEqual", engine.NewFunction("notDeepEqual", func(args []engine.Value) (engine.Value, error) {
		st.addAssert()
		if len(args) >= 2 && testDeepLooseEqual(args[0], args[1]) {
			return engine.Undefined(), fmt.Errorf("%w: values should not be deep equal", engine.ErrAssertion)
		}
		return engine.Undefined(), nil
	}))
	_ = assertObj.Set("notDeepStrictEqual", engine.NewFunction("notDeepStrictEqual", func(args []engine.Value) (engine.Value, error) {
		st.addAssert()
		if len(args) >= 2 && testDeepStrictEqual(args[0], args[1]) {
			return engine.Undefined(), fmt.Errorf("%w: values should not be deep strict equal", engine.ErrAssertion)
		}
		return engine.Undefined(), nil
	}))
	_ = assertObj.Set("ifError", engine.NewFunction("ifError", func(args []engine.Value) (engine.Value, error) {
		st.addAssert()
		if len(args) > 0 && !args[0].IsUndefined() && !args[0].IsNull() {
			return engine.Undefined(), fmt.Errorf("%w: ifError got unwanted exception", engine.ErrAssertion)
		}
		return engine.Undefined(), nil
	}))
	_ = assertObj.Set("fail", engine.NewFunction("fail", func(args []engine.Value) (engine.Value, error) {
		st.addAssert()
		msg := "assertion failed"
		if len(args) > 0 {
			msg = args[0].String()
		}
		return engine.Undefined(), fmt.Errorf("%w: %s", engine.ErrAssertion, msg)
	}))
	_ = assertObj.Set("match", engine.NewFunction("match", func(args []engine.Value) (engine.Value, error) {
		st.addAssert()
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("%w: match: string and regexp required", engine.ErrTypeError)
		}
		if !vmRegexpTest(vm, args[1], args[0]) {
			return engine.Undefined(), fmt.Errorf("%w: match: %q does not match", engine.ErrAssertion, args[0].String())
		}
		return engine.Undefined(), nil
	}))
	_ = assertObj.Set("doesNotMatch", engine.NewFunction("doesNotMatch", func(args []engine.Value) (engine.Value, error) {
		st.addAssert()
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("%w: doesNotMatch: string and regexp required", engine.ErrTypeError)
		}
		if vmRegexpTest(vm, args[1], args[0]) {
			return engine.Undefined(), fmt.Errorf("%w: doesNotMatch: %q should not match", engine.ErrAssertion, args[0].String())
		}
		return engine.Undefined(), nil
	}))
	_ = assertObj.Set("throws", engine.NewFunction("throws", func(args []engine.Value) (engine.Value, error) {
		st.addAssert()
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("%w: throws: function required", engine.ErrAssertion)
		}
		f, ok := args[0].AsFunction()
		if !ok {
			return engine.Undefined(), fmt.Errorf("%w: throws: first argument must be a function", engine.ErrAssertion)
		}
		_, err := f.Call(nil)
		if err == nil {
			return engine.Undefined(), fmt.Errorf("%w: throws: expected exception but none was thrown", engine.ErrAssertion)
		}
		return engine.Undefined(), nil
	}))
	_ = assertObj.Set("rejects", engine.NewFunction("rejects", func(args []engine.Value) (engine.Value, error) {
		st.addAssert()
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("%w: rejects: async function/promise required", engine.ErrTypeError)
		}
		var pv engine.Value
		var err error
		if f, ok := args[0].AsFunction(); ok {
			pv, err = f.Call(nil)
			if err != nil {
				return engine.Undefined(), nil // 同步抛出也算拒绝
			}
		} else {
			pv = args[0]
		}
		if prom, ok := pv.(*interpreter.PromiseValue); ok {
			_, err := vm.AwaitPromise(prom)
			if err != nil {
				return engine.Undefined(), nil // 拒绝 → 通过
			}
			return engine.Undefined(), fmt.Errorf("%w: rejects: promise did not reject", engine.ErrAssertion)
		}
		return engine.Undefined(), fmt.Errorf("%w: rejects: value is not a promise", engine.ErrTypeError)
	}))
	_ = assertObj.Set("doesNotReject", engine.NewFunction("doesNotReject", func(args []engine.Value) (engine.Value, error) {
		st.addAssert()
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		var pv engine.Value
		if f, ok := args[0].AsFunction(); ok {
			var err error
			pv, err = f.Call(nil)
			if err != nil {
				return engine.Undefined(), fmt.Errorf("%w: doesNotReject: got unwanted rejection", engine.ErrAssertion)
			}
		} else {
			pv = args[0]
		}
		if prom, ok := pv.(*interpreter.PromiseValue); ok {
			_, err := vm.AwaitPromise(prom)
			if err != nil {
				return engine.Undefined(), fmt.Errorf("%w: doesNotReject: got unwanted rejection", engine.ErrAssertion)
			}
		}
		return engine.Undefined(), nil
	}))
	// t.assert.snapshot(value)：快照断言（Node 22 experimental 语义）。
	_ = assertObj.Set("snapshot", engine.NewFunction("snapshot", func(args []engine.Value) (engine.Value, error) {
		st.addAssert()
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("%w: snapshot: value required", engine.ErrTypeError)
		}
		return snapshotAssert(vm, args[0])
	}))
	// register() 注册的自定义断言挂到 t.assert（Node 22.14 语义）。
	regMu.Lock()
	for name, fn := range customAsserts {
		_ = assertObj.Set(name, fn)
	}
	regMu.Unlock()
	_ = t.Set("assert", assertObj)

	// t.diagnostic(msg)：输出诊断信息（Node 语义：透传输出）。
	_ = t.Set("diagnostic", engine.NewFunction("diagnostic", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			fmt.Printf("# %s\n", args[0].String())
		}
		return engine.Undefined(), nil
	}))

	// t.skip()：标记跳过（Node 语义：后续断言不再执行，测试标 SKIP）。
	_ = t.Set("skip", engine.NewFunction("skip", func(args []engine.Value) (engine.Value, error) {
		st.mu.Lock()
		st.skipRequested = true
		st.mu.Unlock()
		return engine.Undefined(), errTestSkipped
	}))
	// t.todo()：标记待办（Node 语义：测试执行但标 TODO，失败不计）。
	_ = t.Set("todo", engine.NewFunction("todo", func(args []engine.Value) (engine.Value, error) {
		st.mu.Lock()
		st.todo = true
		st.mu.Unlock()
		return engine.Undefined(), nil
	}))
	// t.plan(n)：声明期望断言数（用例结束时校验）。
	_ = t.Set("plan", engine.NewFunction("plan", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("%w: plan: count required", engine.ErrTypeError)
		}
		n, ok := args[0].Int()
		if !ok || n < 0 {
			return engine.Undefined(), fmt.Errorf("%w: plan: count must be a non-negative integer", engine.ErrTypeError)
		}
		st.mu.Lock()
		st.plan = int(n)
		st.mu.Unlock()
		return engine.Undefined(), nil
	}))
	_ = t.Set("runOnly", engine.NewFunction("runOnly", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	// t.test(name, fn)：子测试（Node 22 语义：经微任务调度执行，返回
	// promise 供父测试 await；同步父测试未 await 的子测试被取消）。
	_ = t.Set("test", engine.NewFunction("test", func(args []engine.Value) (engine.Value, error) {
		name, fn := testNameAndFn(args)
		if !fn.IsFunction() {
			return engine.Undefined(), fmt.Errorf("%w: t.test() requires a function", engine.ErrTypeError)
		}
		sub := newTestRunState(vm, name, joinName(st.full, name), fn)
		st.mu.Lock()
		st.subtests = append(st.subtests, sub)
		st.mu.Unlock()
		p := interpreter.NewPromiseValue(vm.Interp())
		sub.mu.Lock()
		sub.promise = p
		sub.mu.Unlock()
		// 子测试调度到微任务队列：父测试 await 时（AwaitPromise 驱动
		// microtask）执行；父测试同步结束时该微任务仍挂起 → 子测试取消。
		// 父测试的 t.before/t.after/t.beforeEach/t.afterEach 围绕子测试生效
		// （Node 语义：before 首个子测试前一次，after 最后一个后一次）。
		vm.EnqueueMicrotask(func() {
			sub.mu.Lock()
			cancelled := sub.cancelled
			sub.mu.Unlock()
			if cancelled {
				p.Fulfill(engine.Undefined())
				return
			}
			st.mu.Lock()
			firstSub := st.subtestsRun == 0
			st.subtestsRun++
			beforeEachArr := append([]engine.Value(nil), st.beforeEachArr...)
			afterEachArr := append([]engine.Value(nil), st.afterEachArr...)
			beforeHooks := append([]engine.Value(nil), st.beforeHooks...)
			lastSub := st.subtestsRun == len(st.subtests)
			afterHooks := append([]engine.Value(nil), st.afterHooks...)
			st.mu.Unlock()
			hookErr := error(nil)
			if firstSub {
				for _, h := range beforeHooks {
					if err := invokeTestFn(vm, h); err != nil {
						hookErr = err
						break
					}
				}
			}
			if hookErr == nil {
				for _, h := range beforeEachArr {
					if err := invokeTestFn(vm, h); err != nil {
						hookErr = err
						break
					}
				}
			}
			if hookErr != nil {
				sr := TestResult{Name: sub.name, FullName: sub.full, Passed: false, Error: "subtest hook: " + testErrorMessage(vm, hookErr)}
				st.mu.Lock()
				st.subResults = append(st.subResults, sr)
				st.mu.Unlock()
				p.Fulfill(engine.Undefined())
				return
			}
			sr := runSubTestSync(vm, sub)
			for _, h := range afterEachArr {
				if err := invokeTestFn(vm, h); err != nil {
					sr.Passed = false
					if sr.Error == "" {
						sr.Error = "subtest afterEach: " + testErrorMessage(vm, err)
					}
				}
			}
			st.mu.Lock()
			st.subResults = append(st.subResults, sr)
			st.mu.Unlock()
			if lastSub {
				for _, h := range afterHooks {
					if err := invokeTestFn(vm, h); err != nil {
						st.mu.Lock()
						st.subResults = append(st.subResults, TestResult{Name: sub.name, FullName: sub.full, Passed: false, Error: "subtest after: " + testErrorMessage(vm, err)})
						st.mu.Unlock()
						break
					}
				}
			}
			p.Fulfill(engine.Undefined())
		})
		return p, nil
	}))

	// t.before / t.after / t.beforeEach / t.afterEach：子测试钩子
	// （Node 语义：作用域为当前测试的子测试）。
	ctxHook := func(key string) engine.Func {
		return func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 || !args[0].IsFunction() {
				return engine.Undefined(), fmt.Errorf("%w: t.%s() requires a function", engine.ErrTypeError, key)
			}
			st.mu.Lock()
			switch key {
			case "before":
				st.beforeHooks = append(st.beforeHooks, args[0])
			case "after":
				st.afterHooks = append(st.afterHooks, args[0])
			case "beforeEach":
				st.beforeEachArr = append(st.beforeEachArr, args[0])
			case "afterEach":
				st.afterEachArr = append(st.afterEachArr, args[0])
			}
			st.mu.Unlock()
			return engine.Undefined(), nil
		}
	}
	_ = t.Set("before", engine.NewFunction("before", ctxHook("before")))
	_ = t.Set("after", engine.NewFunction("after", ctxHook("after")))
	_ = t.Set("beforeEach", engine.NewFunction("beforeEach", ctxHook("beforeEach")))
	_ = t.Set("afterEach", engine.NewFunction("afterEach", ctxHook("afterEach")))

	// t.waitFor(condition[, options])：轮询条件函数直至返回成功或超时
	// （Node 22.14，P1 语义）。condition 返回 promise（resolve 即成功）；
	// 超时（默认 Infinity）抛 TimeoutError。
	_ = t.Set("waitFor", engine.NewFunction("waitFor", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !args[0].IsFunction() {
			return engine.Undefined(), fmt.Errorf("%w: t.waitFor() requires a condition function", engine.ErrTypeError)
		}
		condFn := args[0]
		// 轮询周期 10ms；timeout 默认 0 = Infinity。
		timeoutMs := int64(0)
		if len(args) > 1 {
			if o, ok := args[1].AsObject(); ok {
				if tv, err := o.Get("timeout"); err == nil && !tv.IsUndefined() {
					if n, ok := tv.Int(); ok {
						timeoutMs = int64(n)
					}
				}
			}
		}
		// 反复调用条件函数直至成功（返回非拒绝 promise 或真值）。
		deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
		for {
			err := invokeTestFn(vm, condFn)
			if err == nil {
				return engine.Undefined(), nil
			}
			if timeoutMs > 0 && time.Now().After(deadline) {
				return engine.Undefined(), fmt.Errorf("operation timed out")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}))

	// t.mock：per-test MockTracker（Node 语义：测试结束时自动还原全部 mock）。
	scopedMock, restoreFn := newScopedTestMock(vm, st)
	_ = t.Set("mock", scopedMock)
	st.mu.Lock()
	st.mockSpies = []*mockSpy{}
	st.mockRestore = restoreFn
	st.mu.Unlock()
	return t
}

// errTestSkipped 标记 t.skip() 的用例（内部错误，不展示给用户）。
var errTestSkipped = fmt.Errorf("test skipped via t.skip()")
