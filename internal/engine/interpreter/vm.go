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

	"github.com/aluka-lang/aluka/internal/engine"
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

// Global returns the global object (implements engine.Context).
func (v *VM) Global() engine.Object { return v.interp.Global() }

// RegisterFunc registers a Go function as a global (implements engine.Context).
func (v *VM) RegisterFunc(name string, fn engine.Func) error {
	return v.interp.RegisterFunc(name, fn)
}

// Close releases context resources (implements engine.Context).
func (v *VM) Close() error { return nil }

// Eval parses, compiles, and executes JS source.
func (v *VM) Eval(src, filename string) (engine.Value, error) {
	prog, err := parser.Parse(src)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("%w: %s: %v", engine.ErrSyntaxError, filename, err)
	}
	comp := compiler.New()
	mod, err := comp.Compile(prog, filename)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("aluka: compile error: %w", err)
	}
	return v.runModule(mod)
}

// runModule executes the top-level function of a module.
func (v *VM) runModule(mod *bytecode.Module) (engine.Value, error) {
	v.module = mod
	main := mod.Functions[0]
	// Reserve locals for the top-level frame.
	v.stack = make([]engine.Value, 0, main.NumLocals+16)
	for i := 0; i < main.NumLocals; i++ {
		v.stack = append(v.stack, engine.Undefined())
	}
	v.frames = append(v.frames, vmFrame{
		tmpl: main,
		base: 0,
	})
	result, err := v.run()
	v.frames = v.frames[:0]
	return result, err
}

// cur returns the top call frame.
func (v *VM) cur() *vmFrame {
	return &v.frames[len(v.frames)-1]
}

// === Stack helpers ========================================================

