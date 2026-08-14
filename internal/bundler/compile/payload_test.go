package compile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// TestCompileFileCJS：无 import/export 的 .ts 按 CJS 编译（与 loader 判定一致）。
func TestCompileFileCJS(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	hello := filepath.Join(dir, "hello.ts")
	if err := os.WriteFile(hello, []byte("console.log('hi');"), 0644); err != nil {
		t.Fatal(err)
	}
	entry, err := CompileFile(vm, hello, "hello.ts")
	if err != nil {
		t.Fatal(err)
	}
	if entry.ModuleType != ModuleTypeCJS {
		t.Errorf("moduleType = %q, want cjs (no import/export)", entry.ModuleType)
	}
	if len(entry.Module.Functions) == 0 {
		t.Error("compiled module has no functions")
	}
}

// TestCompileFileESM：含 import/export 的 .ts 按 ESM 编译。
func TestCompileFileESM(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	main := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(main, []byte("export const x = 42;"), 0644); err != nil {
		t.Fatal(err)
	}
	entry, err := CompileFile(vm, main, "main.ts")
	if err != nil {
		t.Fatal(err)
	}
	if entry.ModuleType != ModuleTypeESM {
		t.Errorf("moduleType = %q, want esm", entry.ModuleType)
	}
}

// TestPackParseRoundTrip：打包 → 解析 → 模块读取往返。
func TestPackParseRoundTrip(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	main := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(main, []byte("export const x = 42;"), 0644); err != nil {
		t.Fatal(err)
	}
	entry, err := CompileFile(vm, main, "main.ts")
	if err != nil {
		t.Fatal(err)
	}

	payload, err := Pack("main.ts", []*EntryData{entry}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, data, err := ParsePayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Entry != "main.ts" {
		t.Errorf("entry = %q, want main.ts (module key)", manifest.Entry)
	}
	if manifest.FormatVersion == 0 {
		t.Error("formatVersion not recorded")
	}
	if len(manifest.Modules) != 1 {
		t.Fatalf("modules = %d, want 1", len(manifest.Modules))
	}
	if got := manifest.ModuleTypeOf("main.ts"); got != ModuleTypeESM {
		t.Errorf("moduleType = %q, want esm", got)
	}
	// P1-4/P3-3：SourceKind/ModuleKind 分类上下文持久化并恢复。
	if got := manifest.SourceKindOf("main.ts"); got != "typescript" {
		t.Errorf("sourceKind = %q, want typescript", got)
	}
	if got := manifest.ModuleKindOf("main.ts"); got != "esm" {
		t.Errorf("moduleKind = %q, want esm", got)
	}
	mod, err := manifest.LoadModule(data, "main.ts")
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.Functions) == 0 {
		t.Error("deserialized module has no functions")
	}
	// 未找到的模块应报错。
	if _, err := manifest.LoadModule(data, "nope.ts"); err == nil {
		t.Error("missing module did not error")
	}
}

func TestPayloadCompression(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var entries []*EntryData
	for i := 0; i < 50; i++ {
		p := filepath.Join(dir, fmt.Sprintf("mod%d.ts", i))
		src := fmt.Sprintf("export function f%d(x) { return x + %d; }", i, i)
		if err := os.WriteFile(p, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}
		entry, err := CompileFile(vm, p, fmt.Sprintf("mod%d.ts", i))
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}

	payload, err := Pack("mod0.ts", entries, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, data, err := ParsePayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Modules) != 50 {
		t.Fatalf("modules = %d, want 50", len(manifest.Modules))
	}
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("mod%d.ts", i)
		mod, err := manifest.LoadModule(data, key)
		if err != nil {
			t.Fatalf("LoadModule(%q): %v", key, err)
		}
		if len(mod.Functions) == 0 {
			t.Fatalf("mod %q has no functions", key)
		}
	}
}

func TestParsePayloadBadMagic(t *testing.T) {
	// 长度合法但 magic 错误（≥ headerSize 的非 payload 数据）。
	_, _, err := ParsePayload([]byte("THIS-IS-NOT-ALUKA-PAYLOAD-BYTES-XXXX"))
	if err == nil || !strings.Contains(err.Error(), "magic") {
		t.Errorf("bad magic error = %v, want magic mismatch", err)
	}
}

// TestParsePayloadVersionMismatch：PayloadVersion 不匹配应报错。
func TestParsePayloadVersionMismatch(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	main := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(main, []byte("1;"), 0644); err != nil {
		t.Fatal(err)
	}
	entry, err := CompileFile(vm, main, "main.ts")
	if err != nil {
		t.Fatal(err)
	}
	orig, err := Pack("main.ts", []*EntryData{entry}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Clone(orig)
	tampered[8] = 99 // 修改 PayloadVersion（header：magic[0:8] | version[8:12]）
	_, _, err = ParsePayload(tampered)
	if err == nil || !strings.Contains(err.Error(), "version mismatch") {
		t.Errorf("version mismatch error = %v", err)
	}
}

// TestFooterRoundTrip：footer 构造/解析/校验和。
func TestFooterRoundTrip(t *testing.T) {
	payload := []byte("some-payload-bytes")
	footer := MakeFooter(12345, uint64(len(payload)), payload)
	if len(footer) != FooterSize {
		t.Fatalf("footer size = %d, want %d", len(footer), FooterSize)
	}
	offset, length, sum, ok := ParseFooter(footer)
	if !ok {
		t.Fatal("ParseFooter failed")
	}
	if offset != 12345 || length != uint64(len(payload)) {
		t.Errorf("offset/len = %d/%d, want 12345/%d", offset, length, len(payload))
	}
	if !VerifyPayload(payload, sum) {
		t.Error("sha256 mismatch")
	}
	if VerifyPayload([]byte("tampered"), sum) {
		t.Error("tampered payload passed verification")
	}
	if _, _, _, ok := ParseFooter([]byte("not-a-footer")); ok {
		t.Error("non-footer parsed as footer")
	}
}
