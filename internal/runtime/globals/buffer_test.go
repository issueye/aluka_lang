package globals

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// newBufferTestContext 创建带 Buffer/Encoding 全局的 VM 测试上下文。
func newBufferTestContext(t *testing.T) engine.Context {
	t.Helper()
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	t.Cleanup(func() { ctx.Close() })
	if err := NewBuffer(ctx, BufferConfig{}); err != nil {
		t.Fatalf("NewBuffer: %v", err)
	}
	if err := NewEncoding(ctx, EncodingConfig{}); err != nil {
		t.Fatalf("NewEncoding: %v", err)
	}
	_ = ctx.Global().Set("globalThis", ctx.Global())
	return ctx
}

// evalGet 执行代码并读取全局键的字符串值。
func evalGet(t *testing.T, ctx engine.Context, code, key string) string {
	t.Helper()
	if _, err := ctx.Eval(code, "buffer_test.js"); err != nil {
		t.Fatalf("Eval: %v", err)
	}
	v, _ := ctx.Global().Get(key)
	return v.String()
}

// TestBufferFromString 验证 Buffer.from 字符串/编码转换。
func TestBufferFromString(t *testing.T) {
	ctx := newBufferTestContext(t)
	if got := evalGet(t, ctx, `
var b = Buffer.from('hello');
globalThis.__r = b.toString() + ':' + b.length;
`, "__r"); got != "hello:5" {
		t.Errorf("from utf8 = %q, want hello:5", got)
	}
	if got := evalGet(t, ctx, `
var b = Buffer.from('68656c6c6f', 'hex');
globalThis.__r = b.toString();
`, "__r"); got != "hello" {
		t.Errorf("from hex = %q, want hello", got)
	}
	if got := evalGet(t, ctx, `
var b = Buffer.from('aGVsbG8=', 'base64');
globalThis.__r = b.toString();
`, "__r"); got != "hello" {
		t.Errorf("from base64 = %q, want hello", got)
	}
}

// TestBufferFromArray 验证 Buffer.from 字节数组。
func TestBufferFromArray(t *testing.T) {
	ctx := newBufferTestContext(t)
	got := evalGet(t, ctx, `
var b = Buffer.from([0x68, 0x69]);
globalThis.__r = b.toString() + ':' + b.length;
`, "__r")
	if got != "hi:2" {
		t.Errorf("from array = %q, want hi:2", got)
	}
}

// TestBufferIndexAccess 验证数字索引读写与只读 length。
func TestBufferIndexAccess(t *testing.T) {
	ctx := newBufferTestContext(t)
	got := evalGet(t, ctx, `
var b = Buffer.from([65, 66, 67]);
b[1] = 90;
globalThis.__r = b[0] + ':' + b[1] + ':' + b[2];
`, "__r")
	if got != "65:90:67" {
		t.Errorf("index access = %q, want 65:90:67", got)
	}
	got = evalGet(t, ctx, `
var b = Buffer.from('abc');
b.length = 100;
globalThis.__r = b.length;
`, "__r")
	if got != "3" {
		t.Errorf("length readonly = %q, want 3", got)
	}
}

// TestBufferAlloc 验证 alloc 与 fill。
func TestBufferAlloc(t *testing.T) {
	ctx := newBufferTestContext(t)
	got := evalGet(t, ctx, `
var b = Buffer.alloc(4, 0x41);
globalThis.__r = b.length + ':' + b.toString();
`, "__r")
	if got != "4:AAAA" {
		t.Errorf("alloc = %q, want 4:AAAA", got)
	}
}

// TestBufferWrite 验证 write 与 toString 切片。
func TestBufferWrite(t *testing.T) {
	ctx := newBufferTestContext(t)
	got := evalGet(t, ctx, `
var b = Buffer.alloc(10);
var n = b.write('hello', 0, 'utf8');
globalThis.__r = n + ':' + b.toString(undefined, 0, 5);
`, "__r")
	if got != "5:hello" {
		t.Errorf("write = %q, want 5:hello", got)
	}
}

