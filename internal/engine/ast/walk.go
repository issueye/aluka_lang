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

// ForEachRef 遍历 node 中的"引用位置"标识符：对每个是变量引用的 Identifier
// 调用 fn（对象字面量/成员访问的非计算属性键、声明名、模式绑定名等位置被
// 跳过；计算属性键 `obj[a]` 与真实引用位置被命中）。只读版本：内部由
// RewriteRefs 包装，单一权威的引用位置遍历实现。
func ForEachRef(node Node, fn func(*Identifier)) {
	RewriteRefs(node, func(id *Identifier) Node { fn(id); return nil })
}

// RewriteRefs 遍历 node 中的"引用位置"标识符并对每个位置调用 fn：fn 返回
// 非 nil 节点时用返回值原地替换该引用位置（Statement 槽位上的裸标识符
// 会被包装为 ExprStmt 以承接表达式替换节点）。返回是否发生替换。
//
// 语义与 ForEachRef 完全一致（见其上注释），额外支持原地改写——供
// TransformESMToCJS 的 live-binding 改写使用，取代原先的反射 +
// 字段名白名单启发式（修复计算属性 `obj[imported]` 漏改等缺陷）。
func RewriteRefs(node Node, fn func(*Identifier) Node) bool {
	return rewriteRefsIn(node, fn)
}

