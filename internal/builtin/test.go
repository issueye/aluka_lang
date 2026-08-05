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
	"fmt"
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
}

type registeredSuite struct {
	name        string
	beforeHooks []engine.Value
	afterHooks  []engine.Value
	tests       []*registeredTest
	suites      []*registeredSuite
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

	// describe(name, fn) / describe(fn)：注册套件并同步执行函数体。
	_ = m.Set("describe", engine.NewFunction("describe", func(args []engine.Value) (engine.Value, error) {
		name, fn := testNameAndFn(args)
		if !fn.IsFunction() {
			return engine.Undefined(), fmt.Errorf("%w: describe() requires a function", engine.ErrTypeError)
		}
		suite := &registeredSuite{name: name}
		regMu.Lock()
		cur := regStack[len(regStack)-1]
		cur.suites = append(cur.suites, suite)
		regStack = append(regStack, suite)
		regMu.Unlock()
		// 同步执行 suite 函数体（其内的 it/describe/beforeEach 注册子项）。
		if f, ok := fn.AsFunction(); ok {
			_, _ = f.Call(nil)
		}
		regMu.Lock()
		regStack = regStack[:len(regStack)-1]
		regMu.Unlock()
		return engine.Undefined(), nil
	}))

	// it(name, fn) / it(fn)：注册用例到当前套件。
	register := func(args []engine.Value) (engine.Value, error) {
		name, fn := testNameAndFn(args)
		if !fn.IsFunction() {
			return engine.Undefined(), fmt.Errorf("%w: it() requires a function", engine.ErrTypeError)
		}
		regMu.Lock()
		cur := regStack[len(regStack)-1]
		cur.tests = append(cur.tests, &registeredTest{name: name, fn: fn})
		regMu.Unlock()
		return engine.Undefined(), nil
	}
	_ = m.Set("it", engine.NewFunction("it", register))
	_ = m.Set("test", engine.NewFunction("test", register))

	// beforeEach(fn) / afterEach(fn)：挂到当前套件。
	hook := func(key string) engine.Func {
		return func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 || !args[0].IsFunction() {
				return engine.Undefined(), fmt.Errorf("%w: %s() requires a function", engine.ErrTypeError, key)
			}
			regMu.Lock()
			cur := regStack[len(regStack)-1]
			if key == "beforeEach" {
				cur.beforeHooks = append(cur.beforeHooks, args[0])
			} else {
				cur.afterHooks = append(cur.afterHooks, args[0])
			}
			regMu.Unlock()
			return engine.Undefined(), nil
		}
	}
	_ = m.Set("beforeEach", engine.NewFunction("beforeEach", hook("beforeEach")))
	_ = m.Set("afterEach", engine.NewFunction("afterEach", hook("afterEach")))

	// mock：mock.method(target, name[, impl]) 替换对象方法为 spy，
	// spy.mock.calls 记录调用参数；mock.restoreAll() 全部还原。
	_ = m.Set("mock", newTestMock(ctx))

	return m, nil
}

// testNameAndFn 从 (name, fn) / (fn) 两种形态提取名字与函数。
func testNameAndFn(args []engine.Value) (string, engine.Value) {
	if len(args) == 0 {
		return "anonymous", engine.Undefined()
	}
	if len(args) >= 2 {
		return args[0].String(), args[1]
	}
	// 单参数形态：名字取函数名。
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
}

var mockSpies []*mockSpy

