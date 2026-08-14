package builtin

// node:assert 内置模块——断言。
// 提供 strict（严格模式，=== 比较）与非 strict（== 比较）两套。

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// NewAssert 构造 node:assert 模块的导出对象。
func NewAssert(ctx engine.Context) (engine.Value, error) {
	// Node 的 assert 模块本身可调用：`assert(value)` 断言 truthy（否则抛
	// AssertionError），同时携带 ok/strictEqual 等方法。undici 等库直接调用
	// `assert(cond)`，若导出为纯对象会报 "is not a function"。
	m := engine.NewFunction("assert", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !truthy(args[0]) {
			msg := "assert: value is not truthy"
			if len(args) > 1 {
				msg = fmt.Sprintf("%s: %s", msg, args[1].String())
			}
			return engine.Undefined(), fmt.Errorf("%w: %s", engine.ErrAssertion, msg)
		}
		return engine.Undefined(), nil
	})

	mo := m.(engine.Object)
	// assert(value, message)：断言 truthy。
	_ = mo.Set("ok", engine.NewFunction("ok", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !truthy(args[0]) {
			msg := "assert.ok: value is not truthy"
			if len(args) > 1 {
				msg = fmt.Sprintf("%s: %s", msg, args[1].String())
			}
			return engine.Undefined(), fmt.Errorf("%w: %s", engine.ErrAssertion, msg)
		}
		return engine.Undefined(), nil
	}))

	// assert.strictEqual(actual, expected, message)
	_ = mo.Set("strictEqual", engine.NewFunction("strictEqual", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 || !strictEqual(args[0], args[1]) {
			msg := "strictEqual mismatch"
			if len(args) >= 2 {
				msg = fmt.Sprintf("expected %s but got %s", args[1].String(), args[0].String())
			}
			return engine.Undefined(), fmt.Errorf("%w: %s", engine.ErrAssertion, msg)
		}
		return engine.Undefined(), nil
	}))

	// assert.notStrictEqual(actual, expected, message)
	_ = mo.Set("notStrictEqual", engine.NewFunction("notStrictEqual", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 && strictEqual(args[0], args[1]) {
			return engine.Undefined(), fmt.Errorf("%w: values should not be strictly equal", engine.ErrAssertion)
		}
		return engine.Undefined(), nil
	}))

	// assert.equal(actual, expected, message)（宽松 == 比较）
	_ = mo.Set("equal", engine.NewFunction("equal", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 || !looseEqual(args[0], args[1]) {
			return engine.Undefined(), fmt.Errorf("%w: expected %s but got %s", engine.ErrAssertion, argString(args, 1), argString(args, 0))
		}
		return engine.Undefined(), nil
	}))

	// assert.notEqual(actual, expected, message)
	_ = mo.Set("notEqual", engine.NewFunction("notEqual", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 && looseEqual(args[0], args[1]) {
			return engine.Undefined(), fmt.Errorf("%w: values should not be loosely equal", engine.ErrAssertion)
		}
		return engine.Undefined(), nil
	}))

	// assert.deepEqual(actual, expected, message)（非严格，== 比较）
	_ = mo.Set("deepEqual", engine.NewFunction("deepEqual", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 || !deepEqual(args[0], args[1], false) {
			msg := "deepEqual mismatch"
			if len(args) >= 2 {
				msg = fmt.Sprintf("expected %s but got %s", args[1].String(), args[0].String())
			}
			return engine.Undefined(), fmt.Errorf("%w: %s", engine.ErrAssertion, msg)
		}
		return engine.Undefined(), nil
	}))

	// assert.deepStrictEqual(actual, expected, message)
	_ = mo.Set("deepStrictEqual", engine.NewFunction("deepStrictEqual", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 || !deepEqual(args[0], args[1], true) {
			msg := "deepStrictEqual mismatch"
			if len(args) >= 2 {
				msg = fmt.Sprintf("expected %s but got %s", args[1].String(), args[0].String())
			}
			return engine.Undefined(), fmt.Errorf("%w: %s", engine.ErrAssertion, msg)
		}
		return engine.Undefined(), nil
	}))

	// assert.throws(fn, errorMatcher, message)
	_ = mo.Set("throws", engine.NewFunction("throws", func(args []engine.Value) (engine.Value, error) {
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

	// assert.doesNotThrow(fn)
	_ = mo.Set("doesNotThrow", engine.NewFunction("doesNotThrow", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		f, ok := args[0].AsFunction()
		if !ok {
			return engine.Undefined(), nil
		}
		_, err := f.Call(nil)
		if err != nil {
			return engine.Undefined(), fmt.Errorf("%w: doesNotThrow: got unwanted exception: %v", engine.ErrAssertion, err)
		}
		return engine.Undefined(), nil
	}))

	// assert.ifError(value)：value 为 falsy 则通过，否则抛出。
	_ = mo.Set("ifError", engine.NewFunction("ifError", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 && truthy(args[0]) {
			return engine.Undefined(), fmt.Errorf("%w: ifError: %s", engine.ErrAssertion, args[0].String())
		}
		return engine.Undefined(), nil
	}))

	// assert.fail(message)
	_ = mo.Set("fail", engine.NewFunction("fail", func(args []engine.Value) (engine.Value, error) {
		msg := "fail"
		if len(args) > 0 {
			msg = args[0].String()
		}
		return engine.Undefined(), fmt.Errorf("%w: %s", engine.ErrAssertion, msg)
	}))

	// assert.match(string, regexp, message)：字符串匹配正则（Node ≥ 15 语义）。
	_ = mo.Set("match", engine.NewFunction("match", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("%w: match: string and regexp arguments required", engine.ErrAssertion)
		}
		if !regexpTest(ctx, args[1], args[0]) {
			msg := fmt.Sprintf("match: expected %s to match %s", args[0].String(), args[1].String())
			if len(args) > 2 {
				msg = fmt.Sprintf("%s: %s", msg, args[2].String())
			}
			return engine.Undefined(), fmt.Errorf("%w: %s", engine.ErrAssertion, msg)
		}
		return engine.Undefined(), nil
	}))

	// assert.doesNotMatch(string, regexp, message)
	_ = mo.Set("doesNotMatch", engine.NewFunction("doesNotMatch", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("%w: doesNotMatch: string and regexp arguments required", engine.ErrAssertion)
		}
		if regexpTest(ctx, args[1], args[0]) {
			msg := fmt.Sprintf("doesNotMatch: expected %s to not match %s", args[0].String(), args[1].String())
			if len(args) > 2 {
				msg = fmt.Sprintf("%s: %s", msg, args[2].String())
			}
			return engine.Undefined(), fmt.Errorf("%w: %s", engine.ErrAssertion, msg)
		}
		return engine.Undefined(), nil
	}))

	// assert.partialDeepStrictEqual(actual, expected, message)：actual 必须
	// 包含 expected 的所有属性（深严格比较），actual 可有多余属性。
	_ = mo.Set("partialDeepStrictEqual", engine.NewFunction("partialDeepStrictEqual", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 || !partialDeepStrictEqualImpl(args[0], args[1]) {
			msg := "partialDeepStrictEqual mismatch"
			if len(args) >= 2 {
				msg = fmt.Sprintf("expected %s but got %s", args[1].String(), args[0].String())
			}
			if len(args) > 2 {
				msg = fmt.Sprintf("%s: %s", msg, args[2].String())
			}
			return engine.Undefined(), fmt.Errorf("%w: %s", engine.ErrAssertion, msg)
		}
		return engine.Undefined(), nil
	}))

	// assert.rejects(promise|fn, [error], [message]) → Promise：断言目标 reject。
	// assert.doesNotReject(...)：断言目标 resolve（Node ≥ 10 语义）。
	_ = mo.Set("rejects", engine.NewFunction("rejects", makeAssertPromise(ctx, true)))
	_ = mo.Set("doesNotReject", engine.NewFunction("doesNotReject", makeAssertPromise(ctx, false)))

	// assert.strict 子对象（所有方法用严格模式）
	strict := engine.NewObject()
	if v, _ := mo.Get("strictEqual"); v != nil {
		_ = strict.Set("equal", v)
	}
	if v, _ := mo.Get("notStrictEqual"); v != nil {
		_ = strict.Set("notEqual", v)
	}
	if v, _ := mo.Get("deepStrictEqual"); v != nil {
		_ = strict.Set("deepEqual", v)
	}
	if v, _ := mo.Get("ok"); v != nil {
		_ = strict.Set("ok", v)
	}
	if v, _ := mo.Get("throws"); v != nil {
		_ = strict.Set("throws", v)
	}
	// 补全严格版方法（node:assert/strict 直接解构使用，Pi 用到
	// deepStrictEqual/strictEqual/rejects）。
	for _, name := range []string{"strictEqual", "notStrictEqual", "deepStrictEqual",
		"notEqual", "doesNotThrow", "ifError", "fail", "rejects", "doesNotReject",
		"match", "doesNotMatch", "partialDeepStrictEqual"} {
		if v, _ := mo.Get(name); v != nil {
			_ = strict.Set(name, v)
		}
	}
	_ = mo.Set("strict", strict)

	return m, nil
}

