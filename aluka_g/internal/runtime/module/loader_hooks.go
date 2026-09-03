package module

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/engine/parser"
)

// === node:module.register + loader hooks（Node 22 语义）===================
//
// register(specifier, parentURL) 把 hooks 模块（导出 resolve/load/
// initialize）挂到本 Loader 的钩子链上：此后 require/import 的解析与
// 读取阶段先经过 hooks，nextResolve/nextLoad 落到默认实现。jiti/register
// 即依赖此机制（jiti-hooks.mjs 提供 resolve/load）。
//
// 与 Node 的有意差异（文档登记）：
//   - hooks 不拦截 node:/data:/aluka: 内置与虚拟模块（默认解析在前）；
//   - 仅支持顶层 hooks 链（多个 register 后注册者优先，Node 语义一致）；
//   - load 仅支持返回 { source, format } 覆盖；format 限
//     commonjs/module/json，builtin 等 format 回退默认加载。

// registerHook 是一条已注册的 loader 钩子。
type registerHook struct {
	url        string // hooks 模块标识（诊断用）
	resolve    engine.Value
	load       engine.Value
	initialize engine.Value
}

// RegisterHook 实现 node:module.register(specifier, parentURL[, options])：
// 解析 hooks 模块 → 执行（ESM）→ 提取 resolve/load/initialize → 调用
// initialize → 头插钩子链。specifier 为相对路径（相对 parentURL 目录）、
// file:// URL 或裸模块名。
func (l *Loader) RegisterHook(specifier, parentURL string) error {
	parentPath := NormalizeModulePath(parentURL)
	if parentPath == "" || parentPath == "/" {
		parentPath = l.entryPath
	}
	if parentPath == "" {
		return fmt.Errorf("module.register: cannot resolve hooks %q without a parent URL", specifier)
	}

	var hooksPath string
	switch {
	case strings.HasPrefix(specifier, "file:"):
		hooksPath = NormalizeModulePath(specifier)
	case strings.HasPrefix(specifier, "./") || strings.HasPrefix(specifier, "../"):
		hooksPath = filepath.Join(filepath.Dir(parentPath), filepath.FromSlash(specifier))
	case filepath.IsAbs(specifier):
		hooksPath = specifier
	default:
		// 裸模块名（如 "ts-node/esm"）：按 import 语境从 parent 解析。
		resolved, err := l.resolver.ResolveImport(specifier, parentPath)
		if err != nil {
			return fmt.Errorf("module.register: cannot resolve hooks %q: %w", specifier, err)
		}
		hooksPath = resolved
	}
	abs, err := filepath.Abs(hooksPath)
	if err != nil {
		return fmt.Errorf("module.register: cannot resolve hooks path: %w", err)
	}

	// 用默认加载执行 hooks 模块（自身不经过 hooks，避免递归）。
	ns, err := l.loadESM(abs)
	if err != nil {
		return fmt.Errorf("module.register: cannot load hooks %q: %w", abs, err)
	}
	nsObj, ok := ns.AsObject()
	if !ok {
		return fmt.Errorf("module.register: hooks %q did not export an object", abs)
	}
	hook := &registerHook{url: abs}
	// ESM 命名空间导出是活绑定 getter（AccessorValue）：Object.Get 不触发
	// JS getter，需经 Getter 求值得到真实函数（与 JSON.stringify 的
	// resolveJSONAccessor 同一语义）。
	for key, dst := range map[string]*engine.Value{"resolve": &hook.resolve, "load": &hook.load, "initialize": &hook.initialize} {
		v, err := nsObj.Get(key)
		if err != nil {
			continue
		}
		if acc, ok := v.(*engine.AccessorValue); ok && !acc.Getter.IsUndefined() {
			gv, gerr := interpreter.CallWithThis(acc.Getter, nsObj, nil)
			if gerr == nil {
				v = gv
			}
		}
		if v.IsFunction() {
			*dst = v
		}
	}
	if hook.initialize != nil {
		if _, err := l.callHookFn(hook.initialize, nil); err != nil {
			return fmt.Errorf("module.register: hooks %q initialize failed: %w", abs, err)
		}
	}
	// 后注册者优先（Node：最近注册的钩子最先被调用）。
	l.mu.Lock()
	l.hooks = append([]*registerHook{hook}, l.hooks...)
	l.mu.Unlock()
	return nil
}