func rewriteRefsIn(node Node, fn func(*Identifier) Node) bool {
	switch n := node.(type) {
	case *Identifier:
		// 顶层直接入口：无父槽位，仅触发回调（read 场景）。
		fn(n)
		return false
	case *Program:
		return rewriteStmtSlice(&n.Body, fn)
	case *VarDecl:
		changed := false
		for i := range n.Decls {
			if rewriteVarDeclarator(&n.Decls[i], fn) {
				changed = true
			}
		}
		return changed
	case *FunctionDecl:
		return rewriteFunctionLike(n.Defaults, n.ParamPatterns, n.Body, fn)
	case *FunctionExpr:
		return rewriteFunctionLike(n.Defaults, n.ParamPatterns, n.Body, fn)
	case *ArrowFunc:
		changed := false
		if rewriteFunctionDefaults(n.Defaults, fn) {
			changed = true
		}
		for i := range n.ParamPatterns {
			target := Node(n.ParamPatterns[i])
			if rewritePatternDecl(&target, fn) {
				n.ParamPatterns[i] = target.(Pattern)
				changed = true
			}
		}
		if rewriteNodeSlot(&n.Body, fn) {
			changed = true
		}
		return changed
	case *ClassDecl:
		return rewriteClassLike(&n.SuperClass, n.Body, fn)
	case *ClassExpr:
		return rewriteClassLike(&n.SuperClass, n.Body, fn)
	case *BlockStmt:
		return rewriteStmtSlice(&n.Body, fn)
	case *ExprStmt:
		return rewriteExprSlot(&n.Expr, fn)
	case *EmptyStmt:
		return false
	case *IfStmt:
		changed := false
		if rewriteExprSlot(&n.Test, fn) {
			changed = true
		}
		if rewriteStmtSlot(&n.Consequent, fn) {
			changed = true
		}
		if rewriteStmtSlot(&n.Alternate, fn) {
			changed = true
		}
		return changed
	case *WhileStmt:
		changed := false
		if rewriteExprSlot(&n.Test, fn) {
			changed = true
		}
		if rewriteStmtSlot(&n.Body, fn) {
			changed = true
		}
		return changed
	case *DoWhileStmt:
		changed := false
		if rewriteStmtSlot(&n.Body, fn) {
			changed = true
		}
		if rewriteExprSlot(&n.Test, fn) {
			changed = true
		}
		return changed
	case *ForStmt:
		changed := false
		if rewriteNodeSlot(&n.Init, fn) {
			changed = true
		}
		if rewriteExprSlot(&n.Test, fn) {
			changed = true
		}
		if rewriteExprSlot(&n.Update, fn) {
			changed = true
		}
		if rewriteStmtSlot(&n.Body, fn) {
			changed = true
		}
		return changed
	case *ForInStmt:
		changed := false
		if rewriteForLeft(&n.Left, fn) {
			changed = true
		}
		if rewriteExprSlot(&n.Right, fn) {
			changed = true
		}
		if rewriteStmtSlot(&n.Body, fn) {
			changed = true
		}
		return changed
	case *ForOfStmt:
		changed := false
		if rewriteForLeft(&n.Left, fn) {
			changed = true
		}
		if rewriteExprSlot(&n.Right, fn) {
			changed = true
		}
		if rewriteStmtSlot(&n.Body, fn) {
			changed = true
		}
		return changed
	case *ReturnStmt:
		return rewriteExprSlot(&n.Arg, fn)
	case *BreakStmt, *ContinueStmt:
		return false
	case *ThrowStmt:
		return rewriteExprSlot(&n.Arg, fn)
	case *TryStmt:
		changed := false
		if n.Block != nil && rewriteRefsIn(n.Block, fn) {
			changed = true
		}
		if n.Handler != nil && n.Handler.Body != nil && rewriteRefsIn(n.Handler.Body, fn) {
			changed = true
		}
		if n.Finally != nil && rewriteRefsIn(n.Finally, fn) {
			changed = true
		}
		return changed
	case *SwitchStmt:
		changed := false
		if rewriteExprSlot(&n.Disc, fn) {
			changed = true
		}
		for i := range n.Cases {
			c := &n.Cases[i]
			if rewriteExprSlot(&c.Test, fn) {
				changed = true
			}
			if rewriteStmtSlice(&c.Consequent, fn) {
				changed = true
			}
		}
		return changed
	case *LabeledStmt:
		return rewriteStmtSlot(&n.Body, fn)
	case *ImportDecl:
		return false
	case *ExportDecl:
		return rewriteStmtSlot(&n.Declaration, fn)
	case *ExportDefaultDecl:
		return rewriteExprSlot(&n.Expression, fn)
	case *TemplateLit:
		return rewriteExprSlice(&n.Expressions, fn)
	case *TaggedTemplateExpr:
		changed := false
		if rewriteExprSlot(&n.Tag, fn) {
			changed = true
		}
		if n.Template != nil && rewriteRefsIn(n.Template, fn) {
			changed = true
		}
		return changed
	case *ArrayLit:
		return rewriteExprSlice(&n.Elements, fn)
	case *ObjectLit:
		changed := false
		for i := range n.Properties {
			p := &n.Properties[i]
			if p.Computed && rewriteExprSlot(&p.Key, fn) {
				changed = true
			}
			if rewriteExprSlot(&p.Value, fn) {
				changed = true
			}
			if rewriteExprSlot(&p.Default, fn) {
				changed = true
			}
		}
		return changed
	case *MemberExpr:
		changed := false
		if rewriteExprSlot(&n.Object, fn) {
			changed = true
		}
		if n.Computed && rewriteExprSlot(&n.Property, fn) {
			changed = true
		}
		return changed
	case *CallExpr:
		changed := false
		if rewriteExprSlot(&n.Callee, fn) {
			changed = true
		}
		if rewriteExprSlice(&n.Arguments, fn) {
			changed = true
		}
		return changed
	case *NewExpr:
		changed := false
		if rewriteExprSlot(&n.Callee, fn) {
			changed = true
		}
		if rewriteExprSlice(&n.Arguments, fn) {
			changed = true
		}
		return changed
	case *UnaryExpr:
		return rewriteExprSlot(&n.Arg, fn)
	case *UpdateExpr:
		return rewriteExprSlot(&n.Arg, fn)
	case *BinaryExpr:
		changed := false
		if rewriteExprSlot(&n.Left, fn) {
			changed = true
		}
		if rewriteExprSlot(&n.Right, fn) {
			changed = true
		}
		return changed
	case *LogicalExpr:
		changed := false
		if rewriteExprSlot(&n.Left, fn) {
			changed = true
		}
		if rewriteExprSlot(&n.Right, fn) {
			changed = true
		}
		return changed
	case *AssignExpr:
		changed := false
		if n.Left != nil {
			if _, isPat := n.Left.(Pattern); isPat {
				if rewritePatternAssign(&n.Left, fn) {
					changed = true
				}
			} else if rewriteNodeSlot(&n.Left, fn) {
				changed = true
			}
		}
		if rewriteExprSlot(&n.Right, fn) {
			changed = true
		}
		return changed
	case *ConditionalExpr:
		changed := false
		if rewriteExprSlot(&n.Test, fn) {
			changed = true
		}
		if rewriteExprSlot(&n.Consequent, fn) {
			changed = true
		}
		if rewriteExprSlot(&n.Alternate, fn) {
			changed = true
		}
		return changed
	case *SequenceExpr:
		return rewriteExprSlice(&n.Expressions, fn)
	case *SpreadElement:
		return rewriteExprSlot(&n.Arg, fn)
	case *YieldExpr:
		return rewriteExprSlot(&n.Argument, fn)
	case *AwaitExpr:
		return rewriteExprSlot(&n.Argument, fn)
	case *ArrayPattern:
		changed := false
		for i := range n.Elements {
			el := &n.Elements[i]
			if el.Target != nil {
				target := Node(el.Target)
				if rewritePatternDecl(&target, fn) {
					el.Target = target.(Pattern)
					changed = true
				}
			}
			if rewriteExprSlot(&el.Default, fn) {
				changed = true
			}
		}
		return changed
	case *ObjectPattern:
		changed := false
		for i := range n.Properties {
			p := &n.Properties[i]
			if p.Computed && rewriteExprSlot(&p.Key, fn) {
				changed = true
			}
			if rewriteExprSlot(&p.Default, fn) {
				changed = true
			}
			if p.Value != nil {
				target := Node(p.Value)
				if rewritePatternDecl(&target, fn) {
					p.Value = target.(Pattern)
					changed = true
				}
			}
		}
		return changed
	}
	return false
}

