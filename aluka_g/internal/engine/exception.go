package engine

// maxExceptionMessage 是 Go error.Error() 侧 JS 异常文本上限。
// 模块加载失败时 loader 会 fmt.Errorf("%w", jsThrow)；若 Error() 对任意
// 对象做全量 inspect，属性图可膨胀到 GB 级并在 Windows 上 VirtualAlloc 失败。
const maxExceptionMessage = 16 * 1024

// FormatException 把 JS 异常值格式化为短消息，供 Go error.Error() 使用。
//
// 对 Error 类对象只取 name / message / stack（字符串），不展开其它自有属性
// （cause、附加的巨型 payload 等）。非 Error 对象返回 "[object Object]"，
// 避免 throw 模块命名空间或 schema 时把整图序列化进错误字符串。
func FormatException(v Value) string {
	if v == nil {
		return "undefined"
	}
	switch v.Type() {
	case TypeUndefined:
		return "undefined"
	case TypeNull:
		return "null"
	case TypeBoolean, TypeNumber, TypeBigInt, TypeSymbol, TypeFunction:
		return truncateException(v.String())
	case TypeString:
		return truncateException(v.String())
	}

	o, ok := v.AsObject()
	if !ok {
		return truncateException(v.String())
	}

	if stack := ownPrimitiveString(o, "stack"); stack != "" {
		return truncateException(stack)
	}
	name := ownPrimitiveString(o, "name")
	if name == "" {
		name = "Error"
	}
	msg := ownPrimitiveString(o, "message")
	if msg == "" {
		if name == "Error" {
			return "[object Object]"
		}
		return truncateException(name)
	}
	return truncateException(name + ": " + msg)
}

func ownPrimitiveString(o Object, key string) string {
	v, err := o.Get(key)
	if err != nil || v == nil || v.IsUndefined() || v.IsNull() {
		return ""
	}
	switch v.Type() {
	case TypeString, TypeNumber, TypeBoolean, TypeBigInt:
		return v.String()
	default:
		return ""
	}
}

func truncateException(s string) string {
	if len(s) <= maxExceptionMessage {
		return s
	}
	const suffix = "…(truncated)"
	if maxExceptionMessage <= len(suffix) {
		return s[:maxExceptionMessage]
	}
	return s[:maxExceptionMessage-len(suffix)] + suffix
}
