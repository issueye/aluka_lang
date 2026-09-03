package jit

import (
	"fmt"
	"unsafe"

	"github.com/aluka-lang/aluka/internal/engine"
	jitnative "github.com/aluka-lang/aluka/internal/engine/jit/native"
)

// nativeRecMaxFrames 是自递归首轮帧数（Go 侧起始上限）；机器码每次调用
// 前读 Frame.RecLimit 作深度检查，超限以 status=1 返回 Go 侧扩容重试。
// 定义在平台无关文件：native_program.go 的 Execute 路径在所有平台编译，
// 而帧布局常量仅 amd64 发射器需要。
const nativeRecMaxFrames = 256

type nativePropertyInput struct {
	sourceLocal int
	frameLocal  int
	name        string
	write       bool
	guard       propertyGuard
}

// nativeUpvalueInput mirrors nativePropertyInput for a captured cell: the cell
// pointer stays on the Go side (in the plan), while the native frame only ever
// holds the float64 in frameLocal. Entry loads the number, exits and budget
// yields commit it back — the same protocol as property inputs, so the frame
// remains pointer-free.
type nativeUpvalueInput struct {
	cell       TraceUpvalueCell
	frameLocal int
	write      bool
}

type nativeInputPlan struct {
	numberArgs   uint16
	numberLocals uint64
	properties   []nativePropertyInput
	upvalues     []nativeUpvalueInput
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
	// F1 自递归：为含 OpSelfCall 的程序分配递归帧区，帧数按需增长——
	// 首轮 256 帧（66 槽/帧 × 8B：locals 32 槽 + 操作数栈保存 32 槽 +
	// 返回 PC + status），深度超限（机器码 status=1）时扩容重试。leaf
	// 候选是纯函数（无副作用、无外部观察），从头重试安全。
	// recBuf 在整个 Native 调用期间由本 goroutine 持有（GC 存活），机器码经
	// Frame.RecBase（uintptr 数值，不参与 GC 追踪）访问；调用结束后随 GC 回收。
	var recBuf []float64
	recFrames := nativeRecMaxFrames
	if p.hasSelfCall {
		recBuf = make([]float64, recFrames*66)
		frame.RecBase = uint64(uintptr(unsafe.Pointer(&recBuf[0])))
		frame.RecFP = 0
		frame.RecLimit = uint64(recFrames - 1)
	}
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
	var yields uint64
	status := uint64(0)
	for {
		frame.Budget = uint64(budget)
		frame.RecFP = 0
		status = p.nativeCode.Call(frame)
		for status == 2 {
			yields++
			if err := runSafepoint(poll); err != nil {
				return engine.Undefined(), Interrupted, yields, err
			}
			frame.Budget = uint64(budget)
			status = p.nativeCode.CallAt(frame.Resume, frame)
		}
		// status 1 = 自递归深度超限：扩容重试（×4），达全局上限才 GuardFailed。
		if status == 1 && p.hasSelfCall && recFrames < maxNativeRecursionFrames {
			recFrames *= 4
			if recFrames > maxNativeRecursionFrames {
				recFrames = maxNativeRecursionFrames
			}
			recBuf = make([]float64, recFrames*66)
			frame.RecBase = uint64(uintptr(unsafe.Pointer(&recBuf[0])))
			frame.RecLimit = uint64(recFrames - 1)
			continue
		}
		break
	}
	if status != 0 {
		// status 1 = F1 自递归深度超限（已达全局上限）：GuardFailed 回退
		// Tier 0（非 Malformed）。
		if status == 1 {
			return engine.Undefined(), GuardFailed, yields, nil
		}
		return engine.Undefined(), Malformed, yields, fmt.Errorf("jit: native status %d", status)
	}
	return engine.Number(frame.Result), Executed, yields, nil
}

// maxNativeRecursionFrames 是自递归 Native 的全局深度上限（帧数）。16K 帧 ×
// 528B ≈ 8.4MB，超过 Node/V8 默认调用栈（约 1 万帧）；更深递归回退 Tier 0
// （Go 栈自动增长，无上限）。
const maxNativeRecursionFrames = 16384

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
	for _, in := range p.Code {
		if in.Op == OpGetElem || in.Op == OpLoadUpvalueRef {
			// Element values and object-valued cells are engine.Values read per
			// iteration through the Go-side objects buffer; the pointer-free
			// Native frame cannot dereference them. Auto keeps such traces in
			// the Quick tier (the rejection is recorded once per program).
			return nil, nil, fmt.Errorf("jit: native cannot represent object-backed reads (kept in Quick tier)")
		}
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
	// Upvalue cells get their frame locals first (the set is known up front),
	// then property inputs are appended lazily as the lowering encounters them.
	if len(p.traceUpvalues) != 0 {
		if !trace {
			return nil, nil, fmt.Errorf("jit: upvalue cells require a trace program")
		}
		if p.NumLocals+len(p.traceUpvalues) >= maxQuickSlots {
			return nil, nil, fmt.Errorf("jit: native upvalue input limit exceeded")
		}
		for i := range p.traceUpvalues {
			frameLocal := p.NumLocals + i
			plan.upvalues = append(plan.upvalues, nativeUpvalueInput{
				cell:       p.traceUpvalues[i].cell,
				frameLocal: frameLocal,
				write:      p.traceUpvalues[i].write,
			})
			preassigned |= uint64(1) << frameLocal
		}
	}
	propertyBase := p.NumLocals + len(plan.upvalues)
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
		if propertyBase+len(plan.properties) >= maxQuickSlots {
			return 0, fmt.Errorf("jit: native property input limit exceeded")
		}
		index := len(plan.properties)
		frameLocal := propertyBase + index
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
		// Upvalue reads and writes become plain frame-local moves: entry loaded
		// the cell's number into frameLocal and the commit writes it back.
		if in.Op == OpLoadUpvalueNum || in.Op == OpStoreUpvalueNum {
			if int(in.Operand) >= len(plan.upvalues) {
				return nil, nil, fmt.Errorf("jit: native upvalue operand out of range")
			}
			frameLocal := uint32(plan.upvalues[in.Operand].frameLocal)
			if in.Op == OpLoadUpvalueNum {
				code = append(code, Instr{Op: OpLoadLocal, Operand: frameLocal})
			} else {
				code = append(code, Instr{Op: OpStoreLocal, Operand: frameLocal})
			}
			continue
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
		NumLocals:           p.NumLocals + len(plan.upvalues) + len(plan.properties),
		SelfUpvalue:         -1,
		hasSelfCall:         !trace && p.hasSelfCall,
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
