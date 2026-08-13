package module

import "testing"

func TestSourceModuleKindUsesNormalizedExplicitExtensions(t *testing.T) {
	r := NewResolver()
	cases := []struct {
		path string
		want ModuleKind
	}{
		{"main.MJS", ModuleESM},
		{"main.MTS", ModuleESM},
		{"main.CJS", ModuleCommonJS},
		{"main.CTS", ModuleCommonJS},
		{"main.JSON", ModuleScript},
	}
	for _, tc := range cases {
		if got := r.SourceModuleKind(tc.path); got != tc.want {
			t.Errorf("SourceModuleKind(%q) = %s, want %s", tc.path, got, tc.want)
		}
	}
}

func TestParseSourceUnitRecordsLanguageAndStages(t *testing.T) {
	unit, err := ParseSourceUnit([]byte("const x: number = 1;"), "main.ts", ModuleCommonJS)
	if err != nil {
		t.Fatalf("ParseSourceUnit: %v", err)
	}
	if unit.SourceKind != SourceTypeScript {
		t.Fatalf("SourceKind = %s, want typescript", unit.SourceKind)
	}
	if unit.Stage&(StageParsed|StageTypeStripped) != StageParsed|StageTypeStripped {
		t.Fatalf("Stage = %d, want parsed + type stripped", unit.Stage)
	}
	if unit.Program == nil {
		t.Fatal("Program is nil")
	}
}

func TestMarkStageMonotonic(t *testing.T) {
	unit, err := ParseSourceUnit([]byte("const x = 1;"), "m.js", ModuleCommonJS)
	if err != nil {
		t.Fatal(err)
	}
	if err := unit.MarkStage(StageShaken); err != nil {
		t.Fatalf("MarkStage(Shaken): %v", err)
	}
	if err := unit.MarkStage(StageMinified); err != nil {
		t.Fatalf("MarkStage(Minified): %v", err)
	}
	// 重复标记同一阶段 → 诊断（只增不减）。
	if err := unit.MarkStage(StageShaken); err == nil {
		t.Fatal("MarkStage(Shaken) second time = nil, want diagnostic")
	}
	// RequireStages 校验已完成阶段。
	if err := unit.RequireStages(StageShaken | StageMinified); err != nil {
		t.Fatalf("RequireStages: %v", err)
	}
	if err := unit.RequireStages(StageESMLowered); err == nil {
		t.Fatal("RequireStages(ESMLowered) = nil, want missing-stage diagnostic")
	}
}
