package module

// T2-B4 编译产物运行时动态导入测试（docs/jiti-dynamic-import-plan.md M1）。
//
// 产物模式下构建期未静态解析的动态导入（import(变量)）与
// createRequire(import.meta.url) 相对解析：虚拟父路径经 manifest.RootDir
// 映射回构建机磁盘，走文件系统现场加载未嵌入模块。
//
// 测试用 rootDirEmbedded 模拟「命中不了任何嵌入模块、但带 RootDir」的
// 产物存储；入口模块用 vm.Compile(WrapCJSSource) 手工编译后
// RunPrecompiled 以虚拟 key 执行，与真实产物形态一致。

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// rootDirEmbedded 模拟带 RootDir 的产物嵌入式存储（所有 specifier 均未
// 嵌入——即构建期未静态解析的动态导入场景）。
type rootDirEmbedded struct{ root string }

func (e rootDirEmbedded) ResolveEmbedded(string, string) (string, bool)                   { return "", false }
func (e rootDirEmbedded) ModuleTypeOf(string) string                                      { return "" }
func (e rootDirEmbedded) LoadModule(string) (*bytecode.Module, error)                     { return nil, fmt.Errorf("not embedded") }
func (e rootDirEmbedded) LoadJSON(string) ([]byte, bool)                                  { return nil, false }
func (e rootDirEmbedded) RootDir() string                                                 { return e.root }

// runCompiledEntry 以虚拟 key 编译并执行一个 CJS 入口（模拟产物内模块）。
func runCompiledEntry(t *testing.T, env *testEnv, key, src string) {
	t.Helper()
	vm, ok := env.loader.ctx.(*interpreter.VM)
	if !ok {
		t.Fatal("test env requires bytecode VM")
	}
	mod, err := vm.Compile(WrapCJSSource(src), key)
	if err != nil {
		t.Fatalf("compile entry %q: %v", key, err)
	}
	if _, err := env.loader.RunPrecompiled(key, mod, false); err != nil {
		t.Fatalf("run entry %q: %v", key, err)
	}
}

// TestCompiledVariableDynamicImportFromDisk: 产物模式下 import(变量) 以
// RootDir 为基准回退磁盘，加载 .cjs 与 .ts（TS 转译经既有文件链路）。
func TestCompiledVariableDynamicImportFromDisk(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"mod-a.cjs": `module.exports = { add: function(a, b) { return a + b; } };`,
		"mod-b.ts":  `export const sq = (x: number): number => x * x;`,
	})
	env.loader.SetEmbedded(rootDirEmbedded{root: env.dir})

	runCompiledEntry(t, env, "virtual-main.cjs", `
globalThis.__which = "a";
import("./mod-" + globalThis.__which + ".cjs").then(function(m) {
  globalThis.__r = m.add(2, 3);
});
`)
	if got := env.globalGet("__r"); got != "5" {
		t.Errorf("variable dynamic import cjs: got %q, want 5", got)
	}

	runCompiledEntry(t, env, "virtual-main.cjs", `
globalThis.__which = "b";
import("./mod-" + globalThis.__which + ".ts").then(function(m) {
  globalThis.__r = m.sq(7);
});
`)
	if got := env.globalGet("__r"); got != "49" {
		t.Errorf("variable dynamic import ts: got %q, want 49", got)
	}
}

// TestCompiledBunURLParentRequire: createRequire(import.meta.url) 在产物
// 模式下父路径为 bun://~BUN/<key>，相对 require 应映射 RootDir 后解析。
func TestCompiledBunURLParentRequire(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"mod-a.cjs": `module.exports = "from-disk";`,
	})
	env.loader.SetEmbedded(rootDirEmbedded{root: env.dir})

	// import.meta.url 在产物模式为 bun://~BUN/plugin-ui.ts（makeImportMetaFunc）。
	req := env.loader.MakeRequireFunc("bun://~BUN/plugin-ui.ts")
	val, err := req.Call([]engine.Value{engine.Str("./mod-a.cjs")})
	if err != nil {
		t.Fatalf("require via bun:// parent: %v", err)
	}
	if got := val.String(); got != "from-disk" {
		t.Errorf("bun:// parent require: got %q, want from-disk", got)
	}
}

