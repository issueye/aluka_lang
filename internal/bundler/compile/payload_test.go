package compile

import (
	"bytes"
	"encoding/base64"
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

// TestPackRootDirRoundTrip: RootDir（T2-B4 运行时动态导入的磁盘回退基准）
// 写入 manifest 并可解析还原；未设置时保持空串（旧产物兼容语义）。
func TestPackRootDirRoundTrip(t *testing.T) {
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

	payload, err := PackWithOptions("main.ts", []*EntryData{entry}, nil, nil, PackOptions{RootDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := ParsePayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.RootDir != dir {
		t.Errorf("rootDir = %q, want %q", manifest.RootDir, dir)
	}

	// 不设置 RootDir 时（Pack 兼容入口）字段为零值，解析后为空串。
	payload2, err := Pack("main.ts", []*EntryData{entry}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest2, _, err := ParsePayload(payload2)
	if err != nil {
		t.Fatal(err)
	}
	if manifest2.RootDir != "" {
		t.Errorf("rootDir without option = %q, want empty", manifest2.RootDir)
	}
}

// TestEmbeddedRootDir: NewEmbedded 从 manifest 暴露 RootDir，供运行时
// embedded 未命中时回退文件系统。
func TestEmbeddedRootDir(t *testing.T) {
	payload, err := PackWithOptions("main.ts", nil, nil, nil, PackOptions{RootDir: `C:\proj\src`})
	if err != nil {
		t.Fatal(err)
	}
	manifest, data, err := ParsePayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	emb := NewEmbedded(manifest, data)
	if got := emb.RootDir(); got != `C:\proj\src` {
		t.Errorf("Embedded.RootDir = %q, want C:\\proj\\src", got)
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

// TestPackWebAssetsRoundTrip：--gui 模式内嵌前端资源的打包/解析往返。
func TestPackWebAssetsRoundTrip(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	main := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(main, []byte("export const x = 1;"), 0644); err != nil {
		t.Fatal(err)
	}
	entry, err := CompileFile(vm, main, "main.ts")
	if err != nil {
		t.Fatal(err)
	}

	webAssets := map[string][]byte{
		"index.html":      []byte("<!DOCTYPE html><html><body>hi</body></html>"),
		"assets/app.css":  []byte("body { margin: 0 }"),
		"assets/logo.png": {0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A},
	}

	payload, err := PackWithWebAssets("main.ts", []*EntryData{entry}, nil, nil, webAssets)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := ParsePayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.WebAssets) != 3 {
		t.Fatalf("webAssets = %d entries, want 3", len(manifest.WebAssets))
	}

	// 经运行时挂载接口解码校验内容一致
	decoded, err := decodeWebAssetsForTest(manifest.WebAssets)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range webAssets {
		if got := decoded[path]; !bytes.Equal(got, want) {
			t.Errorf("web asset %q mismatch: got %d bytes, want %d bytes", path, len(got), len(want))
		}
	}

	// 不带 webAssets 的普通产物：字段应为空（omitempty）
	plainPayload, err := Pack("main.ts", []*EntryData{entry}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	plainManifest, _, err := ParsePayload(plainPayload)
	if err != nil {
		t.Fatal(err)
	}
	if len(plainManifest.WebAssets) != 0 {
		t.Errorf("plain payload unexpectedly carries %d web assets", len(plainManifest.WebAssets))
	}
}

// decodeWebAssetsForTest 复刻 gui.MountEmbeddedWebAssets 的 base64 解码
// （compile 包不便反向依赖 gui，避免引入 UI 层依赖）。
func decodeWebAssetsForTest(webAssets map[string]string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(webAssets))
	for path, b64 := range webAssets {
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, err
		}
		out[path] = data
	}
	return out, nil
}

// TestPackIconRoundTrip：--icon 内嵌应用图标的打包/解析往返。
func TestPackIconRoundTrip(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	main := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(main, []byte("export const x = 1;"), 0644); err != nil {
		t.Fatal(err)
	}
	entry, err := CompileFile(vm, main, "main.ts")
	if err != nil {
		t.Fatal(err)
	}

	// 最小合法 .ico：ICONDIR + 1 目录项 + 图像数据
	ico := []byte{0, 0, 0, 1, 0, 1,
		16, 16, 0, 0, 1, 0, 32, 0,
		4, 0, 0, 0, 22, 0, 0, 0,
		0xDE, 0xAD, 0xBE, 0xEF}

	payload, err := PackWithOptions("main.ts", []*EntryData{entry}, nil, nil, PackOptions{Icon: ico})
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := ParsePayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	got, err := base64.StdEncoding.DecodeString(manifest.Icon)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, ico) {
		t.Errorf("icon roundtrip mismatch: got %d bytes, want %d", len(got), len(ico))
	}
}
