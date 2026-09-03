package jit

import (
	"fmt"
	"math"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

type TraceProgram struct {
	program *Program
	startPC int
	written []bool
	exits   []DeoptExit
}

type TraceCallGuard struct {
	PC          int
	SourceLocal int
	Target      engine.Value
}

type TraceMethodGuard struct {
	PC          int
	SourceLocal int
	Target      engine.Value
	Method      string
	Property    string
}

// TraceUpvalueCell is a Number-valued captured cell the trace tier may read
// and write. The interpreter owns the concrete cell (its `*upvalue`); the jit
// package only ever calls these two methods, so it never depends on the
// interpreter's closure representation.
//
// LoadNumber reports ok=false when the cell currently holds a non-Number (the
// trace must fall back to Tier 0). StoreNumber reports false when the cell can
// no longer accept the write. Both are called at trace entry and at commit
// points only — never per loop iteration — so the interface dispatch is off
// the hot path.
type TraceUpvalueCell interface {
	LoadNumber() (float64, bool)
	StoreNumber(float64) bool
}

// TraceUpvalueGuard binds one upvalue index of the traced frame to its cell.
// The bridge supplies a guard per upvalue index the trace range touches, after
// checking that no cell aliases a local slot of the traced frame (an aliased
// open cell would be read per-iteration by Tier 0 but cached by the trace).
type TraceUpvalueGuard struct {
	Index int
	Cell  TraceUpvalueCell
}

type traceCallGuard struct {
	sourceLocal int
	target      engine.Value
}

type traceMethodGuard struct {
	sourceLocal int
	target      engine.Value
	method      string
	property    string
}

// traceUpvalue is the compiled form of a TraceUpvalueGuard: OpLoadUpvalueNum /
// OpStoreUpvalueNum operands index the program's traceUpvalues slice.
type traceUpvalue struct {
	index int
	cell  TraceUpvalueCell
	write bool
}

type DeoptExit struct {
	ID          int
	ResumePC    int
	LocalSlots  []uint16
	StackDepth  int
	StackValues []engine.Value
	// PendingException is the JS value the VM must throw when resuming this
	// exit (an exception exit). Nil means no pending exception: the exit is a
	// normal side exit / yield and execution continues at ResumePC.
	PendingException engine.Value
}

func SameDeoptExit(a, b DeoptExit) bool {
	return a.ID == b.ID && a.ResumePC == b.ResumePC && a.StackDepth == b.StackDepth &&
		sameTraceValues(a.StackValues, b.StackValues) &&
		samePendingException(a.PendingException, b.PendingException)
}

// samePendingException compares the pending-exception values of two exits.
// Nil (no exception) must match nil; otherwise values compare with the same
// semantics as trace stack values (numbers bitwise incl. NaN, strings by
// value, everything else by identity).
func samePendingException(a, b engine.Value) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return sameTraceValues([]engine.Value{a}, []engine.Value{b})
}

func CompileTrace(tmpl *bytecode.FuncTemplate, startPC, backedgePC int) (*TraceProgram, error) {
	return CompileTraceWithCallGuards(tmpl, startPC, backedgePC, nil)
}

func CompileTraceWithCallGuards(tmpl *bytecode.FuncTemplate, startPC, backedgePC int, callGuards []TraceCallGuard) (*TraceProgram, error) {
	return CompileTraceWithGuards(tmpl, startPC, backedgePC, callGuards, nil)
}

func CompileTraceWithGuards(tmpl *bytecode.FuncTemplate, startPC, backedgePC int, callGuards []TraceCallGuard, methodGuards []TraceMethodGuard) (*TraceProgram, error) {
	return CompileTraceWithUpvalues(tmpl, startPC, backedgePC, callGuards, methodGuards, nil)
}

