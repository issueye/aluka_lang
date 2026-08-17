package module

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/ipc"
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

	// virtualModules 为宿主注册的模块导出，用于编译产物加载外部扩展时
	// 复用已嵌入的 SDK/TUI/typebox 实例。
	virtualModules map[string]engine.Value

	// objectProto 缓存 Object.prototype，用于把模块导出对象的原型设为
	// Object.prototype（engine.NewObject 产生的对象原型为 nil，缺少
	// hasOwnProperty/toString 等常用方法，会破坏依赖它的 npm 包）。
	objectProto engine.Object

	// entryPath 记录入口文件绝对路径（用于 import.meta.main 判定）。
	entryPath string
}

// NewLoader creates a module loader bound to the given context.
func NewLoader(ctx engine.Context) *Loader {
	return &Loader{
		ctx:            ctx,
		resolver:       NewResolver(),
		cache:          make(map[string]engine.Value),
		builtins:       make(map[string]engine.Value),
		builtinFns:     make(map[string]func(engine.Context) (engine.Value, error)),
		virtualModules: make(map[string]engine.Value),
	}
}

// SetEntryPath 设置入口模块路径（支持文件路径或虚拟路径）。
func (l *Loader) SetEntryPath(path string) {
	if abs, err := filepath.Abs(path); err == nil {
		l.entryPath = abs
	} else {
		l.entryPath = path
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

// RegisterVirtualModule 注册宿主提供的模块导出。外部扩展中的同名 import/
// require 会直接复用该值，不再访问文件系统。
func (l *Loader) RegisterVirtualModule(name string, value engine.Value) {
	l.virtualModules[name] = value
}

// Run is the entry point for executing a file as the main module.
// It determines the module type (ESM or CJS) from the file extension and
// package.json, then loads and executes the module.
func (l *Loader) Run(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("module: cannot resolve path %q: %w", path, err)
	}
	if l.entryPath == "" {
		l.entryPath = absPath
	}

	if DetectSourceKind(absPath) == SourceJSON {
		return l.loadJSONFile(absPath)
	}
	if l.resolver.SourceModuleKind(absPath) == ModuleESM {
		return l.loadESMFile(absPath)
	}
	return l.loadCJSFile(absPath)
}

// require is the CJS require function for a given parent module path.
// It resolves the specifier, checks the cache, and loads the module.
func (l *Loader) require(specifier, parentPath string) (engine.Value, error) {
	return l.requireCtx(specifier, parentPath, false)
}

// RequireModule 以 require 语境按说明符加载并执行模块，返回 module.exports。
// 供宿主（如 bundler 的 SFC 官方编译后端）在构建期执行依赖包；
// 缓存语义与 require 一致（同进程同说明符只执行一次）。
func (l *Loader) RequireModule(specifier, parentPath string) (engine.Value, error) {
	return l.requireCtx(specifier, parentPath, false)
}

// requireCtx 是 require 的内部实现，importCtx 指定解析语境（false = require
// 语境，true = import 语境）。Node 语义：ESM 静态导入/动态 import() 用
// import 语境解析 exports 条件（含 "import"），CJS require 用 require 语境
// （不含 "import"）——否则 require 一个带 {"import":..., "require":...}
// 条件的包会错误加载 ESM 入口。
func (l *Loader) requireCtx(specifier, parentPath string, importCtx bool) (engine.Value, error) {
	// Bun SQLite 兼容别名：复用纯 Go 的 node:sqlite 实现。
	if specifier == "bun:sqlite" {
		return l.loadBuiltin("node:sqlite")
	}
	// Aluka 原生 IPC 与动态插件模块拦截: import plugin from "aluka:plugin:xxx"
	if strings.HasPrefix(specifier, "aluka:plugin:") {
		pluginName := strings.TrimPrefix(specifier, "aluka:plugin:")
		return l.loadPluginModule(pluginName)
	}
	if specifier == "aluka:ipc" {
		alukaVal, err := l.ctx.Global().Get("Aluka")
		if err == nil && alukaVal.IsObject() {
			if ao, ok := alukaVal.AsObject(); ok {
				if ipcVal, err := ao.Get("ipc"); err == nil {
					return ipcVal, nil
				}
			}
		}
	}
	if specifier == "aluka:gui" {
		alukaVal, err := l.ctx.Global().Get("Aluka")
		if err == nil && alukaVal.IsObject() {
			if ao, ok := alukaVal.AsObject(); ok {
				if guiVal, err := ao.Get("gui"); err == nil {
					return guiVal, nil
				}
			}
		}
	}
	// 内置模块拦截：node: 前缀（如 node:fs、node:path、node:fs/promises）。
	if isBuiltinSpecifier(specifier) {
		return l.loadBuiltin(specifier)
	}
	// 无前缀裸名（如 require('path')）：若注册表中有同名内置模块则优先内置
	// （Node.js 语义，内置模块优先于 node_modules 同名包）。
	if isBareSpecifier(specifier) && l.hasBuiltin(specifier) {
		return l.loadBuiltin("node:" + specifier)
	}
	if virtual, ok := l.virtualModules[specifier]; ok {
		return virtual, nil
	}

	// 产物模式优先按构建期映射加载。绝对路径入口以及其后续依赖允许
	// 回退文件系统，用于加载用户安装的 TypeScript 扩展。
	if l.embedded != nil {
		if key, ok := l.embedded.ResolveEmbedded(specifier, parentPath); ok {
			// 循环依赖/重复 require：模块已在执行或完成时返回缓存
			// （RunPrecompiled 内部会预填 cache）。
			l.mu.Lock()
			if cached, cachedOK := l.cache[key]; cachedOK {
				l.mu.Unlock()
				return cached, nil
			}
			l.mu.Unlock()
			// JSON 资源（M3）：直接解析嵌入字节，语义同文件模式的 loadJSON。
			if l.embedded.ModuleTypeOf(key) == "json" {
				data, assetOK := l.embedded.LoadJSON(key)
				if !assetOK {
					return engine.Undefined(), fmt.Errorf("module: compiled mode: JSON asset %q not found", key)
				}
				val, err := parseOrderedJSON(data)
				if err != nil {
					return engine.Undefined(), fmt.Errorf("module: invalid JSON in %q: %w", key, err)
				}
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
		if !filepath.IsAbs(specifier) && !filepath.IsAbs(parentPath) {
			return engine.Undefined(), fmt.Errorf("module: compiled mode: cannot load external module %q from %q (not embedded; rebuild with aluka build)", specifier, parentPath)
		}
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

	if DetectSourceKind(absPath) == SourceJSON {
		return l.loadJSON(absPath)
	}
	if l.resolver.SourceModuleKind(absPath) == ModuleESM {
		return l.loadESM(absPath)
	}
	return l.loadCJS(absPath)
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

	// 保序解析：JSON 对象键保持文档顺序（JSON.stringify 键序语义；
	// encoding/json 的 map 解析键序随机，会造成键序偶发漂移）。
	result, err := parseOrderedJSON(data)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("module: invalid JSON in %q: %w", absPath, err)
	}
	l.mu.Lock()
	l.cache[absPath] = result
	l.mu.Unlock()
	return result, nil
}

// parseOrderedJSON 保序解析 JSON 文本为 engine.Value
// （对象键保持文档顺序，数组保持元素顺序）。
func parseOrderedJSON(data []byte) (engine.Value, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeJSONValue(dec)
	if err != nil {
		return engine.Undefined(), err
	}
	return v, nil
}

// decodeJSONValue 递归解码单个 JSON 值（保序）。
func decodeJSONValue(dec *json.Decoder) (engine.Value, error) {
	tok, err := dec.Token()
	if err != nil {
		return engine.Undefined(), err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := engine.NewObject()
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return engine.Undefined(), err
				}
				key, _ := keyTok.(string)
				val, err := decodeJSONValue(dec)
				if err != nil {
					return engine.Undefined(), err
				}
				_ = obj.Set(key, val)
			}
			if _, err := dec.Token(); err != nil { // 消费 '}'
				return engine.Undefined(), err
			}
			return obj, nil
		case '[':
			var elems []engine.Value
			for dec.More() {
				val, err := decodeJSONValue(dec)
				if err != nil {
					return engine.Undefined(), err
				}
				elems = append(elems, val)
			}
			if _, err := dec.Token(); err != nil { // 消费 ']'
				return engine.Undefined(), err
			}
			return engine.NewArray(elems), nil
		}
		return engine.Undefined(), fmt.Errorf("module: unexpected delimiter %v", t)
	case string:
		return engine.Str(t), nil
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return engine.Undefined(), fmt.Errorf("module: bad number %q", t)
		}
		return engine.Number(f), nil
	case bool:
		return engine.Boolean(t), nil
	case nil:
		return engine.Null(), nil
	}
	return engine.Undefined(), fmt.Errorf("module: unexpected token %v", tok)
}

