package builtin

// node:assert 内置模块——断言。
// 提供 strict（严格模式，=== 比较）与非 strict（== 比较）两套。

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewAssert 构造 node:assert 模块的导出对象。
func NewAssert(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// assert(value, message)：断言 truthy。
	_ = m.Set("ok", engine.NewFunction("ok", func(args []engine.Value) (engine.Value, error) {
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
	_ = m.Set("strictEqual", engine.NewFunction("strictEqual", func(args []engine.Value) (engine.Value, error) {
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
	_ = m.Set("notStrictEqual", engine.NewFunction("notStrictEqual", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 && strictEqual(args[0], args[1]) {
			return engine.Undefined(), fmt.Errorf("%w: values should not be strictly equal", engine.ErrAssertion)
		}
		return engine.Undefined(), nil
	}))

	// assert.equal(actual, expected, message)（宽松 == 比较）
	_ = m.Set("equal", engine.NewFunction("equal", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 || !looseEqual(args[0], args[1]) {
			return engine.Undefined(), fmt.Errorf("%w: expected %s but got %s", engine.ErrAssertion, argString(args, 1), argString(args, 0))
		}
		return engine.Undefined(), nil
	}))

	// assert.notEqual(actual, expected, message)
	_ = m.Set("notEqual", engine.NewFunction("notEqual", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 && looseEqual(args[0], args[1]) {
			return engine.Undefined(), fmt.Errorf("%w: values should not be loosely equal", engine.ErrAssertion)
		}
		return engine.Undefined(), nil
	}))

	// assert.deepEqual(actual, expected, message)（非严格，== 比较）
	_ = m.Set("deepEqual", engine.NewFunction("deepEqual", func(args []engine.Value) (engine.Value, error) {
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
	_ = m.Set("deepStrictEqual", engine.NewFunction("deepStrictEqual", func(args []engine.Value) (engine.Value, error) {
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
	_ = m.Set("throws", engine.NewFunction("throws", func(args []engine.Value) (engine.Value, error) {
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
	_ = m.Set("doesNotThrow", engine.NewFunction("doesNotThrow", func(args []engine.Value) (engine.Value, error) {
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
	_ = m.Set("ifError", engine.NewFunction("ifError", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 && truthy(args[0]) {
			return engine.Undefined(), fmt.Errorf("%w: ifError: %s", engine.ErrAssertion, args[0].String())
		}
		return engine.Undefined(), nil
	}))

	// assert.fail(message)
	_ = m.Set("fail", engine.NewFunction("fail", func(args []engine.Value) (engine.Value, error) {
		msg := "fail"
		if len(args) > 0 {
			msg = args[0].String()
		}
		return engine.Undefined(), fmt.Errorf("%w: %s", engine.ErrAssertion, msg)
	}))

	// assert.strict 子对象（所有方法用严格模式）
	strict := engine.NewObject()
	if v, _ := m.Get("strictEqual"); v != nil {
		_ = strict.Set("equal", v)
	}
	if v, _ := m.Get("notStrictEqual"); v != nil {
		_ = strict.Set("notEqual", v)
	}
	if v, _ := m.Get("deepStrictEqual"); v != nil {
		_ = strict.Set("deepEqual", v)
	}
	if v, _ := m.Get("ok"); v != nil {
		_ = strict.Set("ok", v)
	}
	if v, _ := m.Get("throws"); v != nil {
		_ = strict.Set("throws", v)
	}
	_ = m.Set("strict", strict)

	return m, nil
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
