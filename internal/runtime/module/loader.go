package module

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// EmbeddedResolver 是产物模式（aluka build --compile）的嵌入式模块存储
// 接口（M2）。产物运行时不做文件系统解析：按构建期解析映射（resolutions）
// 直接加载嵌入的预编译模块。实现位于 internal/bundler/compile（本包不依赖
// compile 包，避免 bundler ↔ module 循环依赖）。
type EmbeddedResolver interface {
	// ResolveEmbedded 按构建期解析映射解析 specifier（父模块路径 → 模块路径）。
	ResolveEmbedded(specifier, parentPath string) (string, bool)
	// ModuleTypeOf 返回模块类型（"esm" | "cjs" | "json"）。
	ModuleTypeOf(key string) string
	// LoadModule 反序列化嵌入的预编译模块（实现内部可缓存）。
	LoadModule(key string) (*bytecode.Module, error)
	// LoadJSON 读取嵌入的 JSON 资源（M3：import x from './data.json'）。
	LoadJSON(key string) ([]byte, bool)
}

// Loader loads and caches modules. It supports both CommonJS (require) and
// ESM (import/export) module formats.
type Loader struct {
	ctx      engine.Context
	resolver *Resolver

	mu    sync.Mutex
	cache map[string]engine.Value // resolved path → module.exports value

	// bcCache 是字节码磁盘缓存（1C.14），命中时跳过 parse+compile。
	bcCache bytecodeCache

	// embedded 非 nil 时进入产物模式：require/import 走构建期解析映射，
	// 未命中报错（不加载外部文件，Bun 编译产物同语义）。
	embedded EmbeddedResolver

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

// SetEmbedded 启用产物模式（aluka build --compile）：require/import 按构建期
// 解析映射加载嵌入模块，未命中报错（不访问文件系统）。
func (l *Loader) SetEmbedded(er EmbeddedResolver) {
	l.embedded = er
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
	return l.requireCtx(specifier, parentPath, false)
}

// requireCtx 是 require 的内部实现，importCtx 指定解析语境（false = require
// 语境，true = import 语境）。Node 语义：ESM 静态导入/动态 import() 用
// import 语境解析 exports 条件（含 "import"），CJS require 用 require 语境
// （不含 "import"）——否则 require 一个带 {"import":..., "require":...}
// 条件的包会错误加载 ESM 入口。
func (l *Loader) requireCtx(specifier, parentPath string, importCtx bool) (engine.Value, error) {
	// 内置模块拦截：node: 前缀（如 node:fs、node:path、node:fs/promises）。
	if isBuiltinSpecifier(specifier) {
		return l.loadBuiltin(specifier)
	}
	// 无前缀裸名（如 require('path')）：若注册表中有同名内置模块则优先内置
	// （Node.js 语义，内置模块优先于 node_modules 同名包）。
	if isBareSpecifier(specifier) && l.hasBuiltin(specifier) {
		return l.loadBuiltin("node:" + specifier)
	}

	// 产物模式（M2）：按构建期解析映射加载嵌入模块，未命中报错
	// （不加载外部文件，Bun 编译产物同语义）。
	if l.embedded != nil {
		key, ok := l.embedded.ResolveEmbedded(specifier, parentPath)
		if !ok {
			return engine.Undefined(), fmt.Errorf("module: compiled mode: cannot load external module %q from %q (not embedded; rebuild with aluka build)", specifier, parentPath)
		}
		// 循环依赖/重复 require：模块已在执行或完成时返回缓存
		// （RunPrecompiled 内部会预填 cache）。
		l.mu.Lock()
		if cached, ok := l.cache[key]; ok {
			l.mu.Unlock()
			return cached, nil
		}
		l.mu.Unlock()
		// JSON 资源（M3）：直接解析嵌入字节，语义同文件模式的 loadJSON。
		if l.embedded.ModuleTypeOf(key) == "json" {
			data, ok := l.embedded.LoadJSON(key)
			if !ok {
				return engine.Undefined(), fmt.Errorf("module: compiled mode: JSON asset %q not found", key)
			}
			var v interface{}
			if err := json.Unmarshal(data, &v); err != nil {
				return engine.Undefined(), fmt.Errorf("module: invalid JSON in %q: %w", key, err)
			}
			val := jsonToValue(v)
			l.mu.Lock()
			l.cache[key] = val
			l.mu.Unlock()
			return val, nil
		}
		mod, err := l.embedded.LoadModule(key)
		if err != nil {
			return engine.Undefined(), err
		}
		return l.RunPrecompiled(key, mod, l.embedded.ModuleTypeOf(key) == "esm")
	}

	var resolved string
	var err error
	if importCtx {
		resolved, err = l.resolver.ResolveImport(specifier, parentPath)
	} else {
		resolved, err = l.resolver.Resolve(specifier, parentPath)
	}
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
// 支持第二参数 import attributes：import(x, { with: { type: 'json' } })。
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
		attrType := ""
		if len(args) > 1 {
			t, err := importAttributeType(args[1])
			if err != nil {
				return l.rejectImport(err)
			}
			attrType = t
		}
		exports, err := l.requireWithAttributes(spec, modulePath, attrType)
		if err != nil {
			return l.rejectImport(err)
		}
		// 动态 import 解析为模块命名空间：JSON（type: json）与其他
		// 非命名空间导出包装为 { default: <value> }（Node 语义）。
		if attrType == "json" {
			ns := engine.NewObject()
			if p, err := l.objectProtoValue(); err == nil {
				engine.SetProto(ns, p)
			}
			_ = ns.Set("default", exports)
			return l.resolveImport(ns)
		}
		return l.resolveImport(exports)
	})
}