// TestCompiledRootDirMissingFileRejects: RootDir 回退但磁盘文件不存在时，
// 动态 import 返回 rejected promise（.catch 捕获，而非同步抛出）。
func TestCompiledRootDirMissingFileRejects(t *testing.T) {
	env := newTestEnv(t, nil)
	env.loader.SetEmbedded(rootDirEmbedded{root: env.dir})

	runCompiledEntry(t, env, "virtual-main.cjs", `
import("./nope-" + "x" + ".cjs").then(function() {
  globalThis.__r = "resolved";
}, function(e) {
  globalThis.__r = "rejected:" + (e !== undefined);
});
`)
	if got := env.globalGet("__r"); got != "rejected:true" {
		t.Errorf("missing file: got %q, want rejected:true", got)
	}
}

// TestDynamicImportDataURLBase64: data:text/javascript;base64, 按 ESM 语义
// 编译执行（jiti 的 ESM 转译主路径之一），支持命名/默认导出。
func TestDynamicImportDataURLBase64(t *testing.T) {
	src := `export const x = 7; export default "hello-data";`
	url := "data:text/javascript;base64," + base64.StdEncoding.EncodeToString([]byte(src))
	env := newTestEnv(t, map[string]string{
		"main.cjs": fmt.Sprintf(`import(%q).then(function(m) {
  globalThis.__x = m.x;
  globalThis.__d = m.default;
});`, url),
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__x"); got != "7" {
		t.Errorf("data URL named export: got %q, want 7", got)
	}
	if got := env.globalGet("__d"); got != "hello-data" {
		t.Errorf("data URL default export: got %q, want hello-data", got)
	}
}

// TestDynamicImportDataURLPercentEncoded: 非 base64 的 percent-encoding 载荷。
func TestDynamicImportDataURLPercentEncoded(t *testing.T) {
	url := "data:text/javascript," + strings.ReplaceAll(`export const y = "ok";`, " ", "%20")
	env := newTestEnv(t, map[string]string{
		"main.cjs": fmt.Sprintf(`import(%q).then(function(m) {
  globalThis.__y = m.y;
});`, url),
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__y"); got != "ok" {
		t.Errorf("data URL percent-encoded: got %q, want ok", got)
	}
}

// TestDecodeDataURLSource: 解码器单元级：base64 / percent / 非法载荷。
func TestDecodeDataURLSource(t *testing.T) {
	src := []byte(`export const z = 1;`)
	b64 := base64.StdEncoding.EncodeToString(src)
	got, err := decodeDataURLSource("data:text/javascript;base64," + b64)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	if string(got) != string(src) {
		t.Errorf("decode base64: got %q, want %q", got, src)
	}

	got2, err := decodeDataURLSource("data:text/javascript;charset=utf-8;base64," + b64)
	if err != nil || string(got2) != string(src) {
		t.Errorf("decode with charset: got %q, err %v", got2, err)
	}

	got3, err := decodeDataURLSource("data:text/javascript," + strings.ReplaceAll(string(src), " ", "%20"))
	if err != nil || string(got3) != string(src) {
		t.Errorf("decode percent: got %q, err %v", got3, err)
	}

	if _, err := decodeDataURLSource("data:image/png;base64,AAAA"); err == nil {
		t.Error("non-JS media type did not error")
	}
	if _, err := decodeDataURLSource("data:text/javascript;base64,!!!"); err == nil {
		t.Error("invalid base64 did not error")
	}
	if _, err := decodeDataURLSource("data:text/javascript;no-comma"); err == nil {
		t.Error("missing comma did not error")
	}
}

// TestMapEmbeddedParentToDisk: 虚拟父路径 → 构建机磁盘路径映射。
func TestMapEmbeddedParentToDisk(t *testing.T) {
	root := filepath.FromSlash("/proj/app")
	absParent := filepath.Join(t.TempDir(), "abs-main.ts")
	cases := []struct {
		parent string
		want   string
	}{
		{"plugin-ui.ts", filepath.Join(root, "plugin-ui.ts")},
		{"src/main/index.ts", filepath.Join(root, "src/main/index.ts")},
		{"../../agent/src/loader.ts", filepath.Join(root, "../../agent/src/loader.ts")},
		{"bun://~BUN/plugin-ui.ts", filepath.Join(root, "plugin-ui.ts")},
		{absParent, absParent},
	}
	for _, c := range cases {
		if got := mapEmbeddedParentToDisk(root, c.parent); got != c.want {
			t.Errorf("map(%q) = %q, want %q", c.parent, got, c.want)
		}
	}
	if got := mapEmbeddedParentToDisk("", "plugin-ui.ts"); got != "" {
		t.Errorf("empty rootDir: got %q, want empty", got)
	}
}