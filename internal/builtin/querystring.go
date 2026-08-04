package builtin

// node:querystring 内置模块——URL 查询字符串解析与序列化。

import (
	"net/url"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewQueryString 构造 node:querystring 模块的导出对象。
func NewQueryString(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// parse(str, sep, eq, options)
	_ = m.Set("parse", engine.NewFunction("parse", func(args []engine.Value) (engine.Value, error) {
		input := strArg(args, 0)
		sep := "&"
		if len(args) > 1 && args[1].String() != "" {
			sep = args[1].String()
		}
		eq := "="
		if len(args) > 2 && args[2].String() != "" {
			eq = args[2].String()
		}
		result := engine.NewObject()
		if input == "" {
			return result, nil
		}
		// 去掉开头的 ?
		input = strings.TrimPrefix(input, "?")
		for _, pair := range strings.Split(input, sep) {
			kv := strings.SplitN(pair, eq, 2)
			key, _ := url.QueryUnescape(kv[0])
			val := ""
			if len(kv) > 1 {
				val, _ = url.QueryUnescape(kv[1])
			}
			// 已存在的 key 转为数组。
			if existing, err := result.Get(key); err == nil && !existing.IsUndefined() {
				if existingArr, ok := existing.(*engine.ArrayValue); ok {
					existingArr.Append(engine.Str(val))
				} else {
					// 转为数组
					arr := engine.NewArray([]engine.Value{existing, engine.Str(val)})
					_ = result.Set(key, arr)
				}
			} else {
				_ = result.Set(key, engine.Str(val))
			}
		}
		return result, nil
	}))

	// stringify(obj, sep, eq, options)
	_ = m.Set("stringify", engine.NewFunction("stringify", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		sep := "&"
		if len(args) > 1 && args[1].String() != "" {
			sep = args[1].String()
		}
		eq := "="
		if len(args) > 2 && args[2].String() != "" {
			eq = args[2].String()
		}
		obj, ok := args[0].AsObject()
		if !ok {
			return engine.Str(""), nil
		}
		var pairs []string
		for _, key := range obj.Keys() {
			val, _ := obj.Get(key)
			if arr, ok := val.(*engine.ArrayValue); ok {
				for _, e := range arr.Elems() {
					pairs = append(pairs, url.QueryEscape(key)+eq+url.QueryEscape(e.String()))
				}
			} else {
				pairs = append(pairs, url.QueryEscape(key)+eq+url.QueryEscape(val.String()))
			}
		}
		return engine.Str(strings.Join(pairs, sep)), nil
	}))

	// escape(str)
	_ = m.Set("escape", engine.NewFunction("escape", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(url.QueryEscape(strArg(args, 0))), nil
	}))

	// unescape(str)
	_ = m.Set("unescape", engine.NewFunction("unescape", func(args []engine.Value) (engine.Value, error) {
		s, err := url.QueryUnescape(strArg(args, 0))
		if err != nil {
			return engine.Str(strArg(args, 0)), nil
		}
		return engine.Str(s), nil
	}))

	return m, nil
}
