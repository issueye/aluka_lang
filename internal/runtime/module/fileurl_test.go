package module

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// TestPathToFileURLStringEscapesSpecialChars：路径含 #/?/空格/非 ASCII 时
// 必须百分号转义，否则 url.Parse 把 # 后内容当 fragment、? 后当 query，
// FileURLToPath 往返被截断（对齐 Node pathToFileURL）。
func TestPathToFileURLStringEscapesSpecialChars(t *testing.T) {
	if u := PathToFileURLString(filepath.Join("dir", "a#b.js")); !strings.Contains(u, "%23") {
		t.Errorf("PathToFileURLString(a#b.js) = %q, want %%23 escaping", u)
	}
	if u := PathToFileURLString(filepath.Join("dir", "a b.js")); !strings.Contains(u, "%20") {
		t.Errorf("PathToFileURLString(a b.js) = %q, want %%20 escaping", u)
	}
	if u := PathToFileURLString(filepath.Join("dir", "a?b.js")); !strings.Contains(u, "%3F") {
		t.Errorf("PathToFileURLString(a?b.js) = %q, want %%3F escaping", u)
	}

	// 文件系统往返（? 在 Windows 文件名中非法，仅做字符串断言）。
	dir := t.TempDir()
	for _, name := range []string{"proj#1", "sp ace", "中文"} {
		target := filepath.Join(dir, name, "mod.js")
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(""), 0644); err != nil {
			t.Fatal(err)
		}
		u := PathToFileURLString(target)
		got := FileURLToPath(u)
		if filepath.Clean(got) != filepath.Clean(target) {
			t.Errorf("round-trip %q: url=%q → %q, want %q", target, u, got, target)
		}
	}
}