// CompileTraceWithUpvalues additionally admits OpLoadUpvalue/OpStoreUpvalue in
// the trace range for every upvalue index covered by upvalueGuards. Indices the
// range touches without a guard keep rejecting the trace.
func CompileTraceWithUpvalues(tmpl *bytecode.FuncTemplate, startPC, backedgePC int,
	callGuards []TraceCallGuard, methodGuards []TraceMethodGuard, upvalueGuards []TraceUpvalueGuard) (*TraceProgram, error) {
	if err := rejectTraceCandidateWithUpvalues(tmpl, startPC, backedgePC, upvalueGuards); err != nil {
		return nil, err
	}
	p := &Program{NumParams: tmpl.NumParams, NumLocals: tmpl.NumLocals, SelfUpvalue: -1}
	trace := &TraceProgram{program: p, startPC: startPC, written: make([]bool, tmpl.NumLocals)}
	upvalueByIndex := make(map[int]int, len(upvalueGuards))
	for _, guard := range upvalueGuards {
		if guard.Index < 0 || guard.Index >= len(tmpl.Upvalues) || guard.Cell == nil {
			return nil, fmt.Errorf("jit: invalid trace upvalue guard")
		}
		if _, duplicate := upvalueByIndex[guard.Index]; duplicate {
			return nil, fmt.Errorf("jit: duplicate trace upvalue guard")
		}
		upvalueByIndex[guard.Index] = len(p.traceUpvalues)
		p.traceUpvalues = append(p.traceUpvalues, traceUpvalue{index: guard.Index, cell: guard.Cell})
	}
	guardByPC := make(map[int]TraceCallGuard, len(callGuards))
	for _, guard := range callGuards {
		if guard.PC < startPC || guard.PC > backedgePC || guard.PC%bytecode.InstrSize != 0 ||
			guard.SourceLocal < 0 || guard.SourceLocal >= tmpl.NumLocals || guard.Target == nil {
			return nil, fmt.Errorf("jit: invalid trace call guard")
		}
		if _, duplicate := guardByPC[guard.PC]; duplicate {
			return nil, fmt.Errorf("jit: duplicate trace call guard")
		}
		guardByPC[guard.PC] = guard
	}
	methodByPC := make(map[int]TraceMethodGuard, len(methodGuards))
	for _, guard := range methodGuards {
		if guard.PC < startPC || guard.PC > backedgePC || guard.PC%bytecode.InstrSize != 0 ||
			guard.SourceLocal < 0 || guard.SourceLocal >= tmpl.NumLocals || guard.Target == nil || guard.Method == "" || guard.Property == "" {
			return nil, fmt.Errorf("jit: invalid trace method guard")
		}
		if _, duplicate := methodByPC[guard.PC]; duplicate {
			return nil, fmt.Errorf("jit: duplicate trace method guard")
		}
		methodByPC[guard.PC] = guard
	}
	for pc := startPC; pc <= backedgePC; pc += bytecode.InstrSize {
		if bytecode.Opcode(tmpl.Code[pc]) != bytecode.OpStoreLocal {
			continue
		}
		slot := int(uint32(tmpl.Code[pc+1])<<16 | uint32(tmpl.Code[pc+2])<<8 | uint32(tmpl.Code[pc+3]))
		for _, guard := range callGuards {
			if guard.SourceLocal == slot {
				return nil, fmt.Errorf("jit: guarded callee local is written in trace")
			}
		}
		for _, guard := range methodGuards {
			if guard.SourceLocal == slot {
				return nil, fmt.Errorf("jit: guarded method receiver local is written in trace")
			}
		}
	}
	pcToIR := make(map[int]int, (backedgePC-startPC)/bytecode.InstrSize+1)
	type exitFixup struct {
		instruction int
		exitID      int
	}
	var exitFixups []exitFixup
	for pc := startPC; pc <= backedgePC; pc += bytecode.InstrSize {
		pcToIR[pc] = len(p.Code)
		op := bytecode.Opcode(tmpl.Code[pc])
		arg := uint32(tmpl.Code[pc+1])<<16 | uint32(tmpl.Code[pc+2])<<8 | uint32(tmpl.Code[pc+3])
		switch op {
		case bytecode.OpPushInt:
			p.Code = append(p.Code, Instr{Op: OpConst, Value: float64(arg)})
		case bytecode.OpPushNegInt:
			p.Code = append(p.Code, Instr{Op: OpConst, Value: -float64(arg)})
		case bytecode.OpPushConst:
			if int(arg) >= len(tmpl.Constants) {
				return nil, fmt.Errorf("jit: trace non-number constant")
			}
			switch tmpl.Constants[arg].Type() {
			case engine.TypeNumber:
				n, _ := tmpl.Constants[arg].Float()
				p.Code = append(p.Code, Instr{Op: OpConst, Value: n})
			case engine.TypeString:
				p.Code = append(p.Code, Instr{Op: OpConstString, Operand: uint32(len(p.stringConsts)), Name: tmpl.Constants[arg].String()})
				p.stringConsts = append(p.stringConsts, tmpl.Constants[arg])
			default:
				return nil, fmt.Errorf("jit: trace non-number constant")
			}
		case bytecode.OpLoadLocal:
			if int(arg) >= tmpl.NumLocals {
				return nil, fmt.Errorf("jit: trace local out of range")
			}
			p.Code = append(p.Code, Instr{Op: OpLoadLocal, Operand: arg})
		case bytecode.OpStoreLocal:
			if int(arg) >= tmpl.NumLocals {
				return nil, fmt.Errorf("jit: trace local out of range")
			}
			trace.written[arg] = true
			p.Code = append(p.Code, Instr{Op: OpStoreLocal, Operand: arg})
		case bytecode.OpLoadUpvalue:
			slot, guarded := upvalueByIndex[int(arg)]
			if !guarded {
				return nil, fmt.Errorf("jit: trace unguarded upvalue %d", arg)
			}
			p.Code = append(p.Code, Instr{Op: OpLoadUpvalueNum, Operand: uint32(slot)})
		case bytecode.OpStoreUpvalue:
			slot, guarded := upvalueByIndex[int(arg)]
			if !guarded {
				return nil, fmt.Errorf("jit: trace unguarded upvalue %d", arg)
			}
			p.traceUpvalues[slot].write = true
			p.Code = append(p.Code, Instr{Op: OpStoreUpvalueNum, Operand: uint32(slot)})
		case bytecode.OpGetPropLocal:
			slot, nameIdx := int(arg>>16), int(arg&0xFFFF)
			if slot >= tmpl.NumLocals || nameIdx >= len(tmpl.Constants) || tmpl.Constants[nameIdx].Type() != engine.TypeString {
				return nil, fmt.Errorf("jit: trace property operand")
			}
			p.Code = append(p.Code, Instr{Op: OpLoadLocal, Operand: uint32(slot)}, Instr{Op: OpGetProp, Name: tmpl.Constants[nameIdx].String()})
		case bytecode.OpGetProp:
			if int(arg) >= len(tmpl.Constants) || tmpl.Constants[arg].Type() != engine.TypeString {
				return nil, fmt.Errorf("jit: trace property name")
			}
			p.Code = append(p.Code, Instr{Op: OpGetProp, Name: tmpl.Constants[arg].String()})
		case bytecode.OpSetPropTop:
			if int(arg) >= len(tmpl.Constants) || tmpl.Constants[arg].Type() != engine.TypeString {
				return nil, fmt.Errorf("jit: trace property write name")
			}
			p.Code = append(p.Code, Instr{Op: OpSetProp, Name: tmpl.Constants[arg].String()})
		case bytecode.OpCall:
			guard, guarded := guardByPC[pc]
			if arg != 0 || !guarded || len(p.Code) == 0 ||
				p.Code[len(p.Code)-1].Op != OpLoadLocal || int(p.Code[len(p.Code)-1].Operand) != guard.SourceLocal ||
				pc+bytecode.InstrSize > backedgePC || bytecode.Opcode(tmpl.Code[pc+bytecode.InstrSize]) != bytecode.OpPop {
				return nil, fmt.Errorf("jit: trace unsupported opcode %s", op)
			}
			guardIndex := len(p.traceCallGuards)
			p.traceCallGuards = append(p.traceCallGuards, traceCallGuard{sourceLocal: guard.SourceLocal, target: guard.Target})
			p.Code = append(p.Code, Instr{Op: OpGuardNoopCall, Operand: uint32(guardIndex)})
			delete(guardByPC, pc)
		case bytecode.OpCallMethod:
			guard, guarded := methodByPC[pc]
			nameIndex := int(arg & 0xFFFF)
			if arg>>16 != 0 || !guarded || nameIndex >= len(tmpl.Constants) || tmpl.Constants[nameIndex].Type() != engine.TypeString ||
				len(p.Code) == 0 || p.Code[len(p.Code)-1].Op != OpLoadLocal ||
				int(p.Code[len(p.Code)-1].Operand) != guard.SourceLocal {
				return nil, fmt.Errorf("jit: trace unsupported opcode %s", op)
			}
			methodIndex := len(p.traceMethodGuards)
			p.traceMethodGuards = append(p.traceMethodGuards, traceMethodGuard{
				sourceLocal: guard.SourceLocal, target: guard.Target, method: guard.Method, property: guard.Property,
			})
			p.Code = append(p.Code, Instr{Op: OpGuardMethodGet, Operand: uint32(methodIndex)})
			delete(methodByPC, pc)
		case bytecode.OpAdd, bytecode.OpSub, bytecode.OpMul, bytecode.OpDiv, bytecode.OpMod, bytecode.OpPow,
			bytecode.OpBitAnd, bytecode.OpBitOr, bytecode.OpBitXor, bytecode.OpShl, bytecode.OpShr, bytecode.OpUShr,
			bytecode.OpEq, bytecode.OpStrictEq, bytecode.OpNe, bytecode.OpStrictNe,
			bytecode.OpLt, bytecode.OpLe, bytecode.OpGt, bytecode.OpGe:
			mapped := map[bytecode.Opcode]Op{
				bytecode.OpAdd: OpAdd, bytecode.OpSub: OpSub, bytecode.OpMul: OpMul, bytecode.OpDiv: OpDiv,
				bytecode.OpMod: OpMod, bytecode.OpPow: OpPow, bytecode.OpEq: OpEq, bytecode.OpStrictEq: OpStrictEq,
				bytecode.OpBitAnd: OpBitAnd, bytecode.OpBitOr: OpBitOr, bytecode.OpBitXor: OpBitXor,
				bytecode.OpShl: OpShl, bytecode.OpShr: OpShr, bytecode.OpUShr: OpUShr,
				bytecode.OpNe: OpNe, bytecode.OpStrictNe: OpStrictNe,
				bytecode.OpLt: OpLt, bytecode.OpLe: OpLe, bytecode.OpGt: OpGt, bytecode.OpGe: OpGe,
			}
			p.Code = append(p.Code, Instr{Op: mapped[op]})
		case bytecode.OpNeg:
			p.Code = append(p.Code, Instr{Op: OpNeg})
		case bytecode.OpNot:
			p.Code = append(p.Code, Instr{Op: OpNot})
		case bytecode.OpBitNot:
			p.Code = append(p.Code, Instr{Op: OpBitNot})
		case bytecode.OpUnaryPlus:
			p.Code = append(p.Code, Instr{Op: OpUnaryPlus})
		case bytecode.OpPop:
			p.Code = append(p.Code, Instr{Op: OpPop})
		case bytecode.OpInc, bytecode.OpDec:
			// Same expansion as the function lowering: ++ / -- become the
			// Number sequence (x++ -> x + 1, x-- -> x - 1); BigInt
			// operands fail the arithmetic guard and fall back to Tier 0.
			p.Code = append(p.Code, Instr{Op: OpConst, Value: 1})
			if op == bytecode.OpInc {
				p.Code = append(p.Code, Instr{Op: OpAdd})
			} else {
				p.Code = append(p.Code, Instr{Op: OpSub})
			}
		case bytecode.OpDup:
			p.Code = append(p.Code, Instr{Op: OpDup})
		case bytecode.OpSwap:
			p.Code = append(p.Code, Instr{Op: OpSwap})
		case bytecode.OpJmp, bytecode.OpJmpTruePop, bytecode.OpJmpFalsePop,
			bytecode.OpJmpTrueKeep, bytecode.OpJmpFalseKeep, bytecode.OpJmpNullishKeep:
			target := pc + bytecode.InstrSize + bytecode.SignedOperand(arg)
			irOp := OpJump
			switch op {
			case bytecode.OpJmpTruePop:
				irOp = OpJumpTrue
			case bytecode.OpJmpFalsePop:
				irOp = OpJumpFalse
			case bytecode.OpJmpTrueKeep:
				irOp = OpJumpTrueKeep
			case bytecode.OpJmpFalseKeep:
				irOp = OpJumpFalseKeep
			case bytecode.OpJmpNullishKeep:
				irOp = OpJumpNullishKeep
			}
			p.Code = append(p.Code, Instr{Op: irOp, Operand: uint32(target)})
		case bytecode.OpThrow:
			// The thrown value sits on the stack top. Compile to a dedicated
			// exception exit placed right here: the executor pops the value
			// into DeoptExit.PendingException and the VM throws it on resume,
			// entering the existing try/catch/finally state machine. ResumePC
			// is the instruction after the throw (diagnostic; the VM does not
			// resume execution at it). A jump is NOT used so the exit-fixup
			// pass below cannot mistake this for a normal branch target.
			exitID := len(trace.exits)
			trace.exits = append(trace.exits, DeoptExit{ID: exitID, ResumePC: pc + bytecode.InstrSize})
			for len(p.traceExceptionExits) < len(trace.exits) {
				p.traceExceptionExits = append(p.traceExceptionExits, false)
			}
			p.traceExceptionExits[exitID] = true
			p.Code = append(p.Code, Instr{Op: OpTraceExit, Operand: uint32(exitID)})
		default:
			return nil, fmt.Errorf("jit: trace unsupported opcode %s", op)
		}
	}
	if len(guardByPC) != 0 || len(methodByPC) != 0 {
		return nil, fmt.Errorf("jit: unused trace call guard")
	}
	exitByPC := make(map[int]int)
	for i := range p.Code {
		switch p.Code[i].Op {
		case OpJump, OpJumpTrue, OpJumpFalse, OpJumpTrueKeep, OpJumpFalseKeep, OpJumpNullishKeep:
			targetPC := int(p.Code[i].Operand)
			if target, ok := pcToIR[targetPC]; ok {
				p.Code[i].Operand = uint32(target)
				continue
			}
			exitID, ok := exitByPC[targetPC]
			if !ok {
				exitID = len(trace.exits)
				exitByPC[targetPC] = exitID
				trace.exits = append(trace.exits, DeoptExit{ID: exitID, ResumePC: targetPC})
				for len(p.traceExceptionExits) < len(trace.exits) {
					p.traceExceptionExits = append(p.traceExceptionExits, false)
				}
			}
			exitFixups = append(exitFixups, exitFixup{instruction: i, exitID: exitID})
		}
	}
	if len(trace.exits) == 0 {
		return nil, fmt.Errorf("jit: trace has no exit")
	}
	if len(p.traceExceptionExits) != len(trace.exits) {
		return nil, fmt.Errorf("jit: exception exit map size %d != exits %d", len(p.traceExceptionExits), len(trace.exits))
	}
	localSlots := make([]uint16, 0, len(trace.written))
	for slot, written := range trace.written {
		if written {
			localSlots = append(localSlots, uint16(slot))
		}
	}
	p.traceExitDepths = make([]uint8, len(trace.exits))
	for i := range p.traceExitDepths {
		p.traceExitDepths[i] = ^uint8(0)
	}
	for i := range trace.exits {
		trace.exits[i].LocalSlots = append([]uint16(nil), localSlots...)
	}
	// Normal exits get their OpTraceExit appended here (exception exits were
	// placed at their throw site), and every fixup target is resolved to the
	// exact IR position of its exit's OpTraceExit.
	exitIRPos := make([]int, len(trace.exits))
	for i := range exitIRPos {
		exitIRPos[i] = -1
	}
	for i := range trace.exits {
		if p.traceExceptionExits[i] {
			continue
		}
		exitIRPos[i] = len(p.Code)
		p.Code = append(p.Code, Instr{Op: OpTraceExit, Operand: uint32(i)})
	}
	for _, fixup := range exitFixups {
		pos := exitIRPos[fixup.exitID]
		if pos < 0 {
			return nil, fmt.Errorf("jit: exit %d has no OpTraceExit to fix up", fixup.exitID)
		}
		p.Code[fixup.instruction].Operand = uint32(pos)
	}
	p.propertyGuards = make([]propertyGuard, len(p.Code))
	if err := p.Verify(); err != nil {
		return nil, err
	}
	for i := range trace.exits {
		if p.traceExitDepths[i] == ^uint8(0) {
			return nil, fmt.Errorf("jit: unreachable trace exit %d", i)
		}
		trace.exits[i].StackDepth = int(p.traceExitDepths[i])
	}
	return trace, nil
}

