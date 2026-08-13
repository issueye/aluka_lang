package compile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// writeTempFile 在临时目录写入文件并返回绝对路径。
func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestCompileExplicitExtensionsDoNotCross：显式扩展永不跨模块协议——
// .mjs 无 ESM 声明仍 ESM；.cjs/.cts 含 ESM 声明报明确诊断（不偷偷回退 ESM）。
func TestCompileExplicitExtensionsDoNotCross(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	esmCases := []struct {
		name    string
		content string
	}{
		{"plain.mjs", "const x = 1;"}, // 无声明，.mjs 强制 ESM
		{"plain.MJS", "const x = 1;"}, // 大写扩展，大小写规范化
	}
	for _, tc := range esmCases {
		p := writeTempFile(t, dir, tc.name, tc.content)
		entry, err := CompileFile(vm, p, tc.name)
		if err != nil {
			t.Fatalf("CompileFile(%s): %v", tc.name, err)
		}
		if entry.ModuleType != ModuleTypeESM {
			t.Errorf("CompileFile(%s) ModuleType = %q, want esm", tc.name, entry.ModuleType)
		}
	}
	cjsCases := []struct {
		name    string
		content string
	}{
		{"decl.cjs", "export const x = 1;"},
		{"decl.CTS", "export const x = 1;"},
	}
	for _, tc := range cjsCases {
		p := writeTempFile(t, dir, tc.name, tc.content)
		if _, err := CompileFile(vm, p, tc.name); err == nil {
			t.Fatalf("CompileFile(%s) = nil error, want explicit CJS/ESM diagnostic", tc.name)
		}
	}
}

// TestCompileImplicitJSTSOneTimePromotion：隐式 .ts/.js 仅允许一次语法提升；
// 提升后 ModuleKind 记录为 ESM，SourceKind 保留 TS/JS。
func TestCompileImplicitJSTSOneTimePromotion(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// .ts 无 ESM 声明（默认 package type commonjs）→ CJS。
	cjs := writeTempFile(t, dir, "plain.ts", "const x = 1;")
	entry, err := CompileFile(vm, cjs, "plain.ts")
	if err != nil {
		t.Fatal(err)
	}
	if entry.ModuleType != ModuleTypeCJS || entry.ModuleKind != module.ModuleCommonJS {
		t.Errorf("plain.ts = %s/%s, want cjs/cjs", entry.ModuleType, entry.ModuleKind)
	}
	if entry.SourceKind != module.SourceTypeScript {
		t.Errorf("plain.ts SourceKind = %s, want typescript", entry.SourceKind)
	}
	// .ts 含 ESM 声明 → 一次提升为 ESM。
	esm := writeTempFile(t, dir, "decl.ts", "export const x = 1;")
	entry, err = CompileFile(vm, esm, "decl.ts")
	if err != nil {
		t.Fatal(err)
	}
	if entry.ModuleType != ModuleTypeESM || entry.ModuleKind != module.ModuleESM {
		t.Errorf("decl.ts = %s/%s, want esm/esm", entry.ModuleType, entry.ModuleKind)
	}
}

// TestCompileTSDiagnosticMatchesRuntime：.ts 中的非 declare enum 在编译期
// 即报 ERR_UNSUPPORTED_TYPESCRIPT_SYNTAX，与 runtime loader 一致。
func TestCompileTSDiagnosticMatchesRuntime(t *testing.T) {
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	enumFile := writeTempFile(t, dir, "bad.ts", "enum E { A, B }")
	if _, err := CompileFile(vm, enumFile, "bad.ts"); err == nil {
		t.Fatal("CompileFile(bad.ts with enum) = nil error, want ERR_UNSUPPORTED_TYPESCRIPT_SYNTAX")
	}
	// declare enum 属于环境声明，允许。
	okFile := writeTempFile(t, dir, "ok.ts", "declare enum E { A, B } const x = 1;")
	if _, err := CompileFile(vm, okFile, "ok.ts"); err != nil {
		t.Fatalf("CompileFile(ok.ts with declare enum) error: %v", err)
	}
}
