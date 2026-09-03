package compiler

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

func (c *Compiler) compileStmtValue(s ast.Statement) error {
	switch n := s.(type) {
	case *ast.ExprStmt:
		return c.compileExpr(n.Expr)
	case *ast.IfStmt:
		return c.compileIfValue(n)
	case *ast.BlockStmt:
		return c.compileBlockValue(n)
	case *ast.TryStmt:
		return c.compileTryValue(n)
	case *ast.EmptyStmt:
		c.emit(bytecode.OpPushUndefined, 0)
		return nil
	default:
		// Statements that don't naturally produce a value: compile normally
		// and push undefined as the result.
		if err := c.compileStmt(s); err != nil {
			return err
		}
		c.emit(bytecode.OpPushUndefined, 0)
		return nil
	}
}

// compileIfValue compiles an if statement leaving the branch's value on stack.
func (c *Compiler) compileIfValue(s *ast.IfStmt) error {
	if err := c.compileExpr(s.Test); err != nil {
		return err
	}
	jumpFalse := c.emit(bytecode.OpJmpFalsePop, 0)
	if err := c.compileStmtValue(s.Consequent); err != nil {
		return err
	}
	if s.Alternate == nil {
		// No else: when test is false, push undefined.
		jumpEnd := c.emit(bytecode.OpJmp, 0)
		c.patchJumpToHere(jumpFalse)
		c.emit(bytecode.OpPushUndefined, 0)
		c.patchJumpToHere(jumpEnd)
		return nil
	}
	jumpEnd := c.emit(bytecode.OpJmp, 0)
	c.patchJumpToHere(jumpFalse)
	if err := c.compileStmtValue(s.Alternate); err != nil {
		return err
	}
	c.patchJumpToHere(jumpEnd)
	return nil
}

// compileBlockValue compiles a block leaving the last statement's value.
func (c *Compiler) compileBlockValue(b *ast.BlockStmt) error {
	c.pushBlock()
	defer c.popBlock()
	if len(b.Body) == 0 {
		c.emit(bytecode.OpPushUndefined, 0)
		return nil
	}
	last := len(b.Body) - 1
	for i, st := range b.Body {
		if i == last {
			return c.compileStmtValue(st)
		}
		if err := c.compileStmt(st); err != nil {
			return err
		}
	}
	return nil
}

// hoistTopLevel pre-declares var and function declarations in the top-level
// function scope. This ensures NumLocals is set correctly before codegen.

func (c *Compiler) compileStmts(stmts []ast.Statement) error {
	for _, s := range stmts {
		if err := c.compileStmt(s); err != nil {
			return err
		}
	}
	return nil
}

func (c *Compiler) compileStmt(s ast.Statement) error {
	// 记录语句起始行（覆盖率 LineStarts 用）。
	if n, ok := s.(interface{ Pos() ast.Pos }); ok {
		c.cur().curLine = n.Pos().Line
	}
	switch n := s.(type) {
	case *ast.VarDecl:
		return c.compileVarDecl(n)
	case *ast.FunctionDecl:
		return c.compileFunctionDecl(n)
	case *ast.ClassDecl:
		return c.compileClassDecl(n)
	case *ast.BlockStmt:
		return c.compileBlock(n)
	case *ast.ExprStmt:
		if err := c.compileExpr(n.Expr); err != nil {
			return err
		}
		c.emit(bytecode.OpPop, 0)
		return nil
	case *ast.EmptyStmt:
		return nil
	case *ast.IfStmt:
		return c.compileIf(n)
	case *ast.WhileStmt:
		return c.compileWhile(n)
	case *ast.DoWhileStmt:
		return c.compileDoWhile(n)
	case *ast.ForStmt:
		return c.compileFor(n)
	case *ast.ForInStmt:
		return c.compileForInOrOf(n.Left, n.Right, n.Body, false, false)
	case *ast.ForOfStmt:
		return c.compileForInOrOf(n.Left, n.Right, n.Body, true, n.IsAwait)
	case *ast.ReturnStmt:
		return c.compileReturn(n)
	case *ast.BreakStmt:
		return c.compileBreak(n)
	case *ast.ContinueStmt:
		return c.compileContinue(n)
	case *ast.ThrowStmt:
		return c.compileThrow(n)
	case *ast.TryStmt:
		return c.compileTry(n)
	case *ast.SwitchStmt:
		return c.compileSwitch(n)
	case *ast.LabeledStmt:
		return c.compileLabeled(n)
	}
	return fmt.Errorf("unsupported statement %T", s)
}

func (c *Compiler) compileBlock(b *ast.BlockStmt) error {
	c.pushBlock()
	defer c.popBlock()
	// 块内函数声明提升（块级作用域开头绑定）。
	c.hoistFunctionDecls(b.Body)
	return c.compileStmts(b.Body)
}

func (c *Compiler) compileIf(s *ast.IfStmt) error {
	if err := c.compileExpr(s.Test); err != nil {
		return err
	}
	jumpFalse := c.emit(bytecode.OpJmpFalsePop, 0)
	if err := c.compileStmt(s.Consequent); err != nil {
		return err
	}
	if s.Alternate == nil {
		c.patchJumpToHere(jumpFalse)
		return nil
	}
	jumpEnd := c.emit(bytecode.OpJmp, 0)
	c.patchJumpToHere(jumpFalse)
	if err := c.compileStmt(s.Alternate); err != nil {
		return err
	}
	c.patchJumpToHere(jumpEnd)
	return nil
}

