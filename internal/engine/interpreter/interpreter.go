package interpreter

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/lexer"
	"github.com/aluka-lang/aluka/internal/engine/parser"
)

// Engine implements engine.Engine using an AST-walking interpreter.
// 说明（P0-4）：AST-walking 解释器长期未与 parser/compiler 同步，无法处理
// ES2015+ 语法（class/解构等会 panic）。`--ast` 现已复用字节码 VM 引擎，
// 保证功能完整；Engine 类型保留以维持引擎抽象接口不变。
type Engine struct{}

// NewEngine creates a new interpreter engine.
func NewEngine() *Engine { return &Engine{} }

// NewContext 复用字节码 VM 引擎（P0-4：AST 解释器已废弃为 CLI 引擎路径）。
func (e *Engine) NewContext() (engine.Context, error) {
	return NewVM()
}

func (e *Engine) Shutdown() error { return nil }
func (e *Engine) Version() string { return "aluka-interpreter-1A" }

// VMEngine implements engine.Engine using the bytecode VM (Phase 1B).
type VMEngine struct{}

// NewVMEngine creates a new VM-based engine.
func NewVMEngine() *VMEngine { return &VMEngine{} }

func (e *VMEngine) NewContext() (engine.Context, error) {
	return NewVM()
}

func (e *VMEngine) Shutdown() error { return nil }
func (e *VMEngine) Version() string { return "aluka-vm-1B" }

// Interpreter is an AST-walking JS execution context.
type Interpreter struct {
	global    *Scope
	globalObj engine.Object

	// Built-in prototypes
	objectProto   engine.Object
	arrayProto    engine.Object
	functionProto engine.Object
	stringProto   engine.Object
	numberProto   engine.Object
	booleanProto  engine.Object
	bigintProto   engine.Object
	errorProto    engine.Object
	promiseProto  engine.Object
	mapProto      engine.Object
	setProto      engine.Object
	weakMapProto  engine.Object
	weakSetProto  engine.Object
	regexpProto   engine.Object

	// Built-in constructors
	constructors map[string]engine.Object

	argumentsSupported bool

	// microtaskQueue holds pending microtasks (Promise reactions, queueMicrotask).
	microtaskQueue []func()

	// currentVM is the VM currently executing on this interpreter (set in
	// runModule). Used by native callbacks (Proxy traps, etc.) that need to
	// invoke JS functions through the VM.
	currentVM *VM

	// 事件循环（node:http / 定时器基础设施）：
	// taskCh 接收从任意 goroutine 投递的任务，由 RunLoop 在 JS 执行 goroutine 上执行。
	taskCh chan func()
	// active 是活跃句柄计数（已投递任务 + 活跃定时器/服务器）；归零时
	// idleCh 收到信号，RunLoop 据此退出。loopDone 标记循环已结束。
	active   int
	idleCh   chan struct{}
	stopCh   chan struct{}
	loopOnce sync.Once // 确保 RunLoop 只启动一次
	loopMu   sync.Mutex
	loopDone bool
}

// NewInterpreter creates an interpreter with built-in globals set up.
func NewInterpreter() (*Interpreter, error) {
	interp := &Interpreter{
		global:             NewScope(),
		globalObj:          engine.NewObject(),
		constructors:       make(map[string]engine.Object),
		argumentsSupported: true,
		taskCh:             make(chan func(), 64),
		idleCh:             make(chan struct{}, 1),
		stopCh:             make(chan struct{}),
	}
	interp.setupBuiltins()
	interp.setupGlobalThis()
	return interp, nil
}

// Eval parses and executes JS source code.
func (interp *Interpreter) Eval(code string, filename string) (engine.Value, error) {
	prog, err := parser.Parse(code)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("%w: %s: %v", engine.ErrSyntaxError, filename, err)
	}
	// Hoist declarations to global scope
	interp.hoist(prog.Body, interp.global)

	var result engine.Value = engine.Undefined()
	for _, stmt := range prog.Body {
		val, err := interp.execStmt(stmt, interp.global)
		if err != nil {
			// Convert Go errors to JS errors for top-level reporting
			if _, isCF := isControlFlow(err); isCF {
				return engine.Undefined(), err
			}
			if je, ok := err.(*jsError); ok {
				return engine.Undefined(), fmt.Errorf("%s", je.value.String())
			}
			return engine.Undefined(), fmt.Errorf("%s", err.Error())
		}
		if val != nil {
			result = val
		}
	}
	return result, nil
}

func (interp *Interpreter) Global() engine.Object { return interp.globalObj }

func (interp *Interpreter) RegisterFunc(name string, fn engine.Func) error {
	return interp.globalObj.Set(name, engine.NewFunction(name, fn))
}

func (interp *Interpreter) Close() error { return nil }

// newArray creates an ArrayValue with the interpreter's arrayProto attached,
// so array methods (join, push, map, ...) are reachable via the prototype
// chain. All builtin functions that return arrays should use this helper
// instead of engine.NewArray directly.
func (interp *Interpreter) newArray(elems []engine.Value) engine.Value {
	arr := engine.NewArray(elems)
	engine.SetProto(arr, interp.arrayProto)
	return arr
}

// === Hoisting =============================================================

