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

// TestEmitNewURLAsset 验证 new URL 静态资产：资产随产物输出 + 打印期把
// 原始 spec 改写为相对 chunk 的产物路径（对齐 esbuild）。
func TestEmitNewURLAsset(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"src/index.js":      "import { img } from './pages/page.js';\nconst pic = new URL('./pages/pic.png', import.meta.url).href;\nconsole.log(img, pic);",
		"src/pages/page.js": "export const img = new URL('../logo.png', import.meta.url).href;",
		"src/logo.png":      "PNG-LOGO",
		"src/pages/pic.png": "PNG-PIC",
	})
	out := emitEntry(t, dir, "src/index.js", Options{TreeShake: true})
	if string(out.Assets["logo.png"]) != "PNG-LOGO" {
		t.Errorf("logo.png 资产缺失或内容不符: %q", out.Assets["logo.png"])
	}
	if string(out.Assets["pages/pic.png"]) != "PNG-PIC" {
		t.Errorf("pages/pic.png 资产缺失或内容不符: %q", out.Assets["pages/pic.png"])
	}
	// 全部产物文本中找改写后的 URL：相对 assets/ chunk 应解析回产物根目录。
	var all strings.Builder
	for name, data := range out.Assets {
		if strings.HasSuffix(name, ".js") {
			all.Write(data)
			all.WriteByte('\n')
		}
	}
	js := all.String()
	if !strings.Contains(js, `new URL("../logo.png",`) {
		t.Errorf("page 模块 spec 未改写到产物相对路径:\n%s", js)
	}
	if !strings.Contains(js, `new URL("../pages/pic.png",`) {
		t.Errorf("entry 模块 spec 未改写到产物相对路径:\n%s", js)
	}
}

// TestEmitNewURLAssetNonRelative 非相对路径/非 import.meta 基址的引用
// 原样保留且不产生资产。
func TestEmitNewURLAssetNonRelative(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"src/index.js": "const a = new URL('/abs.png', import.meta.url).href;\nconst b = new URL('./x.png', 'https://base/').href;\nconsole.log(a, b);",
	})
	out := emitEntry(t, dir, "src/index.js", Options{TreeShake: true})
	var all strings.Builder
	for name, data := range out.Assets {
		if strings.HasSuffix(name, ".js") {
			all.Write(data)
			all.WriteByte('\n')
		}
	}
	js := all.String()
	if !strings.Contains(js, `new URL("/abs.png",`) || !strings.Contains(js, `new URL("./x.png",`) {
		t.Errorf("非相对引用应原样保留:\n%s", js)
	}
	if len(out.Assets) == 0 {
		t.Fatal("产物为空")
	}
	for name := range out.Assets {
		if strings.HasSuffix(name, ".png") {
			t.Errorf("不应产生 png 资产: %s", name)
		}
	}
}
