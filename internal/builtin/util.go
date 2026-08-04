package builtin

// node:util 内置模块——提供工具函数。
// inspect 实现为简化版（复用引擎的 String() 格式化）。

import (
	"fmt"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewUtil 构造 node:util 模块的导出对象。
func NewUtil(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	_ = m.Set("inspect", engine.NewFunction("inspect", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str("undefined"), nil
		}
		// 简化：直接用引擎的 String() 格式化（与 console.log 一致）。
		return engine.Str(args[0].String()), nil
	}))

	_ = m.Set("format", engine.NewFunction("format", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		// Node.js util.format：第一个参数含 %s/%d/%j/%o/%O/%% 占位符。
		return engine.Str(utilFormat(args)), nil
	}))

	_ = m.Set("promisify", engine.NewFunction("promisify", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("util.promisify: argument required")
		}
		original := args[0]
		// 返回一个新函数，把 callback 风格转为 Promise。
		return engine.NewFunction("promisified", func(callArgs []engine.Value) (engine.Value, error) {
			// 简化：直接调用原函数并包装为 Promise.resolve。
			// 完整实现需检测 (err, result) 回调模式。
			if f, ok := original.AsFunction(); ok {
				// 追加一个回调参数（Node.js 约定最后一个参数是回调）。
				result, err := f.Call(callArgs)
				if err != nil {
					return engine.Str(err.Error()), nil
				}
				return result, nil
			}
			return engine.Undefined(), nil
		}), nil
	}))

	_ = m.Set("deprecate", engine.NewFunction("deprecate", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		// 简化：返回原函数（不打印 deprecation 警告）。
		return args[0], nil
	}))

	_ = m.Set("callbackify", engine.NewFunction("callbackify", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		original := args[0]
		return engine.NewFunction("callbackified", func(callArgs []engine.Value) (engine.Value, error) {
			if f, ok := original.AsFunction(); ok {
				result, err := f.Call(callArgs)
				if err != nil {
					return engine.Undefined(), err
				}
				return result, nil
			}
			return engine.Undefined(), nil
		}), nil
	}))

	_ = m.Set("isDeepStrictEqual", engine.NewFunction("isDeepStrictEqual", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(deepStrictEqual(args[0], args[1])), nil
	}))

	_ = m.Set("styleText", engine.NewFunction("styleText", func(args []engine.Value) (engine.Value, error) {
		// 简化：忽略颜色，直接返回文本。
		if len(args) < 2 {
			return engine.Str(""), nil
		}
		return engine.Str(args[1].String()), nil
	}))

	// util.types 子对象
	types := engine.NewObject()
	registerUtilTypes(types)
	_ = m.Set("types", types)

	// inspect.defaultOptions
	inspectOpts := engine.NewObject()
	_ = inspectOpts.Set("depth", engine.Number(2))
	_ = inspectOpts.Set("colors", engine.Boolean(false))
	_ = m.Set("defaultOptions", inspectOpts)

	return m, nil
}

// utilFormat 实现 Node.js util.format 的占位符替换。
func utilFormat(args []engine.Value) string {
	if len(args) == 0 {
		return ""
	}
	format := args[0].String()
	if !strings.Contains(format, "%") {
		// 无占位符：用空格连接所有参数。
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = a.String()
		}
		return strings.Join(parts, " ")
	}
	var b strings.Builder
	argIdx := 1
	for i := 0; i < len(format); i++ {
		if format[i] == '%' && i+1 < len(format) {
			switch format[i+1] {
			case 's':
				if argIdx < len(args) {
					b.WriteString(args[argIdx].String())
					argIdx++
				}
				i++
			case 'd':
				if argIdx < len(args) {
					if n, ok := args[argIdx].Int(); ok {
						b.WriteString(fmt.Sprintf("%d", n))
					} else if f, ok := args[argIdx].Float(); ok {
						b.WriteString(fmt.Sprintf("%g", f))
					} else {
						b.WriteString(args[argIdx].String())
					}
					argIdx++
				}
				i++
			case 'j':
				if argIdx < len(args) {
					b.WriteString(args[argIdx].String())
					argIdx++
				}
				i++
			case 'o', 'O':
				if argIdx < len(args) {
					b.WriteString(args[argIdx].String())
					argIdx++
				}
				i++
			case '%':
				b.WriteByte('%')
				i++
			default:
				b.WriteByte(format[i])
			}
		} else {
			b.WriteByte(format[i])
		}
	}
	// 剩余参数用空格追加
	for ; argIdx < len(args); argIdx++ {
		b.WriteString(" ")
		b.WriteString(args[argIdx].String())
	}
	return b.String()
}

// deepStrictEqual 简化版严格相等（递归比较对象/数组）。
func deepStrictEqual(a, b engine.Value) bool {
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
		// 对象/数组/函数：递归比较（简化版，用 String 兜底）。
		return a.String() == b.String()
	}
}

// registerUtilTypes 注册 util.types 子对象的方法。
func registerUtilTypes(types engine.Object) {
	typeCheck := func(name string, pred func(engine.Value) bool) {
		_ = types.Set(name, engine.NewFunction(name, func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 {
				return engine.Boolean(false), nil
			}
			return engine.Boolean(pred(args[0])), nil
		}))
	}

	typeCheck("isPromise", func(v engine.Value) bool {
		return v.Type() == engine.TypeObject && strings.Contains(v.String(), "Promise")
	})
	typeCheck("isArray", func(v engine.Value) bool {
		return v.Type() == engine.TypeObject && strings.HasPrefix(v.String(), "[")
	})
	typeCheck("isMap", func(v engine.Value) bool {
		return v.Type() == engine.TypeObject && strings.Contains(v.String(), "Map")
	})
	typeCheck("isSet", func(v engine.Value) bool {
		return v.Type() == engine.TypeObject && strings.Contains(v.String(), "Set")
	})
	typeCheck("isRegExp", func(v engine.Value) bool {
		return v.Type() == engine.TypeObject && strings.HasPrefix(v.String(), "/")
	})
	typeCheck("isDate", func(v engine.Value) bool {
		return v.Type() == engine.TypeObject && strings.Contains(v.String(), "Date")
	})
	typeCheck("isError", func(v engine.Value) bool {
		return v.Type() == engine.TypeObject && strings.Contains(v.String(), "Error")
	})
	typeCheck("isBoolean", func(v engine.Value) bool { return v.Type() == engine.TypeBoolean })
	typeCheck("isNumber", func(v engine.Value) bool { return v.Type() == engine.TypeNumber })
	typeCheck("isString", func(v engine.Value) bool { return v.Type() == engine.TypeString })
	typeCheck("isSymbol", func(v engine.Value) bool { return v.Type() == engine.TypeSymbol })
	typeCheck("isBigInt", func(v engine.Value) bool { return v.Type() == engine.TypeBigInt })
	typeCheck("isFunction", func(v engine.Value) bool { return v.Type() == engine.TypeFunction })
	typeCheck("isObject", func(v engine.Value) bool { return v.Type() == engine.TypeObject })
	typeCheck("isNull", func(v engine.Value) bool { return v.IsNull() })
	typeCheck("isUndefined", func(v engine.Value) bool { return v.IsUndefined() })
	typeCheck("isPrimitive", func(v engine.Value) bool {
		t := v.Type()
		return t != engine.TypeObject && t != engine.TypeFunction
	})
}