// rewriteExprSlot 处理 Expression 槽位：命中匹配引用则替换，否则递归深入。
func rewriteExprSlot(slot *Expression, fn func(*Identifier) Node) bool {
	if *slot == nil {
		return false
	}
	if id, ok := (*slot).(*Identifier); ok {
		if repl := fn(id); repl != nil {
			*slot = repl.(Expression)
			return true
		}
		return false
	}
	return rewriteRefsIn(*slot, fn)
}

// rewriteNodeSlot 处理 Node 槽位（ForStmt.Init / for 左值 / ArrowFunc.Body 等）。
func rewriteNodeSlot(slot *Node, fn func(*Identifier) Node) bool {
	if *slot == nil {
		return false
	}
	if id, ok := (*slot).(*Identifier); ok {
		if repl := fn(id); repl != nil {
			*slot = repl
			return true
		}
		return false
	}
	return rewriteRefsIn(*slot, fn)
}

// rewriteStmtSlot 处理 Statement 槽位：裸标识符被替换时包装为 ExprStmt
//（替换节点是表达式，不能直接赋给 Statement 接口）。
func rewriteStmtSlot(slot *Statement, fn func(*Identifier) Node) bool {
	if *slot == nil {
		return false
	}
	if id, ok := (*slot).(*Identifier); ok {
		if repl := fn(id); repl != nil {
			if stmt, ok := repl.(Statement); ok {
				*slot = stmt
			} else {
				*slot = &ExprStmt{Expr: repl.(Expression), Loc: id.Loc}
			}
			return true
		}
		return false
	}
	return rewriteRefsIn(*slot, fn)
}

func rewriteExprSlice(slots *[]Expression, fn func(*Identifier) Node) bool {
	changed := false
	for i := range *slots {
		if rewriteExprSlot(&(*slots)[i], fn) {
			changed = true
		}
	}
	return changed
}

func rewriteStmtSlice(slots *[]Statement, fn func(*Identifier) Node) bool {
	changed := false
	for i := range *slots {
		if rewriteStmtSlot(&(*slots)[i], fn) {
			changed = true
		}
	}
	return changed
}

// rewriteVarDeclarator 处理声明语境 declarator：绑定名跳过，模式默认值与
// Init 命中。
func rewriteVarDeclarator(d *VarDeclarator, fn func(*Identifier) Node) bool {
	changed := false
	if d.Pattern != nil {
		target := Node(d.Pattern)
		if rewritePatternDecl(&target, fn) {
			d.Pattern = target.(Pattern)
			changed = true
		}
	}
	if rewriteExprSlot(&d.Init, fn) {
		changed = true
	}
	return changed
}

// rewriteFunctionDefaults 处理函数默认值参数表达式。
func rewriteFunctionDefaults(defaults []Expression, fn func(*Identifier) Node) bool {
	changed := false
	for i := range defaults {
		if rewriteExprSlot(&defaults[i], fn) {
			changed = true
		}
	}
	return changed
}

