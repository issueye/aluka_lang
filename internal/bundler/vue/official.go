package vue

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strconv"

	"github.com/aluka-lang/aluka/internal/builtin"
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

// officialDriver 在 VM 内驱动 vue/compiler-sfc。输入经 globalThis 传入，结果经
// __sfcResult 回传。script/template 分别返回，facade 由 Go 侧组装为独立模块。
const officialDriver = `(function () {
  function locationOf(value, fallback) {
    var loc = value && value.loc ? value.loc : fallback;
    if (loc && loc.start) loc = loc.start;
    return {
      line: Number(loc && loc.line) || 0,
      column: Number(loc && loc.column) || 0
    };
  }
  function fail(value, fallback, prefix, relativeTo) {
    var loc = locationOf(value, fallback);
    if (relativeTo && loc.line) {
      var base = locationOf(null, relativeTo);
      if (loc.line === 1 && base.column) loc.column += base.column - 1;
      loc.line += base.line - 1;
    }
    var message = value && value.message ? value.message : String(value);
    globalThis.__sfcResult = {
      diagnostic: {
        message: (prefix || "") + message,
        line: loc.line,
        column: loc.column
      }
    };
  }
  try {
    var ns = globalThis.__sfcNS;
    var r = ns.parse(globalThis.__sfcSrc, { filename: globalThis.__sfcName });
    if (r.errors && r.errors.length) {
      fail(r.errors[0], null, "");
      return;
    }
    var d = r.descriptor;
    if (d.script && d.script.src) {
      fail("official backend does not support external <script src>", d.script.loc, "");
      return;
    }
    if (d.scriptSetup && d.scriptSetup.src) {
      fail("official backend does not support external <script setup src>", d.scriptSetup.loc, "");
      return;
    }
    if (d.template && d.template.src) {
      fail("official backend does not support external <template src>", d.template.loc, "");
      return;
    }
    if (d.styles && d.styles.length) {
      fail("official backend: <style> blocks are not wired into the graph asset pipeline; use an entry CSS file", d.styles[0].loc, "");
      return;
    }
    if (d.customBlocks && d.customBlocks.length) {
      fail("official backend: custom SFC blocks are not supported", d.customBlocks[0].loc, "");
      return;
    }
    var id = globalThis.__sfcId;
    var script = "const __sfc__ = {};\nexport default __sfc__;";
    var scriptLang = "js";
    var bindings;
    if (d.script || d.scriptSetup) {
      try {
        var s = ns.compileScript(d, {
          id: id,
          inlineTemplate: false,
          genDefaultAs: "__sfc__"
        });
        script = s.content + "\nexport default __sfc__;";
        bindings = s.bindings;
        var lang = (d.scriptSetup && d.scriptSetup.lang) || (d.script && d.script.lang) || "js";
        if (lang === "ts" || lang === "tsx") scriptLang = lang;
      } catch (e) {
        fail(e, null, "", d.scriptSetup ? d.scriptSetup.loc : d.script.loc);
        return;
      }
    }
    var template = "";
    if (d.template) {
      var t = ns.compileTemplate({
        source: d.template.content,
        filename: globalThis.__sfcName,
        id: id,
        compilerOptions: { bindingMetadata: bindings }
      });
      if (t.errors && t.errors.length) {
        fail(t.errors[0], d.template.loc, "", d.template.loc);
        return;
      }
      template = t.code;
    }
    globalThis.__sfcResult = {
      script: script,
      scriptLang: scriptLang,
      template: template
    };
  } catch (e) {
    fail(e, null, "official compiler failed: ");
  }
})();`

// Diagnostic 是 official SFC 编译器返回的结构化源码诊断。
type Diagnostic struct {
	Filename string
	Line     int
	Column   int
	Message  string
}

func (d *Diagnostic) Error() string {
	if d.Line > 0 && d.Column > 0 {
		return fmt.Sprintf("vue: %s:%d:%d: %s", d.Filename, d.Line, d.Column, d.Message)
	}
	if d.Line > 0 {
		return fmt.Sprintf("vue: %s:%d: %s", d.Filename, d.Line, d.Message)
	}
	return fmt.Sprintf("vue: %s: %s", d.Filename, d.Message)
}

// OfficialCompiler 是官方 @vue/compiler-sfc 后端：构建期在自研 VM 内执行
// 依赖包的编译器（纯 JS、无外部工具链）。loader 属于该构建实例并被明确
// 保活，使 compiler-sfc 的依赖缓存可由同一构建内的所有 SFC 复用。
type OfficialCompiler struct {
	vm        *interpreter.VM
	entryPath string
	loader    *module.Loader
	inited    bool
	initErr   error
}

