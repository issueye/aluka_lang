// 跨模块共享：值比较（严格/宽松/深比较）与断言 helper。

package builtin

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

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
