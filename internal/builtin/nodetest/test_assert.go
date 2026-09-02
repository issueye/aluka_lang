// node:test 断言实现：deepStrictEqual/deepEqual 结构比较、正则匹配与错误消息格式化。

package nodetest

import (
	"github.com/aluka-lang/aluka/internal/builtin/nodebase"
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

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
	if nodebase.StrictEqual(a, b) {
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