func (c *Compiler) compileWhile(s *ast.WhileStmt) error {
	loopStart := c.curPC()
	c.pushLoop(loopStart, 0)
	defer c.popLoop()
	if err := c.compileExpr(s.Test); err != nil {
		return err
	}
	jumpExit := c.emit(bytecode.OpJmpFalsePop, 0)

	// ES2015 per-iteration 绑定：体块内被闭包捕获的 let/const 每轮封存。
	captured := loopBodyCapturedBlockNames(s.Body)
	iterationSlotStart := 0
	if len(captured) > 0 {
		iterationSlotStart = c.cur().tmpl.NumLocals
	}
	if err := c.compileStmt(s.Body); err != nil {
		return err
	}
	continuePC := c.curPC()
	c.topLoop().continueTarget = continuePC
	c.patchLoopContinues(continuePC)
	if len(captured) > 0 {
		c.emit(bytecode.OpCloseUpvalues, uint32(iterationSlotStart))
	}
	c.emit(bytecode.OpJmp, uint32(loopStart-(c.curPC()+bytecode.InstrSize)))
	breakCleanupPC := c.curPC()
	if len(captured) > 0 {
		c.emit(bytecode.OpCloseUpvalues, uint32(iterationSlotStart))
	}
	exit := c.curPC()
	c.patchJumpToHere(jumpExit)
	c.topLoop().breakTarget = exit
	c.patchLoopBreaks(breakCleanupPC)
	return nil
}

func (c *Compiler) compileDoWhile(s *ast.DoWhileStmt) error {
	loopStart := c.curPC()
	c.pushLoop(0, 0) // continue target patched later
	defer c.popLoop()

	captured := loopBodyCapturedBlockNames(s.Body)
	iterationSlotStart := 0
	if len(captured) > 0 {
		iterationSlotStart = c.cur().tmpl.NumLocals
	}
	if err := c.compileStmt(s.Body); err != nil {
		return err
	}
	continuePC := c.curPC()
	c.topLoop().continueTarget = continuePC
	c.patchLoopContinues(continuePC)
	if len(captured) > 0 {
		c.emit(bytecode.OpCloseUpvalues, uint32(iterationSlotStart))
	}
	if err := c.compileExpr(s.Test); err != nil {
		return err
	}
	c.emit(bytecode.OpJmpTruePop, uint32(loopStart-(c.curPC()+bytecode.InstrSize)))
	breakCleanupPC := c.curPC()
	if len(captured) > 0 {
		c.emit(bytecode.OpCloseUpvalues, uint32(iterationSlotStart))
	}
	exit := c.curPC()
	c.topLoop().breakTarget = exit
	c.patchLoopBreaks(breakCleanupPC)
	return nil
}