// rewriteFunctionLike 处理函数声明/表达式的引用位置：默认值与函数体命中；
// 函数名/参数/rest 参数为绑定名（跳过）。
func rewriteFunctionLike(defaults []Expression, patterns []Pattern, body Node, fn func(*Identifier) Node) bool {
	changed := false
	if rewriteFunctionDefaults(defaults, fn) {
		changed = true
	}
	for i := range patterns {
		target := Node(patterns[i])
		if rewritePatternDecl(&target, fn) {
			patterns[i] = target.(Pattern)
			changed = true
		}
	}
	if body != nil && rewriteRefsIn(body, fn) {
		changed = true
	}
	return changed
}

// rewriteClassLike 处理类的引用位置：超类与类体（方法体/字段初始化/计算键）
// 命中；类名/方法名（非计算键）为绑定名（跳过）。
func rewriteClassLike(super *Expression, body *ClassBody, fn func(*Identifier) Node) bool {
	changed := false
	if rewriteExprSlot(super, fn) {
		changed = true
	}
	if body == nil {
		return changed
	}
	for i := range body.Methods {
		m := &body.Methods[i]
		if m.Computed && rewriteExprSlot(&m.Key, fn) {
			changed = true
		}
		if m.Value != nil && rewriteRefsIn(m.Value, fn) {
			changed = true
		}
		if rewriteExprSlot(&m.Init, fn) {
			changed = true
		}
	}
	return changed
}

// rewritePatternDecl 声明语境解构模式：绑定名跳过，仅命中计算属性键与默认值。
func rewritePatternDecl(slot *Node, fn func(*Identifier) Node) bool {
	switch pat := (*slot).(type) {
	case *Identifier:
		return false // 绑定名
	case *ArrayPattern:
		changed := false
		for i := range pat.Elements {
			el := &pat.Elements[i]
			if el.Target != nil {
				target := Node(el.Target)
				if rewritePatternDecl(&target, fn) {
					el.Target = target.(Pattern)
					changed = true
				}
			}
			if rewriteExprSlot(&el.Default, fn) {
				changed = true
			}
		}
		return changed
	case *ObjectPattern:
		changed := false
		for i := range pat.Properties {
			p := &pat.Properties[i]
			if p.Computed && rewriteExprSlot(&p.Key, fn) {
				changed = true
			}
			if rewriteExprSlot(&p.Default, fn) {
				changed = true
			}
			if p.Value != nil {
				target := Node(p.Value)
				if rewritePatternDecl(&target, fn) {
					p.Value = target.(Pattern)
					changed = true
				}
			}
		}
		return changed
	}
	return false
}

// rewritePatternAssign 赋值语境解构模式：目标是对既有变量的引用，命中；
// 非计算属性键仍是属性名，跳过。
func rewritePatternAssign(slot *Node, fn func(*Identifier) Node) bool {
	switch pat := (*slot).(type) {
	case *Identifier:
		if repl := fn(pat); repl != nil {
			*slot = repl
			return true
		}
		return false
	case *ArrayPattern:
		changed := false
		for i := range pat.Elements {
			el := &pat.Elements[i]
			if el.Target != nil {
				target := Node(el.Target)
				if rewritePatternAssign(&target, fn) {
					el.Target = target.(Pattern)
					changed = true
				}
			}
			if rewriteExprSlot(&el.Default, fn) {
				changed = true
			}
		}
		return changed
	case *ObjectPattern:
		changed := false
		for i := range pat.Properties {
			p := &pat.Properties[i]
			if p.Computed && rewriteExprSlot(&p.Key, fn) {
				changed = true
			}
			if rewriteExprSlot(&p.Default, fn) {
				changed = true
			}
			if p.Value != nil {
				target := Node(p.Value)
				if rewritePatternAssign(&target, fn) {
					p.Value = target.(Pattern)
					changed = true
				}
			}
		}
		return changed
	}
	return rewriteRefsIn(*slot, fn)
}

// rewriteForLeft 处理 for-in/of 左值：声明语境（VarDecl）绑定名跳过；
// 表达式/模式语境（对既有变量赋值）按引用处理。
func rewriteForLeft(slot *Node, fn func(*Identifier) Node) bool {
	if *slot == nil {
		return false
	}
	switch left := (*slot).(type) {
	case *VarDecl:
		changed := false
		for i := range left.Decls {
			if rewriteVarDeclarator(&left.Decls[i], fn) {
				changed = true
			}
		}
		return changed
	case *ArrayPattern, *ObjectPattern, *Identifier:
		return rewritePatternAssign(slot, fn)
	default:
		return rewriteRefsIn(*slot, fn)
	}
}
