package builtin

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// 本文件测试 4 个 Node.js 内置模块（node:path / node:os / node:url / node:util）。
// 直接测试工厂函数返回的导出对象，不经过文件系统加载。
//
// 辅助：newCtx 创建带 VM 的 context（用于工厂函数）。
func newCtx(t *testing.T) engine.Context {
	t.Helper()
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ctx.Close() })
	return ctx
}

// callMethod 在对象上调用方法，返回结果。
func callMethod(t *testing.T, obj engine.Value, method string, args ...engine.Value) engine.Value {
	t.Helper()
	o, ok := obj.AsObject()
	if !ok {
		t.Fatalf("not an object: %v", obj)
	}
	fn, err := o.Get(method)
	if err != nil || !fn.IsFunction() {
		t.Fatalf("method %q not found", method)
	}
	f, _ := fn.AsFunction()
	result, err := f.Call(args)
	if err != nil {
		t.Fatalf("call %s: %v", method, err)
	}
	return result
}

// getProp 取对象属性值。
func getProp(t *testing.T, obj engine.Value, key string) engine.Value {
	t.Helper()
	o, ok := obj.AsObject()
	if !ok {
		t.Fatalf("not an object: %v", obj)
	}
	v, _ := o.Get(key)
	return v
}

// === node:path ==========================================================

func TestPathJoin(t *testing.T) {
	ctx := newCtx(t)
	m, _ := NewPath(ctx)

	// join
	got := callMethod(t, m, "join", engine.Str("a"), engine.Str("b"), engine.Str("c"))
	// 跨平台：windows 返回 a\b\c，posix 返回 a/b/c。验证不含 a 且分隔正确。
	s := got.String()
	if s != "a/b/c" && s != `a\b\c` {
		t.Errorf("join = %q", s)
	}

	// join 过滤空与 .
	got = callMethod(t, m, "join", engine.Str("a"), engine.Str(""), engine.Str("b"))
	s = got.String()
	if s != "a/b" && s != `a\b` {
		t.Errorf("join with empty = %q", s)
	}
}

func TestPathDirnameBasename(t *testing.T) {
	ctx := newCtx(t)
	m, _ := NewPath(ctx)

	got := callMethod(t, m, "dirname", engine.Str("/foo/bar/baz.txt"))
	if got.String() != "/foo/bar" && got.String() != `\foo\bar` {
		t.Errorf("dirname = %q", got.String())
	}

	got = callMethod(t, m, "basename", engine.Str("/foo/bar/baz.txt"))
	if got.String() != "baz.txt" {
		t.Errorf("basename = %q", got.String())
	}

	// basename 去扩展名
	got = callMethod(t, m, "basename", engine.Str("/foo/bar/baz.txt"), engine.Str(".txt"))
	if got.String() != "baz" {
		t.Errorf("basename with ext = %q", got.String())
	}
}

func TestPathExtname(t *testing.T) {
	ctx := newCtx(t)
	m, _ := NewPath(ctx)

	cases := []struct {
		input string
		want  string
	}{
		{"file.txt", ".txt"},
		{"file.tar.gz", ".gz"},
		{"file", ""},
		{".bashrc", ""},
	}
	for _, c := range cases {
		got := callMethod(t, m, "extname", engine.Str(c.input))
		if got.String() != c.want {
			t.Errorf("extname(%q) = %q, want %q", c.input, got.String(), c.want)
		}
	}
}

