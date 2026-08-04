package globals

// 全局 URL / URLSearchParams（WHATWG 标准，开发计划 2.6）。
//
// 基于 Go net/url 解析，URL 对象以闭包持有 *url.URL 状态；searchParams
// 与 URL 绑定——对 searchParams 的修改会同步更新 URL 的 search/href。
// 属性为简化实现：可读可写，但赋值不触发重新解析（Node 语义的子集）。

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// URLConfig 配置 URL 全局（当前无可用选项）。
type URLConfig struct{}

// NewURL 注册全局 URL / URLSearchParams 构造器。
func NewURL(ctx engine.Context, cfg URLConfig) error {
	_ = ctx.Global().Set("URL", engine.NewFunction("URL", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("%w: URL requires at least one argument", engine.ErrTypeError)
		}
		input := args[0].String()
		base := ""
		if len(args) > 1 {
			base = args[1].String()
		}
		u, err := parseURL(input, base)
		if err != nil {
			return engine.Undefined(), err
		}
		return newURLInstance(u), nil
	}))
	_ = ctx.Global().Set("URLSearchParams", engine.NewFunction("URLSearchParams", func(args []engine.Value) (engine.Value, error) {
		var pairs []urlParam
		if len(args) > 0 && !args[0].IsUndefined() {
			pairs = parseSearchParamsInit(args[0])
		}
		return newURLSearchParamsInstance(pairs, nil), nil
	}))
	return nil
}

// parseURL 解析 URL（支持 base 相对解析）。
func parseURL(input, base string) (*url.URL, error) {
	if base != "" {
		b, err := url.Parse(base)
		if err != nil {
			return nil, fmt.Errorf("URL: invalid base %q: %w", base, err)
		}
		rel, err := url.Parse(input)
		if err != nil {
			return nil, fmt.Errorf("URL: invalid URL %q: %w", input, err)
		}
		return b.ResolveReference(rel), nil
	}
	u, err := url.Parse(input)
	if err != nil {
		return nil, fmt.Errorf("URL: invalid URL %q: %w", input, err)
	}
	return u, nil
}

// newURLInstance 构造 URL 对象。
func newURLInstance(u *url.URL) engine.Value {
	obj := engine.NewObject()
	setURLProps(obj, u)

	// 绑定 searchParams：修改时同步更新 search/href。
	sp := newURLSearchParamsInstance(parseQueryPairs(u.RawQuery), func(newPairs []urlParam) {
		u.RawQuery = encodePairs(newPairs)
		setURLProps(obj, u)
	})
	_ = obj.Set("searchParams", sp)

	_ = obj.Set("toString", engine.NewFunction("toString", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(u.String()), nil
	}))
	_ = obj.Set("toJSON", engine.NewFunction("toJSON", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(u.String()), nil
	}))
	return obj
}

// setURLProps 将 *url.URL 的组件写入 URL 对象属性。
func setURLProps(obj engine.Object, u *url.URL) {
	hostname := u.Hostname()
	port := u.Port()
	host := u.Host
	if hostname == "" && host != "" {
		hostname = host
	}
	pathname := u.Path
	if pathname == "" {
		pathname = "/"
	}
	search := ""
	if u.RawQuery != "" {
		search = "?" + u.RawQuery
	}
	hash := ""
	if u.Fragment != "" {
		hash = "#" + u.Fragment
	}
	origin := ""
	if u.Scheme != "" {
		origin = u.Scheme + "://" + host
	}
	username := ""
	password := ""
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}
	_ = obj.Set("href", engine.Str(u.String()))
	_ = obj.Set("origin", engine.Str(origin))
	_ = obj.Set("protocol", engine.Str(u.Scheme+":"))
	_ = obj.Set("username", engine.Str(username))
	_ = obj.Set("password", engine.Str(password))
	_ = obj.Set("host", engine.Str(host))
	_ = obj.Set("hostname", engine.Str(hostname))
	_ = obj.Set("port", engine.Str(port))
	_ = obj.Set("pathname", engine.Str(pathname))
	_ = obj.Set("search", engine.Str(search))
	_ = obj.Set("hash", engine.Str(hash))
}

// --- URLSearchParams -------------------------------------------------------

// urlParam 是有序的键值对（保持插入顺序，Node 语义）。
type urlParam struct{ key, val string }

