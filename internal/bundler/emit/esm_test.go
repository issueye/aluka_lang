package emit

import (
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/parser"
)

func TestBuildNativeESMGraph(t *testing.T) {
	util := parseMod(t, "util.ts", `export const add = (a, b) => a + b; export default 1;`)
	main := parseMod(t, "main.ts", `import add, { add as plus } from "./util"; export const n = plus(1, 2);`)
	main.Resolved = map[string]string{"./util": "util.ts"}

	out, err := BuildNativeESM(Bundle{
		EntryID: "main.ts",
		Modules: []Module{util, main},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.EntryFile, "assets/main-") || !strings.HasSuffix(out.EntryFile, ".js") {
		t.Fatalf("entry file = %s", out.EntryFile)
	}
	entry := string(out.Files[out.EntryFile])
	if strings.Contains(entry, "__def(") || strings.Contains(entry, "__req(") {
		t.Fatalf("native ESM leaked wrap runtime:\n%s", entry)
	}
	if !strings.Contains(entry, `from "./util-`) {
		t.Fatalf("missing rewritten relative import:\n%s", entry)
	}
	if !strings.Contains(entry, "export const n=") && !strings.Contains(entry, "export const n =") {
		t.Fatalf("missing native export:\n%s", entry)
	}
	barrel := BuildESMBarrel("main.js", out.EntryFile, []string{"n"})
	if !strings.Contains(barrel, `export * from "./`+out.EntryFile+`"`) && !strings.Contains(barrel, `export * from "./`+strings.ReplaceAll(out.EntryFile, "\\", "/")+`"`) {
		t.Fatalf("barrel = %s", barrel)
	}
}

func TestBuildNativeESMJSONAndCSS(t *testing.T) {
	main := parseMod(t, "app.ts", `import info from "./data.json"; import "./theme.css"; export const title = info.title;`)
	main.Resolved = map[string]string{"./data.json": "data.json", "./theme.css": "theme.css"}
	out, err := BuildNativeESM(Bundle{
		EntryID: "app.ts",
		Modules: []Module{main},
		Assets: map[string][]byte{
			"data.json":  []byte(`{"title":"Aluka Static Build"}`),
			"theme.css":  []byte(`h1{color:red}`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := string(out.Files[out.EntryFile])
	if strings.Contains(entry, "theme.css") || strings.Contains(entry, ".css") {
		t.Fatalf("CSS import should be stripped:\n%s", entry)
	}
	if !strings.Contains(entry, "Aluka Static Build") {
		found := false
		for name, data := range out.Files {
			if strings.Contains(string(data), "Aluka Static Build") && strings.Contains(string(data), "export default") {
				found = true
				if !strings.Contains(entry, name[strings.LastIndex(name, "/")+1:]) {
					t.Fatalf("entry does not import hashed JSON %s:\n%s", name, entry)
				}
			}
		}
		if !found {
			t.Fatalf("JSON module missing; files=%v entry=%s", keysOf(out.Files), entry)
		}
	}
}

func TestBuildNativeESMCJSInterop(t *testing.T) {
	cjs := parseMod(t, "react.js", `module.exports = {version: "18.3.1"}; module.exports.useState = function(){};`)
	cjs.IsCJS = true
	main := parseMod(t, "entry.ts", `import React from "react"; export const reactVersion = React.version;`)
	main.Resolved = map[string]string{"react": "react.js"}
	out, err := BuildNativeESM(Bundle{
		EntryID: "entry.ts",
		Modules: []Module{cjs, main},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := string(out.Files[out.EntryFile])
	if !strings.Contains(entry, "function __interop") {
		t.Fatalf("missing CJS interop:\n%s", entry)
	}
	cjsOut := ""
	for name, data := range out.Files {
		if strings.HasPrefix(name, "assets/react-") {
			cjsOut = string(data)
			break
		}
	}
	if !strings.Contains(cjsOut, "export default module.exports") {
		t.Fatalf("CJS wrapper missing default export:\n%s", cjsOut)
	}
	if strings.Contains(cjsOut, "__def(") {
		t.Fatalf("CJS wrapper used wrap runtime:\n%s", cjsOut)
	}
}

func TestBuildNativeESMDynamicImport(t *testing.T) {
	lazy := parseMod(t, "lazy.ts", `export const value = "lazy-ok";`)
	main := parseMod(t, "main.ts", `export async function load() { return (await import("./lazy.ts")).value; }`)
	main.Resolved = map[string]string{"./lazy.ts": "lazy.ts"}
	main.DynamicImports = map[string]DynamicImport{"./lazy.ts": {Target: "lazy.ts"}}
	out, err := BuildNativeESM(Bundle{EntryID: "main.ts", Modules: []Module{lazy, main}})
	if err != nil {
		t.Fatal(err)
	}
	entry := string(out.Files[out.EntryFile])
	if strings.Contains(entry, "__alukaImport") {
		t.Fatalf("dynamic import not native:\n%s", entry)
	}
	if !strings.Contains(entry, `import("./lazy-`) && !strings.Contains(entry, "import(\"./lazy-") {
		t.Fatalf("missing native import():\n%s", entry)
	}
	if len(out.Async) != 1 {
		t.Fatalf("async files = %v", out.Async)
	}
}

func TestHashedAssetPath(t *testing.T) {
	got := HashedAssetPath("src/index.tsx", "deadbeef", ".js")
	if got != "assets/index-deadbeef.js" {
		t.Fatalf("got %s", got)
	}
}

func TestPrintNativeImportExportRoundtrip(t *testing.T) {
	src := `import {a as b} from "./m.js"; export const x = b; export default x;`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	out := Print(prog)
	if !strings.Contains(out, `import {a as b} from "./m.js"`) {
		t.Fatalf("print import: %s", out)
	}
	if !strings.Contains(out, "export const x=") && !strings.Contains(out, "export var x=") {
		t.Fatalf("print export: %s", out)
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
