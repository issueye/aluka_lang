// Package minify 实现构建期最小化（T2-B2，docs/test-bundle-optimize-plan.md §5.2）：
//
//   - 死代码消除：常量条件的 if/while 分支、return/throw 后的不可达语句。
//   - 未使用声明删除：函数体内未被引用的 function/class/var 声明。
//   - 常量折叠：字面量二元/一元/条件/逻辑运算折叠为字面量。
//
// 变换作用于 AST，随后经 compile.CompileProgram 重新编译。字节码本地
// 变量按 slot 索引（无名字），故不做标识符压缩；函数名保留（Function.name
// 语义与错误堆栈可见性）。
package minify

import (
	"strconv"

	"github.com/aluka-lang/aluka/internal/bundler/astutil"
	"github.com/aluka-lang/aluka/internal/engine/ast"
)

// Program 对程序做最小化变换（原地修改）。
func Program(prog *ast.Program) {
	prog.Body = minimizeBlock(prog.Body)
}

// minimizeBlock 处理语句块：常量折叠 → 未用声明删除 → 死代码消除。
func minimizeBlock(body []ast.Statement) []ast.Statement {
	// 第一遍：折叠 + 删除未使用声明。
	out := make([]ast.Statement, 0, len(body))
	declared := collectDecls(body)
	refs := astutil.CollectRefs(blockNode(body))
	for _, stmt := range body {
		stmt = foldStmt(stmt)
		switch n := stmt.(type) {
		case *ast.FunctionDecl:
			if n.Name != nil && refs[n.Name.Name] == 0 {
				continue // 未引用的局部函数删除
			}
		case *ast.ClassDecl:
			if n.Name != nil && refs[n.Name.Name] == 0 {
				continue
			}
		case *ast.VarDecl:
			// 删除未引用且无副作用的 declarator。
			var decls []ast.VarDeclarator
			for _, d := range n.Decls {
				if d.Name != nil && refs[d.Name.Name] == 0 &&
					(d.Init == nil || !astutil.HasSideEffects(d.Init)) {
					_ = declared
					continue
				}
				decls = append(decls, d)
			}
			if len(decls) == 0 {
				continue
			}
			n.Decls = decls
		}
		out = append(out, stmt)
	}
	// 第二遍：return/throw 后的不可达语句删除。
	var final []ast.Statement
	terminated := false
	for _, stmt := range out {
		if terminated {
			continue
		}
		final = append(final, stmt)
		if terminates(stmt) {
			terminated = true
		}
	}
	// 第三遍：递归进入嵌套函数体/块。
	for i, stmt := range final {
		final[i] = descend(stmt)
	}
	return final
}

// descend 递归处理嵌套作用域（函数体、块）。
func descend(stmt ast.Statement) ast.Statement {
	switch n := stmt.(type) {
	case *ast.FunctionDecl:
		if n.Body != nil {
			n.Body.Body = minimizeBlock(n.Body.Body)
		}
	case *ast.BlockStmt:
		n.Body = minimizeBlock(n.Body)
	case *ast.IfStmt:
		n.Consequent = descend(n.Consequent)
		if n.Alternate != nil {
			n.Alternate = descend(n.Alternate)
		}
	case *ast.WhileStmt:
		n.Body = descend(n.Body)
	case *ast.DoWhileStmt:
		n.Body = descend(n.Body)
	case *ast.ForStmt:
		if n.Body != nil {
			n.Body = descend(n.Body)
		}
	case *ast.ForInStmt:
		if n.Body != nil {
			n.Body = descend(n.Body)
		}
	case *ast.ForOfStmt:
		if n.Body != nil {
			n.Body = descend(n.Body)
		}
	case *ast.TryStmt:
		if n.Block != nil {
			n.Block.Body = minimizeBlock(n.Block.Body)
		}
		if n.Finally != nil {
			n.Finally.Body = minimizeBlock(n.Finally.Body)
		}
	}
	return stmt
}

// foldStmt 折叠语句直接位置的表达式（常量条件分支同时做 DCE）。
func foldStmt(stmt ast.Statement) ast.Statement {
	switch n := stmt.(type) {
	case *ast.VarDecl:
		for i := range n.Decls {
			if n.Decls[i].Init != nil {
				n.Decls[i].Init = foldExpr(n.Decls[i].Init)
			}
		}
	case *ast.ExprStmt:
		n.Expr = foldExpr(n.Expr)
	case *ast.ReturnStmt:
		if n.Arg != nil {
			n.Arg = foldExpr(n.Arg)
		}
	case *ast.IfStmt:
		n.Test = foldExpr(n.Test)
		if v, ok := astutil.FoldConst(n.Test); ok {
			b, _ := truthy(v)
			if b {
				if n.Consequent != nil {
					return foldStmt(n.Consequent)
				}
				return &ast.EmptyStmt{Loc: n.Loc}
			}
			if n.Alternate != nil {
				return foldStmt(n.Alternate)
			}
			return &ast.EmptyStmt{Loc: n.Loc}
		}
	case *ast.WhileStmt:
		n.Test = foldExpr(n.Test)
		if v, ok := astutil.FoldConst(n.Test); ok {
			b, _ := truthy(v)
			if !b {
				return &ast.EmptyStmt{Loc: n.Loc}
			}
		}
	case *ast.DoWhileStmt:
		n.Test = foldExpr(n.Test)
	case *ast.ForStmt:
		if n.Test != nil {
			n.Test = foldExpr(n.Test)
		}
		if n.Update != nil {
			n.Update = foldExpr(n.Update)
		}
	}
	return stmt
}

