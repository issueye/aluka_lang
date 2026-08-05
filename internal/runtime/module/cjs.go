package module

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// loadCJS loads and executes a CommonJS module.
//
// 实现（P0-1）：将模块源码包装为带模块作用域参数的函数
//
//	(function(require, module, exports, __filename, __dirname, __import) { SRC })
//
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
	// 剥离 UTF-8 BOM。文件开头若有 BOM（常见于 Windows 编辑器），若保留并
	// 被 wrapCJSSource 嵌入到包装函数体中间，lexer 会因 BOM 字符无法前进而
	// 死循环（CPU/内存暴涨）。Node 同样会在编译前剥离 BOM。
	src = stripBOM(src)

	// Create module and exports objects
	exports := l.newExports()
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
		// Node 22 的 module-syntax detection：typeless .js 文件解析为 CJS
		// 失败但含顶层 import/export 语法时，重新按 ESM 加载（Node 22.7+）。
		if hasESMSyntax(string(src)) {
			l.mu.Lock()
			delete(l.cache, absPath)
			l.mu.Unlock()
			return l.loadESM(absPath)
		}
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

// hasESMSyntax 粗略检测源码是否含顶层 import/export 语法（Node 22 的
// module-syntax detection 语义：typeless .js 若含 ESM 语法则按 ESM 加载）。
// 排除动态 import(...)、import.meta、字符串/注释内容及 import/export 前缀标识符
// （如 exported/imported）。对整个文本扫描（minified 单行文件的关键字不在行首）。
func hasESMSyntax(src string) bool {
	cleaned := stripCommentsAndStrings(src)
	for _, kw := range []string{"import", "export"} {
		for i := 0; i+len(kw) <= len(cleaned); {
			j := strings.Index(cleaned[i:], kw)
			if j < 0 {
				break
			}
			pos := i + j
			// 前驱/后继字符必须是边界（非标识符字符），避免 exported 误判。
			beforeOK := pos == 0 || !isIdentChar(cleaned[pos-1])
			after := pos + len(kw)
			afterOK := after >= len(cleaned) || !isIdentChar(cleaned[after])
			if beforeOK && afterOK {
				if kw == "import" {
					// import( 动态导入、import.meta 非静态导入。
					rest := cleaned[after:]
					if !strings.HasPrefix(rest, "(") && !strings.HasPrefix(rest, ".") {
						return true
					}
				} else {
					return true
				}
			}
			i = pos + len(kw)
		}
	}
	return false
}

// isIdentChar 判断字符是否属于标识符字符集。
func isIdentChar(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// hasKeywordPrefix 判断 t 是否以关键字 kw 开头且后随非标识符字符
// （空白、{、*、= 等），避免 exported/imported 误判。
func hasKeywordPrefix(t, kw string) bool {
	if !strings.HasPrefix(t, kw) {
		return false
	}
	if len(t) == len(kw) {
		return true
	}
	c := t[len(kw)]
	if c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
		return false
	}
	return true
}

// stripCommentsAndStrings 移除行注释、块注释与字符串字面量，返回可用于
// 语法检测的文本（按行保持结构）。
func stripCommentsAndStrings(src string) string {
	var b strings.Builder
	inStr := byte(0) // 0 = 不在字符串中；' / "
	inBlock := false
	i := 0
	for i < len(src) {
		c := src[i]
		if inStr != 0 {
			b.WriteByte(c)
			if c == '\\' && i+1 < len(src) {
				b.WriteByte(src[i+1])
				i += 2
				continue
			}
			if c == inStr {
				inStr = 0
			}
			i++
			continue
		}
		if inBlock {
			if c == '*' && i+1 < len(src) && src[i+1] == '/' {
				inBlock = false
				i += 2
				continue
			}
			if c == '\n' {
				b.WriteByte('\n')
			}
			i++
			continue
		}
		if c == '/' && i+1 < len(src) {
			if src[i+1] == '/' {
				for i < len(src) && src[i] != '\n' {
					i++
				}
				continue
			}
			if src[i+1] == '*' {
				inBlock = true
				i += 2
				continue
			}
		}
		if c == '\'' || c == '"' || c == '`' {
			inStr = c
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
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
