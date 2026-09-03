package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newDevFixture 建立最小 dev 工程：HTML 入口引用 main.ts。
func newDevFixture(t *testing.T) (*devServer, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte("import { v } from './dep.ts';\nexport const r = v;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dep.ts"), []byte("export const v = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>root</h1><script type=\"module\" src=\"./main.ts\"></script>"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := devOptions{host: "127.0.0.1", port: 0, outdir: filepath.Join(dir, "dist"), entry: filepath.Join(dir, "index.html")}
	return newDevServer(o), dir
}

// firstBuiltJS 返回 dist 下任意一个已写出的 .js 路径（HTML 入口默认原生 ESM，
// 主脚本在 assets/*-<hash>.js，不一定有 main.js barrel）。
func firstBuiltJS(t *testing.T, outdir string) string {
	t.Helper()
	var found string
	_ = filepath.WalkDir(outdir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || found != "" {
			return err
		}
		if strings.EqualFold(filepath.Ext(path), ".js") {
			found = path
		}
		return nil
	})
	if found == "" {
		t.Fatalf("no .js assets under %s", outdir)
	}
	return found
}

func distURLPath(outdir, absFile string) string {
	rel, err := filepath.Rel(outdir, absFile)
	if err != nil {
		return "/" + filepath.Base(absFile)
	}
	return "/" + filepath.ToSlash(rel)
}

// anyJSContains 扫描产物目录中是否有 JS 含 needle。
func anyJSContains(outdir, needle string) bool {
	found := false
	_ = filepath.WalkDir(outdir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || found {
			return err
		}
		if !strings.EqualFold(filepath.Ext(path), ".js") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr == nil && strings.Contains(string(data), needle) {
			found = true
		}
		return nil
	})
	return found
}

// TestDevServerHealthOK：初始构建成功后 health 为 ok。
func TestDevServerHealthOK(t *testing.T) {
	s, _ := newDevFixture(t)
	if err := s.rebuild(); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/__aluka/health", nil))
	if rec.Code != 200 {
		t.Fatalf("health status = %d", rec.Code)
	}
	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Error != "" {
		t.Errorf("health = %+v, want ok", body)
	}
}

// TestDevServerHealthError：依赖删除导致重建失败时 health 返回错误，
// 且旧产物继续可服务。
func TestDevServerHealthError(t *testing.T) {
	s, dir := newDevFixture(t)
	if err := s.rebuild(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "dep.ts")); err != nil {
		t.Fatal(err)
	}
	if err := s.rebuild(); err == nil {
		t.Fatal("rebuild after dep removal should fail")
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/__aluka/health", nil))
	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.OK || body.Error == "" {
		t.Errorf("health = %+v, want error state", body)
	}
	// 旧产物仍可服务。
	jsPath := firstBuiltJS(t, s.opts.outdir)
	jsRec := httptest.NewRecorder()
	s.mux.ServeHTTP(jsRec, httptest.NewRequest("GET", distURLPath(s.opts.outdir, jsPath), nil))
	if jsRec.Code != 200 {
		t.Errorf("stale js status = %d, want 200", jsRec.Code)
	}
}

// TestDevServerStaticAndSPAFallback：静态文件直出，未命中路径回退 index.html。
func TestDevServerStaticAndSPAFallback(t *testing.T) {
	s, _ := newDevFixture(t)
	if err := s.rebuild(); err != nil {
		t.Fatal(err)
	}
	jsPath := firstBuiltJS(t, s.opts.outdir)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", distURLPath(s.opts.outdir, jsPath), nil))
	if rec.Code != 200 {
		t.Fatalf("js asset status = %d", rec.Code)
	}
	spa := httptest.NewRecorder()
	s.mux.ServeHTTP(spa, httptest.NewRequest("GET", "/some/route", nil))
	if spa.Code != 200 {
		t.Fatalf("SPA fallback status = %d", spa.Code)
	}
	if !strings.Contains(spa.Body.String(), "<h1>root</h1>") {
		t.Errorf("SPA fallback body = %q", spa.Body.String())
	}
}

// TestDevServerReloadBroadcast：重建成功后 SSE 客户端收到 reload 事件。
func TestDevServerReloadBroadcast(t *testing.T) {
	s, _ := newDevFixture(t)
	if err := s.rebuild(); err != nil {
		t.Fatal(err)
	}
	ch := make(chan string, 4)
	s.mu.Lock()
	s.clients[ch] = struct{}{}
	s.mu.Unlock()

	if err := s.rebuild(); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-ch:
		if event != "reload" {
			t.Errorf("event = %q, want reload", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no reload event received")
	}
}

// TestDevServerRebuildPicksUpChanges：依赖修改后重建产物包含新内容。
func TestDevServerRebuildPicksUpChanges(t *testing.T) {
	s, dir := newDevFixture(t)
	if err := s.rebuild(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dep.ts"), []byte("export const v = 99;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.rebuild(); err != nil {
		t.Fatal(err)
	}
	if !anyJSContains(s.opts.outdir, "99") {
		t.Errorf("rebuilt assets missing updated dep value 99")
	}
}
