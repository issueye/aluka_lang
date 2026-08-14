// 统一 AST 遍历基础设施（参考 typescript-go 的 ForEachChild/ForEachReference
// 设计）：把"每个节点类型的子节点有哪些"集中到 ForEachChild 一处维护，
// 消费方（编译器/解释器/tree-shake/minify/ESM 改写）不再各自手写大 type
// switch 或反射遍历。
package ast

import "reflect"

// ForEachChild 枚举 node 的全部子节点，逐个调用 fn。
// 当 fn 返回 true 时立即终止并返回 true（可用于短路搜索）；否则返回 false。
//
// 覆盖全部节点类型：语句/表达式/模式/类体/解构元素等。叶子节点
// （Identifier、各种字面量、ThisExpr 等）没有子节点，返回 false。
// 语义：纯机械枚举，不区分"声明名"与"引用"——需要按位置语义遍历的
// 消费方请用 ForEachRef（引用位置）或自行在回调里按节点类型处理。
func ForEachChild(node Node, fn func(Node) bool) bool {
	if node == nil {
		return false
	}
	switch n := node.(type) {
	// ---- 语句 ----
	case *Program:
		for _, s := range n.Body {
			if visitChild(s, fn) {
				return true
			}
		}
	case *VarDecl:
		for i := range n.Decls {
			if forEachDeclarator(&n.Decls[i], fn) {
				return true
			}
		}
	case *FunctionDecl:
		return forEachFunctionLike(n.Name, n.Params, n.ParamPatterns, n.Defaults, n.RestParam, n.Body, fn)
	case *BlockStmt:
		for _, s := range n.Body {
			if visitChild(s, fn) {
				return true
			}
		}
	case *ExprStmt:
		return visitChild(n.Expr, fn)
	case *EmptyStmt:
		// 无子节点
	case *IfStmt:
		if visitChild(n.Test, fn) || visitChild(n.Consequent, fn) {
			return true
		}
		return visitChild(n.Alternate, fn)
	case *WhileStmt:
		if visitChild(n.Test, fn) {
			return true
		}
		return visitChild(n.Body, fn)
	case *DoWhileStmt:
		if visitChild(n.Body, fn) {
			return true
		}
		return visitChild(n.Test, fn)
	case *ForStmt:
		if visitChild(n.Init, fn) || visitChild(n.Test, fn) || visitChild(n.Update, fn) {
			return true
		}
		return visitChild(n.Body, fn)
	case *ForInStmt:
		if visitChild(n.Left, fn) || visitChild(n.Right, fn) {
			return true
		}
		return visitChild(n.Body, fn)
	case *ForOfStmt:
		if visitChild(n.Left, fn) || visitChild(n.Right, fn) {
			return true
		}
		return visitChild(n.Body, fn)
	case *ReturnStmt:
		return visitChild(n.Arg, fn)
	case *BreakStmt, *ContinueStmt:
		// Label 是字符串，无子节点
	case *ThrowStmt:
		return visitChild(n.Arg, fn)
	case *TryStmt:
		if visitChild(n.Block, fn) {
			return true
		}
		if n.Handler != nil {
			if visitChild(n.Handler.Param, fn) || visitChild(n.Handler.Body, fn) {
				return true
			}
		}
		return visitChild(n.Finally, fn)
	case *SwitchStmt:
		if visitChild(n.Disc, fn) {
			return true
		}
		for i := range n.Cases {
			c := &n.Cases[i]
			if visitChild(c.Test, fn) {
				return true
			}
			for _, s := range c.Consequent {
				if visitChild(s, fn) {
					return true
				}
			}
		}
	case *LabeledStmt:
		return visitChild(n.Body, fn)
	case *ClassDecl:
		return forEachClassLike(n.Name, n.SuperClass, n.Body, fn)
	case *ImportDecl:
		// Specifiers 是字符串对，无子节点
	case *ExportDecl:
		return visitChild(n.Declaration, fn)
	case *ExportDefaultDecl:
		return visitChild(n.Expression, fn)

	// ---- 表达式 ----
	case *Identifier, *NumberLit, *BigIntLit, *StringLit, *BoolLit,
		*NullLit, *UndefinedLit, *RegexLit, *ThisExpr, *SuperExpr, *NewTargetExpr:
		// 叶子节点
	case *TemplateLit:
		for _, e := range n.Expressions {
			if visitChild(e, fn) {
				return true
			}
		}
	case *TaggedTemplateExpr:
		if visitChild(n.Tag, fn) {
			return true
		}
		return visitChild(n.Template, fn)
	case *ArrayLit:
		for _, e := range n.Elements {
			if visitChild(e, fn) {
				return true
			}
		}
	case *ObjectLit:
		for i := range n.Properties {
			p := &n.Properties[i]
			if visitChild(p.Key, fn) || visitChild(p.Value, fn) || visitChild(p.Default, fn) {
				return true
			}
		}
	case *MemberExpr:
		if visitChild(n.Object, fn) {
			return true
		}
		return visitChild(n.Property, fn)
	case *CallExpr:
		if visitChild(n.Callee, fn) {
			return true
		}
		for _, a := range n.Arguments {
			if visitChild(a, fn) {
				return true
			}
		}
	case *NewExpr:
		if visitChild(n.Callee, fn) {
			return true
		}
		for _, a := range n.Arguments {
			if visitChild(a, fn) {
				return true
			}
		}
	case *UnaryExpr:
		return visitChild(n.Arg, fn)
	case *UpdateExpr:
		return visitChild(n.Arg, fn)
	case *BinaryExpr:
		if visitChild(n.Left, fn) {
			return true
		}
		return visitChild(n.Right, fn)
	case *LogicalExpr:
		if visitChild(n.Left, fn) {
			return true
		}
		return visitChild(n.Right, fn)
	case *AssignExpr:
		// Left 是表达式或解构模式（Node 接口），均作为子节点
		if visitChild(n.Left, fn) {
			return true
		}
		return visitChild(n.Right, fn)
	case *ConditionalExpr:
		if visitChild(n.Test, fn) || visitChild(n.Consequent, fn) {
			return true
		}
		return visitChild(n.Alternate, fn)
	case *SequenceExpr:
		for _, e := range n.Expressions {
			if visitChild(e, fn) {
				return true
			}
		}
	case *SpreadElement:
		return visitChild(n.Arg, fn)
	case *FunctionExpr:
		return forEachFunctionLike(n.Name, n.Params, n.ParamPatterns, n.Defaults, n.RestParam, n.Body, fn)
	case *ArrowFunc:
		return forEachFunctionLike(nil, n.Params, n.ParamPatterns, n.Defaults, n.RestParam, n.Body, fn)
	case *YieldExpr:
		return visitChild(n.Argument, fn)
	case *AwaitExpr:
		return visitChild(n.Argument, fn)
	case *ClassExpr:
		return forEachClassLike(n.Name, n.SuperClass, n.Body, fn)

	// ---- 模式 ----
	case *ArrayPattern:
		for i := range n.Elements {
			el := &n.Elements[i]
			if visitChild(el.Target, fn) {
				return true
			}
			if visitChild(el.Default, fn) {
				return true
			}
		}
	case *ObjectPattern:
		for i := range n.Properties {
			p := &n.Properties[i]
			if visitChild(p.Key, fn) || visitChild(p.Value, fn) || visitChild(p.Default, fn) {
				return true
			}
		}
	}
	return false
}