// NewAssertStrict 构造 node:assert/strict 模块导出——与 assert.strict 相同
// （Node 语义：node:assert/strict ≡ assert.strict，比较均为严格模式）。
func NewAssertStrict(ctx engine.Context) (engine.Value, error) {
	m, err := NewAssert(ctx)
	if err != nil {
		return nil, err
	}
	if mo, ok := m.AsObject(); ok {
		if v, err := mo.Get("strict"); err == nil && v != nil {
			return v, nil
		}
	}
	return m, nil
}

// makeAssertPromise 构造 rejects/doesNotReject 的 Promise 断言
// （Node ≥ 10 语义）。expectReject=true 时断言目标 reject（rejects），
// false 时断言目标 resolve（doesNotReject）。
//
// 目标为函数时先调用（同步 throw 视为 reject）；随后经 then 链监听
// fulfill/reject。期望错误 error（可选）按 Node 语义匹配：构造函数
// （instanceof）/正则（test message）/对象（属性相等）/其他（严格相等）。
func makeAssertPromise(ctx engine.Context, expectReject bool) engine.Func {
	return func(args []engine.Value) (engine.Value, error) {
		vm, ok := ctx.(*interpreter.VM)
		if !ok {
			return engine.Undefined(), fmt.Errorf("assert.rejects: requires the VM engine")
		}
		p := interpreter.NewPromiseValue(vm.Interp())
		if len(args) == 0 {
			p.Reject(makeErrorValue(ctx, fmt.Errorf("assert.rejects: missing promise argument")))
			return p, nil
		}

		var expected engine.Value = engine.Undefined()
		if len(args) > 1 {
			expected = args[1]
		}

		// 目标为函数：先调用（同步 throw 按 reject 处理）。
		var target engine.Value
		if f, ok := args[0].AsFunction(); ok {
			v, err := f.Call(nil)
			if err != nil {
				settleAssertPromise(p, makeErrorValue(ctx, err), expected, expectReject, ctx)
				return p, nil
			}
			target = v
		} else {
			target = args[0]
		}

		onFulfilled := engine.NewFunction("__assertFulfilled", func(ca []engine.Value) (engine.Value, error) {
			if expectReject {
				p.Reject(makeErrorValue(ctx, fmt.Errorf("%w: expected promise to reject, but it fulfilled", engine.ErrAssertion)))
			} else {
				p.Fulfill(engine.Undefined())
			}
			return engine.Undefined(), nil
		})
		onRejected := engine.NewFunction("__assertRejected", func(ca []engine.Value) (engine.Value, error) {
			reason := engine.Undefined()
			if len(ca) > 0 {
				reason = ca[0]
			}
			settleAssertPromise(p, reason, expected, expectReject, ctx)
			return engine.Undefined(), nil
		})
		// 真 Promise：经 Go 层 Then 挂接（engine.Function.Call 无 this
		// 绑定，不能经 JS 的 target.then() 调用）。
		if pv, ok := target.(*interpreter.PromiseValue); ok {
			pv.Then(onFulfilled, onRejected)
		} else if o, ok := target.AsObject(); ok {
			if thenFn, err := o.Get("then"); err == nil && thenFn.IsFunction() {
				if tf, ok := thenFn.AsFunction(); ok {
					if _, err := tf.Call([]engine.Value{onFulfilled, onRejected}); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				}
			}
		}
		return p, nil
	}
}

