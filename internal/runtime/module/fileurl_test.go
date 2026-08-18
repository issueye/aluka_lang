package module

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFileURLToPathRoundTrip(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mod.js")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	url := PathToFileURLString(target)
	got := FileURLToPath(url)
	if filepath.Clean(got) != filepath.Clean(target) {
		t.Fatalf("FileURLToPath(%q) = %q, want %q", url, got, target)
	}
}

func TestResolveFileURLSpecifierAndParent(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "mod.js")
	parent := filepath.Join(dir, "main.js")
	if err := os.WriteFile(mod, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parent, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	r := NewResolver()

	got, err := r.Resolve(PathToFileURLString(mod), parent)
	if err != nil {
		t.Fatalf("Resolve file URL specifier: %v", err)
	}
	if filepath.Clean(got) != filepath.Clean(mod) {
		t.Fatalf("got %q, want %q", got, mod)
	}

	// parent 为 file:// 时，相对路径仍应相对该文件所在目录解析。
	got, err = r.Resolve("./mod.js", PathToFileURLString(parent))
	if err != nil {
		t.Fatalf("Resolve with file URL parent: %v", err)
	}
	if filepath.Clean(got) != filepath.Clean(mod) {
		t.Fatalf("relative via file URL parent: got %q, want %q", got, mod)
	}
}

func TestNormalizeModulePathIgnoresNonFile(t *testing.T) {
	if got := NormalizeModulePath("./rel.js"); got != "./rel.js" {
		t.Fatalf("got %q", got)
	}
	if got := NormalizeModulePath("lodash"); got != "lodash" {
		t.Fatalf("got %q", got)
	}
}

func TestFileURLToPathWindowsDrive(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows drive letter semantics")
	}
	got := FileURLToPath("file:///C:/Users/test/app.js")
	want := `C:\Users\test\app.js`
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