func (c *Compiler) compileFor(s *ast.ForStmt) error {
	c.pushBlock()
	defer c.popBlock()

	// ES2015 per-iteration 绑定（`for (let i = 0; ...)` 的闭包语义）：
	// 每次迭代的闭包必须捕获当次迭代的 i 值（node: 0 1 2，而非 3 3 3）。
	// 实现（对齐 for...of 的 iterationSlotStart 机制）：
	//   - 头槽 headSlot：init/update/test 读写，循环外不可见
	//   - 迭代槽 iterSlot：body 引用；每次迭代开头从 headSlot 复制，
	//     body 末尾 CloseUpvalues 关闭捕获（闭包拿到当次副本）
	//   - update 后同步 iterSlot → headSlot 供下次迭代
	headSlots := map[string]int{}
	letInit := false
	if vd, ok := s.Init.(*ast.VarDecl); ok && vd.Kind == "let" {
		// 仅当 let 变量被循环体中的闭包捕获时才需要 per-iteration 绑定
		// （每次迭代新副本）；否则保持单槽位，循环指令形状不变
		// （arrayPush 等 JIT 匹配器与 trace 编译依赖该形状）。
		var letNames []string
		for _, d := range vd.Decls {
			if d.Pattern == nil {
				letNames = append(letNames, d.Name.Name)
			}
		}
		captured := forLetCapturedNames(s.Body, letNames)
		if len(captured) > 0 {
			letInit = true
			for _, name := range captured {
				headSlots[name] = c.declareLocal(name)
			}
		}
	}
	if s.Init != nil {
		switch init := s.Init.(type) {
		case *ast.VarDecl:
			if err := c.compileVarDecl(init); err != nil {
				return err
			}
		case ast.Expression:
			if err := c.compileExpr(init); err != nil {
				return err
			}
			c.emit(bytecode.OpPop, 0)
		}
	}
	loopStart := c.curPC()
	var jumpExit int
	if s.Test != nil {
		if err := c.compileExpr(s.Test); err != nil {
			return err
		}
		jumpExit = c.emit(bytecode.OpJmpFalsePop, 0)
	}
	c.pushLoop(loopStart, 0)
	defer c.popLoop()

	if letInit && len(headSlots) > 0 {
		// 迭代作用域：为每个 let 名字分配迭代槽（遮蔽头槽）。
		c.pushBlock()
		defer c.popBlock()
		iterationSlotStart := c.cur().tmpl.NumLocals
		iterSlots := make([]int, 0, len(headSlots))
		headNames := make([]string, 0, len(headSlots))
		for name := range headSlots {
			iterSlots = append(iterSlots, c.declareLocal(name))
			headNames = append(headNames, name)
		}
		// 每次迭代开头：headSlot → iterSlot 复制。
		for i, name := range headNames {
			c.emit(bytecode.OpLoadLocal, uint32(headSlots[name]))
			c.emit(bytecode.OpStoreLocal, uint32(iterSlots[i]))
		}
		if err := c.compileStmt(s.Body); err != nil {
			return err
		}
		// 迭代结束关闭捕获（continue 跳到这里之后执行 close）。
		continuePC := c.curPC()
		c.topLoop().continueTarget = continuePC
		c.patchLoopContinues(continuePC)
		c.emit(bytecode.OpCloseUpvalues, uint32(iterationSlotStart))
		if s.Update != nil {
			if err := c.compileExpr(s.Update); err != nil {
				return err
			}
			c.emit(bytecode.OpPop, 0)
		}
		// update 后同步 iterSlot → headSlot（供下次迭代的 test/复制）。
		for i, name := range headNames {
			c.emit(bytecode.OpLoadLocal, uint32(iterSlots[i]))
			c.emit(bytecode.OpStoreLocal, uint32(headSlots[name]))
		}
		c.emit(bytecode.OpJmp, uint32(loopStart-(c.curPC()+bytecode.InstrSize)))
		breakCleanupPC := c.curPC()
		c.emit(bytecode.OpCloseUpvalues, uint32(iterationSlotStart))
		exit := c.curPC()
		if s.Test != nil {
			c.patchJumpToHere(jumpExit)
		}
		c.topLoop().breakTarget = exit
		c.patchLoopBreaks(breakCleanupPC)
		return nil
	}

	// per-iteration 块级绑定：体块内被闭包捕获的 let/const 每轮封存。
	captured := loopBodyCapturedBlockNames(s.Body)
	bodyIterationSlotStart := 0
	if len(captured) > 0 {
		bodyIterationSlotStart = c.cur().tmpl.NumLocals
	}
	if err := c.compileStmt(s.Body); err != nil {
		return err
	}
	continuePC := c.curPC()
	c.topLoop().continueTarget = continuePC
	c.patchLoopContinues(continuePC)
	if len(captured) > 0 {
		c.emit(bytecode.OpCloseUpvalues, uint32(bodyIterationSlotStart))
	}
	if s.Update != nil {
		if err := c.compileExpr(s.Update); err != nil {
			return err
		}
		c.emit(bytecode.OpPop, 0)
	}
	c.emit(bytecode.OpJmp, uint32(loopStart-(c.curPC()+bytecode.InstrSize)))
	breakCleanupPC := c.curPC()
	if len(captured) > 0 {
		c.emit(bytecode.OpCloseUpvalues, uint32(bodyIterationSlotStart))
	}
	exit := c.curPC()
	if s.Test != nil {
		c.patchJumpToHere(jumpExit)
	}
	c.topLoop().breakTarget = exit
	c.patchLoopBreaks(breakCleanupPC)
	return nil
}

func (c *Compiler) compileForInOrOf(left ast.Node, right ast.Expression, body ast.Statement, isOf bool, isAwait bool) error {
	if isOf {
		return c.compileForOf(left, right, body, isAwait)
	}
	return c.compileForIn(left, right, body)
}

