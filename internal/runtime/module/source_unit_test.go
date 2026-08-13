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