// hoist pre-declares var and function declarations in the given scope.
func (interp *Interpreter) hoist(stmts []ast.Statement, scope *Scope) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.VarDecl:
			if s.Kind == "var" {
				for _, d := range s.Decls {
					if !scope.HasOwn(d.Name.Name) {
						scope.Declare(d.Name.Name, engine.Undefined())
					}
				}
			}
		case *ast.FunctionDecl:
			if s.Name != nil {
				c := newClosure(interp, scope, s.Params, s.Defaults, s.RestParam, s.Body, s.Name.Name, false)
				scope.Declare(s.Name.Name, c)
			}
		case *ast.BlockStmt:
			// Don't recurse into blocks for var hoisting (var is function-scoped,
			// but for simplicity we hoist within the current function scope).
			interp.hoist(s.Body, scope)
		case *ast.IfStmt:
			interp.hoist([]ast.Statement{s.Consequent}, scope)
			if s.Alternate != nil {
				interp.hoist([]ast.Statement{s.Alternate}, scope)
			}
		case *ast.ForStmt:
			if vd, ok := s.Init.(*ast.VarDecl); ok && vd.Kind == "var" {
				for _, d := range vd.Decls {
					if !scope.HasOwn(d.Name.Name) {
						scope.Declare(d.Name.Name, engine.Undefined())
					}
				}
			}
			if s.Body != nil {
				interp.hoist([]ast.Statement{s.Body}, scope)
			}
		case *ast.WhileStmt:
			if s.Body != nil {
				interp.hoist([]ast.Statement{s.Body}, scope)
			}
		case *ast.DoWhileStmt:
			if s.Body != nil {
				interp.hoist([]ast.Statement{s.Body}, scope)
			}
		case *ast.ForInStmt:
			if vd, ok := s.Left.(*ast.VarDecl); ok && vd.Kind == "var" {
				for _, d := range vd.Decls {
					if !scope.HasOwn(d.Name.Name) {
						scope.Declare(d.Name.Name, engine.Undefined())
					}
				}
			}
		case *ast.ForOfStmt:
			if vd, ok := s.Left.(*ast.VarDecl); ok && vd.Kind == "var" {
				for _, d := range vd.Decls {
					if !scope.HasOwn(d.Name.Name) {
						scope.Declare(d.Name.Name, engine.Undefined())
					}
				}
			}
		case *ast.TryStmt:
			interp.hoist(s.Block.Body, scope)
			if s.Handler != nil && s.Handler.Body != nil {
				interp.hoist(s.Handler.Body.Body, scope)
			}
			if s.Finally != nil {
				interp.hoist(s.Finally.Body, scope)
			}
		case *ast.SwitchStmt:
			for _, c := range s.Cases {
				interp.hoist(c.Consequent, scope)
			}
		}
	}
}

// === Statement execution ==================================================

// controlFlow carries return/break/continue signals up the call stack.
type controlFlow struct {
	kind  string // "return", "break", "continue"
	value engine.Value
	label string
}

func (e *controlFlow) Error() string { return e.kind }

func isControlFlow(err error) (*controlFlow, bool) {
	cf, ok := err.(*controlFlow)
	return cf, ok
}

// execStmt executes a statement, returning the expression result (or nil).
func (interp *Interpreter) execStmt(stmt ast.Statement, scope *Scope) (engine.Value, error) {
	switch s := stmt.(type) {
	case *ast.VarDecl:
		return interp.execVarDecl(s, scope)
	case *ast.FunctionDecl:
		// Already hoisted; just ensure it's in scope
		if s.Name != nil {
			if !scope.HasOwn(s.Name.Name) {
				c := newClosure(interp, scope, s.Params, s.Defaults, s.RestParam, s.Body, s.Name.Name, false)
				scope.Declare(s.Name.Name, c)
			}
		}
		return nil, nil
	case *ast.BlockStmt:
		return interp.execBlock(s, scope.NewChild())
	case *ast.ExprStmt:
		return interp.evalExpr(s.Expr, scope)
	case *ast.EmptyStmt:
		return nil, nil
	case *ast.IfStmt:
		return interp.execIf(s, scope)
	case *ast.WhileStmt:
		return interp.execWhile(s, scope)
	case *ast.DoWhileStmt:
		return interp.execDoWhile(s, scope)
	case *ast.ForStmt:
		return interp.execFor(s, scope)
	case *ast.ForInStmt:
		return interp.execForIn(s, scope)
	case *ast.ForOfStmt:
		return interp.execForOf(s, scope)
	case *ast.ReturnStmt:
		var val engine.Value = engine.Undefined()
		if s.Arg != nil {
			v, err := interp.evalExpr(s.Arg, scope)
			if err != nil {
				return nil, err
			}
			val = v
		}
		return nil, &controlFlow{kind: "return", value: val}
	case *ast.BreakStmt:
		return nil, &controlFlow{kind: "break", label: s.Label}
	case *ast.ContinueStmt:
		return nil, &controlFlow{kind: "continue", label: s.Label}
	case *ast.ThrowStmt:
		v, err := interp.evalExpr(s.Arg, scope)
		if err != nil {
			return nil, err
		}
		return nil, &jsError{value: v}
	case *ast.TryStmt:
		return interp.execTry(s, scope)
	case *ast.SwitchStmt:
		return interp.execSwitch(s, scope)
	case *ast.LabeledStmt:
		// Execute body; break with matching label exits the statement
		_, err := interp.execStmt(s.Body, scope)
		if err != nil {
			if cf, ok := isControlFlow(err); ok && cf.kind == "break" && cf.label == s.Label {
				return nil, nil
			}
		}
		return nil, err
	}
	return nil, fmt.Errorf("%w: unexpected statement type %T", engine.ErrNotImplemented, stmt)
}

func (interp *Interpreter) execBlock(block *ast.BlockStmt, scope *Scope) (engine.Value, error) {
	interp.hoist(block.Body, scope)
	var result engine.Value
	for _, stmt := range block.Body {
		val, err := interp.execStmt(stmt, scope)
		if err != nil {
			return nil, err
		}
		if val != nil {
			result = val
		}
	}
	return result, nil
}

func (interp *Interpreter) execVarDecl(d *ast.VarDecl, scope *Scope) (engine.Value, error) {
	for _, decl := range d.Decls {
		var val engine.Value = engine.Undefined()
		if decl.Init != nil {
			v, err := interp.evalExpr(decl.Init, scope)
			if err != nil {
				return nil, err
			}
			val = v
		}
		if d.Kind == "var" {
			// var is function-scoped; walk up to find existing binding
			if !scope.Set(decl.Name.Name, val) {
				scope.Declare(decl.Name.Name, val)
			}
		} else {
			// let/const are block-scoped
			scope.Declare(decl.Name.Name, val)
		}
	}
	return nil, nil
}

func (interp *Interpreter) execIf(s *ast.IfStmt, scope *Scope) (engine.Value, error) {
	test, err := interp.evalExpr(s.Test, scope)
	if err != nil {
		return nil, err
	}
	cond, _ := test.Bool()
	if cond {
		return interp.execStmt(s.Consequent, scope)
	}
	if s.Alternate != nil {
		return interp.execStmt(s.Alternate, scope)
	}
	return nil, nil
}

