package jit

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	jitnative "github.com/aluka-lang/aluka/internal/engine/jit/native"
)

type nativePropertyInput struct {
	sourceLocal int
	frameLocal  int
	name        string
	write       bool
	guard       propertyGuard
}

type nativeInputPlan struct {
	numberArgs   uint16
	numberLocals uint64
	properties   []nativePropertyInput
	callees      []nativeCalleeGuard
	stackBase    int
}

type nativeCalleeGuard struct {
	sourceLocal int
	target      engine.Value
	method      string
}

func (p *Program) CompileNative() error {
	return p.compileNative(false)
}

func (p *Program) CompileNativeForDump() error {
	return p.compileNative(true)
}

// hasExceptionExit reports whether the program contains an exception exit.
// Exception exits carry a JS pending-exception value that Native cannot
// represent (no Go pointers / engine.Values in the frame), so such programs
// must never be published as machine code.
func (p *Program) hasExceptionExit() bool {
	for _, isException := range p.traceExceptionExits {
		if isException {
			return true
		}
	}
	return false
}

func (p *Program) compileNative(retainDebugBytes bool) error {
	if p == nil {
		return fmt.Errorf("jit: nil program")
	}
	if p.nativeCode != nil {
		return nil
	}
	if p.hasExceptionExit() {
		return fmt.Errorf("jit: native cannot represent exception exit (pending JS exception)")
	}
	lowered, plan, err := lowerNativeInputs(p)
	if err != nil {
		return err
	}
	code, err := compileNativeProgram(lowered, retainDebugBytes)
	if err != nil {
		return err
	}
	p.nativeCode = code
	p.nativePlan = plan
	return nil
}

func (p *Program) NativeDebugBytes() []byte {
	if p == nil || p.nativeCode == nil {
		return nil
	}
	return p.nativeCode.DebugBytes()
}

func (p *Program) NativeDisassembly() string {
	return jitnative.Disassemble(p.NativeDebugBytes())
}

func (p *Program) CloneForNative() *Program {
	if p == nil {
		return nil
	}
	clone := *p
	clone.Code = append([]Instr(nil), p.Code...)
	clone.traceExitDepths = append([]uint8(nil), p.traceExitDepths...)
	clone.traceExceptionExits = append([]bool(nil), p.traceExceptionExits...)
	clone.stringConsts = append([]engine.Value(nil), p.stringConsts...)
	clone.propertyGuards = nil
	clone.nativeCode = nil
	clone.nativePlan = nil
	return &clone
}

func (p *Program) AdoptNativeFrom(compiled *Program) error {
	if p == nil || compiled == nil || compiled.nativeCode == nil || compiled.nativePlan == nil {
		return fmt.Errorf("jit: invalid prepared native program")
	}
	if p.nativeCode != nil {
		return fmt.Errorf("jit: native program is already installed")
	}
	p.nativeCode, p.nativePlan = compiled.nativeCode, compiled.nativePlan
	compiled.nativeCode, compiled.nativePlan = nil, nil
	return nil
}

func (p *Program) HasNative() bool { return p != nil && p.nativeCode != nil }

func (p *Program) NativeSize() int {
	if p == nil || p.nativeCode == nil {
		return 0
	}
	return p.nativeCode.Size()
}

func (p *Program) ExecuteNative(thisVal engine.Value, args []engine.Value) (engine.Value, ExitReason, error) {
	value, reason, _, err := p.ExecuteNativeBudget(thisVal, args, 65536)
	return value, reason, err
}

func (p *Program) ExecuteNativeBudget(thisVal engine.Value, args []engine.Value, budget uint32) (engine.Value, ExitReason, uint64, error) {
	return p.ExecuteNativeBudgetWithSafepoint(thisVal, args, budget, nil)
}

