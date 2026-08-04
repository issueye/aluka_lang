package builtin

// node:v8 内置模块（开发计划 3.15，subset）。
// serialize/deserialize（JSON 简化）、getHeapStatistics。

import (
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

// NewV8 构造 node:v8 模块导出对象。
func NewV8(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// serialize(value) → Buffer（用 JSON 序列化简化）。
	_ = m.Set("serialize", engine.NewFunction("serialize", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return globals.NewBufferInstance(nil), nil
		}
		data, err := json.Marshal(valueToJSON(args[0]))
		if err != nil {
			return engine.Undefined(), err
		}
		return globals.NewBufferInstance(data), nil
	}))

	// deserialize(buffer) → value。
	_ = m.Set("deserialize", engine.NewFunction("deserialize", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		data, ok := engine.AsBuffer(args[0])
		if !ok {
			data = []byte(args[0].String())
		}
		var v interface{}
		if err := json.Unmarshal(data, &v); err != nil {
			return engine.Undefined(), fmt.Errorf("v8.deserialize: %w", err)
		}
		return jsonToEngine(v), nil
	}))

	// getHeapStatistics() → 对象。
	_ = m.Set("getHeapStatistics", engine.NewFunction("getHeapStatistics", func(args []engine.Value) (engine.Value, error) {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		obj := engine.NewObject()
		_ = obj.Set("heap_size_limit", engine.Number(float64(ms.Sys)))
		_ = obj.Set("total_heap_size", engine.Number(float64(ms.HeapAlloc)))
		_ = obj.Set("used_heap_size", engine.Number(float64(ms.HeapInuse)))
		return obj, nil
	}))

	// getHeapSnapshot：简化 no-op（返回空 Buffer）。
	_ = m.Set("getHeapSnapshot", engine.NewFunction("getHeapSnapshot", func(args []engine.Value) (engine.Value, error) {
		return globals.NewBufferInstance([]byte("{}")), nil
	}))

	return m, nil
}

// valueToJSON 把 engine.Value 转成可 JSON 序列化的 Go 值。
func valueToJSON(v engine.Value) interface{} {
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
		if o, ok := v.AsObject(); ok {
			if a, ok := v.(*engine.ArrayValue); ok {
				out := make([]interface{}, 0, len(a.Elems()))
				for _, e := range a.Elems() {
					out = append(out, valueToJSON(e))
				}
				return out
			}
			obj := make(map[string]interface{})
			for _, k := range o.Keys() {
				if val, err := o.Get(k); err == nil {
					obj[k] = valueToJSON(val)
				}
			}
			return obj
		}
	}
	return nil
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