// compileForOf compiles `for (left of right) body` using the ES2015 iterator
// protocol: get [Symbol.iterator](), call .next(), check {done, value}.
// 当 isAwait 为 true 时编译为 `for await (left of right) body`（ES2018）：
// 使用 OpGetAsyncIterator 获取迭代器，并对每次 .next() 的结果执行 OpAwait。
func (c *Compiler) compileForOf(left ast.Node, right ast.Expression, body ast.Statement, isAwait bool) error {
	c.pushBlock()
	defer c.popBlock()

	// Evaluate iterable and get iterator.
	tmpIter := c.declareLocal("__iter__")
	if err := c.compileExpr(right); err != nil {
		return err
	}
	if isAwait {
		c.emit(bytecode.OpGetAsyncIterator, 0) // ES2018: Symbol.asyncIterator (回退 Symbol.iterator)
	} else {
		c.emit(bytecode.OpGetIterator, 0) // pop iterable, push iterator
	}
	c.emit(bytecode.OpStoreLocal, uint32(tmpIter))

	tmpResult := c.declareLocal("__iter_result__")

	nameNext := c.cur().tmpl.AddStringConst("next")
	nameDone := c.cur().tmpl.AddStringConst("done")
	nameValue := c.cur().tmpl.AddStringConst("value")

	loopStart := c.curPC()

	// Call iter.next() — push iterator as receiver, 0 args.
	c.emit(bytecode.OpLoadLocal, uint32(tmpIter))
	c.emit(bytecode.OpCallMethod, uint32(nameNext)) // 0 args encoded in high bits
	if isAwait {
		// for await：next() 返回 Promise，需 await 解包后再存储结果。
		c.emit(bytecode.OpAwait, 0)
	}
	c.emit(bytecode.OpStoreLocal, uint32(tmpResult))

	// Check done: if result.done is truthy, exit loop.
	c.emit(bytecode.OpLoadLocal, uint32(tmpResult))
	c.emit(bytecode.OpGetProp, uint32(nameDone))
	jumpExit := c.emit(bytecode.OpJmpTruePop, 0)

	c.pushLoop(loopStart, 0)
	defer c.popLoop()

	// ES2015 语义：for (let/const x of ...) 每次迭代创建新的块作用域绑定，
	// 使闭包捕获每次迭代的值（而非共享同一槽位的最终值）。
	// The compiler reuses local slots, so captured slots are closed at the end
	// of each iteration before the next value is stored into them.
	// 仅当迭代变量是 let/const 声明（VarDecl 且含 Decls）才存在 per-iteration
	// 绑定需要关闭；`for (x of ...)`（赋给已有变量）无迭代槽，发射
	// CloseUpvalues(NumLocals) 会引用越界槽（validate 报 slot out of range）。
	c.pushBlock()
	defer c.popBlock()
	iterationSlotStart := c.cur().tmpl.NumLocals
	hasIterBindings := false

	// Get value and bind to left.
	c.emit(bytecode.OpLoadLocal, uint32(tmpResult))
	c.emit(bytecode.OpGetProp, uint32(nameValue))
	if vd, ok := left.(*ast.VarDecl); ok {
		hasIterBindings = len(vd.Decls) > 0
		for _, d := range vd.Decls {
			if d.Pattern != nil {
				tmpSlot := c.newSlot()
				c.emit(bytecode.OpStoreLocal, uint32(tmpSlot))
				if err := c.compileBindPattern(d.Pattern, tmpSlot, vd.Kind); err != nil {
					return err
				}
			} else {
				slot := c.declareLocal(d.Name.Name)
				c.emit(bytecode.OpStoreLocal, uint32(slot))
			}
		}
	} else if err := c.assignTo(left); err != nil {
		return err
	}

	if err := c.compileStmt(body); err != nil {
		return err
	}
	continuePC := c.curPC()
	c.topLoop().continueTarget = continuePC
	c.patchLoopContinues(continuePC)
	if hasIterBindings {
		c.emit(bytecode.OpCloseUpvalues, uint32(iterationSlotStart))
	}
	c.emit(bytecode.OpJmp, uint32(loopStart-(c.curPC()+bytecode.InstrSize)))
	breakCleanupPC := c.curPC()
	if hasIterBindings {
		c.emit(bytecode.OpCloseUpvalues, uint32(iterationSlotStart))
	}
	exit := c.curPC()
	c.patchJumpToHere(jumpExit)
	c.topLoop().breakTarget = exit
	c.patchLoopBreaks(breakCleanupPC)
	return nil
}

// compileForIn compiles `for (left in right) body` using OpEnumKeys + index.
// OpEnumKeys 按 EnumerateObjectProperties 语义枚举原型链可枚举键，避免
// 脱糖到全局 Object.keys（会被用户覆写且不含原型链键）。
func (c *Compiler) compileForIn(left ast.Node, right ast.Expression, body ast.Statement) error {
	c.pushBlock()
	defer c.popBlock()
	tmpRight := c.declareLocal("__iter_src__")
	if err := c.compileExpr(right); err != nil {
		return err
	}
	c.emit(bytecode.OpStoreLocal, uint32(tmpRight))

	tmpKeys := c.declareLocal("__iter_keys__")
	tmpIdx := c.declareLocal("__iter_idx__")
	tmpLen := c.declareLocal("__iter_len__")

	// keys = EnumerateObjectProperties(source)
	nameLen := c.cur().tmpl.AddStringConst("length")
	c.emit(bytecode.OpLoadLocal, uint32(tmpRight))
	c.emit(bytecode.OpEnumKeys, 0)
	c.emit(bytecode.OpStoreLocal, uint32(tmpKeys))
	c.emit(bytecode.OpLoadLocal, uint32(tmpKeys))
	c.emit(bytecode.OpGetProp, uint32(nameLen))
	c.emit(bytecode.OpStoreLocal, uint32(tmpLen))
	c.emit(bytecode.OpPushInt, 0)
	c.emit(bytecode.OpStoreLocal, uint32(tmpIdx))

	loopStart := c.curPC()
	c.emit(bytecode.OpLoadLocal, uint32(tmpIdx))
	c.emit(bytecode.OpLoadLocal, uint32(tmpLen))
	c.emit(bytecode.OpLt, 0)
	jumpExit := c.emit(bytecode.OpJmpFalsePop, 0)

	c.pushLoop(loopStart, 0)
	defer c.popLoop()

	// ES2015 per-iteration 绑定：
	//   - 头变量（`for (const/let k in ...)`）每次迭代独立副本；
	//   - 体块内被闭包捕获的 let/const 同样按迭代封存。
	// 两类绑定若存在，共同使用 iterationSlotStart 作为关闭下界，continue/break
	// 目标处 OpCloseUpvalues 封存本轮 upvalue，下一轮重开（对齐 for-of 机制）。
	headIsLetConst := false
	if vd, ok := left.(*ast.VarDecl); ok {
		headIsLetConst = (vd.Kind == "let" || vd.Kind == "const") && len(vd.Decls) > 0
	}
	bodyCaptured := loopBodyCapturedBlockNames(body)
	needClose := headIsLetConst || len(bodyCaptured) > 0
	c.pushBlock()
	defer c.popBlock()
	iterationSlotStart := c.cur().tmpl.NumLocals

	// Load current key.
	c.emit(bytecode.OpLoadLocal, uint32(tmpKeys))
	c.emit(bytecode.OpLoadLocal, uint32(tmpIdx))
	c.emit(bytecode.OpGetElem, 0)
	if vd, ok := left.(*ast.VarDecl); ok {
		for _, d := range vd.Decls {
			if d.Pattern != nil {
				tmpSlot := c.newSlot()
				c.emit(bytecode.OpStoreLocal, uint32(tmpSlot))
				if err := c.compileBindPattern(d.Pattern, tmpSlot, vd.Kind); err != nil {
					return err
				}
			} else {
				slot := c.declareLocal(d.Name.Name)
				c.emit(bytecode.OpStoreLocal, uint32(slot))
			}
		}
	} else if err := c.assignTo(left); err != nil {
		return err
	}

	if err := c.compileStmt(body); err != nil {
		return err
	}
	continuePC := c.curPC()
	c.topLoop().continueTarget = continuePC
	c.patchLoopContinues(continuePC)
	if needClose {
		c.emit(bytecode.OpCloseUpvalues, uint32(iterationSlotStart))
	}
	c.emit(bytecode.OpLoadLocal, uint32(tmpIdx))
	c.emit(bytecode.OpPushInt, 1)
	c.emit(bytecode.OpAdd, 0)
	c.emit(bytecode.OpStoreLocal, uint32(tmpIdx))
	c.emit(bytecode.OpJmp, uint32(loopStart-(c.curPC()+bytecode.InstrSize)))
	breakCleanupPC := c.curPC()
	if needClose {
		c.emit(bytecode.OpCloseUpvalues, uint32(iterationSlotStart))
	}
	exit := c.curPC()
	c.patchJumpToHere(jumpExit)
	c.topLoop().breakTarget = exit
	c.patchLoopBreaks(breakCleanupPC)
	return nil
}