// foldExpr 递归折叠表达式（子表达式折叠后尝试整体折叠）。
func foldExpr(e ast.Expression) ast.Expression {
	if e == nil {
		return nil
	}
	switch n := e.(type) {
	case *ast.BinaryExpr:
		l := foldExpr(n.Left)
		r := foldExpr(n.Right)
		n2 := &ast.BinaryExpr{Op: n.Op, Left: l, Right: r, Loc: n.Loc}
		if v, ok := astutil.FoldConst(n2); ok {
			return litOf(v, n.Loc)
		}
		return n2
	case *ast.UnaryExpr:
		a := foldExpr(n.Arg)
		n2 := &ast.UnaryExpr{Op: n.Op, Arg: a, Loc: n.Loc}
		if v, ok := astutil.FoldConst(n2); ok {
			return litOf(v, n.Loc)
		}
		return n2
	case *ast.LogicalExpr:
		l := foldExpr(n.Left)
		r := foldExpr(n.Right)
		n2 := &ast.LogicalExpr{Op: n.Op, Left: l, Right: r, Loc: n.Loc}
		if v, ok := astutil.FoldConst(n2); ok {
			return litOf(v, n.Loc)
		}
		return n2
	case *ast.ConditionalExpr:
		t := foldExpr(n.Test)
		c := foldExpr(n.Consequent)
		a := foldExpr(n.Alternate)
		n2 := &ast.ConditionalExpr{Test: t, Consequent: c, Alternate: a, Loc: n.Loc}
		if v, ok := astutil.FoldConst(n2); ok {
			return litOf(v, n.Loc)
		}
		return n2
	case *ast.TemplateLit:
		if len(n.Expressions) == 0 {
			return e
		}
		// 带插值模板：仅折叠插值表达式本身。
		for i, ex := range n.Expressions {
			n.Expressions[i] = foldExpr(ex)
		}
		return e
	case *ast.ArrayLit:
		for i, el := range n.Elements {
			n.Elements[i] = foldExpr(el)
		}
		return e
	case *ast.CallExpr:
		for i, a := range n.Arguments {
			n.Arguments[i] = foldExpr(a)
		}
		return e
	}
	return e
}

// litOf 把折叠值转为字面量节点。
func litOf(v interface{}, loc ast.Pos) ast.Expression {
	switch t := v.(type) {
	case float64:
		return &ast.NumberLit{Value: t, Raw: strconv.FormatFloat(t, 'g', -1, 64), Loc: loc}
	case string:
		return &ast.StringLit{Value: t, Loc: loc}
	case bool:
		return &ast.BoolLit{Value: t, Loc: loc}
	case nil:
		return &ast.NullLit{Loc: loc}
	case undefined:
		return &ast.UndefinedLit{Loc: loc}
	}
	return &ast.UndefinedLit{Loc: loc}
}

// undefined 标记 undefined 折叠值（与 astutil 内部约定一致）。
type undefined struct{}

// truthy 折叠值真值判定。
func truthy(v interface{}) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case float64:
		return t != 0 && t == t, true
	case string:
		return t != "", true
	case nil, undefined:
		return false, true
	}
	return false, false
}

// collectDecls 收集块内声明的名字（供引用分析）。
func collectDecls(body []ast.Statement) map[string]bool {
	decls := make(map[string]bool)
	for _, stmt := range body {
		switch n := stmt.(type) {
		case *ast.VarDecl:
			for _, d := range n.Decls {
				if d.Name != nil {
					decls[d.Name.Name] = true
				}
			}
		case *ast.FunctionDecl:
			if n.Name != nil {
				decls[n.Name.Name] = true
			}
		case *ast.ClassDecl:
			if n.Name != nil {
				decls[n.Name.Name] = true
			}
		}
	}
	return decls
}

// blockNode 把语句列表包装为节点（供 CollectRefs 遍历）。
func blockNode(body []ast.Statement) interface{} {
	return &ast.BlockStmt{Body: body}
}

// terminates 判断语句是否终止控制流（return/throw/break/continue）。
func terminates(stmt ast.Statement) bool {
	switch n := stmt.(type) {
	case *ast.ReturnStmt, *ast.ThrowStmt:
		return true
	case *ast.BlockStmt:
		for _, s := range n.Body {
			if terminates(s) {
				return true
			}
		}
		return false
	case *ast.IfStmt:
		if n.Alternate == nil {
			return false
		}
		return terminates(n.Consequent) && terminates(n.Alternate)
	case *ast.ExprStmt:
		if call, ok := n.Expr.(*ast.CallExpr); ok {
			// 保守：以 throw 语义的内置调用不识别。
			_ = call
		}
		return false
	}
	return false
}
