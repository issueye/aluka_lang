// Quick 执行器：Go 解释 IR、安全点轮询、退出原因与帧管理。

package jit

import (
	"fmt"
	"math"
	"runtime"

	"github.com/aluka-lang/aluka/internal/engine"
)

type ExitReason uint8

const (
	Executed ExitReason = iota
	Yielded
	Interrupted
	GuardFailed
	Malformed
)

// Execute runs the typed program. false means a guard failed and the caller
// must execute the original bytecode. It never mutates JS objects.
func (p *Program) Execute(thisVal engine.Value, args []engine.Value) (engine.Value, ExitReason, error) {
	return p.ExecuteWithSafepoint(thisVal, args, 0, nil)
}

// ExecuteWithSafepoint runs the typed program and polls at loop backedges and
// recursive calls after each budget interval.
func (p *Program) ExecuteWithSafepoint(thisVal engine.Value, args []engine.Value, budget uint32, poll Safepoint) (engine.Value, ExitReason, error) {
	if p == nil {
		return engine.Undefined(), Malformed, nil
	}
	var argBuf [8]quickValue
	var objectBuf [maxQuickSlots]engine.Value
	objectCount := 0
	// The string constant pool occupies the front of the object buffer so
	// OpConstString refs and string locals share one objects slice.
	for _, constant := range p.stringConsts {
		if objectCount >= len(objectBuf) {
			return engine.Undefined(), GuardFailed, nil
		}
		objectBuf[objectCount] = constant
		objectCount++
	}
	if len(args) > len(argBuf) {
		return engine.Undefined(), GuardFailed, nil
	}
	for i, arg := range args {
		value := fromEngine(arg, &objectBuf, &objectCount)
		if value.kind == quickInvalid || value.kind == quickSelf {
			return engine.Undefined(), GuardFailed, nil
		}
		argBuf[i] = value
	}
	quickThis := fromEngine(thisVal, &objectBuf, &objectCount)
	var safepoint *quickSafepoint
	if budget != 0 || poll != nil {
		if budget == 0 {
			budget = 65536
		}
		safepoint = &quickSafepoint{interval: budget, remaining: budget, poll: poll}
	}
	result, reason, err := p.executeQuick(quickThis, &argBuf, len(args), &objectBuf, &objectCount, safepoint)
	if err != nil || reason != Executed {
		return engine.Undefined(), reason, err
	}
	return result.toEngine(objectBuf[:objectCount]), Executed, nil
}

type quickSafepoint struct {
	interval  uint32
	remaining uint32
	poll      Safepoint
}

func (s *quickSafepoint) tick() error {
	if s == nil {
		return nil
	}
	if s.remaining > 1 {
		s.remaining--
		return nil
	}
	s.remaining = s.interval
	return runSafepoint(s.poll)
}

func runSafepoint(poll Safepoint) error {
	runtime.Gosched()
	if poll != nil {
		return poll()
	}
	return nil
}

// quickFrame 是显式栈迭代（F1-Fast）的一个帧：每次自递归调用占用一层。
// prog 记录该帧执行的 program（OpSelfCall 可能切到 callTarget）。
// locals/stack 用固定数组（免分配、免 duffzero 清零——复用帧时只覆盖使用区）。
type quickFrame struct {
	prog   *Program
	locals [maxQuickSlots]quickValue
	stack  [maxQuickSlots]quickValue
	ip     int
	sp     int
}

// quickMaxFrames 是显式栈的最大帧数（对齐原递归 depth>4096 的 GuardFailed 语义）。
const quickMaxFrames = 4096

// quickFrameInit 是帧栈初始容量：fib 类递归深度 ≤ 64 时零扩容；深递归按需翻倍。
const quickFrameInit = 64