func (p *Program) ExecuteNativeBudgetWithSafepoint(thisVal engine.Value, args []engine.Value, budget uint32, poll Safepoint) (engine.Value, ExitReason, uint64, error) {
	if p == nil || p.nativeCode == nil {
		return engine.Undefined(), Malformed, 0, nil
	}
	if len(args) > 8 || p.nativePlan == nil {
		return engine.Undefined(), GuardFailed, 0, nil
	}
	if budget == 0 {
		budget = 65536
	}
	frame := &jitnative.Frame{}
	for i := 0; i < p.NumParams; i++ {
		if p.nativePlan.numberArgs&(uint16(1)<<i) == 0 {
			continue
		}
		if i >= len(args) || args[i].Type() != engine.TypeNumber {
			return engine.Undefined(), GuardFailed, 0, nil
		}
		frame.Args[i], _ = args[i].Float()
	}
	for i := range p.nativePlan.properties {
		input := &p.nativePlan.properties[i]
		value, ok := nativeSourceValue(thisVal, args, input.sourceLocal)
		if !ok {
			return engine.Undefined(), GuardFailed, 0, nil
		}
		number, ok := input.guard.loadNumber(value, input.name)
		if !ok {
			return engine.Undefined(), GuardFailed, 0, nil
		}
		frame.Locals[input.frameLocal] = number
	}
	frame.Budget = uint64(budget)
	status := p.nativeCode.Call(frame)
	var yields uint64
	for status == 2 {
		yields++
		if err := runSafepoint(poll); err != nil {
			return engine.Undefined(), Interrupted, yields, err
		}
		frame.Budget = uint64(budget)
		status = p.nativeCode.CallAt(frame.Resume, frame)
	}
	if status != 0 {
		return engine.Undefined(), Malformed, yields, fmt.Errorf("jit: native status %d", status)
	}
	return engine.Number(frame.Result), Executed, yields, nil
}

func (p *Program) Close() error {
	if p == nil || p.nativeCode == nil {
		return nil
	}
	err := p.nativeCode.Close()
	p.nativeCode = nil
	p.nativePlan = nil
	return err
}

func lowerNativeInputs(p *Program) (*Program, *nativeInputPlan, error) {
	return lowerNativeInputsForMode(p, false)
}

func lowerNativeTraceInputs(p *Program) (*Program, *nativeInputPlan, error) {
	return lowerNativeInputsForMode(p, true)
}

