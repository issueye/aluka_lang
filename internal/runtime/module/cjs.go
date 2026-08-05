package module

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// loadCJS loads and executes a CommonJS module.
//
// 实现（P0-1）：将模块源码包装为带模块作用域参数的函数
//   (function(require, module, exports, __filename, __dirname, __import) { SRC })
// 并以此为词法参数调用。这样 require/module 等是包装函数的局部参数，
// 模块内的普通函数/箭头函数/async 函数闭包经 upvalue 链捕获它们，
// 异步恢复后依然可用（修复原"全局属性 + save/restore"方案在 await 后
// 丢失 require 的缺陷）。
func (l *Loader) loadCJS(absPath string) (engine.Value, error) {
	// Check cache (double-check after lock released)
	l.mu.Lock()
	if cached, ok := l.cache[absPath]; ok {
		l.mu.Unlock()
		return cached, nil
	}
	l.mu.Unlock()

	src, err := os.ReadFile(absPath)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("module: cannot read %q: %w", absPath, err)
	}

	// Create module and exports objects
	exports := engine.NewObject()
	moduleObj := engine.NewObject()
	_ = moduleObj.Set("exports", exports)
	_ = moduleObj.Set("id", engine.Str(absPath))
	_ = moduleObj.Set("filename", engine.Str(absPath))
	_ = moduleObj.Set("loaded", engine.Boolean(false))
	_ = moduleObj.Set("path", engine.Str(filepath.Dir(absPath)))

	// Pre-populate cache with the initial exports object to handle
	// circular dependencies (Node returns the current module.exports).
	l.mu.Lock()
	l.cache[absPath] = exports
	l.mu.Unlock()

	vm, ok := l.ctx.(*interpreter.VM)
	if !ok {
		// 非 VM 引擎（AST 解释器）：退化为旧的全局 save/restore 方案。
		return l.loadCJSViaGlobals(absPath, string(src), moduleObj, exports)
	}

	// 包装源码为模块函数（P0-1），保持行号与缓存键稳定（包装为固定前缀/后缀）。
	wrapped := wrapCJSSource(string(src))
	var evalErr error
	var wrapper engine.Value
	if mod, err := l.bcCache.compileOrLoad(absPath, func() (*bytecode.Module, error) {
		return vm.Compile(wrapped, absPath)
	}); err != nil {
		evalErr = err
	} else {
		wrapper, evalErr = vm.RunModule(mod)
	}

	if evalErr != nil {
		l.mu.Lock()
		delete(l.cache, absPath)
		l.mu.Unlock()
		return engine.Undefined(), fmt.Errorf("module: error in %q: %w", absPath, evalErr)
	}

	// 以词法参数调用模块函数：require/module/exports/__filename/__dirname/__import。
	// `this` 绑定为 module.exports（CJS 顶层 this 语义）。
	requireFn := l.makeRequireFunc(absPath)
	importFn := l.makeImportFunc(absPath)
	_, evalErr = vm.InvokeFn(wrapper, exports, []engine.Value{
		requireFn,
		moduleObj,
		exports,
		engine.Str(absPath),
		engine.Str(filepath.Dir(absPath)),
		importFn,
	})
	// 模块函数执行完毕后在顶层排空微任务队列（Promise reactions/async 继续），
	// 使 import().then(...) 等顶层异步回调在 loadCJS 返回前完成（P0-1）。
	vm.DrainMicrotasks()
	if evalErr != nil {
		l.mu.Lock()
		delete(l.cache, absPath)
		l.mu.Unlock()
		return engine.Undefined(), fmt.Errorf("module: error in %q: %w", absPath, evalErr)
	}

	// Get the final module.exports (may have been reassigned).
	// 注意：module.exports 可能被 Object.defineProperty 重定义为 getter 访问器
	// （如 ansi-styles）。Go 侧 moduleObj.Get 不触发 JS getter，故在模块源码
	// 末尾注入导出语句，让 getter 在原 module 上下文求值。
	var finalExports engine.Value
	finalExports = exports
	// 尝试经 moduleObj.Get 读取；若结果是 AccessorValue（含 get/set），说明
	// exports 被重定义为访问器（如 ansi-styles 的 Object.defineProperty(module,
	// 'exports', {get})）——经 VM 调用 getter 取真实值。
	if v, err := moduleObj.Get("exports"); err == nil && v != nil {
		if acc, ok := v.(*engine.AccessorValue); ok {
			if !acc.Getter.IsUndefined() {
				if gv, gerr := vm.InvokeFn(acc.Getter, moduleObj, nil); gerr == nil {
					finalExports = gv
				}
			}
		} else if !v.IsUndefined() && !v.IsNull() {
			finalExports = v
		}
	}

	// Mark as loaded
	_ = moduleObj.Set("loaded", engine.Boolean(true))

	// Update cache with final exports
	l.mu.Lock()
	l.cache[absPath] = finalExports
	l.mu.Unlock()

	return finalExports, nil
}

// wrapCJSSource 将模块源码包装为带模块作用域参数的函数表达式。
// 包装为常量前缀/后缀，保证字节码缓存的键（源文件 mtime/size）稳定。
func wrapCJSSource(src string) string {
	const prefix = "(function(require, module, exports, __filename, __dirname, __import) {\n"
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
	require   engine.Value
	module    engine.Value
	exports   engine.Value
	filename  engine.Value
	dirname   engine.Value
	importFn  engine.Value
	hasRequire  bool
	hasModule   bool
	hasExports  bool
	hasFilename bool
	hasDirname  bool
	hasImport   bool
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
}
