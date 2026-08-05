package module

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/engine/parser"
)

// loadESM loads and executes an ESM module, returning its exports.
func (l *Loader) loadESM(absPath string) (engine.Value, error) {
	return l.loadESMModule(absPath)
}

// loadESMFile loads, transforms, and executes an ESM module file (for Run).
func (l *Loader) loadESMFile(absPath string) error {
	_, err := l.loadESMModule(absPath)
	return err
}

// loadESMModule loads an ESM module and returns its exports.
func (l *Loader) loadESMModule(absPath string) (engine.Value, error) {
	src, err := os.ReadFile(absPath)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("module: cannot read %q: %w", absPath, err)
	}
	// 剥离 UTF-8 BOM（同 CJS，避免 BOM 字符导致 lexer 死循环）。
	src = stripBOM(src)

	prog, err := parser.ParseModule(string(src))
	if err != nil {
		return engine.Undefined(), fmt.Errorf("module: parse error in %q: %w", absPath, err)
	}

	// Check if the source actually has import/export; if not, treat as CJS.
	// 例外：.mjs 强制 ESM（Node 语义）；含顶层 await（TLA）也按 ESM
	// （无 import/export 的纯 TLA 模块，如 scripts/*.mjs）。
	if !hasESMDecls(prog) && !ast.HasTopLevelAwait(prog) && filepath.Ext(absPath) != ".mjs" {
		return l.loadCJS(absPath)
	}

	// Transform ESM AST to CJS-equivalent AST
	transformed := transformESMToCJS(prog, absPath)

	// Use the VM's EvalProgram to compile and run the transformed AST.
	vm, ok := l.ctx.(*interpreter.VM)
	if !ok {
		return engine.Undefined(), fmt.Errorf("module: ESM requires the bytecode VM engine")
	}

	// Create module/exports objects
	exports := l.newExports()
	moduleObj := engine.NewObject()
	_ = moduleObj.Set("exports", exports)

	// Pre-populate cache
	l.mu.Lock()
	l.cache[absPath] = exports
	l.mu.Unlock()

	// 包装转换后的 AST 为模块函数（P0-1）：require/module/exports 等作为
	// 词法参数注入，使 async/回调中的引用在异步恢复后依然可用。
	prog2 := wrapESMAST(transformed, absPath)

	// 编译转换后的 AST（优先字节码缓存），然后执行。
	// 字节码缓存（1C.14）：缓存键基于源文件元数据（转换是确定性的）。
	mod, compileErr := l.bcCache.compileOrLoad(absPath, func() (*bytecode.Module, error) {
		return vm.CompileAST(prog2, absPath)
	})
	var evalErr error
	var wrapper engine.Value
	if compileErr != nil {
		evalErr = compileErr
	} else {
		wrapper, evalErr = vm.RunModule(mod)
	}
	if evalErr != nil {
		l.mu.Lock()
		delete(l.cache, absPath)
		l.mu.Unlock()
		return engine.Undefined(), fmt.Errorf("module: error in %q: %w", absPath, evalErr)
	}

	// 以词法参数调用模块函数（this = exports）。
	requireFn := l.makeRequireFunc(absPath)
	importFn := l.makeImportFunc(absPath)
	modResult, evalErr := vm.InvokeFn(wrapper, exports, []engine.Value{
		requireFn,
		moduleObj,
		exports,
		engine.Str(absPath),
		engine.Str(filepath.Dir(absPath)),
		importFn,
	})
	// TLA：模块函数为 async，InvokeFn 返回 promise——同步等待 settle
	// （驱动微任务/任务队列，直至顶层 await 链完成）。
	if pv, ok := modResult.(*interpreter.PromiseValue); ok {
		_, evalErr = vm.AwaitPromise(pv)
	}
	vm.DrainMicrotasks()
	if evalErr != nil {
		l.mu.Lock()
		delete(l.cache, absPath)
		l.mu.Unlock()
		return engine.Undefined(), fmt.Errorf("module: error in %q: %w", absPath, evalErr)
	}

	// Update cache with final exports
	finalExports, _ := moduleObj.Get("exports")
	l.mu.Lock()
	l.cache[absPath] = finalExports
	l.mu.Unlock()

	return finalExports, nil
}

