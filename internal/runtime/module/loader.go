package module

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aluka-lang/aluka/internal/engine"
)

// Loader loads and caches modules. It supports both CommonJS (require) and
// ESM (import/export) module formats.
type Loader struct {
	ctx      engine.Context
	resolver *Resolver

	mu    sync.Mutex
	cache map[string]engine.Value // resolved path → module.exports value

	// bcCache 是字节码磁盘缓存（1C.14），命中时跳过 parse+compile。
	bcCache bytecodeCache

	// builtins 注册 Node.js 内置模块（node:fs / node:path 等）。
	// key 为去掉 node: 前缀的模块名（如 "path"、"fs/promises"）。
	builtins   map[string]engine.Value                               // 已构造的导出对象缓存
	builtinFns map[string]func(engine.Context) (engine.Value, error) // 工厂函数

	// objectProto 缓存 Object.prototype，用于把模块导出对象的原型设为
	// Object.prototype（engine.NewObject 产生的对象原型为 nil，缺少
	// hasOwnProperty/toString 等常用方法，会破坏依赖它的 npm 包）。
	objectProto engine.Object
}

// NewLoader creates a module loader bound to the given context.
func NewLoader(ctx engine.Context) *Loader {
	return &Loader{
		ctx:        ctx,
		resolver:   NewResolver(),
		cache:      make(map[string]engine.Value),
		builtins:   make(map[string]engine.Value),
		builtinFns: make(map[string]func(engine.Context) (engine.Value, error)),
	}
}

// SetNoCache 禁用字节码缓存（对应 --no-cache）。
func (l *Loader) SetNoCache(disabled bool) {
	l.bcCache.disabled = disabled
}

// stripBOM 剥离开头的 UTF-8 BOM（EF BB BF）。若文件内容以 BOM 开头则移除，
// 防止 BOM 被嵌入 CJS 包装函数体后导致 lexer 死循环。
func stripBOM(src []byte) []byte {
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		return src[3:]
	}
	return src
}

// objectProtoValue 返回全局 Object.prototype（带缓存）。
func (l *Loader) objectProtoValue() (engine.Object, error) {
	if l.objectProto != nil {
		return l.objectProto, nil
	}
	ov, err := l.ctx.Global().Get("Object")
	if err != nil || !ov.IsObject() {
		return nil, fmt.Errorf("module: Object constructor unavailable: %v", err)
	}
	ovObj, _ := ov.AsObject()
	pv, err := ovObj.Get("prototype")
	if err != nil || !pv.IsObject() {
		return nil, fmt.Errorf("module: Object.prototype unavailable: %v", err)
	}
	po, _ := pv.AsObject()
	l.objectProto = po
	return po, nil
}

// newExports 创建带 Object.prototype 原型的模块导出对象。
func (l *Loader) newExports() engine.Object {
	o := engine.NewObject()
	if p, err := l.objectProtoValue(); err == nil {
		engine.SetProto(o, p)
	}
	return o
}

// ensureExportsProto 若导出值是可赋值原型的对象，则把其原型设为 Object.prototype。
// engine.NewObject 产生的对象原型为 nil，缺少 hasOwnProperty 等常用方法。
func (l *Loader) ensureExportsProto(v engine.Value) {
	if !v.IsObject() {
		return
	}
	if setter, ok := v.(interface{ SetProto(engine.Object) }); ok {
		if p, err := l.objectProtoValue(); err == nil {
			setter.SetProto(p)
		}
	}
}

// RegisterBuiltin 注册一个 Node.js 内置模块工厂。
// name 为去掉 node: 前缀的模块名（如 "path"）。首次 require 时调用 factory 构造导出对象。
func (l *Loader) RegisterBuiltin(name string, factory func(engine.Context) (engine.Value, error)) {
	l.builtinFns[name] = factory
}