// makeImportReqFunc 构造 ESM 静态导入的同步加载函数（__importReq）。
// 与 require 的区别仅在解析语境：import 条件（含 "import"）。返回
// module.exports——transformESMToCJS 生成的代码按 .default/.命名 访问。
func (l *Loader) makeImportReqFunc(modulePath string) engine.Function {
	return engine.NewFunction("__importReq", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("__importReq: missing module specifier")
		}
		return l.requireCtx(args[0].String(), modulePath, true)
	})
}

// makeImportMetaFunc 构造 import.meta 元数据访问函数（__importMeta()）。
// 返回当前模块的元数据对象：{ url, dirname, filename, resolve }。
// parser 把 import.meta lower 为对全局 __importMeta() 的调用。
// 产物模式下 url 用 bun:// 前缀（虚拟路径，Bun 编译产物风格）；
// 文件模式保持 file:// URL。
func (l *Loader) makeImportMetaFunc(modulePath string) engine.Value {
	meta := engine.NewObject()
	if l.embedded != nil {
		_ = meta.Set("url", engine.Str("bun://"+filepath.ToSlash(modulePath)))
	} else {
		_ = meta.Set("url", engine.Str(pathToFileURLString(modulePath)))
	}
	_ = meta.Set("dirname", engine.Str(filepath.Dir(modulePath)))
	_ = meta.Set("filename", engine.Str(modulePath))
	_ = meta.Set("resolve", engine.NewFunction("resolve", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("import.meta.resolve: missing specifier")
		}
		// import.meta.resolve 按 import 语境解析（Node 语义）。
		resolved, err := l.resolver.ResolveImport(args[0].String(), modulePath)
		if err != nil {
			return engine.Undefined(), err
		}
		absPath, err := filepath.Abs(resolved)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Str(pathToFileURLString(absPath)), nil
	}))
	return engine.NewFunction("__importMeta", func(args []engine.Value) (engine.Value, error) {
		return meta, nil
	})
}

// pathToFileURLString 将绝对路径转为 file:// URL（Windows 驱动器盘符带斜杠）。
func pathToFileURLString(abs string) string {
	slash := filepath.ToSlash(abs)
	if len(slash) >= 2 && slash[1] == ':' {
		slash = "/" + slash
	}
	return "file://" + slash
}

// requireWithAttributes 按 import attributes 加载模块：
// type 为 "json" 时强制走 JSON 模块加载；"module" 或不指定走常规加载；
// 其他 type 报错。动态 import() 语境（import 条件解析）。
// json 与常规分支统一走 requireCtx：产物模式命中嵌入模块/JSON 资源；
// 文件模式走文件系统解析（.json 扩展名由 requireCtx 尾段判定为 loadJSON）。
func (l *Loader) requireWithAttributes(specifier, parentPath, attrType string) (engine.Value, error) {
	if attrType != "" && attrType != "module" && attrType != "json" {
		return engine.Undefined(), fmt.Errorf("import: unsupported import attribute type %q", attrType)
	}
	return l.requireCtx(specifier, parentPath, true)
}

// importAttributeType 从 import 第二参数提取 attributes：
// { with: { type: 'json' } } → "json"。无 attributes 返回空串。
func importAttributeType(opts engine.Value) (string, error) {
	if opts.IsUndefined() || opts.IsNull() {
		return "", nil
	}
	obj, ok := opts.AsObject()
	if !ok {
		return "", fmt.Errorf("import: options must be an object")
	}
	withVal, err := obj.Get("with")
	if err != nil || withVal.IsUndefined() || withVal.IsNull() {
		return "", nil
	}
	withObj, ok := withVal.AsObject()
	if !ok {
		return "", fmt.Errorf("import: options.with must be an object")
	}
	typeVal, err := withObj.Get("type")
	if err != nil || typeVal.IsUndefined() {
		return "", nil
	}
	return typeVal.String(), nil
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
// reject 值为 Error 对象（Node 语义：import() 失败 reject Error，`e.message`
// 可用）——曾 reject 字符串导致 `e.message` 为 undefined。
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
				// Error("msg") 与 new Error("msg") 等价（ES 规范），
				// 构造带 message 的 Error 对象。
				errVal := engine.Str(err.Error())
				if errCtor, ge := l.ctx.Global().Get("Error"); ge == nil && errCtor.IsFunction() {
					if ef, ok := errCtor.AsFunction(); ok {
						if ev, ce := ef.Call([]engine.Value{errVal}); ce == nil {
							errVal = ev
						}
					}
				}
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

// GetBuiltin 加载内置模块（process.getBuiltinModule 使用，Node ≥ 22.3）。
// specifier 可为 "node:fs" 或 "fs"；非内置 specifier 返回 undefined（不报错）。
func (l *Loader) GetBuiltin(specifier string) (engine.Value, error) {
	name := strings.TrimPrefix(specifier, "node:")
	if !l.hasBuiltin(name) {
		return engine.Undefined(), nil
	}
	return l.loadBuiltin("node:" + name)
}
