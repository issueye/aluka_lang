package globals

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gstream"
)

// newStreamTestContext 创建带 Streams 全局的 VM 上下文。
func newStreamTestContext(t *testing.T) engine.Context {
	t.Helper()
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	t.Cleanup(func() { ctx.Close() })
	if err := gstream.NewStream(ctx, gstream.StreamConfig{}); err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	return ctx
}

// TestTransformStreamWriteRead 回归测试（P1-1）：TransformStream 经
// getWriter().write() 写入的数据能流向 readable 端，close 后 read 返回 done。
func TestTransformStreamWriteRead(t *testing.T) {
	ctx := newStreamTestContext(t)
	code := `
(async function() {
  var t = new TransformStream();
  var w = t.writable.getWriter();
  await w.write(5);
  await w.write(6);
  await w.close();
  var r = t.readable.getReader();
  var a = await r.read();
  var b = await r.read();
  var c = await r.read();
  globalThis.__r = a.value + "|" + b.value + "|" + c.done;
})();
`
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got := webGlobalGet(ctx, "__r"); got != "5|6|true" {
		t.Errorf("TransformStream = %q, want 5|6|true", got)
	}
}

// TestTransformStreamCustom 回归测试：自定义 transform 回调对数据做变换。
func TestTransformStreamCustom(t *testing.T) {
	ctx := newStreamTestContext(t)
	code := `
(async function() {
  var t = new TransformStream({ transform: function(chunk, ctrl) { ctrl.enqueue(chunk * 2); } });
  var w = t.writable.getWriter();
  await w.write(21);
  var r = await t.readable.getReader().read();
  globalThis.__r = r.value;
})();
`
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got := webGlobalGet(ctx, "__r"); got != "42" {
		t.Errorf("TransformStream custom = %q, want 42", got)
	}
}

// TestReadableStreamBasic 验证 ReadableStream 构造与读取。
func TestReadableStreamBasic(t *testing.T) {
	ctx := newStreamTestContext(t)
	code := `
(async function() {
  var s = new ReadableStream({ start: function(c) { c.enqueue(1); c.enqueue(2); c.close(); } });
  var r = s.getReader();
  var a = await r.read();
  var b = await r.read();
  var c = await r.read();
  globalThis.__r = a.value + "|" + b.value + "|" + c.done;
})();
`
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got := webGlobalGet(ctx, "__r"); got != "1|2|true" {
		t.Errorf("ReadableStream = %q, want 1|2|true", got)
	}
}
