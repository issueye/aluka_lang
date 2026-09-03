package interpreter

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"sync"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/lexer"
	"github.com/aluka-lang/aluka/internal/engine/parser"
)

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

	// nextTickQueue is kept separate because Node drains process.nextTick jobs
	// before Promise reactions and queueMicrotask callbacks.
	nextTickQueue []func()
	// microtaskQueue holds pending microtasks (Promise reactions, queueMicrotask).
	microtaskQueue []func()
	// unhandledQueue holds promises rejected without a handler, judged at the
	// end of each microtask checkpoint (Node's unhandledRejection timing).
	unhandledQueue []*PromiseValue

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

// taskQueueSize 返回事件循环任务队列缓冲容量。
// 环境变量 ALUKA_TASK_QUEUE_SIZE 覆盖默认值（须为正整数，非法值回退默认）。
func taskQueueSize() int {
	if v := os.Getenv("ALUKA_TASK_QUEUE_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultTaskQueueSize
}

// defaultTaskQueueSize 是事件循环任务队列的默认缓冲容量。
// 高并发 HTTP/IO 场景（express 压测）下 64 会导致 PostTask 阻塞投递方
// 形成死锁；1024 为实测安全值。可用环境变量 ALUKA_TASK_QUEUE_SIZE 覆盖。
const defaultTaskQueueSize = 1024

// NewInterpreter creates an interpreter with built-in globals set up.
func NewInterpreter() (*Interpreter, error) {
	interp := &Interpreter{
		global:             NewScope(),
		globalObj:          engine.NewObject(),
		constructors:       make(map[string]engine.Object),
		argumentsSupported: true,
		taskCh:             make(chan func(), taskQueueSize()),
		idleCh:             make(chan struct{}, 1),
		stopCh:             make(chan struct{}),
	}
	interp.setupBuiltins()
	interp.setupGlobalThis()
	// globalThis/undefined/NaN/Infinity 在 setupGlobalThis 后置注册，统一
	// 收口为不可枚举（对齐 Node）。
	for _, k := range []string{"globalThis", "undefined", "NaN", "Infinity"} {
		_ = engine.DefineOwnProperty(interp.globalObj, k, engine.Descriptor{HasEnumerable: true, Enumerable: false})
	}
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

// ObjectPrototype 返回 %Object.prototype%（供 globals 注册把接口原型接上标准原型链）。
func (interp *Interpreter) ObjectPrototype() engine.Object { return interp.objectProto }

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

// 语句/表达式/赋值/错误映射的实现分文件：
//   - interp_stmt.go   语句执行（块/声明/分支/循环/try/switch）
//   - interp_expr.go   表达式求值（字面量/成员/调用/运算符）
//   - interp_assign.go 赋值与解构模式绑定
//   - interp_error.go  Go error → JS 异常值映射
//   - engine_entry.go  Engine / VMEngine 引擎入口

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