func (t *TraceProgram) GuardedNoopCalls() int {
	if t == nil || t.program == nil {
		return 0
	}
	return len(t.program.traceCallGuards)
}

func (t *TraceProgram) GuardedMethodCalls() int {
	if t == nil || t.program == nil {
		return 0
	}
	return len(t.program.traceMethodGuards)
}

func (t *TraceProgram) DeoptExits() []DeoptExit {
	if t == nil {
		return nil
	}
	exits := make([]DeoptExit, len(t.exits))
	for i := range t.exits {
		exits[i] = t.exits[i]
		exits[i].LocalSlots = append([]uint16(nil), t.exits[i].LocalSlots...)
		exits[i].StackValues = nil
	}
	return exits
}

func (t *TraceProgram) DumpIR() string {
	if t == nil || t.program == nil {
		return "<nil>\n"
	}
	return t.program.DumpIR()
}

func (t *TraceProgram) Execute(locals []engine.Value) (int, ExitReason, error) {
	return t.ExecuteBudget(locals, 0)
}

// ExecuteBudget executes at most budget loop backedges. A zero budget runs to
// the trace's semantic exit. Budget exhaustion commits only completed loop
// iterations and resumes Tier 0 at the loop header.
func (t *TraceProgram) ExecuteBudget(locals []engine.Value, budget uint32) (int, ExitReason, error) {
	exit, reason, err := t.ExecuteBudgetDetailed(locals, budget)
	return exit.ResumePC, reason, err
}