// loadJSONFile is like loadJSON but discards the return value (for Run).
func (l *Loader) loadJSONFile(absPath string) error {
	_, err := l.loadJSON(absPath)
	return err
}

// makeRequireFunc creates a JS require function for the given module path.
func (l *Loader) makeRequireFunc(modulePath string) engine.Function {
	requireFn := engine.NewFunction("require", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("require: missing module specifier")
		}
		spec := args[0].String()
		return l.require(spec, modulePath)
	})

	resolveFn := engine.NewFunction("resolve", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("require.resolve: missing module specifier")
		}
		spec := args[0].String()
		paths := requireResolvePathsOption(args)
		resolved, err := l.resolveRequire(spec, modulePath, paths)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Str(resolved), nil
	})
	if resolveObj, ok := resolveFn.AsObject(); ok {
		_ = resolveObj.Set("paths", engine.NewFunction("paths", func(args []engine.Value) (engine.Value, error) {
			if len(args) > 0 {
				spec := strings.TrimPrefix(args[0].String(), "node:")
				if _, ok := l.builtinFns[spec]; ok {
					return engine.Null(), nil
				}
				if _, ok := l.builtins[spec]; ok {
					return engine.Null(), nil
				}
			}
			return engine.NewArray(stringValues(nodeModuleSearchPaths(modulePath))), nil
		}))
	}
	if requireObj, ok := requireFn.AsObject(); ok {
		_ = requireObj.Set("resolve", resolveFn)
		_ = requireObj.Set("cache", engine.NewObject())
		_ = requireObj.Set("extensions", engine.NewObject())
		_ = requireObj.Set("main", engine.Undefined())
	}
	return requireFn
}

