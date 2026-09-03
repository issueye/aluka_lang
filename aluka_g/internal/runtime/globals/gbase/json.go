// JS 值与 Go interface{} 的 JSON 双向转换（IPC/GUI 消息共用）。

package gbase

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// 辅助序列化
func ValueToJSON(v engine.Value) interface{} {
	switch {
	case v.IsUndefined() || v.IsNull():
		return nil
	case v.Type() == engine.TypeString:
		return v.String()
	case v.Type() == engine.TypeBoolean:
		b, _ := v.Bool()
		return b
	case v.Type() == engine.TypeNumber:
		f, _ := v.Float()
		return f
	default:
		if a, ok := v.(*engine.ArrayValue); ok {
			out := make([]interface{}, 0, len(a.Elems()))
			for _, e := range a.Elems() {
				out = append(out, ValueToJSON(e))
			}
			return out
		}
		if o, ok := v.AsObject(); ok {
			obj := make(map[string]interface{})
			for _, k := range o.Keys() {
				if val, err := o.Get(k); err == nil {
					obj[k] = ValueToJSON(val)
				}
			}
			return obj
		}
	}
	return nil
}

// jsonToEngine 将 Go 解码的 JSON 接口转换为 engine.Value。
func JSONToEngine(v interface{}) engine.Value {
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
			elems[i] = JSONToEngine(e)
		}
		return engine.NewArray(elems)
	case map[string]interface{}:
		obj := engine.NewObject()
		for k, e := range val {
			_ = obj.Set(k, JSONToEngine(e))
		}
		return obj
	default:
		return engine.Undefined()
	}
}