func (c *Compiler) compileReturn(s *ast.ReturnStmt) error {
	if s.Arg == nil {
		c.emit(bytecode.OpReturnUndef, 0)
		return nil
	}
	if err := c.compileExpr(s.Arg); err != nil {
		return err
	}
	c.emit(bytecode.OpReturn, 0)
	return nil
}

// === break / continue =====================================================

func (c *Compiler) pushLoop(loopStart, continueTarget int) {
	lc := &loopCtx{
		depth:          len(c.loopStack),
		continueTarget: continueTarget,
		breakTarget:    -1,
		hasLabel:       c.curLabel,
	}
	// 标签只属于紧邻的循环；被取走后立即清空，避免泄漏到嵌套循环
	// （否则内层循环也会拿到外层标签，误匹配 labeled break/continue）。
	c.curLabel = ""
	if loopStart != 0 {
		lc.continueTarget = loopStart
	}
	c.loopStack = append(c.loopStack, lc)
}

func (c *Compiler) popLoop() {
	c.loopStack = c.loopStack[:len(c.loopStack)-1]
}

func (c *Compiler) topLoop() *loopCtx { return c.loopStack[len(c.loopStack)-1] }

func (c *Compiler) compileBreak(s *ast.BreakStmt) error {
	if len(c.loopStack) == 0 && s.Label == "" {
		return fmt.Errorf("illegal break statement")
	}
	op := bytecode.OpJmp
	var tryRegions []int
	if len(c.tryStack) > 0 {
		// 位于 try 区域内：跳转可能穿出区域，先发 OpTryExitJmp（区域边界确定
		// 后 finalize 时若目标仍在区域内会降级回 OpJmp）。
		op = bytecode.OpTryExitJmp
		tryRegions = append([]int(nil), c.tryStack...)
	}
	pc := c.emit(op, 0)
	depth := len(c.loopStack) - 1
	c.pendingBreaks = append(c.pendingBreaks, pendingJump{
		pc: pc, depth: depth, label: s.Label,
		tryRegions: tryRegions, tryPending: len(tryRegions), tryAllInside: true,
	})
	return nil
}

func (c *Compiler) compileContinue(s *ast.ContinueStmt) error {
	if len(c.loopStack) == 0 {
		return fmt.Errorf("illegal continue statement")
	}
	op := bytecode.OpJmp
	var tryRegions []int
	if len(c.tryStack) > 0 {
		op = bytecode.OpTryExitJmp
		tryRegions = append([]int(nil), c.tryStack...)
	}
	pc := c.emit(op, 0)
	depth := len(c.loopStack) - 1
	c.pendingContinues = append(c.pendingContinues, pendingJump{
		pc: pc, depth: depth, label: s.Label,
		tryRegions: tryRegions, tryPending: len(tryRegions), tryAllInside: true,
	})
	return nil
}

// jumpTargetInsideTryRegion 判定跳转目标是否仍落在 try 区域的活动范围内
// （与 VM 运行期 jumpInsideRegion 判定一致：site 所在 phase 的区域边界）。
// site 的 phase 由编译位置静态决定（site 在 try 块 → phase 0；catch 块 →
// phase 1；finally 块 → phase 2），与运行期 handler 状态一致。
func jumpTargetInsideTryRegion(te *bytecode.TryEntry, sitePC, targetPC int) bool {
	if sitePC >= te.StartPC && sitePC <= te.EndPC {
		return targetPC >= te.StartPC && targetPC <= te.EndPC
	}
	if te.HasCatch && sitePC >= te.CatchPC && sitePC <= te.CatchEndPC {
		return targetPC >= te.CatchPC && targetPC <= te.CatchEndPC
	}
	if te.HasFinally && sitePC >= te.FinallyPC && sitePC <= te.FinallyEndPC {
		return targetPC >= te.FinallyPC && targetPC <= te.FinallyEndPC
	}
	// site 不在该区域的活动范围内（理论上不发生）：保守保持 OpTryExitJmp。
	return false
}

