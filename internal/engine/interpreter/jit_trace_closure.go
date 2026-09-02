// 闭包 upvalue trace 特化：数值 upvalue 闭包的表达式解析、别名检测、匹配与执行。

package interpreter

import (
	"math"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

type closureIncrementTraceState struct {
	calleeLocal int
	indexLocal  int
	boundLocal  int
	sumLocal    int
	target      *vmClosure
	// plan is the parsed numeric-upvalue body of the callee closure. It is
	// the R4-2 generalization of the single `() => ++n` shape: any sequence
	// of numeric upvalue read/write statements followed by one numeric
	// return (e.g. `() => { a++; b += a; return b; }`), including read-only
	// bodies (`() => a + b`) and in-frame (non-escaping) closures.
	plan *closurePlan
	// upvalues is the identity snapshot of the callee's upvalue list taken
	// when the trace state was built. Every execution re-checks
	// target.upvalues[i] == upvalues[i], so a second closure instance of the
	// same template with different captured cells falls back to Tier 0.
	upvalues []*upvalue
	startPC  int
	exitPC   int
}

type closureExprKind uint8

const (
	closureExprUpvalue closureExprKind = iota
	closureExprConst
	closureExprBin
)

type closureExpr struct {
	kind  closureExprKind
	slot  int             // closureExprUpvalue: captured upvalue index
	value float64         // closureExprConst: constant
	op    bytecode.Opcode // closureExprBin: ADD SUB MUL DIV MOD POW
	left  *closureExpr
	right *closureExpr
}

type closureWrite struct {
	slot int
	expr closureExpr
}

type closurePlan struct {
	writes   []closureWrite
	result   closureExpr
	readOnly bool // no writes: safe without any write-back
	// resultFirst marks single-expression postfix bodies (`() => n++`):
	// the returned value is captured before the write, while every other
	// shape returns the expression evaluated after all writes.
	resultFirst bool
}

// matchNumericUpvalueClosure parses the callee template's body into a
// closurePlan, or returns false when the body is not one of the supported
// numeric upvalue shapes. It validates the template shape (no params, no
// async/generator/varargs, no locals, one capture per upvalue) and the
// concrete bytecode; runtime value checks (numeric upvalues, identity) stay
// in the executor.
func matchNumericUpvalueClosure(target *vmClosure) (*closurePlan, bool) {
	if target == nil || target.tmpl == nil || target.tmpl.IsAsync || target.tmpl.IsGenerator ||
		target.tmpl.IsVarArgs || target.tmpl.NumParams != 0 || target.tmpl.NumLocals != 1 {
		return nil, false
	}
	numUpvalues := len(target.tmpl.Upvalues)
	if numUpvalues == 0 || numUpvalues != len(target.upvalues) {
		return nil, false
	}
	code := target.tmpl.Code
	end := len(code)
	if end >= bytecode.InstrSize && bytecode.Opcode(code[end-bytecode.InstrSize]) == bytecode.OpReturnUndef {
		end -= bytecode.InstrSize
	}
	if end == 0 {
		return nil, false
	}
	plan := &closurePlan{}
	pc := 0
	for pc < end {
		op := bytecode.Opcode(code[pc])
		arg := uint32(code[pc+1])<<16 | uint32(code[pc+2])<<8 | uint32(code[pc+3])
		if op == bytecode.OpReturn {
			return nil, false // RETURN must close an expression statement
		}
		// `a++` / `a--` statements compile to
		// LOAD_UPVALUE(i) DUP INC|DEC STORE_UPVALUE(i) POP.
		if pc+5*bytecode.InstrSize <= end && op == bytecode.OpLoadUpvalue {
			op1 := bytecode.Opcode(code[pc+1*bytecode.InstrSize])
			op2 := bytecode.Opcode(code[pc+2*bytecode.InstrSize])
			op3 := bytecode.Opcode(code[pc+3*bytecode.InstrSize])
			op4 := bytecode.Opcode(code[pc+4*bytecode.InstrSize])
			arg1 := uint32(code[pc+3*bytecode.InstrSize+1])<<16 | uint32(code[pc+3*bytecode.InstrSize+2])<<8 | uint32(code[pc+3*bytecode.InstrSize+3])
			if op1 == bytecode.OpDup && (op2 == bytecode.OpInc || op2 == bytecode.OpDec) &&
				op3 == bytecode.OpStoreUpvalue && int(arg1) == int(arg) && op4 == bytecode.OpPop {
				delta := float64(1)
				if op2 == bytecode.OpDec {
					delta = -1
				}
				plan.writes = append(plan.writes, closureWrite{
					slot: int(arg),
					expr: closureExpr{kind: closureExprBin, op: bytecode.OpAdd,
						left:  &closureExpr{kind: closureExprUpvalue, slot: int(arg)},
						right: &closureExpr{kind: closureExprConst, value: delta}},
				})
				pc += 5 * bytecode.InstrSize
				continue
			}
			// Single-expression bodies `() => ++n` / `() => n++` compile to
			// LOAD_UPVALUE(i) INC|DEC DUP STORE_UPVALUE(i) RETURN (prefix:
			// returns the new value) and LOAD_UPVALUE(i) DUP INC|DEC
			// STORE_UPVALUE(i) RETURN (postfix: returns the old value).
			prefix := (op1 == bytecode.OpInc || op1 == bytecode.OpDec) &&
				op2 == bytecode.OpDup && op3 == bytecode.OpStoreUpvalue &&
				int(arg1) == int(arg) && op4 == bytecode.OpReturn
			postfix := op1 == bytecode.OpDup && (op2 == bytecode.OpInc || op2 == bytecode.OpDec) &&
				op3 == bytecode.OpStoreUpvalue && int(arg1) == int(arg) && op4 == bytecode.OpReturn
			if (prefix || postfix) && pc+5*bytecode.InstrSize == end {
				delta := float64(1)
				if (prefix && op1 == bytecode.OpDec) || (postfix && op2 == bytecode.OpDec) {
					delta = -1
				}
				plan.writes = append(plan.writes, closureWrite{
					slot: int(arg),
					expr: closureExpr{kind: closureExprBin, op: bytecode.OpAdd,
						left:  &closureExpr{kind: closureExprUpvalue, slot: int(arg)},
						right: &closureExpr{kind: closureExprConst, value: delta}},
				})
				// Prefix returns the new value: the upvalue read evaluates
				// after the write. Postfix returns the old value: capture it
				// before the write (resultFirst).
				plan.result = closureExpr{kind: closureExprUpvalue, slot: int(arg)}
				plan.resultFirst = postfix
				pc = end
				break
			}
		}
		// General statement: <expr> DUP STORE_UPVALUE(i) POP (write) or
		// <expr> RETURN (final return; a body of a single expression is the
		// read-only capture shape `() => a + b`).
		expr, used, ok := parseClosureExpr(code, pc, end, target.tmpl, numUpvalues)
		if !ok {
			return nil, false
		}
		next := pc + used
		if next < end && bytecode.Opcode(code[next]) == bytecode.OpReturn && next+bytecode.InstrSize == end {
			plan.result = *expr
			pc = end
			break
		}
		if next+3*bytecode.InstrSize > end {
			return nil, false
		}
		op1 := bytecode.Opcode(code[next])
		arg1 := uint32(code[next+bytecode.InstrSize+1])<<16 | uint32(code[next+bytecode.InstrSize+2])<<8 | uint32(code[next+bytecode.InstrSize+3])
		op2 := bytecode.Opcode(code[next+bytecode.InstrSize])
		op3 := bytecode.Opcode(code[next+2*bytecode.InstrSize])
		if op1 != bytecode.OpDup || op2 != bytecode.OpStoreUpvalue || int(arg1) >= numUpvalues || op3 != bytecode.OpPop {
			return nil, false
		}
		plan.writes = append(plan.writes, closureWrite{slot: int(arg1), expr: *expr})
		pc = next + 3*bytecode.InstrSize
	}
	if pc != end {
		return nil, false
	}
	plan.readOnly = len(plan.writes) == 0
	return plan, true
}

// parseClosureExpr parses the expression starting at pc (within [0, limit))
// into a closureExpr tree. The bytecode compiler emits postfix binary
// expressions (atoms pushed left-to-right, each BINOP combining the top two),
// so a stack machine over atoms reproduces the exact evaluation order. An
// atom is a captured upvalue or a Number constant (OpPushConst resolves
// through the template constant pool; String constants reject the shape).
// Returns the expression, the instruction count consumed and ok.
func parseClosureExpr(code []byte, pc, limit int, tmpl *bytecode.FuncTemplate, numUpvalues int) (*closureExpr, int, bool) {
	if pc < 0 || limit <= pc || limit > len(code) || limit%bytecode.InstrSize != 0 || pc%bytecode.InstrSize != 0 {
		return nil, 0, false
	}
	stack := make([]closureExpr, 0, 4)
	used := pc
	for used < limit {
		op := bytecode.Opcode(code[used])
		arg := uint32(code[used+1])<<16 | uint32(code[used+2])<<8 | uint32(code[used+3])
		switch op {
		case bytecode.OpLoadUpvalue:
			if int(arg) >= numUpvalues {
				return nil, 0, false
			}
			stack = append(stack, closureExpr{kind: closureExprUpvalue, slot: int(arg)})
			used += bytecode.InstrSize
		case bytecode.OpPushInt:
			stack = append(stack, closureExpr{kind: closureExprConst, value: float64(arg)})
			used += bytecode.InstrSize
		case bytecode.OpPushNegInt:
			stack = append(stack, closureExpr{kind: closureExprConst, value: -float64(arg)})
			used += bytecode.InstrSize
		case bytecode.OpPushConst:
			if int(arg) >= len(tmpl.Constants) || tmpl.Constants[arg].Type() != engine.TypeNumber {
				return nil, 0, false
			}
			n, _ := tmpl.Constants[arg].Float()
			stack = append(stack, closureExpr{kind: closureExprConst, value: n})
			used += bytecode.InstrSize
		case bytecode.OpAdd, bytecode.OpSub, bytecode.OpMul, bytecode.OpDiv, bytecode.OpMod, bytecode.OpPow:
			if len(stack) < 2 {
				return nil, 0, false
			}
			right := stack[len(stack)-1]
			left := stack[len(stack)-2]
			stack = stack[:len(stack)-2]
			stack = append(stack, closureExpr{kind: closureExprBin, op: op, left: &left, right: &right})
			used += bytecode.InstrSize
		default:
			goto done
		}
	}
done:
	if len(stack) != 1 {
		return nil, 0, false
	}
	result := stack[0]
	return &result, used - pc, true
}

// closureTraceUpvalueAliased reports whether any captured upvalue of the
// callee aliases a local that the traced loop itself reads or writes
// (calleeLocal, indexLocal, boundLocal, sumLocal). Such an open upvalue
// would be observed mid-slice by the batch executor, which caches upvalue
// values at entry and writes them back once at the commit point; Tier 0
// instead reads the evolving local every iteration. Aliasing any other frame
// local is safe: the trace slice never touches it, and the single write-back
// reproduces the final state exactly.
func closureTraceUpvalueAliased(trace *closureIncrementTraceState, locals []engine.Value) bool {
	if trace == nil {
		return false
	}
	for _, uv := range trace.upvalues {
		if uv == nil || uv.slot == nil {
			continue // closed upvalue: no alias with the current frame
		}
		for _, slot := range []int{trace.calleeLocal, trace.indexLocal, trace.boundLocal, trace.sumLocal} {
			if slot >= 0 && slot < len(locals) && uv.slot == &locals[slot] {
				return true
			}
		}
	}
	return false
}

// matchClosureIncrementTrace recognizes the benchmark-critical form:
//
//	for (; i < bound; i++) sum += incrementClosure()
//
// where incrementClosure is exactly () => ++numericUpvalue.
func (v *VM) matchClosureIncrementTrace(frame *vmFrame, startPC, backedgePC int) *closureIncrementTraceState {
	if frame == nil || frame.tmpl == nil || frame.base < 0 {
		return nil
	}
	code := frame.tmpl.Code
	const instructionCount = 17
	if startPC < 0 || backedgePC != startPC+(instructionCount-1)*bytecode.InstrSize ||
		backedgePC+bytecode.InstrSize > len(code) {
		return nil
	}
	op := func(index int) bytecode.Opcode { return bytecode.Opcode(code[startPC+index*bytecode.InstrSize]) }
	arg := func(index int) uint32 { return jitTraceOperand(code, startPC+index*bytecode.InstrSize) }
	if op(0) != bytecode.OpLoadLocal || op(1) != bytecode.OpLoadLocal || op(2) != bytecode.OpLt ||
		op(3) != bytecode.OpJmpFalsePop || op(4) != bytecode.OpLoadLocal || op(5) != bytecode.OpLoadLocal ||
		op(6) != bytecode.OpCall || arg(6) != 0 || op(7) != bytecode.OpAdd || op(8) != bytecode.OpDup ||
		op(9) != bytecode.OpStoreLocal || op(10) != bytecode.OpPop || op(11) != bytecode.OpLoadLocal ||
		op(12) != bytecode.OpDup || op(13) != bytecode.OpInc ||
		op(14) != bytecode.OpStoreLocal || op(15) != bytecode.OpPop ||
		op(16) != bytecode.OpJmp {
		return nil
	}
	indexLocal := int(arg(0))
	boundLocal := int(arg(1))
	sumLocal := int(arg(4))
	calleeLocal := int(arg(5))
	if int(arg(9)) != sumLocal || int(arg(11)) != indexLocal || int(arg(14)) != indexLocal {
		return nil
	}
	localCount := frame.tmpl.NumLocals
	for _, slot := range []int{indexLocal, boundLocal, sumLocal, calleeLocal} {
		if slot < 0 || slot >= localCount {
			return nil
		}
	}
	if indexLocal == boundLocal || indexLocal == sumLocal || indexLocal == calleeLocal ||
		boundLocal == sumLocal || boundLocal == calleeLocal || sumLocal == calleeLocal {
		return nil
	}
	backedgeTarget := backedgePC + bytecode.InstrSize + bytecode.SignedOperand(arg(16))
	exitPC := startPC + 4*bytecode.InstrSize + bytecode.SignedOperand(arg(3))
	if backedgeTarget != startPC || exitPC <= backedgePC || exitPC > len(code) {
		return nil
	}
	localsEnd := frame.base + localCount
	if localsEnd > len(v.stack) {
		return nil
	}
	locals := v.stack[frame.base:localsEnd]
	target, ok := locals[calleeLocal].(*vmClosure)
	if !ok || target.vm != v {
		return nil
	}
	plan, ok := matchNumericUpvalueClosure(target)
	if !ok {
		return nil
	}
	trace := &closureIncrementTraceState{
		calleeLocal: calleeLocal, indexLocal: indexLocal, boundLocal: boundLocal,
		sumLocal: sumLocal, target: target, plan: plan,
		upvalues: target.upvalues, startPC: startPC, exitPC: exitPC,
	}
	if closureTraceUpvalueAliased(trace, locals) {
		return nil
	}
	if _, _, _, _, ok := trace.closureLoopNumbers(locals); !ok {
		return nil
	}
	return trace
}

func (t *closureIncrementTraceState) closureLoopNumbers(locals []engine.Value) (float64, float64, float64, []float64, bool) {
	if t == nil || t.target == nil || t.plan == nil || len(t.upvalues) == 0 ||
		t.indexLocal < 0 || t.indexLocal >= len(locals) ||
		t.boundLocal < 0 || t.boundLocal >= len(locals) || t.sumLocal < 0 || t.sumLocal >= len(locals) {
		return 0, 0, 0, nil, false
	}
	index, indexOK := locals[t.indexLocal].Float()
	bound, boundOK := locals[t.boundLocal].Float()
	sum, sumOK := locals[t.sumLocal].Float()
	if !indexOK || !boundOK || !sumOK {
		return 0, 0, 0, nil, false
	}
	values := make([]float64, len(t.upvalues))
	for i := range t.upvalues {
		upvalueValue, upvalueOK := closureUpvalue(t.target, i)
		if !upvalueOK || upvalueValue == nil || upvalueValue.Type() != engine.TypeNumber {
			return 0, 0, 0, nil, false
		}
		current, _ := upvalueValue.Float()
		if math.IsNaN(current) || math.IsInf(current, 0) {
			return 0, 0, 0, nil, false
		}
		values[i] = current
	}
	const maxSafeInteger = float64(1<<53 - 1)
	if math.IsNaN(index) || math.IsInf(index, 0) || math.Trunc(index) != index || index < 0 || index > maxSafeInteger ||
		math.IsNaN(bound) || math.IsInf(bound, 0) || math.Trunc(bound) != bound || bound < 0 || bound > maxSafeInteger ||
		math.IsNaN(sum) || math.IsInf(sum, 0) {
		return 0, 0, 0, nil, false
	}
	return index, bound, sum, values, true
}

// evalClosureExpr evaluates a closureExpr against the upvalue value cache.
// The arithmetic mirrors Tier 0's Number semantics exactly (IEEE-754
// add/sub/mul/div, math.Pow, math.Mod with NaN for a zero divisor), so the
// batch evaluation is bit-identical to executing the closure body.
func evalClosureExpr(e *closureExpr, values []float64) float64 {
	if e == nil {
		return math.NaN()
	}
	switch e.kind {
	case closureExprConst:
		return e.value
	case closureExprUpvalue:
		if e.slot >= 0 && e.slot < len(values) {
			return values[e.slot]
		}
		return math.NaN()
	case closureExprBin:
		left := evalClosureExpr(e.left, values)
		right := evalClosureExpr(e.right, values)
		switch e.op {
		case bytecode.OpAdd:
			return left + right
		case bytecode.OpSub:
			return left - right
		case bytecode.OpMul:
			return left * right
		case bytecode.OpDiv:
			return left / right
		case bytecode.OpMod:
			// Tier 0 OpMod: math.Mod, NaN when the divisor is zero.
			return math.Mod(left, right)
		case bytecode.OpPow:
			return math.Pow(left, right)
		default:
			return math.NaN()
		}
	default:
		return math.NaN()
	}
}

func (v *VM) executeClosureIncrementTrace(trace *closureIncrementTraceState, locals []engine.Value) (int, jit.ExitReason, error) {
	if trace == nil || trace.calleeLocal < 0 || trace.calleeLocal >= len(locals) ||
		locals[trace.calleeLocal] != trace.target || trace.plan == nil {
		return 0, jit.GuardFailed, nil
	}
	// Callee identity + captured upvalue identity: the plan binds to the
	// concrete captured cells, so a different closure instance of the same
	// template (or an upvalue cell that was replaced) must fall back.
	if len(trace.target.upvalues) != len(trace.upvalues) {
		return 0, jit.GuardFailed, nil
	}
	for i := range trace.upvalues {
		if trace.target.upvalues[i] != trace.upvalues[i] {
			return 0, jit.GuardFailed, nil
		}
	}
	if closureTraceUpvalueAliased(trace, locals) {
		return 0, jit.GuardFailed, nil
	}
	index, bound, sum, values, ok := trace.closureLoopNumbers(locals)
	if !ok {
		return 0, jit.GuardFailed, nil
	}
	if index >= bound {
		return trace.exitPC, jit.Executed, nil
	}
	remaining := int(bound - index)
	budget := int(v.jitConfig.TraceBudget)
	if budget <= 0 {
		budget = 65536
	}
	count := remaining
	if count > budget {
		count = budget
	}
	for i := 0; i < count; i++ {
		if trace.plan.resultFirst {
			// `() => n++`: the return value is the pre-write capture.
			sum += evalClosureExpr(&trace.plan.result, values)
		}
		for w := range trace.plan.writes {
			values[trace.plan.writes[w].slot] = evalClosureExpr(&trace.plan.writes[w].expr, values)
		}
		if !trace.plan.resultFirst {
			sum += evalClosureExpr(&trace.plan.result, values)
		}
	}
	// Atomic commit: every written upvalue is stored once, then the loop
	// locals. A read-only plan writes nothing back.
	for w := range trace.plan.writes {
		if !storeClosureUpvalue(trace.target, trace.plan.writes[w].slot, engine.Number(values[trace.plan.writes[w].slot])) {
			return 0, jit.GuardFailed, nil
		}
	}
	locals[trace.sumLocal] = engine.Number(sum)
	locals[trace.indexLocal] = engine.Number(index + float64(count))
	if count >= budget {
		if err := v.pollJITSafepoint(); err != nil {
			return trace.startPC, jit.Interrupted, err
		}
		return trace.startPC, jit.Yielded, nil
	}
	return trace.exitPC, jit.Executed, nil
}