// wrapESMAST 将转换后的 ESM AST 包装为模块函数表达式：
//
//	(function(require, module, exports, __filename, __dirname, __import) { <body> })
func wrapESMAST(prog *ast.Program, filename string) *ast.Program {
	params := []*ast.Identifier{
		{Name: "require"},
		{Name: "module"},
		{Name: "exports"},
		{Name: "__filename"},
		{Name: "__dirname"},
		{Name: "__import"},
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

// hasESMDecls returns true if the program contains import/export declarations.
func hasESMDecls(prog *ast.Program) bool {
	for _, stmt := range prog.Body {
		switch stmt.(type) {
		case *ast.ImportDecl, *ast.ExportDecl, *ast.ExportDefaultDecl:
			return true
		}
	}
	return false
}

// transformESMToCJS rewrites an ESM program's AST to use CJS equivalents:
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
func transformESMToCJS(prog *ast.Program, filename string) *ast.Program {
	var newBody []ast.Statement
	var exportAssignments []ast.Statement
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
					// import x from 'mod' → var x = __imp_N.default !== undefined ? __imp_N.default : __imp_N
					newBody = append(newBody, makeDefaultImport(spec.Local, impVar, n.Loc))
				} else {
					// import {a as b} from 'mod' → var b = __imp_N.a
					newBody = append(newBody, makeVarDecl(spec.Local, &ast.MemberExpr{
						Object:   &ast.Identifier{Name: impVar, Loc: n.Loc},
						Property: &ast.Identifier{Name: spec.Imported, Loc: n.Loc},
						Loc:      n.Loc,
					}, n.Loc))
				}
			}

		case *ast.ExportDecl:
			if n.Declaration != nil {
				// export <decl> → keep the declaration, add export assignment
				newBody = append(newBody, n.Declaration)
				// Collect export assignments for the declared names
				for _, name := range declNames(n.Declaration) {
					exportAssignments = append(exportAssignments, makeExportAssignment(name, name, n.Loc))
				}
			} else if n.IsStar && n.Source != "" {
				// export * from 'mod' → Object.assign(module.exports, require('mod'))
				newBody = append(newBody, makeStarReexport(n.Source, n.Loc))
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

	return &ast.Program{
		Body:       newBody,
		SourceFile: prog.SourceFile,
		Loc:        prog.Loc,
	}
}

// makeRequireCall creates: var __imp_N = require('mod')
func makeRequireCall(varName, source string, loc ast.Pos) *ast.VarDecl {
	return &ast.VarDecl{
		Kind: "var",
		Decls: []ast.VarDeclarator{{
			Name: &ast.Identifier{Name: varName, Loc: loc},
			Init: &ast.CallExpr{
				Callee:    &ast.Identifier{Name: "require", Loc: loc},
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

// makeStarReexport creates: Object.assign(module.exports, require('mod'))
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
					Callee:    &ast.Identifier{Name: "require", Loc: loc},
					Arguments: []ast.Expression{&ast.StringLit{Value: source, Loc: loc}},
					Loc:       loc,
				},
			},
			Loc: loc,
		},
		Loc: loc,
	}
}

// declNames extracts the names declared by a VarDecl, FunctionDecl, or ClassDecl.
func declNames(stmt ast.Statement) []string {
	switch n := stmt.(type) {
	case *ast.VarDecl:
		var names []string
		for _, d := range n.Decls {
			if d.Name != nil {
				names = append(names, d.Name.Name)
			}
			if d.Pattern != nil {
				names = append(names, patternNames(d.Pattern)...)
			}
		}
		return names
	case *ast.FunctionDecl:
		if n.Name != nil {
			return []string{n.Name.Name}
		}
	case *ast.ClassDecl:
		if n.Name != nil {
			return []string{n.Name.Name}
		}
	}
	return nil
}

// patternNames extracts names from a destructuring pattern.
func patternNames(p ast.Pattern) []string {
	var names []string
	switch pat := p.(type) {
	case *ast.Identifier:
		names = append(names, pat.Name)
	case *ast.ArrayPattern:
		for _, elem := range pat.Elements {
			if elem.Target != nil {
				names = append(names, patternNames(elem.Target)...)
			}
		}
	case *ast.ObjectPattern:
		for _, prop := range pat.Properties {
			names = append(names, patternNames(prop.Value)...)
		}
	}
	return names
}
