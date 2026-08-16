package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWatchSnapshotExcludesOutdir：输出目录（构建产物）不进入 watch 快照，
// 避免重建写盘再次触发变更检测形成无限重建。
func TestWatchSnapshotExcludesOutdir(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	distDir := filepath.Join(dir, "dist")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(srcDir, "main.ts")
	if err := os.WriteFile(entry, []byte("export const a = 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	dep := filepath.Join(srcDir, "util.ts")
	if err := os.WriteFile(dep, []byte("export const u = 2;"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 产物文件在 dist 下：不应被监听。
	if err := os.WriteFile(filepath.Join(distDir, "main.js"), []byte("var a=1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "chunk-00000001.js"), []byte("var u=2;"), 0o644); err != nil {
		t.Fatal(err)
	}

	snap := watchSnapshot(entry, distDir)
	for path := range snap {
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			t.Fatal(err)
		}
		if rel == filepath.Join("dist", "main.js") || rel == filepath.Join("dist", "chunk-00000001.js") {
			t.Errorf("snapshot includes build output %q", rel)
		}
	}
	if _, ok := snap[entry]; !ok {
		t.Errorf("snapshot missing entry %q", entry)
	}
	if _, ok := snap[dep]; !ok {
		t.Errorf("snapshot missing dependency %q", dep)
	}
}

// TestWriteWebAssetsTrackedRemovesStale：上一轮写入但本轮未生成的 chunk
// 应被清理，避免依赖删除后陈旧产物残留。
func TestWriteWebAssetsTrackedRemovesStale(t *testing.T) {
	dir := t.TempDir()
	opts := buildOptions{outdir: dir}
	written := map[string]bool{}

	first := map[string][]byte{
		"main.js":     []byte("v1"),
		"chunk-aa.js": []byte("lazy-v1"),
	}
	if err := writeWebAssetsTracked("src/main.ts", first, opts, written); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main.js", "chunk-aa.js"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("first write missing %s: %v", name, err)
		}
	}

	second := map[string][]byte{"main.js": []byte("v2")}
	if err := writeWebAssetsTracked("src/main.ts", second, opts, written); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "chunk-aa.js")); !os.IsNotExist(err) {
		t.Errorf("stale chunk-aa.js not removed (stat err = %v)", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "main.js"))
	if err != nil || string(data) != "v2" {
		t.Errorf("main.js = %q, %v; want v2", data, err)
	}
}

// TestIsValidJSIdentifier：UMD global 名只接受合法标识符（拒绝引号注入、
// 数字开头、保留字）。
func TestIsValidJSIdentifier(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"MyApp", true},
		{"$lib", true},
		{"_private", true},
		{"lib2", true},
		{"", false},
		{"2fast", false},
		{"my-lib", false},
		{`x"];alert(1)//`, false},
		{"class", false},
		{"import", false},
		{"my app", false},
	}
	for _, c := range cases {
		if got := isValidJSIdentifier(c.in); got != c.want {
			t.Errorf("isValidJSIdentifier(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
