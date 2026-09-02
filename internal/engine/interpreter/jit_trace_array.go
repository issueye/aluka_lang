// 数组 trace 特化：Array.push 与数组索引循环的匹配、数值提取与执行。

package interpreter

import (
	"math"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

type arrayPushTraceState struct {
	receiverLocal int
	indexLocal    int
	boundLocal    int
	boundConst    float64
	boundIsLocal  bool
	pushTarget    engine.Value
	startPC       int
	exitPC        int
}

// arrayIndexTraceState is the R4-5 packed Number read shape:
//
//	for (; i < bound; i++) sum += array[i]
//
// The bulk executor accumulates the exact per-iteration float sequence into
// the sum local and advances the index local atomically per committed chunk,
// so a guard failure or safepoint interruption always resumes in a state Tier
// 0 can continue from.
type arrayIndexTraceState struct {
	arrayLocal   int
	sumLocal     int
	indexLocal   int
	boundLocal   int
	boundConst   float64
	boundIsLocal bool
	startPC      int
	exitPC       int
}

// matchArrayPushTrace recognizes only the compiler's canonical form for:
//
//	for (; i < bound; i++) array.push(i)
//
// Keeping the matcher exact makes the bulk execution below equivalent to the
// bytecode it replaces and leaves all other calls on the normal deopt path.
func (v *VM) matchArrayPushTrace(frame *vmFrame, startPC, backedgePC int) *arrayPushTraceState {
	if frame == nil || frame.tmpl == nil || frame.base < 0 {
		return nil
	}
	code := frame.tmpl.Code
	const instructionCount = 14
	if startPC < 0 || backedgePC != startPC+(instructionCount-1)*bytecode.InstrSize ||
		backedgePC+bytecode.InstrSize > len(code) {
		return nil
	}
	op := func(index int) bytecode.Opcode { return bytecode.Opcode(code[startPC+index*bytecode.InstrSize]) }
	arg := func(index int) uint32 { return jitTraceOperand(code, startPC+index*bytecode.InstrSize) }
	if op(0) != bytecode.OpLoadLocal || op(2) != bytecode.OpLt || op(3) != bytecode.OpJmpFalsePop ||
		op(4) != bytecode.OpLoadLocal || op(5) != bytecode.OpLoadLocal || op(6) != bytecode.OpCallMethod ||
		op(7) != bytecode.OpPop || op(8) != bytecode.OpLoadLocal || op(9) != bytecode.OpDup ||
		op(10) != bytecode.OpInc || op(11) != bytecode.OpStoreLocal ||
		op(12) != bytecode.OpPop || op(13) != bytecode.OpJmp {
		return nil
	}
	indexLocal := int(arg(0))
	receiverLocal := int(arg(4))
	if int(arg(5)) != indexLocal || int(arg(8)) != indexLocal || int(arg(11)) != indexLocal ||
		indexLocal < 0 || indexLocal >= frame.tmpl.NumLocals || receiverLocal < 0 || receiverLocal >= frame.tmpl.NumLocals ||
		indexLocal == receiverLocal {
		return nil
	}
	callArg := arg(6)
	nameIndex := int(callArg & 0xFFFF)
	if callArg>>16 != 1 || nameIndex < 0 || nameIndex >= len(frame.tmpl.Constants) ||
		frame.tmpl.Constants[nameIndex].Type() != engine.TypeString || frame.tmpl.Constants[nameIndex].String() != "push" {
		return nil
	}
	backedgeTarget := backedgePC + bytecode.InstrSize + bytecode.SignedOperand(arg(13))
	exitPC := startPC + 4*bytecode.InstrSize + bytecode.SignedOperand(arg(3))
	if backedgeTarget != startPC || exitPC <= backedgePC || exitPC > len(code) {
		return nil
	}

	trace := &arrayPushTraceState{
		receiverLocal: receiverLocal,
		indexLocal:    indexLocal,
		boundLocal:    -1,
		startPC:       startPC,
		exitPC:        exitPC,
	}
	switch op(1) {
	case bytecode.OpLoadLocal:
		trace.boundLocal = int(arg(1))
		trace.boundIsLocal = true
		if trace.boundLocal < 0 || trace.boundLocal >= frame.tmpl.NumLocals ||
			trace.boundLocal == indexLocal || trace.boundLocal == receiverLocal {
			return nil
		}
	case bytecode.OpPushInt:
		trace.boundConst = float64(arg(1))
	case bytecode.OpPushNegInt:
		trace.boundConst = -float64(arg(1))
	case bytecode.OpPushConst:
		constantIndex := int(arg(1))
		if constantIndex < 0 || constantIndex >= len(frame.tmpl.Constants) ||
			frame.tmpl.Constants[constantIndex].Type() != engine.TypeNumber {
			return nil
		}
		trace.boundConst, _ = frame.tmpl.Constants[constantIndex].Float()
	default:
		return nil
	}

	localsEnd := frame.base + frame.tmpl.NumLocals
	if localsEnd > len(v.stack) {
		return nil
	}
	locals := v.stack[frame.base:localsEnd]
	receiver, ok := locals[receiverLocal].(*engine.ArrayValue)
	if !ok {
		return nil
	}
	pushTarget, err := v.interp.arrayProto.Get("push")
	if err != nil || pushTarget == nil {
		return nil
	}
	currentMethod, err := receiver.Get("push")
	if err != nil || currentMethod != pushTarget {
		return nil
	}
	trace.pushTarget = pushTarget
	if _, _, ok := trace.arrayPushNumbers(locals); !ok {
		return nil
	}
	return trace
}

func (t *arrayPushTraceState) arrayPushNumbers(locals []engine.Value) (float64, float64, bool) {
	if t == nil || t.indexLocal < 0 || t.indexLocal >= len(locals) {
		return 0, 0, false
	}
	index, ok := locals[t.indexLocal].Float()
	if !ok {
		return 0, 0, false
	}
	bound := t.boundConst
	if t.boundIsLocal {
		if t.boundLocal < 0 || t.boundLocal >= len(locals) {
			return 0, 0, false
		}
		bound, ok = locals[t.boundLocal].Float()
		if !ok {
			return 0, 0, false
		}
	}
	const maxSafeInteger = float64(1<<53 - 1)
	if math.IsNaN(index) || math.IsInf(index, 0) || math.Trunc(index) != index || index < 0 || index > maxSafeInteger ||
		math.IsNaN(bound) || math.IsInf(bound, 0) || math.Trunc(bound) != bound || bound < 0 || bound > maxSafeInteger {
		return 0, 0, false
	}
	return index, bound, true
}

func (v *VM) executeArrayPushTrace(trace *arrayPushTraceState, locals []engine.Value) (int, jit.ExitReason, error) {
	if trace == nil || trace.receiverLocal < 0 || trace.receiverLocal >= len(locals) {
		return 0, jit.GuardFailed, nil
	}
	receiver, ok := locals[trace.receiverLocal].(*engine.ArrayValue)
	if !ok {
		return 0, jit.GuardFailed, nil
	}
	method, err := receiver.Get("push")
	if err != nil || method != trace.pushTarget {
		return 0, jit.GuardFailed, nil
	}
	index, bound, ok := trace.arrayPushNumbers(locals)
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
	if !receiver.CanAppend(count) {
		return 0, jit.GuardFailed, nil
	}
	receiver.AppendNumberRange(index, count)
	locals[trace.indexLocal] = engine.Number(index + float64(count))
	if count >= budget {
		if err := v.pollJITSafepoint(); err != nil {
			return trace.startPC, jit.Interrupted, err
		}
		return trace.startPC, jit.Yielded, nil
	}
	return trace.exitPC, jit.Executed, nil
}

// matchArrayIndexTrace recognizes the compiler's canonical R4-5 packed Number
// read form:
//
//	for (; i < bound; i++) sum += array[i]
//
//	0 LoadLocal i     4 LoadLocal sum   8 Add     12 LoadLocal i
//	1 <bound>         5 LoadLocal array 9 Dup     13 Dup
//	2 Lt              6 LoadLocal i    10 Store   14 Inc
//	3 JmpFalsePop     7 GetElem        11 Pop     15 StoreLocal i
//	                                             16 Pop
//	                                             17 Jmp back
//
// The matcher is exact so the bulk execution below is equivalent to the
// bytecode it replaces; every other read shape (prototype index, hole or
// mixed-type elements, Proxy receiver, unsafe numbers) stays on the Tier 0
// path.
func (v *VM) matchArrayIndexTrace(frame *vmFrame, startPC, backedgePC int) *arrayIndexTraceState {
	if frame == nil || frame.tmpl == nil || frame.base < 0 {
		return nil
	}
	code := frame.tmpl.Code
	const instructionCount = 18
	if startPC < 0 || backedgePC != startPC+(instructionCount-1)*bytecode.InstrSize ||
		backedgePC+bytecode.InstrSize > len(code) {
		return nil
	}
	op := func(index int) bytecode.Opcode { return bytecode.Opcode(code[startPC+index*bytecode.InstrSize]) }
	arg := func(index int) uint32 { return jitTraceOperand(code, startPC+index*bytecode.InstrSize) }
	if op(0) != bytecode.OpLoadLocal || op(2) != bytecode.OpLt || op(3) != bytecode.OpJmpFalsePop ||
		op(4) != bytecode.OpLoadLocal || op(5) != bytecode.OpLoadLocal || op(6) != bytecode.OpLoadLocal ||
		op(7) != bytecode.OpGetElem || op(8) != bytecode.OpAdd || op(9) != bytecode.OpDup ||
		op(10) != bytecode.OpStoreLocal || op(11) != bytecode.OpPop || op(12) != bytecode.OpLoadLocal ||
		op(13) != bytecode.OpDup || op(14) != bytecode.OpInc || op(15) != bytecode.OpStoreLocal ||
		op(16) != bytecode.OpPop || op(17) != bytecode.OpJmp {
		return nil
	}
	indexLocal := int(arg(0))
	sumLocal := int(arg(4))
	arrayLocal := int(arg(5))
	if int(arg(6)) != indexLocal || int(arg(12)) != indexLocal || int(arg(15)) != indexLocal ||
		int(arg(10)) != sumLocal {
		return nil
	}
	localCount := frame.tmpl.NumLocals
	for _, slot := range []int{indexLocal, sumLocal, arrayLocal} {
		if slot < 0 || slot >= localCount {
			return nil
		}
	}
	if indexLocal == sumLocal || indexLocal == arrayLocal || sumLocal == arrayLocal {
		return nil
	}
	boundLocal, boundConst, boundIsLocal, ok := traceBoundOperand(frame.tmpl, op, arg, 1, indexLocal)
	if !ok {
		return nil
	}
	// The bound must not alias the sum local: the body stores sum every
	// iteration, so a bound that is also the sum would change mid-loop and
	// make the chunked range diverge from Tier 0.
	if boundIsLocal && boundLocal == sumLocal {
		return nil
	}
	backedgeTarget := backedgePC + bytecode.InstrSize + bytecode.SignedOperand(arg(17))
	exitPC := startPC + 4*bytecode.InstrSize + bytecode.SignedOperand(arg(3))
	if backedgeTarget != startPC || exitPC <= backedgePC || exitPC > len(code) {
		return nil
	}
	localsEnd := frame.base + localCount
	if localsEnd > len(v.stack) {
		return nil
	}
	locals := v.stack[frame.base:localsEnd]
	if _, ok := locals[arrayLocal].(*engine.ArrayValue); !ok {
		return nil
	}
	trace := &arrayIndexTraceState{
		arrayLocal: arrayLocal, sumLocal: sumLocal, indexLocal: indexLocal,
		boundLocal: boundLocal, boundConst: boundConst, boundIsLocal: boundIsLocal,
		startPC: startPC, exitPC: exitPC,
	}
	if _, _, _, ok := trace.arrayIndexNumbers(locals); !ok {
		return nil
	}
	return trace
}

func (t *arrayIndexTraceState) arrayIndexNumbers(locals []engine.Value) (float64, float64, float64, bool) {
	if t == nil || t.indexLocal < 0 || t.indexLocal >= len(locals) ||
		t.sumLocal < 0 || t.sumLocal >= len(locals) {
		return 0, 0, 0, false
	}
	index, ok := locals[t.indexLocal].Float()
	if !ok {
		return 0, 0, 0, false
	}
	bound := t.boundConst
	if t.boundIsLocal {
		if t.boundLocal < 0 || t.boundLocal >= len(locals) {
			return 0, 0, 0, false
		}
		bound, ok = locals[t.boundLocal].Float()
		if !ok {
			return 0, 0, 0, false
		}
	}
	sum, ok := locals[t.sumLocal].Float()
	if !ok {
		return 0, 0, 0, false
	}
	const maxSafeInteger = float64(1<<53 - 1)
	if math.IsNaN(index) || math.IsInf(index, 0) || math.Trunc(index) != index || index < 0 || index > maxSafeInteger ||
		math.IsNaN(bound) || math.IsInf(bound, 0) || math.Trunc(bound) != bound || bound < 0 || bound > maxSafeInteger ||
		math.IsNaN(sum) || math.IsInf(sum, 0) {
		return 0, 0, 0, false
	}
	return index, bound, sum, true
}

// executeArrayIndexTrace bulk-executes the packed Number read chunk. The
// length guard clamps the chunk to the current element storage (an index at
// or above len resolves through the prototype chain in Tier 0 and must fall
// back); a non-Number element in the range fails the whole chunk before any
// local is touched. The sum and index locals are updated atomically per
// committed chunk.
func (v *VM) executeArrayIndexTrace(trace *arrayIndexTraceState, locals []engine.Value) (int, jit.ExitReason, error) {
	if trace == nil || trace.arrayLocal < 0 || trace.arrayLocal >= len(locals) {
		return 0, jit.GuardFailed, nil
	}
	receiver, ok := locals[trace.arrayLocal].(*engine.ArrayValue)
	if !ok {
		return 0, jit.GuardFailed, nil
	}
	index, bound, sum, ok := trace.arrayIndexNumbers(locals)
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
	elems := receiver.Elems()
	if index >= float64(len(elems)) {
		return 0, jit.GuardFailed, nil
	}
	if maxCount := len(elems) - int(index); count > maxCount {
		count = maxCount
	}
	start := int(index)
	for i := 0; i < count; i++ {
		element := elems[start+i]
		if element == nil || element.Type() != engine.TypeNumber {
			return 0, jit.GuardFailed, nil
		}
		number, _ := element.Float()
		sum += number
	}
	locals[trace.sumLocal] = engine.Number(sum)
	locals[trace.indexLocal] = engine.Number(index + float64(count))
	// A chunk clamped by the element storage (index+count < bound) means the
	// loop continues reading through the prototype chain in Tier 0; the trace
	// must hand the loop back at its head instead of exiting it early.
	if count >= budget {
		if err := v.pollJITSafepoint(); err != nil {
			return trace.startPC, jit.Interrupted, err
		}
		return trace.startPC, jit.Yielded, nil
	}
	if index+float64(count) < bound {
		return trace.startPC, jit.Yielded, nil
	}
	return trace.exitPC, jit.Executed, nil
}
