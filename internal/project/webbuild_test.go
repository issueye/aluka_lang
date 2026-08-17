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
