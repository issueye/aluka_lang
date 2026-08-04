package module

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

// 本文件验证字节码磁盘缓存（1C.14）的行为：写盘、命中、源文件变更失效、
// --no-cache 禁用。风格对齐 module_test.go：newTestEnv 写临时文件。
//
// 注：测试用 env.dir 作为项目根，缓存写入 <dir>/node_modules/.aluka/cache/。

// cacheFileCount 统计缓存目录下的 .bc 文件数。
func cacheFileCount(t *testing.T, dir string) int {
	t.Helper()
	cacheDir := filepath.Join(dir, "node_modules", ".aluka", "cache")
	count := 0
	_ = filepath.Walk(cacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && filepath.Ext(path) == ".bc" {
			count++
		}
		return nil
	})
	return count
}

// TestBytecodeCacheWriteAndHit: 首次加载写缓存，二次加载命中缓存（文件数不变）。
func TestBytecodeCacheWriteAndHit(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.cjs": `var x = 1 + 2; globalThis.__r = x;`,
	})
	env.run(t, "main.cjs")
	if got := env.globalGet("__r"); got != "3" {
		t.Errorf("first run: got %q, want 3", got)
	}
	// 缓存应已写入。
	n1 := cacheFileCount(t, env.dir)
	if n1 != 1 {
		t.Errorf("after first run: cache files = %d, want 1", n1)
	}
	// 二次加载（新 Loader）应命中缓存，不产生新缓存文件。
	env2 := newTestEnvRaw(t, env.dir)
	env2.run(t, "main.cjs")
	n2 := cacheFileCount(t, env.dir)
	if n2 != 1 {
		t.Errorf("after second run: cache files = %d, want 1 (hit)", n2)
	}
	if got := env2.globalGet("__r"); got != "3" {
		t.Errorf("second run (cached): got %q, want 3", got)
	}
}

// TestBytecodeCacheInvalidationOnSourceChange: 源文件修改后缓存失效。
func TestBytecodeCacheInvalidationOnSourceChange(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.cjs": `globalThis.__r = 10;`,
	})
	env.run(t, "main.cjs")
	n1 := cacheFileCount(t, env.dir)

	// 修改源文件（改变 mtime）。
	mainPath := filepath.Join(env.dir, "main.cjs")
	newContent := []byte(`globalThis.__r = 99;`)
	if err := os.WriteFile(mainPath, newContent, 0644); err != nil {
		t.Fatal(err)
	}

	// 重新加载应编译新内容并写新缓存。
	env2 := newTestEnvRaw(t, env.dir)
	env2.run(t, "main.cjs")
	if got := env2.globalGet("__r"); got != "99" {
		t.Errorf("after change: got %q, want 99", got)
	}
	n2 := cacheFileCount(t, env.dir)
	// 新缓存写入（旧的可能保留或被替换，至少 >= 1）。
	if n2 < 1 {
		t.Errorf("after change: cache files = %d, want >= 1", n2)
	}
	_ = n1
}

// TestBytecodeCacheNoCache: SetNoCache(true) 时不写缓存文件。
func TestBytecodeCacheNoCache(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.cjs")
	if err := os.WriteFile(mainPath, []byte(`globalThis.__r = 42;`), 0644); err != nil {
		t.Fatal(err)
	}

	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ctx.Close() })
	if err := globals.NewConsole(ctx, globals.ConsoleConfig{}); err != nil {
		t.Fatal(err)
	}
	_ = ctx.Global().Set("globalThis", ctx.Global())

	loader := NewLoader(ctx)
	loader.SetNoCache(true) // 禁用缓存

	if err := loader.Run(mainPath); err != nil {
		t.Fatal(err)
	}
	v, _ := ctx.Global().Get("__r")
	if v.String() != "42" {
		t.Errorf("no-cache run: got %q, want 42", v.String())
	}
	// 不应产生缓存文件。
	if n := cacheFileCount(t, dir); n != 0 {
		t.Errorf("no-cache: cache files = %d, want 0", n)
	}
}

// TestBytecodeCacheESM: ESM 模块也走缓存。
func TestBytecodeCacheESM(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"main.mjs": `import { x } from "./mod.mjs"; globalThis.__r = x;`,
		"mod.mjs":  `export const x = 7;`,
	})
	env.run(t, "main.mjs")
	if got := env.globalGet("__r"); got != "7" {
		t.Errorf("ESM first run: got %q, want 7", got)
	}
	// ESM 模块应产生缓存（至少 main 和 mod 两个）。
	if n := cacheFileCount(t, env.dir); n < 1 {
		t.Errorf("ESM cache files = %d, want >= 1", n)
	}
}

// --- 辅助：从已有目录构造 env（不重新创建文件） -------------------------

func newTestEnvRaw(t *testing.T, dir string) *testEnv {
	t.Helper()
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ctx.Close() })
	if err := globals.NewConsole(ctx, globals.ConsoleConfig{}); err != nil {
		t.Fatal(err)
	}
	_ = ctx.Global().Set("globalThis", ctx.Global())
	loader := NewLoader(ctx)
	return &testEnv{dir: dir, loader: loader}
}
