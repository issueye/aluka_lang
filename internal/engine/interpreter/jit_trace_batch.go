// 数组批量写 trace 特化：连续索引写入的匹配、数值提取与执行。

package interpreter

import (
	"math"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// arrayBatchWriteTraceState is the R4-6 safe batch write shape:
//
//	for (; i < bound; i++) array[i] = i              // key == value == loop var
//	for (; i < bound; i++) { array[j] = i; j++; }    // separate key counter
//	for (; i < bound; i++) array[j++] = i            // post-incremented key
//
// The bulk executor writes array[key+t] = value+t for the committed chunk and
// synchronizes the length property once per chunk, matching the per-iteration
// Tier 0 Set semantics (extend with holes, fill, length slot sync).
type arrayBatchWriteTraceState struct {
	arrayLocal   int
	keyLocal     int
	valueLocal   int
	indexLocal   int
	boundLocal   int
	boundConst   float64
	boundIsLocal bool
	startPC      int
	exitPC       int
}

// matchArrayBatchWriteTrace recognizes the compiler's canonical R4-6 batch
// write forms, where the loop variable (value) and the write key both advance
// by exactly one per iteration:
//
//	W1: for (; i < bound; i++) array[i] = i            // key == value == loop var
//	W2: for (; i < bound; i++) { array[j] = i; j++; }  // separate key counter
//	W3: for (; i < bound; i++) array[j++] = i          // post-incremented key
//
//	head: 0 LoadLocal v  1 <bound>  2 Lt  3 JmpFalsePop exit
//	body: 4 LoadLocal v  5 Dup  6 LoadLocal array  7 LoadLocal key
//	      W1/W2: 8 SetElemTop 9 Pop
//	      W3:    8 Dup 9 Inc 10 StoreLocal key 11 SetElemTop 12 Pop
//	tail: increment blocks {LoadLocal c, Dup, Inc, StoreLocal c, Pop}* then Jmp back
//
// W1 requires one block for v; W2 requires blocks for key then v; W3 requires
// one block for v (the key is incremented inside the body). The bulk executor
// then writes array[key+t] = value+t for the committed chunk and syncs the
// length property once, which is the exact final state of the per-iteration
// Tier 0 Set sequence.
func (v *VM) matchArrayBatchWriteTrace(frame *vmFrame, startPC, backedgePC int) *arrayBatchWriteTraceState {
	if frame == nil || frame.tmpl == nil || frame.base < 0 {
		return nil
	}
	code := frame.tmpl.Code
	if startPC < 0 || backedgePC <= startPC+7*bytecode.InstrSize ||
		backedgePC+bytecode.InstrSize > len(code) {
		return nil
	}
	op := func(index int) bytecode.Opcode { return bytecode.Opcode(code[startPC+index*bytecode.InstrSize]) }
	arg := func(index int) uint32 { return jitTraceOperand(code, startPC+index*bytecode.InstrSize) }
	if op(0) != bytecode.OpLoadLocal || op(2) != bytecode.OpLt || op(3) != bytecode.OpJmpFalsePop ||
		op(4) != bytecode.OpLoadLocal || op(5) != bytecode.OpDup || op(6) != bytecode.OpLoadLocal ||
		op(7) != bytecode.OpLoadLocal {
		return nil
	}
	valueLocal := int(arg(0))
	arrayLocal := int(arg(6))
	keyLocal := int(arg(7))
	if int(arg(4)) != valueLocal {
		return nil
	}
	bodyLength := 6
	keyIncInBody := false
	switch op(8) {
	case bytecode.OpSetElemTop:
		if op(9) != bytecode.OpPop {
			return nil
		}
	case bytecode.OpDup:
		bodyLength = 9
		keyIncInBody = true
		if op(9) != bytecode.OpInc || op(10) != bytecode.OpStoreLocal || int(arg(10)) != keyLocal ||
			op(11) != bytecode.OpSetElemTop || op(12) != bytecode.OpPop {
			return nil
		}
	default:
		return nil
	}
	localCount := frame.tmpl.NumLocals
	for _, slot := range []int{valueLocal, arrayLocal, keyLocal} {
		if slot < 0 || slot >= localCount {
			return nil
		}
	}
	if valueLocal == arrayLocal || keyLocal == arrayLocal {
		return nil
	}
	// Parse the tail increment blocks, then the backedge jump. pc is an
	// instruction index within the loop range (the last index is the backedge
	// Jmp), so every bound comparison here is in index units.
	var incBlocks []int
	tailStart := 4 + bodyLength
	lastIndex := (backedgePC - startPC) / bytecode.InstrSize
	pc := tailStart
	for {
		if op(pc) == bytecode.OpJmp {
			break
		}
		if pc+4 > lastIndex ||
			op(pc) != bytecode.OpLoadLocal || op(pc+1) != bytecode.OpDup || op(pc+2) != bytecode.OpInc ||
			op(pc+3) != bytecode.OpStoreLocal || op(pc+4) != bytecode.OpPop {
			return nil
		}
		incBlocks = append(incBlocks, int(arg(pc)))
		pc += 5
	}
	if pc != lastIndex {
		return nil
	}
	// Validate the increment schedule per form.
	if keyLocal == valueLocal {
		if keyIncInBody || len(incBlocks) != 1 || incBlocks[0] != valueLocal {
			return nil
		}
	} else {
		if keyIncInBody {
			if len(incBlocks) != 1 || incBlocks[0] != valueLocal {
				return nil
			}
		} else if len(incBlocks) != 2 || incBlocks[0] != keyLocal || incBlocks[1] != valueLocal {
			return nil
		}
	}
	boundLocal, boundConst, boundIsLocal, ok := traceBoundOperand(frame.tmpl, op, arg, 1, valueLocal)
	if !ok {
		return nil
	}
	// The bound must not alias the key local: the loop tail increments the
	// key every iteration, so a bound that is also the key would move with
	// the writes and make the chunked range diverge from Tier 0 (the
	// value/index aliasing is already excluded above).
	if boundIsLocal && (boundLocal == keyLocal || boundLocal == valueLocal) {
		return nil
	}
	backedgeTarget := backedgePC + bytecode.InstrSize + bytecode.SignedOperand(arg(pc))
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
	trace := &arrayBatchWriteTraceState{
		arrayLocal: arrayLocal, keyLocal: keyLocal, valueLocal: valueLocal,
		indexLocal: valueLocal, boundLocal: boundLocal, boundConst: boundConst,
		boundIsLocal: boundIsLocal, startPC: startPC, exitPC: exitPC,
	}
	if _, _, _, _, ok := trace.arrayBatchNumbers(locals); !ok {
		return nil
	}
	return trace
}

func (t *arrayBatchWriteTraceState) arrayBatchNumbers(locals []engine.Value) (float64, float64, float64, float64, bool) {
	if t == nil || t.indexLocal < 0 || t.indexLocal >= len(locals) ||
		t.keyLocal < 0 || t.keyLocal >= len(locals) {
		return 0, 0, 0, 0, false
	}
	index, ok := locals[t.indexLocal].Float()
	if !ok {
		return 0, 0, 0, 0, false
	}
	bound := t.boundConst
	if t.boundIsLocal {
		if t.boundLocal < 0 || t.boundLocal >= len(locals) {
			return 0, 0, 0, 0, false
		}
		bound, ok = locals[t.boundLocal].Float()
		if !ok {
			return 0, 0, 0, 0, false
		}
	}
	key, ok := locals[t.keyLocal].Float()
	if !ok {
		return 0, 0, 0, 0, false
	}
	value, ok := locals[t.valueLocal].Float()
	if !ok {
		return 0, 0, 0, 0, false
	}
	const maxSafeInteger = float64(1<<53 - 1)
	if math.IsNaN(index) || math.IsInf(index, 0) || math.Trunc(index) != index || index < 0 || index > maxSafeInteger ||
		math.IsNaN(bound) || math.IsInf(bound, 0) || math.Trunc(bound) != bound || bound < 0 || bound > maxSafeInteger ||
		math.IsNaN(key) || math.IsInf(key, 0) || math.Trunc(key) != key || key < 0 || key > maxSafeInteger ||
		math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, 0, 0, 0, false
	}
	return index, bound, key, value, true
}

