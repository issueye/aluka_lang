// node:test 注册表：describe/it 收集的 suite 树、模块导出面与自定义断言注册。

package builtin

import (
	"fmt"
	"sync"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

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
