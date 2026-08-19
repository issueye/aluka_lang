package module

import (
	"fmt"
	"path/filepath"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// loadESM loads and executes an ESM module, returning its exports.
func (l *Loader) loadESM(absPath string) (engine.Value, error) {
	return l.loadModuleFile(absPath)
}

// loadESMFile loads, transforms, and executes an ESM module file (for Run).
func (l *Loader) loadESMFile(absPath string) error {
	_, err := l.loadModuleFile(absPath)
	return err
}

// RunPrecompiled 执行一个已编译的模块（文件模式与构建产物模式共用）。
//
// path 是模块标识路径（产物模式为构建时记录的路径，可为虚拟路径）；
// isESM 决定词法参数集（ESM 多 __importMeta/__importReq）与 TLA 等待语义。
//
// 流程：构造 module/exports → 缓存预填（循环依赖）→ RunModule → 以词法
// 参数 InvokeFn → TLA settle + 排空微任务 → 读取最终 exports 更新缓存。
func (l *Loader) RunPrecompiled(path string, mod *bytecode.Module, isESM bool) (engine.Value, error) {
	vm, ok := l.ctx.(*interpreter.VM)
	if !ok {
		return engine.Undefined(), fmt.Errorf("module: RunPrecompiled requires the bytecode VM engine")
	}

	// Create module/exports objects
	exports := l.newExports()
	moduleObj := engine.NewObject()
	_ = moduleObj.Set("exports", exports)
	_ = moduleObj.Set("id", engine.Str(path))
	_ = moduleObj.Set("filename", engine.Str(path))
	_ = moduleObj.Set("loaded", engine.Boolean(false))
	_ = moduleObj.Set("path", engine.Str(filepath.Dir(path)))

	// Pre-populate cache for circular dependencies.
	l.mu.Lock()
	l.cache[path] = exports
	l.mu.Unlock()

	wrapper, evalErr := vm.RunModule(mod)
	if evalErr != nil {
		l.mu.Lock()
		delete(l.cache, path)
		l.mu.Unlock()
		return engine.Undefined(), fmt.Errorf("module: error in %q: %w", path, evalErr)
	}

	// 以词法参数调用模块函数（this = exports）。ESM 额外注入
	// __importMeta/__importReq；CJS 无 TLA（模块函数非 async）。
	requireFn := l.makeRequireFunc(path)
	importFn := l.makeImportFunc(path)
	var args []engine.Value
	if isESM {
		args = []engine.Value{
			requireFn,
			moduleObj,
			exports,
			engine.Str(path),
			engine.Str(filepath.Dir(path)),
			importFn,
			l.makeImportMetaFunc(path),
			l.makeImportReqFunc(path),
		}
	} else {
		args = []engine.Value{
			requireFn,
			moduleObj,
			exports,
			engine.Str(path),
			engine.Str(filepath.Dir(path)),
			importFn,
		}
	}
	modResult, evalErr := vm.InvokeFn(wrapper, exports, args)
	// TLA：ESM 模块函数为 async，InvokeFn 返回 promise——同步等待 settle
	// （驱动微任务/任务队列，直至顶层 await 链完成）。
	if isESM {
		if pv, ok := modResult.(*interpreter.PromiseValue); ok {
			_, evalErr = vm.AwaitPromise(pv)
		}
	}
	vm.DrainMicrotasks()
	if evalErr != nil {
		l.mu.Lock()
		delete(l.cache, path)
		l.mu.Unlock()
		return engine.Undefined(), fmt.Errorf("module: error in %q: %w", path, evalErr)
	}

	// Get the final module.exports (may have been reassigned).
	// 注意：module.exports 可能被 Object.defineProperty 重定义为 getter 访问器
	// （如 ansi-styles）。Go 侧 moduleObj.Get 不触发 JS getter，故经 VM 调用
	// getter 取真实值。
	var finalExports engine.Value
	finalExports = exports
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

	// ESM 模块的 CJS 互操作标记（Node 22 require(esm) 语义）：
	// 导出对象带 __esModule: true，CJS 侧可据此识别 ESM 导出。
	if isESM {
		if fo, ok := finalExports.AsObject(); ok {
			_ = fo.Set("__esModule", engine.Boolean(true))
		}
	}

	// Update cache with final exports
	l.mu.Lock()
	l.cache[path] = finalExports
	l.mu.Unlock()

	return finalExports, nil
}

// WrapESMAST 将转换后的 ESM AST 包装为模块函数表达式（构建产物模式复用）：
//
//	(function(require, module, exports, __filename, __dirname, __import, __importMeta, __importReq) { <body> })
func WrapESMAST(prog *ast.Program, filename string) *ast.Program {
	params := []*ast.Identifier{
		{Name: "require"},
		{Name: "module"},
		{Name: "exports"},
		{Name: "__filename"},
		{Name: "__dirname"},
		{Name: "__import"},
		{Name: "__importMeta"},
		{Name: "__importReq"},
	}
	fnExpr := &ast.FunctionExpr{
		Name:     nil,
		Params:   params,
		Defaults: make([]ast.Expression, len(params)),
		Body:     &ast.BlockStmt{Body: prog.Body, Loc: prog.Loc},
		// 模块含顶层 await（TLA）时模块函数为 async：顶层 await 经
		// asyncRunner 挂起/恢复，loadESM 等待返回的 promise settle。
		IsAsync: ast.HasTopLevelAwait(prog),
		Loc:     prog.Loc,
	}
	// 顶层：返回模块函数（表达式），供 RunModule 求值为闭包。
	return &ast.Program{
		Body: []ast.Statement{
			&ast.ExprStmt{Expr: fnExpr, Loc: prog.Loc},
		},
		SourceFile: prog.SourceFile,
		Loc:        prog.Loc,
	}
}

// HasESMDecls returns true if the program contains import/export declarations.
func HasESMDecls(prog *ast.Program) bool {
	for _, stmt := range prog.Body {
		switch stmt.(type) {
		case *ast.ImportDecl, *ast.ExportDecl, *ast.ExportDefaultDecl:
			return true
		}
	}
	return false
}

// TransformESMToCJS rewrites an ESM program's AST to use CJS equivalents
// （构建产物模式复用，产物在执行前已转换，运行时零开销）：
//
//	import x from 'mod'           →  var __imp_N = require('mod'); var x = __imp_N.default !== undefined ? __imp_N.default : __imp_N
//	import * as ns from 'mod'     →  var ns = require('mod')
//	import {a, b as c} from 'mod' →  var __imp_N = require('mod'); var a = __imp_N.a; var c = __imp_N.b
//	import 'mod'                   →  require('mod')
//	（import 的 require 调用统一提升到模块体顶部，见函数内注释）
//
//	export var x = 1              →  var x = 1;  (plus enumerable live getter on exports)
//	export function f() {}        →  function f() {}  (plus live getter)
//	export class C {}             →  class C {}  (plus live getter)
//	export {a, b as c}            →  live getter；a 为导入绑定时经 liveImpRef 守卫
//	export default expr           →  var __default_N = expr at end; live getter reads it
//	export * from 'mod'           →  require hoisted; Object.assign copy deferred to end
func TransformESMToCJS(prog *ast.Program, filename string) *ast.Program {
	var newBody []ast.Statement
	// import 的 require 调用提升到模块体顶部（按源码顺序）：ESM 语义要求
	// 所有模块请求在模块体执行前求值，import 语句位置不影响依赖求值时机
	//（对齐 Node/Babel CJS 产物；body 中语句先于 import 文本位置引用导入
	// 绑定时也能取到已求值的模块）。
	var importRequires []ast.Statement
	var exportGetters []ast.Statement
	var lateExports []ast.Statement
	// export {导入名 as x}（无 from）：导入语句可出现在 export 之后，需等
	// 主循环结束、导入来源齐全后再生成带守卫的 getter。
	var localExports []localExportSpec
	lazyBindings := make(map[string]ast.Expression)
	importSources := make(map[string]importBinding)
	impCounter := 0
	defaultTmp := 0

	for _, stmt := range prog.Body {
		switch n := stmt.(type) {
		case *ast.ImportDecl:
			// Generate require() call and local bindings
			impVar := fmt.Sprintf("__imp_%d", impCounter)
			impCounter++

			// var __imp_N = require('mod')  — hoisted below
			importRequires = append(importRequires, makeRequireCall(impVar, n.Source, n.Loc))

			// Bind specifiers
			for _, spec := range n.Specifiers {
				if spec.Imported == "*" {
					// import * as ns from 'mod' → var ns = __imp_N
					newBody = append(newBody, makeVarDecl(spec.Local, &ast.Identifier{Name: impVar, Loc: n.Loc}, n.Loc))
					importSources[spec.Local] = importBinding{impVar: impVar, imported: "*", source: n.Source}
				} else if spec.Imported == "" {
					// Named/default imports use a live property lookup. A plain local
					// snapshot breaks ESM cycles (for example TypeBox's Record modules).
					lazyBindings[spec.Local] = makeDefaultImportExpr(impVar, n.Loc)
					importSources[spec.Local] = importBinding{impVar: impVar, imported: "", source: n.Source}
				} else {
					lazyBindings[spec.Local] = makeNamedImportExpr(impVar, spec.Imported, n.Loc)
					importSources[spec.Local] = importBinding{impVar: impVar, imported: spec.Imported, source: n.Source}
				}
			}

		case *ast.ExportDecl:
			if n.Declaration != nil {
				// export <decl> → keep the declaration, live getter on exports
				newBody = append(newBody, n.Declaration)
				for _, name := range ast.DeclNames(n.Declaration) {
					exportGetters = append(exportGetters, makeExportGetter(name, name, n.Loc))
				}
			} else if n.IsStar && n.Source != "" {
				if n.StarName != "" {
					// export * as ns from 'mod' → 先加载，再 live getter；
					// 循环期间 __imp_N 未赋值时经 require 取部分导出活对象。
					impVar := fmt.Sprintf("__imp_%d", impCounter)
					impCounter++
					importRequires = append(importRequires, makeRequireCall(impVar, n.Source, n.Loc))
					exportGetters = append(exportGetters, makeExportGetterExpr(n.StarName, liveImpRef(impVar, n.Source, n.Loc), n.Loc))
				} else {
					// export * from 'mod'：require 提升；拷贝放到模块末尾，
					// 避免循环依赖时对尚未赋值的命名导出做快照。
					impVar := fmt.Sprintf("__imp_%d", impCounter)
					impCounter++
					importRequires = append(importRequires, makeRequireCall(impVar, n.Source, n.Loc))
					lateExports = append(lateExports, makeStarCopy(impVar, n.Loc))
				}
			} else if n.Source != "" {
				// export {a, b} from 'mod' → live getter 读依赖的命名导出
				impVar := fmt.Sprintf("__imp_%d", impCounter)
				impCounter++
				importRequires = append(importRequires, makeRequireCall(impVar, n.Source, n.Loc))
				for _, spec := range n.Specifiers {
					exportGetters = append(exportGetters, makeExportGetterFrom(spec.Exported, impVar, n.Source, spec.Local, n.Loc))
				}
			} else {
				// export {a, b as c} → 延后生成（见 localExports 注释）
				for _, spec := range n.Specifiers {
					localExports = append(localExports, localExportSpec{exported: spec.Exported, local: spec.Local, loc: n.Loc})
				}
			}

		case *ast.ExportDefaultDecl:
			if fn, ok := n.Expression.(*ast.FunctionExpr); ok && fn.Name != nil {
				// export default function foo() {} → 函数声明提升 + default 活绑定
				fnDecl := &ast.FunctionDecl{
					Name:          fn.Name,
					Params:        fn.Params,
					ParamPatterns: fn.ParamPatterns,
					Defaults:      fn.Defaults,
					RestParam:     fn.RestParam,
					Body:          fn.Body,
					IsAsync:       fn.IsAsync,
					IsGenerator:   fn.IsGenerator,
					Loc:           fn.Loc,
				}
				newBody = append(newBody, fnDecl)
				exportGetters = append(exportGetters, makeExportGetter("default", fn.Name.Name, n.Loc))
			} else if cls, ok := n.Expression.(*ast.ClassExpr); ok && cls.Name != nil {
				clsDecl := &ast.ClassDecl{
					Name:       cls.Name,
					SuperClass: cls.SuperClass,
					Body:       cls.Body,
					Loc:        cls.Loc,
				}
				newBody = append(newBody, clsDecl)
				exportGetters = append(exportGetters, makeExportGetter("default", cls.Name.Name, n.Loc))
			} else {
				// export default expr：先占位绑定，求值后再赋值（避免重复求值）。
				tmp := fmt.Sprintf("__default_%d", defaultTmp)
				defaultTmp++
				exportGetters = append(exportGetters, makeExportGetter("default", tmp, n.Loc))
				lateExports = append(lateExports, makeVarDecl(tmp, n.Expression, n.Loc))
			}

		default:
			// Keep non-import/export statements as-is
			newBody = append(newBody, stmt)
		}
	}

	// export {导入名 as x}：getter 先于 require 安装，循环依赖的对方模块在
	// 我们的 import 求值期间读取时 __imp_N 尚未赋值。经 liveImpRef 回退到
	// require 取依赖方缓存预填的部分导出（活对象），可读到其已提升的函数
	// 导出（Node ESM 链接语义近似）；裸 `return __imp_N.x` 会在此抛
	// TypeError。非导入名的局部导出仍用普通 getter。
	for _, s := range localExports {
		b, isImport := importSources[s.local]
		if !isImport {
			exportGetters = append(exportGetters, makeExportGetter(s.exported, s.local, s.loc))
			continue
		}
		var lazy ast.Expression
		switch b.imported {
		case "":
			lazy = makeDefaultImportExprOn(liveImpRef(b.impVar, b.source, s.loc), s.loc)
		case "*":
			lazy = liveImpRef(b.impVar, b.source, s.loc)
		default:
			lazy = &ast.MemberExpr{
				Object:   liveImpRef(b.impVar, b.source, s.loc),
				Property: &ast.Identifier{Name: b.imported, Loc: s.loc},
				Loc:      s.loc,
			}
		}
		exportGetters = append(exportGetters, makeExportGetterExpr(s.exported, lazy, s.loc))
	}

	// 命名导出 getter 必须先于 import require 安装：循环依赖的对方模块
	// 在我们的 import 求值期间就能读到 hoisted function 等活绑定。
	var body []ast.Statement
	body = append(body, exportGetters...)
	body = append(body, importRequires...)
	body = append(body, newBody...)
	body = append(body, lateExports...)
	newBody = body
	if len(lazyBindings) > 0 {
		rewriteImportedIdentifiers(newBody, lazyBindings)
	}

	return &ast.Program{
		Body:       newBody,
		SourceFile: prog.SourceFile,
		Loc:        prog.Loc,
	}
}

// makeDefaultImportExpr creates the live lookup used for a default import.
func makeDefaultImportExpr(impVar string, loc ast.Pos) ast.Expression {
	return makeDefaultImportExprOn(&ast.Identifier{Name: impVar, Loc: loc}, loc)
}

// makeDefaultImportExprOn 是 makeDefaultImportExpr 的泛化形态，依赖对象
// （root）可为 liveImpRef 生成的守卫表达式：
//
//	root.default !== undefined ? root.default : root
func makeDefaultImportExprOn(root ast.Expression, loc ast.Pos) ast.Expression {
	defaultProp := &ast.MemberExpr{
		Object:   root,
		Property: &ast.Identifier{Name: "default", Loc: loc},
		Loc:      loc,
	}
	return &ast.ConditionalExpr{
		Test: &ast.BinaryExpr{
			Left:  defaultProp,
			Op:    "!==",
			Right: &ast.UndefinedLit{Loc: loc},
			Loc:   loc,
		},
		Consequent: defaultProp,
		Alternate:  root,
		Loc:        loc,
	}
}

func makeNamedImportExpr(impVar, imported string, loc ast.Pos) ast.Expression {
	member := &ast.MemberExpr{
		Object:   &ast.Identifier{Name: impVar, Loc: loc},
		Property: &ast.Identifier{Name: imported, Loc: loc},
		Loc:      loc,
	}
	// TypeBox publishes these patterns as constants from modules that form a
	// cycle through its type engine. Keep their standard values if the cycle
	// exposes the dependency before its const initializer has run.
	fallbacks := map[string]string{
		"StringPattern":  ".*",
		"IntegerPattern": "-?(?:0|[1-9][0-9]*)",
		"NumberPattern":  "-?(?:0|[1-9][0-9]*)(?:\\.[0-9]+)?",
	}
	if fallback, ok := fallbacks[imported]; ok {
		return &ast.ConditionalExpr{
			Test: &ast.BinaryExpr{
				Left:  member,
				Op:    "!==",
				Right: &ast.UndefinedLit{Loc: loc},
				Loc:   loc,
			},
			Consequent: member,
			Alternate:  &ast.StringLit{Value: fallback, Loc: loc},
			Loc:        loc,
		}
	}
	return member
}

// rewriteImportedIdentifiers replaces references to named/default imports with
// member expressions that read the current export value. This preserves ESM
// live-binding behavior across circular dependencies while leaving declaration
// names and non-computed property keys untouched.
//
// 基于统一引用位置遍历 ast.RewriteRefs（internal/engine/ast/walk.go）原地
// 改写，取代原先的反射 + 字段名白名单启发式。相比旧实现修复：计算属性
// `obj[imported]` 中的引用现被正确改写（原实现按字段名 `Property` 一刀切
// 跳过）；声明名/非计算属性键/模式绑定名依旧不触碰。
func rewriteImportedIdentifiers(body []ast.Statement, bindings map[string]ast.Expression) {
	for _, stmt := range body {
		ast.RewriteRefs(stmt, func(id *ast.Identifier) ast.Node {
			if repl, found := bindings[id.Name]; found {
				return repl
			}
			return nil
		})
	}
}

// makeRequireCall creates: var __imp_N = __importReq('mod')
//
// 用 __importReq 而非 require：ESM 静态导入按 import 语境解析 exports 条件
// （含 "import"），与 Node 的 import 语义一致；require 语境（不含 "import"）
// 会让 `import x from 'pkg'` 与 require('pkg') 解析到不同入口。
func makeRequireCall(varName, source string, loc ast.Pos) *ast.VarDecl {
	return &ast.VarDecl{
		Kind: "var",
		Decls: []ast.VarDeclarator{{
			Name: &ast.Identifier{Name: varName, Loc: loc},
			Init: &ast.CallExpr{
				Callee:    &ast.Identifier{Name: "__importReq", Loc: loc},
				Arguments: []ast.Expression{&ast.StringLit{Value: source, Loc: loc}},
				Loc:       loc,
			},
		}},
		Loc: loc,
	}
}

// importBinding 记录一个导入本地名的来源：依赖 require 变量、导入名
// （"" 为 default，"*" 为命名空间导入）与模块说明符（循环期间惰性 require 用）。
type importBinding struct {
	impVar   string
	imported string
	source   string
}

// localExportSpec 是无 from 的 `export {a as b}` 待延后生成的导出。
type localExportSpec struct {
	exported string
	local    string
	loc      ast.Pos
}

// liveImpRef 生成 `__imp_N != null ? __imp_N : __importReq('source')`：
// 模块求值完成后走已赋值的 __imp_N（零额外调用）；循环依赖期间 __imp_N
// 尚未赋值，回退 require 取缓存预填的部分导出（活对象），使对方模块能
// 读到我们已提升/已就绪的导出。require 对加载中的模块命中预填缓存，
// 不会重入求值。
func liveImpRef(impVar, source string, loc ast.Pos) ast.Expression {
	return &ast.ConditionalExpr{
		Test: &ast.BinaryExpr{
			Left:  &ast.Identifier{Name: impVar, Loc: loc},
			Op:    "!=",
			Right: &ast.NullLit{Loc: loc},
			Loc:   loc,
		},
		Consequent: &ast.Identifier{Name: impVar, Loc: loc},
		Alternate: &ast.CallExpr{
			Callee:    &ast.Identifier{Name: "__importReq", Loc: loc},
			Arguments: []ast.Expression{&ast.StringLit{Value: source, Loc: loc}},
			Loc:       loc,
		},
		Loc: loc,
	}
}

// makeVarDecl creates: var name = <expr>
func makeVarDecl(name string, expr ast.Expression, loc ast.Pos) *ast.VarDecl {
	return &ast.VarDecl{
		Kind: "var",
		Decls: []ast.VarDeclarator{{
			Name: &ast.Identifier{Name: name, Loc: loc},
			Init: expr,
		}},
		Loc: loc,
	}
}

// makeExportGetter creates:
//
//	globalThis.Object.defineProperty(module.exports, "<exported>", {
//	  enumerable: true, configurable: true,
//	  get: function() { return <local>; }
//	})
//
// 活绑定：循环 require 在模块体尚未跑完时也能读到已提升的 function / 随后初始化的 const。
func makeExportGetter(exported, local string, loc ast.Pos) ast.Statement {
	return makeExportGetterExpr(exported, &ast.Identifier{Name: local, Loc: loc}, loc)
}

// makeExportGetterFrom creates a live getter: return <liveRef>.<local>
// （liveRef 见 liveImpRef：循环期间 __imp_N 未赋值时回退 require 取依赖方
// 部分导出，能读到其已提升的函数导出；正常路径只读已赋值的 __imp_N）。
func makeExportGetterFrom(exported, impVar, source, local string, loc ast.Pos) ast.Statement {
	member := &ast.MemberExpr{
		Object:   liveImpRef(impVar, source, loc),
		Property: &ast.Identifier{Name: local, Loc: loc},
		Loc:      loc,
	}
	return makeExportGetterExpr(exported, member, loc)
}

func makeExportGetterExpr(exported string, rhs ast.Expression, loc ast.Pos) ast.Statement {
	getter := &ast.FunctionExpr{
		Params:   []*ast.Identifier{},
		Defaults: []ast.Expression{},
		Body: &ast.BlockStmt{
			Body: []ast.Statement{
				&ast.ReturnStmt{Arg: rhs, Loc: loc},
			},
			Loc: loc,
		},
		Loc: loc,
	}
	desc := &ast.ObjectLit{
		Properties: []ast.Property{
			{Key: &ast.Identifier{Name: "enumerable", Loc: loc}, Value: &ast.BoolLit{Value: true, Loc: loc}, Loc: loc},
			{Key: &ast.Identifier{Name: "configurable", Loc: loc}, Value: &ast.BoolLit{Value: true, Loc: loc}, Loc: loc},
			{Key: &ast.Identifier{Name: "get", Loc: loc}, Value: getter, Loc: loc},
		},
		Loc: loc,
	}
	return &ast.ExprStmt{
		Expr: &ast.CallExpr{
			Callee: globalObjectMember("defineProperty", loc),
			Arguments: []ast.Expression{
				&ast.MemberExpr{
					Object:   &ast.Identifier{Name: "module", Loc: loc},
					Property: &ast.Identifier{Name: "exports", Loc: loc},
					Loc:      loc,
				},
				&ast.StringLit{Value: exported, Loc: loc},
				desc,
			},
			Loc: loc,
		},
		Loc: loc,
	}
}

// globalObjectMember 生成 globalThis.Object.<prop>。注入的
// Object.defineProperty / Object.assign 不能写成裸标识符 Object：模块若
// `import { Object } from ...`（TypeBox types/object.mjs 的工厂名），
// rewriteImportedIdentifiers 会把我们生成的 Object.defineProperty 改成
// __imp.Object.defineProperty，而 export getter 又在 require 之前执行，
// 于是变成 undefined.Object → "reading 'Object'"。
func globalObjectMember(prop string, loc ast.Pos) *ast.MemberExpr {
	return &ast.MemberExpr{
		Object: &ast.MemberExpr{
			Object:   &ast.Identifier{Name: "globalThis", Loc: loc},
			Property: &ast.Identifier{Name: "Object", Loc: loc},
			Loc:      loc,
		},
		Property: &ast.Identifier{Name: prop, Loc: loc},
		Loc:      loc,
	}
}

// makeStarCopy creates: globalThis.Object.assign(module.exports, __imp_N)
// require 已提升；在模块末尾拷贝，循环依赖的对方此时通常已写完命名导出。
func makeStarCopy(impVar string, loc ast.Pos) ast.Statement {
	return &ast.ExprStmt{
		Expr: &ast.CallExpr{
			Callee: globalObjectMember("assign", loc),
			Arguments: []ast.Expression{
				&ast.MemberExpr{
					Object:   &ast.Identifier{Name: "module", Loc: loc},
					Property: &ast.Identifier{Name: "exports", Loc: loc},
					Loc:      loc,
				},
				&ast.Identifier{Name: impVar, Loc: loc},
			},
			Loc: loc,
		},
		Loc: loc,
	}
}
