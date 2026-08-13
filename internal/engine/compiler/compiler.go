// Package compiler translates an AST into bytecode.FuncTemplate(s).
//
// Strategy:
//   - One FuncTemplate per function (and one for the top-level program).
//   - Lexical scoping via a scope chain; `var` binds at function scope, `let`/
//     `const` at block scope.
//   - Variable resolution: local → upvalue → global, in that order.
//   - Upvalue captures are recorded per-function for OpMakeClosure.
//   - Forward jumps (if/else, loops, switch) are back-patched.
//   - `this` lives in local slot 0 of each function frame.
//   - Arguments occupy slots 1..N; locals start at N+1.
package compiler

import (
	"fmt"
	"math/big"
	"reflect"
	"strconv"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// Compiler holds the compilation state for a single module.
type Compiler struct {
	module *bytecode.Module

	funcStack []*funcCtx // stack of active function compilation contexts

	// loopStack tracks break/continue targets for the innermost loops.
	loopStack []*loopCtx

	// tryStack 统计当前函数内活动的 try 区域索引栈（compileTry/compileTryValue
	// 进入时压入、退出时弹出）。break/continue 编译时若 tryStack 非空则发出
	// OpTryExitJmp，让 VM 在跳转穿出 try 区域前先运行 finally；patch 时若
	// 目标仍在区域内则降级回 OpJmp。
	tryStack []int

	// tryFinalized 标记各 try 区域是否已完成边界记录（compileTry 末尾）。
	// OpTryExitJmp 的降级判定需要「operand 已 patch（循环结束）且区域边界已
	// 齐全（try 结束）」两者都满足；try 与循环的编译顺序双向都可能，故用
	// 该标记在 patch 与 finalize 两侧互相触发。
	tryFinalized map[int]bool

	// pendingBreaks collects forward break jumps that must be patched when
	// their enclosing loop exits. Each entry records the loop depth at which
	// it was emitted; on loop exit we patch entries with matching depth.
	pendingBreaks    []pendingJump
	pendingContinues []pendingJump

	// classCounter generates unique upvalue names for each class's
	// __home_ctor_N__ / __home_proto_N__ bindings (supporting `super`).
	classCounter int
	// curClassID is the ID of the class currently being compiled (for super
	// resolution). -1 when not inside a class body.
	curClassID int

	// optionalChainStack tracks pending OpOptionalJump PCs for the current
	// optional chaining (?.) context. Each entry is a list of jump PCs that
	// must be patched to the end of the chain when it completes.
	optionalChainStack [][]int
	// optionalChainResiduals 与 optionalChainStack 平行：每级 OpOptionalJump
	// 短路时需清理的链内残留值个数（短路发生时栈上链内值数 - 1）。残留数
	// 编码进 operand 高 8 位，VM 短路时弹出清理，避免残留污染后续栈。
	optionalChainResiduals [][]int
	// optChainPushCount 是当前可选链内"栈上链值数"（短路时残留 = 计数-1）。
	// 链内指令的净栈效应在发射处维护（optChainDelta）；嵌套链继承外层计数
	// （内层短路残留含外层未消费值），end 恢复为外层值 + 1（内层结果压入 1）。
	optChainPushCount  int
	optChainPushSaved  []int
	optChainPushActive bool

	// curLabel is the label of the labeled statement currently being compiled
	// (set by compileLabeled). Loops pick it up as their loopCtx.hasLabel so
	// that labeled break/continue jumps can be resolved to this loop.
	curLabel string

	// lastFuncExprIdx 是最近一次 compileFunction 编译的函数模板索引
	// （I-2 const 绑定登记用：compileVarDecl 在 Init 为函数表达式时读取）。
	lastFuncExprIdx int
}

type pendingJump struct {
	pc    int
	depth int
	label string

	// tryRegions 是跳转指令发射时处于活动状态的 try 区域索引（tryStack 快照）。
	// tryPending/tryAllInside 配合 compileTry 末尾的 finalizeTryExitJmps：
	// 每个区域边界确定后逐一判定，目标落在全部区域内时把 OpTryExitJmp
	// 降级回 OpJmp（等价跳转，保留循环的 JIT 可编译性）。
	// patched 标记 operand 已由 patchLoopBreaks/patchLoopContinues 填充；
	// 条目在 finalize 完成后才会从 pending 列表移除。
	tryRegions   []int
	tryPending   int
	tryAllInside bool
	patched      bool
}

type loopCtx struct {
	depth          int
	continueTarget int // PC to jump to on `continue` (backward jump)
	breakTarget    int // PC to jump to on `break`; -1 means "patch at loop exit"
	hasLabel       string
}

// funcCtx is the per-function compilation context.
type funcCtx struct {
	tmpl *bytecode.FuncTemplate

	// curLine 是当前编译语句的源行号（覆盖率 LineStarts 用）。
	curLine int

	// usedArguments 标记函数体引用了 own `arguments`（未引用则运行时
	// 跳过每帧 arguments 对象创建，O-5 调用快速路径）。
	usedArguments bool

	scopes []*scope // scope chain; scopes[0] is the function scope

	// upvalueIndex maps a name to its index in tmpl.Upvalues.
	upvalueIndex map[string]int

	// inlineCandidates 记录当前函数作用域内 const/let 绑定名 → 函数模板
	// 索引（-1 = 绑定不可内联）。I-2 调用点展开用；同名重绑定会覆盖。
	inlineCandidates map[string]int
}

type scope struct {
	parent *scope
	decls  map[string]int // name → local slot in current function
	isFunc bool           // function scope (vars hoist here)
}

// isolateControlFlow gives each function its own loop and pending-jump state.
// Function declarations are compiled during hoisting while the enclosing
// function may itself be inside a loop; sharing these slices lets a child
// function's continue/break jumps target bytecode in the parent function.
func (c *Compiler) isolateControlFlow() func() {
	savedLoops := c.loopStack
	savedBreaks := c.pendingBreaks
	savedContinues := c.pendingContinues
	savedLabel := c.curLabel
	savedTryStack := c.tryStack
	savedTryFinalized := c.tryFinalized
	c.loopStack = nil
	c.pendingBreaks = nil
	c.pendingContinues = nil
	c.curLabel = ""
	c.tryStack = nil
	c.tryFinalized = nil
	return func() {
		c.loopStack = savedLoops
		c.pendingBreaks = savedBreaks
		c.pendingContinues = savedContinues
		c.curLabel = savedLabel
		c.tryStack = savedTryStack
		c.tryFinalized = savedTryFinalized
	}
}

// New creates a Compiler.
func New() *Compiler { return &Compiler{module: bytecode.NewModule(), curClassID: -1} }

// Compile compiles a whole program AST into a Module. The top-level program
// is returned as Functions[0].
func (c *Compiler) Compile(prog *ast.Program, filename string) (*bytecode.Module, error) {
	tmpl := &bytecode.FuncTemplate{
		Name:       "<main>",
		NumLocals:  1, // slot 0 = global `this` (undefined at top level)
		SourceFile: filename,
	}
	c.module.AddFunction(tmpl)

	fc := &funcCtx{
		tmpl:             tmpl,
		upvalueIndex:     make(map[string]int),
		inlineCandidates: map[string]int{},
	}
	fc.scopes = []*scope{{decls: make(map[string]int), isFunc: true}}
	c.funcStack = append(c.funcStack, fc)

	// slot 0 is reserved for `this`
	fc.scopes[0].decls["__this__"] = 0

	// Hoist top-level var/function declarations so NumLocals is correct.
	c.hoistTopLevel(prog.Body)

	// 函数声明提升：编译并绑定所有顶层函数声明，使其名字在后续语句前可用。
	c.hoistFunctionDecls(prog.Body)

	// Compile all statements except the last normally; the last statement is
	// compiled in "value mode" so its value is returned (REPL semantics,
	// matching the AST interpreter).
	n := len(prog.Body)
	for i, s := range prog.Body {
		if i == n-1 {
			if err := c.compileStmtValue(s); err != nil {
				return nil, err
			}
			c.emit(bytecode.OpReturn, 0)
			if err := c.finalizeMaxStack(); err != nil {
				return nil, err
			}
			return c.module, nil
		}
		if err := c.compileStmt(s); err != nil {
			return nil, err
		}
	}
	// Implicit return undefined at end of program.
	c.emit(bytecode.OpReturnUndef, 0)
	if err := c.finalizeMaxStack(); err != nil {
		return nil, err
	}
	return c.module, nil
}

// finalizeMaxStack 为模块内每个函数模板填充 MaxStack（操作数栈峰值上界），
// 供 VM 按帧预分配栈、使 push 无分支。在 Compile 的所有返回路径前调用，
// 覆盖 Compile/CompileAST/EvalProgram（含 REPL、缓存未命中重编译）全路径。
func (c *Compiler) finalizeMaxStack() error {
	for _, fn := range c.module.Functions {
		if fn == nil {
			continue
		}
		ms, err := bytecode.ComputeMaxStack(c.module, fn)
		if err != nil {
			return fmt.Errorf("aluka: compile %s: %w", fn.SourceFile, err)
		}
		fn.MaxStack = ms
	}
	return nil
}

// compileStmtValue compiles a statement in "value mode": it leaves the
// statement's value on top of the stack (instead of popping it). Used for the
// last top-level statement to implement REPL semantics.
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
func (c *Compiler) hoistTopLevel(stmts []ast.Statement) {
	var walk func([]ast.Statement)
	walk = func(ss []ast.Statement) {
		for _, s := range ss {
			switch st := s.(type) {
			case *ast.VarDecl:
				// var 提升到函数作用域；let/const 也提前占槽（初始 undefined），
				// 使函数声明提升编译的函数体能正确捕获同作用域的 let/const
				// （TDZ 简化为 undefined，对真实包兼容）。
				if st.Kind == "var" {
					c.hoistVarDeclarators(st.Decls)
				} else {
					for _, d := range st.Decls {
						if d.Name != nil {
							c.declareLocal(d.Name.Name)
						} else if d.Pattern != nil {
							c.declarePatternSlots(d.Pattern)
						}
					}
				}
			case *ast.FunctionDecl:
				if st.Name != nil {
					c.declareVar(st.Name.Name)
				}
			case *ast.ClassDecl:
				// class 声明同样需预先占槽（与 compileClassDecl 的 declareVar
				// 一致），否则被提升到顶部的函数声明闭包会把类名解析为全局
				// （undefined），导致 `function f(){ return Foo; }` 取到 undefined。
				if st.Name != nil {
					c.declareVar(st.Name.Name)
				}
			case *ast.BlockStmt:
				walk(st.Body)
			case *ast.IfStmt:
				walk([]ast.Statement{st.Consequent})
				if st.Alternate != nil {
					walk([]ast.Statement{st.Alternate})
				}
			case *ast.ForStmt:
				if vd, ok := st.Init.(*ast.VarDecl); ok && vd.Kind == "var" {
					c.hoistVarDeclarators(vd.Decls)
				}
				if st.Body != nil {
					walk([]ast.Statement{st.Body})
				}
			case *ast.WhileStmt:
				if st.Body != nil {
					walk([]ast.Statement{st.Body})
				}
			case *ast.DoWhileStmt:
				if st.Body != nil {
					walk([]ast.Statement{st.Body})
				}
			case *ast.ForInStmt:
				if vd, ok := st.Left.(*ast.VarDecl); ok && vd.Kind == "var" {
					c.hoistVarDeclarators(vd.Decls)
				}
				if st.Body != nil {
					walk([]ast.Statement{st.Body})
				}
			case *ast.ForOfStmt:
				if vd, ok := st.Left.(*ast.VarDecl); ok && vd.Kind == "var" {
					c.hoistVarDeclarators(vd.Decls)
				}
				if st.Body != nil {
					walk([]ast.Statement{st.Body})
				}
			}
		}
	}
	walk(stmts)
}

// === scope helpers ========================================================

func (c *Compiler) cur() *funcCtx { return c.funcStack[len(c.funcStack)-1] }

func (c *Compiler) pushBlock() {
	fc := c.cur()
	fc.scopes = append(fc.scopes, &scope{
		parent: fc.scopes[len(fc.scopes)-1],
		decls:  make(map[string]int),
	})
}

func (c *Compiler) popBlock() {
	fc := c.cur()
	fc.scopes = fc.scopes[:len(fc.scopes)-1]
}

func (fc *funcCtx) functionScope() *scope { return fc.scopes[0] }

// newSlot allocates a fresh local slot in the current function.
func (c *Compiler) newSlot() int {
	slot := c.cur().tmpl.NumLocals
	c.cur().tmpl.NumLocals++
	return slot
}

// declareLocal binds name to a fresh slot in the innermost scope.
func (c *Compiler) declareLocal(name string) int {
	fc := c.cur()
	s := fc.scopes[len(fc.scopes)-1]
	if slot, ok := s.decls[name]; ok {
		return slot
	}
	slot := c.newSlot()
	s.decls[name] = slot
	return slot
}

// declareVar binds name at the function scope (var hoisting).
func (c *Compiler) declareVar(name string) int {
	fc := c.cur()
	s := fc.functionScope()
	if slot, ok := s.decls[name]; ok {
		return slot
	}
	slot := c.newSlot()
	s.decls[name] = slot
	return slot
}

// resolve returns (kind, slot) where kind is "local" | "upvalue" | "global".
func (c *Compiler) resolve(name string) (string, int) {
	fc := c.cur()
	for i := len(fc.scopes) - 1; i >= 0; i-- {
		if slot, ok := fc.scopes[i].decls[name]; ok {
			if name == "arguments" {
				fc.usedArguments = true
			}
			return "local", slot
		}
	}
	if uv, ok := c.resolveUpvalue(name); ok {
		return "upvalue", uv
	}
	gIdx := fc.tmpl.AddStringConst(name)
	return "global", gIdx
}

// resolveUpvalue resolves name as an upvalue for the CURRENT (innermost)
// function. It returns the upvalue index in c.cur() that should be used to
// reference name, or (0,false) if name is not visible in any enclosing
// function.
//
// Algorithm (mirrors Lua's singlevaraux): each enclosing function is searched
// in turn; the first one that owns name (as a local or already-captured
// upvalue) becomes the source, and every function between it and the current
// one gets an upvalue capture so the chain resolves at runtime.
func (c *Compiler) resolveUpvalue(name string) (int, bool) {
	if len(c.funcStack) < 2 {
		return 0, false
	}
	enclosingIdx := len(c.funcStack) - 2
	isLocal, idx, ok := c.resolveUpvalueFrom(name, enclosingIdx)
	if !ok {
		return 0, false
	}
	// 箭头函数词法继承外层 own `arguments`：拥有槽的外层函数必须创建
	// arguments 对象（O-5：resolveUpvalueFrom 已在拥有者上置位）。
	// Create the upvalue in the current function. isLocal/idx describe the
	// source relative to the immediately-enclosing function.
	return c.addUpvalue(name, isLocal, idx), true
}

// resolveUpvalueFrom returns (isLocal, index, ok) describing where name lives
// relative to funcStack[ctxIdx]. It creates upvalue captures in each
// intermediate function so nested closures can chain through to the original
// owner:
//   - name is a local in funcStack[ctxIdx] → (true, slot, true). No upvalue
//     is created in ctxIdx; the caller (one level in) creates it.
//   - name is already an upvalue in funcStack[ctxIdx] → (false, upvalIdx, true).
//   - otherwise recurse; create an upvalue in funcStack[ctxIdx] and return
//     (false, newIdx, true).
func (c *Compiler) resolveUpvalueFrom(name string, ctxIdx int) (bool, int, bool) {
	if ctxIdx < 0 {
		return false, 0, false
	}
	fc := c.funcStack[ctxIdx]
	// Found as a local in this function: report it; the caller creates the
	// upvalue capture (so IsLocal=true is relative to the caller's parent).
	for i := len(fc.scopes) - 1; i >= 0; i-- {
		if slot, ok := fc.scopes[i].decls[name]; ok {
			if name == "arguments" {
				// 拥有 arguments 槽的函数必须创建对象（嵌套箭头/闭包引用）。
				fc.usedArguments = true
			}
			return true, slot, true
		}
	}
	// Already captured as an upvalue by this function: reuse it.
	if idx, ok := fc.upvalueIndex[name]; ok {
		return false, idx, true
	}
	// Not visible here: recurse to the next enclosing function.
	isLocal, idx, ok := c.resolveUpvalueFrom(name, ctxIdx-1)
	if !ok {
		return false, 0, false
	}
	// Create an upvalue in THIS function pointing at the source (which lives
	// in funcStack[ctxIdx-1]).
	newIdx := c.addUpvalueToFunc(ctxIdx, name, isLocal, idx)
	return false, newIdx, true
}

// addUpvalue records a capture in the CURRENT function's template, returning
// the upvalue index the current function should use to reference it.
func (c *Compiler) addUpvalue(name string, isLocal bool, srcIdx int) int {
	return c.addUpvalueToFunc(len(c.funcStack)-1, name, isLocal, srcIdx)
}

// addUpvalueToFunc records a capture in funcStack[fIdx]'s template, returning
// the upvalue index within that function. Deduplicates by name.
func (c *Compiler) addUpvalueToFunc(fIdx int, name string, isLocal bool, srcIdx int) int {
	fc := c.funcStack[fIdx]
	if idx, ok := fc.upvalueIndex[name]; ok {
		return idx
	}
	fc.tmpl.Upvalues = append(fc.tmpl.Upvalues, bytecode.UpvalueCapture{
		IsLocal: isLocal,
		Index:   srcIdx,
	})
	idx := len(fc.tmpl.Upvalues) - 1
	fc.upvalueIndex[name] = idx
	return idx
}

// === statements ===========================================================

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

func (c *Compiler) compileVarDecl(d *ast.VarDecl) error {
	for _, decl := range d.Decls {
		if decl.Pattern != nil {
			// Destructuring declaration.
			if decl.Init == nil {
				// `let [a, b];` — declare all bindings as undefined.
				for _, name := range patternNames(decl.Pattern) {
					if d.Kind == "var" {
						c.declareVar(name)
					} else {
						c.declareLocal(name)
					}
				}
				continue
			}
			// Evaluate init → store in temp slot → bind pattern.
			if err := c.compileExpr(decl.Init); err != nil {
				return err
			}
			tmpSlot := c.newSlot()
			c.emit(bytecode.OpStoreLocal, uint32(tmpSlot))
			if err := c.compileBindPattern(decl.Pattern, tmpSlot, d.Kind); err != nil {
				return err
			}
			continue
		}
		var slot int
		if d.Kind == "var" {
			slot = c.declareVar(decl.Name.Name)
		} else {
			slot = c.declareLocal(decl.Name.Name)
		}
		if decl.Init != nil {
			if d.Kind == "const" {
				// I-2：登记 const 绑定的函数表达式为内联候选（同名重绑定覆盖
				// 为不可内联）；let/var 可重赋值，不登记。
				if fe, ok := decl.Init.(*ast.ArrowFunc); ok {
					if err := c.compileExpr(decl.Init); err != nil {
						return err
					}
					if fe.IsAsync {
						c.cur().inlineCandidates[decl.Name.Name] = -1
					} else {
						c.cur().inlineCandidates[decl.Name.Name] = c.lastFuncExprIdx
					}
				} else if fe, ok := decl.Init.(*ast.FunctionExpr); ok {
					if err := c.compileExpr(decl.Init); err != nil {
						return err
					}
					if fe.IsAsync || fe.IsGenerator || fe.Name != nil {
						// NFE（具名函数表达式）体内引用自身，内联展开后
						// 自引用绑定丢失，不可内联。
						c.cur().inlineCandidates[decl.Name.Name] = -1
					} else {
						c.cur().inlineCandidates[decl.Name.Name] = c.lastFuncExprIdx
					}
				} else {
					c.cur().inlineCandidates[decl.Name.Name] = -1
					if err := c.compileExpr(decl.Init); err != nil {
						return err
					}
				}
			} else {
				if err := c.compileExpr(decl.Init); err != nil {
					return err
				}
			}
			c.emit(bytecode.OpStoreLocal, uint32(slot))
		}
	}
	return nil
}

// patternNames extracts all identifier names bound by a destructuring pattern.
func patternNames(p ast.Pattern) []string {
	switch pat := p.(type) {
	case *ast.Identifier:
		return []string{pat.Name}
	case *ast.ArrayPattern:
		var names []string
		for _, el := range pat.Elements {
			if el.Target != nil {
				names = append(names, patternNames(el.Target)...)
			}
		}
		return names
	case *ast.ObjectPattern:
		var names []string
		for _, prop := range pat.Properties {
			names = append(names, patternNames(prop.Value)...)
		}
		return names
	}
	return nil
}

// compileBindPattern emits code to destructure the value in srcSlot into the
// bindings declared by the pattern. `kind` is "var" or "let"/"const" and
// controls whether bindings go to the function scope or the block scope.
// declarePatternSlots 为解构模式中的所有绑定名提前占槽（用于 let/const 提升）。
func (c *Compiler) declarePatternSlots(p ast.Pattern) {
	switch pat := p.(type) {
	case *ast.Identifier:
		c.declareLocal(pat.Name)
	case *ast.ArrayPattern:
		for _, el := range pat.Elements {
			if el.Target != nil {
				c.declarePatternSlots(el.Target)
			}
		}
	case *ast.ObjectPattern:
		for _, prop := range pat.Properties {
			if prop.Value != nil {
				c.declarePatternSlots(prop.Value)
			} else if id, ok := prop.Key.(*ast.Identifier); ok {
				c.declareLocal(id.Name)
			}
		}
	}
}

func (c *Compiler) compileBindPattern(p ast.Pattern, srcSlot int, kind string) error {
	switch pat := p.(type) {
	case *ast.Identifier:
		var slot int
		if kind == "var" {
			slot = c.declareVar(pat.Name)
		} else {
			slot = c.declareLocal(pat.Name)
		}
		c.emit(bytecode.OpLoadLocal, uint32(srcSlot))
		c.emit(bytecode.OpStoreLocal, uint32(slot))

	case *ast.ArrayPattern:
		for i, el := range pat.Elements {
			if el.Target == nil {
				continue // hole
			}
			tmpSlot := c.newSlot()
			if el.IsRest {
				// rest: result = src.slice(i) — method call with this=src
				c.emit(bytecode.OpLoadLocal, uint32(srcSlot)) // receiver
				c.emit(bytecode.OpPushInt, uint32(i))         // arg
				sliceNameIdx := c.cur().tmpl.AddStringConst("slice")
				operand := uint32(1)<<16 | uint32(sliceNameIdx&0xFFFF)
				c.emit(bytecode.OpCallMethod, operand)
				c.emit(bytecode.OpStoreLocal, uint32(tmpSlot))
			} else {
				// element: result = src[i]
				c.emit(bytecode.OpLoadLocal, uint32(srcSlot))
				c.emit(bytecode.OpPushInt, uint32(i))
				c.emit(bytecode.OpGetElem, 0)
				c.emit(bytecode.OpStoreLocal, uint32(tmpSlot))
			}
			// Apply default if value is undefined.
			if el.Default != nil {
				c.emit(bytecode.OpLoadLocal, uint32(tmpSlot))
				c.emit(bytecode.OpPushUndefined, 0)
				c.emit(bytecode.OpStrictEq, 0)
				jSkip := c.emit(bytecode.OpJmpFalsePop, 0)
				if err := c.compileExpr(el.Default); err != nil {
					return err
				}
				c.emit(bytecode.OpStoreLocal, uint32(tmpSlot))
				c.patchJumpToHere(jSkip)
			}
			if err := c.compileBindPattern(el.Target, tmpSlot, kind); err != nil {
				return err
			}
		}

	case *ast.ObjectPattern:
		for propIndex, prop := range pat.Properties {
			tmpSlot := c.newSlot()
			if prop.IsRest {
				// Object rest: create { ...src } then delete already-bound keys.
				c.emit(bytecode.OpNewObject, 0)
				c.emit(bytecode.OpLoadLocal, uint32(srcSlot))
				c.emit(bytecode.OpSpreadObject, 0)
				// Delete each already-bound key from the rest object.
				for _, bound := range pat.Properties[:propIndex] {
					c.emit(bytecode.OpDup, 0) // dup rest obj
					if bound.Computed {
						if err := c.compileExpr(bound.Key); err != nil {
							return err
						}
						c.emit(bytecode.OpDelElem, 0)
					} else {
						nameIdx := c.cur().tmpl.AddStringConst(propKey(bound.Key))
						c.emit(bytecode.OpDelProp, uint32(nameIdx))
					}
					c.emit(bytecode.OpPop, 0) // discard bool
				}
				c.emit(bytecode.OpStoreLocal, uint32(tmpSlot))
				c.compileBindPattern(prop.Value, tmpSlot, kind)
				continue
			}
			// result = src[key]
			c.emit(bytecode.OpLoadLocal, uint32(srcSlot))
			if prop.Computed {
				if err := c.compileExpr(prop.Key); err != nil {
					return err
				}
				c.emit(bytecode.OpGetElem, 0)
			} else {
				key := propKey(prop.Key)
				nameIdx := c.cur().tmpl.AddStringConst(key)
				c.emit(bytecode.OpGetProp, uint32(nameIdx))
			}
			c.emit(bytecode.OpStoreLocal, uint32(tmpSlot))
			// Apply default if value is undefined.
			if prop.Default != nil {
				c.emit(bytecode.OpLoadLocal, uint32(tmpSlot))
				c.emit(bytecode.OpPushUndefined, 0)
				c.emit(bytecode.OpStrictEq, 0)
				jSkip := c.emit(bytecode.OpJmpFalsePop, 0)
				if err := c.compileExpr(prop.Default); err != nil {
					return err
				}
				c.emit(bytecode.OpStoreLocal, uint32(tmpSlot))
				c.patchJumpToHere(jSkip)
			}
			if err := c.compileBindPattern(prop.Value, tmpSlot, kind); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Compiler) compileFunctionDecl(d *ast.FunctionDecl) error {
	// 函数声明已在 hoistFunctionDecls 中提升编译（名字提前绑定），
	// 这里跳过避免重复编译。
	if d.Name == nil {
		return nil
	}
	return nil
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

// compileForIn compiles `for (left in right) body` using Object.keys + index.
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

	// keys = Object.keys(source)
	nameLen := c.cur().tmpl.AddStringConst("length")
	objIdx := c.cur().tmpl.AddStringConst("Object")
	keysIdx := c.cur().tmpl.AddStringConst("keys")
	c.emit(bytecode.OpLoadGlobal, uint32(objIdx))
	c.emit(bytecode.OpGetProp, uint32(keysIdx))
	c.emit(bytecode.OpLoadLocal, uint32(tmpRight))
	c.emit(bytecode.OpCall, 1)
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

func (c *Compiler) compileExpr(e ast.Expression) error {
	switch n := e.(type) {
	case *ast.NumberLit:
		f := n.Value
		if f == float64(int64(f)) {
			iv := int64(f)
			if iv >= 0 && iv < (1<<24) {
				c.emit(bytecode.OpPushInt, uint32(iv))
				return nil
			}
			if iv < 0 && iv > -(1<<24) {
				c.emit(bytecode.OpPushNegInt, uint32(-iv))
				return nil
			}
		}
		idx := c.cur().tmpl.AddConst(engine.Number(f))
		c.emit(bytecode.OpPushConst, uint32(idx))
		return nil
	case *ast.BigIntLit:
		// BigInt 字面量：解析十进制字符串为 *big.Int，放入常量池。
		bi, ok := new(big.Int).SetString(n.Text, 10)
		if !ok {
			return fmt.Errorf("invalid BigInt literal: %s", n.Text)
		}
		idx := c.cur().tmpl.AddConst(engine.BigInt(bi))
		c.emit(bytecode.OpPushConst, uint32(idx))
		return nil
	case *ast.StringLit:
		idx := c.cur().tmpl.AddStringConst(n.Value)
		c.emit(bytecode.OpPushConst, uint32(idx))
		return nil
	case *ast.BoolLit:
		if n.Value {
			c.emit(bytecode.OpPushTrue, 0)
		} else {
			c.emit(bytecode.OpPushFalse, 0)
		}
		return nil
	case *ast.NullLit:
		c.emit(bytecode.OpPushNull, 0)
		return nil
	case *ast.UndefinedLit:
		c.emit(bytecode.OpPushUndefined, 0)
		return nil
	case *ast.Identifier:
		return c.compileIdentifier(n)
	case *ast.ThisExpr:
		// `this` 按词法解析 `__this__`：普通函数声明为 local slot 0；
		// 箭头函数未声明，经 upvalue 链解析为外层函数的 `this`（P0-2）。
		kind, idx := c.resolve("__this__")
		switch kind {
		case "local":
			c.emit(bytecode.OpLoadLocal, uint32(idx))
		case "upvalue":
			c.emit(bytecode.OpLoadUpvalue, uint32(idx))
		default:
			c.emit(bytecode.OpPushUndefined, 0)
		}
		return nil
	case *ast.NewTargetExpr:
		// `new.target` 按词法解析 `__newTarget__`：非箭头函数为 local 槽位
		// （VM 在 new 调用时填入构造器），箭头函数经 upvalue 链继承外层。
		kind, idx := c.resolve("__newTarget__")
		switch kind {
		case "local", "upvalue":
			if kind == "local" {
				c.emit(bytecode.OpLoadLocal, uint32(idx))
			} else {
				c.emit(bytecode.OpLoadUpvalue, uint32(idx))
			}
		default:
			c.emit(bytecode.OpPushUndefined, 0)
		}
		return nil
	case *ast.ArrayLit:
		// Fast path: no spread → use OpNewArray.
		hasSpread := false
		for _, el := range n.Elements {
			if _, ok := el.(*ast.SpreadElement); ok {
				hasSpread = true
				break
			}
		}
		if !hasSpread {
			for _, el := range n.Elements {
				if el == nil {
					// Preserve sparse-array position and length. The current array
					// representation stores holes as undefined values.
					c.emit(bytecode.OpPushUndefined, 0)
					continue
				}
				if err := c.compileExpr(el); err != nil {
					return err
				}
			}
			c.emit(bytecode.OpNewArray, uint32(len(n.Elements)))
			return nil
		}
		// Slow path: build incrementally with spread support.
		c.emit(bytecode.OpBuildArray, 0)
		for _, el := range n.Elements {
			if el == nil {
				c.emit(bytecode.OpPushUndefined, 0)
				c.emit(bytecode.OpArrayPush, 0)
				continue
			}
			if sp, ok := el.(*ast.SpreadElement); ok {
				if err := c.compileExpr(sp.Arg); err != nil {
					return err
				}
				c.emit(bytecode.OpArraySpread, 0)
			} else {
				if err := c.compileExpr(el); err != nil {
					return err
				}
				c.emit(bytecode.OpArrayPush, 0)
			}
		}
		return nil
	case *ast.ObjectLit:
		if isSimpleObjectLiteral(n) {
			for _, prop := range n.Properties {
				keyIdx := c.cur().tmpl.AddStringConst(propKey(prop.Key))
				c.emit(bytecode.OpPushConst, uint32(keyIdx))
				if err := c.compileExpr(prop.Value); err != nil {
					return err
				}
			}
			c.emit(bytecode.OpNewObject, uint32(len(n.Properties)))
			return nil
		}
		c.emit(bytecode.OpNewObject, 0)
		for _, prop := range n.Properties {
			if prop.Kind == ast.PropertySpread {
				// `{...obj}`: copy own enumerable props into the target.
				if err := c.compileExpr(prop.Value); err != nil {
					return err
				}
				c.emit(bytecode.OpSpreadObject, 0)
				continue
			}
			if prop.Computed {
				// { [expr]: value } — evaluate key at runtime.
				if err := c.compileExpr(prop.Key); err != nil {
					return err
				}
				if err := c.compileExpr(prop.Value); err != nil {
					return err
				}
				c.emit(bytecode.OpSetPropComputedObj, 0)
				continue
			}
			// get/set 访问器：把函数注册为对象上的 accessor。
			if prop.Kind == ast.PropertyGet || prop.Kind == ast.PropertySet {
				if err := c.compileExpr(prop.Value); err != nil {
					return err
				}
				key := propKey(prop.Key)
				nameIdx := c.cur().tmpl.AddStringConst(key)
				if prop.Kind == ast.PropertyGet {
					c.emit(bytecode.OpSetGetterObj, uint32(nameIdx))
				} else {
					c.emit(bytecode.OpSetSetterObj, uint32(nameIdx))
				}
				continue
			}
			if err := c.compileExpr(prop.Value); err != nil {
				return err
			}
			key := propKey(prop.Key)
			nameIdx := c.cur().tmpl.AddStringConst(key)
			c.emit(bytecode.OpSetPropObj, uint32(nameIdx))
		}
		return nil
	case *ast.BinaryExpr:
		return c.compileBinary(n)
	case *ast.LogicalExpr:
		return c.compileLogical(n)
	case *ast.UnaryExpr:
		return c.compileUnary(n)
	case *ast.UpdateExpr:
		return c.compileUpdate(n)
	case *ast.AssignExpr:
		return c.compileAssign(n)
	case *ast.CallExpr:
		return c.compileCall(n)
	case *ast.NewExpr:
		return c.compileNew(n)
	case *ast.MemberExpr:
		return c.compileMember(n)
	case *ast.FunctionExpr:
		// 具名函数表达式（NFE）：名字在函数体内绑定为不可变自引用。
		return c.compileFunction(funcNameFromExpr(n), n.Params, n.ParamPatterns, n.Defaults, n.RestParam, n.Body, n.IsAsync, n.IsGenerator, false, n.Name != nil)
	case *ast.ArrowFunc:
		return c.compileFunction("", n.Params, n.ParamPatterns, n.Defaults, n.RestParam, n.Body, n.IsAsync, false, true, false)
	case *ast.ClassExpr:
		return c.compileClassExpr(n)
	case *ast.ConditionalExpr:
		return c.compileConditional(n)
	case *ast.SequenceExpr:
		for i, e := range n.Expressions {
			if err := c.compileExpr(e); err != nil {
				return err
			}
			if i < len(n.Expressions)-1 {
				c.emit(bytecode.OpPop, 0)
			}
		}
		return nil
	case *ast.RegexLit:
		// 正则字面量：压入 pattern 与 flags，OpMakeRegexp 构造 RegExp 实例。
		patIdx := c.cur().tmpl.AddStringConst(n.Pattern)
		flagsIdx := c.cur().tmpl.AddStringConst(n.Flags)
		c.emit(bytecode.OpPushConst, uint32(patIdx))
		c.emit(bytecode.OpPushConst, uint32(flagsIdx))
		c.emit(bytecode.OpMakeRegexp, 0)
		return nil
	case *ast.TemplateLit:
		return c.compileTemplateLit(n)
	case *ast.TaggedTemplateExpr:
		return c.compileTaggedTemplate(n)
	case *ast.YieldExpr:
		return c.compileYield(n)
	case *ast.AwaitExpr:
		return c.compileAwait(n)
	}
	return fmt.Errorf("unsupported expression %T", e)
}

// isSimpleObjectLiteral reports whether a literal can be built in one batch.
// Computed keys, spread, and accessors need the partially-built object and keep
// using the incremental instruction sequence.
func isSimpleObjectLiteral(n *ast.ObjectLit) bool {
	for _, prop := range n.Properties {
		if prop.Computed || prop.Kind == ast.PropertySpread ||
			prop.Kind == ast.PropertyGet || prop.Kind == ast.PropertySet {
			return false
		}
	}
	return true
}

// compileTaggedTemplate compiles a tagged template `tag`a${x}b“.
// 栈布局（复用现有指令，无需新 opcode）：
//
//	非成员 tag：compile tag → [tag]
//	成员 tag obj.tag：compile obj → [obj]（OpCallMethod 内部取方法并绑定 this=obj）
//	计算成员 tag obj[k]：OpDup/GetElem/Swap → [method, obj]
//	再压 cooked quasis + OpNewArray → [..., stringsArr]
//	压 raw quasis + OpNewArray + OpSetPropObj("raw") → [..., stringsArr]
//	编译插值表达式 → [..., stringsArr, e1..eM]
//	OpCall/OpCallMethod/OpCallWithThis（参数数 = 1+插值数）
func (c *Compiler) compileTaggedTemplate(n *ast.TaggedTemplateExpr) error {
	tmpl := n.Template
	switch m := n.Tag.(type) {
	case *ast.MemberExpr:
		if m.Computed {
			// [obj] → OpDup → [obj, obj] → compile key → [obj, obj, key]
			// → OpGetElem → [obj, method] → OpSwap → [method, obj(this)]
			if err := c.compileExpr(m.Object); err != nil {
				return err
			}
			c.emit(bytecode.OpDup, 0)
			if err := c.compileExpr(m.Property); err != nil {
				return err
			}
			c.emit(bytecode.OpGetElem, 0)
			c.emit(bytecode.OpSwap, 0)
		} else {
			if err := c.compileExpr(m.Object); err != nil {
				return err
			}
		}
	default:
		if err := c.compileExpr(n.Tag); err != nil {
			return err
		}
	}
	// TemplateStringsArray：cooked quasis。
	for _, q := range tmpl.Quasis {
		idx := c.cur().tmpl.AddStringConst(q)
		c.emit(bytecode.OpPushConst, uint32(idx))
	}
	c.emit(bytecode.OpNewArray, uint32(len(tmpl.Quasis)))
	// strings.raw 数组：raw quasis。
	for _, rq := range tmpl.RawQuasis {
		idx := c.cur().tmpl.AddStringConst(rq)
		c.emit(bytecode.OpPushConst, uint32(idx))
	}
	c.emit(bytecode.OpNewArray, uint32(len(tmpl.RawQuasis)))
	rawIdx := c.cur().tmpl.AddStringConst("raw")
	c.emit(bytecode.OpSetPropObj, uint32(rawIdx))
	// 插值表达式。
	for _, e := range tmpl.Expressions {
		if err := c.compileExpr(e); err != nil {
			return err
		}
	}
	numArgs := uint32(1 + len(tmpl.Expressions))
	switch m := n.Tag.(type) {
	case *ast.MemberExpr:
		if m.Computed {
			c.emit(bytecode.OpCallWithThis, numArgs)
		} else {
			nameIdx := c.cur().tmpl.AddStringConst(m.Property.(*ast.Identifier).Name)
			c.emit(bytecode.OpCallMethod, numArgs<<16|uint32(nameIdx&0xFFFF))
		}
	default:
		c.emit(bytecode.OpCall, numArgs)
	}
	return nil
}

// compileTemplateLit compiles a template literal by concatenating string
// quasis with interpolated expression values using the binary + operator.
// `Hello, ${name}!` compiles as: "" + "Hello, " + name + "!" (with the
// leading empty quasi when the template starts with ${).
func (c *Compiler) compileTemplateLit(n *ast.TemplateLit) error {
	// Start with the first quasi (may be empty).
	idx := c.cur().tmpl.AddStringConst(n.Quasis[0])
	c.emit(bytecode.OpPushConst, uint32(idx))
	// Interleave: expression + next quasi.
	for i, expr := range n.Expressions {
		if err := c.compileExpr(expr); err != nil {
			return err
		}
		c.emit(bytecode.OpAdd, 0)
		quasiIdx := c.cur().tmpl.AddStringConst(n.Quasis[i+1])
		c.emit(bytecode.OpPushConst, uint32(quasiIdx))
		c.emit(bytecode.OpAdd, 0)
	}
	return nil
}

// compileYield compiles a `yield` / `yield*` expression.
//
//	yield        -> push undefined; OpYield
//	yield expr   -> compile expr; OpYield
//	yield* expr  -> compile expr; OpGetIterator; loop { next(); if done push
//	               value & break; else OpYield (discard sent value) }
//
// OpYield pops the yielded value and suspends; on resume it pushes the value
// passed to .next(v), which becomes the result of the yield expression.
func (c *Compiler) compileYield(n *ast.YieldExpr) error {
	if n.Delegate {
		return c.compileYieldStar(n)
	}
	if n.Argument == nil {
		c.emit(bytecode.OpPushUndefined, 0)
	} else {
		if err := c.compileExpr(n.Argument); err != nil {
			return err
		}
	}
	c.emit(bytecode.OpYield, 0)
	return nil
}

// compileYieldStar compiles `yield* expr`: iterate the iterable produced by
// expr and yield each value in turn. The result of the yield* expression is
// the iterator's final {value} (when done becomes true).
func (c *Compiler) compileYieldStar(n *ast.YieldExpr) error {
	c.pushBlock()
	defer c.popBlock()

	// Evaluate the iterable and obtain its iterator.
	if err := c.compileExpr(n.Argument); err != nil {
		return err
	}
	c.emit(bytecode.OpGetIterator, 0)
	tmpIter := c.declareLocal("__yield_star_iter__")
	c.emit(bytecode.OpStoreLocal, uint32(tmpIter))

	tmpResult := c.declareLocal("__yield_star_result__")

	nameNext := c.cur().tmpl.AddStringConst("next")
	nameDone := c.cur().tmpl.AddStringConst("done")
	nameValue := c.cur().tmpl.AddStringConst("value")

	loopStart := c.curPC()

	// iter.next()
	c.emit(bytecode.OpLoadLocal, uint32(tmpIter))
	c.emit(bytecode.OpCallMethod, uint32(nameNext)) // 0 args
	c.emit(bytecode.OpStoreLocal, uint32(tmpResult))

	// if result.done -> push result.value and exit loop.
	c.emit(bytecode.OpLoadLocal, uint32(tmpResult))
	c.emit(bytecode.OpGetProp, uint32(nameDone))
	jExit := c.emit(bytecode.OpJmpTruePop, 0)

	c.pushLoop(loopStart, 0)
	defer c.popLoop()

	// yield result.value (discard the value sent back by .next()).
	c.emit(bytecode.OpLoadLocal, uint32(tmpResult))
	c.emit(bytecode.OpGetProp, uint32(nameValue))
	c.emit(bytecode.OpYield, 0)
	c.emit(bytecode.OpPop, 0) // discard sent value

	// continue -> jump back to loopStart.
	continuePC := c.curPC()
	c.topLoop().continueTarget = continuePC
	c.patchLoopContinues(continuePC)
	c.emit(bytecode.OpJmp, uint32(loopStart-(c.curPC()+bytecode.InstrSize)))

	// exit: push the final value as the result of yield*.
	exit := c.curPC()
	c.patchJumpToHere(jExit)
	c.emit(bytecode.OpLoadLocal, uint32(tmpResult))
	c.emit(bytecode.OpGetProp, uint32(nameValue))
	c.topLoop().breakTarget = exit
	c.patchLoopBreaks(exit)
	return nil
}

// compileAwait compiles `await expr`: evaluate the argument, then emit OpAwait
// which suspends the async function until the (promise-wrapped) value settles.
// On resume, OpAwait pushes the resolved value (or throws the rejection).
func (c *Compiler) compileAwait(n *ast.AwaitExpr) error {
	if n.Argument == nil {
		c.emit(bytecode.OpPushUndefined, 0)
	} else {
		if err := c.compileExpr(n.Argument); err != nil {
			return err
		}
	}
	c.emit(bytecode.OpAwait, 0)
	return nil
}

func (c *Compiler) compileIdentifier(n *ast.Identifier) error {
	kind, idx := c.resolve(n.Name)
	switch kind {
	case "local":
		c.emit(bytecode.OpLoadLocal, uint32(idx))
	case "upvalue":
		c.emit(bytecode.OpLoadUpvalue, uint32(idx))
	case "global":
		c.emit(bytecode.OpLoadGlobal, uint32(idx))
	}
	return nil
}

func (c *Compiler) compileBinary(n *ast.BinaryExpr) error {
	if err := c.compileExpr(n.Left); err != nil {
		return err
	}
	if err := c.compileExpr(n.Right); err != nil {
		return err
	}
	op, err := binaryOpcode(n.Op)
	if err != nil {
		return err
	}
	c.emit(op, 0)
	return nil
}

func binaryOpcode(op string) (bytecode.Opcode, error) {
	switch op {
	case "+":
		return bytecode.OpAdd, nil
	case "-":
		return bytecode.OpSub, nil
	case "*":
		return bytecode.OpMul, nil
	case "/":
		return bytecode.OpDiv, nil
	case "%":
		return bytecode.OpMod, nil
	case "**":
		return bytecode.OpPow, nil
	case "&":
		return bytecode.OpBitAnd, nil
	case "|":
		return bytecode.OpBitOr, nil
	case "^":
		return bytecode.OpBitXor, nil
	case "<<":
		return bytecode.OpShl, nil
	case ">>":
		return bytecode.OpShr, nil
	case ">>>":
		return bytecode.OpUShr, nil
	case "==":
		return bytecode.OpEq, nil
	case "!=":
		return bytecode.OpNe, nil
	case "===":
		return bytecode.OpStrictEq, nil
	case "!==":
		return bytecode.OpStrictNe, nil
	case "<":
		return bytecode.OpLt, nil
	case "<=":
		return bytecode.OpLe, nil
	case ">":
		return bytecode.OpGt, nil
	case ">=":
		return bytecode.OpGe, nil
	case "instanceof":
		return bytecode.OpInstanceof, nil
	case "in":
		return bytecode.OpIn, nil
	}
	return 0, fmt.Errorf("unsupported binary op %s", op)
}

func (c *Compiler) compileLogical(n *ast.LogicalExpr) error {
	if err := c.compileExpr(n.Left); err != nil {
		return err
	}
	switch n.Op {
	case "&&":
		j := c.emit(bytecode.OpJmpFalseKeep, 0)
		if err := c.compileExpr(n.Right); err != nil {
			return err
		}
		c.patchJumpToHere(j)
	case "||":
		j := c.emit(bytecode.OpJmpTrueKeep, 0)
		if err := c.compileExpr(n.Right); err != nil {
			return err
		}
		c.patchJumpToHere(j)
	case "??":
		j := c.emit(bytecode.OpJmpNullishKeep, 0)
		if err := c.compileExpr(n.Right); err != nil {
			return err
		}
		c.patchJumpToHere(j)
	default:
		return fmt.Errorf("unsupported logical op %s", n.Op)
	}
	return nil
}

func (c *Compiler) compileUnary(n *ast.UnaryExpr) error {
	if n.Op == "typeof" {
		if id, ok := n.Arg.(*ast.Identifier); ok {
			kind, _ := c.resolve(id.Name)
			if kind == "global" {
				nameIdx := c.cur().tmpl.AddStringConst(id.Name)
				c.emit(bytecode.OpTypeofGlobal, uint32(nameIdx))
				return nil
			}
		}
	}
	if n.Op == "delete" {
		// delete obj.prop  (non-computed member access)
		if member, ok := n.Arg.(*ast.MemberExpr); ok && !member.Computed {
			if err := c.compileExpr(member.Object); err != nil {
				return err
			}
			propName := ""
			if id, ok := member.Property.(*ast.Identifier); ok {
				propName = id.Name
			} else if s, ok := member.Property.(*ast.StringLit); ok {
				propName = s.Value
			}
			nameIdx := c.cur().tmpl.AddStringConst(propName)
			c.emit(bytecode.OpDelProp, uint32(nameIdx))
			return nil
		}
		// Fallback: evaluate and discard, return true.
		if err := c.compileExpr(n.Arg); err != nil {
			return err
		}
		c.emit(bytecode.OpPop, 0)
		c.emit(bytecode.OpPushTrue, 0)
		return nil
	}
	if err := c.compileExpr(n.Arg); err != nil {
		return err
	}
	switch n.Op {
	case "!":
		c.emit(bytecode.OpNot, 0)
	case "-":
		c.emit(bytecode.OpNeg, 0)
	case "+":
		c.emit(bytecode.OpUnaryPlus, 0)
	case "typeof":
		c.emit(bytecode.OpTypeof, 0)
	case "void":
		c.emit(bytecode.OpPop, 0)
		c.emit(bytecode.OpPushUndefined, 0)
	case "~":
		c.emit(bytecode.OpBitNot, 0)
	default:
		return fmt.Errorf("unsupported unary op %s", n.Op)
	}
	return nil
}

func (c *Compiler) compileUpdate(n *ast.UpdateExpr) error {
	// Get current value
	if err := c.compileExpr(n.Arg); err != nil {
		return err
	}
	if !n.Prefix {
		// postfix: we need the OLD value as the result. Dup it.
		c.emit(bytecode.OpDup, 0)
	}
	// Compute new value. OpInc/OpDec pick the successor/predecessor at
	// runtime, preserving the operand type: BigInt stays BigInt (x++ adds
	// 1n, per ES), Number stays Number. A plain `x + 1` must still throw on
	// BigInt, so this cannot reuse OpAdd/OpSub with an inline 1.
	if n.Op == "++" {
		c.emit(bytecode.OpInc, 0)
	} else {
		c.emit(bytecode.OpDec, 0)
	}
	// prefix 需要把"新值"作为表达式结果留在栈上，赋值前 Dup 一份。
	if n.Prefix {
		c.emit(bytecode.OpDup, 0)
	}
	// Store back (consumes the new value).
	if err := c.assignTo(n.Arg); err != nil {
		return err
	}
	// For postfix, the old value remains on the stack (we Dup'd it).
	return nil
}

func (c *Compiler) compileAssign(n *ast.AssignExpr) error {
	if n.Op == "=" {
		if err := c.compileExpr(n.Right); err != nil {
			return err
		}
		c.emit(bytecode.OpDup, 0)
		return c.assignTo(n.Left)
	}
	// 逻辑赋值运算符（ES2021）：||= / &&= / ??= 有短路语义。
	// 左值只求值一次：读取 → 短路判断 → 条件成立才编译右值并赋值。
	// 栈布局：[leftVal] → (跳过时保留) / (赋值时为 [rightVal]，赋值后保留)
	switch n.Op {
	case "||=":
		return c.compileLogicalAssign(n.Left, n.Right, bytecode.OpJmpTrueKeep)
	case "&&=":
		return c.compileLogicalAssign(n.Left, n.Right, bytecode.OpJmpFalseKeep)
	case "??=":
		return c.compileLogicalAssign(n.Left, n.Right, bytecode.OpJmpNullishKeep)
	}
	// compound: desugar a OP= b → a = a OP b
	leftExpr, ok := n.Left.(ast.Expression)
	if !ok {
		// 解构模式不能作为复合赋值左值（JS 语法错误）。
		return fmt.Errorf("invalid compound assignment target %T", n.Left)
	}
	if err := c.compileExpr(leftExpr); err != nil {
		return err
	}
	if err := c.compileExpr(n.Right); err != nil {
		return err
	}
	op, err := binaryOpcode(stripAssignSuffix(n.Op))
	if err != nil {
		return err
	}
	c.emit(op, 0)
	c.emit(bytecode.OpDup, 0)
	return c.assignTo(n.Left)
}

// compileLogicalAssign 编译逻辑赋值（||= / &&= / ??=）。
// jmpOp 决定短路条件：OpJmpTrueKeep（||=，truthy 跳过）、OpJmpFalseKeep（&&=，falsy 跳过）、
// OpJmpNullishKeep（??=，null/undefined 跳过）。
//
// 这些 Keep 跳转指令的语义：满足条件时跳过并保留栈顶值；不满足时 pop 栈顶值。
// 字节码序列：
//
//	compileExpr(left)          // push 当前左值
//	OpJmpTrueKeep end          // 满足短路条件 → 保留 left 跳到 end；否则 pop left
//	compileExpr(right)         // push 右值（left 已被跳转指令 pop）
//	OpDup                      // 复制（一份赋值，一份作为结果保留）
//	assignTo(left)             // 赋值给左值
//	end:                       // 栈顶为结果值（left 或 right）
func (c *Compiler) compileLogicalAssign(left ast.Node, right ast.Expression, jmpOp bytecode.Opcode) error {
	if _, ok := left.(*ast.ObjectPattern); ok {
		// 解构模式不能作为逻辑赋值左值（JS 语法错误）。
		return fmt.Errorf("invalid logical assignment target %T", left)
	}
	if _, ok := left.(*ast.ArrayPattern); ok {
		// 解构模式不能作为逻辑赋值左值（JS 语法错误）。
		return fmt.Errorf("invalid logical assignment target %T", left)
	}
	leftExpr, ok := left.(ast.Expression)
	if !ok {
		return fmt.Errorf("invalid logical assignment target %T", left)
	}
	if err := c.compileExpr(leftExpr); err != nil {
		return err
	}
	jumpSkip := c.emit(jmpOp, 0)
	// 不满足短路条件：跳转指令已 pop 左值，直接编译右值并赋值。
	if err := c.compileExpr(right); err != nil {
		return err
	}
	c.emit(bytecode.OpDup, 0)
	if err := c.assignTo(left); err != nil {
		return err
	}
	c.patchJumpToHere(jumpSkip)
	return nil
}

func stripAssignSuffix(op string) string {
	if len(op) > 1 && op[len(op)-1] == '=' {
		return op[:len(op)-1]
	}
	return op
}

// assignTo emits a store into the given reference (identifier / member).
// The value to store must already be on top of the stack.
func (c *Compiler) assignTo(ref ast.Node) error {
	switch r := ref.(type) {
	case *ast.Identifier:
		kind, idx := c.resolve(r.Name)
		switch kind {
		case "local":
			c.emit(bytecode.OpStoreLocal, uint32(idx))
		case "upvalue":
			c.emit(bytecode.OpStoreUpvalue, uint32(idx))
		case "global":
			c.emit(bytecode.OpStoreGlobal, uint32(idx))
		}
		return nil
	case *ast.MemberExpr:
		if r.Computed {
			if err := c.compileExpr(r.Object); err != nil {
				return err
			}
			if err := c.compileExpr(r.Property); err != nil {
				return err
			}
			c.emit(bytecode.OpSetElemTop, 0)
		} else {
			key := r.Property.(*ast.Identifier).Name
			nameIdx := c.cur().tmpl.AddStringConst(key)
			if err := c.compileExpr(r.Object); err != nil {
				return err
			}
			c.emit(bytecode.OpSetPropTop, uint32(nameIdx))
		}
		return nil
	case *ast.ObjectPattern, *ast.ArrayPattern:
		// 解构赋值：({a, b} = x) / [a, b] = x。栈顶值为右值，
		// storePattern 会按模式展开存入已有引用并保留右值。
		return c.storePattern(ref)
	}
	return fmt.Errorf("invalid assignment target %T", ref)
}

// storePattern 把栈顶值按模式解构并存储到已有引用（ES2015 解构赋值）。
// 与声明解构（compileBindPattern）不同：不声明新变量，标识符按
// local/upvalue/global 解析，成员表达式走属性存储；值经临时槽展开后
// 重新压回栈顶，保证赋值表达式的结果（右值）保留。
func (c *Compiler) storePattern(p ast.Node) error {
	switch pat := p.(type) {
	case *ast.Identifier:
		return c.assignTo(pat)
	case *ast.MemberExpr:
		return c.assignTo(pat)
	case *ast.ArrayPattern:
		tmp := c.newSlot()
		c.emit(bytecode.OpStoreLocal, uint32(tmp)) // 右值入临时槽
		for i, el := range pat.Elements {
			if el.Target == nil {
				continue // 空洞
			}
			if el.IsRest {
				// rest: src.slice(i)
				c.emit(bytecode.OpLoadLocal, uint32(tmp))
				c.emit(bytecode.OpPushInt, uint32(i))
				sliceNameIdx := c.cur().tmpl.AddStringConst("slice")
				operand := uint32(1)<<16 | uint32(sliceNameIdx&0xFFFF)
				c.emit(bytecode.OpCallMethod, operand)
			} else {
				c.emit(bytecode.OpLoadLocal, uint32(tmp))
				c.emit(bytecode.OpPushInt, uint32(i))
				c.emit(bytecode.OpGetElem, 0)
			}
			if err := c.compileDefaultGuard(el.Default); err != nil {
				return err
			}
			if err := c.storePattern(el.Target); err != nil {
				return err
			}
		}
		c.emit(bytecode.OpLoadLocal, uint32(tmp))
		return nil
	case *ast.ObjectPattern:
		tmp := c.newSlot()
		c.emit(bytecode.OpStoreLocal, uint32(tmp)) // 右值入临时槽
		for propIndex, prop := range pat.Properties {
			if prop.Value == nil {
				return fmt.Errorf("invalid assignment target in destructuring")
			}
			if prop.IsRest {
				// rest: { ...src } 再删除已绑定键
				c.emit(bytecode.OpNewObject, 0)
				c.emit(bytecode.OpLoadLocal, uint32(tmp))
				c.emit(bytecode.OpSpreadObject, 0)
				for _, bound := range pat.Properties[:propIndex] {
					c.emit(bytecode.OpDup, 0)
					if bound.Computed {
						if err := c.compileExpr(bound.Key); err != nil {
							return err
						}
						c.emit(bytecode.OpDelElem, 0)
					} else {
						nameIdx := c.cur().tmpl.AddStringConst(propKey(bound.Key))
						c.emit(bytecode.OpDelProp, uint32(nameIdx))
					}
					c.emit(bytecode.OpPop, 0)
				}
				if err := c.storePattern(prop.Value); err != nil {
					return err
				}
				continue
			}
			// result = src[key]
			c.emit(bytecode.OpLoadLocal, uint32(tmp))
			if prop.Computed {
				if err := c.compileExpr(prop.Key); err != nil {
					return err
				}
				c.emit(bytecode.OpGetElem, 0)
			} else {
				nameIdx := c.cur().tmpl.AddStringConst(propKey(prop.Key))
				c.emit(bytecode.OpGetProp, uint32(nameIdx))
			}
			if err := c.compileDefaultGuard(prop.Default); err != nil {
				return err
			}
			if err := c.storePattern(prop.Value); err != nil {
				return err
			}
		}
		c.emit(bytecode.OpLoadLocal, uint32(tmp))
		return nil
	}
	return fmt.Errorf("invalid assignment target %T", p)
}

// compileDefaultGuard 编译解构默认值守卫：栈顶值 === undefined 时用
// 默认值替换，否则保持栈顶原值。defaultExpr 为 nil 时无操作。
// 栈序：[v] → (undefined 分支) pop v → push default；
// (非 undefined 分支) 跳过后仍为 [v]。
func (c *Compiler) compileDefaultGuard(defaultExpr ast.Expression) error {
	if defaultExpr == nil {
		return nil
	}
	c.emit(bytecode.OpDup, 0)
	c.emit(bytecode.OpPushUndefined, 0)
	c.emit(bytecode.OpStrictEq, 0)
	jSkip := c.emit(bytecode.OpJmpFalsePop, 0)
	// 走到这里说明 v === undefined：丢弃 v，压入默认值。
	c.emit(bytecode.OpPop, 0)
	if err := c.compileExpr(defaultExpr); err != nil {
		return err
	}
	c.patchJumpToHere(jSkip)
	return nil
}

func (c *Compiler) compileCall(n *ast.CallExpr) error {
	// Detect spread in args to choose fast vs slow path.
	hasSpread := false
	for _, a := range n.Arguments {
		if _, ok := a.(*ast.SpreadElement); ok {
			hasSpread = true
			break
		}
	}

	// super(args) — construct the parent class with this = current slot 0.
	if _, ok := n.Callee.(*ast.SuperExpr); ok {
		c.emitSuperCtor()
		if !hasSpread {
			for _, a := range n.Arguments {
				if err := c.compileExpr(a); err != nil {
					return err
				}
			}
			c.emit(bytecode.OpConstructThis, uint32(len(n.Arguments)))
			return nil
		}
		c.compileArgsArray(n.Arguments)
		c.emit(bytecode.OpConstructThisArgs, 0)
		return nil
	}

	// super.method(args) — call method from parent prototype with this = slot 0.
	if m, ok := n.Callee.(*ast.MemberExpr); ok {
		if _, isSuper := m.Object.(*ast.SuperExpr); isSuper {
			c.emitSuperProto()
			nameIdx := c.cur().tmpl.AddStringConst(m.Property.(*ast.Identifier).Name)
			c.emit(bytecode.OpGetProp, uint32(nameIdx))
			if !hasSpread {
				for _, a := range n.Arguments {
					if err := c.compileExpr(a); err != nil {
						return err
					}
				}
				c.emit(bytecode.OpCallThis, uint32(len(n.Arguments)))
				return nil
			}
			c.compileArgsArray(n.Arguments)
			c.emit(bytecode.OpCallThisArgs, 0)
			return nil
		}
	}

	// === Optional chaining support ===
	// Determine if this CallExpr is the head of an optional chain.
	chainHead := hasOptionalAccess(n) && !c.inOptionalChain()
	if chainHead {
		c.beginOptionalChain()
	}

	// Optional call with MemberExpr callee: a.b?.() or a?.b?.()
	// Need to get the method, check nullish, then call with this=receiver.
	if n.Optional {
		if m, ok := n.Callee.(*ast.MemberExpr); ok && !m.Computed {
			// 1. Compile receiver (may contain inner optional nodes)
			if err := c.compileExpr(m.Object); err != nil {
				return err
			}
			// receiver 压入；m.Object 若是 member（a.b / o?.x?.y），链首已在
			// 最内层计数（member 级联 GET_PROP 净 0），不重复 +1。
			if !optionalExprTracksResult(m.Object) {
				c.optPushValue()
			}
			// 2. If m.Optional, check if receiver is nullish (short-circuit)
			if m.Optional {
				c.emitOptionalJump()
			}
			// 3. Store receiver in temp for `this` binding
			tempSlot := c.newSlot()
			c.emit(bytecode.OpStoreLocal, uint32(tempSlot))
			c.optChainDelta(-1) // receiver 弹入 temp
			// 4. Get method from receiver
			c.emit(bytecode.OpLoadLocal, uint32(tempSlot))
			c.optPushValue() // receiver 重压（短路残留含 this）
			nameIdx := c.cur().tmpl.AddStringConst(m.Property.(*ast.Identifier).Name)
			c.emit(bytecode.OpGetProp, uint32(nameIdx))
			// 5. Check if method is nullish (n.Optional short-circuit)
			c.emitOptionalJump()
			// 6. Call with this=receiver
			c.emit(bytecode.OpLoadLocal, uint32(tempSlot)) // stack: method receiver
			c.optPushValue()                               // receiver 重压（this 绑定）
			if !hasSpread {
				for _, a := range n.Arguments {
					if err := c.compileExpr(a); err != nil {
						return err
					}
					c.optPushValue()
				}
				c.emit(bytecode.OpCallWithThis, uint32(len(n.Arguments)))
				c.optChainDelta(-(len(n.Arguments) + 1))
			} else {
				c.compileArgsArray(n.Arguments)
				c.optPushValue()
				c.emit(bytecode.OpCallWithThisArgs, 0)
				c.optChainDelta(-2)
			}
			if chainHead {
				c.endOptionalChain()
			}
			return nil
		}

		// Optional call with non-MemberExpr callee: a?.()
		if err := c.compileExpr(n.Callee); err != nil {
			return err
		}
		if !optionalExprTracksResult(n.Callee) {
			c.optPushValue() // callee 压入（已计数的子链不重复）
		}
		c.emitOptionalJump()
		if !hasSpread {
			for _, a := range n.Arguments {
				if err := c.compileExpr(a); err != nil {
					return err
				}
				c.optPushValue()
			}
			c.emit(bytecode.OpCall, uint32(len(n.Arguments)))
			c.optChainDelta(-len(n.Arguments))
		} else {
			c.compileArgsArray(n.Arguments)
			c.optPushValue()
			c.emit(bytecode.OpCallArgs, 0)
			c.optChainDelta(-1)
		}
		if chainHead {
			c.endOptionalChain()
		}
		return nil
	}

	// Method call: foo.bar(args) or foo?.bar(args) or foo[expr](args) — keep receiver as `this`.
	if m, ok := n.Callee.(*ast.MemberExpr); ok {
		// 计算成员调用 obj[expr](args)：编译 obj，dup，取 method，swap 使栈为
		// [method, obj(this), args]，用 OpCallWithThis 调用（保留 this 绑定）。
		if m.Computed {
			if err := c.compileExpr(m.Object); err != nil {
				return err
			}
			if !optionalExprTracksResult(m.Object) {
				c.optPushValue() // obj 压入（已计数的子链不重复）
			}
			if m.Optional {
				c.emitOptionalJump()
			}
			c.emit(bytecode.OpDup, 0) // [obj, obj]
			c.optPushValue()          // obj 副本
			if err := c.compileExpr(m.Property); err != nil {
				return err
			}
			c.optPushValue() // key 压入
			// [obj, obj, key]
			c.emit(bytecode.OpGetElem, 0) // [obj, method]
			c.optChainDelta(-1)           // 弹 obj 副本 + key，压 method
			c.emit(bytecode.OpSwap, 0)    // [method, obj(this)]
			if !hasSpread {
				for _, a := range n.Arguments {
					if err := c.compileExpr(a); err != nil {
						return err
					}
					c.optPushValue()
				}
				c.emit(bytecode.OpCallWithThis, uint32(len(n.Arguments)))
				c.optChainDelta(-(len(n.Arguments) + 1))
			} else {
				c.compileArgsArray(n.Arguments)
				c.optPushValue()
				c.emit(bytecode.OpCallWithThisArgs, 0)
				c.optChainDelta(-2)
			}
			if chainHead {
				c.endOptionalChain()
			}
			return nil
		}
		if err := c.compileExpr(m.Object); err != nil {
			return err
		}
		if !optionalExprTracksResult(m.Object) {
			c.optPushValue() // receiver 压入（已计数的子链不重复）
		}
		// If m.Optional, check if receiver is nullish (short-circuit)
		if m.Optional {
			c.emitOptionalJump()
		}
		if !hasSpread {
			// Fast path: args inline.
			for _, a := range n.Arguments {
				if err := c.compileExpr(a); err != nil {
					return err
				}
				c.optPushValue()
			}
			nameIdx := c.cur().tmpl.AddStringConst(m.Property.(*ast.Identifier).Name)
			operand := uint32(len(n.Arguments))<<16 | uint32(nameIdx&0xFFFF)
			c.emit(bytecode.OpCallMethod, operand)
			c.optChainDelta(-len(n.Arguments))
		} else {
			// Slow path: build args array, then OpCallMethodArgs.
			c.compileArgsArray(n.Arguments)
			c.optPushValue()
			nameIdx := c.cur().tmpl.AddStringConst(m.Property.(*ast.Identifier).Name)
			c.emit(bytecode.OpCallMethodArgs, uint32(nameIdx))
			c.optChainDelta(-2)
		}
		if chainHead {
			c.endOptionalChain()
		}
		return nil
	}

	// Regular call: f(args)
	// I-2 内联展开：const 绑定的可内联函数调用（非 optional、非 spread）。
	if !n.Optional && !hasSpread {
		if id, ok := n.Callee.(*ast.Identifier); ok {
			if c.tryInlineCall(id, n.Arguments) {
				if chainHead {
					c.endOptionalChain()
				}
				return nil
			}
		}
	}
	if err := c.compileExpr(n.Callee); err != nil {
		return err
	}
	if !hasSpread {
		for _, a := range n.Arguments {
			if err := c.compileExpr(a); err != nil {
				return err
			}
		}
		c.emit(bytecode.OpCall, uint32(len(n.Arguments)))
	} else {
		c.compileArgsArray(n.Arguments)
		c.emit(bytecode.OpCallArgs, 0)
	}
	if chainHead {
		c.endOptionalChain()
	}
	return nil
}

// compileArgsArray emits code that builds an array of arguments on top of the
// stack, expanding any SpreadElement nodes. Used for spread-call slow paths.
func (c *Compiler) compileArgsArray(args []ast.Expression) {
	c.emit(bytecode.OpBuildArray, 0)
	for _, a := range args {
		if sp, ok := a.(*ast.SpreadElement); ok {
			_ = c.compileExpr(sp.Arg)
			c.emit(bytecode.OpArraySpread, 0)
		} else {
			_ = c.compileExpr(a)
			c.emit(bytecode.OpArrayPush, 0)
		}
	}
}

func (c *Compiler) compileNew(n *ast.NewExpr) error {
	hasSpread := false
	for _, a := range n.Arguments {
		if _, ok := a.(*ast.SpreadElement); ok {
			hasSpread = true
			break
		}
	}
	if err := c.compileExpr(n.Callee); err != nil {
		return err
	}
	if !hasSpread {
		for _, a := range n.Arguments {
			if err := c.compileExpr(a); err != nil {
				return err
			}
		}
		c.emit(bytecode.OpNew, uint32(len(n.Arguments)))
		return nil
	}
	c.compileArgsArray(n.Arguments)
	c.emit(bytecode.OpNewArgs, 0)
	return nil
}

func (c *Compiler) compileMember(n *ast.MemberExpr) error {
	// super.prop — look up on parent prototype.
	if _, isSuper := n.Object.(*ast.SuperExpr); isSuper {
		c.emitSuperProto()
		if n.Computed {
			if err := c.compileExpr(n.Property); err != nil {
				return err
			}
			c.emit(bytecode.OpGetElem, 0)
		} else {
			key := n.Property.(*ast.Identifier).Name
			nameIdx := c.cur().tmpl.AddStringConst(key)
			c.emit(bytecode.OpGetProp, uint32(nameIdx))
		}
		return nil
	}

	// Determine if this is the head of an optional chain. The head is the
	// outermost MemberExpr/CallExpr containing an optional access, compiled
	// from the top down. Nested nodes see inOptionalChain() == true.
	chainHead := hasOptionalAccess(n) && !c.inOptionalChain()
	if chainHead {
		c.beginOptionalChain()
	}

	// O2-D1 superinstruction：`localVar.prop`（非计算、非可选、对象为局部
	// 变量）合并为单条 OpGetPropLocal（slot<<16 | nameIdx），省 1 次
	// dispatch 与压栈/弹栈。
	if !n.Computed && !n.Optional && !chainHead {
		if id, ok := n.Object.(*ast.Identifier); ok {
			if kind, idx := c.resolve(id.Name); kind == "local" {
				nameIdx := c.cur().tmpl.AddStringConst(n.Property.(*ast.Identifier).Name)
				c.emit(bytecode.OpGetPropLocal, uint32(idx)<<16|uint32(nameIdx&0xFFFF))
				return nil
			}
		}
	}

	if err := c.compileExpr(n.Object); err != nil {
		return err
	}
	// 链值压入（对象）：member 以及成员/可选 call 已在子表达式中维护链栈
	// 计数，不重复 +1。否则 `map.get(k)?.prop` 会把调用结果计两次，短路
	// 清理块多发一个 POP，把临时 local 槽误当操作数弹掉。
	if !optionalExprTracksResult(n.Object) {
		c.optPushValue()
	}

	// If this node is optional, emit short-circuit jump after evaluating
	// the object: if nullish, pop + push undefined + jump to chain end.
	if n.Optional {
		c.emitOptionalJump()
	}

	if n.Computed {
		if err := c.compileExpr(n.Property); err != nil {
			return err
		}
		c.optPushValue() // key 压入
		c.emit(bytecode.OpGetElem, 0)
		c.optChainDelta(-1) // 弹对象 + key，压结果
	} else {
		key := n.Property.(*ast.Identifier).Name
		nameIdx := c.cur().tmpl.AddStringConst(key)
		c.emit(bytecode.OpGetProp, uint32(nameIdx))
	}

	if chainHead {
		c.endOptionalChain()
	}
	return nil
}

// optionalExprTracksResult reports whether a child expression already accounts
// for its result in the active optional-chain stack counter.
func optionalExprTracksResult(expr ast.Expression) bool {
	switch n := expr.(type) {
	case *ast.MemberExpr:
		return true
	case *ast.CallExpr:
		if n.Optional {
			return true
		}
		_, isMethodCall := n.Callee.(*ast.MemberExpr)
		return isMethodCall
	case *ast.AwaitExpr:
		return optionalExprTracksResult(n.Argument)
	default:
		return false
	}
}

func (c *Compiler) compileConditional(n *ast.ConditionalExpr) error {
	if err := c.compileExpr(n.Test); err != nil {
		return err
	}
	jFalse := c.emit(bytecode.OpJmpFalsePop, 0)
	if err := c.compileExpr(n.Consequent); err != nil {
		return err
	}
	jEnd := c.emit(bytecode.OpJmp, 0)
	c.patchJumpToHere(jFalse)
	if err := c.compileExpr(n.Alternate); err != nil {
		return err
	}
	c.patchJumpToHere(jEnd)
	return nil
}

// === function compilation =================================================

// compileFunction compiles a function (decl, expr, or arrow) into a
// FuncTemplate and emits OpMakeClosure in the enclosing function.
// `defaults[i]` is the default expression for params[i] (nil = no default).
// `rest` is the ES2015 rest parameter name (or "" if none).
// `isArrow` 为 true 时编译箭头函数：不声明本函数级 `this` 槽位，
// `this` 经 upvalue 链解析为外层函数的 `this`（P0-2）。
func (c *Compiler) compileFunction(name string, params []*ast.Identifier, patterns []ast.Pattern, defaults []ast.Expression, rest *ast.Identifier, body ast.Node, isAsync, isGenerator, isArrow bool, bindSelf bool) error {
	restoreControlFlow := c.isolateControlFlow()
	defer restoreControlFlow()

	// I-1：简单函数体判定（单表达式或单条 `return expr;`），供可内联标记。
	simpleBody := false
	switch b := body.(type) {
	case ast.Expression:
		simpleBody = true
	case *ast.BlockStmt:
		if len(b.Body) == 1 {
			if rs, ok := b.Body[0].(*ast.ReturnStmt); ok && rs.Arg != nil {
				simpleBody = true
			}
		}
	}

	// 隔离外层可选链：嵌套函数编译时 c.cur() 切换到本函数字节码缓冲区，
	// 若内层链的 OpOptionalJump 记入外层链，外层 endOptionalChain 会用
	// 错误缓冲区 patch（PatchOperand out of range panic）。
	savedOptionalStack := c.optionalChainStack
	c.optionalChainStack = nil
	savedOptionalResiduals := c.optionalChainResiduals
	c.optionalChainResiduals = nil
	savedChainPushCount := c.optChainPushCount
	savedChainPushSaved := c.optChainPushSaved
	savedChainPushActive := c.optChainPushActive
	c.optChainPushCount = 0
	c.optChainPushSaved = nil
	c.optChainPushActive = false
	defer func() {
		c.optionalChainStack = savedOptionalStack
		c.optionalChainResiduals = savedOptionalResiduals
		c.optChainPushCount = savedChainPushCount
		c.optChainPushSaved = savedChainPushSaved
		c.optChainPushActive = savedChainPushActive
	}()

	// 普通函数：`this` slot = 0；params = slots 1..N；rest 参数 = slot N+1。
	// 箭头函数：无 own `this`（slot 0 仍保留以兼容 frame 布局，但不会被引用）。
	numLocals := 1 + len(params)
	if rest != nil {
		numLocals++
	}
	tmpl := &bytecode.FuncTemplate{
		Name:          name,
		NumParams:     len(params),
		NumLocals:     numLocals,
		IsVarArgs:     rest != nil,
		IsAsync:       isAsync,
		IsGenerator:   isGenerator,
		IsArrow:       isArrow,
		ArgumentsSlot: -1,
		NFESlot:       -1,
		SourceFile:    c.cur().tmpl.SourceFile,
	}
	funcIdx := c.module.AddFunction(tmpl)
	c.lastFuncExprIdx = funcIdx // I-2 const 绑定登记用（最近编译的函数模板）

	fc := &funcCtx{
		tmpl:             tmpl,
		upvalueIndex:     make(map[string]int),
		inlineCandidates: map[string]int{},
	}
	fc.scopes = []*scope{{decls: make(map[string]int), isFunc: true}}
	if !isArrow {
		fc.scopes[0].decls["__this__"] = 0
	}
	for i, p := range params {
		if p != nil {
			fc.scopes[0].decls[p.Name] = i + 1
		}
	}
	// 非箭头函数均绑定 `arguments` 对象（slot = numLocals，紧随 this/params/rest）。
	// 箭头函数不绑定 own arguments（词法继承外层），与 JS 语义一致。
	if rest != nil {
		fc.scopes[0].decls[rest.Name] = 1 + len(params)
	}
	if bindSelf && astBodyReferencesName(body, name) {
		// 具名函数表达式（NFE）：仅当函数体内实际引用名字时分配自引用槽
		// （运行时帧建立时写入闭包自身）。未引用的带名函数表达式
		// （如 `add1 = function add1(x) {...}`）不分配，避免 JIT 误拒绝。
		nfeSlot := tmpl.NumLocals
		tmpl.NFESlot = nfeSlot
		tmpl.NumLocals++
		fc.scopes[0].decls[name] = nfeSlot
	}
	if !isArrow {
		argsSlot := tmpl.NumLocals
		tmpl.ArgumentsSlot = argsSlot
		tmpl.NumLocals++
		fc.scopes[0].decls["arguments"] = argsSlot
		// new.target 槽位：非箭头函数分配；箭头函数不声明，
		// 经 upvalue 链词法解析到外层函数（与 `this` 同机制）。
		ntSlot := tmpl.NumLocals
		tmpl.NewTargetSlot = ntSlot
		tmpl.NumLocals++
		fc.scopes[0].decls["__newTarget__"] = ntSlot
	}
	c.funcStack = append(c.funcStack, fc)

	// O-6 简单回调检测：纯箭头函数（非 async/非 generator、无默认值/rest）
	// 体为单表达式时生成 NativeCallback 描述（数组高阶方法 Go 侧直执行）。
	// 必须在 body 编译前调用（向常量池追加字面量/属性名索引）。
	// 注意：parser 对多参数箭头会填 nil 条目的 ParamPatterns/Defaults 数组
	// （单参数时才是空数组），必须按“是否存在非 nil 条目”判断，而非 len==0。
	if isArrow && !isAsync && !isGenerator && rest == nil &&
		!hasNonNilPatterns(patterns) && !hasNonNilDefaults(defaults) {
		tmpl.NativeCallback = c.analyzeSimpleCallback(params, body)
	}

	// Emit default-parameter initialization at function entry. For each param
	// with a default expression, if the bound argument is `undefined`, evaluate
	// the default and store it. (JS triggers defaults on === undefined, not on
	// falsy, so we use a strict equality check against undefined.)
	for i, def := range defaults {
		if def == nil {
			continue
		}
		slot := i + 1
		c.emit(bytecode.OpLoadLocal, uint32(slot))
		c.emit(bytecode.OpPushUndefined, 0)
		c.emit(bytecode.OpStrictEq, 0) // pushes true if param === undefined
		jSkip := c.emit(bytecode.OpJmpFalsePop, 0)
		if err := c.compileExpr(def); err != nil {
			return err
		}
		c.emit(bytecode.OpStoreLocal, uint32(slot))
		c.patchJumpToHere(jSkip)
	}

	// 解构参数（({a, b}, [x]) => ...）：参数 slot 已填充，生成绑定指令。
	// 模式绑定名经 compileBindPattern 的 declareLocal 声明为局部变量。
	for i, pat := range patterns {
		if pat == nil {
			continue
		}
		if err := c.compileBindPattern(pat, i+1, "let"); err != nil {
			c.funcStack = c.funcStack[:len(c.funcStack)-1]
			return err
		}
	}

	bodyErr := func() error {
		switch b := body.(type) {
		case *ast.BlockStmt:
			c.hoistFunc(b)
			// 函数声明提升：在 body 开头依次编译所有 FunctionDecl，
			// 使其名字在后续语句执行前已绑定到函数对象（JS 语义）。
			c.hoistFunctionDecls(b.Body)
			return c.compileStmts(b.Body)
		case ast.Expression:
			if err := c.compileExpr(b); err != nil {
				return err
			}
			c.emit(bytecode.OpReturn, 0)
		}
		return nil
	}()
	if bodyErr != nil {
		c.funcStack = c.funcStack[:len(c.funcStack)-1]
		return bodyErr
	}
	c.emit(bytecode.OpReturnUndef, 0)
	fc = c.cur()
	if !fc.usedArguments {
		fc.tmpl.NoArgumentsObject = true
	}
	// I-1 可内联判定：纯箭头函数（非 async/generator/rest/默认值/解构、
	// 参数 ≤ 8、体为单表达式）、编译体后无闭包捕获（upvalueIndex 为空——
	// 箭头函数的 this/arguments 只能经 upvalue 引用，空即未引用）、未引用
	// own arguments、指令在白名单内（复制时可安全重映射）。仅作标记，调用
	// 点展开见 compileCall；未展开走正常调用。
	if isArrow && !isAsync && !isGenerator && rest == nil &&
		!hasNonNilDefaults(defaults) && !hasNonNilPatterns(patterns) &&
		len(params) <= 8 && simpleBody && len(fc.upvalueIndex) == 0 && !fc.usedArguments &&
		isInlinableCode(fc.tmpl.Code) {
		fc.tmpl.Inlinable = true
	}
	c.funcStack = c.funcStack[:len(c.funcStack)-1]

	// I-2：恢复 lastFuncExprIdx 为本函数模板索引。const 绑定的内联候选登记
	// 在 compileExpr(init) 返回后读取该字段；若函数体含嵌套函数表达式，
	// 嵌套编译会把字段覆盖为内层模板索引，导致外层 const 登记到错误模板
	// （调用点错误内联内层函数体）。
	c.lastFuncExprIdx = funcIdx

	// Emit OpMakeClosure in the enclosing function.
	c.emit(bytecode.OpMakeClosure, uint32(funcIdx))
	return nil
}

// hoistFunc pre-declares var/function declarations in the function scope.
func (c *Compiler) hoistFunc(body *ast.BlockStmt) {
	// 预声明函数体顶层的 let/const 绑定（P0-1 配套修复）：否则嵌套函数
	// 在编译时引用后续声明的 let/const（如 `function F(){return f;} const f=...`）
	// 会因尚未声明而被 resolve 为全局，导致闭包捕获失败（undefined）。
	for _, s := range body.Body {
		if vd, ok := s.(*ast.VarDecl); ok && (vd.Kind == "let" || vd.Kind == "const") {
			c.hoistLetConst(vd.Decls)
		}
	}
	var walk func(stmts []ast.Statement)
	walk = func(stmts []ast.Statement) {
		for _, s := range stmts {
			switch st := s.(type) {
			case *ast.VarDecl:
				if st.Kind == "var" {
					c.hoistVarDeclarators(st.Decls)
				}
			case *ast.FunctionDecl:
				if st.Name != nil {
					c.declareVar(st.Name.Name)
				}
			case *ast.ClassDecl:
				// class 声明同样需预先占槽（与 compileClassDecl 的 declareVar
				// 一致），否则被提升到顶部的函数声明闭包会把类名解析为全局
				// （undefined），导致 `function f(){ return Foo; }` 取到 undefined。
				if st.Name != nil {
					c.declareVar(st.Name.Name)
				}
			case *ast.BlockStmt:
				walk(st.Body)
			case *ast.IfStmt:
				walk([]ast.Statement{st.Consequent})
				if st.Alternate != nil {
					walk([]ast.Statement{st.Alternate})
				}
			case *ast.ForStmt:
				if vd, ok := st.Init.(*ast.VarDecl); ok && vd.Kind == "var" {
					c.hoistVarDeclarators(vd.Decls)
				}
				if st.Body != nil {
					walk([]ast.Statement{st.Body})
				}
			case *ast.WhileStmt:
				if st.Body != nil {
					walk([]ast.Statement{st.Body})
				}
			case *ast.DoWhileStmt:
				if st.Body != nil {
					walk([]ast.Statement{st.Body})
				}
			case *ast.ForInStmt:
				if vd, ok := st.Left.(*ast.VarDecl); ok && vd.Kind == "var" {
					c.hoistVarDeclarators(vd.Decls)
				}
				if st.Body != nil {
					walk([]ast.Statement{st.Body})
				}
			case *ast.ForOfStmt:
				if vd, ok := st.Left.(*ast.VarDecl); ok && vd.Kind == "var" {
					c.hoistVarDeclarators(vd.Decls)
				}
				if st.Body != nil {
					walk([]ast.Statement{st.Body})
				}
			case *ast.TryStmt:
				walk(st.Block.Body)
				if st.Handler != nil && st.Handler.Body != nil {
					walk(st.Handler.Body.Body)
				}
				if st.Finally != nil {
					walk(st.Finally.Body)
				}
			case *ast.SwitchStmt:
				for _, cs := range st.Cases {
					walk(cs.Consequent)
				}
			}
		}
	}
	walk(body.Body)
}

// hoistFunctionDecls 在当前作用域开头编译所有顶层函数声明（提升语义）。
// 之后 compileFunctionDecl 遇到这些声明时跳过（避免重复编译）。
// 嵌套块级作用域内的函数声明不在此处理（由 compileStmts 正常编译）。
func (c *Compiler) hoistFunctionDecls(stmts []ast.Statement) {
	for _, s := range stmts {
		if fd, ok := s.(*ast.FunctionDecl); ok && fd.Name != nil {
			slot := c.declareVar(fd.Name.Name)
			if err := c.compileFunction(fd.Name.Name, fd.Params, fd.ParamPatterns, fd.Defaults, fd.RestParam, fd.Body, fd.IsAsync, fd.IsGenerator, false, false); err != nil {
				// 提升编译出错：推迟到正常编译路径报错（这里不中断）。
				_ = err
				_ = slot
				continue
			}
			c.emit(bytecode.OpStoreLocal, uint32(slot))
		}
	}
}

// hoistVarDeclarators declares all var-scoped bindings from declarators,
// handling both simple names and destructuring patterns.
func (c *Compiler) hoistVarDeclarators(decls []ast.VarDeclarator) {
	for _, d := range decls {
		if d.Pattern != nil {
			for _, name := range patternNames(d.Pattern) {
				c.declareVar(name)
			}
		} else {
			c.declareVar(d.Name.Name)
		}
	}
}

// hoistLetConst 预声明 let/const 声明符中的绑定名到当前作用域。
// 用于函数体顶层 let/const 的提前声明，使嵌套函数能正确闭包捕获。
func (c *Compiler) hoistLetConst(decls []ast.VarDeclarator) {
	for _, d := range decls {
		if d.Pattern != nil {
			for _, name := range patternNames(d.Pattern) {
				c.declareLocal(name)
			}
		} else if d.Name != nil {
			c.declareLocal(d.Name.Name)
		}
	}
}

// === helpers ==============================================================

func (c *Compiler) emit(op bytecode.Opcode, operand uint32) int {
	// LineStarts：行号变化点（稀疏表，lineForPC 二分查找用）。
	// 每行只记录第一条指令的 PC；同一行的后续指令共享行号。
	if line := c.cur().curLine; line > 0 {
		ls := &c.cur().tmpl.LineStarts
		if len(*ls) == 0 || (*ls)[len(*ls)-1].Line != line {
			*ls = append(*ls, bytecode.LineEntry{PC: len(c.cur().tmpl.Code), Line: line})
		}
	}
	return bytecode.Encode(&c.cur().tmpl.Code, op, operand)
}

func (c *Compiler) curPC() int {
	return len(c.cur().tmpl.Code)
}

// patchJumpToHere patches the jump at pc to target the current PC.
func (c *Compiler) patchJumpToHere(pc int) {
	target := c.curPC()
	delta := target - (pc + bytecode.InstrSize)
	bytecode.PatchOperand(c.cur().tmpl.Code, pc, uint32(delta))
}

// === Optional chaining (?.) support ==========================================

// hasOptionalAccess returns true if the expression tree (following Object/Callee
// chains) contains any MemberExpr or CallExpr with Optional=true.
func hasOptionalAccess(expr ast.Expression) bool {
	switch n := expr.(type) {
	case *ast.MemberExpr:
		return n.Optional || hasOptionalAccess(n.Object)
	case *ast.CallExpr:
		return n.Optional || hasOptionalAccess(n.Callee)
	}
	return false
}

// beginOptionalChain starts a new optional chain context.
func (c *Compiler) beginOptionalChain() {
	c.optionalChainStack = append(c.optionalChainStack, nil)
	c.optionalChainResiduals = append(c.optionalChainResiduals, nil)
	// 嵌套链继承外层计数（内层短路残留包含外层未消费值，如 f?.(x?.y)
	// 中内层链短路时栈上还有外层 callee f）。
	c.optChainPushSaved = append(c.optChainPushSaved, c.optChainPushCount)
	c.optChainPushActive = true
}

// optChainDelta 维护当前链的栈上链值数（链内指令净栈效应，非链时 no-op）。
func (c *Compiler) optChainDelta(d int) {
	if c.optChainPushActive && len(c.optionalChainStack) > 0 {
		c.optChainPushCount += d
	}
}

// optPushValue 记录链内又压入一个值（对象/callee/参数/属性 key 等子表达式）。
func (c *Compiler) optPushValue() { c.optChainDelta(1) }

// emitOptionalJump emits an OpOptionalJump and records it in the current chain.
// 短路时 VM 弹栈顶压 undefined 后跳链尾；链内残留（本次短路跳过的中间值）
// 由 endOptionalChain 生成的清理块弹出（operand 只有 24 位，残留数无法编码）。
func (c *Compiler) emitOptionalJump() {
	pc := c.emit(bytecode.OpOptionalJump, 0)
	chain := &c.optionalChainStack[len(c.optionalChainStack)-1]
	*chain = append(*chain, pc)
	residual := c.optChainPushCount - 1
	if residual < 0 {
		residual = 0
	}
	res := &c.optionalChainResiduals[len(c.optionalChainResiduals)-1]
	*res = append(*res, residual)
}

// endOptionalChain 在链尾生成短路清理块：每个 OpOptionalJump 短路时栈上
// 残留 r 个链内中间值（VM 只弹栈顶压 undefined），清理块 POP × r 后跳汇聚
// 点，保证短路路径与正常路径在链尾后栈深一致（否则残留污染后续栈）。
func (c *Compiler) endOptionalChain() {
	idx := len(c.optionalChainStack) - 1
	jumps := c.optionalChainStack[idx]
	residuals := c.optionalChainResiduals[idx]
	// 正常路径（链尾）：跳汇聚点。
	tailJmp := c.emit(bytecode.OpJmp, 0)
	// 各级短路清理块：短路后栈为 [残留..., 结果]（VM 弹链尾值压 undefined，
	// 结果在栈顶）——先经 temp slot 暂存结果，POP × residual 弹掉残留，再
	// 压回结果，保证汇聚点栈深与正常路径一致（否则残留污染后续栈、结果
	// 丢失导致短路路径条件错误）。
	cleanupJmps := make([]int, len(jumps))
	var tempSlot uint32
	haveTemp := false
	for i := range jumps {
		if residuals[i] == 0 {
			// 无残留：短路后栈即 [结果]，无需清理。
			cleanup := c.curPC()
			cleanupJmps[i] = c.emit(bytecode.OpJmp, 0)
			c.patchOptionalJumpToHere(jumps[i], cleanup)
			continue
		}
		if !haveTemp {
			tempSlot = uint32(c.newSlot())
			haveTemp = true
		}
		cleanup := c.curPC()
		c.emit(bytecode.OpStoreLocal, tempSlot) // 弹结果暂存
		for j := 0; j < residuals[i]; j++ {
			c.emit(bytecode.OpPop, 0) // 弹残留
		}
		c.emit(bytecode.OpLoadLocal, tempSlot) // 压回结果
		cleanupJmps[i] = c.emit(bytecode.OpJmp, 0)
		c.patchOptionalJumpToHere(jumps[i], cleanup)
	}
	// 汇聚点：patch 正常路径与各清理块的 JMP。
	c.patchJumpToHere(tailJmp)
	for _, jmp := range cleanupJmps {
		c.patchJumpToHere(jmp)
	}
	c.optionalChainStack = c.optionalChainStack[:idx]
	c.optionalChainResiduals = c.optionalChainResiduals[:idx]
	// 恢复外层计数：内层链结果也在栈上压入 1 个值。
	outer := c.optChainPushSaved[idx]
	c.optChainPushSaved = c.optChainPushSaved[:idx]
	c.optChainPushCount = outer + 1
	if len(c.optionalChainStack) == 0 {
		c.optChainPushActive = false
		c.optChainPushCount = 0
	}
}

// patchOptionalJumpToHere 将 OpOptionalJump 的偏移 patch 到目标 PC。
func (c *Compiler) patchOptionalJumpToHere(pc, target int) {
	delta := target - (pc + bytecode.InstrSize)
	bytecode.PatchOperand(c.cur().tmpl.Code, pc, uint32(delta))
}

// inOptionalChain returns true if there's an active optional chain context.
func (c *Compiler) inOptionalChain() bool {
	return len(c.optionalChainStack) > 0
}

// propKey returns the property name for a literal key.
func propKey(key ast.Expression) string {
	switch k := key.(type) {
	case *ast.Identifier:
		return k.Name
	case *ast.StringLit:
		return k.Value
	case *ast.NumberLit:
		return strconv.FormatFloat(k.Value, 'g', -1, 64)
	}
	return ""
}

func funcNameFromExpr(n *ast.FunctionExpr) string {
	if n.Name != nil {
		return n.Name.Name
	}
	return ""
}

// === class compilation (ES2015) ===========================================

// superCtorName / superProtoName return the unique upvalue binding names for
// a class's superclass constructor and prototype, used by `super` resolution.
func superCtorName(classID int) string {
	return fmt.Sprintf("__home_ctor_%d__", classID)
}
func superProtoName(classID int) string {
	return fmt.Sprintf("__home_proto_%d__", classID)
}

// emitSuperCtor loads the current class's superclass constructor (for super()
// calls in a derived constructor).
func (c *Compiler) emitSuperCtor() {
	kind, idx := c.resolve(superCtorName(c.curClassID))
	c.emitLoadByKind(kind, idx)
}

// emitSuperProto loads the current class's superclass prototype (for
// super.method() calls).
func (c *Compiler) emitSuperProto() {
	kind, idx := c.resolve(superProtoName(c.curClassID))
	c.emitLoadByKind(kind, idx)
}

func (c *Compiler) emitLoadByKind(kind string, idx int) {
	switch kind {
	case "local":
		c.emit(bytecode.OpLoadLocal, uint32(idx))
	case "upvalue":
		c.emit(bytecode.OpLoadUpvalue, uint32(idx))
	case "global":
		c.emit(bytecode.OpLoadGlobal, uint32(idx))
	}
}

// compileClassDecl compiles `class Name [extends Super] { body }` as a
// statement: the constructor is stored in a variable named Name.
func (c *Compiler) compileClassDecl(d *ast.ClassDecl) error {
	nameSlot := c.declareVar(d.Name.Name)
	if err := c.compileClass(d.Name.Name, d.SuperClass, d.Body); err != nil {
		return err
	}
	c.emit(bytecode.OpStoreLocal, uint32(nameSlot))
	return nil
}

// compileClassExpr compiles `class [Name] [extends Super] { body }` as an
// expression: the constructor is left on the stack.
func (c *Compiler) compileClassExpr(e *ast.ClassExpr) error {
	name := ""
	if e.Name != nil {
		name = e.Name.Name
	}
	if name == "" {
		return c.compileClass(name, e.SuperClass, e.Body)
	}

	// A named class expression has a private lexical self-binding visible to
	// its constructor, methods, and field initializers, but not to the outer
	// scope. Capture a hidden local and temporarily expose it under the class
	// name while method templates are compiled.
	hiddenName := fmt.Sprintf("__class_expr_self_%d__", c.classCounter)
	selfSlot := c.declareLocal(hiddenName)
	scope := c.cur().scopes[len(c.cur().scopes)-1]
	previousSlot, hadPrevious := scope.decls[name]
	scope.decls[name] = selfSlot
	defer func() {
		if hadPrevious {
			scope.decls[name] = previousSlot
		} else {
			delete(scope.decls, name)
		}
	}()

	if err := c.compileClass(name, e.SuperClass, e.Body); err != nil {
		return err
	}
	// Preserve the expression result while initializing the captured binding.
	c.emit(bytecode.OpDup, 0)
	c.emit(bytecode.OpStoreLocal, uint32(selfSlot))
	return nil
}

// compileClass is the core class compilation routine. It compiles all methods
// into FuncTemplates, builds a ClassTemplate, and emits OpMakeClass. After
// OpMakeClass, the constructor function is on top of the stack.
func (c *Compiler) compileClass(name string, super ast.Expression, body *ast.ClassBody) error {
	classID := c.classCounter
	c.classCounter++
	savedClassID := c.curClassID
	c.curClassID = classID
	defer func() { c.curClassID = savedClassID }()

	hasSuper := super != nil

	// Evaluate the superclass and stash it in unique local slots so that
	// methods can capture them as upvalues for `super` resolution.
	if hasSuper {
		if err := c.compileExpr(super); err != nil {
			return err
		}
		// stack: [super]
		c.emit(bytecode.OpDup, 0)
		// stack: [super, super]
		ctorSlot := c.declareLocal(superCtorName(classID))
		c.emit(bytecode.OpStoreLocal, uint32(ctorSlot))
		// stack: [super]
		c.emit(bytecode.OpLoadLocal, uint32(ctorSlot))
		protoIdx := c.cur().tmpl.AddStringConst("prototype")
		c.emit(bytecode.OpGetProp, uint32(protoIdx))
		protoSlot := c.declareLocal(superProtoName(classID))
		c.emit(bytecode.OpStoreLocal, uint32(protoSlot))
		// stack: [super]  — consumed by OpMakeClass below
	}

	classTpl := &bytecode.ClassTemplate{
		Name:     name,
		HasSuper: hasSuper,
	}

	// Collect class field declarations (ES2022 / TypeScript). Instance fields
	// with initializers are injected into the constructor body; static fields
	// are assigned to the class after OpMakeClass. Fields without initializers
	// (e.g. `x: number;`) have no runtime effect and are skipped.
	var instanceFieldInits []ast.Statement
	var staticFields []*ast.MethodDefinition
	for fieldIndex, m := range body.Methods {
		if m.Kind != ast.MethodField {
			continue
		}
		if m.Static {
			staticFields = append(staticFields, &m)
			continue
		}
		if m.Init == nil {
			continue
		}
		fieldKey := m.Key
		if m.Computed {
			// Computed field names are evaluated once when the class is defined,
			// not once per instance. Store the key in the surrounding scope; the
			// generated constructor captures that slot as an upvalue.
			keyName := fmt.Sprintf("__class_field_key_%d_%d__", classID, fieldIndex)
			keySlot := c.declareLocal(keyName)
			if err := c.compileExpr(m.Key); err != nil {
				return err
			}
			c.emit(bytecode.OpStoreLocal, uint32(keySlot))
			fieldKey = &ast.Identifier{Name: keyName, Loc: m.Loc}
		}
		instanceFieldInits = append(instanceFieldInits, &ast.ExprStmt{
			Expr: &ast.AssignExpr{
				Op: "=",
				Left: &ast.MemberExpr{
					Object:   &ast.ThisExpr{Loc: m.Loc},
					Property: fieldKey,
					Computed: m.Computed,
					Loc:      m.Loc,
				},
				Right: m.Init,
				Loc:   m.Loc,
			},
			Loc: m.Loc,
		})
	}

	// Compile the constructor (or synthesize a default). Instance field
	// initializers are prepended to the constructor body so they run before
	// user code (base class) or after super() (derived synthesized ctor).
	hasCtor := false
	for _, m := range body.Methods {
		if m.Kind == ast.MethodConstructor {
			ctorFn := *m.Value // shallow copy — we'll replace Body
			if len(instanceFieldInits) > 0 {
				newBody := make([]ast.Statement, 0, len(instanceFieldInits)+len(m.Value.Body.Body))
				if !hasSuper {
					// Base class: field inits run first.
					newBody = append(newBody, instanceFieldInits...)
					newBody = append(newBody, m.Value.Body.Body...)
				} else {
					// Derived class: field inits should run after super().
					// For the MVP, we prepend them — user is responsible for
					// calling super() first. A correct implementation would
					// split the body at the super() call.
					newBody = append(newBody, instanceFieldInits...)
					newBody = append(newBody, m.Value.Body.Body...)
				}
				ctorFn.Body = &ast.BlockStmt{Body: newBody, Loc: m.Value.Body.Loc}
			}
			idx, err := c.compileMethod("constructor", &ctorFn)
			if err != nil {
				return err
			}
			classTpl.CtorIdx = idx
			hasCtor = true
			break
		}
	}
	if !hasCtor {
		var idx int
		var err error
		if hasSuper {
			if len(instanceFieldInits) > 0 {
				// Synthesize: function(...args) { super(...args); fieldInits }
				superCall := &ast.ExprStmt{
					Expr: &ast.CallExpr{
						Callee: &ast.SuperExpr{},
						Arguments: []ast.Expression{
							&ast.SpreadElement{Arg: &ast.Identifier{Name: "args"}},
						},
					},
				}
				body := append([]ast.Statement{superCall}, instanceFieldInits...)
				ctorFn := &ast.FunctionExpr{
					RestParam: &ast.Identifier{Name: "args"},
					Body:      &ast.BlockStmt{Body: body},
				}
				idx, err = c.compileMethod("constructor", ctorFn)
			} else {
				idx, err = c.compileDefaultDerivedCtor()
			}
		} else if len(instanceFieldInits) > 0 {
			// Synthesize a base constructor that initializes fields.
			ctorFn := &ast.FunctionExpr{
				Body: &ast.BlockStmt{Body: instanceFieldInits},
			}
			idx, err = c.compileMethod("constructor", ctorFn)
		} else {
			idx, err = c.compileDefaultBaseCtor()
		}
		if err != nil {
			return err
		}
		classTpl.CtorIdx = idx
	}

	// Compile non-constructor, non-field methods.
	// 计算键方法（[expr]() {}）：键表达式按方法顺序求值压栈，供
	// OpMakeClass 弹出使用；记录其在 Methods 中的索引。
	for _, m := range body.Methods {
		if m.Kind == ast.MethodConstructor || m.Kind == ast.MethodField {
			continue
		}
		if m.Computed {
			if err := c.compileExpr(m.Key); err != nil {
				return err
			}
			classTpl.ComputedIdx = append(classTpl.ComputedIdx, len(classTpl.Methods))
		}
		methodName := propKey(m.Key)
		if m.Computed {
			methodName = "computed"
		}
		idx, err := c.compileMethod(methodName, m.Value)
		if err != nil {
			return err
		}
		kind := bytecode.MethodKindNormal
		switch m.Kind {
		case ast.MethodGetter:
			kind = bytecode.MethodKindGetter
		case ast.MethodSetter:
			kind = bytecode.MethodKindSetter
		}
		classTpl.Methods = append(classTpl.Methods, bytecode.ClassMethodTemplate{
			Name:    methodName,
			Kind:    kind,
			Static:  m.Static,
			TmplIdx: idx,
		})
	}

	classIdx := c.module.AddClass(classTpl)
	c.emit(bytecode.OpMakeClass, uint32(classIdx))

	// Static field initialization: `Class.field = init` after the class is
	// created. The constructor (class function) is on top of the stack.
	// 布局 [class, class, val] → OpSetPropObj（val 弹栈、写 class 属性、
	// class 留在栈顶供后续字段/表达式继续使用）。
	for _, f := range staticFields {
		if f.Init == nil {
			continue
		}
		c.emit(bytecode.OpDup, 0)
		if f.Computed {
			if err := c.compileExpr(f.Key); err != nil {
				return err
			}
			if err := c.compileExpr(f.Init); err != nil {
				return err
			}
			c.emit(bytecode.OpSetPropComputedObj, 0)
			continue
		}
		fieldName := propKey(f.Key)
		nameIdx := c.cur().tmpl.AddStringConst(fieldName)
		if err := c.compileExpr(f.Init); err != nil {
			return err
		}
		c.emit(bytecode.OpSetPropObj, uint32(nameIdx))
	}
	return nil
}

// compileMethod compiles a class method body into a FuncTemplate and returns
// its index. Unlike compileFunction, it does NOT emit OpMakeClosure — the
// class assembler (OpMakeClass) creates the closure at runtime.
func (c *Compiler) compileMethod(name string, fn *ast.FunctionExpr) (int, error) {
	restoreControlFlow := c.isolateControlFlow()
	defer restoreControlFlow()

	params := fn.Params
	defaults := fn.Defaults
	rest := fn.RestParam

	numLocals := 1 + len(params)
	if rest != nil {
		numLocals++
	}
	tmpl := &bytecode.FuncTemplate{
		Name:        name,
		NumParams:   len(params),
		NumLocals:   numLocals,
		IsVarArgs:   rest != nil,
		IsAsync:     fn.IsAsync,
		IsGenerator: fn.IsGenerator,
		SourceFile:  c.cur().tmpl.SourceFile,
	}
	funcIdx := c.module.AddFunction(tmpl)

	fc := &funcCtx{
		tmpl:             tmpl,
		upvalueIndex:     make(map[string]int),
		inlineCandidates: map[string]int{},
	}
	fc.scopes = []*scope{{decls: make(map[string]int), isFunc: true}}
	fc.scopes[0].decls["__this__"] = 0
	for i, p := range params {
		if p != nil {
			fc.scopes[0].decls[p.Name] = i + 1
		}
	}
	if rest != nil {
		fc.scopes[0].decls[rest.Name] = 1 + len(params)
	}
	// 方法同样绑定 own `arguments` 对象。必须显式分配槽并递增 NumLocals，
	// 否则 ArgumentsSlot 默认 0 会覆盖 slot 0 的 `this`。
	argsSlot := tmpl.NumLocals
	tmpl.ArgumentsSlot = argsSlot
	tmpl.NumLocals++
	fc.scopes[0].decls["arguments"] = argsSlot
	// 类方法同样分配 new.target 槽位（new.target.prototype 等用法）。
	ntSlot := tmpl.NumLocals
	tmpl.NewTargetSlot = ntSlot
	tmpl.NumLocals++
	fc.scopes[0].decls["__newTarget__"] = ntSlot
	c.funcStack = append(c.funcStack, fc)

	// Default-parameter initialization at function entry.
	for i, def := range defaults {
		if def == nil {
			continue
		}
		slot := i + 1
		c.emit(bytecode.OpLoadLocal, uint32(slot))
		c.emit(bytecode.OpPushUndefined, 0)
		c.emit(bytecode.OpStrictEq, 0)
		jSkip := c.emit(bytecode.OpJmpFalsePop, 0)
		if err := c.compileExpr(def); err != nil {
			return 0, err
		}
		c.emit(bytecode.OpStoreLocal, uint32(slot))
		c.patchJumpToHere(jSkip)
	}

	// Class methods and constructors use the same destructuring parameter
	// semantics as ordinary functions. Bind patterns only after whole-parameter
	// defaults have been applied (for example constructor({x = fallback} = {})).
	for i, pat := range fn.ParamPatterns {
		if pat == nil {
			continue
		}
		if err := c.compileBindPattern(pat, i+1, "let"); err != nil {
			c.funcStack = c.funcStack[:len(c.funcStack)-1]
			return 0, err
		}
	}

	c.hoistFunc(fn.Body)
	// Methods are function scopes too. Hoist and emit nested function
	// declarations before compiling the body; npm stream implementations use
	// local async generator declarations inside static methods.
	c.hoistFunctionDecls(fn.Body.Body)
	if err := c.compileStmts(fn.Body.Body); err != nil {
		return 0, err
	}
	c.emit(bytecode.OpReturnUndef, 0)
	c.funcStack = c.funcStack[:len(c.funcStack)-1]
	return funcIdx, nil
}

// compileDefaultBaseCtor synthesizes an empty constructor for a base class
// (no extends): `function() {}`.
func (c *Compiler) compileDefaultBaseCtor() (int, error) {
	tmpl := &bytecode.FuncTemplate{
		Name:       "constructor",
		NumParams:  0,
		NumLocals:  1, // slot 0 = this
		SourceFile: c.cur().tmpl.SourceFile,
		// 合成空构造器不引用 `arguments` / `new.target`：显式置 -1，
		// 避免默认 0 覆盖 this。
		ArgumentsSlot: -1,
		NewTargetSlot: -1,
	}
	funcIdx := c.module.AddFunction(tmpl)
	fc := &funcCtx{
		tmpl:             tmpl,
		upvalueIndex:     make(map[string]int),
		inlineCandidates: map[string]int{},
	}
	fc.scopes = []*scope{{decls: make(map[string]int), isFunc: true}}
	fc.scopes[0].decls["__this__"] = 0
	c.funcStack = append(c.funcStack, fc)
	c.emit(bytecode.OpReturnUndef, 0)
	c.funcStack = c.funcStack[:len(c.funcStack)-1]
	return funcIdx, nil
}

// compileDefaultDerivedCtor synthesizes `function(...args) { super(...args); }`
// for a derived class with no explicit constructor.
func (c *Compiler) compileDefaultDerivedCtor() (int, error) {
	tmpl := &bytecode.FuncTemplate{
		Name:       "constructor",
		NumParams:  0,
		NumLocals:  3, // slot 0 = this, slot 1 = rest "args", slot 2 = new.target
		IsVarArgs:  true,
		SourceFile: c.cur().tmpl.SourceFile,
		// 合成派生构造器通过 rest 转发 super，不引用 `arguments`：置 -1。
		// new.target 槽必须分配：super() 调用原生父类构造（Error/DOMException
		// 等）时，constructThis 需要 newTarget.prototype 修正实例原型。
		ArgumentsSlot: -1,
		NewTargetSlot: 2,
	}
	funcIdx := c.module.AddFunction(tmpl)
	fc := &funcCtx{
		tmpl:             tmpl,
		upvalueIndex:     make(map[string]int),
		inlineCandidates: map[string]int{},
	}
	fc.scopes = []*scope{{decls: make(map[string]int), isFunc: true}}
	fc.scopes[0].decls["__this__"] = 0
	fc.scopes[0].decls["args"] = 1
	fc.scopes[0].decls["__newTarget__"] = 2
	c.funcStack = append(c.funcStack, fc)

	// super(...args): load home-ctor, load rest args, construct with this.
	c.emitSuperCtor()
	c.emit(bytecode.OpLoadLocal, 1) // rest param "args"
	c.emit(bytecode.OpConstructThisArgs, 0)
	c.emit(bytecode.OpPop, 0)

	c.emit(bytecode.OpReturnUndef, 0)
	c.funcStack = c.funcStack[:len(c.funcStack)-1]
	return funcIdx, nil
}

// astBodyReferencesName 扫描函数体（含嵌套函数）是否引用了指定标识符。
// 用于 NFE 自引用槽的按需分配：仅当体内实际引用名字时才需要槽位。
// 简化实现：同名 Identifier（引用或声明）即视为引用——遮蔽等罕见场景
// 只会多分配一个槽位（语义正确，仅 JIT 拒绝的轻微性能损失）。
func astBodyReferencesName(body ast.Node, name string) bool {
	found := false
	visited := make(map[ast.Node]bool) // 防共享/循环引用导致无限递归
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if found || n == nil || visited[n] {
			return
		}
		visited[n] = true
		if id, ok := n.(*ast.Identifier); ok && id.Name == name {
			found = true
			return
		}
		// 反射遍历子节点（ast 节点均为结构体/切片/指针）。
		rv := reflect.ValueOf(n)
		walkValue(rv, walk, visited)
	}
	walk(body)
	return found
}

func walkValue(rv reflect.Value, walk func(ast.Node), visited map[ast.Node]bool) {
	switch rv.Kind() {
	case reflect.Struct:
		t := rv.Type()
		for i := 0; i < rv.NumField(); i++ {
			if t.Field(i).PkgPath != "" {
				continue // 未导出字段
			}
			walkValue(rv.Field(i), walk, visited)
		}
	case reflect.Ptr:
		if !rv.IsNil() {
			// 通知访问者后必须继续遍历字段：此前仅 walk(n) 导致嵌套
			// 子树（函数体/表达式内的引用）永远不被扫描——astBodyReferencesName
			// 恒 false（NFE 自引用槽从不分配）与 forLetCapturedNames 恒空
			// （per-iteration 绑定从不启用）均由此引起。
			if n, ok := rv.Interface().(ast.Node); ok {
				walk(n)
			}
			walkValue(rv.Elem(), walk, visited)
		}
	case reflect.Slice:
		for i := 0; i < rv.Len(); i++ {
			walkValue(rv.Index(i), walk, visited)
		}
	case reflect.Interface:
		if !rv.IsNil() {
			walkValue(rv.Elem(), walk, visited)
		}
	}
}

// forLetCapturedNames 返回 body 中嵌套函数（闭包）内引用的名字子集。
// 用于判断 C 风格 for 的 let 变量是否需要 per-iteration 绑定：仅当
// 循环体内的闭包捕获该变量时（ES2015 语义要求每次迭代独立副本），
// 才启用头槽+迭代槽复制；否则保持单槽（JIT 匹配器依赖的指令形状）。
func forLetCapturedNames(body ast.Node, names []string) []string {
	if len(names) == 0 {
		return nil
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	found := map[string]bool{}
	visited := make(map[ast.Node]bool) // 防共享/循环引用
	var walkFn func(n ast.Node, inFunc bool)
	walkFn = func(n ast.Node, inFunc bool) {
		if n == nil || visited[n] {
			return
		}
		visited[n] = true
		if inFunc {
			if id, ok := n.(*ast.Identifier); ok && want[id.Name] {
				found[id.Name] = true
				return // 已找到该名，不必继续下钻
			}
		}
		// 嵌套函数：进入其内部子树（闭包捕获上下文）。
		switch f := n.(type) {
		case *ast.FunctionExpr:
			walkFn(f.Body, true)
			// 函数默认值/参数中的引用不视为捕获（参数绑定遮蔽）；
			// 简化实现：仅扫描函数体。返回后不再走反射（防重复）。
			return
		case *ast.ArrowFunc:
			walkFn(f.Body, true)
			return
		case *ast.FunctionDecl:
			walkFn(f.Body, true)
			return
		}
		rv := reflect.ValueOf(n)
		walkValue(rv, func(child ast.Node) { walkFn(child, inFunc) }, visited)
	}
	walkFn(body, false)
	if len(found) == 0 {
		return nil
	}
	out := make([]string, 0, len(found))
	for _, n := range names {
		if found[n] {
			out = append(out, n)
		}
	}
	return out
}

// collectLoopBodyBlockNames 收集循环体内块级作用域（let/const）声明的绑定名。
// 不进入嵌套函数体（嵌套函数内部的声明属于该函数自己的作用域，不共享本循环
// 的迭代作用域）；var/function/class 声明经 hoistFunc 提升到函数作用域（槽在
// 循环前分配），不在此列。
func collectLoopBodyBlockNames(body ast.Statement) []string {
	seen := map[string]bool{}
	var names []string
	visited := make(map[ast.Node]bool) // 防共享/循环引用
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if n == nil || visited[n] {
			return
		}
		visited[n] = true
		switch f := n.(type) {
		case *ast.FunctionExpr, *ast.ArrowFunc, *ast.FunctionDecl:
			return // 嵌套函数体：内部声明不属于本循环迭代作用域
		case *ast.VarDecl:
			if f.Kind == "let" || f.Kind == "const" {
				for _, d := range f.Decls {
					if d.Pattern != nil {
						for _, name := range patternNames(d.Pattern) {
							if !seen[name] {
								seen[name] = true
								names = append(names, name)
							}
						}
					} else if d.Name != nil {
						if !seen[d.Name.Name] {
							seen[d.Name.Name] = true
							names = append(names, d.Name.Name)
						}
					}
				}
			}
			return // init 内无新的块级绑定名，不深入
		}
		rv := reflect.ValueOf(n)
		walkValue(rv, func(child ast.Node) { walk(child) }, visited)
	}
	walk(body)
	return names
}

// loopBodyCapturedBlockNames 返回循环体内被嵌套函数（闭包）捕获的块级
// let/const 绑定名。用于判断 classic for / while / do-while / for-in 的循环体
// 是否需要 per-iteration 封存（ES2015 语义：每次迭代的块级声明是独立副本，
// 闭包应捕获当次迭代的值，而非共享同一槽位的终值）。
func loopBodyCapturedBlockNames(body ast.Statement) []string {
	names := collectLoopBodyBlockNames(body)
	if len(names) == 0 {
		return nil
	}
	return forLetCapturedNames(body, names)
}