func (v *VM) push(val engine.Value) { v.stack = append(v.stack, val) }

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
			pc := frame.pc
			if pc >= len(code) {
				// Ran off the end without OpReturn — treat as undefined return.
				return v.doReturn(engine.Undefined()), nil
			}
			op, operand, next := bytecode.Decode(code, pc)
			frame.pc = next

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
				v.push(closure)

			// --- Binary arithmetic & bitwise ---
			case bytecode.OpAdd:
				r := v.pop()
				l := v.pop()
				v.push(v.binAdd(l, r))
			case bytecode.OpSub:
				r := v.pop()
				l := v.pop()
				ln, _ := l.Float()
				rn, _ := r.Float()
				v.push(engine.Number(ln - rn))
			case bytecode.OpMul:
				r := v.pop()
				l := v.pop()
				ln, _ := l.Float()
				rn, _ := r.Float()
				v.push(engine.Number(ln * rn))
			case bytecode.OpDiv:
				r := v.pop()
				l := v.pop()
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
			case bytecode.OpMod:
				r := v.pop()
				l := v.pop()
				ln, _ := l.Float()
				rn, _ := r.Float()
				if rn == 0 {
					v.push(engine.Number(math.NaN()))
				} else {
					v.push(engine.Number(math.Mod(ln, rn)))
				}
			case bytecode.OpPow:
				r := v.pop()
				l := v.pop()
				ln, _ := l.Float()
				rn, _ := r.Float()
				v.push(engine.Number(math.Pow(ln, rn)))
			case bytecode.OpBitAnd:
				r := v.pop()
				l := v.pop()
				ln, _ := l.Int()
				rn, _ := r.Int()
				v.push(engine.Number(float64(ln & rn)))
			case bytecode.OpBitOr:
				r := v.pop()
				l := v.pop()
				ln, _ := l.Int()
				rn, _ := r.Int()
				v.push(engine.Number(float64(ln | rn)))
			case bytecode.OpBitXor:
				r := v.pop()
				l := v.pop()
				ln, _ := l.Int()
				rn, _ := r.Int()
				v.push(engine.Number(float64(ln ^ rn)))
			case bytecode.OpShl:
				r := v.pop()
				l := v.pop()
				ln, _ := l.Int()
				rn, _ := r.Int()
				v.push(engine.Number(float64(ln << (uint(rn) & 31))))
			case bytecode.OpShr:
				r := v.pop()
				l := v.pop()
				ln, _ := l.Int()
				rn, _ := r.Int()
				v.push(engine.Number(float64(ln >> (uint(rn) & 31))))
			case bytecode.OpUShr:
				r := v.pop()
				l := v.pop()
				ln, _ := l.Int()
				rn, _ := r.Int()
				v.push(engine.Number(float64(uint32(ln) >> (uint(rn) & 31))))

			// --- Unary ---
			case bytecode.OpNeg:
				n, _ := v.pop().Float()
				v.push(engine.Number(-n))
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
				n, _ := v.pop().Float()
				v.push(engine.Number(n))

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

			// --- Functions ---
			case bytecode.OpCall:
				numArgs := int(operand)
				result, err := v.doCall(numArgs, engine.Undefined())
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
				name := tmpl.Constants[operand].String()
				val := v.pop()
				obj := v.pop()
				if err := v.setProperty(obj, name, val); err != nil {
					return v.handleThrow(err)
				}
			case bytecode.OpGetElem:
				key := v.pop()
				obj := v.pop()
				val, err := v.getProperty(obj, key.String())
				if err != nil {
					return v.handleThrow(err)
				}
				v.push(val)
			case bytecode.OpSetElem:
				val := v.pop()
				key := v.pop()
				obj := v.pop()
				if err := v.setProperty(obj, key.String(), val); err != nil {
					return v.handleThrow(err)
				}
				v.push(val)
			case bytecode.OpSetElemTop:
				val := v.pop()
				key := v.pop()
				obj := v.pop()
				if err := v.setProperty(obj, key.String(), val); err != nil {
					return v.handleThrow(err)
				}
			case bytecode.OpDelProp:
				// A: name-const index; pop obj, delete own prop, push bool result.
				nameIdx := int(operand)
				name := tmpl.Constants[nameIdx].String()
				obj := v.pop()
				result := true
				if o, ok := obj.AsObject(); ok {
					result = o.Delete(name)
				}
				v.push(engine.Boolean(result))

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
				} else if so, ok := spreadVal.AsObject(); ok {
					// Spread an iterable-like object: copy own enumerable keys.
					for _, k := range so.Keys() {
						if pv, err := so.Get(k); err == nil {
							arr.Append(pv)
						}
					}
				}
			case bytecode.OpSpreadObject:
				src := v.pop()
				dst := v.peek()
				dstObj, ok := dst.AsObject()
				if !ok {
					return v.handleThrow(fmt.Errorf("%w: SPREAD_OBJECT target not an object", engine.ErrTypeError))
				}
				if srcObj, ok := src.AsObject(); ok {
					for _, k := range srcObj.Keys() {
						if pv, err := srcObj.Get(k); err == nil {
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
	for i := 0; i < tmpl.NumLocals; i++ {
		v.stack = append(v.stack, engine.Undefined())
	}
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

	v.frames = append(v.frames, frame)
	result, err := v.run()
	// run() pops the frame on return.
	return result, err
}

// doReturn is called from the run loop when a function returns. It pops the
// current frame, closes any open upvalues pointing into it, and truncates
// the stack. The return value is returned to the caller (NOT pushed — the
// caller's OpCall handler pushes it).
func (v *VM) doReturn(retVal engine.Value) engine.Value {
	frame := v.cur()
	// Close open upvalues that point into this frame's locals.
	v.closeUpvalues(frame.base + frame.tmpl.NumLocals)
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
			slot := &v.stack[frame.base+cap.Index]
			uv := &upvalue{slot: slot}
			frame.openUpvalues = append(frame.openUpvalues, uv)
			uvs[i] = uv
		} else {
			// Inherited upvalue: share the parent's upvalue.
			uvs[i] = frame.upvalues[cap.Index]
		}
	}
	return uvs
}