// Run is the entry point for executing a file as the main module.
// It determines the module type (ESM or CJS) from the file extension and
// package.json, then loads and executes the module.
func (l *Loader) Run(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("module: cannot resolve path %q: %w", path, err)
	}

	mt := l.resolver.ModuleType(absPath)
	switch mt {
	case "module":
		return l.loadESMFile(absPath)
	case "json":
		return l.loadJSONFile(absPath)
	default:
		return l.loadCJSFile(absPath)
	}
}

// require is the CJS require function for a given parent module path.
// It resolves the specifier, checks the cache, and loads the module.
func (l *Loader) require(specifier, parentPath string) (engine.Value, error) {
	// 内置模块拦截：node: 前缀（如 node:fs、node:path、node:fs/promises）。
	if isBuiltinSpecifier(specifier) {
		return l.loadBuiltin(specifier)
	}
	// 无前缀裸名（如 require('path')）：若注册表中有同名内置模块则优先内置
	// （Node.js 语义，内置模块优先于 node_modules 同名包）。
	if isBareSpecifier(specifier) && l.hasBuiltin(specifier) {
		return l.loadBuiltin("node:" + specifier)
	}

	resolved, err := l.resolver.Resolve(specifier, parentPath)
	if err != nil {
		return engine.Undefined(), err
	}

	absPath, err := filepath.Abs(resolved)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("module: cannot resolve path: %w", err)
	}

	l.mu.Lock()
	if cached, ok := l.cache[absPath]; ok {
		l.mu.Unlock()
		return cached, nil
	}
	l.mu.Unlock()

	mt := l.resolver.ModuleType(absPath)
	switch mt {
	case "module":
		return l.loadESM(absPath)
	case "json":
		return l.loadJSON(absPath)
	default:
		return l.loadCJS(absPath)
	}
}

// loadJSON loads a .json file by parsing it and returning the resulting value.
func (l *Loader) loadJSON(absPath string) (engine.Value, error) {
	l.mu.Lock()
	if cached, ok := l.cache[absPath]; ok {
		l.mu.Unlock()
		return cached, nil
	}
	l.mu.Unlock()

	data, err := os.ReadFile(absPath)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("module: cannot read %q: %w", absPath, err)
	}

	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return engine.Undefined(), fmt.Errorf("module: invalid JSON in %q: %w", absPath, err)
	}

	result := jsonToValue(v)
	l.mu.Lock()
	l.cache[absPath] = result
	l.mu.Unlock()
	return result, nil
}

// loadJSONFile is like loadJSON but discards the return value (for Run).
func (l *Loader) loadJSONFile(absPath string) error {
	_, err := l.loadJSON(absPath)
	return err
}

// jsonToValue converts a Go value (from encoding/json) to an engine.Value.
func jsonToValue(v interface{}) engine.Value {
	switch val := v.(type) {
	case nil:
		return engine.Null()
	case bool:
		return engine.Boolean(val)
	case float64:
		return engine.Number(val)
	case string:
		return engine.Str(val)
	case []interface{}:
		elems := make([]engine.Value, len(val))
		for i, e := range val {
			elems[i] = jsonToValue(e)
		}
		return engine.NewArray(elems)
	case map[string]interface{}:
		obj := engine.NewObject()
		for k, e := range val {
			_ = obj.Set(k, jsonToValue(e))
		}
		return obj
	default:
		return engine.Undefined()
	}
}

// makeRequireFunc creates a JS require function for the given module path.
func (l *Loader) makeRequireFunc(modulePath string) engine.Function {
	return engine.NewFunction("require", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("require: missing module specifier")
		}
		spec := args[0].String()
		return l.require(spec, modulePath)
	})
}

// MakeRequireFunc 创建基于指定模块路径的 require 函数（公开版，供
// node:module.createRequire 使用）。
func (l *Loader) MakeRequireFunc(modulePath string) engine.Function {
	return l.makeRequireFunc(modulePath)
}