// finalizeTryExitJmps 在 try 区域边界齐全后（compileTry 末尾）对穿越本区域
// 的待定跳转做最终判定：目标落在全部相关区域内 → 降级回 OpJmp（等价跳转，
// 保留 JIT 可编译性）；任一区域穿出 → 保持 OpTryExitJmp（VM 负责运行 finally）。
// tryExitJmpAllRegionsFinalized 报告条目的所有相关区域是否都已记录完边界。
func (c *Compiler) tryExitJmpAllRegionsFinalized(pj *pendingJump) bool {
	for _, idx := range pj.tryRegions {
		if !c.tryFinalized[idx] {
			return false
		}
	}
	return true
}

// tryExitJmpJudge 对 operand 已 patch、区域边界已齐全的条目做最终判定：
// 目标落在全部区域内 → 降级回 OpJmp；任一区域穿出 → 保持 OpTryExitJmp
// （VM 负责运行 finally）。调用方负责从 pending 列表移除条目。
func (c *Compiler) tryExitJmpJudge(pj *pendingJump) {
	for _, idx := range pj.tryRegions {
		te := &c.cur().tmpl.TryTable[idx]
		target := jumpTargetFromCode(c.cur().tmpl.Code, pj.pc)
		if !jumpTargetInsideTryRegion(te, pj.pc, target) {
			pj.tryAllInside = false
		}
	}
	pj.tryPending = 0
	if pj.tryAllInside {
		// 目标仍在所有区域的活动范围内：OpTryExitJmp 等价于 OpJmp。
		c.cur().tmpl.Code[pj.pc] = byte(bytecode.OpJmp)
	}
}

// finalizeTryExitJmps 在 try 区域边界齐全后（compileTry 末尾）处理穿越本区域
// 的待定跳转：operand 已 patch 且所有区域已齐 → 立即判定并移除；operand 未
// patch（try 在循环体内，patch 尚未发生）→ 保留，由 patch 侧二次触发。
func (c *Compiler) finalizeTryExitJmps(tryIdx int) {
	if c.tryFinalized == nil {
		c.tryFinalized = make(map[int]bool)
	}
	c.tryFinalized[tryIdx] = true
	finalize := func(list []pendingJump) []pendingJump {
		kept := list[:0]
		for i := range list {
			pj := &list[i]
			if len(pj.tryRegions) == 0 {
				kept = append(kept, list[i])
				continue
			}
			found := false
			for _, idx := range pj.tryRegions {
				if idx == tryIdx {
					found = true
					break
				}
			}
			if !found {
				kept = append(kept, list[i])
				continue
			}
			if pj.patched && c.tryExitJmpAllRegionsFinalized(pj) {
				c.tryExitJmpJudge(pj)
				continue // 判定完成：移除
			}
			kept = append(kept, list[i])
		}
		return kept
	}
	c.pendingBreaks = finalize(c.pendingBreaks)
	c.pendingContinues = finalize(c.pendingContinues)
}

// tryExitJmpOnPatched 在 patch 侧触发判定：条目已 patch 且所有相关区域已
// 记录完边界 → 立即判定（不再等待后续 finalize）。
// 返回 true 表示条目已处理完毕（调用方不应保留）；无 tryRegions 的普通
// 跳转（非 OpTryExitJmp）同样视为已处理，不得保留在 pending 列表中——
// 否则外层循环 patch 时会再次匹配并改写其 operand。
func (c *Compiler) tryExitJmpOnPatched(pj *pendingJump) bool {
	if len(pj.tryRegions) == 0 {
		return true
	}
	if !c.tryExitJmpAllRegionsFinalized(pj) {
		return false
	}
	c.tryExitJmpJudge(pj)
	return true
}

// jumpTargetFromCode 读取跳转指令的目标 PC（operand 为相对偏移）。
func jumpTargetFromCode(code []byte, pc int) int {
	operand := uint32(code[pc+1])<<16 | uint32(code[pc+2])<<8 | uint32(code[pc+3])
	return pc + bytecode.InstrSize + bytecode.SignedOperand(operand)
}

// patchLoopBreaks patches all pending break jumps at the current loop depth.
func (c *Compiler) patchLoopBreaks(exitPC int) {
	curDepth := len(c.loopStack) // topLoop is about to be popped; use current depth
	topLabel := c.topLoop().hasLabel
	kept := c.pendingBreaks[:0]
	for i := range c.pendingBreaks {
		pj := &c.pendingBreaks[i]
		// 无标签：匹配当前循环深度；带标签：匹配当前循环的标签（忽略深度，
		// 因为 `break label` 可能出现在标签循环的任意嵌套深度）。
		if (pj.label == "" && pj.depth == curDepth-1) ||
			(pj.label != "" && pj.label == topLabel) {
			delta := exitPC - (pj.pc + bytecode.InstrSize)
			bytecode.PatchOperand(c.cur().tmpl.Code, pj.pc, uint32(delta))
			pj.patched = true
			// OpTryExitJmp 的降级判定需等区域边界确定（compileTry 末尾
			// finalize）；区域已齐则立即判定移除，否则保留待 finalize。
			if !c.tryExitJmpOnPatched(pj) {
				kept = append(kept, *pj)
			}
		} else {
			kept = append(kept, *pj)
		}
	}
	c.pendingBreaks = kept
}