func (interp *Interpreter) execWhile(s *ast.WhileStmt, scope *Scope) (engine.Value, error) {
	for {
		test, err := interp.evalExpr(s.Test, scope)
		if err != nil {
			return nil, err
		}
		cond, _ := test.Bool()
		if !cond {
			break
		}
		_, err = interp.execStmt(s.Body, scope)
		if err != nil {
			if cf, ok := isControlFlow(err); ok {
				if cf.kind == "break" {
					break
				}
				if cf.kind == "continue" {
					continue
				}
				return nil, err
			}
			return nil, err
		}
	}
	return nil, nil
}

func (interp *Interpreter) execDoWhile(s *ast.DoWhileStmt, scope *Scope) (engine.Value, error) {
	for {
		_, err := interp.execStmt(s.Body, scope)
		if err != nil {
			if cf, ok := isControlFlow(err); ok {
				if cf.kind == "break" {
					break
				}
				if cf.kind == "continue" {
					// fall through to test
				} else {
					return nil, err
				}
			} else {
				return nil, err
			}
		}
		test, err := interp.evalExpr(s.Test, scope)
		if err != nil {
			return nil, err
		}
		cond, _ := test.Bool()
		if !cond {
			break
		}
	}
	return nil, nil
}

func (interp *Interpreter) execFor(s *ast.ForStmt, scope *Scope) (engine.Value, error) {
	loopScope := scope.NewChild()
	if s.Init != nil {
		switch init := s.Init.(type) {
		case *ast.VarDecl:
			if _, err := interp.execVarDecl(init, loopScope); err != nil {
				return nil, err
			}
		case ast.Expression:
			if _, err := interp.evalExpr(init, loopScope); err != nil {
				return nil, err
			}
		}
	}
	for {
		if s.Test != nil {
			test, err := interp.evalExpr(s.Test, loopScope)
			if err != nil {
				return nil, err
			}
			cond, _ := test.Bool()
			if !cond {
				break
			}
		}
		_, err := interp.execStmt(s.Body, loopScope)
		if err != nil {
			if cf, ok := isControlFlow(err); ok {
				if cf.kind == "break" {
					break
				}
				if cf.kind == "continue" {
					// fall through to update
				} else {
					return nil, err
				}
			} else {
				return nil, err
			}
		}
		if s.Update != nil {
			if _, err := interp.evalExpr(s.Update, loopScope); err != nil {
				return nil, err
			}
		}
	}
	return nil, nil
}

func (interp *Interpreter) execForIn(s *ast.ForInStmt, scope *Scope) (engine.Value, error) {
	right, err := interp.evalExpr(s.Right, scope)
	if err != nil {
		return nil, err
	}
	obj, ok := right.AsObject()
	if !ok {
		return nil, nil
	}
	for _, key := range obj.Keys() {
		loopScope := scope.NewChild()
		interp.assignForLeft(s.Left, engine.Str(key), loopScope)
		_, err := interp.execStmt(s.Body, loopScope)
		if err != nil {
			if cf, ok := isControlFlow(err); ok {
				if cf.kind == "break" {
					break
				}
				if cf.kind == "continue" {
					continue
				}
				return nil, err
			}
			return nil, err
		}
	}
	return nil, nil
}

func (interp *Interpreter) execForOf(s *ast.ForOfStmt, scope *Scope) (engine.Value, error) {
	right, err := interp.evalExpr(s.Right, scope)
	if err != nil {
		return nil, err
	}
	// Support arrays and strings
	switch v := right.(type) {
	case *engine.ArrayValue:
		for _, elem := range v.Elems() {
			loopScope := scope.NewChild()
			interp.assignForLeft(s.Left, elem, loopScope)
			_, err := interp.execStmt(s.Body, loopScope)
			if err != nil {
				if cf, ok := isControlFlow(err); ok {
					if cf.kind == "break" {
						break
					}
					if cf.kind == "continue" {
						continue
					}
					return nil, err
				}
				return nil, err
			}
		}
	default:
		if right.Type() == engine.TypeString {
			str := right.String()
			for _, ch := range str {
				loopScope := scope.NewChild()
				interp.assignForLeft(s.Left, engine.Str(string(ch)), loopScope)
				_, err := interp.execStmt(s.Body, loopScope)
				if err != nil {
					if cf, ok := isControlFlow(err); ok {
						if cf.kind == "break" {
							break
						}
						if cf.kind == "continue" {
							continue
						}
						return nil, err
					}
					return nil, err
				}
			}
		}
	}
	return nil, nil
}

func (interp *Interpreter) assignForLeft(left ast.Node, val engine.Value, scope *Scope) {
	switch l := left.(type) {
	case *ast.VarDecl:
		if len(l.Decls) > 0 {
			scope.Declare(l.Decls[0].Name.Name, val)
		}
	case *ast.Identifier:
		scope.Declare(l.Name, val)
	}
}

func (interp *Interpreter) execTry(s *ast.TryStmt, scope *Scope) (engine.Value, error) {
	result, err := interp.execBlock(s.Block, scope.NewChild())
	if err != nil {
		// Skip control flow errors (return/break/continue)
		if _, isCF := isControlFlow(err); isCF {
			if s.Finally != nil {
				_, ferr := interp.execBlock(s.Finally, scope.NewChild())
				if ferr != nil {
					return nil, ferr
				}
			}
			return nil, err
		}
		// Convert to JS error value if not already
		var jsErrVal engine.Value
		if je, ok := err.(*jsError); ok {
			jsErrVal = je.value
		} else {
			jsErrVal = interp.goErrorToJSValue(err)
		}
		// If there's a handler, catch the error
		if s.Handler != nil {
			catchScope := scope.NewChild()
			if s.Handler.Param != nil {
				catchScope.Declare(s.Handler.Param.Name, jsErrVal)
			}
			hresult, herr := interp.execBlock(s.Handler.Body, catchScope)
			if s.Finally != nil {
				_, ferr := interp.execBlock(s.Finally, scope.NewChild())
				if ferr != nil {
					return nil, ferr
				}
			}
			return hresult, herr
		}
		// No handler: run finally and re-throw
		if s.Finally != nil {
			_, ferr := interp.execBlock(s.Finally, scope.NewChild())
			if ferr != nil {
				return nil, ferr
			}
		}
		return nil, &jsError{value: jsErrVal}
	}
	if s.Finally != nil {
		_, ferr := interp.execBlock(s.Finally, scope.NewChild())
		if ferr != nil {
			return nil, ferr
		}
	}
	return result, nil
}

