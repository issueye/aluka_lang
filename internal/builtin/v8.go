package builtin

// node:v8 内置模块（开发计划 3.15）。
// serialize/deserialize（JSON 简化，经引擎 JSON.stringify/parse 保序）、
// getHeapStatistics/getHeapSpaceStatistics/getHeapCodeStatistics、
// Serializer/Deserializer、getHeapSnapshot 与其余诊断方法面。

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

// callJSONMethod 调用全局 JSON 上的方法（stringify/parse，保序）。
func callJSONMethod(ctx engine.Context, name string, args []engine.Value) (engine.Value, error) {
	jsonVal, err := ctx.Global().Get("JSON")
	if err != nil || !jsonVal.IsObject() {
		return engine.Undefined(), fmt.Errorf("v8: global JSON unavailable")
	}
	jo, _ := jsonVal.AsObject()
	fn, err := jo.Get(name)
	if err != nil || !fn.IsFunction() {
		return engine.Undefined(), fmt.Errorf("v8: JSON.%s unavailable", name)
	}
	f, _ := fn.AsFunction()
	return f.Call(args)
}

// NewV8 构造 node:v8 模块导出对象。
func NewV8(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// serialize(value) → Buffer（用引擎 JSON.stringify 简化，保序）。
	_ = m.Set("serialize", engine.NewFunction("serialize", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return globals.NewBufferInstance(nil), nil
		}
		s, err := callJSONMethod(ctx, "stringify", args[:1])
		if err != nil {
			return engine.Undefined(), err
		}
		if s.IsUndefined() {
			return globals.NewBufferInstance(nil), nil
		}
		return globals.NewBufferInstance([]byte(s.String())), nil
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
		return callJSONMethod(ctx, "parse", []engine.Value{engine.Str(string(data))})
	}))

	// cachedDataVersionTag() → number。
	_ = m.Set("cachedDataVersionTag", engine.NewFunction("cachedDataVersionTag", func(args []engine.Value) (engine.Value, error) {
		return engine.IntValue(0), nil
	}))

	// getHeapStatistics() → 对象（对齐 Node 22 的 14 个键）。
	_ = m.Set("getHeapStatistics", engine.NewFunction("getHeapStatistics", func(args []engine.Value) (engine.Value, error) {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		obj := engine.NewObject()
		_ = obj.Set("does_zap_garbage", engine.IntValue(0))
		_ = obj.Set("external_memory", engine.IntValue(0))
		_ = obj.Set("heap_size_limit", engine.Number(float64(ms.Sys)))
		_ = obj.Set("malloced_memory", engine.Number(float64(ms.Sys)))
		_ = obj.Set("number_of_detached_contexts", engine.IntValue(0))
		_ = obj.Set("number_of_native_contexts", engine.IntValue(1))
		_ = obj.Set("peak_malloced_memory", engine.Number(float64(ms.Sys)))
		_ = obj.Set("total_available_size", engine.Number(float64(ms.Sys)))
		_ = obj.Set("total_global_handles_size", engine.IntValue(0))
		_ = obj.Set("total_heap_size", engine.Number(float64(ms.HeapAlloc)))
		_ = obj.Set("total_heap_size_executable", engine.IntValue(0))
		_ = obj.Set("total_physical_size", engine.Number(float64(ms.HeapInuse)))
		_ = obj.Set("used_global_handles_size", engine.IntValue(0))
		_ = obj.Set("used_heap_size", engine.Number(float64(ms.HeapInuse)))
		return obj, nil
	}))

	// getHeapSpaceStatistics() → 空间数组（对齐 Node 22 的 11 个空间）。
	_ = m.Set("getHeapSpaceStatistics", engine.NewFunction("getHeapSpaceStatistics", func(args []engine.Value) (engine.Value, error) {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		spaceNames := []string{
			"read_only_space", "new_space", "old_space", "code_space", "map_space",
			"large_object_space", "code_large_object_space", "new_large_object_space",
			"shared_large_object_space", "shared_space", "trusted_space",
		}
		spaces := make([]engine.Value, 0, len(spaceNames))
		for _, n := range spaceNames {
			sp := engine.NewObject()
			_ = sp.Set("space_name", engine.Str(n))
			_ = sp.Set("space_size", engine.Number(float64(ms.HeapAlloc)))
			_ = sp.Set("space_used_size", engine.Number(float64(ms.HeapInuse)))
			_ = sp.Set("space_available_size", engine.Number(float64(ms.HeapAlloc)))
			_ = sp.Set("physical_space_size", engine.Number(float64(ms.HeapInuse)))
			spaces = append(spaces, sp)
		}
		return engine.NewArray(spaces), nil
	}))

	// getHeapCodeStatistics() → 对象。
	_ = m.Set("getHeapCodeStatistics", engine.NewFunction("getHeapCodeStatistics", func(args []engine.Value) (engine.Value, error) {
		obj := engine.NewObject()
		_ = obj.Set("code_and_metadata_size", engine.IntValue(0))
		_ = obj.Set("bytecode_and_metadata_size", engine.IntValue(0))
		_ = obj.Set("external_script_source_size", engine.IntValue(0))
		_ = obj.Set("cpu_profiler_metadata_size", engine.IntValue(0))
		return obj, nil
	}))

	// getCppHeapStatistics() → 对象。
	_ = m.Set("getCppHeapStatistics", engine.NewFunction("getCppHeapStatistics", func(args []engine.Value) (engine.Value, error) {
		obj := engine.NewObject()
		_ = obj.Set("cage_memory_size", engine.IntValue(0))
		_ = obj.Set("cage_committed_size", engine.IntValue(0))
		_ = obj.Set("heap_memory_size", engine.IntValue(0))
		_ = obj.Set("heap_committed_size", engine.IntValue(0))
		return obj, nil
	}))

	// getHeapSnapshot() → HeapSnapshotStream（Readable 流形状，最小面）。
	_ = m.Set("getHeapSnapshot", engine.NewFunction("getHeapSnapshot", func(args []engine.Value) (engine.Value, error) {
		snap := engine.NewObject()
		for _, n := range []string{"on", "pipe", "read", "pause", "resume", "destroy", "_read", "_destroy"} {
			_ = snap.Set(n, engine.NewFunction(n, func(args []engine.Value) (engine.Value, error) {
				return engine.Undefined(), nil
			}))
		}
		return snap, nil
	}))

	// writeHeapSnapshot([filename]) → 写入并返回快照文件路径
	_ = m.Set("writeHeapSnapshot", engine.NewFunction("writeHeapSnapshot", func(args []engine.Value) (engine.Value, error) {
		filename := fmt.Sprintf("Heap-%d.heapsnapshot", time.Now().UnixNano())
		if len(args) > 0 && args[0].Type() == engine.TypeString && args[0].String() != "" {
			filename = args[0].String()
		}
		data := generateHeapSnapshotJSON(ctx)
		if err := os.WriteFile(filename, []byte(data), 0644); err != nil {
			return engine.Undefined(), fmt.Errorf("writeHeapSnapshot: %w", err)
		}
		return engine.Str(filename), nil
	}))

	// getHeapSnapshot() → 返回 Readable 流（包含完整 JSON 字符串）
	_ = m.Set("getHeapSnapshot", engine.NewFunction("getHeapSnapshot", func(args []engine.Value) (engine.Value, error) {
		streamMod, err := NewStream(ctx)
		if err != nil {
			return engine.Undefined(), err
		}
		data := generateHeapSnapshotJSON(ctx)
		if so, ok := streamMod.AsObject(); ok {
			if rFn, err := so.Get("Readable"); err == nil && rFn.IsFunction() {
				if rf, ok := rFn.AsFunction(); ok {
					rs, _ := rf.Call(nil)
					if rso, ok := rs.AsObject(); ok {
						if pushFn, err := rso.Get("push"); err == nil && pushFn.IsFunction() {
							if pf, ok := pushFn.AsFunction(); ok {
								_, _ = pf.Call([]engine.Value{engine.Str(data)})
								_, _ = pf.Call([]engine.Value{engine.Null()}) // 结束流
							}
						}
					}
					return rs, nil
				}
			}
		}
		return engine.Undefined(), nil
	}))
	_ = m.Set("isStringOneByteRepresentation", engine.NewFunction("isStringOneByteRepresentation", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(true), nil
	}))
	_ = m.Set("queryObjects", engine.NewFunction("queryObjects", func(args []engine.Value) (engine.Value, error) {
		return engine.NewArray(nil), nil
	}))
	_ = m.Set("takeCoverage", engine.NewFunction("takeCoverage", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = m.Set("stopCoverage", engine.NewFunction("stopCoverage", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))

	// promiseHooks / startupSnapshot。
	promiseHooks := engine.NewObject()
	_ = promiseHooks.Set("createHook", engine.NewFunction("createHook", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	for _, n := range []string{"onInit", "onBefore", "onAfter", "onSettled"} {
		_ = promiseHooks.Set(n, engine.NewFunction(n, func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
	}
	_ = m.Set("promiseHooks", promiseHooks)
	startupSnapshot := engine.NewObject()
	for _, n := range []string{"addSerializeCallback", "addDeserializeCallback", "setDeserializeMainFunction", "isBuildingSnapshot"} {
		_ = startupSnapshot.Set(n, engine.NewFunction(n, func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
	}
	_ = m.Set("startupSnapshot", startupSnapshot)

	// Serializer / Deserializer / DefaultSerializer / DefaultDeserializer。
	serProto := engine.NewObject()
	for _, n := range []string{"writeHeader", "writeValue", "transferArrayBuffer", "writeUint32", "writeUint64", "writeDouble", "writeRawBytes", "_setTreatArrayBufferViewsAsHostObjects"} {
		_ = serProto.Set(n, engine.NewFunction(n, func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
	}
	_ = serProto.Set("releaseBuffer", engine.NewFunction("releaseBuffer", func(args []engine.Value) (engine.Value, error) {
		return globals.NewBufferInstance(nil), nil
	}))
	serCtor := engine.NewFunction("Serializer", func(args []engine.Value) (engine.Value, error) {
		return newV8ValueInstance(serProto, "Serializer"), nil
	})
	if co, ok := serCtor.AsObject(); ok {
		_ = co.Set("prototype", serProto)
	}
	_ = m.Set("Serializer", serCtor)
	_ = m.Set("DefaultSerializer", serCtor)

	deserProto := engine.NewObject()
	for _, n := range []string{"readHeader", "readValue", "transferArrayBuffer", "readUint32", "readUint64", "readDouble", "readRawBytes", "getWireFormatVersion"} {
		_ = deserProto.Set(n, engine.NewFunction(n, func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
	}
	deserCtor := engine.NewFunction("Deserializer", func(args []engine.Value) (engine.Value, error) {
		return newV8ValueInstance(deserProto, "Deserializer"), nil
	})
	if co, ok := deserCtor.AsObject(); ok {
		_ = co.Set("prototype", deserProto)
	}
	_ = m.Set("Deserializer", deserCtor)
	_ = m.Set("DefaultDeserializer", deserCtor)

	// GCProfiler 类。
	gcProfProto := engine.NewObject()
	_ = gcProfProto.Set("start", engine.NewFunction("start", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = gcProfProto.Set("stop", engine.NewFunction("stop", func(args []engine.Value) (engine.Value, error) {
		return engine.NewObject(), nil
	}))
	gcProfCtor := engine.NewFunction("GCProfiler", func(args []engine.Value) (engine.Value, error) {
		return newV8ValueInstance(gcProfProto, "GCProfiler"), nil
	})
	if co, ok := gcProfCtor.AsObject(); ok {
		_ = co.Set("prototype", gcProfProto)
	}
	_ = m.Set("GCProfiler", gcProfCtor)

	return m, nil
}

// newV8ValueInstance 构造带指定原型方法集的实例对象。
func newV8ValueInstance(proto engine.Object, name string) engine.Value {
	inst := engine.NewObject()
	for _, k := range proto.Keys() {
		if v, err := proto.Get(k); err == nil {
			_ = inst.Set(k, v)
		}
	}
	_ = inst.Set("name", engine.Str(name))
	return inst
}

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

// generateHeapSnapshotJSON 生成符合 Chrome DevTools Heap Snapshot 规范的 JSON 结构
func generateHeapSnapshotJSON(ctx engine.Context) string {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	stringsList := []string{
		"(root)",
		"globalThis",
		"Object",
		"Array",
		"Function",
		"String",
		"Number",
		"Boolean",
		"AlukaRuntime",
		"PureGoVM",
	}

	nodes := []int{
		9, 0, 1, 0, 2, 0, // node 0: (root) -> id=1, self_size=0, edge_count=2
		3, 1, 2, int(ms.HeapAlloc / 2), 1, 0, // node 1: globalThis -> id=2, edge_count=1
		3, 8, 3, int(ms.HeapAlloc / 2), 0, 0, // node 2: AlukaRuntime -> id=3, edge_count=0
	}

	edges := []int{
		2, 1, 6, // edge 0: property -> globalThis (to node 1, offset 6)
		2, 8, 12, // edge 1: property -> AlukaRuntime (to node 2, offset 12)
		2, 9, 12, // edge 2: property -> PureGoVM
	}

	nodeCount := len(nodes) / 6
	edgeCount := len(edges) / 3

	stringsJSON, _ := json.Marshal(stringsList)
	nodesJSON, _ := json.Marshal(nodes)
	edgesJSON, _ := json.Marshal(edges)

	return fmt.Sprintf(`{
  "snapshot": {
    "meta": {
      "node_fields": ["type", "name", "id", "self_size", "edge_count", "trace_node_id"],
      "node_types": [["hidden", "array", "string", "object", "code", "closure", "regexp", "number", "native", "synthetic", "concatenated string", "sliced string", "symbol", "bigint"], "string", "number", "number", "number", "number"],
      "edge_fields": ["type", "name_or_index", "to_node"],
      "edge_types": [["context", "element", "property", "internal", "hidden", "shortcut", "weak"], "string_or_number", "node_offset"],
      "trace_function_info_fields": ["function_id", "name", "script_name", "script_id", "line", "column"],
      "trace_node_fields": ["id", "function_info_index", "count", "size", "children"],
      "sample_fields": ["timestamp_us", "last_assigned_id"]
    },
    "node_count": %d,
    "edge_count": %d,
    "trace_function_count": 0
  },
  "nodes": %s,
  "edges": %s,
  "trace_function_infos": [],
  "trace_tree": [],
  "samples": [],
  "strings": %s
}`, nodeCount, edgeCount, string(nodesJSON), string(edgesJSON), string(stringsJSON))
}
