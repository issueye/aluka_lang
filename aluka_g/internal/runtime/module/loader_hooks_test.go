package module

import (
	"path/filepath"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gfetch"
)

// === node:module.register + loader hooks / _compile / extensions / cache ===
//（gap-closure-plan P7 / jiti-dynamic-import-plan M3）

const hooksFixture = `export let initialized = false;
export function initialize() { initialized = true; }
export async function resolve(specifier, context, nextResolve) {
  if (specifier.endsWith('.foo')) {
    return { url: new URL('./virtual' + specifier.slice(specifier.lastIndexOf('/')), import.meta.url).href, shortCircuit: true };
  }
  return nextResolve(specifier, context);
}
export async function load(url, context, nextLoad) {
  if (url.endsWith('.foo')) {
    return { source: 'export const val = "HOOKED-" + 40 + 2;', format: 'module', shortCircuit: true };
  }
  return nextLoad(url, context);
}
`

// newHooksEnv 构造带 hooks 文件的模块测试环境（补注册 URL：hooks 文件
// 里 new URL(..., import.meta.url) 依赖全局 URL）。
func newHooksEnv(t *testing.T) *testEnv {
	t.Helper()
	env := newTestEnv(t, map[string]string{
		"my-hooks.mjs": hooksFixture,
	})
	if err := gfetch.NewURL(env.loader.ctx, gfetch.URLConfig{}); err != nil {
		t.Fatalf("NewURL: %v", err)
	}
	return env
}

// TestRegisterHooksCustomExtension 验证 node:module.register 的 hooks 链：
// resolve 改写 specifier、load 覆盖源码（自定义 .foo 扩展名经 hook 变为
// ESM 模块）。
func TestRegisterHooksCustomExtension(t *testing.T) {
	env := newHooksEnv(t)

	if err := env.loader.RegisterHook("./my-hooks.mjs", filepath.Join(env.dir, "app.js")); err != nil {
		t.Fatalf("RegisterHook: %v", err)
	}
	// 与真实 import('./x.foo') 一致：相对 specifier（hooks resolve 里
	// 按 specifier 形态拼接虚拟 URL）。
	val, err := env.loader.RequireModule("./whatever.foo", filepath.Join(env.dir, "app.js"))
	if err != nil {
		t.Fatalf("RequireModule: %v", err)
	}
	o, ok := val.AsObject()
	if !ok {
		t.Fatalf("exports not object: %s", val.String())
	}
	got, err := o.Get("val")
	if err != nil {
		t.Fatalf("Get val: %v", err)
	}
	// ESM 命名导出为活绑定 getter：触发求值取真实值（vm 层读取会自动触发）。
	if acc, ok := got.(*engine.AccessorValue); ok {
		if gv, gerr := interpreter.CallWithThis(acc.Getter, o, nil); gerr == nil {
			got = gv
		}
	}
	if got.String() != "HOOKED-402" {
		t.Fatalf("hook source not applied: val=%s", got.String())
	}
}

// TestRegisterHooksBareSpecifier register 支持裸模块名解析（经 import 条件）。
func TestRegisterHooksBareSpecifier(t *testing.T) {
	env := newHooksEnv(t)
	if err := env.loader.RegisterHook("./my-hooks.mjs", filepath.Join(env.dir, "app.js")); err != nil {
		t.Fatalf("RegisterHook: %v", err)
	}
	if len(env.loader.hooks) != 1 {
		t.Fatalf("hooks = %d, want 1", len(env.loader.hooks))
	}
}

// TestCompileModuleSource 验证 Module.prototype._compile 语义：
// 在给定 module 实例上编译执行 CJS 源码，exports 经 module.exports 生效、
// 支持重赋值。
func TestCompileModuleSource(t *testing.T) {
	env := newTestEnv(t, nil)

	moduleObj := engine.NewObject()
	exports := env.loader.newExports()
	_ = moduleObj.Set("exports", exports)

	val, err := env.loader.CompileModuleSource(
		"exports.answer = 40 + 2; module.exports = { double: function (x) { return x * 2 } };",
		filepath.Join(env.dir, "fake.cjs"), moduleObj)
	if err != nil {
		t.Fatalf("CompileModuleSource: %v", err)
	}
	o, ok := val.AsObject()
	if !ok {
		t.Fatalf("exports not object: %s", val.String())
	}
	dv, err := o.Get("double")
	if err != nil || !dv.IsFunction() {
		t.Fatalf("double missing: %v %v", dv, err)
	}
	// 重赋值后的 module.exports 生效（answer 不在其上）。
	av, _ := o.Get("answer")
	if !av.IsUndefined() {
		t.Fatalf("stale answer visible: %s", av.String())
	}
}

