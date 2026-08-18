package webemit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/bundler/graph"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for path, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func emitEntry(t *testing.T, dir, entry string, opts Options) Result {
	t.Helper()
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	gr, err := graph.Build(vm, module.NewResolver(), filepath.Join(dir, entry))
	if err != nil {
		t.Fatal(err)
	}
	opts.EntryFile = filepath.Join(dir, entry)
	out, err := Emit(gr, opts)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEmitNilGraph(t *testing.T) {
	if _, err := Emit(nil, Options{}); err == nil {
		t.Fatal("want error")
	}
}

func TestEmitBareJSImportsCSS(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"theme.css": "body{color:red}",
		"main.ts":   `import "./theme.css"; export const n = 1;`,
	})
	out := emitEntry(t, dir, "main.ts", Options{TreeShake: true})
	barrel := string(out.Assets["main.js"])
	if !strings.Contains(barrel, ".css") || !strings.Contains(barrel, "import ") {
		t.Fatalf("barrel should side-effect import CSS:\n%s", barrel)
	}
	foundCSS := false
	for name := range out.Assets {
		if strings.HasSuffix(name, ".css") {
			foundCSS = true
			break
		}
	}
	if !foundCSS {
		t.Fatalf("CSS asset missing: %v", assetKeys(out.Assets))
	}
	if out.EntryJS == "" {
		t.Fatal("EntryJS empty")
	}
}

func TestEmitCJSWrap(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.ts": `export const n = 1;`,
	})
	out := emitEntry(t, dir, "main.ts", Options{Format: "cjs"})
	js := string(out.Assets["main.js"])
	if !strings.Contains(js, "exports") && !strings.Contains(js, "module") && !strings.Contains(js, "__") {
		t.Fatalf("cjs wrap missing runtime:\n%s", js)
	}
	if out.EntryJS != "main.js" {
		t.Fatalf("EntryJS = %q", out.EntryJS)
	}
}

func assetKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