func lowerNativeInputsForMode(p *Program, trace bool) (*Program, *nativeInputPlan, error) {
	if p == nil || p.NumParams > 8 {
		return nil, nil, fmt.Errorf("jit: invalid native input program")
	}
	if p.hasExceptionExit() {
		return nil, nil, fmt.Errorf("jit: native cannot represent exception exit (pending JS exception)")
	}
	plan := &nativeInputPlan{}
	code := make([]Instr, 0, len(p.Code))
	preassigned := uint64(0)
	oldToNew := make([]int, len(p.Code))
	targeted := make([]bool, len(p.Code))
	for _, in := range p.Code {
		switch in.Op {
		case OpJump, OpJumpTrue, OpJumpFalse, OpJumpTrueKeep, OpJumpFalseKeep, OpJumpNullishKeep:
			if int(in.Operand) < 0 || int(in.Operand) >= len(targeted) {
				return nil, nil, fmt.Errorf("jit: native input jump target out of range")
			}
			targeted[in.Operand] = true
		}
	}
	type jumpFixup struct {
		index     int
		oldTarget int
	}
	fixups := make([]jumpFixup, 0, 4)
	type propertyKey struct {
		sourceLocal int
		name        string
	}
	propertyIndexes := make(map[propertyKey]int)
	ensureProperty := func(source int, name string) (int, error) {
		maxSource := p.NumParams
		if trace {
			maxSource = p.NumLocals - 1
		}
		if source < 0 || source > maxSource {
			return 0, fmt.Errorf("jit: native property source is unsupported")
		}
		key := propertyKey{sourceLocal: source, name: name}
		if index, ok := propertyIndexes[key]; ok {
			return index, nil
		}
		if p.NumLocals+len(plan.properties) >= maxQuickSlots {
			return 0, fmt.Errorf("jit: native property input limit exceeded")
		}
		index := len(plan.properties)
		frameLocal := p.NumLocals + index
		plan.properties = append(plan.properties, nativePropertyInput{
			sourceLocal: source,
			frameLocal:  frameLocal,
			name:        name,
			// Native entry guards keep the historical two-way PIC semantics
			// (R4-3): the portable Quick-tier guards absorb 2-4 stable shapes,
			// while a third shape in the native input plan stays a miss so the
			// existing third-shape cutoff / disable tests keep their behavior.
			guard: propertyGuard{snapshot: true},
		})
		propertyIndexes[key] = index
		preassigned |= uint64(1) << frameLocal
		return index, nil
	}
	for i := 0; i < len(p.Code); i++ {
		oldToNew[i] = len(code)
		in := p.Code[i]
		if trace && in.Op == OpLoadLocal && i+2 < len(p.Code) &&
			p.Code[i+1].Op == OpGuardNoopCall && p.Code[i+2].Op == OpPop {
			if targeted[i+1] || targeted[i+2] {
				return nil, nil, fmt.Errorf("jit: native noop-call fusion has an interior jump target")
			}
			guardIndex := int(p.Code[i+1].Operand)
			if guardIndex < 0 || guardIndex >= len(p.traceCallGuards) ||
				p.traceCallGuards[guardIndex].sourceLocal != int(in.Operand) {
				return nil, nil, fmt.Errorf("jit: invalid native noop-call guard")
			}
			noop := p.traceCallGuards[guardIndex]
			plan.callees = append(plan.callees, nativeCalleeGuard{
				sourceLocal: noop.sourceLocal, target: noop.target,
			})
			i += 2
			oldToNew[i-1] = len(code)
			oldToNew[i] = len(code)
			continue
		}
		if trace && in.Op == OpLoadLocal && i+1 < len(p.Code) && p.Code[i+1].Op == OpGuardMethodGet {
			if targeted[i+1] {
				return nil, nil, fmt.Errorf("jit: native method fusion has an interior jump target")
			}
			guardIndex := int(p.Code[i+1].Operand)
			if guardIndex < 0 || guardIndex >= len(p.traceMethodGuards) ||
				p.traceMethodGuards[guardIndex].sourceLocal != int(in.Operand) {
				return nil, nil, fmt.Errorf("jit: invalid native method guard")
			}
			method := p.traceMethodGuards[guardIndex]
			propertyIndex, err := ensureProperty(int(in.Operand), method.property)
			if err != nil {
				return nil, nil, err
			}
			plan.callees = append(plan.callees, nativeCalleeGuard{
				sourceLocal: method.sourceLocal, target: method.target, method: method.method,
			})
			code = append(code, Instr{Op: OpLoadLocal, Operand: uint32(plan.properties[propertyIndex].frameLocal)})
			i++
			oldToNew[i] = len(code) - 1
			continue
		}
		if in.Op == OpLoadLocal && i+1 < len(p.Code) &&
			(p.Code[i+1].Op == OpGetProp || trace && p.Code[i+1].Op == OpSetProp) {
			if targeted[i+1] {
				return nil, nil, fmt.Errorf("jit: native property fusion has an interior jump target")
			}
			source := int(in.Operand)
			propertyOp := p.Code[i+1]
			propertyIndex, err := ensureProperty(source, propertyOp.Name)
			if err != nil {
				return nil, nil, err
			}
			input := &plan.properties[propertyIndex]
			if propertyOp.Op == OpSetProp {
				input.write = true
				code = append(code, Instr{Op: OpStoreLocal, Operand: uint32(input.frameLocal)})
			} else {
				code = append(code, Instr{Op: OpLoadLocal, Operand: uint32(input.frameLocal)})
			}
			i++
			oldToNew[i] = len(code) - 1
			continue
		}
		if in.Op == OpSetProp {
			return nil, nil, fmt.Errorf("jit: native property write requires a local object")
		}
		if in.Op == OpLoadLocal {
			slot := int(in.Operand)
			if trace {
				if slot < 0 || slot >= p.NumLocals {
					return nil, nil, fmt.Errorf("jit: native trace local out of range")
				}
				plan.numberLocals |= uint64(1) << slot
				preassigned |= uint64(1) << slot
			} else {
				if slot == 0 {
					return nil, nil, fmt.Errorf("jit: native numeric this is unsupported")
				}
				if slot >= 1 && slot <= p.NumParams {
					plan.numberArgs |= uint16(1) << (slot - 1)
				}
			}
		}
		code = append(code, in)
		switch in.Op {
		case OpJump, OpJumpTrue, OpJumpFalse, OpJumpTrueKeep, OpJumpFalseKeep, OpJumpNullishKeep:
			fixups = append(fixups, jumpFixup{index: len(code) - 1, oldTarget: int(in.Operand)})
		}
	}
	for _, fixup := range fixups {
		code[fixup.index].Operand = uint32(oldToNew[fixup.oldTarget])
	}
	lowered := &Program{
		NumParams:           p.NumParams,
		NumLocals:           p.NumLocals + len(plan.properties),
		SelfUpvalue:         -1,
		Code:                code,
		stringConsts:        append([]engine.Value(nil), p.stringConsts...),
		nativeNumberArgs:    plan.numberArgs,
		nativePreassigned:   preassigned,
		nativeTrace:         trace,
		traceExitDepths:     append([]uint8(nil), p.traceExitDepths...),
		traceExceptionExits: append([]bool(nil), p.traceExceptionExits...),
	}
	plan.stackBase = lowered.NumLocals
	if err := lowered.Verify(); err != nil {
		return nil, nil, err
	}
	return lowered, plan, nil
}

func nativeSourceValue(thisVal engine.Value, args []engine.Value, local int) (engine.Value, bool) {
	if local == 0 {
		return thisVal, thisVal != nil
	}
	index := local - 1
	if index < 0 || index >= len(args) {
		return nil, false
	}
	return args[index], args[index] != nil
}