// settleAssertPromise 按断言结果定值 promise。
func settleAssertPromise(p *interpreter.PromiseValue, reason, expected engine.Value, expectReject bool, ctx engine.Context) {
	if expectReject {
		if assertErrorMatches(ctx, reason, expected) {
			p.Fulfill(engine.Undefined())
		} else {
			p.Reject(makeErrorValue(ctx, fmt.Errorf("%w: expected promise to reject with a matching error, got %v", engine.ErrAssertion, reason)))
		}
	} else {
		p.Reject(reason)
	}
}

// assertErrorMatches 检查实际 reject 值是否匹配期望错误（Node assert 语义）：
// 未提供期望 → 匹配；构造函数 → instanceof（原型链）；正则 → test(message)；
// 对象 → 属性相等；其他 → 严格相等。
func assertErrorMatches(ctx engine.Context, actual, expected engine.Value) bool {
	if expected.IsUndefined() || expected.IsNull() {
		return true
	}
	// 构造函数 → instanceof（沿原型链查找）。
	if expected.IsFunction() {
		if eo, ok := expected.AsObject(); ok {
			if pv, err := eo.Get("prototype"); err == nil {
				target, ok := pv.AsObject()
				if !ok {
					return false
				}
				for cur := engine.GetProto(actual); cur != nil; cur = engine.GetProto(cur) {
					if cur == target {
						return true
					}
				}
			}
		}
		return false
	}
	if eo, ok := expected.AsObject(); ok {
		// 正则 → test(actual 的 message 或字符串形式)。
		// RegExp.prototype.test 是 this 感知的原生方法，Go 侧
		// engine.Function.Call 无 this 绑定——经 Eval 桥接执行
		// __re.test(__msg)（JS 成员调用语义）。
		if testFn, err := eo.Get("test"); err == nil && testFn.IsFunction() {
			msg := engine.Undefined()
			if ao, ok := actual.AsObject(); ok {
				if m, err := ao.Get("message"); err == nil {
					msg = m
				}
			}
			if msg.IsUndefined() {
				msg = actual
			}
			return regexpTest(ctx, expected, msg)
		}
		// 普通对象 → 属性相等（期望对象的每个属性都匹配）。
		if ao, ok := actual.AsObject(); ok {
			for _, k := range eo.Keys() {
				ev, _ := eo.Get(k)
				av, _ := ao.Get(k)
				if !strictEqual(av, ev) {
					return false
				}
			}
			return true
		}
	}
	// 其他 → 严格相等。
	return strictEqual(actual, expected)
}