// visitChild 对非空子节点调用 fn，返回是否提前终止。
//
// 注意 Go 接口陷阱：nil 具体指针（如字段声明的 MethodDefinition.Value）
// 赋给 Node 接口后成为"带类型的 nil 接口"，`c != nil` 判断会失效；
// 因此用 reflect 显式剔除 typed-nil，避免把 nil 节点传给 fn 导致解引用 panic。
func visitChild(c Node, fn func(Node) bool) bool {
	if c == nil {
		return false
	}
	if v := reflect.ValueOf(c); v.Kind() == reflect.Pointer && v.IsNil() {
		return false
	}
	return fn(c)
}

// forEachDeclarator 枚举 VarDeclarator 的子节点（名/模式/初始化表达式）。
func forEachDeclarator(d *VarDeclarator, fn func(Node) bool) bool {
	if visitChild(d.Name, fn) {
		return true
	}
	if visitChild(d.Pattern, fn) {
		return true
	}
	return visitChild(d.Init, fn)
}

// forEachFunctionLike 枚举函数/箭头函数共享形状的子节点。
// Body 为 Node（FunctionDecl/FunctionExpr 是 *BlockStmt，ArrowFunc 是表达式或块）。
func forEachFunctionLike(name *Identifier, params []*Identifier, patterns []Pattern, defaults []Expression, rest *Identifier, body Node, fn func(Node) bool) bool {
	if visitChild(name, fn) {
		return true
	}
	for _, p := range params {
		if visitChild(p, fn) {
			return true
		}
	}
	for _, p := range patterns {
		if visitChild(p, fn) {
			return true
		}
	}
	for _, d := range defaults {
		if visitChild(d, fn) {
			return true
		}
	}
	if visitChild(rest, fn) {
		return true
	}
	return visitChild(body, fn)
}

