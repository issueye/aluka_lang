package gfetch

// Web API：URLPattern（开发计划 3.6）。
//
// 简化实现：支持 pathname 模式匹配（:param 命名参数、* 通配符）。
// new URLPattern('/users/:id') 或 new URLPattern({ pathname: '...' })；
// test(url)/exec(url) 返回 {pathname: {groups}}。

import (
	"regexp"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// URLPatternConfig 配置 URLPattern 全局。
type URLPatternConfig struct{}

// NewURLPattern 注册全局 URLPattern。
func NewURLPattern(ctx engine.Context, cfg URLPatternConfig) error {
	_ = ctx.Global().Set("URLPattern", engine.NewFunction("URLPattern", func(args []engine.Value) (engine.Value, error) {
		return newURLPatternInstance(args), nil
	}))
	return nil
}

// urlPatternState 是 URLPattern 的内部状态。
type urlPatternState struct {
	re    *regexp.Regexp
	names []string
}

// newURLPatternInstance 构造 URLPattern。
func newURLPatternInstance(args []engine.Value) engine.Value {
	obj := engine.NewObject()
	state := &urlPatternState{}

	// 提取 pathname 模式。
	pattern := ""
	if len(args) > 0 {
		switch {
		case args[0].Type() == engine.TypeString:
			pattern = args[0].String()
		default:
			if o, ok := args[0].AsObject(); ok {
				if v, err := o.Get("pathname"); err == nil && !v.IsUndefined() {
					pattern = v.String()
				} else if v, err := o.Get("url"); err == nil && !v.IsUndefined() {
					pattern = v.String()
				}
			}
		}
	}
	compileURLPattern(state, pattern)

	// test(url) → boolean。
	_ = obj.Set("test", engine.NewFunction("test", func(a []engine.Value) (engine.Value, error) {
		path := patternPath(a, 0)
		return engine.Boolean(state.re != nil && state.re.MatchString(path)), nil
	}))

	// exec(url) → {pathname: {groups}} 或 null。
	_ = obj.Set("exec", engine.NewFunction("exec", func(a []engine.Value) (engine.Value, error) {
		path := patternPath(a, 0)
		if state.re == nil {
			return engine.Null(), nil
		}
		m := state.re.FindStringSubmatch(path)
		if m == nil {
			return engine.Null(), nil
		}
		groups := engine.NewObject()
		for i, name := range state.names {
			if i+1 < len(m) {
				_ = groups.Set(name, engine.Str(m[i+1]))
			}
		}
		pathname := engine.NewObject()
		_ = pathname.Set("groups", groups)
		result := engine.NewObject()
		_ = result.Set("pathname", pathname)
		return result, nil
	}))

	return obj
}

// patternPath 从参数提取 URL 的 pathname 部分。
func patternPath(args []engine.Value, i int) string {
	if i >= len(args) {
		return ""
	}
	if args[i].Type() == engine.TypeString {
		s := args[i].String()
		// 提取路径部分。
		if idx := strings.Index(s, "//"); idx >= 0 {
			rest := s[idx+2:]
			if slash := strings.Index(rest, "/"); slash >= 0 {
				return rest[slash:]
			}
			return "/"
		}
		if idx := strings.IndexByte(s, '?'); idx >= 0 {
			return s[:idx]
		}
		return s
	}
	if o, ok := args[i].AsObject(); ok {
		if v, err := o.Get("pathname"); err == nil && !v.IsUndefined() {
			return v.String()
		}
		if v, err := o.Get("href"); err == nil && !v.IsUndefined() {
			return patternPath([]engine.Value{v}, 0)
		}
	}
	return ""
}

// compileURLPattern 把路径模式转成正则。
// '/users/:id' → ^/users/([^/]+)$；'*' → .*。
func compileURLPattern(state *urlPatternState, pattern string) {
	if pattern == "" {
		state.re = regexp.MustCompile(`.*`)
		return
	}
	var b strings.Builder
	b.WriteString("^")
	var names []string
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case ':':
			// 命名参数：读名字。
			j := i + 1
			for j < len(pattern) && (isIdentChar(pattern[j])) {
				j++
			}
			name := pattern[i+1 : j]
			names = append(names, name)
			b.WriteString("([^/]+)")
			i = j - 1
		case '*':
			b.WriteString(".*")
		default:
			if strings.ContainsRune(`.+()[]{}^$|\\`, rune(ch)) {
				b.WriteByte('\\')
			}
			b.WriteByte(ch)
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		state.re = regexp.MustCompile(`.*`)
		return
	}
	state.re = re
	state.names = names
}

func isIdentChar(ch byte) bool {
	return ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')
}