// regexpTest 经 Eval 桥接执行 re.test(target)（Go 侧 engine.Function.Call
// 无 this 绑定，不能直接调用 RegExp.prototype.test）。
func regexpTest(ctx engine.Context, re, target engine.Value) bool {
	if reo, ok := re.AsObject(); ok {
		if testFn, err := reo.Get("test"); err == nil && testFn.IsFunction() {
			_ = ctx.Global().Set("__assertRe", re)
			_ = ctx.Global().Set("__assertMsg", target)
			defer ctx.Global().Delete("__assertRe")
			defer ctx.Global().Delete("__assertMsg")
			if v, err := ctx.Eval("__assertRe.test(__assertMsg)", "assert_regexp_test.js"); err == nil {
				if b, ok := v.Bool(); ok {
					return b
				}
			}
			return false
		}
	}
	return false
}

// partialDeepStrictEqualImpl：actual 必须包含 expected 的所有自有属性
// （递归深严格比较），actual 可有多余属性。数组按索引属性比较，跳过
// 不可枚举的 length（Node 语义：length 不参与比较）。
func partialDeepStrictEqualImpl(actual, expected engine.Value) bool {
	eo, ok := expected.AsObject()
	if !ok {
		return strictEqual(actual, expected)
	}
	ao, ok := actual.AsObject()
	if !ok {
		return false
	}
	for _, k := range eo.Keys() {
		if k == "length" {
			continue
		}
		ev, err1 := eo.Get(k)
		av, err2 := ao.Get(k)
		if err1 != nil || err2 != nil || !partialDeepStrictEqualImpl(av, ev) {
			return false
		}
	}
	return true
}