// callHookFn 调用钩子 JS 函数并等待其 Promise settle（hooks 的
// resolve/load/initialize 均为 async）。
func (l *Loader) callHookFn(fn engine.Value, args []engine.Value) (engine.Value, error) {
	if fn == nil || !fn.IsFunction() {
		return engine.Undefined(), nil
	}
	res, err := interpreter.CallWithThis(fn, engine.Undefined(), args)
	if err != nil {
		return engine.Undefined(), err
	}
	if pv, ok := res.(*interpreter.PromiseValue); ok {
		if vm, ok := l.ctx.(*interpreter.VM); ok {
			return vm.AwaitPromise(pv)
		}
	}
	return res, nil
}

// callResolveHooks 依次调用钩子链的 resolve(specifier, context, nextResolve)。
// 返回 (url, handled, err)：handled=true 表示某钩子短路给出了最终 url
// （file:// 或 data:），应跳过默认解析。
func (l *Loader) callResolveHooks(specifier, parentPath string) (string, bool, error) {
	l.mu.Lock()
	hooks := append([]*registerHook(nil), l.hooks...)
	l.mu.Unlock()
	if len(hooks) == 0 {
		return "", false, nil
	}
	next := l.nextResolveFn(parentPath)
	for i, h := range hooks {
		ctxObj := engine.NewObject()
		_ = ctxObj.Set("parentURL", engine.Str(PathToFileURLString(parentPath)))
		_ = ctxObj.Set("conditions", engine.NewArray(nil))
		res, err := l.callHookFn(h.resolve, []engine.Value{engine.Str(specifier), ctxObj, next})
		if err != nil {
			return "", false, fmt.Errorf("module: resolve hook %q failed: %w", h.url, err)
		}
		if res == nil || res.IsUndefined() {
			continue
		}
		o, ok := res.AsObject()
		if !ok {
			return "", false, fmt.Errorf("module: resolve hook %q returned non-object", h.url)
		}
		urlV, err := o.Get("url")
		if err != nil || urlV.IsUndefined() {
			// 未给出 url：交给下一个钩子。
			if i < len(hooks)-1 {
				continue
			}
			break
		}
		return urlV.String(), true, nil
	}
	return "", false, nil
}

// callLoadHooks 在默认读取之前调用钩子链的 load(url, context, nextLoad)。
// 返回 (source, format, handled, err)：handled=true 时调用方必须使用
// source/format 而非读取磁盘文件。
func (l *Loader) callLoadHooks(absPath string) (string, string, bool, error) {
	l.mu.Lock()
	hooks := append([]*registerHook(nil), l.hooks...)
	l.mu.Unlock()
	if len(hooks) == 0 {
		return "", "", false, nil
	}
	next := l.nextLoadFn()
	url := PathToFileURLString(absPath)
	for i, h := range hooks {
		ctxObj := engine.NewObject()
		res, err := l.callHookFn(h.load, []engine.Value{engine.Str(url), ctxObj, next})
		if err != nil {
			return "", "", false, fmt.Errorf("module: load hook %q failed: %w", h.url, err)
		}
		if res == nil || res.IsUndefined() {
			continue
		}
		o, ok := res.AsObject()
		if !ok {
			return "", "", false, fmt.Errorf("module: load hook %q returned non-object", h.url)
		}
		srcV, _ := o.Get("source")
		fmtV, err2 := o.Get("format")
		format := ""
		if err2 == nil && !fmtV.IsUndefined() {
			format = fmtV.String()
		}
		if srcV.IsUndefined() {
			if i < len(hooks)-1 {
				continue
			}
			break
		}
		source := srcV.String()
		if srcV.Type() == engine.TypeObject {
			// source 为 ArrayBuffer/Uint8Array 形态：转字符串。
			if ab, ok := engine.AsArrayBuffer(srcV); ok {
				source = string(ab)
			} else if b, ok := engine.AsBuffer(srcV); ok {
				source = string(b)
			}
		}
		return source, format, true, nil
	}
	return "", "", false, nil
}

// nextResolveFn 构造钩子链末端 resolve：默认解析（import 语境条件），
// 返回 { url: file:// }；解析失败抛出带 code 的 JS 错误。
func (l *Loader) nextResolveFn(parentPath string) engine.Value {
	return engine.NewFunction("nextResolve", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("nextResolve requires a specifier")
		}
		spec := args[0].String()
		resolved, err := l.resolver.ResolveImport(spec, parentPath)
		if err != nil {
			return engine.Undefined(), &jsModuleError{code: "ERR_MODULE_NOT_FOUND", msg: fmt.Sprintf("Cannot find module %q", spec)}
		}
		o := engine.NewObject()
		_ = o.Set("url", engine.Str(PathToFileURLString(resolved)))
		return o, nil
	})
}

