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

func TestDiagnosticsChannelPublishAndUnsubscribe(t *testing.T) {
	ctx := newCtx(t)
	mod, err := NewDiagnosticsChannel(ctx)
	if err != nil {
		t.Fatal(err)
	}

	first := callMethod(t, mod, "channel", engine.Str("aluka:test"))
	second := callMethod(t, mod, "channel", engine.Str("aluka:test"))
	if first != second {
		t.Fatal("channel(name) should return the cached channel object")
	}

	var message, channelName string
	subscriber := engine.NewFunction("subscriber", func(args []engine.Value) (engine.Value, error) {
		message = args[0].String()
		channelName = args[1].String()
		return engine.Undefined(), nil
	})
	callMethod(t, first, "subscribe", subscriber)
	if got := getProp(t, first, "hasSubscribers").String(); got != "true" {
		t.Fatalf("hasSubscribers after subscribe = %s, want true", got)
	}
	callMethod(t, first, "publish", engine.Str("payload"))
	if message != "payload" || channelName != "aluka:test" {
		t.Fatalf("subscriber got message=%q name=%q", message, channelName)
	}
	if got := callMethod(t, mod, "unsubscribe", engine.Str("aluka:test"), subscriber).String(); got != "true" {
		t.Fatalf("unsubscribe = %s, want true", got)
	}
	if got := getProp(t, first, "hasSubscribers").String(); got != "false" {
		t.Fatalf("hasSubscribers after unsubscribe = %s, want false", got)
	}
}

func TestVMModuleSurface(t *testing.T) {
	ctx := newCtx(t)
	mod, err := NewVMModule(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"runInThisContext", "runInNewContext", "runInContext", "compileFunction", "Script", "createContext", "isContext"} {
		if got := getProp(t, mod, name); !got.IsFunction() {
			t.Fatalf("%s type = %s, want function", name, got.Type())
		}
	}
	// M6-1：runInThisContext 已实现动态求值（不再是不可用的占位）。
	fn, _ := getProp(t, mod, "runInThisContext").AsFunction()
	res, err := fn.Call([]engine.Value{engine.Str("1 + 1")})
	if err != nil {
		t.Fatalf("runInThisContext('1 + 1') error: %v", err)
	}
	if res.String() != "2" {
		t.Fatalf("runInThisContext('1 + 1') = %q, want 2", res.String())
	}
	// M6-1：context 隔离——runInNewContext 的全局不泄漏到宿主。
	runNew, _ := getProp(t, mod, "runInNewContext").AsFunction()
	secretVal, err := runNew.Call([]engine.Value{engine.Str("globalThis.__vm_secret = 's1'")})
	if err != nil {
		t.Fatalf("runInNewContext error: %v", err)
	}
	if secretVal.String() != "s1" {
		t.Fatalf("runInNewContext returned %q, want s1", secretVal.String())
	}
	if got := getProp(t, ctx.Global(), "__vm_secret"); got.Type().String() != "undefined" {
		t.Fatalf("vm context global leaked to host: %s", got.String())
	}
}

