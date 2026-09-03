package module

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSourceModuleKindMatrix：扩展名 × package type 的分类矩阵。
func TestSourceModuleKindMatrix(t *testing.T) {
	dir := t.TempDir()
	// 无 package.json 的目录：.js/.ts 默认 commonjs。
	r := NewResolver()
	cases := []struct {
		name string
		want ModuleKind
	}{
		{"a.mjs", ModuleESM},
		{"a.MJS", ModuleESM},
		{"a.mts", ModuleESM},
		{"a.MTS", ModuleESM},
		{"a.cjs", ModuleCommonJS},
		{"a.CJS", ModuleCommonJS},
		{"a.cts", ModuleCommonJS},
		{"a.CTS", ModuleCommonJS},
		{"a.js", ModuleCommonJS}, // 无 package.json → commonjs
		{"a.ts", ModuleCommonJS},
	}
	for _, tc := range cases {
		p := filepath.Join(dir, tc.name)
		if got := r.SourceModuleKind(p); got != tc.want {
			t.Errorf("SourceModuleKind(%s) = %s, want %s", tc.name, got, tc.want)
		}
	}

	// package.json type=module：.js/.ts 升为 ESM，显式 .cjs/.cts 不受影响。
	modDir := filepath.Join(dir, "mod")
	if err := os.MkdirAll(modDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modDir, "package.json"), []byte(`{"type":"module"}`), 0644); err != nil {
		t.Fatal(err)
	}
	modCases := []struct {
		name string
		want ModuleKind
	}{
		{"a.js", ModuleESM},
		{"a.ts", ModuleESM},
		{"a.mjs", ModuleESM},
		{"a.cjs", ModuleCommonJS}, // 显式扩展优先于 package type
		{"a.cts", ModuleCommonJS},
	}
	for _, tc := range modCases {
		p := filepath.Join(modDir, tc.name)
		if got := r.SourceModuleKind(p); got != tc.want {
			t.Errorf("module-dir SourceModuleKind(%s) = %s, want %s", tc.name, got, tc.want)
		}
	}
}

// TestParseSourceUnitTSPolicy：TS strip-only 诊断在 ParseSourceUnit 统一执行，
// 且大小写扩展同样生效；JS 源文件不触发 TS 诊断。
func TestParseSourceUnitTSPolicy(t *testing.T) {
	bad := `enum E { A, B } const x = 1;`
	cases := []struct {
		name  string
		src   string
		error bool
	}{
		{"e.ts", bad, true},
		{"e.MTS", bad, true}, // 大写扩展同样识别 TS
		{"e.cts", bad, true},
		{"e.js", bad, false},  // JS 源文件：enum 是普通语法
		{"e.mjs", bad, false}, // 同上
		{"e.ts", "declare enum E { A, B } const x = 1;", false}, // 环境声明允许
	}
	for _, tc := range cases {
		_, err := ParseSourceUnit([]byte(tc.src), tc.name, ModuleCommonJS)
		if tc.error && err == nil {
			t.Errorf("ParseSourceUnit(%s) = nil error, want TS diagnostic", tc.name)
		}
		if !tc.error && err != nil {
			t.Errorf("ParseSourceUnit(%s) error: %v", tc.name, err)
		}
	}
}

// TestParseSourceUnitStageMetadata：TS 源文件记录 TypeStripped，JS 不记录。
func TestParseSourceUnitStageMetadata(t *testing.T) {
	js, err := ParseSourceUnit([]byte("const x = 1;"), "a.js", ModuleCommonJS)
	if err != nil {
		t.Fatal(err)
	}
	if js.SourceKind != SourceJavaScript {
		t.Fatalf("a.js SourceKind = %s, want javascript", js.SourceKind)
	}
	if js.Stage&StageTypeStripped != 0 {
		t.Fatalf("a.js Stage has TypeStripped, want none")
	}
	ts, err := ParseSourceUnit([]byte("const x: number = 1;"), "a.ts", ModuleCommonJS)
	if err != nil {
		t.Fatal(err)
	}
	if ts.SourceKind != SourceTypeScript {
		t.Fatalf("a.ts SourceKind = %s, want typescript", ts.SourceKind)
	}
	if ts.Stage&StageTypeStripped == 0 {
		t.Fatalf("a.ts Stage missing TypeStripped")
	}
}

// TestParseFileSourceTLAPromotesToESM：仅有顶层 await 的 .ts/.js 也必须提升为
// ESM（无 import/export 但含 TLA，Node 语义）。
func TestParseFileSourceTLAPromotesToESM(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tla.ts")
	if err := os.WriteFile(p, []byte("const x = await Promise.resolve(42);\n"), 0644); err != nil {
		t.Fatal(err)
	}
	unit, err := ParseFileUnit(p, "tla.ts")
	if err != nil {
		t.Fatal(err)
	}
	if unit.ModuleKind != ModuleESM {
		t.Fatalf("TLA .ts ModuleKind = %s, want esm", unit.ModuleKind)
	}
	if !unit.HasTLA {
		t.Fatal("HasTLA = false, want true")
	}
}
