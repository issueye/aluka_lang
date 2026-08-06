package builtin

// node:path 内置模块——提供路径操作工具。
// 基于 Go path/filepath 标准库，平台自动分发（posix/win32）。
//
// 实现：顶层方法使用当前平台语义；posix/win32 子对象分别固定为对应平台语义。

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewPath 构造 node:path 模块的导出对象。
func NewPath(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// 顶层属性与方法（当前平台语义）。
	registerPathMethods(m, currentPlatform())

	// posix 子对象（固定用 `/` 分隔符）。
	posix := engine.NewObject()
	registerPathMethods(posix, "posix")
	_ = m.Set("posix", posix)

	// win32 子对象（固定用 `\` 分隔符）。
	win32 := engine.NewObject()
	registerPathMethods(win32, "win32")
	_ = m.Set("win32", win32)

	return m, nil
}

// currentPlatform 返回当前平台标识。
func currentPlatform() string {
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return "posix"
}

// pathImpl 封装特定平台的路径操作（避免直接在闭包里重复平台判断）。
type pathImpl struct {
	sep        string
	delimiter  string
	join       func(elem ...string) string
	resolve    func(elem ...string) string
	normalize  func(p string) string
	dirname    func(p string) string
	basename   func(p, ext string) string
	extname    func(p string) string
	rel        func(base, target string) string
	isAbs      func(p string) bool
	toSlash    func(p string) string
	fromSlash  func(p string) string
}