// forEachClassLike 枚举类声明/表达式的子节点（名/超类/类体）。
func forEachClassLike(name *Identifier, super Expression, body *ClassBody, fn func(Node) bool) bool {
	if visitChild(name, fn) || visitChild(super, fn) {
		return true
	}
	if body == nil {
		return false
	}
	for i := range body.Methods {
		m := &body.Methods[i]
		if visitChild(m.Key, fn) || visitChild(m.Value, fn) || visitChild(m.Init, fn) {
			return true
		}
	}
	return false
}

// Walk 对 node 做先序深度优先遍历：先调用 visit(node)，visit 返回 false
// 时跳过该节点的子树（不深入），继续遍历兄弟；返回 true 时继续深入。
// ForEachChild 的提前终止不会向外传播（Walk 遍历完整棵树）。
func Walk(node Node, visit func(Node) bool) {
	if node == nil {
		return
	}
	if !visit(node) {
		return
	}
	ForEachChild(node, func(c Node) bool {
		Walk(c, visit)
		return false
	})
}

// ForEachRef 遍历 node 中的"引用位置"标识符：对每个是变量引用的
// Identifier 调用 fn（对象字面量/成员访问的非计算属性键、声明名、模式
// 绑定名等位置被跳过；计算属性键 `obj[a]` 与真实引用位置被命中）。
//
// 语义对照：
//   - 声明名（var/函数/类名、参数、catch 参数、import Local、模式绑定名、
//     类方法名）→ 不命中
//   - 非计算属性键（`obj.method`、`{key: v}`、模式 `{key: x}`）→ 不命中
//   - 计算属性键（`obj[a]`、`{[k]: v}`）→ 命中
//   - 对象字面量简写 `{a}`（Value 与 Key 同节点）→ 命中
//   - 解构赋值 `({a} = x)` 的目标 → 命中（赋值语境是对既有变量的引用）
//   - 函数默认值 `function f(a = g()) {}` → 命中 g
func ForEachRef(node Node, fn func(*Identifier)) {
	switch n := node.(type) {
	case *Identifier:
		fn(n)
	case *Program:
		for _, s := range n.Body {
			ForEachRef(s, fn)
		}
	case *VarDecl:
		for i := range n.Decls {
			forEachRefDeclarator(&n.Decls[i], fn)
		}
	case *FunctionDecl:
		forEachRefFunction(n.Defaults, n.ParamPatterns, n.Body, fn)
	case *FunctionExpr:
		forEachRefFunction(n.Defaults, n.ParamPatterns, n.Body, fn)
	case *ArrowFunc:
		forEachRefFunction(n.Defaults, n.ParamPatterns, n.Body, fn)
	case *ClassDecl:
		forEachRefClass(n.SuperClass, n.Body, fn)
	case *ClassExpr:
		forEachRefClass(n.SuperClass, n.Body, fn)
	case *ExprStmt:
		if n.Expr != nil {
			ForEachRef(n.Expr, fn)
		}
	case *BlockStmt:
		for _, s := range n.Body {
			ForEachRef(s, fn)
		}
	case *IfStmt:
		if n.Test != nil {
			ForEachRef(n.Test, fn)
		}
		if n.Consequent != nil {
			ForEachRef(n.Consequent, fn)
		}
		if n.Alternate != nil {
			ForEachRef(n.Alternate, fn)
		}
	case *WhileStmt:
		if n.Test != nil {
			ForEachRef(n.Test, fn)
		}
		if n.Body != nil {
			ForEachRef(n.Body, fn)
		}
	case *DoWhileStmt:
		if n.Body != nil {
			ForEachRef(n.Body, fn)
		}
		if n.Test != nil {
			ForEachRef(n.Test, fn)
		}
	case *ForStmt:
		if n.Init != nil {
			ForEachRef(n.Init, fn)
		}
		if n.Test != nil {
			ForEachRef(n.Test, fn)
		}
		if n.Update != nil {
			ForEachRef(n.Update, fn)
		}
		if n.Body != nil {
			ForEachRef(n.Body, fn)
		}
	case *ForInStmt:
		forEachRefForLeft(n.Left, fn)
		if n.Right != nil {
			ForEachRef(n.Right, fn)
		}
		if n.Body != nil {
			ForEachRef(n.Body, fn)
		}
	case *ForOfStmt:
		forEachRefForLeft(n.Left, fn)
		if n.Right != nil {
			ForEachRef(n.Right, fn)
		}
		if n.Body != nil {
			ForEachRef(n.Body, fn)
		}
	case *ReturnStmt:
		if n.Arg != nil {
			ForEachRef(n.Arg, fn)
		}
	case *ThrowStmt:
		if n.Arg != nil {
			ForEachRef(n.Arg, fn)
		}
	case *TryStmt:
		if n.Block != nil {
			ForEachRef(n.Block, fn)
		}
		if n.Handler != nil {
			// Param 是绑定名，不命中
			if n.Handler.Body != nil {
				ForEachRef(n.Handler.Body, fn)
			}
		}
		if n.Finally != nil {
			ForEachRef(n.Finally, fn)
		}
	case *SwitchStmt:
		if n.Disc != nil {
			ForEachRef(n.Disc, fn)
		}
		for i := range n.Cases {
			c := &n.Cases[i]
			if c.Test != nil {
				ForEachRef(c.Test, fn)
			}
			for _, s := range c.Consequent {
				ForEachRef(s, fn)
			}
		}
	case *LabeledStmt:
		if n.Body != nil {
			ForEachRef(n.Body, fn)
		}
	case *ExportDecl:
		if n.Declaration != nil {
			ForEachRef(n.Declaration, fn)
		}
	case *ExportDefaultDecl:
		if n.Expression != nil {
			ForEachRef(n.Expression, fn)
		}
	case *TemplateLit:
		for _, e := range n.Expressions {
			if e != nil {
				ForEachRef(e, fn)
			}
		}
	case *TaggedTemplateExpr:
		if n.Tag != nil {
			ForEachRef(n.Tag, fn)
		}
		if n.Template != nil {
			ForEachRef(n.Template, fn)
		}
	case *ArrayLit:
		for _, e := range n.Elements {
			if e != nil {
				ForEachRef(e, fn)
			}
		}
	case *ObjectLit:
		for i := range n.Properties {
			p := &n.Properties[i]
			if p.Computed && p.Key != nil {
				ForEachRef(p.Key, fn)
			}
			if p.Value != nil {
				ForEachRef(p.Value, fn)
			}
			if p.Default != nil {
				ForEachRef(p.Default, fn)
			}
		}
	case *MemberExpr:
		if n.Object != nil {
			ForEachRef(n.Object, fn)
		}
		if n.Computed && n.Property != nil {
			ForEachRef(n.Property, fn)
		}
	case *CallExpr:
		if n.Callee != nil {
			ForEachRef(n.Callee, fn)
		}
		for _, a := range n.Arguments {
			if a != nil {
				ForEachRef(a, fn)
			}
		}
	case *NewExpr:
		if n.Callee != nil {
			ForEachRef(n.Callee, fn)
		}
		for _, a := range n.Arguments {
			if a != nil {
				ForEachRef(a, fn)
			}
		}
	case *UnaryExpr:
		if n.Arg != nil {
			ForEachRef(n.Arg, fn)
		}
	case *UpdateExpr:
		if n.Arg != nil {
			ForEachRef(n.Arg, fn)
		}
	case *BinaryExpr:
		if n.Left != nil {
			ForEachRef(n.Left, fn)
		}
		if n.Right != nil {
			ForEachRef(n.Right, fn)
		}
	case *LogicalExpr:
		if n.Left != nil {
			ForEachRef(n.Left, fn)
		}
		if n.Right != nil {
			ForEachRef(n.Right, fn)
		}
	case *AssignExpr:
		if n.Left != nil {
			if pat, ok := n.Left.(Pattern); ok {
				forEachRefPatternAssign(pat, fn)
			} else {
				ForEachRef(n.Left, fn)
			}
		}
		if n.Right != nil {
			ForEachRef(n.Right, fn)
		}
	case *ConditionalExpr:
		if n.Test != nil {
			ForEachRef(n.Test, fn)
		}
		if n.Consequent != nil {
			ForEachRef(n.Consequent, fn)
		}
		if n.Alternate != nil {
			ForEachRef(n.Alternate, fn)
		}
	case *SequenceExpr:
		for _, e := range n.Expressions {
			if e != nil {
				ForEachRef(e, fn)
			}
		}
	case *SpreadElement:
		if n.Arg != nil {
			ForEachRef(n.Arg, fn)
		}
	case *YieldExpr:
		if n.Argument != nil {
			ForEachRef(n.Argument, fn)
		}
	case *AwaitExpr:
		if n.Argument != nil {
			ForEachRef(n.Argument, fn)
		}
	case *ArrayPattern:
		for i := range n.Elements {
			el := &n.Elements[i]
			if el.Target != nil {
				forEachRefPatternDecl(el.Target, fn)
			}
			if el.Default != nil {
				ForEachRef(el.Default, fn)
			}
		}
	case *ObjectPattern:
		for i := range n.Properties {
			p := &n.Properties[i]
			if p.Computed && p.Key != nil {
				ForEachRef(p.Key, fn)
			}
			if p.Default != nil {
				ForEachRef(p.Default, fn)
			}
			if p.Value != nil {
				forEachRefPatternDecl(p.Value, fn)
			}
		}
	}
}

