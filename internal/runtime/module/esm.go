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
//
//	export var x = 1              →  var x = 1;  (plus module.exports.x = x at end)
//	export function f() {}        →  function f() {}  (plus module.exports.f = f at end)
//	export class C {}             →  class C {}  (plus module.exports.C = C at end)
//	export {a, b as c}            →  (plus module.exports.a = a; module.exports.c = b at end)
//	export default expr           →  (plus module.exports.default = expr at end)
//	export * from 'mod'           →  Object.assign(module.exports, require('mod'))
func TransformESMToCJS(prog *ast.Program, filename string) *ast.Program {
	var newBody []ast.Statement
	var exportAssignments []ast.Statement
	lazyBindings := make(map[string]ast.Expression)
	impCounter := 0

	for _, stmt := range prog.Body {
		switch n := stmt.(type) {
		case *ast.ImportDecl:
			// Generate require() call and local bindings
			impVar := fmt.Sprintf("__imp_%d", impCounter)
			impCounter++

			// var __imp_N = require('mod')
			newBody = append(newBody, makeRequireCall(impVar, n.Source, n.Loc))

			// Bind specifiers
			for _, spec := range n.Specifiers {
				if spec.Imported == "*" {
					// import * as ns from 'mod' → var ns = __imp_N
					newBody = append(newBody, makeVarDecl(spec.Local, &ast.Identifier{Name: impVar, Loc: n.Loc}, n.Loc))
				} else if spec.Imported == "" {
					// Named/default imports use a live property lookup. A plain local
					// snapshot breaks ESM cycles (for example TypeBox's Record modules).
					lazyBindings[spec.Local] = makeDefaultImportExpr(impVar, n.Loc)
				} else {
					lazyBindings[spec.Local] = makeNamedImportExpr(impVar, spec.Imported, n.Loc)
				}
			}

		case *ast.ExportDecl:
			if n.Declaration != nil {
				// export <decl> → keep the declaration, add export assignment
				newBody = append(newBody, n.Declaration)
				// Collect export assignments for the declared names
				for _, name := range ast.DeclNames(n.Declaration) {
					exportAssignments = append(exportAssignments, makeExportAssignment(name, name, n.Loc))
				}
			} else if n.IsStar && n.Source != "" {
				if n.StarName != "" {
					// export * as ns from 'mod' → module.exports.ns = require('mod')
					impVar := fmt.Sprintf("__imp_%d", impCounter)
					impCounter++
					newBody = append(newBody, makeRequireCall(impVar, n.Source, n.Loc))
					exportAssignments = append(exportAssignments, makeExportAssignment(n.StarName, impVar, n.Loc))
				} else {
					// export * from 'mod' → Object.assign(module.exports, require('mod'))
					newBody = append(newBody, makeStarReexport(n.Source, n.Loc))
				}
			} else if n.Source != "" {
				// export {a, b} from 'mod' → re-export from another module
				impVar := fmt.Sprintf("__imp_%d", impCounter)
				impCounter++
				newBody = append(newBody, makeRequireCall(impVar, n.Source, n.Loc))
				for _, spec := range n.Specifiers {
					exportAssignments = append(exportAssignments, makeExportAssignmentFrom(spec.Exported, impVar, spec.Local, n.Loc))
				}
			} else {
				// export {a, b as c} → module.exports.a = a; module.exports.c = b
				for _, spec := range n.Specifiers {
					exportAssignments = append(exportAssignments, makeExportAssignment(spec.Exported, spec.Local, n.Loc))
				}
			}

		case *ast.ExportDefaultDecl:
			// export default expr → module.exports.default = expr
			exportAssignments = append(exportAssignments, makeExportAssignment("default", "", n.Loc, n.Expression))

		default:
			// Keep non-import/export statements as-is
			newBody = append(newBody, stmt)
		}
	}

	// Append all export assignments at the end
	newBody = append(newBody, exportAssignments...)
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
	imp := &ast.Identifier{Name: impVar, Loc: loc}
	defaultProp := &ast.MemberExpr{
		Object:   imp,
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
		Alternate:  &ast.Identifier{Name: impVar, Loc: loc},
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

// makeDefaultImport creates: var name = __imp.default !== undefined ? __imp.default : __imp
func makeDefaultImport(name, impVar string, loc ast.Pos) *ast.VarDecl {
	imp := &ast.Identifier{Name: impVar, Loc: loc}
	defaultProp := &ast.MemberExpr{
		Object:   imp,
		Property: &ast.Identifier{Name: "default", Loc: loc},
		Loc:      loc,
	}
	undefinedLit := &ast.UndefinedLit{Loc: loc}
	// __imp.default !== undefined
	test := &ast.BinaryExpr{
		Left:  defaultProp,
		Op:    "!==",
		Right: undefinedLit,
		Loc:   loc,
	}
	// __imp.default !== undefined ? __imp.default : __imp
	cond := &ast.ConditionalExpr{
		Test:       test,
		Consequent: defaultProp,
		Alternate:  &ast.Identifier{Name: impVar, Loc: loc},
		Loc:        loc,
	}
	return makeVarDecl(name, cond, loc)
}

// makeExportAssignment creates: module.exports.<exported> = <local>
// If expr is non-nil, uses expr instead of local identifier.
func makeExportAssignment(exported, local string, loc ast.Pos, expr ...ast.Expression) ast.Statement {
	var rhs ast.Expression
	if len(expr) > 0 && expr[0] != nil {
		rhs = expr[0]
	} else {
		rhs = &ast.Identifier{Name: local, Loc: loc}
	}
	return &ast.ExprStmt{
		Expr: &ast.AssignExpr{
			Left: &ast.MemberExpr{
				Object: &ast.MemberExpr{
					Object:   &ast.Identifier{Name: "module", Loc: loc},
					Property: &ast.Identifier{Name: "exports", Loc: loc},
					Loc:      loc,
				},
				Property: &ast.Identifier{Name: exported, Loc: loc},
				Loc:      loc,
			},
			Op:    "=",
			Right: rhs,
			Loc:   loc,
		},
		Loc: loc,
	}
}

// makeExportAssignmentFrom creates: module.exports.<exported> = __imp_N.<local>
func makeExportAssignmentFrom(exported, impVar, local string, loc ast.Pos) ast.Statement {
	return &ast.ExprStmt{
		Expr: &ast.AssignExpr{
			Left: &ast.MemberExpr{
				Object: &ast.MemberExpr{
					Object:   &ast.Identifier{Name: "module", Loc: loc},
					Property: &ast.Identifier{Name: "exports", Loc: loc},
					Loc:      loc,
				},
				Property: &ast.Identifier{Name: exported, Loc: loc},
				Loc:      loc,
			},
			Op: "=",
			Right: &ast.MemberExpr{
				Object:   &ast.Identifier{Name: impVar, Loc: loc},
				Property: &ast.Identifier{Name: local, Loc: loc},
				Loc:      loc,
			},
			Loc: loc,
		},
		Loc: loc,
	}
}

// makeStarReexport creates: Object.assign(module.exports, __importReq('mod'))
func makeStarReexport(source string, loc ast.Pos) ast.Statement {
	return &ast.ExprStmt{
		Expr: &ast.CallExpr{
			Callee: &ast.MemberExpr{
				Object:   &ast.Identifier{Name: "Object", Loc: loc},
				Property: &ast.Identifier{Name: "assign", Loc: loc},
				Loc:      loc,
			},
			Arguments: []ast.Expression{
				&ast.MemberExpr{
					Object:   &ast.Identifier{Name: "module", Loc: loc},
					Property: &ast.Identifier{Name: "exports", Loc: loc},
					Loc:      loc,
				},
				&ast.CallExpr{
					Callee:    &ast.Identifier{Name: "__importReq", Loc: loc},
					Arguments: []ast.Expression{&ast.StringLit{Value: source, Loc: loc}},
					Loc:       loc,
				},
			},
			Loc: loc,
		},
	}
}