// closeUpvalues closes all open upvalues pointing at stack slots >= threshold.
func (v *VM) closeUpvalues(threshold int) {
	frame := v.cur()
	kept := frame.openUpvalues[:0]
	for _, uv := range frame.openUpvalues {
		if uv.slot == nil {
			continue
		}
		// Find the index of the slot this upvalue points to.
		idx := -1
		for i := range v.stack {
			if &v.stack[i] == uv.slot {
				idx = i
				break
			}
		}
		if idx >= threshold {
			uv.closed = *uv.slot
			uv.slot = nil
		} else {
			kept = append(kept, uv)
		}
	}
	frame.openUpvalues = kept
}

// === Property access =====================================================

// getProperty reads a property from a value, handling primitives via prototypes.
func (v *VM) getProperty(obj engine.Value, key string) (engine.Value, error) {
	if obj.IsNull() || obj.IsUndefined() {
		return engine.Undefined(), fmt.Errorf("%w: Cannot read properties of %s (reading '%s')", engine.ErrTypeError, obj.String(), key)
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
		if n, err := strconv.Atoi(key); err == nil {
			elems := arr.Elems()
			if n >= 0 && n < len(elems) {
				return elems[n], nil
			}
			return engine.Undefined(), nil
		}
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
	}
	if o, ok := obj.AsObject(); ok {
		return o.Get(key)
	}
	return engine.Undefined(), nil
}

// setProperty writes a property on a value.
func (v *VM) setProperty(obj engine.Value, key string, val engine.Value) error {
	// Array indexed assignment.
	if arr, ok := obj.(*engine.ArrayValue); ok {
		if n, err := strconv.Atoi(key); err == nil {
			elems := arr.Elems()
			for len(elems) <= n {
				elems = append(elems, engine.Undefined())
			}
			elems[n] = val
			_ = arr.Set("length", engine.IntValue(len(elems)))
			return nil
		}
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
	proto, err := ro.Get("prototype")
	if err != nil || proto.IsUndefined() {
		return false
	}
	protoObj, ok := proto.(engine.Object)
	if !ok {
		return false
	}
	cur := engine.GetProto(l)
	for cur != nil {
		if cur == protoObj {
			return true
		}
		cur = engine.GetProto(cur)
	}
	return false
}

func (v *VM) inOp(l, r engine.Value) bool {
	o, ok := r.AsObject()
	if !ok {
		return false
	}
	_, err := o.Get(l.String())
	return err == nil
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
		if h.phase == 2 {
			// Already in finally; can't re-enter.
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
				return fmt.Errorf("%s", h.exc.String())
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
	case error:
		return v.goErrorToValue(e)
	default:
		return engine.Str(fmt.Sprintf("%v", e))
	}
}

// goErrorToValue converts a Go error to a JS Value (Error object or string).
func (v *VM) goErrorToValue(err error) engine.Value {
	if errCtor, ok := v.interp.constructors["Error"]; ok {
		if f, ok := errCtor.AsFunction(); ok {
			result, callErr := f.Call([]engine.Value{engine.Str(err.Error())})
			if callErr == nil && result.IsObject() {
				return result
			}
		}
	}
	return engine.Str(err.Error())
}

// === vmClosure: bytecode function value ===================================

// vmClosure is a function value backed by a bytecode template + captured upvalues.
type vmClosure struct {
	obj      engine.Object // function object (name, length, prototype, ...)
	vm       *VM
	tmpl     *bytecode.FuncTemplate
	upvalues []*upvalue
}

// newVMClosure creates a vmClosure with a fresh function object.
func newVMClosure(vm *VM, tmpl *bytecode.FuncTemplate, upvalues []*upvalue) *vmClosure {
	return &vmClosure{
		obj:      engine.NewObject(),
		vm:       vm,
		tmpl:     tmpl,
		upvalues: upvalues,
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

// Call implements engine.Function — calls the closure with this=undefined.
func (c *vmClosure) Call(args []engine.Value) (engine.Value, error) {
	return c.vm.callClosure(c, engine.Undefined(), args, false)
}