// forEachRefDeclarator 处理声明语境：绑定名跳过，模式默认值与 Init 是引用。
func forEachRefDeclarator(d *VarDeclarator, fn func(*Identifier)) {
	if d.Pattern != nil {
		forEachRefPatternDecl(d.Pattern, fn)
	}
	if d.Init != nil {
		ForEachRef(d.Init, fn)
	}
}

// forEachRefFunction 处理函数体引用：默认值、参数解构默认值与函数体命中；
// 函数名、参数名、rest 参数是绑定名，不命中。
func forEachRefFunction(defaults []Expression, patterns []Pattern, body Node, fn func(*Identifier)) {
	for _, d := range defaults {
		if d != nil {
			ForEachRef(d, fn)
		}
	}
	for _, p := range patterns {
		if p != nil {
			forEachRefPatternDecl(p, fn)
		}
	}
	if body != nil {
		ForEachRef(body, fn)
	}
}

// forEachRefClass 处理类引用：超类、方法体/字段初始化命中；
// 类名、方法名（非计算 Key）是绑定名，不命中。
func forEachRefClass(super Expression, body *ClassBody, fn func(*Identifier)) {
	if super != nil {
		ForEachRef(super, fn)
	}
	if body == nil {
		return
	}
	for i := range body.Methods {
		m := &body.Methods[i]
		if m.Computed && m.Key != nil {
			ForEachRef(m.Key, fn)
		}
		if m.Value != nil {
			ForEachRef(m.Value, fn)
		}
		if m.Init != nil {
			ForEachRef(m.Init, fn)
		}
	}
}