func TestDiagnosticsTracingChannel(t *testing.T) {
	ctx := newCtx(t)
	mod, err := NewDiagnosticsChannel(ctx)
	if err != nil {
		t.Fatal(err)
	}

	tracing := callMethod(t, mod, "tracingChannel", engine.Str("aluka:test"))
	if got := getProp(t, tracing, "hasSubscribers").String(); got != "false" {
		t.Fatalf("initial tracing hasSubscribers = %s", got)
	}
	start := getProp(t, tracing, "start")
	callMethod(t, start, "subscribe", engine.NewFunction("subscriber", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	if got := getProp(t, tracing, "hasSubscribers").String(); got != "true" {
		t.Fatalf("subscribed tracing hasSubscribers = %s", got)
	}
	result := callMethod(t, tracing, "tracePromise", engine.NewFunction("work", func(args []engine.Value) (engine.Value, error) {
		return engine.Str("done"), nil
	}), engine.NewObject())
	if result.String() != "done" {
		t.Fatalf("tracePromise result = %q", result.String())
	}
}

func TestConstantsBuiltinAliases(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var legacy = require('constants');
var modern = require('node:constants');
globalThis.__r = (legacy === modern) + ':' + legacy.F_OK + ':' + (typeof legacy.O_RDONLY);
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "true:0:number" {
		t.Errorf("constants aliases = %q", got)
	}
}

func TestUtilDebuglogEnabled(t *testing.T) {
	t.Setenv("NODE_DEBUG", "http,undici*")
	ctx := newCtx(t)
	mod, err := NewUtil(ctx)
	if err != nil {
		t.Fatal(err)
	}

	undici := callMethod(t, mod, "debuglog", engine.Str("undici"))
	if !undici.IsFunction() || getProp(t, undici, "enabled").String() != "true" {
		t.Fatal("debuglog(undici) should return an enabled function")
	}
	other := callMethod(t, mod, "debuglog", engine.Str("other"))
	if getProp(t, other, "enabled").String() != "false" {
		t.Fatal("debuglog(other) should be disabled")
	}
}

func TestUtilTypesArrayBufferPredicates(t *testing.T) {
	ctx := newCtx(t)
	types, err := NewUtilTypes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	buffer := engine.NewArrayBuffer([]byte{1, 2})
	uint8 := engine.NewTypedArrayValue(engine.KindUint8, []byte{1, 2})
	int16 := engine.NewTypedArrayValue(engine.KindInt16, make([]byte, 2))

	if got := callMethod(t, types, "isArrayBuffer", buffer).String(); got != "true" {
		t.Fatalf("isArrayBuffer(ArrayBuffer) = %s, want true", got)
	}
	if got := callMethod(t, types, "isUint8Array", uint8).String(); got != "true" {
		t.Fatalf("isUint8Array(Uint8Array) = %s, want true", got)
	}
	if got := callMethod(t, types, "isUint8Array", int16).String(); got != "false" {
		t.Fatalf("isUint8Array(Int16Array) = %s, want false", got)
	}
	if got := callMethod(t, types, "isArrayBufferView", int16).String(); got != "true" {
		t.Fatalf("isArrayBufferView(Int16Array) = %s, want true", got)
	}
}

func TestEventsExportsEventEmitterConstructor(t *testing.T) {
	ctx := newCtx(t)
	mod, err := NewEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !mod.IsFunction() {
		t.Fatal("node:events default export should be the EventEmitter constructor")
	}
	if got := getProp(t, mod, "EventEmitter"); got != mod {
		t.Fatal("node:events.EventEmitter should equal the default export")
	}
	if !getProp(t, mod, "once").IsFunction() || !getProp(t, mod, "on").IsFunction() {
		t.Fatal("EventEmitter static methods should be exposed on the default export")
	}
}

func TestStreamWebExportsGlobalConstructors(t *testing.T) {
	ctx := newCtx(t)
	if err := ctx.Global().Set("ReadableStream", engine.NewFunction("ReadableStream", func(args []engine.Value) (engine.Value, error) {
		return engine.NewObject(), nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Global().Set("WritableStream", engine.NewFunction("WritableStream", func(args []engine.Value) (engine.Value, error) {
		return engine.NewObject(), nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Global().Set("TransformStream", engine.NewFunction("TransformStream", func(args []engine.Value) (engine.Value, error) {
		return engine.NewObject(), nil
	})); err != nil {
		t.Fatal(err)
	}
	mod, err := NewStreamWeb(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if getProp(t, mod, "ReadableStream") != getProp(t, ctx.Global(), "ReadableStream") {
		t.Fatal("stream/web should reuse global ReadableStream constructor")
	}
	if !getProp(t, mod, "ReadableStreamTee").IsFunction() {
		t.Fatal("stream/web.ReadableStreamTee should be callable")
	}
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

// TestAssertStrictModule：node:assert/strict 子路径（Pi conformance.ts 依赖）：
// 解构 deepStrictEqual/ok/rejects/strictEqual 可用（Node 语义 ≡ assert.strict）。
func TestAssertStrictModule(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var { deepStrictEqual, ok, rejects, strictEqual } = require('node:assert/strict');
strictEqual(1 + 1, 2);
deepStrictEqual({ a: 1 }, { a: 1 });
ok(true);
rejects(Promise.reject(new Error('boom')), /boom/).then(function() {
  rejects(async function() { throw new Error('x'); }).then(function() {
    globalThis.__r = 'all-ok';
  });
});
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "all-ok" {
		t.Errorf("assert/strict = %q, want all-ok", got)
	}
}

// TestAssertRejectsDoesNotReject：rejects/doesNotReject 语义（fulfill 时
// rejects 失败、reject 时 doesNotReject 失败）。
func TestAssertRejectsDoesNotReject(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var { rejects, doesNotReject } = require('node:assert/strict');
var bad1 = rejects(Promise.resolve(1));
var bad2 = doesNotReject(Promise.reject(new Error('nope')));
Promise.all([bad1, bad2]).then(
  function() { globalThis.__r = 'unexpected'; },
  function() { globalThis.__r = 'both-rejected-as-expected'; }
);
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "both-rejected-as-expected" {
		t.Errorf("rejects negative cases = %q", got)
	}
}

// TestStreamPromisesPipeline：node:stream/promises.pipeline 完成并传数据。
func TestStreamPromisesPipeline(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var { pipeline } = require('node:stream/promises');
var { Readable, Writable } = require('node:stream');
async function main() {
  var out = '';
  var src = new Readable();
  var dst = new Writable({ write: function(c, e, cb) { out += c.toString(); cb(); } });
  src.push('hello');
  src.push(null);
  await pipeline(src, dst);
  globalThis.__r = out;
}
main();
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "hello" {
		t.Errorf("pipeline = %q, want hello", got)
	}
}

// TestReadableFromWebPipeline：Readable.fromWeb + pipeline（Pi tools-manager 模式）。
func TestReadableFromWebPipeline(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var { pipeline } = require('node:stream/promises');
var { Readable, Writable } = require('node:stream');
async function main() {
  var web = new ReadableStream({
    start: function(c) { c.enqueue('web1'); c.enqueue('web2'); c.close(); }
  });
  var out = '';
  var dst = new Writable({ write: function(c, e, cb) { out += c.toString(); cb(); } });
  await pipeline(Readable.fromWeb(web), dst);
  globalThis.__r = out;
}
main();
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "web1web2" {
		t.Errorf("fromWeb pipeline = %q, want web1web2", got)
	}
}

// TestReadableAsyncIterator：for await...of 流迭代（N22-A1）。
func TestReadableAsyncIterator(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var { Readable } = require('node:stream');
async function main() {
  var src = new Readable();
  src.push('a');
  src.push('b');
  src.push(null);
  var out = '';
  for await (var c of src) { out += c; }
  globalThis.__r = out;
}
main();
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "ab" {
		t.Errorf("for-await stream = %q, want ab", got)
	}
}

// TestReadableAsyncIteratorAsyncData：异步数据到达 + 等待中的 next() 唤醒。
func TestReadableAsyncIteratorAsyncData(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var { Readable } = require('node:stream');
async function main() {
  var src = new Readable();
  setTimeout(function() { src.push('x'); src.push(null); }, 10);
  var out = '';
  for await (var c of src) { out += c; }
  globalThis.__r = out;
}
main();
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "x" {
		t.Errorf("async for-await = %q, want x", got)
	}
}
