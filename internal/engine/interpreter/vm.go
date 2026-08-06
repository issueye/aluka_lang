// Package interpreter: vm.go implements the stack-based bytecode VM (Phase 1B).
//
// It reuses the builtins / prototypes / helpers set up by the AST interpreter
// (setupBuiltins, strictEqual, applyBinaryOp, nativeMethod, ...) and adds a
// bytecode execution engine on top. The VM and the AST-walker can coexist so
// 1A tests keep passing while 1B matures.

package interpreter

import (
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/ast"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/compiler"
	"github.com/aluka-lang/aluka/internal/engine/parser"
)

// VM is a stack-based bytecode VM that shares builtins with the AST interpreter.
type VM struct {
	interp *Interpreter // provides builtins, globalObj, prototypes, etc.
	module *bytecode.Module

	stack  []engine.Value // value stack: holds locals + operands
	frames []vmFrame      // call-frame stack

	// ic 是属性访问内联缓存（隐藏类 shape 缓存，1B.5）。
	ic engine.ICache

	// 覆盖率统计（aluka test --coverage 启用；常态零开销）。
	coverEnabled bool
	coverMu      sync.Mutex
	coverLines   map[string]map[int]bool // 源文件 → 已执行行集合
}

// EnableCoverage 启用行级覆盖率统计。
func (v *VM) EnableCoverage() {
	v.coverEnabled = true
	v.coverLines = make(map[string]map[int]bool)
}

// CoverageLines 返回覆盖率统计（源文件 → 已执行行集合）。
func (v *VM) CoverageLines() map[string]map[int]bool {
	v.coverMu.Lock()
	defer v.coverMu.Unlock()
	out := make(map[string]map[int]bool, len(v.coverLines))
	for f, lines := range v.coverLines {
		m := make(map[int]bool, len(lines))
		for ln := range lines {
			m[ln] = true
		}
		out[f] = m
	}
	return out
}

type vmFrame struct {
	tmpl         *bytecode.FuncTemplate
	pc           int
	base         int // index in vm.stack of this frame's slot 0
	upvalues     []*upvalue
	tryStack     []*vmTryHandler
	openUpvalues []*upvalue // open upvalues pointing into this frame's locals
}

// upvalue is a closure capture. Open upvalues point at a stack slot; when the
// owning frame exits, open upvalues referencing its slots are closed (the
// value is copied into `closed`).
type upvalue struct {
	slot   *engine.Value // non-nil while open
	index  int           // absolute stack index while open (for stack growth rebasing)
	closed engine.Value  // set when closed
}

// vmTryHandler tracks an active try/catch/finally region.
type vmTryHandler struct {
	entry *bytecode.TryEntry
	exc   engine.Value // pending exception (nil means none)
	phase int          // 0=in try, 1=in catch, 2=in finally
}

// NewVM creates a VM backed by a fresh interpreter's builtins.
func NewVM() (*VM, error) {
	interp, err := NewInterpreter()
	if err != nil {
		return nil, err
	}
	return &VM{interp: interp}, nil
}

// Interp returns the backing interpreter (for RegisterFunc / Global access).
func (v *VM) Interp() *Interpreter { return v.interp }

// EnqueueMicrotask 入队一个微任务（node:test 子测试调度等内置模块使用）。
// 微任务在下一次 drainMicrotasks/AwaitPromise 时执行。
func (v *VM) EnqueueMicrotask(fn func()) {
	v.interp.enqueueMicrotask(fn)
}

// Global returns the global object (implements engine.Context).
func (v *VM) Global() engine.Object { return v.interp.Global() }

// RegisterFunc registers a Go function as a global (implements engine.Context).
func (v *VM) RegisterFunc(name string, fn engine.Func) error {
	return v.interp.RegisterFunc(name, fn)
}

// PostTask 投递任务到 JS 执行线程（implements engine.Context）。
func (v *VM) PostTask(fn func()) {
	v.interp.PostTask(fn)
}

// AddRef 跟踪活跃句柄（implements engine.Context）。
func (v *VM) AddRef() func() {
	return v.interp.NewTaskHandle()
}

// RunLoop 启动事件循环（处理定时器/http 回调，直到无 pending 任务）。
func (v *VM) RunLoop() {
	v.interp.RunLoop()
}

// Stop 停止事件循环。
func (v *VM) Stop() {
	v.interp.Stop()
}

// Close releases context resources (implements engine.Context).
func (v *VM) Close() error { return nil }

// Eval parses, compiles, and executes JS source.
func (v *VM) Eval(src, filename string) (engine.Value, error) {
	prog, err := parser.Parse(src)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("%w: %s: %v", engine.ErrSyntaxError, filename, err)
	}
	return v.EvalProgram(prog, filename)
}

// EvalProgram compiles and executes a pre-parsed AST. Used by the module
// loader to run ESM modules after AST transformation.
func (v *VM) EvalProgram(prog *ast.Program, filename string) (engine.Value, error) {
	comp := compiler.New()
	mod, err := comp.Compile(prog, filename)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("aluka: compile error: %w", err)
	}
	return v.runModule(mod)
}

// Compile 解析源码并编译为字节码 Module（不执行）。供字节码缓存使用：
// 加载器可先检查磁盘缓存，未命中时调用此方法编译并写盘，再调用 RunModule 执行。
func (v *VM) Compile(src, filename string) (*bytecode.Module, error) {
	prog, err := parser.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", engine.ErrSyntaxError, filename, err)
	}
	comp := compiler.New()
	mod, err := comp.Compile(prog, filename)
	if err != nil {
		return nil, fmt.Errorf("aluka: compile error: %w", err)
	}
	return mod, nil
}

// CompileAST 编译预解析的 AST 为字节码 Module（不执行）。
func (v *VM) CompileAST(prog *ast.Program, filename string) (*bytecode.Module, error) {
	comp := compiler.New()
	mod, err := comp.Compile(prog, filename)
	if err != nil {
		return nil, fmt.Errorf("aluka: compile error: %w", err)
	}
	return mod, nil
}

// RunModule 执行已编译的字节码 Module（公开版，供缓存恢复后执行）。
func (v *VM) RunModule(mod *bytecode.Module) (engine.Value, error) {
	return v.runModule(mod)
}

// runModule executes the top-level function of a module.
//
// This function is re-entrant: when require() is called inside a module, the
// native require function calls Eval → EvalProgram → runModule for the
// dependency. To avoid clobbering the caller's execution state (stack, frames,
// module), we save and restore them around the nested execution.
func (v *VM) runModule(mod *bytecode.Module) (engine.Value, error) {
	// Save caller state for re-entrant calls.
	savedStack := v.stack
	savedFrames := v.frames
	savedModule := v.module
	isTopLevel := len(savedFrames) == 0

	v.module = mod
	v.interp.currentVM = v
	main := mod.Functions[0]
	// Reserve locals for the top-level frame (fresh stack for this module).
	v.stack = make([]engine.Value, 0, main.NumLocals+16)
	for i := 0; i < main.NumLocals; i++ {
		v.stack = append(v.stack, engine.Undefined())
	}
	// Fresh frames slice — never mutate savedFrames.
	v.frames = []vmFrame{{tmpl: main, base: 0}}
	result, err := v.run()

	if isTopLevel {
		// Clean up leftover stack values before draining microtasks
		// (microtask callbacks reuse v.stack). Keep v.module alive —
		// microtask callbacks (Promise reactions, async continuations)
		// AND event-loop tasks (timers, http handlers, user closures)
		// may call back into the VM and need the module's templates.
		// module 在事件循环（RunLoop）结束后才允许被 GC 回收。
		v.stack = v.stack[:0]
		v.frames = v.frames[:0]
		// Drain the microtask queue (Promise reactions, queueMicrotask
		// callbacks). Errors from microtasks are handled internally by
		// Promise reactions; any uncaught error is silently ignored here.
		v.interp.drainMicrotasks()
	} else {
		// Restore the caller's execution state so its run() loop continues.
		v.stack = savedStack
		v.frames = savedFrames
		v.module = savedModule
	}
	return result, err
}

// cur returns the top call frame.
func (v *VM) cur() *vmFrame {
	return &v.frames[len(v.frames)-1]
}

// === Stack helpers ========================================================

func (v *VM) push(val engine.Value) {
	v.ensureStack(1)
	v.stack = append(v.stack, val)
}

// ensureStack grows the value stack without leaving open upvalues pointing at
// the old backing array. Upvalues also retain an absolute slot index so they
// can be rebound after a grow; this matters because function-frame setup can
// append many locals in one operation, independently of push().
func (v *VM) ensureStack(extra int) {
	if extra <= 0 || len(v.stack)+extra <= cap(v.stack) {
		return
	}
	newCap := cap(v.stack) * 2
	if newCap < len(v.stack)+extra {
		newCap = len(v.stack) + extra
	}
	if newCap < 16 {
		newCap = 16
	}
	next := make([]engine.Value, len(v.stack), newCap)
	copy(next, v.stack)
	v.stack = next
	for _, frame := range v.frames {
		for _, uv := range frame.openUpvalues {
			if uv.slot != nil && uv.index >= 0 && uv.index < len(v.stack) {
				uv.slot = &v.stack[uv.index]
			}
		}
	}
}

func (v *VM) reserveUndefined(n int) {
	if n <= 0 {
		return
	}
	oldLen := len(v.stack)
	v.ensureStack(n)
	v.stack = v.stack[:oldLen+n]
	for i := oldLen; i < len(v.stack); i++ {
		v.stack[i] = engine.Undefined()
	}
}

func (v *VM) appendValues(values []engine.Value) {
	if len(values) == 0 {
		return
	}
	v.ensureStack(len(values))
	v.stack = append(v.stack, values...)
}