// goErrorToJSValue converts a Go error to a JS Error object.
func (interp *Interpreter) goErrorToJSValue(err error) engine.Value {
	errObj := engine.NewObject()
	engine.SetProto(errObj, interp.errorProto)
	msg := err.Error()
	name := "Error"
	if errors.Is(err, engine.ErrTypeError) {
		name = "TypeError"
		msg = strings.TrimPrefix(msg, "aluka: type error: ")
		if ctor, ok := interp.constructors["TypeError"]; ok {
			if proto, perr := ctor.Get("prototype"); perr == nil {
				if po, ok := proto.(engine.Object); ok {
					engine.SetProto(errObj, po)
				}
			}
		}
	} else if errors.Is(err, engine.ErrReferenceError) {
		name = "ReferenceError"
		msg = strings.TrimPrefix(msg, "aluka: reference error: ")
		if ctor, ok := interp.constructors["ReferenceError"]; ok {
			if proto, perr := ctor.Get("prototype"); perr == nil {
				if po, ok := proto.(engine.Object); ok {
					engine.SetProto(errObj, po)
				}
			}
		}
	} else if errors.Is(err, engine.ErrSyntaxError) {
		name = "SyntaxError"
		msg = strings.TrimPrefix(msg, "aluka: syntax error: ")
		if ctor, ok := interp.constructors["SyntaxError"]; ok {
			if proto, perr := ctor.Get("prototype"); perr == nil {
				if po, ok := proto.(engine.Object); ok {
					engine.SetProto(errObj, po)
				}
			}
		}
	} else if errors.Is(err, engine.ErrRangeError) {
		name = "RangeError"
		msg = strings.TrimPrefix(msg, "aluka: range error: ")
		if ctor, ok := interp.constructors["RangeError"]; ok {
			if proto, perr := ctor.Get("prototype"); perr == nil {
				if po, ok := proto.(engine.Object); ok {
					engine.SetProto(errObj, po)
				}
			}
		}
	}
	_ = errObj.Set("name", engine.Str(name))
	_ = errObj.Set("message", engine.Str(msg))
	// 携带 Node 风格错误码的错误（如 ERR_PARSE_ARGS_UNKNOWN_OPTION）→ err.code。
	if ce, ok := err.(interface{ Code() string }); ok {
		_ = errObj.Set("code", engine.Str(ce.Code()))
	}
	// 系统错误（*os.PathError/*os.LinkError/*fs.PathError）：Node 风格
	// code/errno/path 与 message（如 "ENOENT: no such file or directory, open 'x'"）。
	if pe, ok := asPathError(err); ok {
		code, desc, errnoNum := nodeErrnoInfo(pe.Err)
		if code != "" {
			_ = errObj.Set("code", engine.Str(code))
			_ = errObj.Set("errno", engine.IntValue(errnoNum))
			op := pe.Op
			if op == "" {
				op = "syscall"
			}
			msg = fmt.Sprintf("%s: %s, %s '%s'", code, desc, op, pe.Path)
			_ = errObj.Set("message", engine.Str(msg))
			_ = errObj.Set("path", engine.Str(pe.Path))
			_ = errObj.Set("syscall", engine.Str(op))
		}
	}
	// exec 类错误：status/killed（execFileSync/execSync 非零退出与超时）。
	if se, ok := err.(interface{ Status() int }); ok {
		_ = errObj.Set("status", engine.IntValue(se.Status()))
	}
	if ke, ok := err.(interface{ Killed() bool }); ok {
		_ = errObj.Set("killed", engine.Boolean(ke.Killed()))
	}
	// VM/runtime failures converted to JavaScript errors should expose the same
	// V8-style stack property as errors constructed from JavaScript.
	interp.setErrorStack(errObj)
	return errObj
}

func (interp *Interpreter) execSwitch(s *ast.SwitchStmt, scope *Scope) (engine.Value, error) {
	disc, err := interp.evalExpr(s.Disc, scope)
	if err != nil {
		return nil, err
	}
	matched := false
	for i, c := range s.Cases {
		if !matched {
			if c.Test == nil {
				// default: only match if no case matched and we're past all cases
				// (simplified: match default only if no prior case matched)
				// Check if any later case matches
				anyMatch := false
				for j := i + 1; j < len(s.Cases); j++ {
					if s.Cases[j].Test != nil {
						tv, err := interp.evalExpr(s.Cases[j].Test, scope)
						if err != nil {
							return nil, err
						}
						if strictEqual(disc, tv) {
							anyMatch = true
							break
						}
					}
				}
				if anyMatch {
					continue
				}
				matched = true
			} else {
				tv, err := interp.evalExpr(c.Test, scope)
				if err != nil {
					return nil, err
				}
				if strictEqual(disc, tv) {
					matched = true
				}
			}
		}
		if matched {
			for _, stmt := range c.Consequent {
				_, err := interp.execStmt(stmt, scope)
				if err != nil {
					if _, ok := isControlFlow(err); ok {
						return nil, err
					}
					return nil, err
				}
			}
		}
	}
	return nil, nil
}

// === Expression evaluation ================================================

