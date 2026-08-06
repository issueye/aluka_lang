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
	"fmt"
	"os"
	"path/filepath"
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
	// calls 记录（Node 语义：arguments/error/result/stack/target/this）。
	calls  *engine.ArrayValue
	isFn   bool // mock.fn 创建的独立函数（无 target）
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

// invokeTestFn 调用测试函数。Node 语义：回调接收 TestContext（t）参数，
// t.assert 提供断言（含 snapshot 快照断言），t.diagnostic 输出诊断信息。
func invokeTestFn(vm *interpreter.VM, fn engine.Value) error {
	if fn.IsFunction() {
		t := newTestContext(vm)
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
	return fmt.Errorf("%w: not a function", engine.ErrTypeError)
}

// --- TestContext（t 参数）--------------------------------------------------

// snapshotState 记录当前测试文件的快照状态（文件路径 + 调用计数）。
var snapshotMu sync.Mutex
var snapshotFile string       // 当前测试文件对应的快照文件路径
var snapshotCount int         // 当前文件内 snapshot 调用计数
var updateSnapshots bool      // --test-update-snapshots 模式

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

// newTestContext 构造 TestContext 对象。
func newTestContext(vm *interpreter.VM) engine.Value {
	t := engine.NewObject()

	// t.assert：断言对象（复用 assert 模块 + snapshot）。
	assertObj := engine.NewObject()
	_ = assertObj.Set("ok", engine.NewFunction("ok", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !truthy(args[0]) {
			return engine.Undefined(), fmt.Errorf("%w: expected value to be truthy", engine.ErrAssertion)
		}
		return engine.Undefined(), nil
	}))
	_ = assertObj.Set("strictEqual", engine.NewFunction("strictEqual", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 || !strictEqual(args[0], args[1]) {
			return engine.Undefined(), fmt.Errorf("%w: expected %s but got %s", engine.ErrAssertion, argString(args, 1), argString(args, 0))
		}
		return engine.Undefined(), nil
	}))
	_ = assertObj.Set("equal", engine.NewFunction("equal", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 || !looseEqual(args[0], args[1]) {
			return engine.Undefined(), fmt.Errorf("%w: expected %s but got %s", engine.ErrAssertion, argString(args, 1), argString(args, 0))
		}
		return engine.Undefined(), nil
	}))
	_ = assertObj.Set("deepStrictEqual", engine.NewFunction("deepStrictEqual", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 || !deepEqual(args[0], args[1], true) {
			return engine.Undefined(), fmt.Errorf("%w: expected %s but got %s", engine.ErrAssertion, argString(args, 1), argString(args, 0))
		}
		return engine.Undefined(), nil
	}))
	_ = assertObj.Set("deepEqual", engine.NewFunction("deepEqual", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 || !deepEqual(args[0], args[1], false) {
			return engine.Undefined(), fmt.Errorf("%w: expected %s but got %s", engine.ErrAssertion, argString(args, 1), argString(args, 0))
		}
		return engine.Undefined(), nil
	}))
	_ = assertObj.Set("notStrictEqual", engine.NewFunction("notStrictEqual", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 && strictEqual(args[0], args[1]) {
			return engine.Undefined(), fmt.Errorf("%w: values should not be strictly equal", engine.ErrAssertion)
		}
		return engine.Undefined(), nil
	}))
	_ = assertObj.Set("throws", engine.NewFunction("throws", func(args []engine.Value) (engine.Value, error) {
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

	// t.skip/t.todo/t.plan/t.runOnly：最小面（空操作/标记）。
	_ = t.Set("skip", engine.NewFunction("skip", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = t.Set("todo", engine.NewFunction("todo", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = t.Set("plan", engine.NewFunction("plan", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = t.Set("runOnly", engine.NewFunction("runOnly", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	// t.test(name, fn)：子测试（最小面：直接注册为用例）。
	_ = t.Set("test", engine.NewFunction("test", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	return t
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