func (v *VM) pop() engine.Value {
	last := len(v.stack) - 1
	val := v.stack[last]
	v.stack = v.stack[:last]
	return val
}

func (v *VM) peek() engine.Value { return v.stack[len(v.stack)-1] }

// local returns a pointer to a local slot in the current frame.
func (v *VM) local(slot int) *engine.Value {
	return &v.stack[v.cur().base+slot]
}

// === Main loop ============================================================

// run executes the current top frame until it returns.
func (v *VM) run() (engine.Value, error) {
	for {
		frame := v.cur()
		tmpl := frame.tmpl
		code := tmpl.Code

		// Each iteration: decode + dispatch.
		for {
			// Re-fetch frame pointer: call/invoke operations may reallocate
			// v.frames (via append), invalidating the previously cached pointer.
			frame = v.cur()
			pc := frame.pc
			if pc >= len(code) {
				// Ran off the end without OpReturn — treat as undefined return.
				return v.doReturn(engine.Undefined()), nil
			}
			op, operand, next := bytecode.Decode(code, pc)
			frame.pc = next

			// 覆盖率统计（仅启用时）：记录 (源文件, 行) 执行。
			if v.coverEnabled && tmpl.SourceFile != "" {
				if line := lineForPC(tmpl, pc); line > 0 {
					v.coverMu.Lock()
					m := v.coverLines[tmpl.SourceFile]
					if m == nil {
						m = make(map[int]bool)
						v.coverLines[tmpl.SourceFile] = m
					}
					m[line] = true
					v.coverMu.Unlock()
				}
			}

			switch op {
			// --- Literals & stack ---
			case bytecode.OpNop:
			case bytecode.OpPushUndefined:
				v.push(engine.Undefined())
			case bytecode.OpPushNull:
				v.push(engine.Null())
			case bytecode.OpPushTrue:
				v.push(engine.Boolean(true))
			case bytecode.OpPushFalse:
				v.push(engine.Boolean(false))
			case bytecode.OpPushConst:
				v.push(tmpl.Constants[operand])
			case bytecode.OpPushInt:
				v.push(engine.Number(float64(operand)))
			case bytecode.OpPushNegInt:
				v.push(engine.Number(float64(-int(operand))))
			case bytecode.OpPop:
				v.pop()
			case bytecode.OpDup:
				v.push(v.peek())
			case bytecode.OpSwap:
				n := len(v.stack) - 1
				v.stack[n], v.stack[n-1] = v.stack[n-1], v.stack[n]

			// --- Variables ---
			case bytecode.OpLoadLocal:
				v.push(*v.local(int(operand)))
			case bytecode.OpStoreLocal:
				*v.local(int(operand)) = v.pop()
			case bytecode.OpLoadGlobal:
				name := tmpl.Constants[operand].String()
				val, _ := v.interp.globalObj.Get(name)
				v.push(val)
			case bytecode.OpStoreGlobal:
				name := tmpl.Constants[operand].String()
				_ = v.interp.globalObj.Set(name, v.pop())

			// --- Upvalues ---
			case bytecode.OpLoadUpvalue:
				uv := v.cur().upvalues[operand]
				if uv.slot != nil {
					v.push(*uv.slot)
				} else {
					v.push(uv.closed)
				}
			case bytecode.OpStoreUpvalue:
				uv := v.cur().upvalues[operand]
				val := v.pop()
				if uv.slot != nil {
					*uv.slot = val
				} else {
					uv.closed = val
				}
			case bytecode.OpMakeClosure:
				fnIdx := int(operand)
				closureTmpl := v.module.Functions[fnIdx]
				closure := newVMClosure(v, closureTmpl, v.captureUpvalues(closureTmpl))
				engine.SetProto(closure.obj, v.interp.functionProto)
				_ = closure.obj.Set("name", engine.Str(closureTmpl.Name))
				_ = closure.obj.Set("length", engine.IntValue(closureTmpl.NumParams))
				// Create prototype object so `new Fn()` sets the instance's
				// [[Prototype]] to Fn.prototype (needed for instanceof).
				proto := engine.NewObject()
				engine.SetProto(proto, v.interp.objectProto)
				_ = proto.Set("constructor", closure)
				_ = closure.obj.Set("prototype", proto)
				v.push(closure)

				// --- Binary arithmetic & bitwise ---
			case bytecode.OpAdd:
				r := v.pop()
				l := v.pop()
				// BigInt 与非 BigInt 的加法必须显式转换；保持运行时
				// 对混合 String/BigInt 的 TypeError 约定，避免静默拼接。
				if isBigInt(l) || isBigInt(r) {
					result, err := bigintArith2(l, r, '+')
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else if l.Type() == engine.TypeString || r.Type() == engine.TypeString {
					v.push(v.binAdd(l, r))
				} else {
					v.push(v.binAdd(l, r))
				}
			case bytecode.OpSub:
				r := v.pop()
				l := v.pop()
				if isBigInt(l) || isBigInt(r) {
					result, err := bigintArith2(l, r, '-')
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else {
					ln, _ := l.Float()
					rn, _ := r.Float()
					v.push(engine.Number(ln - rn))
				}
			case bytecode.OpMul:
				r := v.pop()
				l := v.pop()
				if isBigInt(l) || isBigInt(r) {
					result, err := bigintArith2(l, r, '*')
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else {
					ln, _ := l.Float()
					rn, _ := r.Float()
					v.push(engine.Number(ln * rn))
				}
			case bytecode.OpDiv:
				r := v.pop()
				l := v.pop()
				if isBigInt(l) || isBigInt(r) {
					result, err := bigintArith2(l, r, '/')
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else {
					ln, _ := l.Float()
					rn, _ := r.Float()
					if rn == 0 {
						if ln == 0 {
							v.push(engine.Number(math.NaN()))
						} else if ln > 0 {
							v.push(engine.Number(math.Inf(1)))
						} else {
							v.push(engine.Number(math.Inf(-1)))
						}
					} else {
						v.push(engine.Number(ln / rn))
					}
				}
			case bytecode.OpMod:
				r := v.pop()
				l := v.pop()
				if isBigInt(l) || isBigInt(r) {
					result, err := bigintArith2(l, r, '%')
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else {
					ln, _ := l.Float()
					rn, _ := r.Float()
					if rn == 0 {
						v.push(engine.Number(math.NaN()))
					} else {
						v.push(engine.Number(math.Mod(ln, rn)))
					}
				}
			case bytecode.OpPow:
				r := v.pop()
				l := v.pop()
				if isBigInt(l) || isBigInt(r) {
					result, err := bigintPow(l, r)
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else {
					ln, _ := l.Float()
					rn, _ := r.Float()
					v.push(engine.Number(math.Pow(ln, rn)))
				}
			case bytecode.OpBitAnd:
				r := v.pop()
				l := v.pop()
				if isBigInt(l) || isBigInt(r) {
					result, err := bigintBitwise(l, r, "&")
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else {
					ln, _ := l.Int()
					rn, _ := r.Int()
					v.push(engine.Number(float64(ln & rn)))
				}
			case bytecode.OpBitOr:
				r := v.pop()
				l := v.pop()
				if isBigInt(l) || isBigInt(r) {
					result, err := bigintBitwise(l, r, "|")
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else {
					ln, _ := l.Int()
					rn, _ := r.Int()
					v.push(engine.Number(float64(ln | rn)))
				}
			case bytecode.OpBitXor:
				r := v.pop()
				l := v.pop()
				if isBigInt(l) || isBigInt(r) {
					result, err := bigintBitwise(l, r, "^")
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else {
					ln, _ := l.Int()
					rn, _ := r.Int()
					v.push(engine.Number(float64(ln ^ rn)))
				}
			case bytecode.OpShl:
				r := v.pop()
				l := v.pop()
				if isBigInt(l) || isBigInt(r) {
					result, err := bigintBitwise(l, r, "<<")
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else {
					ln, _ := l.Int()
					rn, _ := r.Int()
					v.push(engine.Number(float64(ln << (uint(rn) & 31))))
				}
			case bytecode.OpShr:
				r := v.pop()
				l := v.pop()
				if isBigInt(l) || isBigInt(r) {
					result, err := bigintBitwise(l, r, ">>")
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(result)
				} else {
					ln, _ := l.Int()
					rn, _ := r.Int()
					v.push(engine.Number(float64(ln >> (uint(rn) & 31))))
				}
			case bytecode.OpUShr:
				r := v.pop()
				l := v.pop()
				if isBigInt(l) || isBigInt(r) {
					// BigInt 不支持 >>> （无符号右移），抛 TypeError。
					return v.handleThrow(fmt.Errorf("%w: BigInts have no unsigned right shift, use >> instead", engine.ErrTypeError))
				}
				ln, _ := l.Int()
				rn, _ := r.Int()
				v.push(engine.Number(float64(uint32(ln) >> (uint(rn) & 31))))

			// --- Unary ---
			case bytecode.OpNeg:
				val := v.pop()
				if isBigInt(val) {
					v.push(bigintNeg(val))
				} else {
					n, _ := val.Float()
					v.push(engine.Number(-n))
				}
			case bytecode.OpNot:
				b, _ := v.pop().Bool()
				v.push(engine.Boolean(!b))
			case bytecode.OpBitNot:
				n, _ := v.pop().Int()
				v.push(engine.Number(float64(^n)))
			case bytecode.OpTypeof:
				v.push(engine.Str(v.pop().Type().String()))
			case bytecode.OpTypeofGlobal:
				name := tmpl.Constants[operand].String()
				val, _ := v.interp.globalObj.Get(name)
				v.push(engine.Str(val.Type().String()))
			case bytecode.OpUnaryPlus:
				v.push(engine.Number(jsToNumber(v.pop())))

			// --- Comparisons ---
			case bytecode.OpEq:
				r := v.pop()
				l := v.pop()
				v.push(engine.Boolean(looseEquals(l, r)))
			case bytecode.OpNe:
				r := v.pop()
				l := v.pop()
				v.push(engine.Boolean(!looseEquals(l, r)))
			case bytecode.OpStrictEq:
				r := v.pop()
				l := v.pop()
				v.push(engine.Boolean(strictEqual(l, r)))
			case bytecode.OpStrictNe:
				r := v.pop()
				l := v.pop()
				v.push(engine.Boolean(!strictEqual(l, r)))
			case bytecode.OpLt:
				r := v.pop()
				l := v.pop()
				v.push(engine.Boolean(compareValues(l, r) < 0))
			case bytecode.OpLe:
				r := v.pop()
				l := v.pop()
				v.push(engine.Boolean(compareValues(l, r) <= 0))
			case bytecode.OpGt:
				r := v.pop()
				l := v.pop()
				v.push(engine.Boolean(compareValues(l, r) > 0))
			case bytecode.OpGe:
				r := v.pop()
				l := v.pop()
				v.push(engine.Boolean(compareValues(l, r) >= 0))

			// --- Control flow ---
			case bytecode.OpJmp:
				frame.pc = pc + bytecode.InstrSize + bytecode.SignedOperand(operand)
			case bytecode.OpJmpTruePop:
				val := v.pop()
				if b, _ := val.Bool(); b {
					frame.pc = pc + bytecode.InstrSize + bytecode.SignedOperand(operand)
				}
			case bytecode.OpJmpFalsePop:
				val := v.pop()
				if b, _ := val.Bool(); !b {
					frame.pc = pc + bytecode.InstrSize + bytecode.SignedOperand(operand)
				}
			case bytecode.OpJmpTrueKeep:
				// Keep value if true (jump past right operand); pop if false.
				if b, _ := v.peek().Bool(); b {
					frame.pc = pc + bytecode.InstrSize + bytecode.SignedOperand(operand)
				} else {
					v.pop()
				}
			case bytecode.OpJmpFalseKeep:
				// Keep value if false (jump past right operand); pop if true.
				if b, _ := v.peek().Bool(); !b {
					frame.pc = pc + bytecode.InstrSize + bytecode.SignedOperand(operand)
				} else {
					v.pop()
				}
			case bytecode.OpJmpNullishKeep:
				// For `a ?? b`: if a is nullish, pop a and fall through to b.
				// If a is non-nullish, keep a and jump past b.
				val := v.peek()
				if val.IsUndefined() || val.IsNull() {
					v.pop()
				} else {
					frame.pc = pc + bytecode.InstrSize + bytecode.SignedOperand(operand)
				}
			case bytecode.OpOptionalJump:
				// Optional chaining short-circuit (?.): if top is null/undefined,
				// pop it, push undefined, and jump to the chain end. Otherwise,
				// keep the value on stack and fall through.
				val := v.peek()
				if val.IsUndefined() || val.IsNull() {
					v.pop()
					v.push(engine.Undefined())
					frame.pc = pc + bytecode.InstrSize + bytecode.SignedOperand(operand)
				}

			// --- Functions ---
			case bytecode.OpCall:
				numArgs := int(operand)
				result, err := v.doCall(numArgs, engine.Undefined())
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpCallWithThis:
				// Stack: ... callee this arg0 ... argN-1
				numArgs := int(operand)
				argStart := len(v.stack) - numArgs
				thisVal := v.stack[argStart-1]
				callee := v.stack[argStart-2]
				args := make([]engine.Value, numArgs)
				copy(args, v.stack[argStart:argStart+numArgs])
				v.stack = v.stack[:argStart-2]
				result, err := v.invoke(callee, thisVal, args, false)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpCallWithThisArgs:
				// Stack: ... callee this argsArray
				argsArr := v.pop()
				thisVal := v.pop()
				callee := v.pop()
				args := v.toArrayValues(argsArr)
				result, err := v.invoke(callee, thisVal, args, false)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpCallMethod:
				numArgs := int(operand >> 16)
				nameIdx := int(operand & 0xFFFF)
				result, err := v.doCallMethod(numArgs, nameIdx)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpNew:
				numArgs := int(operand)
				result, err := v.doNew(numArgs)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpReturn:
				retVal := v.pop()
				return v.doReturn(retVal), nil
			case bytecode.OpReturnUndef:
				return v.doReturn(engine.Undefined()), nil

			// --- Objects & arrays ---
			case bytecode.OpNewObject:
				obj := engine.NewObject()
				engine.SetProto(obj, v.interp.objectProto)
				v.push(obj)
			case bytecode.OpNewArray:
				n := int(operand)
				elems := make([]engine.Value, n)
				for i := n - 1; i >= 0; i-- {
					elems[i] = v.pop()
				}
				arr := engine.NewArray(elems)
				engine.SetProto(arr, v.interp.arrayProto)
				v.push(arr)
			case bytecode.OpMakeRegexp:
				// 正则字面量：弹 flags + pattern，构造 RegExp 实例。
				flagsVal := v.pop()
				patVal := v.pop()
				rv, err := v.interp.makeRegexp(patVal.String(), flagsVal.String())
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(rv)
			case bytecode.OpGetProp:
				name := tmpl.Constants[operand].String()
				obj := v.pop()
				val, err := v.getProperty(obj, name)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(val)
			case bytecode.OpSetProp:
				name := tmpl.Constants[operand].String()
				val := v.pop()
				obj := v.pop()
				if err := v.setProperty(obj, name, val); err != nil {
					return v.handleThrow(err)
				}
				v.push(val)
			case bytecode.OpSetPropObj:
				name := tmpl.Constants[operand].String()
				val := v.pop()
				obj := v.peek()
				if err := v.setProperty(obj, name, val); err != nil {
					return v.handleThrow(err)
				}
			case bytecode.OpSetPropTop:
				// Stack: [..., val, obj] — assignTo pushes val first, then obj.
				name := tmpl.Constants[operand].String()
				obj := v.pop()
				val := v.pop()
				if err := v.setProperty(obj, name, val); err != nil {
					return v.handleThrow(err)
				}
			case bytecode.OpSetGetterObj:
				// 对象字面量 getter：栈 [obj, fn]，注册为 accessor getter。
				fn := v.pop()
				obj := v.peek()
				name := tmpl.Constants[operand].String()
				if o, ok := obj.AsObject(); ok {
					engine.UpdateAccessor(o, name, true, fn)
				}
			case bytecode.OpSetSetterObj:
				// 对象字面量 setter：栈 [obj, fn]，注册为 accessor setter。
				fn := v.pop()
				obj := v.peek()
				name := tmpl.Constants[operand].String()
				if o, ok := obj.AsObject(); ok {
					engine.UpdateAccessor(o, name, false, fn)
				}
			case bytecode.OpGetElem:
				key := v.pop()
				obj := v.pop()
				val, err := v.getProperty(obj, propertyKeyOf(key))
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(val)
			case bytecode.OpSetElem:
				val := v.pop()
				key := v.pop()
				obj := v.pop()
				if err := v.setProperty(obj, propertyKeyOf(key), val); err != nil {
					return v.handleThrow(err)
				}
				v.push(val)
			case bytecode.OpSetElemTop:
				// Stack: [..., val, obj, key] — assignTo pushes val, then obj, then key.
				key := v.pop()
				obj := v.pop()
				val := v.pop()
				if err := v.setProperty(obj, propertyKeyOf(key), val); err != nil {
					return v.handleThrow(err)
				}
			case bytecode.OpDelProp:
				// A: name-const index; pop obj, delete own prop, push bool result.
				nameIdx := int(operand)
				name := tmpl.Constants[nameIdx].String()
				obj := v.pop()
				// Proxy interception: dispatch to the deleteProperty trap if defined.
				if p, ok := obj.(*ProxyValue); ok {
					ok, err := p.proxyDelete(name)
					if err != nil {
						return v.handleThrow(err)
					}
					v.push(engine.Boolean(ok))
					break
				}
				result := true
				if o, ok := obj.AsObject(); ok {
					result = o.Delete(name)
				}
				v.push(engine.Boolean(result))
			case bytecode.OpDelElem:
				key := propertyKeyOf(v.pop())
				obj := v.pop()
				result := true
				if o, ok := obj.AsObject(); ok {
					result = o.Delete(key)
				}
				v.push(engine.Boolean(result))
			case bytecode.OpSetPropComputedObj:
				// Stack: [..., obj, key, value] → set obj[key] = value, obj stays.
				val := v.pop()
				key := v.pop()
				keyStr := propertyKeyOf(key)
				obj := v.peek()
				if err := v.setProperty(obj, keyStr, val); err != nil {
					return v.handleThrow(err)
				}

			// --- Spread (ES2015) ---
			case bytecode.OpBuildArray:
				arr := engine.NewArray(nil)
				engine.SetProto(arr, v.interp.arrayProto)
				v.push(arr)
			case bytecode.OpArrayPush:
				val := v.pop()
				arrVal := v.peek()
				arr, ok := arrVal.(*engine.ArrayValue)
				if !ok {
					return v.handleThrow(fmt.Errorf("%w: ARRAY_PUSH target not an array", engine.ErrTypeError))
				}
				arr.Append(val)
			case bytecode.OpArraySpread:
				spreadVal := v.pop()
				arrVal := v.peek()
				arr, ok := arrVal.(*engine.ArrayValue)
				if !ok {
					return v.handleThrow(fmt.Errorf("%w: ARRAY_SPREAD target not an array", engine.ErrTypeError))
				}
				if sa, ok := spreadVal.(*engine.ArrayValue); ok {
					for _, el := range sa.Elems() {
						arr.Append(el)
					}
				} else {
					// Use the iterator protocol for all other iterables
					// (generators, strings, objects with [Symbol.iterator], etc.).
					iter, err := v.getIterator(spreadVal)
					if err != nil {
						return v.handleThrow(err)
					}
					for {
						nextFn, err := v.getProperty(iter, "next")
						if err != nil {
							return v.handleThrow(err)
						}
						result, err := v.invoke(nextFn, iter, nil, false)
						if err != nil {
							return v.handleThrow(err)
						}
						doneVal, err := v.getProperty(result, "done")
						if err != nil {
							return v.handleThrow(err)
						}
						done, _ := doneVal.Bool()
						if done {
							break
						}
						valueVal, err := v.getProperty(result, "value")
						if err != nil {
							return v.handleThrow(err)
						}
						arr.Append(valueVal)
					}
				}
			case bytecode.OpSpreadObject:
				src := v.pop()
				dst := v.peek()
				dstObj, ok := dst.AsObject()
				if !ok {
					return v.handleThrow(fmt.Errorf("%w: SPREAD_OBJECT target not an object", engine.ErrTypeError))
				}
				// Proxy interception: use ownKeys + get traps.
				if p, ok := src.(*ProxyValue); ok {
					keys, err := p.proxyOwnKeys()
					if err != nil {
						return v.handleThrow(err)
					}
					for _, k := range keys {
						pv, err := p.proxyGet(k)
						if err != nil {
							return v.handleThrow(err)
						}
						_ = dstObj.Set(k, pv)
					}
					break
				}
				if srcObj, ok := src.AsObject(); ok {
					for _, k := range srcObj.Keys() {
						// 用 getProperty 读取：getter 访问器会被调用（spread 语义
						// 复制 getter 的结果值，而非 AccessorValue 本身）。
						if pv, err := v.getProperty(src, k); err == nil {
							_ = dstObj.Set(k, pv)
						}
					}
				}
			case bytecode.OpCallArgs:
				// Stack: ... callee argsArray
				argsArr := v.pop()
				callee := v.pop()
				args := v.toArrayValues(argsArr)
				result, err := v.invoke(callee, engine.Undefined(), args, false)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpCallMethodArgs:
				// Stack: ... receiver argsArray
				nameIdx := int(operand)
				argsArr := v.pop()
				receiver := v.pop()
				args := v.toArrayValues(argsArr)
				name := tmpl.Constants[nameIdx].String()
				fn, err := v.getProperty(receiver, name)
				if err != nil {
					return v.handleThrow(err)
				}
				result, err := v.invoke(fn, receiver, args, false)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpNewArgs:
				// Stack: ... callee argsArray
				argsArr := v.pop()
				callee := v.pop()
				args := v.toArrayValues(argsArr)
				result, err := v.invoke(callee, engine.Undefined(), args, true)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)

			// --- Try / catch ---
			case bytecode.OpTryEnter:
				tryIdx := int(operand)
				entry := &tmpl.TryTable[tryIdx]
				v.cur().tryStack = append(v.cur().tryStack, &vmTryHandler{
					entry: entry,
					exc:   nil,
					phase: 0,
				})
			case bytecode.OpTryExit:
				tryIdx := int(operand)
				v.handleTryExit(tryIdx)
			case bytecode.OpTryExitFinally:
				tryIdx := int(operand)
				if rethrow := v.handleTryExitFinally(tryIdx); rethrow != nil {
					return v.handleThrow(rethrow)
				}
			case bytecode.OpThrow:
				exc := v.pop()
				return v.handleThrow(exc)
			case bytecode.OpInstanceof:
				r := v.pop()
				l := v.pop()
				v.push(engine.Boolean(v.instanceof(l, r)))
			case bytecode.OpIn:
				r := v.pop()
				l := v.pop()
				v.push(engine.Boolean(v.inOp(l, r)))

			// --- Class (ES2015) ---
			case bytecode.OpMakeClass:
				classIdx := int(operand)
				result, err := v.doMakeClass(classIdx)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpGetProto:
				obj := v.pop()
				proto := v.getProto(obj)
				if proto != nil {
					v.push(proto)
				} else {
					v.push(engine.Null())
				}
			case bytecode.OpCallThis:
				numArgs := int(operand)
				result, err := v.doCallThis(numArgs, false)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpConstructThis:
				numArgs := int(operand)
				result, err := v.doCallThis(numArgs, true)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpCallThisArgs:
				result, err := v.doCallThisArgs(false)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)
			case bytecode.OpConstructThisArgs:
				result, err := v.doCallThisArgs(true)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(result)

			// --- Iterator protocol (ES2015) ---
			case bytecode.OpGetIterator:
				iterable := v.pop()
				iter, err := v.getIterator(iterable)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(iter)
			case bytecode.OpGetAsyncIterator:
				// ES2018 异步迭代协议：优先 [Symbol.asyncIterator]()，
				// 回退到 [Symbol.iterator]()（其 next 结果由后续 OpAwait 解包）。
				iterable := v.pop()
				iter, err := v.getAsyncIterator(iterable)
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(iter)
			case bytecode.OpYield:
				// Generator yield: pop yielded value, suspend frame, return to caller.
				yieldVal := v.pop()
				return v.doYield(yieldVal)
			case bytecode.OpAwait:
				// Async await: pop awaited value, suspend frame. The asyncRunner
				// catches the awaitSignal and schedules resumption when the
				// value's Promise settles.
				awaitedVal := v.pop()
				return v.doAwait(awaitedVal)

			default:
				return engine.Undefined(), fmt.Errorf("aluka: unknown opcode %s (%d)", op, op)
			}
		}
	}
}

// === Call dispatch ========================================================

// doCall pops numArgs + callee and invokes the function with this=undefined.
// Returns the result value. For bytecode closures, it sets up a new frame and
// runs it to completion (recursive call to run()).
func (v *VM) doCall(numArgs int, thisVal engine.Value) (engine.Value, error) {
	// Stack: ... callee arg0 arg1 ... argN-1
	argStart := len(v.stack) - numArgs
	callee := v.stack[argStart-1]
	// Keep args slice (without callee).
	args := make([]engine.Value, numArgs)
	copy(args, v.stack[argStart:argStart+numArgs])
	// Pop callee + args.
	v.stack = v.stack[:argStart-1]
	return v.invoke(callee, thisVal, args, false)
}

// doCallMethod handles obj.method(args) where the method name is a const index.
// Stack: ... receiver arg0 ... argN-1
func (v *VM) doCallMethod(numArgs, nameIdx int) (engine.Value, error) {
	argStart := len(v.stack) - numArgs
	receiver := v.stack[argStart-1]
	args := make([]engine.Value, numArgs)
	copy(args, v.stack[argStart:argStart+numArgs])
	// Pop receiver + args.
	v.stack = v.stack[:argStart-1]

	name := v.cur().tmpl.Constants[nameIdx].String()
	fn, err := v.getProperty(receiver, name)
	if err != nil {
		return engine.Undefined(), err
	}
	return v.invoke(fn, receiver, args, false)
}

// toArrayValues converts a value (expected to be an array) into a slice of
// engine.Value, used to expand spread arguments at runtime.
func (v *VM) toArrayValues(val engine.Value) []engine.Value {
	if arr, ok := val.(*engine.ArrayValue); ok {
		return arr.Elems()
	}
	if obj, ok := val.AsObject(); ok {
		var out []engine.Value
		for _, k := range obj.Keys() {
			if pv, err := obj.Get(k); err == nil {
				out = append(out, pv)
			}
		}
		return out
	}
	return nil
}

// doNew pops numArgs + constructor and invokes it as a constructor.
func (v *VM) doNew(numArgs int) (engine.Value, error) {
	argStart := len(v.stack) - numArgs
	callee := v.stack[argStart-1]
	args := make([]engine.Value, numArgs)
	copy(args, v.stack[argStart:argStart+numArgs])
	// Pop callee + args.
	v.stack = v.stack[:argStart-1]
	return v.invoke(callee, engine.Undefined(), args, true)
}

// === Class assembly =======================================================

// doMakeClass assembles a class from its ClassTemplate. If the template has a
// superclass, it is popped from the stack. The assembled constructor function
// is returned (the caller pushes it).
func (v *VM) doMakeClass(classIdx int) (engine.Value, error) {
	classTpl := v.module.Classes[classIdx]

	// 编译器先压入父类，再按声明顺序压入计算键，因此必须先逆序弹出
	// 计算键，最后才能取得父类构造器。
	computedKeys := make([]string, len(classTpl.ComputedIdx))
	for i := len(classTpl.ComputedIdx) - 1; i >= 0; i-- {
		computedKeys[i] = propertyKeyOf(v.pop())
	}

	var superCtor engine.Value
	if classTpl.HasSuper {
		superCtor = v.pop()
	}

	// Create the constructor closure.
	ctorTmpl := v.module.Functions[classTpl.CtorIdx]
	ctor := newVMClosure(v, ctorTmpl, v.captureUpvalues(ctorTmpl))
	engine.SetProto(ctor.obj, v.interp.functionProto)
	_ = ctor.obj.Set("name", engine.Str(classTpl.Name))
	_ = ctor.obj.Set("length", engine.IntValue(ctorTmpl.NumParams))

	// Create the prototype object.
	proto := engine.NewObject()
	if classTpl.HasSuper {
		// proto = Object.create(super.prototype)
		if superProto, err := v.getProperty(superCtor, "prototype"); err == nil && !superProto.IsUndefined() {
			if po, ok := superProto.(engine.Object); ok {
				engine.SetProto(proto, po)
			}
		}
	} else {
		engine.SetProto(proto, v.interp.objectProto)
	}
	_ = proto.Set("constructor", ctor)
	_ = ctor.obj.Set("prototype", proto)

	// Wire up static inheritance: constructor's [[Prototype]] = superclass.
	// Use the underlying *objectValue so prototype-chain walks in Get() work.
	if classTpl.HasSuper {
		if superCl, ok := superCtor.(*vmClosure); ok {
			engine.SetProto(ctor.obj, superCl.obj)
		} else if superObj, ok := superCtor.AsObject(); ok {
			engine.SetProto(ctor.obj, superObj)
		}
	}

	// Install methods / accessors.
	computedPos := 0
	for mi, m := range classTpl.Methods {
		mTmpl := v.module.Functions[m.TmplIdx]
		mClosure := newVMClosure(v, mTmpl, v.captureUpvalues(mTmpl))
		engine.SetProto(mClosure.obj, v.interp.functionProto)
		_ = mClosure.obj.Set("name", engine.Str(m.Name))
		_ = mClosure.obj.Set("length", engine.IntValue(mTmpl.NumParams))

		// Create prototype for the method (so it can be used as a constructor).
		mProto := engine.NewObject()
		engine.SetProto(mProto, v.interp.objectProto)
		_ = mProto.Set("constructor", mClosure)
		_ = mClosure.obj.Set("prototype", mProto)

		var target engine.Object
		if m.Static {
			target = ctor.obj
		} else {
			target = proto
		}

		// 计算键方法：用运行时求值的键安装。
		name := m.Name
		if computedPos < len(classTpl.ComputedIdx) && classTpl.ComputedIdx[computedPos] == mi {
			name = computedKeys[computedPos]
			computedPos++
		}

		switch m.Kind {
		case bytecode.MethodKindNormal:
			_ = target.Set(name, mClosure)
		case bytecode.MethodKindGetter:
			engine.UpdateAccessor(target, name, true, mClosure)
		case bytecode.MethodKindSetter:
			engine.UpdateAccessor(target, name, false, mClosure)
		}
	}

	return ctor, nil
}

// === Iterator protocol ====================================================

// getIterator obtains an iterator from an iterable value using the ES2015
// protocol. Arrays and strings are special-cased for efficiency; other objects
// must have a [Symbol.iterator] method.
func (v *VM) getIterator(iterable engine.Value) (engine.Value, error) {
	if iterable.IsNull() || iterable.IsUndefined() {
		return engine.Undefined(), fmt.Errorf("%w: %s is not iterable", engine.ErrTypeError, iterable.String())
	}
	// Array: built-in array iterator.
	if arr, ok := iterable.(*engine.ArrayValue); ok {
		return v.newArrayIterator(arr), nil
	}
	// String: built-in string iterator.
	if iterable.Type() == engine.TypeString {
		return v.newStringIterator(iterable.String()), nil
	}
	// Generator: generators are their own iterators.
	if gen, ok := iterable.(*GeneratorValue); ok {
		return gen, nil
	}
	// Object with [Symbol.iterator]: look up and call the method.
	if obj, ok := iterable.AsObject(); ok {
		symKey := engine.SymbolIterator.SymbolKey()
		if iterMethod, err := obj.Get(symKey); err == nil && !iterMethod.IsUndefined() {
			return v.invoke(iterMethod, iterable, nil, false)
		}
	}
	return engine.Undefined(), fmt.Errorf("%w: %s is not iterable", engine.ErrTypeError, iterable.Type())
}

// getAsyncIterator 使用 ES2018 异步迭代协议从可迭代值中获取异步迭代器。
// 优先查找 [Symbol.asyncIterator]()；若不存在则回退到 [Symbol.iterator]()
// （回退场景下 next() 返回普通值，由后续 OpAwait 经 promiseResolve 包装）。
// 数组/字符串/生成器无内置 asyncIterator，自动回退到同步迭代器。
func (v *VM) getAsyncIterator(iterable engine.Value) (engine.Value, error) {
	if iterable.IsNull() || iterable.IsUndefined() {
		return engine.Undefined(), fmt.Errorf("%w: %s is not iterable", engine.ErrTypeError, iterable.String())
	}
	// 优先：对象上的 [Symbol.asyncIterator] 方法。
	if obj, ok := iterable.AsObject(); ok {
		asyncKey := engine.SymbolAsyncIterator.SymbolKey()
		if iterMethod, err := obj.Get(asyncKey); err == nil && !iterMethod.IsUndefined() {
			return v.invoke(iterMethod, iterable, nil, false)
		}
	}
	// 回退：同步迭代器协议（数组/字符串/生成器/带 [Symbol.iterator] 的对象）。
	return v.getIterator(iterable)
}
func (v *VM) newArrayIterator(arr *engine.ArrayValue) engine.Value {
	idx := 0
	iterObj := engine.NewObject()
	engine.SetProto(iterObj, v.interp.objectProto)
	nextFn := v.interp.nativeMethod("next", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		result := engine.NewObject()
		engine.SetProto(result, v.interp.objectProto)
		elems := arr.Elems()
		if idx >= len(elems) {
			_ = result.Set("value", engine.Undefined())
			_ = result.Set("done", engine.Boolean(true))
		} else {
			_ = result.Set("value", elems[idx])
			_ = result.Set("done", engine.Boolean(false))
			idx++
		}
		return result, nil
	})
	_ = iterObj.Set("next", nextFn)
	// Store [Symbol.iterator] so the iterator itself is iterable.
	_ = iterObj.Set(engine.SymbolIterator.SymbolKey(), v.interp.nativeMethod("[Symbol.iterator]", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return iterObj, nil
	}))
	return iterObj
}

// newStringIterator creates an iterator object for a string.
func (v *VM) newStringIterator(s string) engine.Value {
	idx := 0
	runes := []rune(s)
	iterObj := engine.NewObject()
	engine.SetProto(iterObj, v.interp.objectProto)
	nextFn := v.interp.nativeMethod("next", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		result := engine.NewObject()
		engine.SetProto(result, v.interp.objectProto)
		if idx >= len(runes) {
			_ = result.Set("value", engine.Undefined())
			_ = result.Set("done", engine.Boolean(true))
		} else {
			_ = result.Set("value", engine.Str(string(runes[idx])))
			_ = result.Set("done", engine.Boolean(false))
			idx++
		}
		return result, nil
	})
	_ = iterObj.Set("next", nextFn)
	_ = iterObj.Set(engine.SymbolIterator.SymbolKey(), v.interp.nativeMethod("[Symbol.iterator]", func(this engine.Value, args []engine.Value) (engine.Value, error) {
		return iterObj, nil
	}))
	return iterObj
}

// === super call support ===================================================

// doCallThis pops numArgs + callee and calls/constructs it with this = the
// current frame's slot 0 (the `this` binding). Used for super.method() and
// super() inside class methods/constructors.
func (v *VM) doCallThis(numArgs int, asNew bool) (engine.Value, error) {
	argStart := len(v.stack) - numArgs
	callee := v.stack[argStart-1]
	args := make([]engine.Value, numArgs)
	copy(args, v.stack[argStart:argStart+numArgs])
	v.stack = v.stack[:argStart-1]
	thisVal := *v.local(0)
	if asNew {
		return v.constructThis(callee, thisVal, args)
	}
	return v.invoke(callee, thisVal, args, false)
}

// doCallThisArgs is the spread-args variant of doCallThis.
func (v *VM) doCallThisArgs(asNew bool) (engine.Value, error) {
	argsArr := v.pop()
	callee := v.pop()
	args := v.toArrayValues(argsArr)
	thisVal := *v.local(0)
	if asNew {
		return v.constructThis(callee, thisVal, args)
	}
	return v.invoke(callee, thisVal, args, false)
}

// constructThis calls callee as a constructor but with a pre-existing `this`
// (the current frame's slot 0). This implements super() semantics: the parent
// constructor runs on the same `this` object instead of creating a new one.
func (v *VM) constructThis(callee engine.Value, thisVal engine.Value, args []engine.Value) (engine.Value, error) {
	if cl, ok := callee.(*vmClosure); ok {
		return v.callClosureThis(cl, thisVal, args)
	}
	// Native/non-closure parent: fall back to regular construct. This creates
	// a new `this` instead of reusing the current one — an MVP limitation for
	// extending built-in constructors via super().
	if ac, ok := callee.(*Closure); ok {
		return ac.construct(args)
	}
	return v.invoke(callee, engine.Undefined(), args, true)
}

// callClosureThis sets up a new VM frame for a bytecode closure, reusing the
// caller's `this` value (slot 0) instead of creating a new object. Used by
// super() to chain constructor calls on the same instance.
func (v *VM) callClosureThis(cl *vmClosure, thisVal engine.Value, args []engine.Value) (engine.Value, error) {
	tmpl := cl.tmpl
	frame := vmFrame{
		tmpl:     tmpl,
		base:     len(v.stack),
		upvalues: cl.upvalues,
	}
	v.reserveUndefined(tmpl.NumLocals)
	v.stack[frame.base] = thisVal // reuse the caller's this
	for i := 0; i < tmpl.NumParams && i < len(args); i++ {
		v.stack[frame.base+1+i] = args[i]
	}
	if tmpl.IsVarArgs {
		restSlot := frame.base + 1 + tmpl.NumParams
		var restElems []engine.Value
		if len(args) > tmpl.NumParams {
			restElems = append(restElems, args[tmpl.NumParams:]...)
		}
		restArr := engine.NewArray(restElems)
		engine.SetProto(restArr, v.interp.arrayProto)
		v.stack[restSlot] = restArr
	}
	v.frames = append(v.frames, frame)
	result, err := v.run()
	// super() returns this if the parent ctor didn't return an object.
	if err == nil && !result.IsObject() {
		result = thisVal
	}
	return result, err
}

// invoke calls callee with the given this and args, handling both bytecode
// closures and native functions. When `asNew` is true, it constructs a new
// object for constructors. The callee and args must already be popped from
// the stack before calling invoke; the result is returned (not pushed).
func (v *VM) invoke(callee engine.Value, thisVal engine.Value, args []engine.Value, asNew bool) (engine.Value, error) {
	// Bytecode closure: set up a new frame.
	if cl, ok := callee.(*vmClosure); ok {
		return v.callClosure(cl, thisVal, args, asNew)
	}
	// NativeMethod: receives this.
	if nm, ok := callee.(*NativeMethod); ok {
		if asNew {
			return nm.construct(args)
		}
		return nm.callWith(thisVal, args)
	}
	// AST Closure (from interpreter mode): use callWith/construct.
	if ac, ok := callee.(*Closure); ok {
		if asNew {
			return ac.construct(args)
		}
		return ac.callWith(thisVal, args)
	}
	// Generic engine.Function (e.g. makeFunc results like Object, Array).
	if f, ok := callee.AsFunction(); ok {
		if asNew {
			return v.constructNative(f, callee, args)
		}
		return f.Call(args)
	}
	if asNew {
		return engine.Undefined(), fmt.Errorf("%w: %s is not a constructor", engine.ErrTypeError, callee.Type())
	}
	return engine.Undefined(), fmt.Errorf("%w: %s is not a function", engine.ErrTypeError, callee.String())
}

// constructNative handles `new NativeFunc(args)` for Go-backed functions.
func (v *VM) constructNative(f engine.Function, callee engine.Value, args []engine.Value) (engine.Value, error) {
	// Special-case our makeCtor-style constructors that store the actual
	// callable under "__call__". These build their own `this` object.
	if fo, ok := callee.AsObject(); ok {
		if callProp, err := fo.Get("__call__"); err == nil && !callProp.IsUndefined() {
			if inner, ok := callProp.AsFunction(); ok {
				return inner.Call(args)
			}
		}
	}
	// Generic path: create a new object with proto from f.prototype, call f.
	newObj := engine.NewObject()
	engine.SetProto(newObj, v.interp.objectProto)
	if fo, ok := callee.AsObject(); ok {
		if proto, err := fo.Get("prototype"); err == nil && !proto.IsUndefined() {
			if protoObj, ok := proto.(engine.Object); ok {
				engine.SetProto(newObj, protoObj)
			}
		}
	}
	result, err := f.Call(args)
	if err != nil {
		return engine.Undefined(), err
	}
	if result.IsObject() {
		return result, nil
	}
	return newObj, nil
}

// callClosure sets up a new VM frame for a bytecode closure and runs it.
// The callee and args must already be popped from the stack.
func (v *VM) callClosure(cl *vmClosure, thisVal engine.Value, args []engine.Value, asNew bool) (engine.Value, error) {
	tmpl := cl.tmpl
	// Generator function: calling it returns a generator object rather than
	// executing the body. The body runs lazily on each .next() call.
	if tmpl.IsGenerator {
		gen := NewGeneratorValue(v, tmpl, cl.module, cl.upvalues, thisVal, args)
		return gen, nil
	}
	// Async function: calling it returns a Promise. The body runs with
	// suspension at each OpAwait; the asyncRunner resolves/rejects the
	// Promise when the body completes or throws.
	if tmpl.IsAsync {
		ar := newAsyncRunner(v, tmpl, cl.module, cl.upvalues, thisVal, args)
		return ar.start(), nil
	}
	// 切换到闭包定义时的 module，使函数体内 OpMakeClosure/OpMakeClass 的
	// fnIdx/classIdx 解析到正确的 Functions/Classes（跨模块闭包，如 CJS
	// getter 在另一模块上下文调用）。
	savedModule := v.module
	if cl.module != nil {
		v.module = cl.module
	}
	defer func() { v.module = savedModule }()
	if asNew {
		// Create a new object with proto from cl.prototype.
		newObj := engine.NewObject()
		engine.SetProto(newObj, v.interp.objectProto)
		if proto, err := cl.obj.Get("prototype"); err == nil && !proto.IsUndefined() {
			if protoObj, ok := proto.(engine.Object); ok {
				engine.SetProto(newObj, protoObj)
			}
		}
		thisVal = newObj
	}

	// Set up the new frame.
	frame := vmFrame{
		tmpl:     tmpl,
		base:     len(v.stack),
		upvalues: cl.upvalues,
	}
	// Reserve local slots: slot 0 = this, 1..N = params, rest = undefined.
	v.reserveUndefined(tmpl.NumLocals)
	v.stack[frame.base] = thisVal
	for i := 0; i < tmpl.NumParams && i < len(args); i++ {
		v.stack[frame.base+1+i] = args[i]
	}
	// Rest parameter: collect remaining args into an array at slot 1+NumParams.
	if tmpl.IsVarArgs {
		restSlot := frame.base + 1 + tmpl.NumParams
		var restElems []engine.Value
		if len(args) > tmpl.NumParams {
			restElems = append(restElems, args[tmpl.NumParams:]...)
		}
		restArr := engine.NewArray(restElems)
		engine.SetProto(restArr, v.interp.arrayProto)
		v.stack[restSlot] = restArr
	}

	// Populate the `arguments` object for non-arrow functions (slot from
	// ArgumentsSlot). The arguments object is array-like: it has numeric
	// indices and a `length`. We bind it as a local so `arguments.length` /
	// `arguments[i]` work（get-intrinsic 等 npm 包依赖它）。
	if tmpl.ArgumentsSlot >= 0 {
		argsObj := engine.NewArray(append([]engine.Value{}, args...))
		engine.SetProto(argsObj, v.interp.arrayProto)
		_ = argsObj.Set("callee", cl.obj)
		v.stack[frame.base+tmpl.ArgumentsSlot] = argsObj
	}

	v.frames = append(v.frames, frame)
	result, err := v.run()
	if err != nil {
		// Uncaught exception: run() does NOT pop the frame on error.
		// Clean up so the caller's handleThrow sees its own frame on top.
		v.closeUpvalues(frame.base)
		v.stack = v.stack[:frame.base]
		v.frames = v.frames[:len(v.frames)-1]
		return result, err
	}
	// run() pops the frame on normal return.
	// Constructor semantics: if the function returns a non-object, the
	// newly-created `this` object is the result of `new`.
	if asNew && !result.IsObject() {
		result = thisVal
	}
	return result, nil
}

// doReturn is called from the run loop when a function returns. It pops the
// current frame, closes any open upvalues pointing into it, and truncates
// the stack. The return value is returned to the caller (NOT pushed — the
// caller's OpCall handler pushes it).
func (v *VM) doReturn(retVal engine.Value) engine.Value {
	frame := v.cur()
	// Close ALL open upvalues pointing into this frame's slots (locals +
	// operands). Using frame.base as the threshold ensures closures that
	// captured this frame's locals get a stable copy before the stack is
	// truncated; otherwise the upvalue would hold a dangling pointer.
	v.closeUpvalues(frame.base)
	// Truncate stack to the frame's base (removes locals + operands).
	v.stack = v.stack[:frame.base]
	// Pop the frame.
	v.frames = v.frames[:len(v.frames)-1]
	return retVal
}

// === Upvalue management ===================================================

// captureUpvalues creates upvalue objects for a closure based on its template.
func (v *VM) captureUpvalues(tmpl *bytecode.FuncTemplate) []*upvalue {
	if len(tmpl.Upvalues) == 0 {
		return nil
	}
	frame := v.cur()
	uvs := make([]*upvalue, len(tmpl.Upvalues))
	for i, cap := range tmpl.Upvalues {
		if cap.IsLocal {
			// Open upvalue: point at the parent frame's local slot.
			absIndex := frame.base + cap.Index
			slot := &v.stack[absIndex]
			// Reuse an existing open upvalue for the same slot so that
			// multiple closures capturing the same variable share state
			// (writes by one closure are visible to others).
			var existing *upvalue
			for _, ou := range frame.openUpvalues {
				if ou.slot == slot || (ou.slot != nil && ou.index == absIndex) {
					existing = ou
					break
				}
			}
			if existing != nil {
				uvs[i] = existing
			} else {
				uv := &upvalue{slot: slot, index: absIndex}
				frame.openUpvalues = append(frame.openUpvalues, uv)
				uvs[i] = uv
			}
		} else {
			// Inherited upvalue: share the parent's upvalue.
			uvs[i] = frame.upvalues[cap.Index]
		}
	}
	return uvs
}

// upvalueClose 记录被关闭的 upvalue 及其栈槽绝对索引（async 恢复时用于
// 把闭包修改的值同步回函数体读写的栈槽）。
type upvalueClose struct {
	uv     *upvalue
	absIdx int
}

// closeUpvalues closes all open upvalues pointing at stack slots >= threshold.
// 返回被关闭的 upvalue 列表（供 async 挂起/恢复时同步捕获的局部变量）。
func (v *VM) closeUpvalues(threshold int) []upvalueClose {
	frame := v.cur()
	kept := frame.openUpvalues[:0]
	var closed []upvalueClose
	for _, uv := range frame.openUpvalues {
		if uv.slot == nil {
			continue
		}
		idx := uv.index
		if idx < 0 || idx >= len(v.stack) {
			// Defensive fallback for upvalues created before index tracking.
			idx = -1
			for i := range v.stack {
				if &v.stack[i] == uv.slot {
					idx = i
					break
				}
			}
		}
		if idx >= threshold {
			uv.closed = *uv.slot
			uv.slot = nil
			closed = append(closed, upvalueClose{uv: uv, absIdx: idx})
		} else {
			kept = append(kept, uv)
		}
	}
	frame.openUpvalues = kept
	return closed
}

// === Property access =====================================================

// propertyKeyOf converts a value to a property key string. Symbols use their
// unique SymbolKey(); other values use their string representation.
func propertyKeyOf(key engine.Value) string {
	if sym, ok := key.(*engine.SymbolValue); ok {
		return sym.SymbolKey()
	}
	return key.String()
}

// getProperty reads a property from a value, handling primitives via prototypes.
func (v *VM) getProperty(obj engine.Value, key string) (engine.Value, error) {
	if obj.IsNull() || obj.IsUndefined() {
		return engine.Undefined(), fmt.Errorf("%w: Cannot read properties of %s (reading '%s')", engine.ErrTypeError, obj.String(), key)
	}
	// Proxy interception: dispatch to the get trap if defined.
	if p, ok := obj.(*ProxyValue); ok {
		return p.proxyGet(key)
	}
	// String primitives: handle length + indexed access + string proto methods.
	if obj.Type() == engine.TypeString {
		if key == "length" {
			return engine.IntValue(len(obj.String())), nil
		}
		// Numeric index → character.
		if n, err := strconv.Atoi(key); err == nil {
			s := obj.String()
			if n >= 0 && n < len(s) {
				return engine.Str(string(s[n])), nil
			}
			return engine.Undefined(), nil
		}
		if v.interp.stringProto != nil {
			return v.interp.stringProto.Get(key)
		}
		return engine.Undefined(), nil
	}
	// Array length.
	if arr, ok := obj.(*engine.ArrayValue); ok {
		if key == "length" {
			return engine.IntValue(len(arr.Elems())), nil
		}
		// 仅非负规范索引走元素路径；负索引（如 jsdiff 的 bestPath[-1]）
		// 是普通自有属性，须落到下方 own 属性查找。
		if n, err := strconv.Atoi(key); err == nil && n >= 0 {
			elems := arr.Elems()
			if n < len(elems) {
				return elems[n], nil
			}
			return engine.Undefined(), nil
		}
		// 数组 own 属性 miss：先查显式原型链（绑定了 arrayProto 的数组），
		// 再回退到 interp.arrayProto（Go 侧创建、未绑定原型的数组）。
		if o, ok := arr.AsObject(); ok {
			if val, _ := o.Get(key); !val.IsUndefined() {
				return val, nil
			}
		}
		if v.interp.arrayProto != nil {
			return v.interp.arrayProto.Get(key)
		}
		return engine.Undefined(), nil
	}
	// Number/boolean primitives: look up on prototype.
	switch obj.Type() {
	case engine.TypeNumber:
		if v.interp.numberProto != nil {
			return v.interp.numberProto.Get(key)
		}
	case engine.TypeBoolean:
		if v.interp.booleanProto != nil {
			return v.interp.booleanProto.Get(key)
		}
	case engine.TypeBigInt:
		if v.interp.bigintProto != nil {
			return v.interp.bigintProto.Get(key)
		}
	case engine.TypeFunction:
		// 函数对象：先查 own（name/length 等），miss 后回退 Function.prototype
		// （native 函数的 [[Prototype]] 未链接 functionProto）。
		if o, ok := obj.(engine.Object); ok {
			if val, _ := o.Get(key); !val.IsUndefined() {
				// 访问器属性：调用 getter（this=函数对象）。
				if acc, ok := val.(*engine.AccessorValue); ok && !acc.Getter.IsUndefined() {
					return v.invoke(acc.Getter, obj, nil, false)
				}
				return val, nil
			}
		}
		if v.interp.functionProto != nil {
			return v.interp.functionProto.Get(key)
		}
	}
	// Accessor (getter/setter) interception: if an accessor is found on the
	// prototype chain for this key, invoke the getter with this = obj.
	// For custom value types (MapValue, SetValue, etc.) the proto chain lives
	// on the backing obj, so we search from there.
	backing := v.backingObj(obj)
	if acc, ok := engine.FindAccessor(backing, key); ok {
		if acc.Getter != nil && !acc.Getter.IsUndefined() {
			return v.invoke(acc.Getter, obj, nil, false)
		}
		return engine.Undefined(), nil
	}
	if o, ok := obj.AsObject(); ok {
		// 内联缓存快速路径（隐藏类 own 属性直接读槽）。
		if cv, hit := v.ic.GetCached(obj, key); hit {
			return cv, nil
		}
		val, err := o.Get(key)
		v.ic.CachePut(obj, key)
		return val, err
	}
	return engine.Undefined(), nil
}

// backingObj returns the underlying engine.Object for custom value types
// (PromiseValue, GeneratorValue, MapValue, SetValue, WeakMapValue, WeakSetValue)
// whose proto chain lives on a backing obj. For other values, returns val as-is.
func (v *VM) backingObj(val engine.Value) engine.Value {
	if p, ok := val.(*PromiseValue); ok {
		return p.obj
	}
	if g, ok := val.(*GeneratorValue); ok {
		return g.obj
	}
	if m, ok := val.(*MapValue); ok {
		return m.obj
	}
	if s, ok := val.(*SetValue); ok {
		return s.obj
	}
	if w, ok := val.(*WeakMapValue); ok {
		return w.obj
	}
	if w, ok := val.(*WeakSetValue); ok {
		return w.obj
	}
	if r, ok := val.(*RegexpValue); ok {
		return r.obj
	}
	return val
}

// setProperty writes a property on a value.
func (v *VM) setProperty(obj engine.Value, key string, val engine.Value) error {
	// Proxy interception: dispatch to the set trap if defined.
	if p, ok := obj.(*ProxyValue); ok {
		return p.proxySet(key, val)
	}
	// Accessor (getter/setter) interception: if an accessor is found on the
	// prototype chain for this key, invoke the setter with this = obj.
	// For custom value types, search from the backing obj.
	backing := v.backingObj(obj)
	if acc, ok := engine.FindAccessor(backing, key); ok {
		if acc.Setter != nil && !acc.Setter.IsUndefined() {
			_, err := v.invoke(acc.Setter, obj, []engine.Value{val}, false)
			return err
		}
		// Read-only accessor: silently ignore (strict mode would throw).
		return nil
	}
	// Array indexed assignment：委托给 ArrayValue.Set（正确处理追加索引与 length 同步）。
	if arr, ok := obj.(*engine.ArrayValue); ok {
		return arr.Set(key, val)
	}
	if o, ok := obj.AsObject(); ok {
		return o.Set(key, val)
	}
	// Primitives: silently ignore (strict mode would throw, but we don't enforce).
	return nil
}

// === Operators ===========================================================

func (v *VM) binAdd(l, r engine.Value) engine.Value {
	if l.Type() == engine.TypeString || r.Type() == engine.TypeString {
		return engine.Str(l.String() + r.String())
	}
	ln, _ := l.Float()
	rn, _ := r.Float()
	return engine.Number(ln + rn)
}

func (v *VM) instanceof(l, r engine.Value) bool {
	ro, ok := r.AsObject()
	if !ok {
		return false
	}
	// Proxy interception: if r is a Proxy, get [Symbol.hasInstance] via the
	// get trap (passing the actual Symbol value so the handler can compare
	// `key === Symbol.hasInstance`). If found and callable, invoke it.
	if p, ok := r.(*ProxyValue); ok {
		hasInstanceVal, err := p.proxyGetSymbol(engine.SymbolHasInstance)
		if err == nil && !hasInstanceVal.IsUndefined() && isCallable(hasInstanceVal) {
			result, err := v.invoke(hasInstanceVal, r, []engine.Value{l}, false)
			if err != nil {
				return false
			}
			b, _ := result.Bool()
			return b
		}
		// No [Symbol.hasInstance]: fall through using the target's prototype.
		r = p.target
		ro, ok = r.AsObject()
		if !ok {
			return false
		}
	}
	// Symbol.hasInstance on the constructor takes precedence over [[Prototype]] walk.
	if hasInstanceVal, err := ro.Get(engine.SymbolHasInstance.SymbolKey()); err == nil && !hasInstanceVal.IsUndefined() && isCallable(hasInstanceVal) {
		result, err := v.invoke(hasInstanceVal, r, []engine.Value{l}, false)
		if err != nil {
			return false
		}
		b, _ := result.Bool()
		return b
	}
	proto, err := ro.Get("prototype")
	if err != nil || proto.IsUndefined() {
		return false
	}
	protoObj, ok := proto.(engine.Object)
	if !ok {
		return false
	}
	cur := v.getProto(l)
	for cur != nil {
		if cur == protoObj {
			return true
		}
		cur = v.getProto(cur)
	}
	return false
}

// getProto returns the [[Prototype]] of a value, handling custom value types
// (PromiseValue, GeneratorValue, MapValue, SetValue, WeakMapValue, WeakSetValue)
// whose proto lives on a backing object.
func (v *VM) getProto(val engine.Value) engine.Object {
	if p, ok := val.(*ProxyValue); ok {
		proto, err := p.proxyGetProto()
		if err != nil {
			return nil
		}
		return proto
	}
	if p, ok := val.(*PromiseValue); ok {
		return engine.GetProto(p.obj)
	}
	if g, ok := val.(*GeneratorValue); ok {
		return engine.GetProto(g.obj)
	}
	if m, ok := val.(*MapValue); ok {
		return engine.GetProto(m.obj)
	}
	if s, ok := val.(*SetValue); ok {
		return engine.GetProto(s.obj)
	}
	if w, ok := val.(*WeakMapValue); ok {
		return engine.GetProto(w.obj)
	}
	if w, ok := val.(*WeakSetValue); ok {
		return engine.GetProto(w.obj)
	}
	if r, ok := val.(*RegexpValue); ok {
		return engine.GetProto(r.obj)
	}
	return engine.GetProto(val)
}

func (v *VM) inOp(l, r engine.Value) bool {
	// Proxy interception: dispatch to the has trap if defined.
	if p, ok := r.(*ProxyValue); ok {
		has, err := p.proxyHas(l.String())
		if err != nil {
			return false
		}
		return has
	}
	o, ok := r.AsObject()
	if !ok {
		return false
	}
	key := l.String()
	// Walk the prototype chain checking key existence. We cannot rely on
	// Get() returning an error for missing keys (it returns Undefined, nil),
	// so we check Keys() membership at each level.
	cur := o
	for cur != nil {
		for _, k := range cur.Keys() {
			if k == key {
				return true
			}
		}
		cur = v.getProto(cur)
	}
	return false
}

// === Try / catch =========================================================

// jsThrow wraps a JS exception value as a Go error so it can propagate through
// Go's error return values while preserving the original JS value.
type jsThrow struct {
	val engine.Value
}

func (e *jsThrow) Error() string { return e.val.String() }

// handleThrow processes a thrown exception (value or Go error). It searches
// ONLY the current frame's try-stack for a matching handler. If found, it
// jumps to the catch/finally and resumes execution. If not, it returns a
// *jsThrow so the caller (run → callClosure → invoke → doCall → OpCall) can
// propagate it to the outer frame, which will call handleThrow again.
func (v *VM) handleThrow(exc interface{}) (engine.Value, error) {
	excVal := v.normalizeException(exc)
	_, jumped := v.findHandlerInFrame(excVal)
	if jumped {
		// Handler found in the current frame; resume execution.
		return v.run()
	}
	// No handler in the current frame: wrap and return.
	return engine.Undefined(), &jsThrow{val: excVal}
}

// findHandlerInFrame searches the current frame's try-stack for a handler that
// can catch the exception. If found, it sets the phase, stores the exception,
// sets the PC to catch/finally, and returns (handler, true). If not found,
// returns (nil, false).
func (v *VM) findHandlerInFrame(excVal engine.Value) (*vmTryHandler, bool) {
	frame := v.cur()
	for i := len(frame.tryStack) - 1; i >= 0; i-- {
		h := frame.tryStack[i]
		if h.phase >= 1 {
			// Already in catch (phase 1) or finally (phase 2); a re-thrown
			// exception must propagate to an OUTER handler, not re-enter this one.
			// Pop this handler so the search continues to enclosing handlers.
			frame.tryStack = frame.tryStack[:i]
			continue
		}
		// This is the handler to use. Pop handlers above it.
		frame.tryStack = frame.tryStack[:i+1]
		if h.entry.HasCatch {
			h.phase = 1
			h.exc = excVal
			// Push the exception value; the catch code does OpStoreLocal into the param.
			v.push(excVal)
			frame.pc = h.entry.CatchPC
			return h, true
		}
		if h.entry.HasFinally {
			h.phase = 2
			h.exc = excVal
			frame.pc = h.entry.FinallyPC
			return h, true
		}
		// No catch and no finally: pop and continue searching.
		frame.tryStack = frame.tryStack[:i]
	}
	return nil, false
}

// handleTryExit is called for OpTryExit (normal exit from try or catch).
func (v *VM) handleTryExit(tryIdx int) {
	frame := v.cur()
	for i := len(frame.tryStack) - 1; i >= 0; i-- {
		h := frame.tryStack[i]
		if h.entry == &frame.tmpl.TryTable[tryIdx] {
			// If in catch (phase 1), the exception is handled — clear it.
			if h.phase == 1 {
				h.exc = nil
			}
			// If there's a finally, transition to finally phase (don't pop).
			if h.entry.HasFinally {
				h.phase = 2
				return
			}
			// No finally: pop the handler.
			frame.tryStack = frame.tryStack[:i]
			return
		}
	}
}

// handleTryExitFinally is called for OpTryExitFinally. It pops the handler
// and returns a non-nil error if the exception should be re-thrown.
func (v *VM) handleTryExitFinally(tryIdx int) error {
	frame := v.cur()
	for i := len(frame.tryStack) - 1; i >= 0; i-- {
		h := frame.tryStack[i]
		if h.entry == &frame.tmpl.TryTable[tryIdx] {
			frame.tryStack = frame.tryStack[:i]
			if h.exc != nil {
				return &jsThrow{val: h.exc}
			}
			return nil
		}
	}
	return nil
}

// normalizeException converts a thrown value (engine.Value or Go error) into
// an engine.Value suitable for the JS catch clause.
func (v *VM) normalizeException(exc interface{}) engine.Value {
	switch e := exc.(type) {
	case engine.Value:
		return e
	case *jsThrow:
		return e.val
	case error:
		return v.goErrorToValue(e)
	default:
		return engine.Str(fmt.Sprintf("%v", e))
	}
}

// goErrorToValue converts a Go error to a JS Value (Error object or string).
func (v *VM) goErrorToValue(err error) engine.Value {
	return v.interp.goErrorToJSValue(err)
}

// === vmClosure: bytecode function value ===================================

// vmClosure is a function value backed by a bytecode template + captured upvalues.
type vmClosure struct {
	obj      engine.Object // function object (name, length, prototype, ...)
	vm       *VM
	tmpl     *bytecode.FuncTemplate
	upvalues []*upvalue
	module   *bytecode.Module // 定义时的 module（OpMakeClosure 内部创建子闭包时用）
}

// newVMClosure creates a vmClosure with a fresh function object.
func newVMClosure(vm *VM, tmpl *bytecode.FuncTemplate, upvalues []*upvalue) *vmClosure {
	return &vmClosure{
		obj:      engine.NewObject(),
		vm:       vm,
		tmpl:     tmpl,
		upvalues: upvalues,
		module:   vm.module,
	}
}

func (c *vmClosure) Type() engine.ValueType { return engine.TypeFunction }
func (c *vmClosure) String() string {
	if name, _ := c.obj.Get("name"); !name.IsUndefined() {
		return "[Function: " + name.String() + "]"
	}
	return "[Function (anonymous)]"
}
func (c *vmClosure) Int() (int, bool)                    { return 0, false }
func (c *vmClosure) Float() (float64, bool)              { return 0, false }
func (c *vmClosure) Bool() (bool, bool)                  { return true, true }
func (c *vmClosure) IsUndefined() bool                   { return false }
func (c *vmClosure) IsNull() bool                        { return false }
func (c *vmClosure) IsObject() bool                      { return true }
func (c *vmClosure) IsFunction() bool                    { return true }
func (c *vmClosure) AsObject() (engine.Object, bool)     { return c, true }
func (c *vmClosure) AsFunction() (engine.Function, bool) { return c, true }

func (c *vmClosure) Get(key string) (engine.Value, error)   { return c.obj.Get(key) }
func (c *vmClosure) Set(key string, val engine.Value) error { return c.obj.Set(key, val) }
func (c *vmClosure) Keys() []string                         { return c.obj.Keys() }
func (c *vmClosure) Delete(key string) bool                 { return c.obj.Delete(key) }
func (c *vmClosure) Proto() engine.Object                   { return engine.GetProto(c.obj) }
func (c *vmClosure) SetProto(proto engine.Object)           { engine.SetProto(c.obj, proto) }

// Call implements engine.Function — calls the closure with this=undefined.
func (c *vmClosure) Call(args []engine.Value) (engine.Value, error) {
	return c.vm.callClosure(c, engine.Undefined(), args, false)
}

// callWith 以指定 this 调用（实现 callableValue，供 Function.prototype
// call/apply/bind 正确绑定 this；P0-2 配套修复）。
func (c *vmClosure) callWith(thisVal engine.Value, args []engine.Value) (engine.Value, error) {
	return c.vm.callClosure(c, thisVal, args, false)
}

// construct 以 new 语义调用（供 Function.prototype.call/apply 对构造器路径）。
func (c *vmClosure) construct(args []engine.Value) (engine.Value, error) {
	return c.vm.callClosure(c, engine.Undefined(), args, true)
}

// InvokeFn 以指定 this 和参数调用函数值（供外部包调用 JS 函数，如 loadCJS 触发 getter）。
func (v *VM) InvokeFn(fn, this engine.Value, args []engine.Value) (engine.Value, error) {
	return v.invoke(fn, this, args, false)
}

// DrainMicrotasks 排空微任务队列（Promise reactions、queueMicrotask、async
// 继续）。仅当无活跃 JS 帧（顶层模块加载场景）时安全调用，供 Loader 在
// 模块函数包装（P0-1）执行完毕后触发，模拟原 RunModule 顶层分支的排水行为。
func (v *VM) DrainMicrotasks() {
	if len(v.frames) == 0 {
		v.interp.drainMicrotasks()
	}
}

// AwaitPromise 同步等待 promise settle（顶层 await / TLA 的模块加载语义）。
// 循环驱动微任务队列与投递的任务（IO 回调等），直至 promise 完成。
// 供 Loader 在 async 模块函数（含 TLA）执行后调用。
func (v *VM) AwaitPromise(p *PromiseValue) (engine.Value, error) {
	for {
		switch p.state {
		case promiseFulfilled:
			return p.result, nil
		case promiseRejected:
			return engine.Undefined(), &jsThrow{val: p.result}
		}
		v.interp.drainMicrotasks()
		select {
		case fn := <-v.interp.taskCh:
			fn()
		case <-v.interp.idleCh:
			// 空闲信号：无任务在途，继续微任务驱动（TLA 依赖同步 promise）。
		default:
			// 微任务已排空且无投递任务：IO 在途（await fetch 等），短暂让出。
			time.Sleep(time.Millisecond)
		}
	}
}

// FlushMicrotasks 无条件排空微任务队列（implements engine.Context）。
// 与 DrainMicrotasks 不同，不计较是否有活跃 JS 帧：HTTP handler 等在
// 同步返回后仍需驱动 Promise/async 续期，直到响应完成。
func (v *VM) FlushMicrotasks() bool {
	return v.interp.drainMicrotasksReport()
}
