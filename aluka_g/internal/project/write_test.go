package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/bundler/plugin"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	alukart "github.com/aluka-lang/aluka/internal/project/aluka"
)

func TestAssetTargetRejectsEscape(t *testing.T) {
	dir := t.TempDir()
	cases := []string{
		"../escape.txt",
		"..\\escape.txt",
		filepath.Join("..", "escape.txt"),
	}
	for _, name := range cases {
		if _, err := assetTarget(dir, name); err == nil {
			t.Errorf("assetTarget(%q) = nil, want error", name)
		}
	}
	if _, err := assetTarget(dir, filepath.Join(dir, "abs.txt")); err == nil {
		t.Error("absolute asset name should fail")
	}
	ok, err := assetTarget(dir, "plugin-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if ok != filepath.Join(dir, "plugin-manifest.json") {
		t.Fatalf("got %q", ok)
	}
	nested, err := assetTarget(dir, "assets/x.js")
	if err != nil {
		t.Fatal(err)
	}
	if nested != filepath.Join(dir, "assets", "x.js") {
		t.Fatalf("nested = %q", nested)
	}
}

func TestWriteAssetsRejectsEscape(t *testing.T) {
	dir := t.TempDir()
	err := WriteAssets("main.ts", map[string][]byte{
		"../escape.txt": []byte("nope"),
	}, Options{OutDir: dir}, nil)
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("WriteAssets escape = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(dir), "escape.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("escaped file should not exist: %v", statErr)
	}
}

func TestWriteAssetsTrackedStaleCleanup(t *testing.T) {
	dir := t.TempDir()
	opts := Options{OutDir: dir}
	written := map[string]bool{}
	first := map[string][]byte{
		"main.js":     []byte("v1"),
		"chunk-aa.js": []byte("lazy"),
	}
	if err := WriteAssets("src/main.ts", first, opts, written); err != nil {
		t.Fatal(err)
	}
	second := map[string][]byte{"main.js": []byte("v2")}
	if err := WriteAssets("src/main.ts", second, opts, written); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "chunk-aa.js")); !os.IsNotExist(err) {
		t.Errorf("stale chunk not removed: %v", err)
	}
}

type closeCounter struct {
	plugin.Nop
	closes int
}

func (c *closeCounter) CloseBundle() error {
	c.closes++
	return nil
}

func TestBuildWebClosesOnFailure(t *testing.T) {
	h := &closeCounter{}
	_, err := BuildWeb(nil, nil, filepath.Join(t.TempDir(), "missing.ts"), Options{
		Plugins:   h,
		TreeShake: true,
	})
	if err == nil {
		t.Fatal("expected build error")
	}
	if h.closes != 1 {
		t.Fatalf("CloseBundle calls = %d, want 1", h.closes)
	}
}

type escapeBundleHost struct{ plugin.Nop }

func (escapeBundleHost) GenerateBundle([]string) (map[string]string, error) {
	return map[string]string{"../escape.txt": "x"}, nil
}

func TestBuildWebRejectsEscapingGenerateBundle(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(entry, []byte("export const n = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	_, err = BuildWeb(alukart.New(vm), nil, entry, Options{
		OutDir:    filepath.Join(dir, "dist"),
		Plugins:   escapeBundleHost{},
		TreeShake: true,
	})
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("generateBundle escape = %v", err)
	}
}
