package vue

import (
	"fmt"
	"hash/fnv"
	"path/filepath"

	"github.com/aluka-lang/aluka/internal/builtin"
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// officialDriver 在 VM 内驱动 vue/compiler-sfc。输入经 globalThis 传入
// （__sfcSrc/__sfcName/__sfcId/__sfcNS），结果经 __sfcResult 回传：
// { code } 或 { error }。驱动器自身不 import——命名空间由 Go 侧经
// Loader.RequireModule 预加载（同进程只执行一次依赖链）。
const officialDriver = `(function () {
  var ns = globalThis.__sfcNS;
  var r = ns.parse(globalThis.__sfcSrc, { filename: globalThis.__sfcName });
  if (r.errors && r.errors.length) {
    globalThis.__sfcResult = { error: String(r.errors[0].message || r.errors[0]) };
    return;
  }
  var d = r.descriptor;
  if (d.styles && d.styles.length) {
    globalThis.__sfcResult = { error: "official backend: <style> blocks are not wired into the graph asset pipeline; use an entry CSS file" };
    return;
  }
  if (d.customBlocks && d.customBlocks.length) {
    globalThis.__sfcResult = { error: "official backend: custom SFC blocks are not supported" };
    return;
  }
  var id = globalThis.__sfcId;
  var script = "";
  var bindings;
  if (d.script || d.scriptSetup) {
    var s = ns.compileScript(d, { id: id, inlineTemplate: false });
    script = s.content;
    bindings = s.bindings;
  }
  var tpl = "";
  if (d.template) {
    var t = ns.compileTemplate({
      source: d.template.content,
      filename: globalThis.__sfcName,
      id: id,
      compilerOptions: { bindingMetadata: bindings }
    });
    if (t.errors && t.errors.length) {
      globalThis.__sfcResult = { error: String(t.errors[0].message || t.errors[0]) };
      return;
    }
    tpl = t.code;
  }
  // Vite 同款挂接：用 compiler-sfc 官方 rewriteDefault 做 AST 感知的默认
  // 导出改写（不能用 lastIndexOf 文本替换——注释/字符串里可能含同名文本）。
  // 编译出的 render 挂到组件后再导出，否则选项式组件无 render，运行时
  // 静默渲染为空占位节点。
  var code = "";
  if (tpl) {
    code += script ? ns.rewriteDefault(script, "__sfc__") : "const __sfc__ = {};";
    code += "\n" + tpl + "\n__sfc__.render = render;\nexport default __sfc__;";
  } else {
    code += script || "export default {};";
  }
  globalThis.__sfcResult = { code: code };
})();`

// OfficialCompiler 是官方 @vue/compiler-sfc 后端：构建期在自研 VM 内执行
// 依赖包的编译器（纯 JS、无外部工具链）。vm 必须与 graph 构建共用，
// entryPath 提供 node_modules 解析基准。
type OfficialCompiler struct {
	vm        *interpreter.VM
	entryPath string
	inited    bool
	initErr   error
}

// NewOfficialCompiler 创建官方后端（惰性初始化：首个 Transform 时加载
// vue/compiler-sfc 依赖链）。
func NewOfficialCompiler(vm *interpreter.VM, entryPath string) *OfficialCompiler {
	return &OfficialCompiler{vm: vm, entryPath: entryPath}
}

// Transform 经官方 compiler-sfc 编译 SFC。JS 侧异常映射为带文件名的
// 构建错误（不静默回退 subset）。
func (c *OfficialCompiler) Transform(src, name string) (string, error) {
	if err := c.init(); err != nil {
		return "", err
	}
	g := c.vm.Global()
	// 每次编译的输入/输出都可能很大；所有返回路径（含异常）统一清理，
	// 防 watch/多 SFC 构建在 globalThis 上累计保活源码与产物字符串。
	defer func() {
		for _, key := range []string{"__sfcSrc", "__sfcName", "__sfcId", "__sfcResult"} {
			_ = g.Delete(key)
		}
	}()
	id := sfcScopeID(name)
	_ = g.Set("__sfcSrc", engine.Str(src))
	_ = g.Set("__sfcName", engine.Str(name))
	_ = g.Set("__sfcId", engine.Str(id))
	if _, err := c.vm.Eval(officialDriver, "aluka-official-sfc-driver.js"); err != nil {
		return "", fmt.Errorf("vue: official compiler failed for %s: %w", name, err)
	}
	res, err := g.Get("__sfcResult")
	if err != nil {
		return "", fmt.Errorf("vue: official compiler result unavailable for %s: %w", name, err)
	}
	obj, ok := res.AsObject()
	if !ok {
		return "", fmt.Errorf("vue: official compiler result invalid for %s", name)
	}
	if e, _ := obj.Get("error"); e != nil && !e.IsUndefined() && !e.IsNull() {
		return "", fmt.Errorf("vue: %s: %s", name, e.String())
	}
	code, _ := obj.Get("code")
	return code.String(), nil
}

// init 加载 vue/compiler-sfc 命名空间到 globalThis.__sfcNS（一次）。
// Loader 显式 SetNoCache：web 构建不写字节码磁盘缓存（webbuild gate 约束）。
func (c *OfficialCompiler) init() error {
	if c.inited {
		return c.initErr
	}
	c.inited = true
	c.installBuildGlobals()
	// require 的解析基准是"父模块文件路径"（resolver 从其所在目录向上
	// 爬 node_modules），传入口文件本身而非目录。
	parent, err := filepath.Abs(c.entryPath)
	if err != nil {
		c.initErr = fmt.Errorf("vue: resolve entry path: %w", err)
		return c.initErr
	}
	loader := module.NewLoader(c.vm)
	loader.SetNoCache(true)
	// 编译器依赖链 require Node 内置模块（path/util/...），与 CLI run 流程
	// 同一套注册。
	builtin.RegisterAll(loader)
	ns, err := loader.RequireModule("vue/compiler-sfc", parent)
	if err != nil {
		c.initErr = fmt.Errorf("vue: load vue/compiler-sfc (official backend): %w", err)
		return c.initErr
	}
	if err := c.vm.Global().Set("__sfcNS", ns); err != nil {
		c.initErr = fmt.Errorf("vue: install compiler namespace: %w", err)
		return c.initErr
	}
	return nil
}

// installBuildGlobals 为构建 VM 补齐编译器依赖链所需的最小全局。
// web 构建 VM 是裸的（runtime globals 只在 run 流程注册），而 compiler-sfc
// 的 CJS 构建直接读 process.env.NODE_ENV；console 缺失时编译警告会崩。
func (c *OfficialCompiler) installBuildGlobals() {
	g := c.vm.Global()
	if p, err := g.Get("process"); err != nil || p == nil || p.IsUndefined() {
		env := engine.NewObject()
		_ = env.Set("NODE_ENV", engine.Str("production"))
		proc := engine.NewObject()
		_ = proc.Set("env", env)
		_ = proc.Set("version", engine.Str("v22.0.0"))
		_ = g.Set("process", proc)
	}
	if cv, err := g.Get("console"); err != nil || cv == nil || cv.IsUndefined() {
		console := engine.NewObject()
		for _, name := range []string{"log", "warn", "error", "info"} {
			_ = console.Set(name, engine.NewFunction(name, func(args []engine.Value) (engine.Value, error) {
				return engine.Undefined(), nil
			}))
		}
		_ = g.Set("console", console)
	}
}

// sfcScopeID 生成 scoped CSS / 模板匹配用的稳定 id（与 Vite 的 hash 用途
// 一致，不必跨构建稳定）。
func sfcScopeID(name string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return fmt.Sprintf("%08x", h.Sum32())
}