// nextLoadFn 构造钩子链末端 load：默认读取并分类，返回
// { source, format, shortCircuit: false }。
func (l *Loader) nextLoadFn() engine.Value {
	return engine.NewFunction("nextLoad", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("nextLoad requires a url")
		}
		url := args[0].String()
		absPath := NormalizeModulePath(url)
		format := "commonjs"
		switch DetectSourceKind(absPath) {
		case SourceJSON:
			format = "json"
		case SourceTypeScript:
			format = "module"
		}
		data, err := os.ReadFile(absPath)
		if err != nil {
			return engine.Undefined(), err
		}
		o := engine.NewObject()
		_ = o.Set("source", engine.Str(string(data)))
		_ = o.Set("format", engine.Str(format))
		_ = o.Set("shortCircuit", engine.Boolean(false))
		return o, nil
	})
}

// jsModuleError 是带 Node 风格 code 属性的 JS 错误（钩子链 err 传播用）。
type jsModuleError struct {
	code string
	msg  string
}

func (e *jsModuleError) Error() string { return e.msg }

// runUnitFromSource 用给定源码（钩子转译产物）构造 SourceUnit 并执行，
// 跳过磁盘字节码缓存（内容与磁盘文件可能不一致）。
func (l *Loader) runUnitFromSource(absPath, source, format string) (engine.Value, error) {
	var kind ModuleKind
	switch format {
	case "json":
		data := []byte(source)
		val, err := parseOrderedJSON(data)
		if err != nil {
			return engine.Undefined(), fmt.Errorf("module: invalid JSON in %q: %w", absPath, err)
		}
		l.mu.Lock()
		l.cache[absPath] = val
		l.mu.Unlock()
		return val, nil
	case "commonjs":
		kind = ModuleCommonJS
	default:
		kind = ModuleESM
	}
	unit, err := parseHookSourceUnit([]byte(source), absPath, kind)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("module: hook source in %q: %w", absPath, err)
	}
	vm, ok := l.ctx.(*interpreter.VM)
	if !ok {
		return engine.Undefined(), fmt.Errorf("module: hooks require the bytecode VM engine")
	}
	return l.runUnitNoCache(vm, unit, absPath)
}

// parseHookSourceUnit 解析 hook 转译产物：与 ParseSourceUnit 一致，但跳过
// checkUnsupportedTS（产物已是 JS——jiti 等转译器负责剥离 TS 语法；对 CJS
// 风格产物做 token 级 enum/namespace 检测会造成误报）。
func parseHookSourceUnit(src []byte, path string, kind ModuleKind) (*SourceUnit, error) {
	src = stripSourceBOM(src)
	if DetectSourceKind(path) == SourceJSON {
		return &SourceUnit{Path: path, Source: src, SourceKind: SourceJSON, ModuleKind: kind}, nil
	}
	prog, err := parser.ParseModule(string(src))
	if err != nil {
		return nil, fmt.Errorf("parse error in %q: %w", path, err)
	}
	prog = ast.LowerJSX(prog).(*ast.Program)
	return &SourceUnit{
		Path:       path,
		Source:     src,
		SourceKind: DetectSourceKind(path),
		ModuleKind: kind,
		Program:    prog,
	}, nil
}

// runUnitNoCache 编译并执行 SourceUnit，不经过磁盘字节码缓存
// （钩子转译产物与磁盘文件内容可能不一致）。
func (l *Loader) runUnitNoCache(vm *interpreter.VM, unit *SourceUnit, fsPath string) (engine.Value, error) {
	var mod *bytecode.Module
	var err error
	if unit.ModuleKind == ModuleESM {
		prog := ast.DeepCopy(unit.Program)
		transformed := TransformESMToCJS(prog, unit.Path)
		wrapped := WrapESMAST(transformed, unit.Path)
		mod, err = vm.CompileAST(wrapped, unit.Path)
	} else {
		if HasESMDecls(unit.Program) {
			return engine.Undefined(), fmt.Errorf("module: %q: module type is commonjs but source contains ESM import/export syntax", unit.Path)
		}
		wrapped := WrapCJSSource(string(unit.Source))
		mod, err = vm.Compile(wrapped, unit.Path)
	}
	if err != nil {
		return engine.Undefined(), fmt.Errorf("module: error in %q: %w", unit.Path, err)
	}
	return l.RunPrecompiled(unit.Path, mod, unit.ModuleKind == ModuleESM)
}
