package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// mkdirWrite 创建目录并写文件。
func mkdirWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writePkg(t *testing.T, dir, name, version string, deps map[string]string) {
	t.Helper()
	pj := `{"name": "` + name + `", "version": "` + version + `"`
	if len(deps) > 0 {
		pj += `, "dependencies": {`
		first := true
		for k, v := range deps {
			if !first {
				pj += ","
			}
			pj += `"` + k + `": "` + v + `"`
			first = false
		}
		pj += `}`
	}
	pj += `}`
	mkdirWrite(t, filepath.Join(dir, "package.json"), pj)
}

// TestGlobs 验证数组与 {packages: []} 两种 forms。
func TestGlobs(t *testing.T) {
	arr := map[string]any{"workspaces": []any{"packages/*", "components/*"}}
	if got := Globs(arr); len(got) != 2 || got[0] != "packages/*" {
		t.Errorf("array Globs = %v", got)
	}
	obj := map[string]any{"workspaces": map[string]any{"packages": []any{"apps/*"}}}
	if got := Globs(obj); len(got) != 1 || got[0] != "apps/*" {
		t.Errorf("object Globs = %v", got)
	}
	if got := Globs(map[string]any{}); got != nil {
		t.Errorf("no workspaces Globs = %v, want nil", got)
	}
}

// TestMatchGlob 验证 ** 通配。
func TestMatchGlob(t *testing.T) {
	cases := map[string]struct {
		pat  string
		rel  string
		want bool
	}{
		"single": {"packages/*", "packages/a", true},
		"nested": {"packages/*", "packages/a/b", false},
		"deep":   {"packages/**", "packages/a/b/c", true},
		"deep0":  {"packages/**", "packages/a", true},
		"prefix": {"components/*", "packages/a", false},
		"multi":  {"packages/*/src", "packages/a/src", true},
		"exact":  {"pkg", "pkg", true},
	}
	for name, c := range cases {
		if got := matchGlob(c.pat, c.rel); got != c.want {
			t.Errorf("%s: matchGlob(%q, %q) = %v, want %v", name, c.pat, c.rel, got, c.want)
		}
	}
}

// TestDiscover 验证完整发现流程（含本地包互依赖与排除）。
func TestDiscover(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, "root", "1.0.0", map[string]string{})
	writePkg(t, filepath.Join(root, "packages", "a"), "pkg-a", "1.0.0", map[string]string{"is-number": "^7.0.0"})
	writePkg(t, filepath.Join(root, "packages", "b"), "pkg-b", "0.5.0", map[string]string{"pkg-a": "^1.0.0"})
	// 排除目录：不应出现。
	writePkg(t, filepath.Join(root, "packages", "skipped"), "pkg-skipped", "1.0.0", map[string]string{})
	// 无 name 的目录应被跳过。
	mkdirWrite(t, filepath.Join(root, "packages", "noname", "package.json"), `{"version": "1.0.0"}`)

	mkdirWrite(t, filepath.Join(root, "package.json"), `{"name":"root","workspaces":["packages/*","!packages/skipped"]}`)

	pkgs, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("Discover returned %d pkgs, want 2: %+v", len(pkgs), pkgs)
	}
	byName := map[string]*Package{}
	for _, p := range pkgs {
		byName[p.Name] = p
	}
	if p := byName["pkg-a"]; p == nil || len(p.Dependencies) != 1 || p.Dependencies[0].Name != "is-number" {
		t.Errorf("pkg-a deps = %+v", byName["pkg-a"])
	}
	if p := byName["pkg-b"]; p == nil || p.Version != "0.5.0" {
		t.Errorf("pkg-b = %+v", byName["pkg-b"])
	}
	if byName["pkg-skipped"] != nil {
		t.Errorf("excluded pkg-skipped should not be discovered")
	}
}

// TestDiscoverNoWorkspaces 验证无 workspaces 字段时返回 nil。
func TestDiscoverNoWorkspaces(t *testing.T) {
	root := t.TempDir()
	writePkg(t, root, "root", "1.0.0", map[string]string{})
	pkgs, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if pkgs != nil {
		t.Errorf("Discover = %v, want nil", pkgs)
	}
}