// makeImportFunc creates a JS dynamic-import function for the given module
// path. It implements ES2020 dynamic import(): always returns a Promise that
// resolves to the module's namespace (exports) or rejects on load failure.
//
// 实现说明：动态 import 复用 require() 的同步加载链路，再用全局 Promise
// 把结果包装成已 settled 的 Promise。通过 engine.Function.Call 调用
// Promise.resolve / Promise.reject 静态方法，避免依赖 interpreter 包。
func (l *Loader) makeImportFunc(modulePath string) engine.Function {
	return engine.NewFunction("__import", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return l.rejectImport(fmt.Errorf("import: missing module specifier"))
		}
		spec := args[0].String()
		exports, err := l.require(spec, modulePath)
		if err != nil {
			return l.rejectImport(err)
		}
		return l.resolveImport(exports)
	})
}

// resolveImport wraps a value in a resolved Promise via the global Promise.resolve.
func (l *Loader) resolveImport(v engine.Value) (engine.Value, error) {
	promiseCtor, err := l.ctx.Global().Get("Promise")
	if err != nil || !promiseCtor.IsFunction() {
		// 回退：若全局无 Promise（不应发生），直接返回原值。
		return v, nil
	}
	// Promise 构造器同时也是对象，取其 resolve/reject 静态方法。
	if ctorObj, ok := promiseCtor.AsObject(); ok {
		resolveFn, err := ctorObj.Get("resolve")
		if err == nil && resolveFn.IsFunction() {
			if rf, ok := resolveFn.AsFunction(); ok {
				return rf.Call([]engine.Value{v})
			}
		}
	}
	return v, nil
}

// rejectImport wraps an error in a rejected Promise via the global Promise.reject.
func (l *Loader) rejectImport(err error) (engine.Value, error) {
	promiseCtor, e := l.ctx.Global().Get("Promise")
	if e != nil || !promiseCtor.IsFunction() {
		// 回退：让错误同步抛出。
		return engine.Undefined(), err
	}
	if ctorObj, ok := promiseCtor.AsObject(); ok {
		rejectFn, e := ctorObj.Get("reject")
		if e == nil && rejectFn.IsFunction() {
			if rf, ok := rejectFn.AsFunction(); ok {
				// 用字符串包装错误消息（后续可改为构造 Error 对象）。
				errVal := engine.Str(err.Error())
				return rf.Call([]engine.Value{errVal})
			}
		}
	}
	return engine.Undefined(), err
}

// isBuiltinSpecifier 判断 specifier 是否为内置模块（node: 前缀形式）。
func isBuiltinSpecifier(specifier string) bool {
	return strings.HasPrefix(specifier, "node:")
}

// isBareSpecifier 判断是否为裸模块名（非相对/绝对/盘符路径）。
func isBareSpecifier(spec string) bool {
	if spec == "" {
		return false
	}
	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") || strings.HasPrefix(spec, "/") {
		return false
	}
	if len(spec) >= 2 && spec[1] == ':' {
		return false // Windows 盘符（如 C:\...）。
	}
	return true
}

// hasBuiltin 判断注册表中是否存在指定内置模块。
func (l *Loader) hasBuiltin(name string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.builtinFns[name]
	return ok
}

// loadBuiltin 加载内置模块。specifier 形如 "node:path" 或 "node:fs/promises"。
// 首次加载调用注册的工厂函数构造导出对象，之后缓存。
func (l *Loader) loadBuiltin(specifier string) (engine.Value, error) {
	// 去掉 node: 前缀，得到模块名（可能含子路径，如 "fs/promises"）。
	name := strings.TrimPrefix(specifier, "node:")

	l.mu.Lock()
	if cached, ok := l.builtins[name]; ok {
		l.mu.Unlock()
		return cached, nil
	}
	factory, ok := l.builtinFns[name]
	l.mu.Unlock()
	if !ok {
		return engine.Undefined(), fmt.Errorf("module: no such built-in module: %s", specifier)
	}

	// 构造导出对象。
	exports, err := factory(l.ctx)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("module: failed to load %s: %w", specifier, err)
	}
	// 规范化导出对象原型（engine.NewObject 产生的对象原型为 nil）。
	l.ensureExportsProto(exports)

	l.mu.Lock()
	l.builtins[name] = exports
	l.mu.Unlock()
	return exports, nil
}