// newURLSearchParamsInstance 构造 URLSearchParams 对象。
// onChange 非 nil 时，任何修改后回调最新 pairs（用于同步绑定 URL）。
func newURLSearchParamsInstance(pairs []urlParam, onChange func([]urlParam)) engine.Value {
	obj := engine.NewObject()
	_ = obj.Set("size", engine.IntValue(len(pairs)))

	update := func() {
		_ = obj.Set("size", engine.IntValue(len(pairs)))
		if onChange != nil {
			onChange(pairs)
		}
	}

	// append(name, value)
	_ = obj.Set("append", engine.NewFunction("append", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		pairs = append(pairs, urlParam{args[0].String(), args[1].String()})
		update()
		return engine.Undefined(), nil
	}))
	// delete(name)
	_ = obj.Set("delete", engine.NewFunction("delete", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		key := args[0].String()
		out := pairs[:0]
		for _, p := range pairs {
			if p.key != key {
				out = append(out, p)
			}
		}
		pairs = out
		update()
		return engine.Undefined(), nil
	}))
	// get(name)：返回第一个匹配值
	_ = obj.Set("get", engine.NewFunction("get", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Null(), nil
		}
		key := args[0].String()
		for _, p := range pairs {
			if p.key == key {
				return engine.Str(p.val), nil
			}
		}
		return engine.Null(), nil
	}))
	// getAll(name)：返回所有匹配值数组
	_ = obj.Set("getAll", engine.NewFunction("getAll", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.NewArray(nil), nil
		}
		key := args[0].String()
		vals := make([]engine.Value, 0)
		for _, p := range pairs {
			if p.key == key {
				vals = append(vals, engine.Str(p.val))
			}
		}
		return engine.NewArray(vals), nil
	}))
	// has(name)
	_ = obj.Set("has", engine.NewFunction("has", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		key := args[0].String()
		for _, p := range pairs {
			if p.key == key {
				return engine.Boolean(true), nil
			}
		}
		return engine.Boolean(false), nil
	}))
	// set(name, value)：删除同名后追加
	_ = obj.Set("set", engine.NewFunction("set", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		key, val := args[0].String(), args[1].String()
		out := pairs[:0]
		for _, p := range pairs {
			if p.key != key {
				out = append(out, p)
			}
		}
		out = append(out, urlParam{key, val})
		pairs = out
		update()
		return engine.Undefined(), nil
	}))
	// sort()：按键稳定排序
	_ = obj.Set("sort", engine.NewFunction("sort", func(args []engine.Value) (engine.Value, error) {
		sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].key < pairs[j].key })
		update()
		return engine.Undefined(), nil
	}))
	// toString()
	_ = obj.Set("toString", engine.NewFunction("toString", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(encodePairs(pairs)), nil
	}))
	// keys() / values() / entries()：返回数组（简化，Node 返回迭代器）
	_ = obj.Set("keys", engine.NewFunction("keys", func(args []engine.Value) (engine.Value, error) {
		keys := make([]engine.Value, 0, len(pairs))
		for _, p := range pairs {
			keys = append(keys, engine.Str(p.key))
		}
		return engine.NewArray(keys), nil
	}))
	_ = obj.Set("values", engine.NewFunction("values", func(args []engine.Value) (engine.Value, error) {
		vals := make([]engine.Value, 0, len(pairs))
		for _, p := range pairs {
			vals = append(vals, engine.Str(p.val))
		}
		return engine.NewArray(vals), nil
	}))
	_ = obj.Set("entries", engine.NewFunction("entries", func(args []engine.Value) (engine.Value, error) {
		entries := make([]engine.Value, 0, len(pairs))
		for _, p := range pairs {
			entries = append(entries, engine.NewArray([]engine.Value{engine.Str(p.key), engine.Str(p.val)}))
		}
		return engine.NewArray(entries), nil
	}))
	// forEach(callback[, thisArg])
	_ = obj.Set("forEach", engine.NewFunction("forEach", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !args[0].IsFunction() {
			return engine.Undefined(), nil
		}
		f, _ := args[0].AsFunction()
		for _, p := range pairs {
			_, _ = f.Call([]engine.Value{engine.Str(p.val), engine.Str(p.key), obj})
		}
		return engine.Undefined(), nil
	}))

	return obj
}

// parseSearchParamsInit 解析 URLSearchParams 构造参数。
func parseSearchParamsInit(v engine.Value) []urlParam {
	switch {
	case v.Type() == engine.TypeString:
		return parseQueryPairs(v.String())
	default:
		if o, ok := v.AsObject(); ok {
			// 数组形式：[[k,v],...] 或对象形式 {k:v}
			if a, ok := v.(*engine.ArrayValue); ok {
				pairs := make([]urlParam, 0)
				for _, e := range a.Elems() {
					if pair, ok := e.(*engine.ArrayValue); ok && len(pair.Elems()) >= 2 {
						pairs = append(pairs, urlParam{pair.Elems()[0].String(), pair.Elems()[1].String()})
					}
				}
				return pairs
			}
			// 普通对象
			pairs := make([]urlParam, 0, len(o.Keys()))
			for _, k := range o.Keys() {
				if val, err := o.Get(k); err == nil {
					pairs = append(pairs, urlParam{k, val.String()})
				}
			}
			return pairs
		}
	}
	return nil
}

// parseQueryPairs 解析查询字符串为有序键值对。
// WHATWG 语义：同名键聚合到首次出现的位置（new URLSearchParams(str) 的
// toString 按此顺序输出）。
func parseQueryPairs(query string) []urlParam {
	if query == "" {
		return nil
	}
	parts := strings.Split(query, "&")
	order := make([]string, 0)
	values := make(map[string][]string)
	for _, part := range parts {
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		key, _ := url.QueryUnescape(kv[0])
		val := ""
		if len(kv) == 2 {
			val, _ = url.QueryUnescape(kv[1])
		}
		if _, ok := values[key]; !ok {
			order = append(order, key)
		}
		values[key] = append(values[key], val)
	}
	pairs := make([]urlParam, 0)
	for _, key := range order {
		for _, v := range values[key] {
			pairs = append(pairs, urlParam{key, v})
		}
	}
	return pairs
}

// encodePairs 编码有序键值对为查询字符串（空格→+，Node 语义）。
func encodePairs(pairs []urlParam) string {
	var b strings.Builder
	for i, p := range pairs {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(p.key))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(p.val))
	}
	return b.String()
}