func (interp *Interpreter) evalExpr(expr ast.Expression, scope *Scope) (engine.Value, error) {
	switch e := expr.(type) {
	case *ast.NumberLit:
		return engine.Number(e.Value), nil
	case *ast.StringLit:
		return engine.Str(e.Value), nil
	case *ast.BoolLit:
		return engine.Boolean(e.Value), nil
	case *ast.NullLit:
		return engine.Null(), nil
	case *ast.UndefinedLit:
		return engine.Undefined(), nil
	case *ast.TemplateLit:
		// Concatenate quasis with interpolated expression values.
		var b strings.Builder
		b.WriteString(e.Quasis[0])
		for i, expr := range e.Expressions {
			val, err := interp.evalExpr(expr, scope)
			if err != nil {
				return nil, err
			}
			b.WriteString(val.String())
			b.WriteString(e.Quasis[i+1])
		}
		return engine.Str(b.String()), nil
	case *ast.TaggedTemplateExpr:
		// tag`a${x}b`：构造 TemplateStringsArray（含 .raw），以 undefined 为 this 调用 tag。
		tag, err := interp.evalExpr(e.Tag, scope)
		if err != nil {
			return nil, err
		}
		quasis := make([]engine.Value, 0, len(e.Template.Quasis))
		for _, q := range e.Template.Quasis {
			quasis = append(quasis, engine.Str(q))
		}
		strs := engine.NewArray(quasis)
		rawQuasis := make([]engine.Value, 0, len(e.Template.RawQuasis))
		for _, rq := range e.Template.RawQuasis {
			rawQuasis = append(rawQuasis, engine.Str(rq))
		}
		if err := strs.Set("raw", engine.NewArray(rawQuasis)); err != nil {
			return nil, err
		}
		args := []engine.Value{strs}
		for _, ex := range e.Template.Expressions {
			v, err := interp.evalExpr(ex, scope)
			if err != nil {
				return nil, err
			}
			args = append(args, v)
		}
		callable, err := asCallable(tag)
		if err != nil {
			return nil, err
		}
		return callable.callWith(engine.Undefined(), args)
	case *ast.RegexLit:
		// 正则字面量：构造真实 RegExp 实例（Go regexp 翻译层内核）。
		return interp.makeRegexp(e.Pattern, e.Flags)
	case *ast.Identifier:
		if v, ok := scope.Get(e.Name); ok {
			return v, nil
		}
		// Check global object
		if v, err := interp.globalObj.Get(e.Name); err == nil && !v.IsUndefined() {
			return v, nil
		}
		// Get 对缺失属性也返回 Undefined：globalObj 上显式存在但值为
		// undefined 的属性（如全局 undefined 本身）需与"缺失"区分。
		if interp.globalHas(e.Name) {
			return engine.Undefined(), nil
		}
		return nil, fmt.Errorf("%w: %s is not defined", engine.ErrReferenceError, e.Name)
	case *ast.ThisExpr:
		if v, ok := scope.Get("__this__"); ok {
			return v, nil
		}
		return engine.Undefined(), nil
	case *ast.ArrayLit:
		return interp.evalArrayLit(e, scope)
	case *ast.ObjectLit:
		return interp.evalObjectLit(e, scope)
	case *ast.MemberExpr:
		return interp.evalMember(e, scope)
	case *ast.CallExpr:
		return interp.evalCall(e, scope)
	case *ast.NewExpr:
		return interp.evalNew(e, scope)
	case *ast.UnaryExpr:
		return interp.evalUnary(e, scope)
	case *ast.UpdateExpr:
		return interp.evalUpdate(e, scope)
	case *ast.BinaryExpr:
		return interp.evalBinary(e, scope)
	case *ast.LogicalExpr:
		return interp.evalLogical(e, scope)
	case *ast.AssignExpr:
		return interp.evalAssign(e, scope)
	case *ast.ConditionalExpr:
		return interp.evalConditional(e, scope)
	case *ast.SequenceExpr:
		return interp.evalSequence(e, scope)
	case *ast.FunctionExpr:
		c := newClosure(interp, scope, e.Params, e.Defaults, e.RestParam, e.Body, funcName(e.Name), false)
		return c, nil
	case *ast.ArrowFunc:
		c := newClosure(interp, scope, e.Params, e.Defaults, e.RestParam, e.Body, "", true)
		return c, nil
	case *ast.SuperExpr:
		return nil, fmt.Errorf("%w: super is not supported in Phase 1A", engine.ErrNotImplemented)
	case *ast.NewTargetExpr:
		return nil, fmt.Errorf("%w: new.target is not supported in Phase 1A", engine.ErrNotImplemented)
	}
	return nil, fmt.Errorf("%w: unexpected expression type %T", engine.ErrNotImplemented, expr)
}

func funcName(id *ast.Identifier) string {
	if id == nil {
		return ""
	}
	return id.Name
}

func (interp *Interpreter) evalArrayLit(e *ast.ArrayLit, scope *Scope) (engine.Value, error) {
	var elems []engine.Value
	for _, el := range e.Elements {
		if el == nil {
			elems = append(elems, engine.Undefined())
			continue
		}
		v, err := interp.evalExpr(el, scope)
		if err != nil {
			return nil, err
		}
		elems = append(elems, v)
	}
	arr := engine.NewArray(elems)
	engine.SetProto(arr, interp.arrayProto)
	return arr, nil
}

func (interp *Interpreter) evalObjectLit(e *ast.ObjectLit, scope *Scope) (engine.Value, error) {
	obj := engine.NewObject()
	engine.SetProto(obj, interp.objectProto)
	for _, prop := range e.Properties {
		if prop.Kind == ast.PropertySpread {
			// Spread: copy enumerable own properties
			v, err := interp.evalExpr(prop.Value, scope)
			if err != nil {
				return nil, err
			}
			if src, ok := v.AsObject(); ok {
				for _, k := range src.Keys() {
					sv, _ := src.Get(k)
					_ = obj.Set(k, sv)
				}
			}
			continue
		}
		var key string
		switch k := prop.Key.(type) {
		case *ast.Identifier:
			key = k.Name
		case *ast.StringLit:
			key = k.Value
		case *ast.NumberLit:
			key = k.Raw
		default:
			if prop.Computed {
				kv, err := interp.evalExpr(prop.Key, scope)
				if err != nil {
					return nil, err
				}
				key = kv.String()
			} else {
				return nil, fmt.Errorf("%w: invalid object key", engine.ErrSyntaxError)
			}
		}
		v, err := interp.evalExpr(prop.Value, scope)
		if err != nil {
			return nil, err
		}
		// get/set 访问器：注册为 accessor（而非普通属性）。
		if prop.Kind == ast.PropertyGet || prop.Kind == ast.PropertySet {
			engine.UpdateAccessor(obj, key, prop.Kind == ast.PropertyGet, v)
			continue
		}
		_ = obj.Set(key, v)
	}
	return obj, nil
}

