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
}

type pendingJump struct {
	pc    int
	depth int
	label string
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

	scopes []*scope // scope chain; scopes[0] is the function scope

	// upvalueIndex maps a name to its index in tmpl.Upvalues.
	upvalueIndex map[string]int
}

type scope struct {
	parent *scope
	decls  map[string]int // name → local slot in current function
	isFunc bool           // function scope (vars hoist here)
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
		tmpl:         tmpl,
		upvalueIndex: make(map[string]int),
	}
	fc.scopes = []*scope{{decls: make(map[string]int), isFunc: true}}
	c.funcStack = append(c.funcStack, fc)

	// slot 0 is reserved for `this`
	fc.scopes[0].decls["__this__"] = 0

	// Hoist top-level var/function declarations so NumLocals is correct.
	c.hoistTopLevel(prog.Body)

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
			return c.module, nil
		}
		if err := c.compileStmt(s); err != nil {
			return nil, err
		}
	}
	// Implicit return undefined at end of program.
	c.emit(bytecode.OpReturnUndef, 0)
	return c.module, nil
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
				if st.Kind == "var" {
					c.hoistVarDeclarators(st.Decls)
				}
			case *ast.FunctionDecl:
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
			case *ast.ForOfStmt:
				if vd, ok := st.Left.(*ast.VarDecl); ok && vd.Kind == "var" {
					c.hoistVarDeclarators(vd.Decls)
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
		return c.compileForInOrOf(n.Left, n.Right, n.Body, false)
	case *ast.ForOfStmt:
		return c.compileForInOrOf(n.Left, n.Right, n.Body, true)
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
			if err := c.compileExpr(decl.Init); err != nil {
				return err
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
		for _, prop := range pat.Properties {
			tmpSlot := c.newSlot()
			if prop.IsRest {
				// Object rest: create { ...src } then delete already-bound keys.
				boundKeys := objectPatternBoundKeys(pat)
				c.emit(bytecode.OpNewObject, 0)
				c.emit(bytecode.OpLoadLocal, uint32(srcSlot))
				c.emit(bytecode.OpSpreadObject, 0)
				// Delete each already-bound key from the rest object.
				for _, k := range boundKeys {
					nameIdx := c.cur().tmpl.AddStringConst(k)
					c.emit(bytecode.OpDup, 0)                   // dup rest obj
					c.emit(bytecode.OpDelProp, uint32(nameIdx)) // pop obj, push bool
					c.emit(bytecode.OpPop, 0)                   // discard bool
				}
				c.emit(bytecode.OpStoreLocal, uint32(tmpSlot))
				c.compileBindPattern(prop.Value, tmpSlot, kind)
				continue
			}
			// result = src[key]
			c.emit(bytecode.OpLoadLocal, uint32(srcSlot))
			key := propKey(prop.Key)
			nameIdx := c.cur().tmpl.AddStringConst(key)
			c.emit(bytecode.OpGetProp, uint32(nameIdx))
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

// objectPatternBoundKeys returns the property keys already bound by earlier
// (non-rest) properties in the object pattern. Used for object-rest exclusion.
func objectPatternBoundKeys(pat *ast.ObjectPattern) []string {
	var keys []string
	for _, prop := range pat.Properties {
		if prop.IsRest {
			break
		}
		keys = append(keys, propKey(prop.Key))
	}
	return keys
}

func (c *Compiler) compileFunctionDecl(d *ast.FunctionDecl) error {
	if d.Name == nil {
		return nil
	}
	slot := c.declareVar(d.Name.Name)
	if err := c.compileFunction(d.Name.Name, d.Params, d.Defaults, d.RestParam, d.Body, false, d.IsGenerator); err != nil {
		return err
	}
	c.emit(bytecode.OpStoreLocal, uint32(slot))
	return nil
}

func (c *Compiler) compileBlock(b *ast.BlockStmt) error {
	c.pushBlock()
	defer c.popBlock()
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
	if err := c.compileStmt(s.Body); err != nil {
		return err
	}
	c.patchLoopContinues(loopStart)
	c.emit(bytecode.OpJmp, uint32(loopStart-(c.curPC()+bytecode.InstrSize)))
	exit := c.curPC()
	c.patchJumpToHere(jumpExit)
	c.topLoop().breakTarget = exit
	c.patchLoopBreaks(exit)
	return nil
}

func (c *Compiler) compileDoWhile(s *ast.DoWhileStmt) error {
	loopStart := c.curPC()
	c.pushLoop(0, 0) // continue target patched later
	defer c.popLoop()
	if err := c.compileStmt(s.Body); err != nil {
		return err
	}
	continuePC := c.curPC()
	c.topLoop().continueTarget = continuePC
	c.patchLoopContinues(continuePC)
	if err := c.compileExpr(s.Test); err != nil {
		return err
	}
	c.emit(bytecode.OpJmpTruePop, uint32(loopStart-(c.curPC()+bytecode.InstrSize)))
	exit := c.curPC()
	c.topLoop().breakTarget = exit
	c.patchLoopBreaks(exit)
	return nil
}

func (c *Compiler) compileFor(s *ast.ForStmt) error {
	c.pushBlock()
	defer c.popBlock()
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
	if err := c.compileStmt(s.Body); err != nil {
		return err
	}
	continuePC := c.curPC()
	c.topLoop().continueTarget = continuePC
	c.patchLoopContinues(continuePC)
	if s.Update != nil {
		if err := c.compileExpr(s.Update); err != nil {
			return err
		}
		c.emit(bytecode.OpPop, 0)
	}
	c.emit(bytecode.OpJmp, uint32(loopStart-(c.curPC()+bytecode.InstrSize)))
	exit := c.curPC()
	if s.Test != nil {
		c.patchJumpToHere(jumpExit)
	}
	c.topLoop().breakTarget = exit
	c.patchLoopBreaks(exit)
	return nil
}

func (c *Compiler) compileForInOrOf(left ast.Node, right ast.Expression, body ast.Statement, isOf bool) error {
	if isOf {
		return c.compileForOf(left, right, body)
	}
	return c.compileForIn(left, right, body)
}

// compileForOf compiles `for (left of right) body` using the ES2015 iterator
// protocol: get [Symbol.iterator](), call .next(), check {done, value}.
func (c *Compiler) compileForOf(left ast.Node, right ast.Expression, body ast.Statement) error {
	c.pushBlock()
	defer c.popBlock()

	// Evaluate iterable and get iterator.
	tmpIter := c.declareLocal("__iter__")
	if err := c.compileExpr(right); err != nil {
		return err
	}
	c.emit(bytecode.OpGetIterator, 0) // pop iterable, push iterator
	c.emit(bytecode.OpStoreLocal, uint32(tmpIter))

	tmpResult := c.declareLocal("__iter_result__")

	nameNext := c.cur().tmpl.AddStringConst("next")
	nameDone := c.cur().tmpl.AddStringConst("done")
	nameValue := c.cur().tmpl.AddStringConst("value")

	loopStart := c.curPC()

	// Call iter.next() — push iterator as receiver, 0 args.
	c.emit(bytecode.OpLoadLocal, uint32(tmpIter))
	c.emit(bytecode.OpCallMethod, uint32(nameNext)) // 0 args encoded in high bits
	c.emit(bytecode.OpStoreLocal, uint32(tmpResult))

	// Check done: if result.done is truthy, exit loop.
	c.emit(bytecode.OpLoadLocal, uint32(tmpResult))
	c.emit(bytecode.OpGetProp, uint32(nameDone))
	jumpExit := c.emit(bytecode.OpJmpTruePop, 0)

	c.pushLoop(loopStart, 0)
	defer c.popLoop()

	// Get value and bind to left.
	c.emit(bytecode.OpLoadLocal, uint32(tmpResult))
	c.emit(bytecode.OpGetProp, uint32(nameValue))
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
	c.emit(bytecode.OpJmp, uint32(loopStart-(c.curPC()+bytecode.InstrSize)))
	exit := c.curPC()
	c.patchJumpToHere(jumpExit)
	c.topLoop().breakTarget = exit
	c.patchLoopBreaks(exit)
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
	c.emit(bytecode.OpLoadLocal, uint32(tmpIdx))
	c.emit(bytecode.OpPushInt, 1)
	c.emit(bytecode.OpAdd, 0)
	c.emit(bytecode.OpStoreLocal, uint32(tmpIdx))
	c.emit(bytecode.OpJmp, uint32(loopStart-(c.curPC()+bytecode.InstrSize)))
	exit := c.curPC()
	c.patchJumpToHere(jumpExit)
	c.topLoop().breakTarget = exit
	c.patchLoopBreaks(exit)
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
	}
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
	pc := c.emit(bytecode.OpJmp, 0)
	depth := len(c.loopStack) - 1
	c.pendingBreaks = append(c.pendingBreaks, pendingJump{pc: pc, depth: depth, label: s.Label})
	return nil
}

func (c *Compiler) compileContinue(s *ast.ContinueStmt) error {
	if len(c.loopStack) == 0 {
		return fmt.Errorf("illegal continue statement")
	}
	pc := c.emit(bytecode.OpJmp, 0)
	depth := len(c.loopStack) - 1
	c.pendingContinues = append(c.pendingContinues, pendingJump{pc: pc, depth: depth, label: s.Label})
	return nil
}

// patchLoopBreaks patches all pending break jumps at the current loop depth.
func (c *Compiler) patchLoopBreaks(exitPC int) {
	curDepth := len(c.loopStack) // topLoop is about to be popped; use current depth
	kept := c.pendingBreaks[:0]
	for _, pj := range c.pendingBreaks {
		if pj.depth == curDepth-1 && pj.label == "" {
			delta := exitPC - (pj.pc + bytecode.InstrSize)
			bytecode.PatchOperand(c.cur().tmpl.Code, pj.pc, uint32(delta))
		} else {
			kept = append(kept, pj)
		}
	}
	c.pendingBreaks = kept
}

// patchLoopContinues patches all pending continue jumps at the current loop depth.
func (c *Compiler) patchLoopContinues(targetPC int) {
	curDepth := len(c.loopStack)
	kept := c.pendingContinues[:0]
	for _, pj := range c.pendingContinues {
		if pj.depth == curDepth-1 && pj.label == "" {
			delta := targetPC - (pj.pc + bytecode.InstrSize)
			bytecode.PatchOperand(c.cur().tmpl.Code, pj.pc, uint32(delta))
		} else {
			kept = append(kept, pj)
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

	c.emit(bytecode.OpTryEnter, uint32(tryIdx))
	if err := c.compileBlock(s.Block); err != nil {
		return err
	}
	c.emit(bytecode.OpTryExit, uint32(tryIdx))

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
		c.emit(bytecode.OpTryExit, uint32(tryIdx))
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
		c.emit(bytecode.OpTryExitFinally, uint32(tryIdx))
	}
	return nil
}

func (c *Compiler) compileSwitch(s *ast.SwitchStmt) error {
	c.pushBlock()
	defer c.popBlock()
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

	// Switch acts as a break target.
	c.pushLoop(0, 0)
	c.topLoop().breakTarget = endPC
	c.patchLoopBreaks(endPC)
	c.popLoop()
	return nil
}

func (c *Compiler) compileLabeled(s *ast.LabeledStmt) error {
	// Simplified: execute body; labeled break patches filter by label.
	return c.compileStmt(s.Body)
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
		c.emit(bytecode.OpLoadLocal, 0)
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
		return c.compileFunction(funcNameFromExpr(n), n.Params, n.Defaults, n.RestParam, n.Body, n.IsAsync, n.IsGenerator)
	case *ast.ArrowFunc:
		return c.compileFunction("", n.Params, n.Defaults, n.RestParam, n.Body, n.IsAsync, false)
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
		c.emit(bytecode.OpNewObject, 0)
		srcName := c.cur().tmpl.AddStringConst("source")
		srcIdx := c.cur().tmpl.AddStringConst(n.Pattern)
		c.emit(bytecode.OpPushConst, uint32(srcIdx))
		c.emit(bytecode.OpSetPropObj, uint32(srcName))
		flagsName := c.cur().tmpl.AddStringConst("flags")
		flagsIdx := c.cur().tmpl.AddStringConst(n.Flags)
		c.emit(bytecode.OpPushConst, uint32(flagsIdx))
		c.emit(bytecode.OpSetPropObj, uint32(flagsName))
		return nil
	case *ast.TemplateLit:
		return c.compileTemplateLit(n)
	case *ast.YieldExpr:
		return c.compileYield(n)
	}
	return fmt.Errorf("unsupported expression %T", e)
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
//   yield        -> push undefined; OpYield
//   yield expr   -> compile expr; OpYield
//   yield* expr  -> compile expr; OpGetIterator; loop { next(); if done push
//                  value & break; else OpYield (discard sent value) }
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
	// Compute new value (for prefix, the value is on top; for postfix, we
	// duplicated so new value is on top with old below).
	c.emit(bytecode.OpPushInt, 1)
	if n.Op == "++" {
		c.emit(bytecode.OpAdd, 0)
	} else {
		c.emit(bytecode.OpSub, 0)
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
	// compound: desugar a OP= b → a = a OP b
	if err := c.compileExpr(n.Left); err != nil {
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
	}
	return fmt.Errorf("invalid assignment target %T", ref)
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
			if !hasSpread {
				for _, a := range n.Arguments {
					if err := c.compileExpr(a); err != nil {
						return err
					}
				}
				nameIdx := c.cur().tmpl.AddStringConst(m.Property.(*ast.Identifier).Name)
				c.emit(bytecode.OpGetProp, uint32(nameIdx))
				c.emit(bytecode.OpCallThis, uint32(len(n.Arguments)))
				return nil
			}
			c.compileArgsArray(n.Arguments)
			nameIdx := c.cur().tmpl.AddStringConst(m.Property.(*ast.Identifier).Name)
			c.emit(bytecode.OpGetProp, uint32(nameIdx))
			c.emit(bytecode.OpCallThisArgs, 0)
			return nil
		}
	}

	// Method call: foo.bar(args) — keep receiver as `this`.
	if m, ok := n.Callee.(*ast.MemberExpr); ok {
		if m.Computed {
			return fmt.Errorf("computed method call not supported in 1B MVP")
		}
		if err := c.compileExpr(m.Object); err != nil {
			return err
		}
		if !hasSpread {
			// Fast path: args inline.
			for _, a := range n.Arguments {
				if err := c.compileExpr(a); err != nil {
					return err
				}
			}
			nameIdx := c.cur().tmpl.AddStringConst(m.Property.(*ast.Identifier).Name)
			operand := uint32(len(n.Arguments))<<16 | uint32(nameIdx&0xFFFF)
			c.emit(bytecode.OpCallMethod, operand)
			return nil
		}
		// Slow path: build args array, then OpCallMethodArgs.
		c.compileArgsArray(n.Arguments)
		nameIdx := c.cur().tmpl.AddStringConst(m.Property.(*ast.Identifier).Name)
		c.emit(bytecode.OpCallMethodArgs, uint32(nameIdx))
		return nil
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
		return nil
	}
	c.compileArgsArray(n.Arguments)
	c.emit(bytecode.OpCallArgs, 0)
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
	if err := c.compileExpr(n.Object); err != nil {
		return err
	}
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
func (c *Compiler) compileFunction(name string, params []*ast.Identifier, defaults []ast.Expression, rest *ast.Identifier, body ast.Node, isAsync, isGenerator bool) error {
	if isAsync {
		// Async needs Promise (Phase 1C/1D); compile as regular function.
	}
	// `this` slot = 0; params = slots 1..N; rest param (if any) = slot N+1;
	// locals start after that.
	numLocals := 1 + len(params)
	if rest != nil {
		numLocals++
	}
	tmpl := &bytecode.FuncTemplate{
		Name:        name,
		NumParams:   len(params),
		NumLocals:   numLocals,
		IsVarArgs:   rest != nil,
		IsGenerator: isGenerator,
		SourceFile:  c.cur().tmpl.SourceFile,
	}
	funcIdx := c.module.AddFunction(tmpl)

	fc := &funcCtx{
		tmpl:         tmpl,
		upvalueIndex: make(map[string]int),
	}
	fc.scopes = []*scope{{decls: make(map[string]int), isFunc: true}}
	fc.scopes[0].decls["__this__"] = 0
	for i, p := range params {
		fc.scopes[0].decls[p.Name] = i + 1
	}
	if rest != nil {
		fc.scopes[0].decls[rest.Name] = 1 + len(params)
	}
	c.funcStack = append(c.funcStack, fc)

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

	switch b := body.(type) {
	case *ast.BlockStmt:
		c.hoistFunc(b)
		if err := c.compileStmts(b.Body); err != nil {
			return err
		}
	case ast.Expression:
		if err := c.compileExpr(b); err != nil {
			return err
		}
		c.emit(bytecode.OpReturn, 0)
	}
	c.emit(bytecode.OpReturnUndef, 0)
	c.funcStack = c.funcStack[:len(c.funcStack)-1]

	// Emit OpMakeClosure in the enclosing function.
	c.emit(bytecode.OpMakeClosure, uint32(funcIdx))
	return nil
}

// hoistFunc pre-declares var/function declarations in the function scope.
func (c *Compiler) hoistFunc(body *ast.BlockStmt) {
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
			case *ast.ForOfStmt:
				if vd, ok := st.Left.(*ast.VarDecl); ok && vd.Kind == "var" {
					c.hoistVarDeclarators(vd.Decls)
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

// === helpers ==============================================================

func (c *Compiler) emit(op bytecode.Opcode, operand uint32) int {
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
	return c.compileClass(name, e.SuperClass, e.Body)
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

	// Compile the constructor (or synthesize a default).
	hasCtor := false
	for _, m := range body.Methods {
		if m.Kind == ast.MethodConstructor {
			idx, err := c.compileMethod("constructor", m.Value)
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
			idx, err = c.compileDefaultDerivedCtor()
		} else {
			idx, err = c.compileDefaultBaseCtor()
		}
		if err != nil {
			return err
		}
		classTpl.CtorIdx = idx
	}

	// Compile non-constructor methods.
	for _, m := range body.Methods {
		if m.Kind == ast.MethodConstructor {
			continue
		}
		if m.Computed {
			return fmt.Errorf("computed class method names not supported in 1C MVP")
		}
		methodName := propKey(m.Key)
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
	return nil
}

// compileMethod compiles a class method body into a FuncTemplate and returns
// its index. Unlike compileFunction, it does NOT emit OpMakeClosure — the
// class assembler (OpMakeClass) creates the closure at runtime.
func (c *Compiler) compileMethod(name string, fn *ast.FunctionExpr) (int, error) {
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
		IsGenerator: fn.IsGenerator,
		SourceFile:  c.cur().tmpl.SourceFile,
	}
	funcIdx := c.module.AddFunction(tmpl)

	fc := &funcCtx{
		tmpl:         tmpl,
		upvalueIndex: make(map[string]int),
	}
	fc.scopes = []*scope{{decls: make(map[string]int), isFunc: true}}
	fc.scopes[0].decls["__this__"] = 0
	for i, p := range params {
		fc.scopes[0].decls[p.Name] = i + 1
	}
	if rest != nil {
		fc.scopes[0].decls[rest.Name] = 1 + len(params)
	}
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

	c.hoistFunc(fn.Body)
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
	}
	funcIdx := c.module.AddFunction(tmpl)
	fc := &funcCtx{
		tmpl:         tmpl,
		upvalueIndex: make(map[string]int),
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
		NumLocals:  2, // slot 0 = this, slot 1 = rest "args"
		IsVarArgs:  true,
		SourceFile: c.cur().tmpl.SourceFile,
	}
	funcIdx := c.module.AddFunction(tmpl)
	fc := &funcCtx{
		tmpl:         tmpl,
		upvalueIndex: make(map[string]int),
	}
	fc.scopes = []*scope{{decls: make(map[string]int), isFunc: true}}
	fc.scopes[0].decls["__this__"] = 0
	fc.scopes[0].decls["args"] = 1
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