// patchLoopContinues patches all pending continue jumps at the current loop depth.
func (c *Compiler) patchLoopContinues(targetPC int) {
	curDepth := len(c.loopStack)
	topLabel := c.topLoop().hasLabel
	kept := c.pendingContinues[:0]
	for i := range c.pendingContinues {
		pj := &c.pendingContinues[i]
		if (pj.label == "" && pj.depth == curDepth-1) ||
			(pj.label != "" && pj.label == topLabel) {
			delta := targetPC - (pj.pc + bytecode.InstrSize)
			bytecode.PatchOperand(c.cur().tmpl.Code, pj.pc, uint32(delta))
			pj.patched = true
			if !c.tryExitJmpOnPatched(pj) {
				kept = append(kept, *pj)
			}
		} else {
			kept = append(kept, *pj)
		}
	}
	c.pendingContinues = kept
}

func (c *Compiler) compileThrow(s *ast.ThrowStmt) error {
	if err := c.compileExpr(s.Arg); err != nil {
		return err
	}
	c.emit(bytecode.OpThrow, 0)
	return nil
}

func (c *Compiler) compileTry(s *ast.TryStmt) error {
	tmpl := c.cur().tmpl
	tryIdx := len(tmpl.TryTable)
	entry := bytecode.TryEntry{StartPC: c.curPC(), HasCatch: s.Handler != nil, HasFinally: s.Finally != nil}
	tmpl.TryTable = append(tmpl.TryTable, entry)

	c.tryStack = append(c.tryStack, tryIdx)
	defer func() { c.tryStack = c.tryStack[:len(c.tryStack)-1] }()

	c.emit(bytecode.OpTryEnter, uint32(tryIdx))
	if err := c.compileBlock(s.Block); err != nil {
		return err
	}
	// try 块末尾：记录区域边界（try 内 return/break/continue 的目标区域判定）。
	tmpl.TryTable[tryIdx].EndPC = c.emit(bytecode.OpTryExit, uint32(tryIdx))

	jmpAfter := c.emit(bytecode.OpJmp, 0)

	// Catch handler.
	if s.Handler != nil {
		handlerPC := c.curPC()
		tmpl.TryTable[tryIdx].CatchPC = handlerPC
		c.pushBlock()
		if s.Handler.Param != nil {
			slot := c.declareLocal(s.Handler.Param.Name)
			c.emit(bytecode.OpStoreLocal, uint32(slot))
		} else {
			c.emit(bytecode.OpPop, 0)
		}
		if err := c.compileStmts(s.Handler.Body.Body); err != nil {
			return err
		}
		c.popBlock()
		tmpl.TryTable[tryIdx].CatchEndPC = c.emit(bytecode.OpTryExit, uint32(tryIdx))
	}

	endPC := c.curPC()
	bytecode.PatchOperand(tmpl.Code, jmpAfter, uint32(endPC-(jmpAfter+bytecode.InstrSize)))

	if s.Finally != nil {
		finallyPC := c.curPC()
		tmpl.TryTable[tryIdx].FinallyPC = finallyPC
		c.pushBlock()
		if err := c.compileStmts(s.Finally.Body); err != nil {
			return err
		}
		c.popBlock()
		tmpl.TryTable[tryIdx].FinallyEndPC = c.emit(bytecode.OpTryExitFinally, uint32(tryIdx))
	}
	// 区域边界已齐全：对穿越本区域的待定跳转做最终判定（降级 OpTryExitJmp）。
	c.finalizeTryExitJmps(tryIdx)
	return nil
}

// compileTryValue compiles a try statement in "value mode": the value of the
// try block (on normal completion) or catch block (on caught exception) — or
// finally block if present — is left on top of the stack. Used for REPL
// semantics when a try statement is the last top-level statement.
//
// 设计要点：try 正常完成时保留块值；catch 接住异常时也用值模式编译 body。
// 两路径在 endPC 汇合，栈深度均为 1。finally 存在时其值覆盖 try/catch 值。
func (c *Compiler) compileTryValue(s *ast.TryStmt) error {
	tmpl := c.cur().tmpl
	tryIdx := len(tmpl.TryTable)
	entry := bytecode.TryEntry{StartPC: c.curPC(), HasCatch: s.Handler != nil, HasFinally: s.Finally != nil}
	tmpl.TryTable = append(tmpl.TryTable, entry)

	c.tryStack = append(c.tryStack, tryIdx)
	defer func() { c.tryStack = c.tryStack[:len(c.tryStack)-1] }()

	// try 块用值模式编译（保留最后表达式值）。
	c.emit(bytecode.OpTryEnter, uint32(tryIdx))
	if err := c.compileBlockValue(s.Block); err != nil {
		return err
	}
	tmpl.TryTable[tryIdx].EndPC = c.emit(bytecode.OpTryExit, uint32(tryIdx))

	jmpAfter := c.emit(bytecode.OpJmp, 0)

	// catch 块用值模式编译（保留 body 最后表达式值）。
	if s.Handler != nil {
		handlerPC := c.curPC()
		tmpl.TryTable[tryIdx].CatchPC = handlerPC
		c.pushBlock()
		if s.Handler.Param != nil {
			slot := c.declareLocal(s.Handler.Param.Name)
			c.emit(bytecode.OpStoreLocal, uint32(slot))
		} else {
			c.emit(bytecode.OpPop, 0)
		}
		if err := c.compileStmtsAsValue(s.Handler.Body.Body); err != nil {
			return err
		}
		c.popBlock()
		tmpl.TryTable[tryIdx].CatchEndPC = c.emit(bytecode.OpTryExit, uint32(tryIdx))
	}

	endPC := c.curPC()
	bytecode.PatchOperand(tmpl.Code, jmpAfter, uint32(endPC-(jmpAfter+bytecode.InstrSize)))

	// finally 块（若有）：finally 的完成值覆盖 try/catch 的值。
	if s.Finally != nil {
		finallyPC := c.curPC()
		tmpl.TryTable[tryIdx].FinallyPC = finallyPC
		c.pushBlock()
		// finally 执行前弹出 try/catch 的结果值（finally 的值才是最终值）。
		c.emit(bytecode.OpPop, 0)
		if err := c.compileStmtsAsValue(s.Finally.Body); err != nil {
			return err
		}
		c.popBlock()
		tmpl.TryTable[tryIdx].FinallyEndPC = c.emit(bytecode.OpTryExitFinally, uint32(tryIdx))
	}
	c.finalizeTryExitJmps(tryIdx)
	return nil
}

