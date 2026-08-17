package builtin

// node:test 内置模块——describe/it/test/beforeEach/afterEach/mock。
//
// 用法与 Node 22 一致：
//
//	import { describe, it, beforeEach, afterEach } from "node:test";
//	import assert from "node:assert";
//
//	describe("suite", () => {
//	  beforeEach(() => { ... });
//	  it("case", () => { assert.strictEqual(1, 1); });
//	});
//
// 测试注册进包级 registry；`aluka test` 子命令加载测试文件后调用
// RunRegisteredTests 执行并汇报。每个测试文件运行前 ResetTestRegistry。
// 用例/钩子支持同步与 async（返回 promise，经 vm.AwaitPromise 驱动）。

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// --- 注册表 ---------------------------------------------------------------

type registeredTest struct {
	name string
	fn   engine.Value
	file string
	line int
	// T1 标记（Node 22 语义）。
	skip bool
	todo bool
	only bool
}

// suiteChild 套件内子项（按注册顺序执行——Node 语义）。
type suiteChild struct {
	isSuite bool
	test    *registeredTest
	suite   *registeredSuite
}

type registeredSuite struct {
	name        string
	beforeHooks []engine.Value // 套件级 before（首用例前执行一次）
	afterHooks  []engine.Value // 套件级 after（末用例后执行一次）
	beforeEach  []engine.Value // 每个用例前
	afterEach   []engine.Value // 每个用例后
	children    []suiteChild   // 注册顺序（tests 与 suites 混合）
	tests       []*registeredTest
	suites      []*registeredSuite
	skip        bool
	todo        bool
	only        bool
}

var (
	regMu    sync.Mutex
	regRoot  = &registeredSuite{name: ""}
	regStack = []*registeredSuite{regRoot}
)

// ResetTestRegistry 清空注册表（每个测试文件运行前调用）。
func ResetTestRegistry() {
	regMu.Lock()
	defer regMu.Unlock()
	regRoot = &registeredSuite{name: ""}
	regStack = []*registeredSuite{regRoot}
}

