package project_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/project"
	alukart "github.com/aluka-lang/aluka/internal/project/aluka"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

func TestBuildWebPluginHooks(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"package.json": `{"name":"plugin-demo"}`,
		"index.html": `<!DOCTYPE html><html><head></head><body>
<script type="module" src="./main.ts"></script>
</body></html>`,
		"main.ts": `import { msg } from "virtual:demo";
document.body.dataset.msg = msg;
`,
		"aluka.config.js": `
function demo() {
  return {
    name: "virtual-demo",
    resolveId(id) {
      if (id === "virtual:demo") return "\0virtual:demo";
    },
    load(id) {
      if (id === "\0virtual:demo") return 'export const msg = "ok";';
    },
    transformIndexHtml(html) {
      return html.replace("</head>", '<meta name="aluka-plugin" content="1"></head>');
    },
    generateBundle(files) {
      return { "plugin-manifest.json": files };
    },
  };
}
module.exports = { outDir: "dist", minify: false, plugins: [demo()] };
`,
	}
	for name, src := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	rt := alukart.New(vm)
	opts := project.Options{TreeShake: true}
	if err := project.ApplyConfig(rt, filepath.Join(dir, "index.html"), &opts); err != nil {
		t.Fatal(err)
	}
	bundled, err := project.BuildWeb(rt, module.NewResolver(), filepath.Join(dir, "index.html"), opts)
	if err != nil {
		t.Fatal(err)
	}
	html := string(bundled.Assets["index.html"])
	if !strings.Contains(html, `name="aluka-plugin"`) {
		t.Fatalf("transformIndexHtml missing: %s", html)
	}
	mani, ok := bundled.Assets["plugin-manifest.json"]
	if !ok || !strings.Contains(string(mani), "index.html") {
		t.Fatalf("generateBundle missing: %q ok=%v", mani, ok)
	}
	foundVirtual := false
	for name, data := range bundled.Assets {
		if strings.Contains(string(data), `msg`) && strings.Contains(string(data), "ok") {
			foundVirtual = true
			_ = name
			break
		}
	}
	if !foundVirtual {
		t.Fatalf("virtual module not in assets: %#v", keys(bundled.Assets))
	}
	if err := project.WriteAssets(filepath.Join(dir, "index.html"), bundled.Assets, opts, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(opts.OutDir, "plugin-manifest.json")); err != nil {
		t.Fatal(err)
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestBuildWebBareJSImportsCSS(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.ts")
	css := filepath.Join(dir, "theme.css")
	if err := os.WriteFile(css, []byte("body{color:red}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte(`import "./theme.css"; export const n = 1;`), 0o644); err != nil {
		t.Fatal(err)
	}
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	bundled, err := project.BuildWeb(alukart.New(vm), module.NewResolver(), entry, project.Options{
		TreeShake: true,
		OutDir:    filepath.Join(dir, "dist"),
	})
	if err != nil {
		t.Fatal(err)
	}
	barrel := string(bundled.Assets["main.js"])
	if !strings.Contains(barrel, ".css") || !strings.Contains(barrel, "import ") {
		t.Fatalf("barrel should side-effect import CSS:\n%s\nassets=%v", barrel, keys(bundled.Assets))
	}
	foundCSS := false
	for name := range bundled.Assets {
		if strings.HasSuffix(name, ".css") {
			foundCSS = true
			break
		}
	}
	if !foundCSS {
		t.Fatalf("CSS asset missing: %v", keys(bundled.Assets))
	}
}

func TestBuildWebHTMLStylesheetsUnique(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "a"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "b"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, "a", "app.css"), []byte("body{color:red}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b", "app.css"), []byte("body{color:blue}"), 0o644); err != nil {
		t.Fatal(err)
	}
	html := filepath.Join(dir, "index.html")
	src := `<!DOCTYPE html><html><head>
<link rel="stylesheet" href="./a/app.css">
<link rel="stylesheet" href="./b/app.css">
</head><body></body></html>`
	if err := os.WriteFile(html, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	bundled, err := project.BuildWeb(alukart.New(vm), module.NewResolver(), html, project.Options{TreeShake: true})
	if err != nil {
		t.Fatal(err)
	}
	cssCount := 0
	for name, data := range bundled.Assets {
		if strings.HasSuffix(name, ".css") {
			cssCount++
			_ = data
		}
	}
	if cssCount < 2 {
		t.Fatalf("want 2 distinct CSS assets, got %d: %v", cssCount, keys(bundled.Assets))
	}
	outHTML := string(bundled.Assets["index.html"])
	hrefCount := 0
	for _, name := range keys(bundled.Assets) {
		if strings.HasSuffix(name, ".css") && strings.Contains(outHTML, filepath.Base(name)) {
			hrefCount++
		}
	}
	if hrefCount < 2 {
		t.Fatalf("HTML should reference both CSS outputs (got %d); html=%s assets=%v", hrefCount, outHTML, keys(bundled.Assets))
	}
}

func TestPluginHookThisBinding(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"package.json": `{"name":"this-demo"}`,
		"main.ts":      `export const n = 1;`,
		"aluka.config.js": `
module.exports = {
  outDir: "dist",
  plugins: [{
    name: "this-check",
    transform(code) {
      if (this && this.name === "this-check") {
        return code + "\nexport const thisOk = true;";
      }
      throw new Error("plugin this unbound: " + (this && this.name));
    },
  }],
};
`,
	}
	for name, src := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	rt := alukart.New(vm)
	opts := project.Options{TreeShake: true}
	if err := project.ApplyConfig(rt, filepath.Join(dir, "main.ts"), &opts); err != nil {
		t.Fatal(err)
	}
	bundled, err := project.BuildWeb(rt, module.NewResolver(), filepath.Join(dir, "main.ts"), opts)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, data := range bundled.Assets {
		if strings.Contains(string(data), "thisOk") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("transform this binding missing; assets=%v", keys(bundled.Assets))
	}
}