func newPathImpl(platform string) pathImpl {
	if platform == "win32" {
		return pathImpl{
			sep:       `\`,
			delimiter: `;`,
			join:      filepath.Join,
			resolve: func(elem ...string) string {
				abs, _ := filepath.Abs(filepath.Join(elem...))
				return abs
			},
			normalize: filepath.Clean,
			dirname:   filepath.Dir,
			basename: func(p, ext string) string {
				b := filepath.Base(p)
				if ext != "" && len(b) > len(ext) && b[len(b)-len(ext):] == ext {
					return b[:len(b)-len(ext)]
				}
				return b
			},
			extname: filepath.Ext,
			rel: func(base, target string) string {
				r, _ := filepath.Rel(base, target)
				return r
			},
			isAbs:     filepath.IsAbs,
			toSlash:   filepath.ToSlash,
			fromSlash: filepath.FromSlash,
		}
	}
	return pathImpl{
		sep:       `/`,
		delimiter: `:`,
		join:      path.Join,
		resolve: func(elem ...string) string {
			resolved := path.Join(elem...)
			if !path.IsAbs(resolved) {
				// 相对路径基于当前工作目录（转成 posix 形式）。
				if wd, err := os.Getwd(); err == nil {
					resolved = path.Join(filepath.ToSlash(wd), resolved)
				}
			}
			return path.Clean(resolved)
		},
		normalize: path.Clean,
		dirname:   path.Dir,
		basename: func(p, ext string) string {
			b := path.Base(p)
			if ext != "" && len(b) > len(ext) && b[len(b)-len(ext):] == ext {
				return b[:len(b)-len(ext)]
			}
			return b
		},
		extname: path.Ext,
		rel: func(base, target string) string {
			r, _ := filepath.Rel(base, target)
			return r
		},
		isAbs:     path.IsAbs,
		toSlash:   func(p string) string { return p },
		fromSlash: func(p string) string { return p },
	}
}

// registerPathMethods 在对象上注册路径方法（join/resolve/dirname 等）。
func registerPathMethods(m engine.Object, platform string) {
	impl := newPathImpl(platform)

	_ = m.Set("sep", engine.Str(impl.sep))
	_ = m.Set("delimiter", engine.Str(impl.delimiter))

	// path.matchesGlob(path, pattern)：glob 匹配（Node 22.3+；注意参数顺序
	// 是 (path, pattern)，与文档示例相反）。win32 时 '/' 与 '\' 等价。
	_ = m.Set("matchesGlob", engine.NewFunction("matchesGlob", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Boolean(false), nil
		}
		p := args[0].String()
		pat := args[1].String()
		if platform == "win32" {
			p = strings.ReplaceAll(p, "\\", "/")
		}
		nocase := runtime.GOOS == "windows" || runtime.GOOS == "darwin"
		for _, cp := range globCompilePattern(pat, nocase) {
			if globFullMatchExact(cp, p) {
				return engine.Boolean(true), nil
			}
		}
		return engine.Boolean(false), nil
	}))

	_ = m.Set("join", engine.NewFunction("join", func(args []engine.Value) (engine.Value, error) {
		parts := toStringSlice(args)
		return engine.Str(impl.join(parts...)), nil
	}))

	_ = m.Set("resolve", engine.NewFunction("resolve", func(args []engine.Value) (engine.Value, error) {
		parts := toStringSlice(args)
		return engine.Str(impl.resolve(parts...)), nil
	}))

	_ = m.Set("normalize", engine.NewFunction("normalize", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str("."), nil
		}
		return engine.Str(impl.normalize(args[0].String())), nil
	}))

	_ = m.Set("dirname", engine.NewFunction("dirname", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str("."), nil
		}
		return engine.Str(impl.dirname(args[0].String())), nil
	}))

	_ = m.Set("basename", engine.NewFunction("basename", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		ext := ""
		if len(args) > 1 {
			ext = args[1].String()
		}
		return engine.Str(impl.basename(args[0].String(), ext)), nil
	}))

	_ = m.Set("extname", engine.NewFunction("extname", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		return engine.Str(nodeExtname(impl, args[0].String())), nil
	}))

	_ = m.Set("relative", engine.NewFunction("relative", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Str(""), nil
		}
		return engine.Str(impl.rel(args[0].String(), args[1].String())), nil
	}))

	_ = m.Set("isAbsolute", engine.NewFunction("isAbsolute", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(impl.isAbs(args[0].String())), nil
	}))

	_ = m.Set("parse", engine.NewFunction("parse", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.NewObject(), nil
		}
		return pathParse(args[0].String(), impl)
	}))

	_ = m.Set("format", engine.NewFunction("format", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		return pathFormat(args[0], impl), nil
	}))

	_ = m.Set("toNamespacedPath", engine.NewFunction("toNamespacedPath", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		// Windows 上会有 \\?\ 前缀处理，这里简化为原样返回。
		return engine.Str(args[0].String()), nil
	}))
}

// pathParse 将路径解析为 {root, dir, base, name, ext} 对象。
func pathParse(p string, impl pathImpl) (engine.Value, error) {
	result := engine.NewObject()
	dir := impl.dirname(p)
	base := impl.basename(p, "")
	ext := impl.extname(p)
	name := base
	if ext != "" && len(base) > len(ext) {
		name = base[:len(base)-len(ext)]
	}
	root := ""
	if impl.isAbs(p) {
		// root 是 dir 的开头部分（/ 或 C:\）。
		if len(dir) > 0 {
			// 简化：posix 的 root 是 "/"，win32 的 root 可能是 "C:\"。
			if dir[0] == '/' || dir[0] == '\\' {
				root = impl.sep
			} else if len(dir) >= 2 && dir[1] == ':' {
				root = dir[:3]
			}
		}
	}
	_ = result.Set("root", engine.Str(root))
	_ = result.Set("dir", engine.Str(dir))
	_ = result.Set("base", engine.Str(base))
	_ = result.Set("name", engine.Str(name))
	_ = result.Set("ext", engine.Str(ext))
	return result, nil
}

// pathFormat 从路径对象 {root, dir, base, name, ext} 构造路径字符串。
func pathFormat(v engine.Value, impl pathImpl) engine.Value {
	obj, ok := v.AsObject()
	if !ok {
		return engine.Str("")
	}
	var dir, base, name, ext, root string
	if d, err := obj.Get("dir"); err == nil {
		dir = d.String()
	}
	if b, err := obj.Get("base"); err == nil {
		base = b.String()
	}
	if n, err := obj.Get("name"); err == nil {
		name = n.String()
	}
	if e, err := obj.Get("ext"); err == nil {
		ext = e.String()
	}
	if r, err := obj.Get("root"); err == nil {
		root = r.String()
	}

	if base == "" {
		base = name + ext
	}
	if dir == "" {
		dir = root
	}
	if dir != "" && base != "" {
		return engine.Str(dir + impl.sep + base)
	}
	return engine.Str(dir + base)
}

// nodeExtname 实现 Node.js path.extname 语义：
// ".bashrc" → ""（整个 basename 视为隐藏文件名，无扩展名）
// "file.txt" → ".txt"
// "file.tar.gz" → ".gz"
// 规则：取 basename 中最后一个 "." 及之后的内容；若 "." 是 basename 首字符则忽略。
func nodeExtname(impl pathImpl, p string) string {
	base := impl.basename(p, "")
	// 找最后一个 "."
	idx := -1
	for i := len(base) - 1; i >= 0; i-- {
		if base[i] == '.' {
			idx = i
			break
		}
	}
	if idx <= 0 {
		// 无 "." 或 "." 在首位（隐藏文件）
		return ""
	}
	return base[idx:]
}

// toStringSlice 将 []engine.Value 转为 []string。
func toStringSlice(args []engine.Value) []string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = a.String()
	}
	return parts
}