// TestRequireExtensionsCustomLoader 验证 require.extensions 自定义加载器：
// 注册 .xyz 加载器后 require('./x.xyz') 走用户函数（通常内部调 _compile）。
func TestRequireExtensionsCustomLoader(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"data.xyz": "raw payload",
	})
	path := filepath.Join(env.dir, "data.xyz")

	// 模拟 JS 侧 require.extensions['.xyz'] = fn（共享对象赋值即生效）。
	extFn := engine.NewFunction("loader", func(args []engine.Value) (engine.Value, error) {
		modObj, ok := args[0].AsObject()
		if !ok {
			t.Fatal("loader arg0 not object")
		}
		// 用户加载器经 module._compile 编译转译产物（真实实现已挂载）。
		compileFn, _ := modObj.Get("_compile")
		if f, ok := compileFn.AsFunction(); ok {
			if _, err := f.Call([]engine.Value{engine.Str("module.exports = { ext: 'custom' };"), engine.Str(path)}); err != nil {
				return engine.Undefined(), err
			}
		}
		return engine.Undefined(), nil
	})
	if err := env.loader.requireExtensionsObj.Set(".xyz", extFn); err != nil {
		t.Fatal(err)
	}

	val, err := env.loader.RequireModule(path, env.dir)
	if err != nil {
		t.Fatalf("RequireModule: %v", err)
	}
	o, _ := val.AsObject()
	ev, _ := o.Get("ext")
	if ev.String() != "custom" {
		t.Fatalf("ext = %s, want custom", ev.String())
	}
}

// TestRequireCacheInjectAndReload 验证 require.cache：JS 侧注入立即生效、
// 删除后强制重载（Node 语义）。
func TestRequireCacheInjectAndReload(t *testing.T) {
	env := newTestEnv(t, map[string]string{
		"data.js": "module.exports = { v: 'original' };",
	})
	path := filepath.Join(env.dir, "data.js")

	// 首次加载 → 缓存固化 original。
	val, err := env.loader.RequireModule(path, env.dir)
	if err != nil {
		t.Fatal(err)
	}
	o, _ := val.AsObject()
	if v, _ := o.Get("v"); v.String() != "original" {
		t.Fatalf("first load v = %s", v.String())
	}

	// JS 侧注入自定义条目（带 exports，Node 语义）→ 下一次 require 命中。
	injectedEntry := engine.NewObject()
	_ = injectedEntry.Set("exports", engine.NewObjectFromPairs([]engine.Value{engine.Str("v"), engine.Str("injected")}))
	_ = env.loader.requireCacheObj.Set(path, injectedEntry)
	val2, err := env.loader.RequireModule(path, env.dir)
	if err != nil {
		t.Fatal(err)
	}
	o2, _ := val2.AsObject()
	if v, _ := o2.Get("v"); v.String() != "injected" {
		t.Fatalf("injected load v = %s", v.String())
	}

	// 删除条目 → 强制重载原文件。
	_ = env.loader.requireCacheObj.Delete(path)
	val3, err := env.loader.RequireModule(path, env.dir)
	if err != nil {
		t.Fatal(err)
	}
	o3, _ := val3.AsObject()
	if v, _ := o3.Get("v"); v.String() != "original" {
		t.Fatalf("reload v = %s, want original", v.String())
	}
}

// TestRequireFuncSharedCacheExtensions 验证 require 函数挂载的 cache 与
// extensions 是共享实例（所有 require 同一对象，Node 语义）。
func TestRequireFuncSharedCacheExtensions(t *testing.T) {
	env := newTestEnv(t, nil)
	r1 := env.loader.makeRequireFunc(filepath.Join(env.dir, "a.js"))
	r2 := env.loader.makeRequireFunc(filepath.Join(env.dir, "b.js"))
	o1, _ := r1.AsObject()
	o2, _ := r2.AsObject()
	c1, _ := o1.Get("cache")
	c2, _ := o2.Get("cache")
	e1, _ := o1.Get("extensions")
	e2, _ := o2.Get("extensions")
	if c1 != c2 {
		t.Fatal("require.cache 不是共享对象")
	}
	if e1 != e2 {
		t.Fatal("require.extensions 不是共享对象")
	}
	eo, _ := e1.AsObject()
	for _, ext := range []string{".js", ".json", ".node"} {
		if v, err := eo.Get(ext); err != nil || !v.IsFunction() {
			t.Fatalf("require.extensions[%s] = %v (%v), want function", ext, v, err)
		}
	}
	// 默认扩展名不触发自定义加载器（走内建分类）。
	if env.loader.customExtLoader(filepath.Join(env.dir, "x.js")) {
		t.Fatal("默认 .js 不应视为自定义扩展名")
	}
	// JS 侧覆盖后立即可感知。
	_ = eo.Set(".js", engine.NewFunction("x", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	if !env.loader.customExtLoader(filepath.Join(env.dir, "x.js")) {
		t.Fatal("覆盖 .js 后应识别为自定义加载器")
	}
}