// TestBufferReadWriteUInt 验证整型读写（大小端）。
func TestBufferReadWriteUInt(t *testing.T) {
	ctx := newBufferTestContext(t)
	got := evalGet(t, ctx, `
var b = Buffer.from([0x12, 0x34, 0x56, 0x78]);
globalThis.__r = b.readUInt8(0) + ':' + b.readUInt16BE(0) + ':' + b.readUInt16LE(0);
`, "__r")
	if got != "18:4660:13330" {
		t.Errorf("read uint = %q, want 18:4660:13330", got)
	}
	got = evalGet(t, ctx, `
var b = Buffer.alloc(2);
b.writeUInt16BE(0x1234, 0);
globalThis.__r = b[0] + ':' + b[1];
`, "__r")
	if got != "18:52" {
		t.Errorf("write uint16BE = %q, want 18:52", got)
	}
}

// TestBufferSlice 验证 slice/subarray 共享底层。
func TestBufferSlice(t *testing.T) {
	ctx := newBufferTestContext(t)
	got := evalGet(t, ctx, `
var b = Buffer.from('abcdef');
var s = b.slice(1, 3);
globalThis.__r = s.toString();
`, "__r")
	if got != "bc" {
		t.Errorf("slice = %q, want bc", got)
	}
}

// TestBufferStaticMethods 验证 byteLength/concat/isBuffer/compare/isEncoding。
func TestBufferStaticMethods(t *testing.T) {
	ctx := newBufferTestContext(t)
	got := evalGet(t, ctx, `
globalThis.__r = Buffer.byteLength('héllo') + ':' +
  Buffer.concat([Buffer.from('ab'), Buffer.from('cd')]).toString() + ':' +
  Buffer.isBuffer(Buffer.from('x')) + ':' + Buffer.isBuffer('x') + ':' +
  Buffer.compare(Buffer.from('a'), Buffer.from('b')) + ':' +
  Buffer.isEncoding('base64');
`, "__r")
	if got != "6:abcd:true:false:-1:true" {
		t.Errorf("static = %q, want 6:abcd:true:false:-1:true", got)
	}
}

// TestBufferEqualsCopy 验证 equals/copy/fill。
func TestBufferEqualsCopy(t *testing.T) {
	ctx := newBufferTestContext(t)
	got := evalGet(t, ctx, `
var a = Buffer.from('abc');
var b = Buffer.from('abc');
var c = Buffer.alloc(3);
a.copy(c, 0, 0, 3);
globalThis.__r = a.equals(b) + ':' + c.toString() + ':' + a.fill(0x78).toString();
`, "__r")
	if got != "true:abc:xxx" {
		t.Errorf("equals/copy/fill = %q, want true:abc:xxx", got)
	}
}

// TestTextEncoderDecoder 验证 TextEncoder/TextDecoder。
func TestTextEncoderDecoder(t *testing.T) {
	ctx := newBufferTestContext(t)
	got := evalGet(t, ctx, `
var te = new TextEncoder();
var b = te.encode('héllo');
globalThis.__r = b.length + ':' + b.toString() + ':' + te.encoding;
`, "__r")
	if got != "6:héllo:utf-8" {
		t.Errorf("encode = %q, want 6:héllo:utf-8", got)
	}
	got = evalGet(t, ctx, `
var td = new TextDecoder();
var s = td.decode(Buffer.from([0x68, 0x69]));
globalThis.__r = s + ':' + td.encoding;
`, "__r")
	if got != "hi:utf-8" {
		t.Errorf("decode = %q, want hi:utf-8", got)
	}
	got = evalGet(t, ctx, `
var td = new TextDecoder('utf-16le');
var s = td.decode(Buffer.from([0x68, 0x00, 0x69, 0x00]));
globalThis.__r = s;
`, "__r")
	if got != "hi" {
		t.Errorf("decode utf16le = %q, want hi", got)
	}
}

// TestAtobBtoa 验证 base64 全局函数往返。
func TestAtobBtoa(t *testing.T) {
	ctx := newBufferTestContext(t)
	got := evalGet(t, ctx, `
globalThis.__r = btoa('hello') + ':' + atob('aGVsbG8=');
`, "__r")
	if got != "aGVsbG8=:hello" {
		t.Errorf("atob/btoa = %q, want aGVsbG8=:hello", got)
	}
}