// forEachRefPatternDecl 声明语境解构模式：绑定名跳过，仅命中计算属性键与默认值。
func forEachRefPatternDecl(p Pattern, fn func(*Identifier)) {
	switch pat := p.(type) {
	case *Identifier:
		// 绑定名，跳过
	case *ArrayPattern:
		for i := range pat.Elements {
			el := &pat.Elements[i]
			if el.Target != nil {
				forEachRefPatternDecl(el.Target, fn)
			}
			if el.Default != nil {
				ForEachRef(el.Default, fn)
			}
		}
	case *ObjectPattern:
		for i := range pat.Properties {
			prop := &pat.Properties[i]
			if prop.Computed && prop.Key != nil {
				ForEachRef(prop.Key, fn)
			}
			if prop.Default != nil {
				ForEachRef(prop.Default, fn)
			}
			if prop.Value != nil {
				forEachRefPatternDecl(prop.Value, fn)
			}
		}
	}
}

// forEachRefPatternAssign 赋值语境解构模式：目标是对既有变量的引用，命中；
// 非计算属性键仍是属性名，跳过。
func forEachRefPatternAssign(p Pattern, fn func(*Identifier)) {
	switch pat := p.(type) {
	case *Identifier:
		fn(pat)
	case *ArrayPattern:
		for i := range pat.Elements {
			el := &pat.Elements[i]
			if el.Target != nil {
				forEachRefPatternAssign(el.Target, fn)
			}
			if el.Default != nil {
				ForEachRef(el.Default, fn)
			}
		}
	case *ObjectPattern:
		for i := range pat.Properties {
			prop := &pat.Properties[i]
			if prop.Computed && prop.Key != nil {
				ForEachRef(prop.Key, fn)
			}
			if prop.Default != nil {
				ForEachRef(prop.Default, fn)
			}
			if prop.Value != nil {
				forEachRefPatternAssign(prop.Value, fn)
			}
		}
	}
}

// forEachRefForLeft 处理 for-in/of 左值：声明语境（VarDecl）绑定名跳过；
// 表达式/模式语境（对既有变量赋值）按引用处理。
func forEachRefForLeft(left Node, fn func(*Identifier)) {
	if left == nil {
		return
	}
	if vd, ok := left.(*VarDecl); ok {
		for i := range vd.Decls {
			forEachRefDeclarator(&vd.Decls[i], fn)
		}
		return
	}
	if pat, ok := left.(Pattern); ok {
		forEachRefPatternAssign(pat, fn)
		return
	}
	ForEachRef(left, fn)
}