func (t *TraceProgram) ExecuteBudgetDetailed(locals []engine.Value, budget uint32) (DeoptExit, ExitReason, error) {
	return t.ExecuteBudgetDetailedWithSafepoint(locals, budget, nil)
}

func (t *TraceProgram) ExecuteBudgetDetailedWithSafepoint(locals []engine.Value, budget uint32, poll Safepoint) (DeoptExit, ExitReason, error) {
	if t == nil || t.program == nil || len(locals) < t.program.NumLocals {
		return DeoptExit{}, Malformed, fmt.Errorf("jit: invalid trace locals")
	}
	var values [maxQuickSlots]quickValue
	var objects [maxQuickSlots]engine.Value
	objectCount := 0
	// The string constant pool occupies the front of the object buffer (same
	// layout as the function executor), so OpConstString refs and string
	// locals resolve through one objects slice.
	for _, constant := range t.program.stringConsts {
		if objectCount >= len(objects) {
			return DeoptExit{}, GuardFailed, nil
		}
		objects[objectCount] = constant
		objectCount++
	}
	for i := 0; i < t.program.NumLocals; i++ {
		values[i] = fromEngine(locals[i], &objects, &objectCount)
	}
	// Upvalue cells are read once at entry into a flat cache; every
	// OpLoadUpvalueNum/OpStoreUpvalueNum then works on the cache and the writes
	// are committed with the property writes at exits and budget yields. A cell
	// holding a non-Number fails the guard here, before any state changed.
	var upvalueCache [maxQuickSlots]float64
	var upvalueDirty [maxQuickSlots]bool
	if len(t.program.traceUpvalues) > len(upvalueCache) {
		return DeoptExit{}, GuardFailed, nil
	}
	for i := range t.program.traceUpvalues {
		number, ok := t.program.traceUpvalues[i].cell.LoadNumber()
		if !ok {
			return DeoptExit{}, GuardFailed, nil
		}
		upvalueCache[i] = number
	}
	var stackBuf [maxQuickSlots]quickValue
	stack := stackBuf[:0]
	push := func(v quickValue) { stack = append(stack, v) }
	pop := func() quickValue { v := stack[len(stack)-1]; stack = stack[:len(stack)-1]; return v }
	var dirty [maxQuickSlots]bool
	type tracePropertyState struct {
		object engine.Value
		name   string
		guard  *propertyGuard
		value  float64
		dirty  bool
	}
	propertyStates := make([]tracePropertyState, 0, 4)
	findProperty := func(object engine.Value, name string) int {
		for i := range propertyStates {
			if propertyStates[i].object == object && propertyStates[i].name == name {
				return i
			}
		}
		return -1
	}
	// commitSideEffects is the R1-5 two-phase commit protocol for the trace's
	// deferred side effects. It is only called at semantic exits and budget
	// yields (never between iterations), so a failure discards the whole
	// slice: neither the properties nor the locals were written yet, and Tier 0
	// can replay the slice cleanly.
	//
	// Phase 1 (validate): every dirty property is re-checked against its
	// guard. This can only fail if the object was mutated between the write
	// site and the exit — impossible inside a single trace slice, so the check
	// is a defensive re-validation of the prepare-time guard.
	//
	// Phase 2 (commit): the deferred values are stored, then the dirty locals
	// are written back. The store loop snapshots the original values first and
	// rolls back the already-stored properties if a store fails, so a partial
	// commit is never observable even on a defensive failure.
	//
	// Dirty upvalue cells join the same batch: they are validated in phase 1
	// (the cell must still hold a Number) and stored in phase 2 with the same
	// rollback discipline, so an upvalue and a property write can never be
	// half-committed relative to each other.
	commitSideEffects := func() bool {
		originals := make([]float64, len(propertyStates))
		var upvalueOriginals [maxQuickSlots]float64
		// Phase 1 must complete for the whole batch before any externally
		// visible mutation. Otherwise a later invalid property could leave an
		// earlier property committed while locals remain uncommitted.
		for i := range propertyStates {
			state := &propertyStates[i]
			if !state.dirty {
				continue
			}
			number, ok := state.guard.loadNumber(state.object, state.name)
			if !ok {
				return false
			}
			originals[i] = number
		}
		for i := range t.program.traceUpvalues {
			if !upvalueDirty[i] {
				continue
			}
			number, ok := t.program.traceUpvalues[i].cell.LoadNumber()
			if !ok {
				return false
			}
			upvalueOriginals[i] = number
		}
		var stored []int
		var storedUpvalues []int
		rollback := func() {
			for k := len(storedUpvalues) - 1; k >= 0; k-- {
				j := storedUpvalues[k]
				_ = t.program.traceUpvalues[j].cell.StoreNumber(upvalueOriginals[j])
			}
			for k := len(stored) - 1; k >= 0; k-- {
				j := stored[k]
				entry := &propertyStates[j]
				_ = entry.guard.storeNumber(entry.object, entry.name, originals[j])
			}
		}
		for i := range propertyStates {
			state := &propertyStates[i]
			if !state.dirty {
				continue
			}
			if !state.guard.storeNumber(state.object, state.name, state.value) {
				rollback()
				return false
			}
			stored = append(stored, i)
		}
		for i := range t.program.traceUpvalues {
			if !upvalueDirty[i] {
				continue
			}
			if !t.program.traceUpvalues[i].cell.StoreNumber(upvalueCache[i]) {
				rollback()
				return false
			}
			storedUpvalues = append(storedUpvalues, i)
		}
		for i, written := range dirty {
			if written {
				locals[i] = values[i].toEngine(objects[:objectCount])
			}
		}
		return true
	}
	var backedges uint32
	for ip := 0; ip < len(t.program.Code); {
		in := t.program.Code[ip]
		ip++
		switch in.Op {
		case OpConst:
			push(numberValue(in.Value))
		case OpConstString:
			// Defensive: Verify bounds the pool, and the prepend above fails
			// GuardFailed when the buffer is exhausted.
			if int(in.Operand) >= len(objects) {
				return DeoptExit{}, GuardFailed, nil
			}
			truthy, _ := objects[in.Operand].Bool()
			push(quickValue{kind: quickString, ref: uint8(in.Operand), b: truthy})
		case OpLoadLocal:
			push(values[in.Operand])
		case OpStoreLocal:
			values[in.Operand] = pop()
			dirty[in.Operand] = true
		case OpLoadUpvalueNum:
			push(numberValue(upvalueCache[in.Operand]))
		case OpStoreUpvalueNum:
			value := pop()
			if value.kind != quickNumber {
				return DeoptExit{}, GuardFailed, nil
			}
			upvalueCache[in.Operand] = value.num
			upvalueDirty[in.Operand] = true
		case OpGetProp:
			object := pop()
			if object.kind != quickObject || int(object.ref) >= objectCount {
				return DeoptExit{}, GuardFailed, nil
			}
			objectValue := objects[object.ref]
			if stateIndex := findProperty(objectValue, in.Name); stateIndex >= 0 {
				push(numberValue(propertyStates[stateIndex].value))
				continue
			}
			guard := &t.program.propertyGuards[ip-1]
			n, ok := guard.loadNumber(objectValue, in.Name)
			if !ok {
				return DeoptExit{}, GuardFailed, nil
			}
			propertyStates = append(propertyStates, tracePropertyState{object: objectValue, name: in.Name, guard: guard, value: n})
			push(numberValue(n))
		case OpSetProp:
			object, value := pop(), pop()
			if object.kind != quickObject || int(object.ref) >= objectCount || !value.isNumber() {
				return DeoptExit{}, GuardFailed, nil
			}
			objectValue := objects[object.ref]
			stateIndex := findProperty(objectValue, in.Name)
			if stateIndex < 0 {
				guard := &t.program.propertyGuards[ip-1]
				current, ok := guard.loadNumber(objectValue, in.Name)
				if !ok {
					return DeoptExit{}, GuardFailed, nil
				}
				propertyStates = append(propertyStates, tracePropertyState{object: objectValue, name: in.Name, guard: guard, value: current})
				stateIndex = len(propertyStates) - 1
			}
			propertyStates[stateIndex].value = value.num
			propertyStates[stateIndex].dirty = true
		case OpGuardNoopCall:
			callee := pop()
			if callee.kind != quickObject || int(callee.ref) >= objectCount ||
				int(in.Operand) >= len(t.program.traceCallGuards) ||
				objects[callee.ref] != t.program.traceCallGuards[in.Operand].target {
				return DeoptExit{}, GuardFailed, nil
			}
			push(quickValue{})
		case OpGuardMethodGet:
			receiver := pop()
			if receiver.kind != quickObject || int(receiver.ref) >= objectCount ||
				int(in.Operand) >= len(t.program.traceMethodGuards) {
				return DeoptExit{}, GuardFailed, nil
			}
			method := t.program.traceMethodGuards[in.Operand]
			objectValue, ok := objects[receiver.ref].AsObject()
			if !ok {
				return DeoptExit{}, GuardFailed, nil
			}
			methodValue, ok := engine.GuardedMethodLookup(objectValue, method.method)
			if !ok || methodValue != method.target {
				return DeoptExit{}, GuardFailed, nil
			}
			if stateIndex := findProperty(objectValue, method.property); stateIndex >= 0 {
				push(numberValue(propertyStates[stateIndex].value))
				continue
			}
			guard := &t.program.propertyGuards[ip-1]
			number, ok := guard.loadNumber(objectValue, method.property)
			if !ok {
				return DeoptExit{}, GuardFailed, nil
			}
			propertyStates = append(propertyStates, tracePropertyState{object: objectValue, name: method.property, guard: guard, value: number})
			push(numberValue(number))
		case OpAdd, OpSub, OpMul, OpDiv, OpMod, OpPow:
			r, l := pop(), pop()
			switch {
			case l.isNumber() && r.isNumber():
				switch in.Op {
				case OpAdd:
					push(numberValue(l.num + r.num))
				case OpSub:
					push(numberValue(l.num - r.num))
				case OpMul:
					push(numberValue(l.num * r.num))
				case OpDiv:
					push(numberValue(l.num / r.num))
				case OpMod:
					push(numberValue(floatMod(l.num, r.num)))
				case OpPow:
					push(numberValue(math.Pow(l.num, r.num)))
				}
			case l.kind == quickString && r.kind == quickString:
				// R3-4: same-type String concat; allocation happens only in the
				// trace executor and the result stays a quickString.
				if in.Op != OpAdd {
					return DeoptExit{}, GuardFailed, nil
				}
				if objectCount >= maxQuickSlots {
					compactTraceObjects(values[:t.program.NumLocals], len(t.program.stringConsts), &objects, &objectCount)
				}
				result, ok := quickStringConcat(l, r, &objects, &objectCount)
				if !ok {
					return DeoptExit{}, GuardFailed, nil
				}
				push(result)
			case l.kind == quickString:
				if in.Op != OpAdd {
					return DeoptExit{}, GuardFailed, nil
				}
				if objectCount >= maxQuickSlots {
					compactTraceObjects(values[:t.program.NumLocals], len(t.program.stringConsts), &objects, &objectCount)
				}
				result, ok := quickStringAnyConcat(l, r, true, &objects, &objectCount)
				if !ok {
					return DeoptExit{}, GuardFailed, nil
				}
				push(result)
			case r.kind == quickString:
				if in.Op != OpAdd {
					return DeoptExit{}, GuardFailed, nil
				}
				if objectCount >= maxQuickSlots {
					compactTraceObjects(values[:t.program.NumLocals], len(t.program.stringConsts), &objects, &objectCount)
				}
				result, ok := quickStringAnyConcat(r, l, false, &objects, &objectCount)
				if !ok {
					return DeoptExit{}, GuardFailed, nil
				}
				push(result)
			case l.kind == quickBigInt && r.kind == quickBigInt:
				// R3-5: same-type BigInt arithmetic. `**` is not part of R3-5
				// and div/mod by zero falls back so Tier 0 raises the
				// identical RangeError.
				if in.Op == OpPow {
					return DeoptExit{}, GuardFailed, nil
				}
				result, ok := quickBigIntArith(l, r, &objects, &objectCount, in.Op)
				if !ok {
					return DeoptExit{}, GuardFailed, nil
				}
				push(result)
			default:
				return DeoptExit{}, GuardFailed, nil
			}
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
				// R3-5: same-type BigInt bitwise ops; `>>>` and negative shifts
				// fall back so Tier 0 raises the identical TypeError/RangeError.
				result, ok := quickBigIntBitwise(l, r, &objects, &objectCount, in.Op)
				if !ok {
					return DeoptExit{}, GuardFailed, nil
				}
				push(result)
			} else {
				return DeoptExit{}, GuardFailed, nil
			}
		case OpNeg:
			n := pop()
			switch {
			case n.isNumber():
				push(numberValue(-n.num))
			case n.kind == quickBigInt:
				// R3-5: unary minus on BigInt.
				result, ok := quickBigIntNeg(n, &objects, &objectCount)
				if !ok {
					return DeoptExit{}, GuardFailed, nil
				}
				push(result)
			default:
				return DeoptExit{}, GuardFailed, nil
			}
		case OpNot:
			value := pop()
			truth, ok := value.truthy()
			if !ok {
				return DeoptExit{}, GuardFailed, nil
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
				result, ok := quickBigIntNot(n, &objects, &objectCount)
				if !ok {
					return DeoptExit{}, GuardFailed, nil
				}
				push(result)
			default:
				return DeoptExit{}, GuardFailed, nil
			}
		case OpUnaryPlus:
			n := pop()
			if !n.isNumber() {
				return DeoptExit{}, GuardFailed, nil
			}
			push(n)
		case OpEq, OpNe:
			r, l := pop(), pop()
			// R3-3: primitives compare per JS semantics through the shared
			// helper; object operands (and the recorded Tier 0 divergences)
			// guard-fail the whole slice so Tier 0 replays it.
			equal, ok := quickLooseEqual(l, r, objects[:objectCount])
			if !ok {
				return DeoptExit{}, GuardFailed, nil
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
				cmp, ok := quickStringCompare(l, r, &objects)
				if !ok {
					return DeoptExit{}, GuardFailed, nil
				}
				b = quickRelational(cmp, in.Op)
			case l.kind == quickBigInt && r.kind == quickBigInt:
				// R3-5: same-type BigInt relational comparison.
				cmp, ok := quickBigIntCompare(l, r, &objects)
				if !ok {
					return DeoptExit{}, GuardFailed, nil
				}
				b = quickRelational(cmp, in.Op)
			default:
				return DeoptExit{}, GuardFailed, nil
			}
			push(booleanValue(b))
		case OpStrictEq, OpStrictNe:
			r, l := pop(), pop()
			equal, ok := strictQuickEqual(l, r, objects[:objectCount])
			if !ok {
				return DeoptExit{}, GuardFailed, nil
			}
			if in.Op == OpStrictNe {
				equal = !equal
			}
			push(booleanValue(equal))
		case OpPop:
			_ = pop()
		case OpDup:
			push(stack[len(stack)-1])
		case OpSwap:
			n := len(stack) - 1
			stack[n], stack[n-1] = stack[n-1], stack[n]
		case OpJump:
			if in.Operand == 0 && budget != 0 {
				backedges++
				if backedges >= budget {
					if !commitSideEffects() {
						return DeoptExit{}, GuardFailed, nil
					}
					if poll != nil {
						if err := runSafepoint(poll); err != nil {
							return DeoptExit{ID: -1, ResumePC: t.startPC}, Interrupted, err
						}
					}
					return DeoptExit{ID: -1, ResumePC: t.startPC}, Yielded, nil
				}
				if objectCount > 24 {
					compactTraceObjects(values[:t.program.NumLocals], len(t.program.stringConsts), &objects, &objectCount)
				}
			}
			ip = int(in.Operand)
		case OpJumpTrue, OpJumpFalse:
			truth, ok := pop().truthy()
			if !ok {
				return DeoptExit{}, GuardFailed, nil
			}
			if in.Op == OpJumpTrue && truth || in.Op == OpJumpFalse && !truth {
				ip = int(in.Operand)
			}
		case OpJumpTrueKeep, OpJumpFalseKeep:
			truth, ok := stack[len(stack)-1].truthy()
			if !ok {
				return DeoptExit{}, GuardFailed, nil
			}
			if in.Op == OpJumpTrueKeep && truth || in.Op == OpJumpFalseKeep && !truth {
				ip = int(in.Operand)
			} else {
				_ = pop()
			}
		case OpJumpNullishKeep:
			nullish, ok := stack[len(stack)-1].nullish()
			if !ok {
				return DeoptExit{}, GuardFailed, nil
			}
			if !nullish {
				ip = int(in.Operand)
			} else {
				_ = pop()
			}
		case OpTraceExit:
			exitID := int(in.Operand)
			if exitID < 0 || exitID >= len(t.exits) {
				return DeoptExit{}, Malformed, fmt.Errorf("jit: invalid trace exit %d", exitID)
			}
			if !commitSideEffects() {
				return DeoptExit{}, GuardFailed, nil
			}
			exit := t.exits[exitID]
			// An exception exit carries the thrown value on the stack top:
			// move it into PendingException instead of restoring it.
			if exitID < len(t.program.traceExceptionExits) && t.program.traceExceptionExits[exitID] {
				if len(stack) < 1 {
					return DeoptExit{}, Malformed, fmt.Errorf("jit: exception exit %d has no thrown value", exitID)
				}
				thrown := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				exit.PendingException = thrown.toEngine(objects[:objectCount])
			}
			if len(stack) != exit.StackDepth {
				return DeoptExit{}, Malformed, fmt.Errorf("jit: trace exit stack depth %d, want %d", len(stack), exit.StackDepth)
			}
			if len(stack) != 0 {
				exit.StackValues = make([]engine.Value, len(stack))
				for i := range stack {
					exit.StackValues[i] = stack[i].toEngine(objects[:objectCount])
				}
			}
			return exit, Executed, nil
		default:
			return DeoptExit{}, Malformed, fmt.Errorf("jit: invalid trace opcode %d", in.Op)
		}
	}
	return DeoptExit{}, Malformed, fmt.Errorf("jit: trace fell off program")
}