func TestPathPosixWin32(t *testing.T) {
	ctx := newCtx(t)
	m, _ := NewPath(ctx)

	posix := getProp(t, m, "posix")
	win32 := getProp(t, m, "win32")

	// posix.sep 固定为 /
	if getProp(t, posix, "sep").String() != "/" {
		t.Error("posix.sep should be /")
	}
	// win32.sep 固定为 \
	if getProp(t, win32, "sep").String() != `\` {
		t.Error("win32.sep should be \\")
	}

	// posix.join 总是用 /
	got := callMethod(t, posix, "join", engine.Str("a"), engine.Str("b"))
	if got.String() != "a/b" {
		t.Errorf("posix.join = %q, want a/b", got.String())
	}
}

func TestPathParse(t *testing.T) {
	ctx := newCtx(t)
	m, _ := NewPath(ctx)
	posix := getProp(t, m, "posix")

	parsed := callMethod(t, posix, "parse", engine.Str("/foo/bar/file.txt"))
	if getProp(t, parsed, "base").String() != "file.txt" {
		t.Errorf("parse base = %q", getProp(t, parsed, "base").String())
	}
	if getProp(t, parsed, "ext").String() != ".txt" {
		t.Errorf("parse ext = %q", getProp(t, parsed, "ext").String())
	}
	if getProp(t, parsed, "name").String() != "file" {
		t.Errorf("parse name = %q", getProp(t, parsed, "name").String())
	}
	if getProp(t, parsed, "dir").String() != "/foo/bar" {
		t.Errorf("parse dir = %q", getProp(t, parsed, "dir").String())
	}
}

// === node:os ============================================================

func TestOSPlatform(t *testing.T) {
	ctx := newCtx(t)
	m, _ := NewOS(ctx)

	got := callMethod(t, m, "platform")
	s := got.String()
	if s != "win32" && s != "linux" && s != "darwin" {
		t.Errorf("platform = %q, expected win32/linux/darwin", s)
	}

	got = callMethod(t, m, "arch")
	if got.String() == "" {
		t.Error("arch should not be empty")
	}
}

func TestOSCPUs(t *testing.T) {
	ctx := newCtx(t)
	m, _ := NewOS(ctx)

	cpus := callMethod(t, m, "cpus")
	// cpus 返回数组，长度 > 0
	if cpus.Type() != engine.TypeObject {
		t.Fatal("cpus should return array")
	}
	// 验证能取 length
	length := getProp(t, cpus, "length")
	n, _ := length.Int()
	if n <= 0 {
		t.Errorf("cpus().length = %d, want > 0", n)
	}
}

func TestOSEOL(t *testing.T) {
	ctx := newCtx(t)
	m, _ := NewOS(ctx)

	eol := getProp(t, m, "EOL")
	if eol.String() != "\n" && eol.String() != "\r\n" {
		t.Errorf("EOL = %q", eol.String())
	}
}

// === node:url ===========================================================

func TestURLParse(t *testing.T) {
	ctx := newCtx(t)
	m, _ := NewURL(ctx)

	parsed := callMethod(t, m, "parse", engine.Str("https://example.com:8080/path?q=1#hash"))
	if getProp(t, parsed, "protocol").String() != "https:" {
		t.Errorf("protocol = %q", getProp(t, parsed, "protocol").String())
	}
	if getProp(t, parsed, "hostname").String() != "example.com" {
		t.Errorf("hostname = %q", getProp(t, parsed, "hostname").String())
	}
	if getProp(t, parsed, "port").String() != "8080" {
		t.Errorf("port = %q", getProp(t, parsed, "port").String())
	}
	if getProp(t, parsed, "pathname").String() != "/path" {
		t.Errorf("pathname = %q", getProp(t, parsed, "pathname").String())
	}
	if getProp(t, parsed, "search").String() != "q=1" {
		t.Errorf("search = %q", getProp(t, parsed, "search").String())
	}
}

func TestURLResolve(t *testing.T) {
	ctx := newCtx(t)
	m, _ := NewURL(ctx)

	got := callMethod(t, m, "resolve", engine.Str("/a/b/c"), engine.Str("../d"))
	if got.String() != "/a/d" {
		t.Errorf("resolve = %q, want /a/d", got.String())
	}
}

func TestURLFileConversion(t *testing.T) {
	ctx := newCtx(t)
	m, _ := NewURL(ctx)

	// pathToFileURL + fileURLToPath 往返
	got := callMethod(t, m, "pathToFileURL", engine.Str("/tmp/test.js"))
	fileURL := got.String()
	if fileURL == "" {
		t.Error("pathToFileURL returned empty")
	}

	got2 := callMethod(t, m, "fileURLToPath", engine.Str(fileURL))
	if got2.String() == "" {
		t.Error("fileURLToPath returned empty")
	}
}

// === node:util ==========================================================

func TestUtilFormat(t *testing.T) {
	ctx := newCtx(t)
	m, _ := NewUtil(ctx)

	got := callMethod(t, m, "format", engine.Str("count: %d, name: %s"), engine.Number(42), engine.Str("test"))
	if got.String() != "count: 42, name: test" {
		t.Errorf("format = %q", got.String())
	}

	// 无占位符
	got = callMethod(t, m, "format", engine.Str("hello"), engine.Str("world"))
	if got.String() != "hello world" {
		t.Errorf("format no placeholder = %q", got.String())
	}
}

func TestUtilInspect(t *testing.T) {
	ctx := newCtx(t)
	m, _ := NewUtil(ctx)

	arr := engine.NewArray([]engine.Value{engine.IntValue(1), engine.IntValue(2), engine.IntValue(3)})
	got := callMethod(t, m, "inspect", arr)
	if got.String() == "" {
		t.Error("inspect returned empty")
	}
}

func TestUtilTypes(t *testing.T) {
	ctx := newCtx(t)
	m, _ := NewUtil(ctx)

	types := getProp(t, m, "types")

	// isNumber
	got := callMethod(t, types, "isNumber", engine.Number(42))
	if got.String() != "true" {
		t.Errorf("isNumber(42) = %q, want true", got.String())
	}

	// isString
	got = callMethod(t, types, "isString", engine.Str("hi"))
	if got.String() != "true" {
		t.Errorf("isString('hi') = %q, want true", got.String())
	}

	// isNumber 对字符串返回 false
	got = callMethod(t, types, "isNumber", engine.Str("42"))
	if got.String() != "false" {
		t.Errorf("isNumber('42') = %q, want false", got.String())
	}
}

func TestUtilIsDeepStrictEqual(t *testing.T) {
	ctx := newCtx(t)
	m, _ := NewUtil(ctx)

	got := callMethod(t, m, "isDeepStrictEqual", engine.Number(1), engine.Number(1))
	if got.String() != "true" {
		t.Errorf("isDeepStrictEqual(1,1) = %q, want true", got.String())
	}

	got = callMethod(t, m, "isDeepStrictEqual", engine.Number(1), engine.Number(2))
	if got.String() != "false" {
		t.Errorf("isDeepStrictEqual(1,2) = %q, want false", got.String())
	}

	got = callMethod(t, m, "isDeepStrictEqual", engine.Str("a"), engine.Str("a"))
	if got.String() != "true" {
		t.Errorf("isDeepStrictEqual('a','a') = %q, want true", got.String())
	}
}
