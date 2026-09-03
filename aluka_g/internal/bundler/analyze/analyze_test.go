package analyze

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/bundler/compile"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

func compileFixture(t *testing.T, root, name, source string) *compile.EntryData {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	mod, err := compile.CompileFile(vm, path, filepath.ToSlash(name))
	if err != nil {
		t.Fatal(err)
	}
	return mod
}

func TestMeasureAndBuildReport(t *testing.T) {
	root := t.TempDir()
	mainMod := compileFixture(t, root, "main.js", "console.log('hello')")
	removedMod := compileFixture(t, root, "node_modules/dead/index.js", "module.exports = 42")
	raw, err := MeasureStage([]*compile.EntryData{mainMod, removedMod})
	if err != nil {
		t.Fatal(err)
	}
	final, err := MeasureStage([]*compile.EntryData{mainMod})
	if err != nil {
		t.Fatal(err)
	}
	report, err := BuildReport(Input{
		Entry:             "main.js",
		RootDir:           root,
		Resolutions:       map[string]map[string]string{"main.js": {"dead": "node_modules/dead/index.js"}},
		UnresolvedDynamic: []string{"main.js", "main.js"},
		Assets:            map[string][]byte{"data.json": []byte(`{"ok":true}`)},
		Raw:               raw,
		Shaken:            final,
		Minified:          final,
		BytecodeOptimized: final,
		PayloadBytes:      final.ModuleBytes + 100,
		Options:           Options{TreeShake: true, MaxPayloadBytes: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Modules) != 1 || report.Modules[0].Path != "main.js" || !report.Modules[0].Entry {
		t.Fatalf("final modules = %#v", report.Modules)
	}
	if len(report.RemovedModules) != 1 || report.RemovedModules[0].Path != "node_modules/dead/index.js" {
		t.Fatalf("removed modules = %#v", report.RemovedModules)
	}
	if len(report.Assets) != 1 || report.Assets[0].Path != "data.json" {
		t.Fatalf("assets = %#v", report.Assets)
	}
	ids := make(map[string]int)
	for _, finding := range report.Findings {
		ids[finding.ID]++
	}
	if ids["DYNAMIC_IMPORT_UNRESOLVED"] != 1 {
		t.Fatalf("dynamic import findings = %d, want 1", ids["DYNAMIC_IMPORT_UNRESOLVED"])
	}
	if ids["PAYLOAD_BUDGET_EXCEEDED"] != 1 {
		t.Fatal("missing payload budget finding")
	}
}

func TestJSONIsDeterministicAndDoesNotLeakRoot(t *testing.T) {
	report := &Report{
		Entry:   "main.js",
		Options: Options{TreeShake: true},
		Modules: []ModuleReport{{Path: "main.js", FinalBytecodeBytes: 10}},
	}
	var a, b bytes.Buffer
	if err := WriteJSON(&a, []*Report{report}); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(&b, []*Report{report}); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Fatal("JSON output is not deterministic")
	}
	if strings.Contains(a.String(), t.TempDir()) {
		t.Fatal("JSON output leaked an absolute temporary path")
	}
	if !strings.Contains(a.String(), `"schemaVersion": 1`) {
		t.Fatalf("missing schema version: %s", a.String())
	}
}

func TestWriteTextIncludesBytecodeStage(t *testing.T) {
	report := &Report{
		Entry: "main.js",
		Stages: Stages{
			Raw:               StageMeasurement{ModuleCount: 1, ModuleBytes: 200},
			Shaken:            StageMeasurement{ModuleCount: 1, ModuleBytes: 180},
			Minified:          StageMeasurement{ModuleCount: 1, ModuleBytes: 160},
			BytecodeOptimized: StageMeasurement{ModuleCount: 1, ModuleBytes: 140},
		},
		Sizes: Sizes{PayloadBytes: 200, ArtifactBytes: 200},
		Modules: []ModuleReport{{
			Path:               "main.js",
			ModuleType:         "cjs",
			FinalBytecodeBytes: 140,
			PayloadShare:       0.7,
		}},
	}
	var out bytes.Buffer
	if err := WriteText(&out, report, 10); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Aluka bundle analysis", "bytecode optimized", "Hot modules", "main.js"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("text report missing %q:\n%s", want, out.String())
		}
	}
}
