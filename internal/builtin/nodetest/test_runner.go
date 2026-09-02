// node:test 执行器：suite/用例调度、name/skip 过滤、子测试与 hook 顺序、AbortSignal。

package nodetest

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

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
