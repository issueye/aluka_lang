package webconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	alukart "github.com/aluka-lang/aluka/internal/project/aluka"
)

func writeRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func loadRoot(t *testing.T, dir string) *Result {
	t.Helper()
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Load(alukart.New(vm), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return res
}

func TestLoadZeroConfig(t *testing.T) {
	dir := writeRoot(t, map[string]string{
		"package.json": `{"name":"zero"}`,
		"index.ts":     `export const n = 1;`,
	})
	res := loadRoot(t, dir)
	if res.Source != "" || res.OutDir != "" || res.Base != "" || res.VueCompiler != "" {
		t.Fatalf("zero-config got %+v", res)
	}
	if res.Minify != nil {
		t.Fatalf("zero-config minify = %v, want nil", res.Minify)
	}
}

func TestLoadSkipsUnrelatedConfigFiles(t *testing.T) {
	dir := writeRoot(t, map[string]string{
		"jest.config.js":   `throw new Error("jest should not be executed");`,
		"eslint.config.js": `module.exports = { rules: {} };`,
	})
	res := loadRoot(t, dir)
	if res.Source != "" {
		t.Fatalf("unrelated configs should be ignored, got source %q", res.Source)
	}
}

func TestLoadDiscoversViteShapeByObjectNotFilename(t *testing.T) {
	dir := writeRoot(t, map[string]string{
		"pack.config.js": `
const { defineConfig } = require("vite");
module.exports = defineConfig({
  base: "/app/",
  build: { outDir: "build-out", assetsDir: "media", minify: true },
  resolve: { alias: { "@": "./src" } },
  define: { __OK__: JSON.stringify(true) },
  extraUnknown: { ignored: true },
});
`,
	})
	res := loadRoot(t, dir)
	if res.Source != "pack.config.js" {
		t.Fatalf("source = %q, want pack.config.js (filename must not be hardcoded in Go)", res.Source)
	}
	if res.Base != "/app/" || res.OutDir != "build-out" || res.AssetsDir != "media" {
		t.Fatalf("vite-shape fields = %+v", res)
	}
	if res.Minify == nil || !*res.Minify {
		t.Fatalf("minify = %v, want true", res.Minify)
	}
	if res.Define["__OK__"] != "true" {
		t.Fatalf("define = %#v", res.Define)
	}
	wantAlias := filepath.Join(dir, "src")
	if filepath.Clean(res.Alias["@"]) != filepath.Clean(wantAlias) {
		t.Fatalf("alias @ = %q, want %q", res.Alias["@"], wantAlias)
	}
}

func TestLoadDiscoversVueCliShapeByObjectNotFilename(t *testing.T) {
	dir := writeRoot(t, map[string]string{
		"cli.config.js": `
module.exports = {
  publicPath: "/cdn/",
  outputDir: "www",
  assetsDir: "static",
  alias: { "@lib": "./lib" },
};
`,
	})
	res := loadRoot(t, dir)
	if res.Source != "cli.config.js" {
		t.Fatalf("source = %q, want cli.config.js", res.Source)
	}
	if res.Base != "/cdn/" || res.OutDir != "www" || res.AssetsDir != "static" {
		t.Fatalf("vue-cli-shape fields = %+v", res)
	}
}

func TestLoadProjectHookWins(t *testing.T) {
	dir := writeRoot(t, map[string]string{
		"aluka.config.js": `module.exports = { outDir: "from-hook", base: "/hook/" };`,
		"pack.config.js": `
const { defineConfig } = require("vite");
module.exports = defineConfig({ outDir: "from-pack", base: "/pack/" });
`,
	})
	res := loadRoot(t, dir)
	if res.Source != "aluka.config.js" {
		t.Fatalf("source = %q, want aluka.config.js", res.Source)
	}
	if res.OutDir != "from-hook" || res.Base != "/hook/" {
		t.Fatalf("hook fields = %+v", res)
	}
}

func TestLoadHookInfersOfficialVueFromPlugin(t *testing.T) {
	dir := writeRoot(t, map[string]string{
		"aluka.config.js": `
const vue = require("@vitejs/plugin-vue");
module.exports = { plugins: [vue()], build: { outDir: "dist" } };
`,
	})
	res := loadRoot(t, dir)
	if res.VueCompiler != "official" {
		t.Fatalf("vueCompiler = %q, want official (from plugin name)", res.VueCompiler)
	}
}

func TestLoadBrokenHookErrors(t *testing.T) {
	dir := writeRoot(t, map[string]string{
		"aluka.config.js": `throw new Error("boom-hook");`,
	})
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	_, err = Load(alukart.New(vm), dir)
	if err == nil || !strings.Contains(err.Error(), "boom-hook") {
		t.Fatalf("broken hook error = %v", err)
	}
}

func TestLoadCustomScriptEnv(t *testing.T) {
	dir := writeRoot(t, map[string]string{
		"aluka.config.js": `module.exports = { outDir: "from-hook" };`,
	})
	script := filepath.Join(t.TempDir(), "custom.cjs")
	if err := os.WriteFile(script, []byte(`
function loadWebConfigJSON(root) {
  return JSON.stringify({ source: "env-script", outDir: "from-env", assetsDir: "a" });
}
module.exports = { loadWebConfigJSON };
`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALUKA_WEB_CONFIG", script)
	res := loadRoot(t, dir)
	if res.Source != "env-script" || res.OutDir != "from-env" {
		t.Fatalf("ALUKA_WEB_CONFIG should replace default loader, got %+v", res)
	}
}

func TestFindRootWalksToPackageJSON(t *testing.T) {
	dir := writeRoot(t, map[string]string{
		"package.json":        `{"name":"app"}`,
		"src/pages/app.ts":    `export default 1;`,
		"nested/package.json": `{"name":"nested"}`,
	})
	got := FindRoot(filepath.Join(dir, "src", "pages", "app.ts"), dir)
	if got != dir {
		t.Fatalf("FindRoot = %q, want %q", got, dir)
	}
}

func TestFindRootStopsAtCWDWithoutPackageJSON(t *testing.T) {
	dir := writeRoot(t, map[string]string{
		"index.ts": `export const n = 1;`,
	})
	got := FindRoot(filepath.Join(dir, "index.ts"), dir)
	if got != dir {
		t.Fatalf("FindRoot = %q, want cwd %q (must not walk into parent package.json)", got, dir)
	}
}

func TestApplyCLIWinsAndUnknownIgnored(t *testing.T) {
	minify := true
	src := &Result{
		OutDir:      "from-file",
		AssetsDir:   "media",
		Base:        "/cdn/",
		Minify:      &minify,
		VueCompiler: "official",
		Alias:       map[string]string{"@": "/abs/src"},
		Define:      map[string]string{"__A__": "1"},
	}
	dst := Applied{OutDir: "cli-out", Minify: false, VueCompiler: "subset"}
	Apply(&dst, src, CLIOverrides{OutDir: true, Minify: true, VueCompiler: true})
	if dst.OutDir != "cli-out" {
		t.Fatalf("CLI outDir overwritten: %q", dst.OutDir)
	}
	if dst.Minify {
		t.Fatal("CLI minify=false overwritten")
	}
	if dst.VueCompiler != "subset" {
		t.Fatalf("CLI vueCompiler overwritten: %q", dst.VueCompiler)
	}
	if dst.AssetsDir != "media" || dst.PublicBase != "/cdn/" {
		t.Fatalf("non-CLI fields not applied: %+v", dst)
	}
	if dst.Alias["@"] != "/abs/src" || dst.Define["__A__"] != "1" {
		t.Fatalf("alias/define not applied: %+v", dst)
	}
}

func TestLoadSessionKeepsPlugins(t *testing.T) {
	dir := writeRoot(t, map[string]string{
		"aluka.config.js": `
module.exports = {
  outDir: "dist",
  plugins: [{
    name: "keep-alive",
    transformIndexHtml(html) { return html + "<!--kept-->"; },
  }],
};
`,
	})
	vm, err := interpreter.NewVM()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := LoadSession(alukart.New(vm), dir)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Result.OutDir != "dist" {
		t.Fatalf("result = %+v", sess.Result)
	}
	html, err := sess.Plugins.TransformIndexHTML("<html></html>")
	if err != nil {
		t.Fatal(err)
	}
	if html != "<html></html><!--kept-->" {
		t.Fatalf("plugin not kept alive: %q", html)
	}
}
