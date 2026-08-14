// Package compiler translates an AST into bytecode.FuncTemplate(s).
package compiler

import (
	"fmt"
	"strconv"

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
	// inStaticCtx 为 true 表示当前正在编译静态上下文（静态方法体 / 静态块 /
	// 静态字段初始化器）。此语境下 super 属性访问解析到父类构造器（而非
	// 父类原型），见 emitSuperProto。compileClass 进入/退出时保存/恢复。
	inStaticCtx bool

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
			for _, name := range ast.PatternNames(d.Pattern) {
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
			for _, name := range ast.PatternNames(d.Pattern) {
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

// emitSuperProto loads the superclass receiver for `super.prop` access:
// the superclass prototype in instance context (super.method() 委托到父类
// 原型)，or the superclass constructor in static context（静态方法/静态块/
// 静态字段初始化器中 super.prop 按规范解析到父类构造器的静态属性）。
func (c *Compiler) emitSuperProto() {
	name := superProtoName(c.curClassID)
	if c.inStaticCtx {
		name = superCtorName(c.curClassID)
	}
	kind, idx := c.resolve(name)
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
		// 统一遍历子节点（internal/engine/ast/walk.go），取代反射遍历。
		ast.ForEachChild(n, func(c ast.Node) bool {
			walk(c)
			return found // 命中后全局提前终止
		})
	}
	walk(body)
	return found
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
			// 简化实现：仅扫描函数体。返回后不再遍历子节点（防重复）。
			return
		case *ast.ArrowFunc:
			walkFn(f.Body, true)
			return
		case *ast.FunctionDecl:
			walkFn(f.Body, true)
			return
		}
		ast.ForEachChild(n, func(c ast.Node) bool {
			walkFn(c, inFunc)
			return false
		})
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
						for _, name := range ast.PatternNames(d.Pattern) {
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
		ast.ForEachChild(n, func(c ast.Node) bool {
			walk(c)
			return false
		})
	}
	walk(body)
	return names
}
// 闭包应捕获当次迭代的值，而非共享同一槽位的终值）。
func loopBodyCapturedBlockNames(body ast.Statement) []string {
	names := collectLoopBodyBlockNames(body)
	if len(names) == 0 {
		return nil
	}
	return forLetCapturedNames(body, names)
}

