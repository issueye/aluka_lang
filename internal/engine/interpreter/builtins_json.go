// 内置 JSON：stringify/parse 的值转换、toJSON 调用、属性顺序保持与 JSON.rawJSON 标记。

package interpreter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// rawJSONMarkerKey 是 JSON.rawJSON 对象的内部标记键（symbol-keyed，不被
// Keys() 枚举，JS 侧不可见）。JSON.isRawJSON 与 valueToJSON 靠它识别
// rawJSON 对象；"rawJSON" 自有属性是 V8 的可观察镜像（writable:false）。
var rawJSONMarkerKey = engine.NewSymbol("aluka.rawJSON").SymbolKey()

// rawJSONBox 是 JSON.rawJSON 原始文本的序列化占位：MarshalJSON 原样输出
// （不转义、不加引号），stringify 遇到 rawJSON 对象时内联其原始 JSON 文本。
type rawJSONBox struct{ text string }

func (b rawJSONBox) MarshalJSON() ([]byte, error) { return []byte(b.text), nil }

// rawJSONValueOf 返回 v 的 rawJSON 原始文本；不是 rawJSON 对象返回 ("", false)。
func (interp *Interpreter) rawJSONValueOf(v engine.Value) (string, bool) {
	o, ok := v.AsObject()
	if !ok {
		return "", false
	}
	text, err := o.Get(rawJSONMarkerKey)
	if err != nil || text.IsUndefined() {
		return "", false
	}
	return text.String(), true
}

func (interp *Interpreter) setupJSON() {
	j := engine.NewObject()
	_ = j.Set("stringify", interp.makeFunc("stringify", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		s, err := interp.jsonValueToJSON(args[0])
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Str(s), nil
	}))
	_ = j.Set("parse", interp.makeFunc("parse", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("%w: JSON.parse requires an argument", engine.ErrSyntaxError)
		}
		var data interface{}
		if err := json.Unmarshal([]byte(args[0].String()), &data); err != nil {
			return nil, fmt.Errorf("%w: %v", engine.ErrSyntaxError, err)
		}
		return jsonToValue(data), nil
	}))
	// JSON.rawJSON / JSON.isRawJSON（JSON.parse source 提案，V8 12 已实现；
	// 行为对齐 Node 22 实测）：rawJSON 对入参做 ToString 后校验其本身是
	// 合法 JSON 文本，产出携带原始文本的 marker 对象；stringify 内联该
	// 文本。symbol 抛 TypeError；对象/数组/undefined/NaN 等非法文本抛
	// SyntaxError。
	_ = j.Set("rawJSON", interp.makeFunc("rawJSON", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("%w: JSON.rawJSON requires an argument", engine.ErrTypeError)
		}
		v := args[0]
		var text string
		switch v.Type() {
		case engine.TypeSymbol:
			return nil, fmt.Errorf("%w: Cannot convert a Symbol value to a string", engine.ErrTypeError)
		case engine.TypeObject, engine.TypeFunction:
			return nil, fmt.Errorf("%w: Invalid value for JSON.rawJSON", engine.ErrSyntaxError)
		default:
			text = v.String()
		}
		if !json.Valid([]byte(text)) {
			return nil, fmt.Errorf("%w: %q is not valid JSON", engine.ErrSyntaxError, text)
		}
		obj := engine.NewObject()
		_ = obj.Set(rawJSONMarkerKey, engine.Str(text))
		_ = engine.DefineOwnProperty(obj, "rawJSON", engine.Descriptor{
			HasValue:        true,
			Value:           engine.Str(text),
			HasWritable:     true,
			Writable:        false,
			HasEnumerable:   true,
			Enumerable:      true,
			HasConfigurable: true,
			Configurable:    false,
		})
		return obj, nil
	}))
	_ = j.Set("isRawJSON", interp.makeFunc("isRawJSON", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		_, ok := interp.rawJSONValueOf(args[0])
		return engine.Boolean(ok), nil
	}))
	_ = interp.globalObj.Set("JSON", j)
}

func (interp *Interpreter) jsonValueToJSON(v engine.Value) (string, error) {
	data, err := interp.valueToJSON(v, make(map[engine.Object]bool), "")
	if err != nil {
		return "", err
	}
	b, err := jsonNoEscape(data)
	return string(b), err
}

// applyToJSON 实现 JSON.stringify 的 toJSON 钩子（ES SerializeJSONProperty：
// 枚举属性前调用）。key 是序列化上下文键——根值为 ""、对象属性为属性名、
// 数组元素为下标字符串，与 Node 一致。
// jiti 的 import.meta.url 替换依赖 JSON.stringify(pathToFileURL(filename))；
// URL.toJSON 必须返回 href 字符串，否则 Babel template.ast 会吃到对象字面量
// 里的 Windows 盘符路径。
func (interp *Interpreter) applyToJSON(v engine.Value, key string) (engine.Value, error) {
	if v == nil || v.IsUndefined() || v.IsNull() {
		return v, nil
	}
	switch v.Type() {
	case engine.TypeObject, engine.TypeFunction:
	default:
		return v, nil
	}
	var toJSON engine.Value
	if vm := interp.currentVM; vm != nil {
		toJSON, _ = vm.getProperty(v, "toJSON")
	} else if o, ok := v.AsObject(); ok {
		toJSON, _ = o.Get("toJSON")
	}
	if toJSON == nil || !toJSON.IsFunction() {
		return v, nil
	}
	var (
		result engine.Value
		err    error
	)
	args := []engine.Value{engine.Str(key)}
	if vm := interp.currentVM; vm != nil {
		result, err = vm.InvokeFn(toJSON, v, args)
	} else if f, ok := toJSON.AsFunction(); ok {
		result, err = f.Call(args)
	} else {
		return v, nil
	}
	if err != nil {
		return engine.Undefined(), err
	}
	return result, nil
}