func requireResolvePathsOption(args []engine.Value) []string {
	if len(args) < 2 || !args[1].IsObject() {
		return nil
	}
	opts, ok := args[1].AsObject()
	if !ok {
		return nil
	}
	pathsValue, err := opts.Get("paths")
	if err != nil {
		return nil
	}
	pathsArray, ok := pathsValue.(*engine.ArrayValue)
	if !ok {
		return nil
	}
	paths := make([]string, 0, len(pathsArray.Elems()))
	for _, value := range pathsArray.Elems() {
		paths = append(paths, value.String())
	}
	return paths
}

func (l *Loader) resolveRequire(spec, modulePath string, paths []string) (string, error) {
	builtin := strings.TrimPrefix(spec, "node:")
	if _, ok := l.builtinFns[builtin]; ok {
		return spec, nil
	}
	if _, ok := l.builtins[builtin]; ok {
		return spec, nil
	}
	if l.embedded != nil {
		if resolved, ok := l.embedded.ResolveEmbedded(spec, modulePath); ok {
			return resolved, nil
		}
	}
	for _, searchPath := range paths {
		parent := filepath.Join(searchPath, "_index.js")
		if resolved, err := l.resolver.Resolve(spec, parent); err == nil {
			return resolved, nil
		}
	}
	return l.resolver.Resolve(spec, modulePath)
}