// executeQuick runs the typed program. `objects` is the shared object buffer:
// args and `this` populate it through fromEngine, and R3-4/R3-5 results
// (String concats, BigInt results) are appended to it via quickAlloc. The
// fixed-size buffer forces a GuardFailed fallback to Tier 0 when it is
// exhausted; Tier 0 never observes the buffer.
//
// 实现（F1-Fast）：显式帧栈迭代，替代 Go 递归。自递归（OpSelfCall）推帧、
// OpReturn 弹帧，消除了每次递归的 Go 调用开销、recursiveArgs 逃逸堆分配与
// localBuf/stackBuf 的 duffzero 清零。帧栈懒扩容（64 → 翻倍 → 4096 上限），
// fib 类浅递归零扩容。
func (p *Program) executeQuick(thisVal quickValue, args *[8]quickValue, argCount int, objects *[maxQuickSlots]engine.Value, objectCount *int, safepoint *quickSafepoint) (quickValue, ExitReason, error) {
	frames := make([]quickFrame, quickFrameInit)
	fp := 0
	frames[fp].prog = p
	frames[fp].ip = 0
	frames[fp].sp = 0
	{
		locals := frames[fp].locals[:p.NumLocals]
		if p.NumLocals > 0 {
			locals[0] = thisVal
		}
		for i := 1; i <= p.NumParams && i < len(locals); i++ {
			locals[i] = quickValue{kind: quickUndefined}
		}
		for i := 0; i < argCount && i+1 < len(locals); i++ {
			locals[i+1] = args[i]
		}
	}
	for {
		frame := &frames[fp]
		cur := frame.prog
		locals := frame.locals[:cur.NumLocals]
		ip := frame.ip
		in := cur.Code[ip]
		frame.ip = ip + 1
		push := func(n quickValue) { frame.stack[frame.sp] = n; frame.sp++ }
		pop := func() quickValue { frame.sp--; return frame.stack[frame.sp] }
		switch in.Op {
		case OpConst:
			push(numberValue(in.Value))
		case OpConstString:
			// The pool is prepended by ExecuteWithSafepoint; a ref beyond the
			// buffer is a defensive guard (Verify bounds the pool already).
			if int(in.Operand) >= len(objects) {
				return quickValue{}, GuardFailed, nil
			}
			truthy, _ := objects[in.Operand].Bool()
			push(quickValue{kind: quickString, ref: uint8(in.Operand), b: truthy})
		case OpLoadLocal:
			push(locals[in.Operand])
		case OpStoreLocal:
			locals[in.Operand] = pop()
		case OpGetProp:
			object := pop()
			if object.kind != quickObject {
				return quickValue{}, GuardFailed, nil
			}
			guard := &cur.propertyGuards[ip-1]
			number, ok := guard.loadNumber(objects[object.ref], in.Name)
			if !ok {
				return quickValue{}, GuardFailed, nil
			}
			push(numberValue(number))
		case OpPushSelf:
			push(quickValue{kind: quickSelf})
		case OpAdd:
			r, l := pop(), pop()
			switch {
			case l.isNumber() && r.isNumber():
				push(numberValue(l.num + r.num))
			case l.kind == quickString && r.kind == quickString:
				// R3-4: both operands guarded to String; the concat allocates
				// only here in Quick and the result stays a quickString so it
				// can feed truthiness / nullish / Return / strict equality.
				result, ok := quickStringConcat(l, r, objects, objectCount)
				if !ok {
					return quickValue{}, GuardFailed, nil
				}
				push(result)
			case l.kind == quickBigInt && r.kind == quickBigInt:
				// R3-5: same-type BigInt addition.
				result, ok := quickBigIntArith(l, r, objects, objectCount, OpAdd)
				if !ok {
					return quickValue{}, GuardFailed, nil
				}
				push(result)
			default:
				// Mixed types (String+Number coercion, BigInt+Number TypeError,
				// ...) fall back to Tier 0.
				return quickValue{}, GuardFailed, nil
			}
		case OpSub, OpMul, OpDiv, OpMod:
			r, l := pop(), pop()
			if l.isNumber() && r.isNumber() {
				var n float64
				switch in.Op {
				case OpSub:
					n = l.num - r.num
				case OpMul:
					n = l.num * r.num
				case OpDiv:
					n = l.num / r.num
				case OpMod:
					n = floatMod(l.num, r.num)
				}
				push(numberValue(n))
			} else if l.kind == quickBigInt && r.kind == quickBigInt {
				// R3-5: same-type BigInt arithmetic; division/modulo by zero
				// falls back so Tier 0 raises the identical RangeError.
				result, ok := quickBigIntArith(l, r, objects, objectCount, in.Op)
				if !ok {
					return quickValue{}, GuardFailed, nil
				}
				push(result)
			} else {
				return quickValue{}, GuardFailed, nil
			}
		case OpPow:
			r, l := pop(), pop()
			if !l.isNumber() || !r.isNumber() {
				return quickValue{}, GuardFailed, nil
			}
			push(numberValue(math.Pow(l.num, r.num)))
		case OpBitAnd, OpBitOr, OpBitXor, OpShl, OpShr, OpUShr:
			r, l := pop(), pop()
			if l.isNumber() && r.isNumber() {
				left, right := quickInt32(l.num), quickUint32(r.num)
				switch in.Op {
				case OpBitAnd:
					push(numberValue(float64(left & quickInt32(r.num))))
				case OpBitOr:
					push(numberValue(float64(left | quickInt32(r.num))))
				case OpBitXor:
					push(numberValue(float64(left ^ quickInt32(r.num))))
				case OpShl:
					push(numberValue(float64(left << (right & 31))))
				case OpShr:
					push(numberValue(float64(left >> (right & 31))))
				case OpUShr:
					push(numberValue(float64(quickUint32(l.num) >> (right & 31))))
				}
			} else if l.kind == quickBigInt && r.kind == quickBigInt {
				// R3-5: same-type BigInt bitwise ops. `>>>` and negative shifts
				// fall back so Tier 0 raises the identical TypeError/RangeError.
				result, ok := quickBigIntBitwise(l, r, objects, objectCount, in.Op)
				if !ok {
					return quickValue{}, GuardFailed, nil
				}
				push(result)
			} else {
				return quickValue{}, GuardFailed, nil
			}
		case OpNeg:
			n := pop()
			switch {
			case n.isNumber():
				push(numberValue(-n.num))
			case n.kind == quickBigInt:
				// R3-5: unary minus on BigInt.
				result, ok := quickBigIntNeg(n, objects, objectCount)
				if !ok {
					return quickValue{}, GuardFailed, nil
				}
				push(result)
			default:
				return quickValue{}, GuardFailed, nil
			}
		case OpNot:
			value := pop()
			truth, ok := value.truthy()
			if !ok {
				return quickValue{}, GuardFailed, nil
			}
			push(booleanValue(!truth))
		case OpBitNot:
			n := pop()
			switch {
			case n.isNumber():
				push(numberValue(float64(^quickInt32(n.num))))
			case n.kind == quickBigInt:
				// R3-5: BigInt bitwise NOT with the correct ES semantics
				// (~x = -x-1). Tier 0's OpBitNot does not dispatch BigInt and
				// yields Number(-1) for every BigInt input (recorded Tier 0
				// bug); Quick intentionally computes the correct result, so
				// differential generators must not route BigInt through `~`.
				result, ok := quickBigIntNot(n, objects, objectCount)
				if !ok {
					return quickValue{}, GuardFailed, nil
				}
				push(result)
			default:
				return quickValue{}, GuardFailed, nil
			}
		case OpUnaryPlus:
			n := pop()
			if !n.isNumber() {
				return quickValue{}, GuardFailed, nil
			}
			push(n)
		case OpEq, OpNe:
			r, l := pop(), pop()
			// R3-3: == / != on primitives execute here per JS semantics via
			// the shared helper; object operands (and the recorded Tier 0
			// string-parse / BigInt-Boolean divergences) guard-fail so Tier 0
			// computes the identical result.
			equal, ok := quickLooseEqual(l, r, objects[:*objectCount])
			if !ok {
				return quickValue{}, GuardFailed, nil
			}
			if in.Op == OpNe {
				equal = !equal
			}
			push(booleanValue(equal))
		case OpLt, OpLe, OpGt, OpGe:
			r, l := pop(), pop()
			var b bool
			switch {
			case l.isNumber() && r.isNumber():
				switch in.Op {
				case OpLt:
					b = l.num < r.num
				case OpLe:
					b = l.num <= r.num
				case OpGt:
					b = l.num > r.num
				case OpGe:
					b = l.num >= r.num
				}
			case l.kind == quickString && r.kind == quickString:
				// R3-4: same-type String relational comparison, ordered exactly
				// like Tier 0's compareValues.
				cmp, ok := quickStringCompare(l, r, objects)
				if !ok {
					return quickValue{}, GuardFailed, nil
				}
				b = quickRelational(cmp, in.Op)
			case l.kind == quickBigInt && r.kind == quickBigInt:
				// R3-5: same-type BigInt relational comparison.
				cmp, ok := quickBigIntCompare(l, r, objects)
				if !ok {
					return quickValue{}, GuardFailed, nil
				}
				b = quickRelational(cmp, in.Op)
			default:
				// Mixed / coerced comparisons (BigInt vs Number, String vs
				// Number, ...) fall back to Tier 0.
				return quickValue{}, GuardFailed, nil
			}
			push(booleanValue(b))
		case OpStrictEq, OpStrictNe:
			r, l := pop(), pop()
			equal, ok := strictQuickEqual(l, r, objects[:*objectCount])
			if !ok {
				return quickValue{}, GuardFailed, nil
			}
			if in.Op == OpStrictNe {
				equal = !equal
			}
			push(booleanValue(equal))
		case OpPop:
			_ = pop()
		case OpDup:
			push(frame.stack[frame.sp-1])
		case OpSwap:
			n := frame.sp - 1
			frame.stack[n], frame.stack[n-1] = frame.stack[n-1], frame.stack[n]
		case OpJump:
			if int(in.Operand) < ip-1 {
				if err := safepoint.tick(); err != nil {
					return quickValue{}, Interrupted, err
				}
			}
			frame.ip = int(in.Operand)
		case OpJumpTrue, OpJumpFalse:
			truth, ok := pop().truthy()
			if !ok {
				return quickValue{}, GuardFailed, nil
			}
			if (in.Op == OpJumpTrue && truth) || (in.Op == OpJumpFalse && !truth) {
				if int(in.Operand) < ip-1 {
					if err := safepoint.tick(); err != nil {
						return quickValue{}, Interrupted, err
					}
				}
				frame.ip = int(in.Operand)
			}
		case OpJumpTrueKeep, OpJumpFalseKeep:
			truth, ok := frame.stack[frame.sp-1].truthy()
			if !ok {
				return quickValue{}, GuardFailed, nil
			}
			take := in.Op == OpJumpTrueKeep && truth || in.Op == OpJumpFalseKeep && !truth
			if take {
				if int(in.Operand) < ip-1 {
					if err := safepoint.tick(); err != nil {
						return quickValue{}, Interrupted, err
					}
				}
				frame.ip = int(in.Operand)
			} else {
				_ = pop()
			}
		case OpJumpNullishKeep:
			nullish, ok := frame.stack[frame.sp-1].nullish()
			if !ok {
				return quickValue{}, GuardFailed, nil
			}
			if !nullish {
				if int(in.Operand) < ip-1 {
					if err := safepoint.tick(); err != nil {
						return quickValue{}, Interrupted, err
					}
				}
				frame.ip = int(in.Operand)
			} else {
				_ = pop()
			}
		case OpSelfCall:
			// F3：safepoint 检查每 16 层递归执行一次。帧上限 4096
			//（quickMaxFrames），poll 最大延迟 4096×~100ns≈0.4ms，
			// OOM/取消响应仍在可接受范围内。
			if fp&15 == 0 {
				if err := safepoint.tick(); err != nil {
					return quickValue{}, Interrupted, err
				}
			}
			n := int(in.Operand)
			var recursiveArgs [8]quickValue
			for i := n - 1; i >= 0; i-- {
				recursiveArgs[i] = pop()
			}
			callee := pop()
			if callee.kind != quickSelf {
				return quickValue{}, GuardFailed, nil
			}
			target := cur
			if cur.callTarget != nil {
				target = cur.callTarget
			}
			// 推帧（F1-Fast）：参数直接拷入新帧 locals，无 Go 递归、无逃逸分配。
			fp++
			if fp >= len(frames) {
				if fp >= quickMaxFrames {
					return quickValue{}, GuardFailed, nil
				}
				newCap := len(frames) * 2
				if newCap > quickMaxFrames {
					newCap = quickMaxFrames
				}
				nf := make([]quickFrame, newCap)
				copy(nf, frames)
				frames = nf
			}
			frames[fp].prog = target
			frames[fp].ip = 0
			frames[fp].sp = 0
			{
				fl := frames[fp].locals[:target.NumLocals]
				if target.NumLocals > 0 {
					fl[0] = quickValue{}
				}
				for i := 1; i <= target.NumParams && i < len(fl); i++ {
					fl[i] = quickValue{kind: quickUndefined}
				}
				for i := 0; i < n && i+1 < len(fl); i++ {
					fl[i+1] = recursiveArgs[i]
				}
			}
		case OpReturn:
			// 弹帧（F1-Fast）：结果推入父帧栈，继续父帧执行。
			result := pop()
			fp--
			if fp < 0 {
				return result, Executed, nil
			}
			frames[fp].stack[frames[fp].sp] = result
			frames[fp].sp++
		case OpReturnUndef:
			fp--
			if fp < 0 {
				return quickValue{kind: quickUndefined}, Executed, nil
			}
			frames[fp].stack[frames[fp].sp] = quickValue{kind: quickUndefined}
			frames[fp].sp++
		default:
			return quickValue{}, Malformed, fmt.Errorf("jit: invalid IR opcode")
		}
	}
	return quickValue{}, Malformed, fmt.Errorf("jit: fell off program")
}