// jsonNoEscape 序列化且不做 HTML 转义（Go json.Marshal 默认把 < > & 转成
// \u003c 等，而 JS JSON.stringify 原样输出）。
func jsonNoEscape(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	b := buf.Bytes()
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
	}
	return b, nil
}

// orderedJSON 保持对象属性插入顺序的 JSON 序列化容器
// （JS 语义：JSON.stringify 按属性插入顺序输出，而非 Go map 的字母序）。
type orderedJSON struct {
	keys []string
	vals []interface{}
}

func (o *orderedJSON) MarshalJSON() ([]byte, error) {
	parts := make([]string, len(o.keys))
	for i, k := range o.keys {
		kb, err := jsonNoEscape(k)
		if err != nil {
			return nil, err
		}
		vb, err := jsonNoEscape(o.vals[i])
		if err != nil {
			return nil, err
		}
		parts[i] = string(kb) + ":" + string(vb)
	}
	return []byte("{" + strings.Join(parts, ",") + "}"), nil
}

// valueToJSON 将 JS 值转为可 JSON 序列化的 Go 结构。key 为当前序列化
// 上下文键（根值 ""、属性名、数组下标），透传给 toJSON 钩子。
// seen 记录当前递归路径上的对象，用于检测循环引用（命中返回 TypeError，
// 避免无限递归导致 Go 栈溢出崩溃）。对象在完成自身序列化后从 seen 移除，
// 因此共享但非循环的引用不会被误判。
func (interp *Interpreter) valueToJSON(v engine.Value, seen map[engine.Object]bool, key string) (interface{}, error) {
	converted, err := interp.applyToJSON(v, key)
	if err != nil {
		return nil, err
	}
	v = converted
	if v == nil || v.IsUndefined() {
		return nil, nil
	}
	switch v.Type() {
	case engine.TypeNull:
		return nil, nil
	case engine.TypeBoolean:
		b, _ := v.Bool()
		return b, nil
	case engine.TypeNumber:
		f, _ := v.Float()
		// JSON.stringify 语义：NaN/±Infinity 序列化为 null（ES 25.5.2
		// SerializeJSONValue 的 ToNumber 分支）；Go json 无法编码它们。
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, nil
		}
		return f, nil
	case engine.TypeString:
		return v.String(), nil
	case engine.TypeObject, engine.TypeFunction:
		if text, ok := interp.rawJSONValueOf(v); ok {
			// rawJSON 对象：内联原始 JSON 文本，不遍历属性。
			return rawJSONBox{text: text}, nil
		}
		if arr, ok := v.(*engine.ArrayValue); ok {
			o, _ := arr.AsObject()
			if seen[o] {
				return nil, fmt.Errorf("%w: Converting circular structure to JSON", engine.ErrTypeError)
			}
			seen[o] = true
			elems := arr.Elems()
			result := make([]interface{}, len(elems))
			for i, e := range elems {
				if e.IsUndefined() {
					result[i] = nil
					continue
				}
				e = interp.resolveJSONAccessor(v, strconv.Itoa(i), e)
				r, err := interp.valueToJSON(e, seen, strconv.Itoa(i))
				if err != nil {
					return nil, err
				}
				result[i] = r
			}
			delete(seen, o)
			return result, nil
		}
		if o, ok := v.AsObject(); ok {
			if seen[o] {
				return nil, fmt.Errorf("%w: Converting circular structure to JSON", engine.ErrTypeError)
			}
			seen[o] = true
			oj := &orderedJSON{}
			for _, k := range o.Keys() {
				raw, _ := o.Get(k)
				val := interp.resolveJSONAccessor(v, k, raw)
				if val.IsFunction() || val.IsUndefined() {
					continue
				}
				r, err := interp.valueToJSON(val, seen, k)
				if err != nil {
					return nil, err
				}
				oj.keys = append(oj.keys, k)
				oj.vals = append(oj.vals, r)
			}
			delete(seen, o)
			return oj, nil
		}
	}
	return nil, nil
}

// resolveJSONAccessor 把 Go 侧 Get 拿到的访问器值（AccessorValue）换成
// getter 求值结果：ESM 命名空间导出是活绑定 getter，Object.Get 不触发
// JS getter，不解析会把导出序列化成 null。非访问器原样返回，常规对象
// 序列化零额外开销。
func (interp *Interpreter) resolveJSONAccessor(owner engine.Value, key string, raw engine.Value) engine.Value {
	if !engine.IsAccessorValue(raw) {
		return raw
	}
	if vm := interp.currentVM; vm != nil {
		if v, err := vm.getProperty(owner, key); err == nil {
			return v
		}
		return engine.Undefined()
	}
	if acc, ok := raw.(*engine.AccessorValue); ok && acc.Getter != nil && acc.Getter.IsFunction() {
		if f, ok := acc.Getter.AsFunction(); ok {
			if v, err := f.Call(nil); err == nil {
				return v
			}
		}
	}
	return engine.Undefined()
}

func jsonToValue(data interface{}) engine.Value {
	switch v := data.(type) {
	case nil:
		return engine.Null()
	case bool:
		return engine.Boolean(v)
	case float64:
		return engine.Number(v)
	case string:
		return engine.Str(v)
	case []interface{}:
		elems := make([]engine.Value, len(v))
		for i, e := range v {
			elems[i] = jsonToValue(e)
		}
		return engine.NewArray(elems)
	case map[string]interface{}:
		obj := engine.NewObject()
		for k, val := range v {
			_ = obj.Set(k, jsonToValue(val))
		}
		return obj
	}
	return engine.Undefined()
}