// executeArrayBatchWriteTrace bulk-executes the batch write chunk: the
// committed range [key, key+count) is filled with value..value+count-1,
// growing the element storage with holes first (exactly the per-write Tier 0
// Set semantics) and syncing the length property once. The length guard keeps
// the final index inside the safe integer domain; the index/key/value locals
// advance atomically per committed chunk so a guard failure or safepoint
// interruption resumes in a state Tier 0 can continue from.
func (v *VM) executeArrayBatchWriteTrace(trace *arrayBatchWriteTraceState, locals []engine.Value) (int, jit.ExitReason, error) {
	if trace == nil || trace.arrayLocal < 0 || trace.arrayLocal >= len(locals) {
		return 0, jit.GuardFailed, nil
	}
	receiver, ok := locals[trace.arrayLocal].(*engine.ArrayValue)
	if !ok {
		return 0, jit.GuardFailed, nil
	}
	index, bound, key, value, ok := trace.arrayBatchNumbers(locals)
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
	// Length guard: the final write index key+count-1 must stay within the
	// safe integer domain so the length synchronization is exact.
	const maxSafeInteger = float64(1<<53 - 1)
	if key+float64(count) > maxSafeInteger+1 {
		return 0, jit.GuardFailed, nil
	}
	if !receiver.CanWriteRange(int(key), count) {
		return 0, jit.GuardFailed, nil
	}
	receiver.WriteNumberRange(int(key), value, count)
	locals[trace.indexLocal] = engine.Number(index + float64(count))
	locals[trace.keyLocal] = engine.Number(key + float64(count))
	locals[trace.valueLocal] = engine.Number(value + float64(count))
	if count >= budget {
		if err := v.pollJITSafepoint(); err != nil {
			return trace.startPC, jit.Interrupted, err
		}
		return trace.startPC, jit.Yielded, nil
	}
	return trace.exitPC, jit.Executed, nil
}