// compactTraceObjects 紧凑化回收 trace 循环中的非存活临时对象引用，
// 保持常量池在前部，并重写当前活跃 locals 的 ref，避免在长循环中填满固定对象池。
func compactTraceObjects(values []quickValue, stringConstsCount int, objects *[maxQuickSlots]engine.Value, objectCount *int) {
	var newObjects [maxQuickSlots]engine.Value
	newCount := stringConstsCount
	for i := 0; i < stringConstsCount && i < maxQuickSlots; i++ {
		newObjects[i] = objects[i]
	}

	for i := range values {
		v := &values[i]
		if v.kind == quickString || v.kind == quickObject || v.kind == quickBigInt || v.kind == quickSymbol {
			oldRef := int(v.ref)
			if oldRef < len(objects) && objects[oldRef] != nil {
				if oldRef < stringConstsCount {
					continue
				}
				found := -1
				for j := stringConstsCount; j < newCount; j++ {
					if newObjects[j] == objects[oldRef] {
						found = j
						break
					}
				}
				if found >= 0 {
					v.ref = uint8(found)
				} else if newCount < maxQuickSlots {
					newObjects[newCount] = objects[oldRef]
					v.ref = uint8(newCount)
					newCount++
				}
			}
		}
	}
	*objects = newObjects
	*objectCount = newCount
}
