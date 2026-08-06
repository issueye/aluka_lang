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
	_ = m.Set("describe", engine.NewFunction("describe", func(args []engine.Value) (engine.Value, error) {
		name, fn, opts := parseOptions(args)
		if !fn.IsFunction() && !opts.skip {
			return engine.Undefined(), fmt.Errorf("%w: describe() requires a function", engine.ErrTypeError)
		}
		suite := &registeredSuite{name: name, skip: opts.skip}
		regMu.Lock()
		cur := regStack[len(regStack)-1]
		cur.suites = append(cur.suites, suite)
		cur.children = append(cur.children, suiteChild{isSuite: true, suite: suite})
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

	// it(name, fn) / it(fn) / it(name, {skip|todo|only}, fn)：注册用例到当前套件。
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
	_ = m.Set("it", engine.NewFunction("it", register))
	_ = m.Set("test", engine.NewFunction("test", register))

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
	itFn, _ := m.Get("it")
	if ito, ok := itFn.AsObject(); ok {
		_ = ito.Set("skip", engine.NewFunction("skip", skipReg))
		_ = ito.Set("todo", engine.NewFunction("todo", todoReg))
		_ = ito.Set("only", engine.NewFunction("only", onlyReg))
	}
	testFn, _ := m.Get("test")
	if tto, ok := testFn.AsObject(); ok {
		_ = tto.Set("skip", engine.NewFunction("skip", skipReg))
		_ = tto.Set("todo", engine.NewFunction("todo", todoReg))
		_ = tto.Set("only", engine.NewFunction("only", onlyReg))
	}
	descFn, _ := m.Get("describe")
	if dso, ok := descFn.AsObject(); ok {
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
				_, _ = f.Call(nil)
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
				_, _ = f.Call(nil)
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

	return m, nil
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
	calls *engine.ArrayValue
	isFn  bool // mock.fn 创建的独立函数（无 target）
}

var mockSpies []*mockSpy

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

// newMockCallsObj 构造 spy 的 .mock 对象（calls/restore）。
// 注意：Node 22 已移除 mock.reset（实测 fn.mock.reset 不存在），
// 仅保留 calls 与 restore。
func newMockCallsObj(spy *mockSpy) engine.Value {
	calls := engine.NewArray(nil)
	engine.SetProto(calls, nil)
	spy.calls = calls
	mo := engine.NewObject()
	_ = mo.Set("calls", calls)
	// mock.fn 的 restore：还原为原实现（无 target 时清空调用记录）。
	_ = mo.Set("restore", engine.NewFunction("restore", func(args []engine.Value) (engine.Value, error) {
		spy.mu.Lock()
		if !spy.isFn && spy.target != nil {
			_ = spy.target.Set(spy.method, spy.original)
			for i, s := range mockSpies {
				if s == spy {
					mockSpies = append(mockSpies[:i], mockSpies[i+1:]...)
					break
				}
			}
		}
		spy.mu.Unlock()
		return engine.Undefined(), nil
	}))
	return mo
}

// makeMockSpyFn 构造 spy 函数（记录调用 + 委托 impl/original）。
func makeMockSpyFn(vm *interpreter.VM, spy *mockSpy, impl engine.Value, original engine.Value, mo engine.Value) engine.Value {
	return interpreter.NewNativeMethod("mockSpy", func(this engine.Value, ca []engine.Value) (engine.Value, error) {
		spy.mu.Lock()
		call := newMockCall(ca, this, engine.Undefined(), nil, false, engine.Undefined())
		spy.calls.Append(call)
		spy.mu.Unlock()
		var result engine.Value
		var err error
		// 委托 impl/original 时保持 this 绑定（Node 语义：
		// mock 函数的 this 透传给原实现）。
		target := impl
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

func newTestMock(ctx engine.Context) engine.Value {
	mockObj := engine.NewObject()
	vm, _ := ctx.(*interpreter.VM)

	// mock.fn([impl]) → 独立 spy 函数（Node 22 语义）。
	_ = mockObj.Set("fn", engine.NewFunction("fn", func(args []engine.Value) (engine.Value, error) {
		var impl engine.Value
		if len(args) > 0 && args[0].IsFunction() {
			impl = args[0]
		}
		spy := &mockSpy{isFn: true}
		mo := newMockCallsObj(spy)
		fn := makeMockSpyFn(vm, spy, impl, engine.Undefined(), mo)
		if fo, ok := fn.AsObject(); ok {
			_ = fo.Set("mock", mo)
		}
		spy.spyFn = fn
		spy.original = impl
		mockSpies = append(mockSpies, spy)
		return fn, nil
	}))

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
		mo := newMockCallsObj(spy)
		spyFn := makeMockSpyFn(vm, spy, impl, original, mo)
		if fo, ok := spyFn.AsObject(); ok {
			_ = fo.Set("mock", mo)
		}
		_ = target.Set(method, spyFn)
		spy.spyFn = spyFn
		mockSpies = append(mockSpies, spy)
		return spyFn, nil
	}))

	_ = mockObj.Set("restoreAll", engine.NewFunction("restoreAll", func(args []engine.Value) (engine.Value, error) {
		for _, s := range mockSpies {
			if !s.isFn && s.target != nil {
				_ = s.target.Set(s.method, s.original)
			}
		}
		mockSpies = nil
		return engine.Undefined(), nil
	}))
	return mockObj
}

// --- 执行器 ---------------------------------------------------------------

// 运行配置（由 cmdTest 设置）。
var (
	// TestNamePattern 非 nil 时只运行匹配完整名称的用例（--test-name-pattern）。
	TestNamePattern *regexp.Regexp
	// TestOnly 启用 only 模式（--test-only）：只运行仅标记的用例。
	TestOnly bool
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
	runSuite(vm, root, "", &results, false, TestOnly)
	return results
}

// hasOnlyMark 递归检查 registry 是否存在 only 标记（测试或套件）。
func hasOnlyMark(s *registeredSuite) bool {
	if s.only {
		return true
	}
	for _, t := range s.tests {
		if t.only {
			return true
		}
	}
	for _, sub := range s.suites {
		if hasOnlyMark(sub) {
			return true
		}
	}
	return false
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
func runSuite(vm *interpreter.VM, s *registeredSuite, prefix string, results *[]TestResult, inheritedSkip, only bool) {
	skip := inheritedSkip || s.skip
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
			runSuite(vm, ch.suite, pfx, results, skip, only)
		} else {
			if r := runTestCase(vm, s, ch.test, joinName(pfx, ch.test.name), skip, only); r != nil {
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
func runTestCase(vm *interpreter.VM, suite *registeredSuite, tc *registeredTest, full string, suiteSkip, only bool) []TestResult {
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
	// skip 判定：套件 skip || 用例 skip（显示 # SKIP）。
	if suiteSkip || tc.skip {
		res.Skipped = true
		return []TestResult{*res}
	}
	if tc.todo {
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
	out := []TestResult{*res}
	if cancelled {
		// 同步父测试取消：未执行的子测试标 cancelled（Node 语义）。
		st.mu.Lock()
		for _, sub := range st.subtests {
			out = append(out, TestResult{Name: sub.name, FullName: sub.full, Passed: true, Cancelled: true})
		}
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
		}
		st.mu.Unlock()
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

// --- 用例运行状态（t.plan / 子测试 / t.skip）-----------------------------

// testRunState 记录单个用例的运行状态（Node 语义）。
type testRunState struct {
	vm   *interpreter.VM
	name string
	full string
	fn   engine.Value

	mu            sync.Mutex
	plan          int  // 0 = 未设置；>0 = 期望断言数
	asserts       int  // t.assert 调用计数
	skipRequested bool // t.skip() 已调用
	todo          bool // t.todo() 已调用
	subtests      []*testRunState
	subResults    []TestResult              // 子测试执行结果（失败传播给父）
	cancelled     bool                      // 子测试被取消（父未 await——Node 语义）
	promise       *interpreter.PromiseValue // t.test 返回的 promise
}

// newTestRunState 构造用例运行状态。
func newTestRunState(vm *interpreter.VM, name, full string, fn engine.Value) *testRunState {
	return &testRunState{vm: vm, name: name, full: full, fn: fn}
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
		if len(args) < 2 || !deepEqual(args[0], args[1], true) {
			return engine.Undefined(), fmt.Errorf("%w: expected %s but got %s", engine.ErrAssertion, argString(args, 1), argString(args, 0))
		}
		return engine.Undefined(), nil
	}))
	_ = assertObj.Set("deepEqual", engine.NewFunction("deepEqual", func(args []engine.Value) (engine.Value, error) {
		st.addAssert()
		if len(args) < 2 || !deepEqual(args[0], args[1], false) {
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
	// t.assert.snapshot(value)：快照断言（Node 22 experimental 语义）。
	_ = assertObj.Set("snapshot", engine.NewFunction("snapshot", func(args []engine.Value) (engine.Value, error) {
		st.addAssert()
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("%w: snapshot: value required", engine.ErrTypeError)
		}
		return snapshotAssert(vm, args[0])
	}))
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
		vm.EnqueueMicrotask(func() {
			sub.mu.Lock()
			cancelled := sub.cancelled
			sub.mu.Unlock()
			if !cancelled {
				sr := runSubTestSync(vm, sub)
				st.mu.Lock()
				st.subResults = append(st.subResults, sr)
				st.mu.Unlock()
			}
			p.Fulfill(engine.Undefined())
		})
		return p, nil
	}))
	return t
}

// errTestSkipped 标记 t.skip() 的用例（内部错误，不展示给用户）。
var errTestSkipped = fmt.Errorf("test skipped via t.skip()")

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
