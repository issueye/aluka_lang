// 跨模块共享 helper（分包基座）。

package builtin

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
)

// mustValueToJSON 忽略循环引用错误的便捷包装（worker 消息序列化用）。
func mustValueToJSON(v engine.Value) interface{} {
	data, _ := valueToJSON(v, make(map[engine.Object]bool))
	return data
}

// valueToJSON 把 engine.Value 转成可 JSON 序列化的 Go 值。
// seen 记录当前递归路径上的对象以检测循环引用（命中返回错误，避免无限递归）。
func valueToJSON(v engine.Value, seen map[engine.Object]bool) (interface{}, error) {
	switch {
	case v.IsUndefined() || v.IsNull():
		return nil, nil
	case v.Type() == engine.TypeString:
		return v.String(), nil
	case v.Type() == engine.TypeBoolean:
		b, _ := v.Bool()
		return b, nil
	case v.Type() == engine.TypeNumber:
		f, _ := v.Float()
		return f, nil
	default:
		if o, ok := v.AsObject(); ok {
			if seen[o] {
				return nil, fmt.Errorf("cannot serialize circular structure")
			}
			seen[o] = true
			if a, ok := v.(*engine.ArrayValue); ok {
				out := make([]interface{}, 0, len(a.Elems()))
				for _, e := range a.Elems() {
					r, err := valueToJSON(e, seen)
					if err != nil {
						return nil, err
					}
					out = append(out, r)
				}
				delete(seen, o)
				return out, nil
			}
			obj := make(map[string]interface{})
			for _, k := range o.Keys() {
				if val, err := o.Get(k); err == nil {
					r, err := valueToJSON(val, seen)
					if err != nil {
						return nil, err
					}
					obj[k] = r
				}
			}
			delete(seen, o)
			return obj, nil
		}
	}
	return nil, nil
}

// jsonToEngine 把 JSON 解码值转回 engine.Value。
func jsonToEngine(v interface{}) engine.Value {
	switch val := v.(type) {
	case nil:
		return engine.Null()
	case bool:
		return engine.Boolean(val)
	case float64:
		return engine.Number(val)
	case string:
		return engine.Str(val)
	case []interface{}:
		elems := make([]engine.Value, len(val))
		for i, e := range val {
			elems[i] = jsonToEngine(e)
		}
		return engine.NewArray(elems)
	case map[string]interface{}:
		obj := engine.NewObject()
		for k, e := range val {
			_ = obj.Set(k, jsonToEngine(e))
		}
		return obj
	default:
		return engine.Undefined()
	}
}
