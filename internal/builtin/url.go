package builtin

// node:url 内置模块——提供 URL 解析与格式化。
// 基于 Go net/url 标准库。
//
// 注意：全局 URL/URLSearchParams 构造器（WHATWG 标准）是一个独立的、更大的
// 实现。此模块提供的是 Node.js 的 legacy url 模块（parse/resolve/format），
// 加上 fileURLToPath/pathToFileURL 转换。

import (
	"net/url"
	"path/filepath"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewURL 构造 node:url 模块的导出对象。
func NewURL(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	_ = m.Set("parse", engine.NewFunction("parse", func(args []engine.Value) (engine.Value, error) {
		input := strArg(args, 0)
		u, err := url.Parse(input)
		if err != nil {
			return engine.Undefined(), err
		}
		return urlToObj(u), nil
	}))

	_ = m.Set("format", engine.NewFunction("format", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		u := objToURL(args[0])
		return engine.Str(u.String()), nil
	}))

	_ = m.Set("resolve", engine.NewFunction("resolve", func(args []engine.Value) (engine.Value, error) {
		from := strArg(args, 0)
		to := strArg(args, 1)
		base, err := url.Parse(from)
		if err != nil {
			return engine.Str(to), nil
		}
		ref, err := url.Parse(to)
		if err != nil {
			return engine.Str(to), nil
		}
		return engine.Str(base.ResolveReference(ref).String()), nil
	}))

	_ = m.Set("fileURLToPath", engine.NewFunction("fileURLToPath", func(args []engine.Value) (engine.Value, error) {
		input := strArg(args, 0)
		// file:///path → /path
		if strings.HasPrefix(input, "file://") {
			rest := input[7:]
			// Windows: file:///C:/path → C:/path
			if len(rest) >= 3 && rest[0] == '/' && rest[2] == ':' {
				rest = rest[1:]
			}
			return engine.Str(filepath.FromSlash(rest)), nil
		}
		return engine.Str(input), nil
	}))

	_ = m.Set("pathToFileURL", engine.NewFunction("pathToFileURL", func(args []engine.Value) (engine.Value, error) {
		input := strArg(args, 0)
		abs, err := filepath.Abs(input)
		if err != nil {
			return engine.Str(""), nil
		}
		slash := filepath.ToSlash(abs)
		// Windows: C:/path → /C:/path
		if len(slash) >= 2 && slash[1] == ':' {
			slash = "/" + slash
		}
		return engine.Str("file://" + slash), nil
	}))

	_ = m.Set("domainToASCII", engine.NewFunction("domainToASCII", func(args []engine.Value) (engine.Value, error) {
		// 简化：直接返回原值（Punycode 转换需额外依赖）。
		return engine.Str(strArg(args, 0)), nil
	}))

	_ = m.Set("domainToUnicode", engine.NewFunction("domainToUnicode", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(strArg(args, 0)), nil
	}))

	return m, nil
}

// urlToObj 将 *url.URL 转为 Node.js UrlObject。
func urlToObj(u *url.URL) engine.Value {
	obj := engine.NewObject()
	_ = obj.Set("protocol", engine.Str(u.Scheme+":"))
	host := u.Host
	hostname := u.Hostname()
	port := u.Port()
	_ = obj.Set("hostname", engine.Str(hostname))
	_ = obj.Set("port", engine.Str(port))
	if port != "" {
		_ = obj.Set("host", engine.Str(host))
	} else {
		_ = obj.Set("host", engine.Str(hostname))
	}
	_ = obj.Set("pathname", engine.Str(u.Path))
	_ = obj.Set("search", engine.Str(u.RawQuery))
	if u.RawQuery != "" {
		_ = obj.Set("query", engine.Str(u.RawQuery))
	} else {
		_ = obj.Set("query", engine.NewObject())
	}
	_ = obj.Set("hash", engine.Str(u.Fragment))
	if u.User != nil {
		_ = obj.Set("auth", engine.Str(u.User.String()))
	} else {
		_ = obj.Set("auth", engine.Null())
	}
	_ = obj.Set("href", engine.Str(u.String()))
	return obj
}

// objToURL 从 engine.Value（UrlObject）构造 *url.URL。
func objToURL(v engine.Value) *url.URL {
	u := &url.URL{}
	obj, ok := v.AsObject()
	if !ok {
		return u
	}
	if p, err := obj.Get("protocol"); err == nil {
		s := p.String()
		s = strings.TrimSuffix(s, ":")
		u.Scheme = s
	}
	if h, err := obj.Get("hostname"); err == nil {
		u.Host = h.String()
		if port, err := obj.Get("port"); err == nil && port.String() != "" {
			u.Host = u.Host + ":" + port.String()
		}
	}
	if pn, err := obj.Get("pathname"); err == nil {
		u.Path = pn.String()
	}
	if q, err := obj.Get("search"); err == nil && q.String() != "" {
		u.RawQuery = q.String()
	} else if q, err := obj.Get("query"); err == nil {
		u.RawQuery = q.String()
	}
	if h, err := obj.Get("hash"); err == nil && h.String() != "" {
		u.Fragment = strings.TrimPrefix(h.String(), "#")
	}
	return u
}