// NewOfficialCompiler 创建官方后端（惰性初始化：首个 Compile 时加载
// vue/compiler-sfc 依赖链）。
func NewOfficialCompiler(vm *interpreter.VM, entryPath string) *OfficialCompiler {
	return &OfficialCompiler{vm: vm, entryPath: entryPath}
}

// Compile 经官方 compiler-sfc 编译 SFC。script/template/facade 分属独立模块，
// TypeScript script 交给 graph 的 TS 前端处理，不做不安全的默认导出文本改写。
func (c *OfficialCompiler) Compile(src, name string) (*CompileResult, error) {
	if err := c.init(); err != nil {
		return nil, err
	}
	g := c.vm.Global()
	defer func() {
		for _, key := range []string{"__sfcSrc", "__sfcName", "__sfcId", "__sfcResult"} {
			_ = g.Delete(key)
		}
	}()
	_ = g.Set("__sfcSrc", engine.Str(src))
	_ = g.Set("__sfcName", engine.Str(name))
	_ = g.Set("__sfcId", engine.Str(sfcScopeID(name)))
	if _, err := c.vm.Eval(officialDriver, "aluka-official-sfc-driver.js"); err != nil {
		return nil, fmt.Errorf("vue: official compiler failed for %s: %w", name, err)
	}
	res, err := g.Get("__sfcResult")
	if err != nil {
		return nil, fmt.Errorf("vue: official compiler result unavailable for %s: %w", name, err)
	}
	obj, ok := res.AsObject()
	if !ok {
		return nil, fmt.Errorf("vue: official compiler result invalid for %s", name)
	}
	if dv, _ := obj.Get("diagnostic"); dv != nil && !dv.IsUndefined() && !dv.IsNull() {
		diagnostic, ok := dv.AsObject()
		if !ok {
			return nil, fmt.Errorf("vue: official compiler diagnostic invalid for %s", name)
		}
		message, _ := diagnostic.Get("message")
		lineValue, _ := diagnostic.Get("line")
		columnValue, _ := diagnostic.Get("column")
		line, _ := lineValue.Int()
		column, _ := columnValue.Int()
		return nil, &Diagnostic{
			Filename: name,
			Line:     line,
			Column:   column,
			Message:  message.String(),
		}
	}

	script, _ := obj.Get("script")
	scriptLang, _ := obj.Get("scriptLang")
	template, _ := obj.Get("template")
	base := filepath.Base(filepath.FromSlash(name))
	scriptExt := ".js"
	switch scriptLang.String() {
	case "ts":
		scriptExt = ".ts"
	case "tsx":
		scriptExt = ".tsx"
	}
	scriptName := base + ".__aluka_script" + scriptExt
	templateName := base + ".__aluka_template.js"
	scriptSpec := "./" + filepath.ToSlash(scriptName)
	templateSpec := "./" + filepath.ToSlash(templateName)

	facade := "import __sfc__ from " + strconv.Quote(scriptSpec) + ";\n" +
		"export * from " + strconv.Quote(scriptSpec) + ";\n"

	modules := []GeneratedModule{{Name: scriptName, Source: script.String()}}
	if template.String() != "" {
		facade += "import { render as __sfc_render__ } from " + strconv.Quote(templateSpec) + ";\n" +
			"__sfc__.render = __sfc_render__;\n"
		modules = append(modules, GeneratedModule{Name: templateName, Source: template.String()})
	}
	facade += "export default __sfc__;\n"
	return &CompileResult{Facade: facade, Modules: modules}, nil
}

// init 加载 vue/compiler-sfc 命名空间到 globalThis.__sfcNS（一次）。
// Loader 显式 SetNoCache：web 构建不写字节码磁盘缓存（webbuild gate 约束）。
func (c *OfficialCompiler) init() error {
	if c.inited {
		return c.initErr
	}
	c.inited = true
	c.installBuildGlobals()
	parent, err := filepath.Abs(c.entryPath)
	if err != nil {
		c.initErr = fmt.Errorf("vue: resolve entry path: %w", err)
		return c.initErr
	}
	c.loader = module.NewLoader(c.vm)
	c.loader.SetNoCache(true)
	builtin.RegisterAll(c.loader)
	ns, err := c.loader.RequireModule("vue/compiler-sfc", parent)
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

// sfcScopeID 生成 scoped CSS / 模板匹配用的稳定 id。
func sfcScopeID(name string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return fmt.Sprintf("%08x", h.Sum32())
}