// makeErrorValue 构造带 message 的 Error 对象（Error("msg") 与 new Error 等价）。
func makeErrorValue(ctx engine.Context, err error) engine.Value {
	if errCtor, ge := ctx.Global().Get("Error"); ge == nil && errCtor.IsFunction() {
		if ef, ok := errCtor.AsFunction(); ok {
			if ev, ce := ef.Call([]engine.Value{engine.Str(err.Error())}); ce == nil {
				return ev
			}
		}
	}
	return engine.Str(err.Error())
}

// truthy 判断值是否为 JS truthy。
func truthy(v engine.Value) bool {
	switch v.Type() {
	case engine.TypeUndefined, engine.TypeNull:
		return false
	case engine.TypeBoolean:
		b, _ := v.Bool()
		return b
	case engine.TypeNumber:
		f, _ := v.Float()
		return f != 0 && f == f // 非 0 非 NaN
	case engine.TypeString:
		return v.String() != ""
	default:
		return true
	}
}

// strictEqual 严格相等（===）。
func strictEqual(a, b engine.Value) bool {
	if a.Type() != b.Type() {
		return false
	}
	switch a.Type() {
	case engine.TypeNumber:
		af, _ := a.Float()
		bf, _ := b.Float()
		return af == bf
	case engine.TypeString:
		return a.String() == b.String()
	case engine.TypeBoolean:
		ab, _ := a.Bool()
		bb, _ := b.Bool()
		return ab == bb
	case engine.TypeBigInt:
		return a.String() == b.String()
	case engine.TypeUndefined, engine.TypeNull:
		return true
	default:
		return a == b // 对象引用相等
	}
}

// looseEqual 实现 JS == 宽松比较（简化：null==undefined、number/string 互转、
// bool→number）。
func looseEqual(a, b engine.Value) bool {
	if a.Type() == b.Type() {
		return strictEqual(a, b)
	}
	if (a.IsNull() && b.IsUndefined()) || (a.IsUndefined() && b.IsNull()) {
		return true
	}
	if a.Type() == engine.TypeNumber && b.Type() == engine.TypeString {
		if rf, ok := b.Float(); ok {
			af, _ := a.Float()
			return af == rf
		}
	}
	if a.Type() == engine.TypeString && b.Type() == engine.TypeNumber {
		if lf, ok := a.Float(); ok {
			bf, _ := b.Float()
			return lf == bf
		}
	}
	if a.Type() == engine.TypeBoolean {
		bv := 0
		if ab, _ := a.Bool(); ab {
			bv = 1
		}
		return looseEqual(engine.IntValue(bv), b)
	}
	if b.Type() == engine.TypeBoolean {
		bv := 0
		if bb, _ := b.Bool(); bb {
			bv = 1
		}
		return looseEqual(a, engine.IntValue(bv))
	}
	return false
}

// argString 安全取第 i 个参数的字符串表示。
func argString(args []engine.Value, i int) string {
	if i < len(args) {
		return args[i].String()
	}
	return "undefined"
}

// deepEqual 深度相等。strict 为 true 时用严格比较。
func deepEqual(a, b engine.Value, strict bool) bool {
	if strict {
		return strictEqual(a, b) || deepStrictEqualImpl(a, b)
	}
	return deepEqualImpl(a, b)
}

func deepStrictEqualImpl(a, b engine.Value) bool {
	return strictEqual(a, b) || (a.String() == b.String() && a.Type() == b.Type())
}

func deepEqualImpl(a, b engine.Value) bool {
	// 简化：类型相同且 String() 相等。
	if a.Type() != b.Type() {
		// == 宽松：数字与字符串可比。
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
	return a.String() == b.String()
}