func (interp *Interpreter) evalMember(e *ast.MemberExpr, scope *Scope) (engine.Value, error) {
	obj, err := interp.evalExpr(e.Object, scope)
	if err != nil {
		return nil, err
	}
	// Optional chaining: if the object is null/undefined, short-circuit to
	// undefined without attempting property access (no TypeError thrown).
	if e.Optional && (obj.IsNull() || obj.IsUndefined()) {
		return engine.Undefined(), nil
	}
	var key string
	if e.Computed {
		kv, err := interp.evalExpr(e.Property, scope)
		if err != nil {
			return nil, err
		}
		key = propertyKeyOf(kv)
	} else {
		if id, ok := e.Property.(*ast.Identifier); ok {
			key = id.Name
		} else {
			return nil, fmt.Errorf("%w: invalid member property", engine.ErrTypeError)
		}
	}
	return interp.getProperty(obj, key)
}

// getProperty retrieves a property from a value, handling primitives and prototype chain.
func (interp *Interpreter) getProperty(obj engine.Value, key string) (engine.Value, error) {
	// null and undefined throw TypeError on property access
	if obj.IsNull() || obj.IsUndefined() {
		return nil, fmt.Errorf("%w: Cannot read properties of %s (reading '%s')", engine.ErrTypeError, obj.String(), key)
	}
	if o, ok := obj.AsObject(); ok {
		return o.Get(key)
	}
	// Primitive string/number/boolean: look up on prototype
	switch obj.Type() {
	case engine.TypeString:
		if key == "length" {
			n, _ := engine.StringLen(obj)
			return engine.IntValue(n), nil
		}
		if n, err := strconv.Atoi(key); err == nil {
			if unit, ok := jsStringUnitAt(obj.String(), n); ok {
				return engine.Str(unit), nil
			}
			return engine.Undefined(), nil
		}
		if interp.stringProto != nil {
			return interp.stringProto.Get(key)
		}
	case engine.TypeNumber:
		if interp.numberProto != nil {
			return interp.numberProto.Get(key)
		}
	case engine.TypeBoolean:
		if interp.booleanProto != nil {
			return interp.booleanProto.Get(key)
		}
	case engine.TypeBigInt:
		if interp.bigintProto != nil {
			return interp.bigintProto.Get(key)
		}
	}
	return engine.Undefined(), nil
}

// globalHas 判断 name 是否为 globalObj 的自有属性。
// 用于区分"属性缺失"与"属性存在但值为 undefined"（Get 对两者都返回 Undefined）。
func (interp *Interpreter) globalHas(name string) bool {
	for _, k := range interp.globalObj.Keys() {
		if k == name {
			return true
		}
	}
	return false
}

func (interp *Interpreter) evalCall(e *ast.CallExpr, scope *Scope) (engine.Value, error) {
	// Determine `this` and the function
	var thisVal engine.Value = engine.Undefined()
	var callee engine.Value
	var err error

	if member, ok := e.Callee.(*ast.MemberExpr); ok {
		receiver, err := interp.evalExpr(member.Object, scope)
		if err != nil {
			return nil, err
		}
		// Optional member access short-circuit: a?.b() / a?.b?.()
		if member.Optional && (receiver.IsNull() || receiver.IsUndefined()) {
			return engine.Undefined(), nil
		}
		var key string
		if member.Computed {
			kv, err := interp.evalExpr(member.Property, scope)
			if err != nil {
				return nil, err
			}
			key = kv.String()
		} else if id, ok := member.Property.(*ast.Identifier); ok {
			key = id.Name
		}
		fn, err := interp.getProperty(receiver, key)
		if err != nil {
			return nil, err
		}
		// Optional call short-circuit: a.b?.() — skip call if method is nullish
		if e.Optional && (fn.IsNull() || fn.IsUndefined()) {
			return engine.Undefined(), nil
		}
		callee = fn
		thisVal = receiver
	} else {
		callee, err = interp.evalExpr(e.Callee, scope)
		if err != nil {
			return nil, err
		}
		// Optional call short-circuit: f?.()
		if e.Optional && (callee.IsNull() || callee.IsUndefined()) {
			return engine.Undefined(), nil
		}
	}

	callable, err := asCallable(callee)
	if err != nil {
		return nil, err
	}

	args, err := interp.evalArgs(e.Arguments, scope)
	if err != nil {
		return nil, err
	}

	return callable.callWith(thisVal, args)
}

func (interp *Interpreter) evalNew(e *ast.NewExpr, scope *Scope) (engine.Value, error) {
	callee, err := interp.evalExpr(e.Callee, scope)
	if err != nil {
		return nil, err
	}
	callable, err := asCallable(callee)
	if err != nil {
		return nil, err
	}
	args, err := interp.evalArgs(e.Arguments, scope)
	if err != nil {
		return nil, err
	}
	return callable.construct(args)
}

