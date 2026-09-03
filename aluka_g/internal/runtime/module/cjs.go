package module

import (
	"fmt"
	"path/filepath"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// loadCJS loads and executes a CommonJS module.
func (l *Loader) loadCJS(absPath string) (engine.Value, error) {
	return l.loadModuleFile(absPath)
}

// WrapCJSSource 将模块源码包装为带模块作用域参数的函数表达式
// （构建产物模式复用，产物在执行前已包装）。
// 包装为常量前缀/后缀，保证字节码缓存的键（源文件 mtime/size）稳定。
// 参数与 ESM 包装对齐：__importMeta（import.meta lower 目标）在 CJS 中
// 同样可用（Bun 兼容语义；Node 会在 parse 期拒绝 import.meta）。
func WrapCJSSource(src string) string {
	const prefix = "(function(require, module, exports, __filename, __dirname, __import, __importMeta) {\n"
	const suffix = "\n});\n"
	return prefix + src + suffix
}

// loadCJSViaGlobals 非 VM 引擎（AST 解释器）的 CJS 加载：沿用旧的
// 全局属性 + save/restore 方案（受限于 AST 解释器不支持函数参数包装）。
func (l *Loader) loadCJSViaGlobals(absPath, src string, moduleObj engine.Object, exports engine.Value) (engine.Value, error) {
	oldGlobals := l.saveGlobals(absPath)
	l.setGlobals(absPath, moduleObj, exports)
	_, evalErr := l.ctx.Eval(src, absPath)
	l.restoreGlobals(oldGlobals)
	if evalErr != nil {
		l.mu.Lock()
		delete(l.cache, absPath)
		l.mu.Unlock()
		return engine.Undefined(), fmt.Errorf("module: error in %q: %w", absPath, evalErr)
	}
	finalExports := exports
	if v, err := moduleObj.Get("exports"); err == nil && !v.IsUndefined() && !v.IsNull() {
		finalExports = v
	}
	l.mu.Lock()
	l.cache[absPath] = finalExports
	l.mu.Unlock()
	return finalExports, nil
}

// loadCJSFile is like loadCJS but discards the return value (for Run).
func (l *Loader) loadCJSFile(absPath string) error {
	_, err := l.loadCJS(absPath)
	return err
}

// savedGlobals holds the previous values of module-scoped globals.
type savedGlobals struct {
	require     engine.Value
	module      engine.Value
	exports     engine.Value
	filename    engine.Value
	dirname     engine.Value
	importFn    engine.Value
	metaFn      engine.Value
	hasRequire  bool
	hasModule   bool
	hasExports  bool
	hasFilename bool
	hasDirname  bool
	hasImport   bool
	hasMeta     bool
}

// saveGlobals saves the current values of module-scoped globals.
func (l *Loader) saveGlobals(path string) savedGlobals {
	g := l.ctx.Global()
	var s savedGlobals

	if v, err := g.Get("require"); err == nil && !v.IsUndefined() {
		s.require = v
		s.hasRequire = true
	}
	if v, err := g.Get("module"); err == nil && !v.IsUndefined() {
		s.module = v
		s.hasModule = true
	}
	if v, err := g.Get("exports"); err == nil && !v.IsUndefined() {
		s.exports = v
		s.hasExports = true
	}
	if v, err := g.Get("__filename"); err == nil && !v.IsUndefined() {
		s.filename = v
		s.hasFilename = true
	}
	if v, err := g.Get("__dirname"); err == nil && !v.IsUndefined() {
		s.dirname = v
		s.hasDirname = true
	}
	if v, err := g.Get("__import"); err == nil && !v.IsUndefined() {
		s.importFn = v
		s.hasImport = true
	}
	if v, err := g.Get("__importMeta"); err == nil && !v.IsUndefined() {
		s.metaFn = v
		s.hasMeta = true
	}

	return s
}

// setGlobals sets the module-scoped globals for a CJS module.
func (l *Loader) setGlobals(path string, moduleObj engine.Object, exports engine.Value) {
	g := l.ctx.Global()
	requireFn := l.makeRequireFunc(path)
	_ = g.Set("require", requireFn)
	_ = g.Set("module", moduleObj)
	_ = g.Set("exports", exports)
	_ = g.Set("__filename", engine.Str(path))
	_ = g.Set("__dirname", engine.Str(filepath.Dir(path)))
	// 动态 import()：注入 __import 全局（parser 把 import(spec) lower 成 __import(spec)）。
	_ = g.Set("__import", l.makeImportFunc(path))
	// import.meta：注入 __importMeta 全局（parser 把 import.meta lower 成 __importMeta()）。
	_ = g.Set("__importMeta", l.makeImportMetaFunc(path))
}

// restoreGlobals restores the previous values of module-scoped globals.
func (l *Loader) restoreGlobals(s savedGlobals) {
	g := l.ctx.Global()
	if s.hasRequire {
		_ = g.Set("require", s.require)
	} else {
		g.Delete("require")
	}
	if s.hasModule {
		_ = g.Set("module", s.module)
	} else {
		g.Delete("module")
	}
	if s.hasExports {
		_ = g.Set("exports", s.exports)
	} else {
		g.Delete("exports")
	}
	if s.hasFilename {
		_ = g.Set("__filename", s.filename)
	} else {
		g.Delete("__filename")
	}
	if s.hasDirname {
		_ = g.Set("__dirname", s.dirname)
	} else {
		g.Delete("__dirname")
	}
	if s.hasImport {
		_ = g.Set("__import", s.importFn)
	} else {
		g.Delete("__import")
	}
	if s.hasMeta {
		_ = g.Set("__importMeta", s.metaFn)
	} else {
		g.Delete("__importMeta")
	}
}

// CompileModuleSource 在给定 module 实例上编译并执行 CJS 源码
//（Module.prototype._compile 实现：require.extensions 自定义加载器与
// 工具链的核心入口）。不参与 require 缓存；module.exports 重赋值生效。
func (l *Loader) CompileModuleSource(code, filename string, moduleObj engine.Object) (engine.Value, error) {
	vm, ok := l.ctx.(*interpreter.VM)
	if !ok {
		return engine.Undefined(), fmt.Errorf("module: _compile requires the bytecode VM engine")
	}
	if filename == "" {
		filename = "<anonymous>"
	}
	exportsV, err := moduleObj.Get("exports")
	if err != nil || exportsV.IsUndefined() {
		exportsV = l.newExports()
		_ = moduleObj.Set("exports", exportsV)
	}
	wrapped := WrapCJSSource(code)
	mod, err := vm.Compile(wrapped, filename)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("module: _compile error in %q: %w", filename, err)
	}
	wrapper, err := vm.RunModule(mod)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("module: _compile error in %q: %w", filename, err)
	}
	requireFn := l.makeRequireFunc(filename)
	importFn := l.makeImportFunc(filename)
	args := []engine.Value{
		requireFn, moduleObj, exportsV,
		engine.Str(filename), engine.Str(filepath.Dir(filename)),
		importFn, l.makeImportMetaFunc(filename),
	}
	if _, err := vm.InvokeFn(wrapper, exportsV, args); err != nil {
		return engine.Undefined(), fmt.Errorf("module: _compile error in %q: %w", filename, err)
	}
	vm.DrainMicrotasks()
	// module.exports 可能被重赋值（Node：_compile 后以 module.exports 为准）。
	if v, err := moduleObj.Get("exports"); err == nil && !v.IsUndefined() {
		exportsV = v
	}
	return exportsV, nil
}