func newTestMock(ctx engine.Context) engine.Value {
	mockObj := engine.NewObject()
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
		spy := &mockSpy{target: target, method: method, original: original}
		calls := engine.NewArray(nil)
		engine.SetProto(calls, nil)
		callsObj := engine.NewObject()
		_ = callsObj.Set("calls", calls)
		spyFn := engine.NewFunction(method, func(ca []engine.Value) (engine.Value, error) {
			spy.mu.Lock()
			call := engine.NewArray(append([]engine.Value{}, ca...))
			calls.Append(call)
			spy.mu.Unlock()
			if impl != nil && impl.IsFunction() {
				if f, ok := impl.AsFunction(); ok {
					return f.Call(ca)
				}
			}
			if original.IsFunction() {
				if f, ok := original.AsFunction(); ok {
					return f.Call(ca)
				}
			}
			return engine.Undefined(), nil
		})
		if fo, ok := spyFn.AsObject(); ok {
			_ = fo.Set("mock", callsObj)
		}
		_ = target.Set(method, spyFn)
		spy.spyFn = spyFn
		mockSpies = append(mockSpies, spy)
		return spyFn, nil
	}))
	_ = mockObj.Set("restoreAll", engine.NewFunction("restoreAll", func(args []engine.Value) (engine.Value, error) {
		for _, s := range mockSpies {
			_ = s.target.Set(s.method, s.original)
		}
		mockSpies = nil
		return engine.Undefined(), nil
	}))
	return mockObj
}

// --- 执行器 ---------------------------------------------------------------

// TestResult 是单个用例的执行结果。
type TestResult struct {
	Name     string
	FullName string
	Passed   bool
	Error    string
	Duration time.Duration
}

// RunRegisteredTests 执行 registry 中全部用例，返回结果列表。
// vm 用于调用 JS 函数与驱动 promise（async 用例/钩子）。
func RunRegisteredTests(vm *interpreter.VM) []TestResult {
	regMu.Lock()
	root := regRoot
	regMu.Unlock()
	var results []TestResult
	runSuite(vm, root, "", &results)
	return results
}

func runSuite(vm *interpreter.VM, s *registeredSuite, prefix string, results *[]TestResult) {
	for _, sub := range s.suites {
		name := sub.name
		if prefix != "" {
			name = prefix + " > " + sub.name
		}
		runSuite(vm, sub, name, results)
	}
	for _, tc := range s.tests {
		full := tc.name
		if prefix != "" {
			full = prefix + " > " + tc.name
		}
		*results = append(*results, runTestCase(vm, s, tc, full))
	}
}

// runTestCase 执行 beforeEach → 用例 → afterEach（套件链上的钩子），
// 返回结果。钩子/用例的 promise 结果经 AwaitPromise 同步等待。
func runTestCase(vm *interpreter.VM, suite *registeredSuite, tc *registeredTest, full string) TestResult {
	res := TestResult{Name: tc.name, FullName: full, Passed: true}
	start := time.Now()
	defer func() { res.Duration = time.Since(start) }()

	// 收集套件链（根 → 叶）。
	var chain []*registeredSuite
	for cur := suite; cur != nil; cur = parentSuite(cur) {
		chain = append([]*registeredSuite{cur}, chain...)
	}

	// beforeEach（外层 → 内层）。
	for _, s := range chain {
		for _, h := range s.beforeHooks {
			if err := invokeTestFn(vm, h); err != nil {
				res.Passed = false
				res.Error = "beforeEach: " + testErrorMessage(vm, err)
				return res
			}
		}
	}

	// 用例本体。
	if err := invokeTestFn(vm, tc.fn); err != nil {
		res.Passed = false
		res.Error = testErrorMessage(vm, err)
		return res
	}

	// afterEach（内层 → 外层）。
	for i := len(chain) - 1; i >= 0; i-- {
		for _, h := range chain[i].afterHooks {
			if err := invokeTestFn(vm, h); err != nil {
				res.Passed = false
				res.Error = "afterEach: " + testErrorMessage(vm, err)
				return res
			}
		}
	}
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

// invokeTestFn 调用 JS 函数；返回 promise 时同步等待 settle。
func invokeTestFn(vm *interpreter.VM, fn engine.Value) error {
	if f, ok := fn.AsFunction(); ok {
		result, err := f.Call(nil)
		if err != nil {
			return err
		}
		if pv, ok := result.(*interpreter.PromiseValue); ok {
			_, err = vm.AwaitPromise(pv)
			return err
		}
		return nil
	}
	return fmt.Errorf("%w: not a function", engine.ErrTypeError)
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