func (interp *Interpreter) evalArgs(args []ast.Expression, scope *Scope) ([]engine.Value, error) {
	var result []engine.Value
	for _, arg := range args {
		if spread, ok := arg.(*ast.SpreadElement); ok {
			v, err := interp.evalExpr(spread.Arg, scope)
			if err != nil {
				return nil, err
			}
			if arr, ok := v.(*engine.ArrayValue); ok {
				result = append(result, arr.Elems()...)
			} else {
				result = append(result, v)
			}
			continue
		}
		v, err := interp.evalExpr(arg, scope)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, nil
}

func (interp *Interpreter) evalUnary(e *ast.UnaryExpr, scope *Scope) (engine.Value, error) {
	if e.Op == "typeof" {
		// typeof doesn't throw on undefined identifiers
		if id, ok := e.Arg.(*ast.Identifier); ok {
			if v, ok := scope.Get(id.Name); ok {
				return engine.Str(v.Type().String()), nil
			}
			// Fall back to the global object (built-ins like Error, Object)
			if v, err := interp.globalObj.Get(id.Name); err == nil && !v.IsUndefined() {
				return engine.Str(v.Type().String()), nil
			}
			return engine.Str("undefined"), nil
		}
	}
	arg, err := interp.evalExpr(e.Arg, scope)
	if err != nil {
		return nil, err
	}
	switch e.Op {
	case "!":
		b, _ := arg.Bool()
		return engine.Boolean(!b), nil
	case "-":
		f, _ := arg.Float()
		return engine.Number(-f), nil
	case "+":
		return engine.Number(jsToNumber(arg)), nil
	case "~":
		n := toInt32(arg)
		return engine.Number(float64(^n)), nil
	case "typeof":
		return engine.Str(arg.Type().String()), nil
	case "void":
		return engine.Undefined(), nil
	case "delete":
		// delete obj.prop / delete obj[key]
		if member, ok := e.Arg.(*ast.MemberExpr); ok {
			obj, err := interp.evalExpr(member.Object, scope)
			if err != nil {
				return nil, err
			}
			if o, ok := obj.AsObject(); ok {
				var key string
				if id, ok := member.Property.(*ast.Identifier); ok {
					key = id.Name
				} else {
					kv, err := interp.evalExpr(member.Property, scope)
					if err != nil {
						return nil, err
					}
					key = kv.String()
				}
				o.Delete(key)
			}
		}
		return engine.Boolean(true), nil
	}
	return nil, fmt.Errorf("%w: unsupported unary operator %s", engine.ErrNotImplemented, e.Op)
}

func (interp *Interpreter) evalUpdate(e *ast.UpdateExpr, scope *Scope) (engine.Value, error) {
	// Get current value
	cur, err := interp.evalExpr(e.Arg, scope)
	if err != nil {
		return nil, err
	}
	f, _ := cur.Float()
	var newVal engine.Value
	if e.Op == "++" {
		newVal = engine.Number(f + 1)
	} else {
		newVal = engine.Number(f - 1)
	}
	// Assign back
	if err := interp.assignToRef(e.Arg, newVal, scope); err != nil {
		return nil, err
	}
	if e.Prefix {
		return newVal, nil
	}
	return cur, nil
}

func (interp *Interpreter) evalBinary(e *ast.BinaryExpr, scope *Scope) (engine.Value, error) {
	left, err := interp.evalExpr(e.Left, scope)
	if err != nil {
		return nil, err
	}
	right, err := interp.evalExpr(e.Right, scope)
	if err != nil {
		return nil, err
	}
	return applyBinaryOp(e.Op, left, right), nil
}

func (interp *Interpreter) evalLogical(e *ast.LogicalExpr, scope *Scope) (engine.Value, error) {
	left, err := interp.evalExpr(e.Left, scope)
	if err != nil {
		return nil, err
	}
	if e.Op == "&&" {
		b, _ := left.Bool()
		if !b {
			return left, nil
		}
		return interp.evalExpr(e.Right, scope)
	}
	// ||
	b, _ := left.Bool()
	if b {
		return left, nil
	}
	return interp.evalExpr(e.Right, scope)
}

func (interp *Interpreter) evalAssign(e *ast.AssignExpr, scope *Scope) (engine.Value, error) {
	right, err := interp.evalExpr(e.Right, scope)
	if err != nil {
		return nil, err
	}
	if e.Op == "=" {
		if err := interp.assignToRef(e.Left, right, scope); err != nil {
			return nil, err
		}
		return right, nil
	}
	// Compound assignment: += -= etc.
	leftExpr, ok := e.Left.(ast.Expression)
	if !ok {
		// 解构模式不能作为复合赋值左值（JS 语法错误）。
		return nil, fmt.Errorf("%w: invalid compound assignment target %T", engine.ErrSyntaxError, e.Left)
	}
	cur, err := interp.evalExpr(leftExpr, scope)
	if err != nil {
		return nil, err
	}
	baseOp := e.Op[:len(e.Op)-1] // strip trailing '='
	result := applyBinaryOp(baseOp, cur, right)
	if err := interp.assignToRef(e.Left, result, scope); err != nil {
		return nil, err
	}
	return result, nil
}

func (interp *Interpreter) assignToRef(target ast.Node, val engine.Value, scope *Scope) error {
	switch t := target.(type) {
	case *ast.Identifier:
		if !scope.Set(t.Name, val) {
			// Implicit global
			_ = interp.globalObj.Set(t.Name, val)
		}
		return nil
	case *ast.MemberExpr:
		obj, err := interp.evalExpr(t.Object, scope)
		if err != nil {
			return err
		}
		var key string
		if t.Computed {
			kv, err := interp.evalExpr(t.Property, scope)
			if err != nil {
				return err
			}
			key = kv.String()
		} else if id, ok := t.Property.(*ast.Identifier); ok {
			key = id.Name
		}
		if o, ok := obj.AsObject(); ok {
			return o.Set(key, val)
		}
		return fmt.Errorf("%w: cannot set property on %s", engine.ErrTypeError, obj.Type())
	case *ast.ObjectPattern, *ast.ArrayPattern:
		return interp.storePatternValue(t, val, scope)
	}
	return fmt.Errorf("%w: invalid assignment target %T", engine.ErrSyntaxError, target)
}

// storePatternValue 按解构模式把 val 存入已有引用（ES2015 解构赋值）。
// 与声明解构（bindPattern）不同：不声明新变量，标识符经 scope.Set 解析
// （未声明时落入隐式全局），成员表达式走属性存储。
func (interp *Interpreter) storePatternValue(target ast.Node, val engine.Value, scope *Scope) error {
	switch pat := target.(type) {
	case *ast.Identifier:
		return interp.assignToRef(pat, val, scope)
	case *ast.MemberExpr:
		return interp.assignToRef(pat, val, scope)
	case *ast.ArrayPattern:
		for i, el := range pat.Elements {
			if el.Target == nil {
				continue // 空洞
			}
			var v engine.Value
			if el.IsRest {
				if arr, ok := val.(*engine.ArrayValue); ok {
					rest := arr.Elems()
					if i < len(rest) {
						rest = rest[i:]
					} else {
						rest = []engine.Value{}
					}
					v = engine.NewArray(rest)
				} else if o, ok := val.AsObject(); ok {
					v = engine.NewArray([]engine.Value{})
					_ = o // 非数组对象按空数组处理（保守）
				} else {
					v = engine.Undefined()
				}
			} else {
				key := strconv.Itoa(i)
				if o, ok := val.AsObject(); ok {
					tmp, err := o.Get(key)
					if err != nil {
						return err
					}
					v = tmp
				} else {
					v = engine.Undefined()
				}
			}
			if el.Default != nil && v.IsUndefined() {
				dv, err := interp.evalExpr(el.Default, scope)
				if err != nil {
					return err
				}
				v = dv
			}
			if err := interp.storePatternValue(el.Target, v, scope); err != nil {
				return err
			}
		}
		return nil
	case *ast.ObjectPattern:
		o, ok := val.AsObject()
		if !ok {
			return fmt.Errorf("%w: cannot destructure %s", engine.ErrTypeError, val.Type())
		}
		boundKeys := make(map[string]bool)
		for propIndex, prop := range pat.Properties {
			if prop.Value == nil {
				return fmt.Errorf("%w: invalid assignment target in destructuring", engine.ErrSyntaxError)
			}
			if prop.IsRest {
				// rest: 复制除已绑定键外的所有键
				rest := engine.NewObject()
				for _, k := range o.Keys() {
					if boundKeys[k] {
						continue
					}
					kv, err := o.Get(k)
					if err != nil {
						return err
					}
					if err := rest.Set(k, kv); err != nil {
						return err
					}
				}
				if err := interp.storePatternValue(prop.Value, rest, scope); err != nil {
					return err
				}
				_ = propIndex
				continue
			}
			var key string
			if prop.Computed {
				kv, err := interp.evalExpr(prop.Key, scope)
				if err != nil {
					return err
				}
				key = kv.String()
			} else if id, ok := prop.Key.(*ast.Identifier); ok {
				key = id.Name
			} else if s, ok := prop.Key.(*ast.StringLit); ok {
				key = s.Value
			}
			boundKeys[key] = true
			v, err := o.Get(key)
			if err != nil {
				return err
			}
			if prop.Default != nil && v.IsUndefined() {
				dv, err := interp.evalExpr(prop.Default, scope)
				if err != nil {
					return err
				}
				v = dv
			}
			if err := interp.storePatternValue(prop.Value, v, scope); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("%w: invalid assignment target %T", engine.ErrSyntaxError, target)
}

func (interp *Interpreter) evalConditional(e *ast.ConditionalExpr, scope *Scope) (engine.Value, error) {
	test, err := interp.evalExpr(e.Test, scope)
	if err != nil {
		return nil, err
	}
	cond, _ := test.Bool()
	if cond {
		return interp.evalExpr(e.Consequent, scope)
	}
	return interp.evalExpr(e.Alternate, scope)
}

func (interp *Interpreter) evalSequence(e *ast.SequenceExpr, scope *Scope) (engine.Value, error) {
	var result engine.Value = engine.Undefined()
	for _, expr := range e.Expressions {
		v, err := interp.evalExpr(expr, scope)
		if err != nil {
			return nil, err
		}
		result = v
	}
	return result, nil
}

// === Function calls ======================================================

// callClosure executes a closure's body with the given this and args.
func (interp *Interpreter) callClosure(c *Closure, thisVal engine.Value, args []engine.Value) (engine.Value, error) {
	fnScope := c.scope.NewChild()
	// Bind `this`
	fnScope.Declare("__this__", thisVal)
	// Bind parameters (with ES2015 default values).
	for i, param := range c.params {
		var val engine.Value = engine.Undefined()
		if i < len(args) {
			val = args[i]
		}
		// Apply default if the argument is undefined.
		if val.IsUndefined() && c.defaults != nil && i < len(c.defaults) && c.defaults[i] != nil {
			v, err := interp.evalExpr(c.defaults[i], fnScope)
			if err != nil {
				return engine.Undefined(), err
			}
			val = v
		}
		fnScope.Declare(param.Name, val)
	}
	// Bind rest parameter: collect remaining args into an array.
	if c.restParam != nil {
		var restElems []engine.Value
		if len(args) > len(c.params) {
			restElems = append(restElems, args[len(c.params):]...)
		}
		restArr := engine.NewArray(restElems)
		engine.SetProto(restArr, interp.arrayProto)
		fnScope.Declare(c.restParam.Name, restArr)
	}
	// Bind arguments object for non-arrow functions (simplified)
	if !c.isArrow && interp.argumentsSupported {
		argsObj := engine.NewArray(args)
		fnScope.Declare("arguments", argsObj)
	}

	// Execute body
	switch body := c.body.(type) {
	case *ast.BlockStmt:
		_, err := interp.execBlock(body, fnScope)
		if err != nil {
			if cf, ok := isControlFlow(err); ok && cf.kind == "return" {
				return cf.value, nil
			}
			return engine.Undefined(), err
		}
		return engine.Undefined(), nil
	case ast.Expression:
		// Arrow concise body
		return interp.evalExpr(body, fnScope)
	default:
		return engine.Undefined(), nil
	}
}

// jsError wraps a thrown JS value so it can propagate as a Go error.
type jsError struct {
	value engine.Value
}

func (e *jsError) Error() string {
	return e.value.String()
}

// === Lexer token passthrough (for parser.Parse) ==========================

// ensure lexer package is used (parser.Parse calls lexer.New internally)
var _ = lexer.TokenEOF

// setupGlobalThis sets globalThis to point to the global object.
func (interp *Interpreter) setupGlobalThis() {
	_ = interp.globalObj.Set("globalThis", interp.globalObj)
	_ = interp.globalObj.Set("undefined", engine.Undefined())
	_ = interp.globalObj.Set("NaN", engine.Number(math.NaN()))
	_ = interp.globalObj.Set("Infinity", engine.Number(math.Inf(1)))
}