// NewTest 构造 node:test 模块的导出对象。
func NewTest(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// testOptions 解析 (name, options, fn) 形态的选项对象。
	parseOptions := func(args []engine.Value) (name string, fn engine.Value, opts testOpts) {
		fn = engine.Undefined()
		opts.skip = false
		opts.todo = false
		opts.only = false
		switch {
		case len(args) == 0:
			name, fn = "anonymous", engine.Undefined()
		case len(args) == 1:
			name, fn = testNameAndFn(args)
		case len(args) == 2:
			name = args[0].String()
			if o, ok := args[1].AsObject(); ok {
				applyTestOpts(o, &opts)
			} else {
				fn = args[1]
			}
		default:
			name = args[0].String()
			if o, ok := args[1].AsObject(); ok {
				applyTestOpts(o, &opts)
			}
			fn = args[2]
		}
		if fn == nil || !fn.IsFunction() {
			// (name, fn) 双参形态：第二参是函数。
			if len(args) == 2 && args[1].IsFunction() {
				fn = args[1]
			}
		}
		return
	}

	// describe(name, fn)：注册套件并同步执行函数体。
	// Node 语义：describe.skip(name) 仍注册套件（子用例标记 skip），
	// 但 describe(name, {skip}) 也合法（无 fn）。
	// suite 是 describe 的别名（Node 22：suite === describe）。
	descFn := engine.NewFunction("describe", func(args []engine.Value) (engine.Value, error) {
		name, fn, opts := parseOptions(args)
		if !fn.IsFunction() && !opts.skip && !opts.todo {
			return engine.Undefined(), fmt.Errorf("%w: describe() requires a function", engine.ErrTypeError)
		}
		suite := &registeredSuite{name: name, skip: opts.skip, only: opts.only}
		regMu.Lock()
		cur := regStack[len(regStack)-1]
		cur.suites = append(cur.suites, suite)
		cur.children = append(cur.children, suiteChild{isSuite: true, suite: suite})
		regStack = append(regStack, suite)
		regMu.Unlock()
		// 同步执行 suite 函数体（其内的 it/describe/beforeEach 注册子项）。
		if f, ok := fn.AsFunction(); ok {
			if _, err := f.Call(nil); err != nil {
				interpreter.ReportUncaught(nil, err)
			}
		}
		regMu.Lock()
		regStack = regStack[:len(regStack)-1]
		regMu.Unlock()
		return engine.Undefined(), nil
	})
	_ = m.Set("describe", descFn)
	_ = m.Set("suite", descFn)

	// it(name, fn) / it(fn) / it(name, {skip|todo|only}, fn)：注册用例到当前套件。
	// Node 22 语义：it 与 test 是同一函数对象（alias）。
	register := func(args []engine.Value) (engine.Value, error) {
		name, fn, opts := parseOptions(args)
		if !fn.IsFunction() && !opts.skip && !opts.todo {
			return engine.Undefined(), fmt.Errorf("%w: it() requires a function", engine.ErrTypeError)
		}
		if !fn.IsFunction() {
			fn = engine.Undefined() // Node 语义：it(name, {skip|todo}) 允许省略 fn
		}
		tc := &registeredTest{name: name, fn: fn, skip: opts.skip, todo: opts.todo, only: opts.only}
		regMu.Lock()
		cur := regStack[len(regStack)-1]
		cur.tests = append(cur.tests, tc)
		cur.children = append(cur.children, suiteChild{isSuite: false, test: tc})
		regMu.Unlock()
		return engine.Undefined(), nil
	}
	itFn := engine.NewFunction("test", register)
	_ = m.Set("it", itFn)
	_ = m.Set("test", itFn)

	// it.skip / it.todo / test.skip / test.todo / describe.skip：标记注册。
	skipReg := func(args []engine.Value) (engine.Value, error) {
		name, fn, opts := parseOptions(args)
		opts.skip = true
		if !fn.IsFunction() {
			fn = engine.Undefined() // Node 语义：test('x', {skip}) 允许省略 fn
		}
		tc := &registeredTest{name: name, fn: fn, skip: true}
		regMu.Lock()
		cur := regStack[len(regStack)-1]
		cur.tests = append(cur.tests, tc)
		cur.children = append(cur.children, suiteChild{isSuite: false, test: tc})
		regMu.Unlock()
		return engine.Undefined(), nil
	}
	todoReg := func(args []engine.Value) (engine.Value, error) {
		name, fn, _ := parseOptions(args)
		if !fn.IsFunction() {
			fn = engine.Undefined() // Node 语义：test('x', {todo}) 允许省略 fn
		}
		tc := &registeredTest{name: name, fn: fn, todo: true}
		regMu.Lock()
		cur := regStack[len(regStack)-1]
		cur.tests = append(cur.tests, tc)
		cur.children = append(cur.children, suiteChild{isSuite: false, test: tc})
		regMu.Unlock()
		return engine.Undefined(), nil
	}
	onlyReg := func(args []engine.Value) (engine.Value, error) {
		name, fn, _ := parseOptions(args)
		if !fn.IsFunction() {
			return engine.Undefined(), fmt.Errorf("%w: it.only() requires a function", engine.ErrTypeError)
		}
		tc := &registeredTest{name: name, fn: fn, only: true}
		regMu.Lock()
		cur := regStack[len(regStack)-1]
		cur.tests = append(cur.tests, tc)
		cur.children = append(cur.children, suiteChild{isSuite: false, test: tc})
		regMu.Unlock()
		return engine.Undefined(), nil
	}
	itV, _ := m.Get("it")
	if ito, ok := itV.AsObject(); ok {
		_ = ito.Set("skip", engine.NewFunction("skip", skipReg))
		_ = ito.Set("todo", engine.NewFunction("todo", todoReg))
		_ = ito.Set("only", engine.NewFunction("only", onlyReg))
	}
	testV, _ := m.Get("test")
	if tto, ok := testV.AsObject(); ok {
		_ = tto.Set("skip", engine.NewFunction("skip", skipReg))
		_ = tto.Set("todo", engine.NewFunction("todo", todoReg))
		_ = tto.Set("only", engine.NewFunction("only", onlyReg))
	}
	descV, _ := m.Get("describe")
	if dso, ok := descV.AsObject(); ok {
		_ = dso.Set("skip", engine.NewFunction("skip", func(args []engine.Value) (engine.Value, error) {
			name, fn, opts := parseOptions(args)
			opts.skip = true
			if !fn.IsFunction() {
				fn = engine.Undefined() // Node 语义：describe.skip('x') 允许省略 fn
			}
			suite := &registeredSuite{name: name, skip: true}
			regMu.Lock()
			cur := regStack[len(regStack)-1]
			cur.suites = append(cur.suites, suite)
			cur.children = append(cur.children, suiteChild{isSuite: true, suite: suite})
			regStack = append(regStack, suite)
			regMu.Unlock()
			if f, ok := fn.AsFunction(); ok {
				if _, err := f.Call(nil); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
			regMu.Lock()
			regStack = regStack[:len(regStack)-1]
			regMu.Unlock()
			return engine.Undefined(), nil
		}))
		_ = dso.Set("only", engine.NewFunction("only", func(args []engine.Value) (engine.Value, error) {
			name, fn, opts := parseOptions(args)
			opts.only = true
			if !fn.IsFunction() {
				return engine.Undefined(), fmt.Errorf("%w: describe.only() requires a function", engine.ErrTypeError)
			}
			suite := &registeredSuite{name: name, only: true}
			regMu.Lock()
			cur := regStack[len(regStack)-1]
			cur.suites = append(cur.suites, suite)
			cur.children = append(cur.children, suiteChild{isSuite: true, suite: suite})
			regStack = append(regStack, suite)
			regMu.Unlock()
			if f, ok := fn.AsFunction(); ok {
				if _, err := f.Call(nil); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
			regMu.Lock()
			regStack = regStack[:len(regStack)-1]
			regMu.Unlock()
			return engine.Undefined(), nil
		}))
		_ = dso.Set("todo", engine.NewFunction("todo", func(args []engine.Value) (engine.Value, error) {
			name, fn, opts := parseOptions(args)
			opts.todo = true
			if !fn.IsFunction() {
				fn = engine.Undefined() // Node 语义：describe.todo('x') 允许省略 fn
			}
			suite := &registeredSuite{name: name, skip: opts.skip, todo: opts.todo, only: opts.only}
			regMu.Lock()
			cur := regStack[len(regStack)-1]
			cur.suites = append(cur.suites, suite)
			cur.children = append(cur.children, suiteChild{isSuite: true, suite: suite})
			regStack = append(regStack, suite)
			regMu.Unlock()
			if f, ok := fn.AsFunction(); ok {
				if _, err := f.Call(nil); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
			regMu.Lock()
			regStack = regStack[:len(regStack)-1]
			regMu.Unlock()
			return engine.Undefined(), nil
		}))
	}

	// beforeEach(fn) / afterEach(fn) / before(fn) / after(fn)：挂到当前套件。
	hook := func(key string) engine.Func {
		return func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 || !args[0].IsFunction() {
				return engine.Undefined(), fmt.Errorf("%w: %s() requires a function", engine.ErrTypeError, key)
			}
			regMu.Lock()
			cur := regStack[len(regStack)-1]
			switch key {
			case "beforeEach":
				cur.beforeEach = append(cur.beforeEach, args[0])
			case "afterEach":
				cur.afterEach = append(cur.afterEach, args[0])
			case "before":
				cur.beforeHooks = append(cur.beforeHooks, args[0])
			case "after":
				cur.afterHooks = append(cur.afterHooks, args[0])
			}
			regMu.Unlock()
			return engine.Undefined(), nil
		}
	}
	_ = m.Set("beforeEach", engine.NewFunction("beforeEach", hook("beforeEach")))
	_ = m.Set("afterEach", engine.NewFunction("afterEach", hook("afterEach")))
	_ = m.Set("before", engine.NewFunction("before", hook("before")))
	_ = m.Set("after", engine.NewFunction("after", hook("after")))

	// mock：mock.method(target, name[, impl]) 替换对象方法为 spy，
	// spy.mock.calls 记录调用参数；mock.restoreAll() 全部还原。
	_ = m.Set("mock", newTestMock(ctx))

	// 顶层 shorthand：skip/todo/only 是 test.skip/test.todo/test.only 的别名
	// （Node 22 语义：test([name], { skip: true }[, fn])）。assert 导出
	// node:assert 模块对象（Node 22：test.assert 可用）。
	if av, err := NewAssert(ctx); err == nil {
		_ = m.Set("assert", av)
	}
	if sv, err := m.Get("test"); err == nil {
		if so, ok := sv.AsObject(); ok {
			for _, key := range []string{"skip", "todo", "only"} {
				if mv, gerr := so.Get(key); gerr == nil {
					_ = m.Set(key, mv)
				}
			}
		}
	}

	// register(name, fn)：注册自定义断言——Node 22.14 曾提供，22.23 运行时已
	// 不再导出（探针验证 undefined）；保留内部机制但不再暴露到模块面。
	_ = m.Set("register", engine.NewFunction("register", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("%w: register(name, fn) requires a name and a function", engine.ErrTypeError)
		}
		name := args[0].String()
		if !args[1].IsFunction() {
			return engine.Undefined(), fmt.Errorf("%w: register(name, fn): fn must be a function", engine.ErrTypeError)
		}
		registerCustomAssert(name, args[1])
		return engine.Undefined(), nil
	}))
	// snapshot 对象：自定义快照序列化与解析路径（Node 22：挂在 t.snapshot 下）。
	snapshotObj := engine.NewObject()
	_ = snapshotObj.Set("setDefaultSnapshotSerializers", engine.NewFunction("setDefaultSnapshotSerializers", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = snapshotObj.Set("setResolveSnapshotPath", engine.NewFunction("setResolveSnapshotPath", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = m.Set("snapshot", snapshotObj)

	// run(options)：程序化运行已注册用例（Node 22 语义）。返回事件流
	// （EventEmitter），异步派发 test:start/test:pass/test:fail/test:skip/
	// test:todo/test:plan/end 事件；同时标记 CLI 不再重复执行。
	_ = m.Set("run", engine.NewFunction("run", func(args []engine.Value) (engine.Value, error) {
		vm := currentVM(ctx)
		if vm == nil {
			return engine.Undefined(), nil
		}
		TestProgrammaticRun = true
		stream := newEmitterInstance()
		// 最小 stream 语义：pipe 返回自身。
		if so, ok := stream.AsObject(); ok {
			_ = so.Set("pipe", engine.NewFunction("pipe", func(ca []engine.Value) (engine.Value, error) {
				return stream, nil
			}))
		}
		ctx.PostTask(func() {
			results := RunRegisteredTests(vm)
			passing, failing, skipped, todo, cancelled := 0, 0, 0, 0, 0
			for _, r := range results {
				data := engine.NewObjectFrom(map[string]engine.Value{
					"name": engine.Str(r.Name),
				})
				_, _ = callEmitterMethod(stream, "emit", []engine.Value{engine.Str("test:start"), data})
				details := engine.NewObjectFrom(map[string]engine.Value{
					"duration_ms": engine.IntValue(int(r.Duration.Milliseconds())),
					"type":        engine.Str("test"),
				})
				d := engine.NewObjectFrom(map[string]engine.Value{
					"name":    engine.Str(r.Name),
					"details": details,
				})
				switch {
				case r.Cancelled:
					cancelled++
					_, _ = callEmitterMethod(stream, "emit", []engine.Value{engine.Str("test:fail"), d})
				case r.Skipped:
					skipped++
					_, _ = callEmitterMethod(stream, "emit", []engine.Value{engine.Str("test:skip"), d})
				case r.Todo:
					todo++
					_, _ = callEmitterMethod(stream, "emit", []engine.Value{engine.Str("test:todo"), d})
				case r.Passed:
					passing++
					_, _ = callEmitterMethod(stream, "emit", []engine.Value{engine.Str("test:pass"), d})
				default:
					failing++
					if r.Error != "" {
						details.Set("error", engine.Str(r.Error))
					}
					_, _ = callEmitterMethod(stream, "emit", []engine.Value{engine.Str("test:fail"), d})
				}
			}
			planEnd := engine.NewObjectFrom(map[string]engine.Value{
				"count":     engine.IntValue(len(results)),
				"passing":   engine.IntValue(passing),
				"failing":   engine.IntValue(failing),
				"skipped":   engine.IntValue(skipped),
				"todo":      engine.IntValue(todo),
				"cancelled": engine.IntValue(cancelled),
			})
			plan := engine.NewObjectFrom(map[string]engine.Value{
				"type": engine.Str("test"),
				"end":  planEnd,
			})
			_, _ = callEmitterMethod(stream, "emit", []engine.Value{engine.Str("test:plan"), plan})
			_, _ = callEmitterMethod(stream, "emit", []engine.Value{engine.Str("end")})
		})
		return stream, nil
	}))

	return m, nil
}

// customAsserts 是 register() 注册的自定义断言（name → fn）。
var customAsserts = map[string]engine.Value{}

// registerCustomAssert 注册自定义断言。
func registerCustomAssert(name string, fn engine.Value) {
	regMu.Lock()
	customAsserts[name] = fn
	regMu.Unlock()
}

// testOpts 用例选项（Node 语义：skip/todo/only）。
type testOpts struct {
	skip bool
	todo bool
	only bool
}

// applyTestOpts 从 options 对象读取 skip/todo/only。
func applyTestOpts(o engine.Object, opts *testOpts) {
	if v, err := o.Get("skip"); err == nil && !v.IsUndefined() {
		if b, ok := v.Bool(); ok {
			opts.skip = b
		}
	}
	if v, err := o.Get("todo"); err == nil && !v.IsUndefined() {
		if b, ok := v.Bool(); ok {
			opts.todo = b
		}
	}
	if v, err := o.Get("only"); err == nil && !v.IsUndefined() {
		if b, ok := v.Bool(); ok {
			opts.only = b
		}
	}
}

// testNameAndFn 从 (name, fn) / (fn) / (name) 形态提取名字与函数。
func testNameAndFn(args []engine.Value) (string, engine.Value) {
	if len(args) == 0 {
		return "anonymous", engine.Undefined()
	}
	if len(args) >= 2 {
		return args[0].String(), args[1]
	}
	// 单参数形态：
	//  - 字符串 → 纯名字（配合 skip/todo 标记，无 fn）。
	//  - 函数 → 名字取函数名。
	if args[0].Type() == engine.TypeString {
		return args[0].String(), engine.Undefined()
	}
	if f, ok := args[0].AsObject(); ok {
		if n, err := f.Get("name"); err == nil && !n.IsUndefined() && n.String() != "" {
			return n.String(), args[0]
		}
	}
	return "anonymous", args[0]
}

// --- mock -----------------------------------------------------------------

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

// newAbortSignal 构造独立 AbortSignal（复用全局 AbortSignal 构造器）。
func newAbortSignal(ctx engine.Context) engine.Value {
	if ctorV, err := ctx.Global().Get("AbortSignal"); err == nil {
		if ctor, ok := ctorV.AsFunction(); ok {
			if sig, cerr := ctor.Call(nil); cerr == nil && sig != nil {
				return sig
			}
		}
	}
	return engine.NewObject()
}

// abortTestSignal 中断测试信号（t.signal）：设置 aborted/reason，触发
// onabort 回调与 'abort' 事件（Node 语义：测试超时/取消时中断）。
func abortTestSignal(sig engine.Value) {
	if sig == nil {
		return
	}
	o, ok := sig.AsObject()
	if !ok {
		return
	}
	if v, err := o.Get("aborted"); err == nil {
		if b, ok := v.Bool(); ok && b {
			return // 已中断
		}
	}
	_ = o.Set("aborted", engine.Boolean(true))
	_ = o.Set("reason", engine.Str("Test cancelled by parent"))
	if v, err := o.Get("onabort"); err == nil && v.IsFunction() {
		if f, ok := v.AsFunction(); ok {
			if _, err := f.Call(nil); err != nil {
				interpreter.ReportUncaught(nil, err)
			}
		}
	}
	if d, err := o.Get("dispatchEvent"); err == nil && d.IsFunction() {
		if f, ok := d.AsFunction(); ok {
			ev := engine.NewObject()
			_ = ev.Set("type", engine.Str("abort"))
			if _, err := f.Call([]engine.Value{ev}); err != nil {
				interpreter.ReportUncaught(nil, err)
			}
		}
	}
}

// --- 执行器 ---------------------------------------------------------------

// 运行配置（由 cmdTest 设置）。
var (
	// TestNamePattern 非 nil 时只运行匹配完整名称的用例（--test-name-pattern）。
	TestNamePattern *regexp.Regexp
	// TestSkipPattern 非 nil 时跳过匹配完整名称的用例（--test-skip-pattern）。
	TestSkipPattern *regexp.Regexp
	// TestOnly 启用 only 模式（--test-only）：只运行仅标记的用例。
	TestOnly bool
	// TestProgrammaticRun 表示用例已由 t.run() 程序化执行（CLI 不再重复）。
	TestProgrammaticRun bool
	// TestDefaultTimeout 是 --test-timeout 注入的全局默认用例超时（0 = 无限）。
	TestDefaultTimeout time.Duration
)

// TestResult 是单个用例的执行结果。
type TestResult struct {
	Name      string
	FullName  string
	Passed    bool
	Skipped   bool
	Todo      bool
	Cancelled bool // 子测试被取消（父未 await——Node 语义，独立统计）
	Error     string
	Duration  time.Duration
}

// RunRegisteredTests 执行 registry 中全部用例，返回结果列表。
// vm 用于调用 JS 函数与驱动 promise（async 用例/钩子）。
// only 模式仅在 --test-only 标志下生效（Node 语义：it.only 标记
// 本身不改变无标志运行的执行集合）。
func RunRegisteredTests(vm *interpreter.VM) []TestResult {
	regMu.Lock()
	root := regRoot
	regMu.Unlock()
	var results []TestResult
	runSuite(vm, root, "", &results, false, false, TestOnly)
	return results
}

// joinName 拼接嵌套名称（Node 语义："parent > child"）。
func joinName(prefix, name string) string {
	if prefix == "" {
		return name
	}
	if name == "" {
		return prefix
	}
	return prefix + " > " + name
}

// runSuite 按注册顺序执行套件（children 混合遍历——Node 语义），
// 处理套件级 before/after 钩子与 skip/only 传播。
func runSuite(vm *interpreter.VM, s *registeredSuite, prefix string, results *[]TestResult, inheritedSkip, inheritedTodo, only bool) {
	skip := inheritedSkip || s.skip
	todo := inheritedTodo || s.todo
	only = only || s.only
	pfx := joinName(prefix, s.name)

	// 无任何可运行测试（空套件/全 skip/only 过滤）：空套件无输出；
	// 有测试但不可运行 → 全部标 SKIP；only 模式下非 only 内容完全隐藏
	// （Node 语义：--test-only 只输出仅标记的测试）。
	if !suiteHasRunnable(s, skip, only) {
		if hasAnyChild(s) && !only {
			markAllSkipped(s, pfx, results)
		}
		return
	}

	// before（套件级，首用例前执行一次）。
	for _, h := range s.beforeHooks {
		if err := invokeTestFn(vm, h); err != nil {
			failAllTests(vm, s, pfx, results, "before: "+testErrorMessage(vm, err))
			return
		}
	}

	// 注册顺序执行 children（tests 与 suites 混合）。
	for _, ch := range s.children {
		if ch.isSuite {
			runSuite(vm, ch.suite, pfx, results, skip, todo, only)
		} else {
			if r := runTestCase(vm, s, ch.test, joinName(pfx, ch.test.name), skip, todo, only); r != nil {
				*results = append(*results, r...)
			}
		}
	}

	// after（套件级，末用例后执行一次）。
	for _, h := range s.afterHooks {
		if err := invokeTestFn(vm, h); err != nil {
			*results = append(*results, TestResult{Name: s.name, FullName: joinName(pfx, "after hook"), Passed: false, Error: "after: " + testErrorMessage(vm, err)})
			return
		}
	}
}

// suiteHasRunnable 判断套件内是否存在将实际执行的用例
// （不受 skip 传播与 only 过滤影响）。
func suiteHasRunnable(s *registeredSuite, skip, only bool) bool {
	if skip {
		return false
	}
	for _, t := range s.tests {
		if t.skip {
			continue
		}
		if !only || t.only {
			return true
		}
	}
	for _, sub := range s.suites {
		if suiteHasRunnable(sub, skip, only) {
			return true
		}
	}
	return false
}

// hasAnyChild 判断套件是否有注册内容（区分空套件）。
func hasAnyChild(s *registeredSuite) bool {
	return len(s.children) > 0
}

// markAllSkipped 把套件内全部用例标记为 SKIP（递归，保留名称层级）。
func markAllSkipped(s *registeredSuite, pfx string, results *[]TestResult) {
	for _, ch := range s.children {
		if ch.isSuite {
			markAllSkipped(ch.suite, joinName(pfx, ch.suite.name), results)
		} else {
			full := joinName(pfx, ch.test.name)
			*results = append(*results, TestResult{Name: ch.test.name, FullName: full, Passed: true, Skipped: true})
		}
	}
}

// failAllTests 钩子失败时套件内全部用例标失败（Node 语义：before 失败 → 套件失败）。
func failAllTests(vm *interpreter.VM, s *registeredSuite, pfx string, results *[]TestResult, msg string) {
	for _, ch := range s.children {
		if ch.isSuite {
			failAllTests(vm, ch.suite, joinName(pfx, ch.suite.name), results, msg)
		} else {
			full := joinName(pfx, ch.test.name)
			*results = append(*results, TestResult{Name: ch.test.name, FullName: full, Passed: false, Error: msg})
		}
	}
}

// namePatternExcluded 判断用例是否被 --test-name-pattern 过滤
// （匹配完整名称，含套件层级——Node 语义）。
func namePatternExcluded(full string) bool {
	if TestNamePattern == nil {
		return false
	}
	return !TestNamePattern.MatchString(full)
}

// runTestCase 执行 beforeEach → 用例 → 子测试 → afterEach（套件链上的钩子）。
// 返回 []TestResult：首元素为用例自身，其余为子测试（Node 统计语义：
// 子测试独立计数）。钩子/用例的 promise 结果经 AwaitPromise 同步等待。
// only 模式排除的用例返回 nil（Node 语义：--test-only 下完全隐藏，非 SKIP）。
func runTestCase(vm *interpreter.VM, suite *registeredSuite, tc *registeredTest, full string, suiteSkip, suiteTodo, only bool) []TestResult {
	res := &TestResult{Name: tc.name, FullName: full, Passed: true}
	start := time.Now()
	defer func() { res.Duration = time.Since(start) }()

	// only 模式排除：不执行、不输出（Node 语义：--test-only 下完全隐藏）。
	if only && !tc.only {
		return nil
	}
	// name-pattern 过滤：不匹配 → 不执行、不输出（Node 语义）。
	if namePatternExcluded(full) {
		return nil
	}
	// skip-pattern 过滤：匹配 → 完全排除（不执行、不计数——Node 实测语义：
	// --test-skip-pattern 命中的测试从运行集合中移除，tests 计数不含它们）。
	if TestSkipPattern != nil && TestSkipPattern.MatchString(full) {
		return nil
	}
	// skip 判定：套件 skip || 用例 skip（显示 # SKIP）。
	if suiteSkip || tc.skip {
		res.Skipped = true
		return []TestResult{*res}
	}
	// todo 判定：套件 todo 传播 || 用例 todo（Node 语义：todo 仍执行，失败不计）。
	if tc.todo || suiteTodo {
		res.Todo = true
	}

	// 收集套件链（根 → 叶）。
	var chain []*registeredSuite
	for cur := suite; cur != nil; cur = parentSuite(cur) {
		chain = append([]*registeredSuite{cur}, chain...)
	}

	// beforeEach（外层 → 内层）。
	for _, s := range chain {
		for _, h := range s.beforeEach {
			if err := invokeTestFn(vm, h); err != nil {
				res.Passed = false
				res.Error = "beforeEach: " + testErrorMessage(vm, err)
				return []TestResult{*res}
			}
		}
	}

	// 用例本体（t.plan 校验 + 子测试）。
	st := newTestRunState(vm, tc.name, full, tc.fn)
	cancelled := false
	// t.mock 的 spy 在测试结束时自动还原（Node 语义）。
	defer func() {
		st.mu.Lock()
		if st.mockRestore != nil {
			st.mockRestore()
		}
		st.mu.Unlock()
	}()
	if err := invokeTestFnWithState(vm, tc.fn, st); err != nil {
		if errors.Is(err, errTestSkipped) {
			res.Skipped = true
			return []TestResult{*res}
		}
		cancelled = errors.Is(err, errSubtestsFailed)
		res.Passed = false
		res.Error = testErrorMessage(vm, err)
	} else if pe := st.planError(); pe != nil {
		res.Passed = false
		res.Error = pe.Error()
	}
	// 子测试失败传播（Node 语义：'1 subtest failed'）。
	if res.Passed && !res.Skipped {
		for _, sr := range st.subResults {
			if !sr.Passed {
				res.Passed = false
				if res.Error == "" {
					res.Error = sr.FullName + ": " + sr.Error
				}
			}
		}
	}

	// afterEach（内层 → 外层）。
	for i := len(chain) - 1; i >= 0; i-- {
		for _, h := range chain[i].afterEach {
			if err := invokeTestFn(vm, h); err != nil {
				res.Passed = false
				res.Error = "afterEach: " + testErrorMessage(vm, err)
				return []TestResult{*res}
			}
		}
	}

	// 子测试独立计数（Node 统计语义）。
	// --test-timeout 全局默认：超时用例标失败（近似实现——不抢占同步执行，
	// 仅事后判定；挂死用例仍会阻塞，见 knownDifference）。
	if TestDefaultTimeout > 0 && time.Since(start) > TestDefaultTimeout && !res.Skipped && !res.Todo {
		res.Passed = false
		res.Error = fmt.Sprintf("test timed out after %dms", TestDefaultTimeout.Milliseconds())
	}
	out := []TestResult{*res}
	if cancelled {
		// 同步父测试取消：未执行的子测试标 cancelled（Node 语义）。
		st.mu.Lock()
		for _, sub := range st.subtests {
			out = append(out, TestResult{Name: sub.name, FullName: sub.full, Passed: true, Cancelled: true})
			abortTestSignal(sub.signal)
		}
		abortTestSignal(st.signal)
		st.mu.Unlock()
	} else {
		out = append(out, st.subResults...)
	}
	return out
}

// runSubTestSync 执行子测试（在微任务中调度；Node 语义：子测试失败/被
// 取消由父测试汇报，不单独输出）。
func runSubTestSync(vm *interpreter.VM, st *testRunState) TestResult {
	res := TestResult{Name: st.name, FullName: st.full, Passed: true}
	start := time.Now()
	defer func() { res.Duration = time.Since(start) }()
	if st.skipRequested {
		res.Skipped = true
		return res
	}
	if st.todo {
		res.Todo = true
	}
	cancelled := false
	if err := invokeTestFnWithState(vm, st.fn, st); err != nil {
		if errors.Is(err, errTestSkipped) {
			res.Skipped = true
			return res
		}
		cancelled = errors.Is(err, errSubtestsFailed)
		res.Passed = false
		res.Error = testErrorMessage(vm, err)
	} else if pe := st.planError(); pe != nil {
		res.Passed = false
		res.Error = pe.Error()
	}
	// 嵌套子测试失败传播。
	if res.Passed && !res.Skipped {
		for _, sr := range st.subResults {
			if !sr.Passed {
				res.Passed = false
				if res.Error == "" {
					res.Error = sr.FullName + ": " + sr.Error
				}
			}
		}
	}
	// 同步子测试取消：嵌套子测试标 cancelled（Node 语义）。
	if cancelled {
		st.mu.Lock()
		for _, sub := range st.subtests {
			st.subResults = append(st.subResults, TestResult{Name: sub.name, FullName: sub.full, Passed: true, Cancelled: true})
			abortTestSignal(sub.signal)
		}
		abortTestSignal(st.signal)
		st.mu.Unlock()
	}
	// t.mock 的 spy 在子测试结束时自动还原。
	st.mu.Lock()
	if st.mockRestore != nil {
		st.mockRestore()
	}
	st.mu.Unlock()
	return res
}

// parentSuite 返回套件的父套件（线性扫描 registry）。
func parentSuite(target *registeredSuite) *registeredSuite {
	regMu.Lock()
	defer regMu.Unlock()
	var find func(s *registeredSuite) *registeredSuite
	find = func(s *registeredSuite) *registeredSuite {
		for _, sub := range s.suites {
			if sub == target {
				return s
			}
			if r := find(sub); r != nil {
				return r
			}
		}
		return nil
	}
	return find(regRoot)
}

// --- 用例运行状态（t.plan / 子测试 / t.skip）-----------------------------

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

// --- TestContext（t 参数）--------------------------------------------------

// snapshotState 记录当前测试文件的快照状态（文件路径 + 调用计数）。
var snapshotMu sync.Mutex
var snapshotFile string  // 当前测试文件对应的快照文件路径
var snapshotCount int    // 当前文件内 snapshot 调用计数
var updateSnapshots bool // --test-update-snapshots 模式

// SetSnapshotFile 由测试运行器设置当前测试文件（用于快照定位）。
func SetSnapshotFile(testFilePath string) {
	snapshotMu.Lock()
	defer snapshotMu.Unlock()
	snapshotFile = testFilePath + ".snapshot"
	snapshotCount = 0
	currentTestFilePath = testFilePath
}

// SetUpdateSnapshots 启用/禁用快照更新模式（--test-update-snapshots）。
func SetUpdateSnapshots(update bool) {
	snapshotMu.Lock()
	updateSnapshots = update
	snapshotMu.Unlock()
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

// vmRegexpTest 用 vm.Eval 绑定 this 调用正则 .test()（直接 f.Call 丢失 this）。
func vmRegexpTest(vm *interpreter.VM, re, target engine.Value) bool {
	g := vm.Global()
	_ = g.Set("__tAssertRe", re)
	_ = g.Set("__tAssertTarget", target)
	defer g.Delete("__tAssertRe")
	defer g.Delete("__tAssertTarget")
	if v, err := vm.Eval("__tAssertRe.test(__tAssertTarget)", "test_assert_regexp.js"); err == nil {
		if b, ok := v.Bool(); ok {
			return b
		}
	}
	return false
}

// testDeepStrictEqual 递归严格深度相等（Node assert.deepStrictEqual 语义）。
// 对象键集一致且每键值严格深等；数组逐元素；原始值要求类型相同。
func testDeepStrictEqual(a, b engine.Value) bool {
	if a == nil || b == nil {
		return a == b
	}
	if strictEqual(a, b) {
		return true
	}
	if arrA, ok := a.(*engine.ArrayValue); ok {
		arrB, ok := b.(*engine.ArrayValue)
		if !ok {
			return false
		}
		elemsA, elemsB := arrA.Elems(), arrB.Elems()
		if len(elemsA) != len(elemsB) {
			return false
		}
		for i := range elemsA {
			if !testDeepStrictEqual(elemsA[i], elemsB[i]) {
				return false
			}
		}
		return true
	}
	if oa, ok := a.AsObject(); ok {
		ob, okb := b.AsObject()
		if !okb {
			return false
		}
		keysA := oa.Keys()
		keysB := ob.Keys()
		if len(keysA) != len(keysB) {
			return false
		}
		for _, k := range keysA {
			va, _ := oa.Get(k)
			vb, err := ob.Get(k)
			if err != nil {
				return false
			}
			if !testDeepStrictEqual(va, vb) {
				return false
			}
		}
		return true
	}
	if a.Type() != b.Type() {
		return false
	}
	return a.String() == b.String()
}

// testDeepLooseEqual 递归宽松深度相等（== 语义：数字/字符串可转换比较）。
func testDeepLooseEqual(a, b engine.Value) bool {
	if testDeepStrictEqual(a, b) {
		return true
	}
	if a.Type() == engine.TypeNumber && b.Type() == engine.TypeString {
		if bf, ok := b.Float(); ok {
			af, _ := a.Float()
			return af == bf
		}
	}
	if a.Type() == engine.TypeString && b.Type() == engine.TypeNumber {
		if af, ok := a.Float(); ok {
			bf, _ := b.Float()
			return af == bf
		}
	}
	return false
}

// snapshotAssert 实现快照断言。
// 序列化格式（Node 22）：字符串 → JSON 字符串（带引号）；对象 → JSON 2 空格。
// 快照文件：<testfile>.snapshot，条目格式 exports[`snap <n>`] = `\n<serialized>\n`;
func snapshotAssert(vm *interpreter.VM, value engine.Value) (engine.Value, error) {
	snapshotMu.Lock()
	file := snapshotFile
	snapshotCount++
	idx := snapshotCount
	update := updateSnapshots
	snapshotMu.Unlock()

	if file == "" {
		return engine.Undefined(), fmt.Errorf("%w: snapshot: no test file context", engine.ErrAssertion)
	}

	// 序列化。
	var serialized string
	switch value.Type() {
	case engine.TypeString:
		b, _ := json.Marshal(value.String())
		serialized = string(b)
	default:
		serialized = snapshotJSON(vm, value)
	}
	entry := fmt.Sprintf("exports[`snap %d`] = `\n%s\n`;\n", idx, serialized)

	// 读取现有快照文件。
	existing := ""
	if data, err := os.ReadFile(file); err == nil {
		existing = string(data)
	}

	if update {
		// 更新模式：写回整个文件（保留其他条目，替换当前编号）。
		merged := snapshotReplaceEntry(existing, idx, entry)
		_ = os.MkdirAll(filepath.Dir(file), 0755)
		return engine.Undefined(), os.WriteFile(file, []byte(merged), 0644)
	}

	// 比较模式。
	if existing == "" {
		return engine.Undefined(), fmt.Errorf("%w: snapshot not found (run with --test-update-snapshots)", engine.ErrAssertion)
	}
	if !strings.Contains(existing, fmt.Sprintf("exports[`snap %d`]", idx)) {
		return engine.Undefined(), fmt.Errorf("%w: snapshot %d not found in %s", engine.ErrAssertion, idx, file)
	}
	if strings.Contains(existing, entry) {
		return engine.Undefined(), nil // 匹配
	}
	return engine.Undefined(), fmt.Errorf("%w: snapshot %d mismatch", engine.ErrAssertion, idx)
}

// snapshotReplaceEntry 替换/追加编号条目。
func snapshotReplaceEntry(existing string, idx int, entry string) string {
	marker := fmt.Sprintf("exports[`snap %d`]", idx)
	// 按块分割（每个条目以 exports[`snap N`] 开头）。
	lines := strings.Split(existing, "\n")
	var out []string
	replaced := false
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			block := strings.Join(cur, "\n")
			if strings.Contains(block, marker) {
				out = append(out, entry)
				replaced = true
			} else {
				out = append(out, block)
			}
		}
		cur = nil
	}
	for _, ln := range lines {
		if strings.HasPrefix(ln, "exports[`snap ") {
			flush()
		}
		cur = append(cur, ln)
	}
	flush()
	if !replaced {
		out = append(out, entry)
	}
	return strings.Join(out, "\n")
}

// snapshotJSON 序列化快照值：对象 → JSON 2 空格缩进（Node 快照格式）；
// 键序保持插入序；不做 HTML 转义。
func snapshotJSON(vm *interpreter.VM, value engine.Value) string {
	data, err := snapToGo(value)
	if err != nil {
		return value.String()
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		return value.String()
	}
	s := buf.String()
	return strings.TrimRight(s, "\n")
}

// snapOrdered 保持插入键序的 JSON 容器。
type snapOrdered struct {
	keys []string
	vals []interface{}
}

func (o *snapOrdered) MarshalJSON() ([]byte, error) {
	parts := make([]string, len(o.keys))
	for i, k := range o.keys {
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		vb, err := json.Marshal(o.vals[i])
		if err != nil {
			return nil, err
		}
		parts[i] = string(kb) + ":" + string(vb)
	}
	return []byte("{" + strings.Join(parts, ",") + "}"), nil
}

// snapToGo 把 engine.Value 转为可 JSON 序列化的 Go 结构（插入键序）。
func snapToGo(v engine.Value) (interface{}, error) {
	if v == nil || v.IsUndefined() || v.IsNull() {
		return nil, nil
	}
	switch v.Type() {
	case engine.TypeBoolean:
		b, _ := v.Bool()
		return b, nil
	case engine.TypeNumber:
		f, _ := v.Float()
		return f, nil
	case engine.TypeString:
		return v.String(), nil
	}
	if arr, ok := v.(*engine.ArrayValue); ok {
		out := make([]interface{}, 0, len(arr.Elems()))
		for _, e := range arr.Elems() {
			ev, err := snapToGo(e)
			if err != nil {
				return nil, err
			}
			out = append(out, ev)
		}
		return out, nil
	}
	if o, ok := v.AsObject(); ok {
		so := &snapOrdered{}
		for _, k := range o.Keys() {
			if k == "length" {
				continue
			}
			val, _ := o.Get(k)
			if val.IsFunction() || val.IsUndefined() {
				continue
			}
			ev, err := snapToGo(val)
			if err != nil {
				return nil, err
			}
			so.keys = append(so.keys, k)
			so.vals = append(so.vals, ev)
		}
		return so, nil
	}
	return nil, nil
}

// testErrorMessage 从 Go error 提取 JS 错误消息（jsThrow → 错误对象 message）。
func testErrorMessage(vm *interpreter.VM, err error) string {
	val := interpreter.ExtractThrowValue(err, vm.Interp())
	if val.IsUndefined() || val.IsNull() {
		return err.Error()
	}
	if o, ok := val.AsObject(); ok {
		if msg, gerr := o.Get("message"); gerr == nil && !msg.IsUndefined() {
			return msg.String()
		}
	}
	return val.String()
}