// compileStmtsAsValue 编译语句列表，最后一条语句用值模式（保留值）。空列表 push undefined。
func (c *Compiler) compileStmtsAsValue(body []ast.Statement) error {
	if len(body) == 0 {
		c.emit(bytecode.OpPushUndefined, 0)
		return nil
	}
	last := len(body) - 1
	for i, st := range body {
		if i == last {
			return c.compileStmtValue(st)
		}
		if err := c.compileStmt(st); err != nil {
			return err
		}
	}
	return nil
}

func (c *Compiler) compileSwitch(s *ast.SwitchStmt) error {
	c.pushBlock()
	defer c.popBlock()
	// Switch 是 break 的合法目标：必须在编译 case 体之前压入 loopCtx，
	// 否则 case 内的 break 会被 compileBreak 判为 "illegal break statement"。
	c.pushLoop(0, 0)

	discSlot := c.declareLocal("__switch_disc__")
	if err := c.compileExpr(s.Disc); err != nil {
		return err
	}
	c.emit(bytecode.OpStoreLocal, uint32(discSlot))

	var caseJumps []int
	var defaultJump int = -1
	for _, cs := range s.Cases {
		if cs.Test == nil {
			defaultJump = c.emit(bytecode.OpJmp, 0)
		} else {
			c.emit(bytecode.OpLoadLocal, uint32(discSlot))
			if err := c.compileExpr(cs.Test); err != nil {
				return err
			}
			c.emit(bytecode.OpStrictEq, 0)
			caseJumps = append(caseJumps, c.emit(bytecode.OpJmpTruePop, 0))
		}
	}
	fallJmp := c.emit(bytecode.OpJmp, 0)

	bodyPCs := make([]int, len(s.Cases))
	for i, cs := range s.Cases {
		bodyPCs[i] = c.curPC()
		c.pushBlock()
		if err := c.compileStmts(cs.Consequent); err != nil {
			return err
		}
		c.popBlock()
		_ = cs
	}

	idx := 0
	for i, cs := range s.Cases {
		if cs.Test == nil {
			if defaultJump >= 0 {
				bytecode.PatchOperand(c.cur().tmpl.Code, defaultJump, uint32(bodyPCs[i]-(defaultJump+bytecode.InstrSize)))
			}
		} else {
			pc := caseJumps[idx]
			bytecode.PatchOperand(c.cur().tmpl.Code, pc, uint32(bodyPCs[i]-(pc+bytecode.InstrSize)))
			idx++
		}
	}
	endPC := c.curPC()
	bytecode.PatchOperand(c.cur().tmpl.Code, fallJmp, uint32(endPC-(fallJmp+bytecode.InstrSize)))

	c.topLoop().breakTarget = endPC
	c.patchLoopBreaks(endPC)
	c.popLoop()
	return nil
}

func (c *Compiler) compileLabeled(s *ast.LabeledStmt) error {
	// 标签只有绑定到循环时才对 break/continue 生效（continue 到非循环标签是
	// 非法 JS）。body 是循环时设置 curLabel，由该循环的 pushLoop 拾取；
	// body 非循环（如块）时保持 curLabel 为空，`break label` 由下方 endPC patch 处理。
	if isLoopStmt(s.Body) {
		c.curLabel = s.Label
	}
	if err := c.compileStmt(s.Body); err != nil {
		return err
	}
	c.curLabel = ""
	// 标签包裹块（非循环）时：`break label` 跳到标签语句末尾。
	endPC := c.curPC()
	kept := c.pendingBreaks[:0]
	for i := range c.pendingBreaks {
		pj := &c.pendingBreaks[i]
		if pj.label == s.Label {
			delta := endPC - (pj.pc + bytecode.InstrSize)
			bytecode.PatchOperand(c.cur().tmpl.Code, pj.pc, uint32(delta))
			pj.patched = true
			if !c.tryExitJmpOnPatched(pj) {
				kept = append(kept, *pj)
			}
		} else {
			kept = append(kept, *pj)
		}
	}
	c.pendingBreaks = kept
	return nil
}

// isLoopStmt 判断语句是否为循环（标签可绑定 break/continue）。
func isLoopStmt(s ast.Statement) bool {
	switch s.(type) {
	case *ast.ForStmt, *ast.WhileStmt, *ast.DoWhileStmt, *ast.ForInStmt, *ast.ForOfStmt:
		return true
	}
	return false
}

// === expressions ==========================================================