func nodeModuleSearchPaths(modulePath string) []string {
	dir := filepath.Dir(modulePath)
	var paths []string
	for {
		paths = append(paths, filepath.Join(dir, "node_modules"))
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return paths
}

func stringValues(values []string) []engine.Value {
	result := make([]engine.Value, len(values))
	for i, value := range values {
		result[i] = engine.Str(value)
	}
	return result
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
		// ESM 模块：exports 已是命名空间（含 __esModule/default/命名）。
		if esmExports, ok := exports.AsObject(); ok {
			if v, err := esmExports.Get("__esModule"); err == nil {
				if b, ok := v.Bool(); ok && b {
					return l.resolveImport(exports)
				}
			}
		}
		// CJS 模块：包装为命名空间 { default: exports } + 拷贝命名导出
		// （Node 经 cjs-module-lexer 静态分析命名导出；这里仅对非函数对象拷贝自有键）。
		ns := engine.NewObject()
		if p, err := l.objectProtoValue(); err == nil {
			engine.SetProto(ns, p)
		}
		_ = ns.Set("default", exports)
		if !exports.IsFunction() {
			if co, ok := exports.AsObject(); ok {
				for _, k := range co.Keys() {
					if k == "default" || k == "__esModule" {
						continue
					}
					if v, err := co.Get(k); err == nil {
						_ = ns.Set(k, v)
					}
				}
			}
		}
		return l.resolveImport(ns)
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
// 产物模式下 url 用 bun://~BUN/ 前缀（虚拟路径，Bun 编译产物风格）；
// 文件模式保持 file:// URL。
func (l *Loader) makeImportMetaFunc(modulePath string) engine.Value {
	meta := engine.NewObject()
	if l.embedded != nil {
		_ = meta.Set("url", engine.Str("bun://~BUN/"+strings.TrimLeft(filepath.ToSlash(modulePath), "/")))
	} else {
		_ = meta.Set("url", engine.Str(pathToFileURLString(modulePath)))
	}
	dirPath := filepath.Dir(modulePath)
	_ = meta.Set("dirname", engine.Str(dirPath))
	_ = meta.Set("dir", engine.Str(dirPath))
	_ = meta.Set("filename", engine.Str(modulePath))
	_ = meta.Set("path", engine.Str(modulePath))

	isMain := false
	if l.entryPath != "" {
		if l.embedded != nil {
			isMain = modulePath == l.entryPath || filepath.ToSlash(modulePath) == filepath.ToSlash(l.entryPath)
		} else {
			if abs, err := filepath.Abs(modulePath); err == nil {
				isMain = abs == l.entryPath
			} else {
				isMain = modulePath == l.entryPath
			}
		}
	}
	_ = meta.Set("main", engine.Boolean(isMain))
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

// isBuiltinSpecifier 判断 specifier 是否为内置模块（node: 或 aluka: 前缀形式）。
func isBuiltinSpecifier(specifier string) bool {
	return strings.HasPrefix(specifier, "node:") || strings.HasPrefix(specifier, "aluka:")
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

// loadBuiltin 加载内置模块。specifier 形如 "node:path"、"aluka:markdown" 或 "node:fs/promises"。
// 首次加载调用注册的工厂函数构造导出对象，之后缓存。
func (l *Loader) loadBuiltin(specifier string) (engine.Value, error) {
	// 去掉 node: 或 aluka: 前缀，得到模块名（可能含子路径，如 "fs/promises"）。
	name := strings.TrimPrefix(specifier, "node:")
	name = strings.TrimPrefix(name, "aluka:")

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

// loadPluginModule 加载 aluka:plugin:<name> 原生 IPC 透明代理模块。
func (l *Loader) loadPluginModule(pluginName string) (engine.Value, error) {
	client, err := ipc.Connect(pluginName)
	if err != nil {
		return nil, fmt.Errorf("module: failed to connect to plugin %q: %w", pluginName, err)
	}

	proxyObj := engine.NewObject()
	_ = proxyObj.Set("__pluginName", engine.Str(pluginName))

	_ = proxyObj.Set("call", engine.NewFunction("call", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("plugin.call requires method name")
		}
		method := args[0].String()
		var params interface{}
		if len(args) > 1 {
			params = pluginValueToJSON(args[1])
		}
		res, err := client.Call(method, params, 30*time.Second)
		if err != nil {
			return nil, err
		}
		return pluginJSONToEngine(res), nil
	}))

	_ = proxyObj.Set("callSync", engine.NewFunction("callSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("plugin.callSync requires method name")
		}
		method := args[0].String()
		var params interface{}
		if len(args) > 1 {
			params = pluginValueToJSON(args[1])
		}
		res, err := client.Call(method, params, 30*time.Second)
		if err != nil {
			return nil, err
		}
		return pluginJSONToEngine(res), nil
	}))

	_ = proxyObj.Set("emit", engine.NewFunction("emit", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("plugin.emit requires event name")
		}
		evt := args[0].String()
		var data interface{}
		if len(args) > 1 {
			data = pluginValueToJSON(args[1])
		}
		if err := client.Emit(evt, data); err != nil {
			return nil, err
		}
		return engine.Undefined(), nil
	}))

	_ = proxyObj.Set("close", engine.NewFunction("close", func(args []engine.Value) (engine.Value, error) {
		_ = client.Close()
		return engine.Undefined(), nil
	}))

	return proxyObj, nil
}

func pluginValueToJSON(v engine.Value) interface{} {
	switch {
	case v.IsUndefined() || v.IsNull():
		return nil
	case v.Type() == engine.TypeString:
		return v.String()
	case v.Type() == engine.TypeBoolean:
		b, _ := v.Bool()
		return b
	case v.Type() == engine.TypeNumber:
		f, _ := v.Float()
		return f
	default:
		if a, ok := v.(*engine.ArrayValue); ok {
			out := make([]interface{}, 0, len(a.Elems()))
			for _, e := range a.Elems() {
				out = append(out, pluginValueToJSON(e))
			}
			return out
		}
		if o, ok := v.AsObject(); ok {
			obj := make(map[string]interface{})
			for _, k := range o.Keys() {
				if val, err := o.Get(k); err == nil {
					obj[k] = pluginValueToJSON(val)
				}
			}
			return obj
		}
	}
	return nil
}

func pluginJSONToEngine(v interface{}) engine.Value {
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
			elems[i] = pluginJSONToEngine(e)
		}
		return engine.NewArray(elems)
	case map[string]interface{}:
		obj := engine.NewObject()
		for k, e := range val {
			_ = obj.Set(k, pluginJSONToEngine(e))
		}
		return obj
	default:
		return engine.Undefined()
	}
}
