package globals

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

func newCompressionTestContext(t *testing.T) engine.Context {
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
	if err := NewStream(ctx, StreamConfig{}); err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	_ = ctx.Global().Set("globalThis", ctx.Global())
	return ctx
}

// TestCompressionStreamGzip 测试 Gzip 压缩与解压缩流
func TestCompressionStreamGzip(t *testing.T) {
	ctx := newCompressionTestContext(t)

	src := `
		globalThis.pass = false;
		async function test() {
			var originalText = "Aluka Native Pure Go JavaScript Runtime • Compression Stream Test";
			
			// 1. 压缩流
			var cs = new CompressionStream("gzip");
			var writer = cs.writable.getWriter();
			await writer.write(new TextEncoder().encode(originalText));
			await writer.close();

			var reader = cs.readable.getReader();
			var compressedChunk = (await reader.read()).value;

			// 2. 解压缩流
			var ds = new DecompressionStream("gzip");
			var dsWriter = ds.writable.getWriter();
			await dsWriter.write(compressedChunk);
			await dsWriter.close();

			var dsReader = ds.readable.getReader();
			var decompressedChunk = (await dsReader.read()).value;
			var decompressedText = new TextDecoder().decode(decompressedChunk);

			if (decompressedText === originalText) {
				globalThis.pass = true;
			}
		}
		test();
	`
	if err := subtleRun(t, ctx, src); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	v, err := ctx.Global().Get("pass")
	if err != nil || v.String() != "true" {
		t.Fatalf("CompressionStream gzip failed, pass=%v", v)
	}
}

// TestCompressionStreamDeflate 测试 Deflate 压缩与解压缩流
func TestCompressionStreamDeflate(t *testing.T) {
	ctx := newCompressionTestContext(t)

	src := `
		globalThis.pass = false;
		async function test() {
			var originalText = "Deflate stream test payload 1234567890";
			
			// 1. 压缩流
			var cs = new CompressionStream("deflate");
			var writer = cs.writable.getWriter();
			await writer.write(new TextEncoder().encode(originalText));
			await writer.close();

			var reader = cs.readable.getReader();
			var compressedChunk = (await reader.read()).value;

			// 2. 解压缩流
			var ds = new DecompressionStream("deflate");
			var dsWriter = ds.writable.getWriter();
			await dsWriter.write(compressedChunk);
			await dsWriter.close();

			var dsReader = ds.readable.getReader();
			var decompressedChunk = (await dsReader.read()).value;
			var decompressedText = new TextDecoder().decode(decompressedChunk);

			if (decompressedText === originalText) {
				globalThis.pass = true;
			}
		}
		test();
	`
	if err := subtleRun(t, ctx, src); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	v, err := ctx.Global().Get("pass")
	if err != nil || v.String() != "true" {
		t.Fatalf("CompressionStream deflate failed, pass=%v", v)
	}
}
