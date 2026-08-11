package jit

import (
	"fmt"
	"math"

	"github.com/aluka-lang/aluka/internal/engine"
	jitnative "github.com/aluka-lang/aluka/internal/engine/jit/native"
)

func (t *TraceProgram) CompileNative() error {
	return t.compileNative(false)
}

func (t *TraceProgram) CompileNativeForDump() error {
	return t.compileNative(true)
}

func (t *TraceProgram) compileNative(retainDebugBytes bool) error {
	if t == nil || t.program == nil {
		return fmt.Errorf("jit: nil trace program")
	}
	if t.program.nativeCode != nil {
		return nil
	}
	lowered, plan, err := lowerNativeTraceInputs(t.program)
	if err != nil {
		return err
	}
	code, err := compileNativeProgram(lowered, retainDebugBytes)
	if err != nil {
		return err
	}
	t.program.nativeCode = code
	t.program.nativePlan = plan
	return nil
}

func (t *TraceProgram) HasNative() bool {
	return t != nil && t.program != nil && t.program.nativeCode != nil
}

func (t *TraceProgram) NativeSize() int {
	if !t.HasNative() {
		return 0
	}
	return t.program.nativeCode.Size()
}

func (t *TraceProgram) NativeDisassembly() string {
	if !t.HasNative() {
		return ""
	}
	return jitnative.Disassemble(t.program.nativeCode.DebugBytes())
}

func (t *TraceProgram) HasPropertyWrites() bool {
	if t == nil || t.program == nil || t.program.nativePlan == nil {
		return false
	}
	for i := range t.program.nativePlan.properties {
		if t.program.nativePlan.properties[i].write {
			return true
		}
	}
	return false
}

func (t *TraceProgram) Close() error {
	if !t.HasNative() {
		return nil
	}
	err := t.program.nativeCode.Close()
	t.program.nativeCode = nil
	t.program.nativePlan = nil
	return err
}

// ExecuteNativeBudgetDetailed runs a native trace to a semantic exit. Budget
// yields return through Go for scheduling and then resume inside native code.
func (t *TraceProgram) ExecuteNativeBudgetDetailed(locals []engine.Value, budget uint32) (DeoptExit, ExitReason, uint64, error) {
	return t.ExecuteNativeBudgetDetailedWithSafepoint(locals, budget, nil)
}

func (t *TraceProgram) ExecuteNativeBudgetDetailedWithSafepoint(locals []engine.Value, budget uint32, poll Safepoint) (DeoptExit, ExitReason, uint64, error) {
	if !t.HasNative() || t.program.nativePlan == nil || len(locals) < t.program.NumLocals {
		return DeoptExit{}, Malformed, 0, fmt.Errorf("jit: invalid native trace locals")
	}
	if budget == 0 {
		budget = 65536
	}
	frame := &jitnative.Frame{Budget: uint64(budget)}
	plan := t.program.nativePlan
	for i := range plan.callees {
		guard := &plan.callees[i]
		if guard.sourceLocal < 0 || guard.sourceLocal >= len(locals) {
			return DeoptExit{}, GuardFailed, 0, nil
		}
		matched := locals[guard.sourceLocal] == guard.target
		if guard.method != "" {
			object, ok := locals[guard.sourceLocal].(engine.Object)
			if !ok {
				matched = false
			} else {
				method, err := object.Get(guard.method)
				matched = err == nil && method == guard.target
			}
		}
		if !matched {
			return DeoptExit{}, GuardFailed, 0, nil
		}
	}
	for slot := 0; slot < t.program.NumLocals; slot++ {
		if plan.numberLocals&(uint64(1)<<slot) == 0 {
			continue
		}
		value := locals[slot]
		if value == nil || value.Type() != engine.TypeNumber {
			return DeoptExit{}, GuardFailed, 0, nil
		}
		number, _ := value.Float()
		setNativeFrameLocal(frame, slot, t.program.NumParams, number)
	}
	propertyObjects := make([]engine.Value, len(plan.properties))
	for i := range plan.properties {
		input := &plan.properties[i]
		if input.sourceLocal < 0 || input.sourceLocal >= len(locals) {
			return DeoptExit{}, GuardFailed, 0, nil
		}
		object := locals[input.sourceLocal]
		for previous := 0; previous < i; previous++ {
			other := &plan.properties[previous]
			if (input.write || other.write) && input.sourceLocal != other.sourceLocal && input.name == other.name &&
				propertyObjects[previous] == object {
				return DeoptExit{}, GuardFailed, 0, nil
			}
		}
		number, ok := input.guard.loadNumber(object, input.name)
		if !ok {
			return DeoptExit{}, GuardFailed, 0, nil
		}
		propertyObjects[i] = object
		frame.Locals[input.frameLocal] = number
	}

	status := t.program.nativeCode.Call(frame)
	var yields uint64
	for status == 2 {
		yields++
		committed, err := t.commitNativeTraceFrame(locals, propertyObjects, frame)
		if err != nil {
			return DeoptExit{}, Malformed, yields, err
		}
		if !committed {
			return DeoptExit{ID: -1, ResumePC: t.startPC}, Yielded, yields, nil
		}
		frame.Status = 0
		if err := runSafepoint(poll); err != nil {
			return DeoptExit{ID: -1, ResumePC: t.startPC}, Interrupted, yields, err
		}
		if !t.nativeTraceInputsMatch(locals, propertyObjects, frame) {
			return DeoptExit{ID: -1, ResumePC: t.startPC}, Yielded, yields, nil
		}
		frame.Budget = uint64(budget)
		status = t.program.nativeCode.CallAt(frame.Resume, frame)
	}
	if status < 3 {
		return DeoptExit{}, Malformed, yields, fmt.Errorf("jit: native trace status %d", status)
	}
	exitID := int(status - 3)
	if exitID < 0 || exitID >= len(t.exits) {
		return DeoptExit{}, Malformed, yields, fmt.Errorf("jit: invalid native trace exit %d", exitID)
	}
	committed, err := t.commitNativeTraceFrame(locals, propertyObjects, frame)
	if err != nil {
		return DeoptExit{}, Malformed, yields, err
	}
	if !committed {
		return DeoptExit{ID: -1, ResumePC: t.startPC}, Yielded, yields, nil
	}
	exit := t.exits[exitID]
	if exit.StackDepth != 0 {
		exit.StackValues = make([]engine.Value, exit.StackDepth)
		for i := range exit.StackValues {
			exit.StackValues[i] = engine.Number(nativeFrameLocal(frame, plan.stackBase+i, t.program.NumParams))
		}
	}
	return exit, Executed, yields, nil
}

type nativePropertySnapshot struct {
	object   engine.Value
	name     string
	guard    *propertyGuard
	original float64
	expected float64
}

// ExecuteNativeBudgetVerified compares a side-effecting native trace with the
// Quick executor while keeping the externally visible property writes atomic.
func (t *TraceProgram) ExecuteNativeBudgetVerified(locals []engine.Value, budget uint32) (DeoptExit, ExitReason, uint64, bool, bool, error) {
	return t.ExecuteNativeBudgetVerifiedWithSafepoint(locals, budget, nil)
}

func (t *TraceProgram) ExecuteNativeBudgetVerifiedWithSafepoint(locals []engine.Value, budget uint32, poll Safepoint) (DeoptExit, ExitReason, uint64, bool, bool, error) {
	if !t.HasPropertyWrites() {
		exit, reason, yields, err := t.ExecuteNativeBudgetDetailedWithSafepoint(locals, budget, poll)
		return exit, reason, yields, false, true, err
	}
	originalLocals := append([]engine.Value(nil), locals...)
	snapshots, ok := t.snapshotNativePropertyWrites(locals)
	if !ok {
		return DeoptExit{}, GuardFailed, 0, false, true, nil
	}
	expectedLocals := append([]engine.Value(nil), locals...)
	verificationProgram := *t.program
	verificationProgram.propertyGuards = append([]propertyGuard(nil), t.program.propertyGuards...)
	verificationTrace := *t
	verificationTrace.program = &verificationProgram
	var expectedExit DeoptExit
	var expectedReason ExitReason
	var expectedErr error
	for {
		expectedExit, expectedReason, expectedErr = verificationTrace.ExecuteBudgetDetailedWithSafepoint(
			expectedLocals, budget, poll)
		if expectedReason != Yielded || expectedErr != nil {
			break
		}
	}
	if expectedErr == nil && expectedReason == Executed {
		ok = captureNativePropertyValues(snapshots, true)
	}
	if !restoreNativePropertyValues(snapshots, false) {
		return DeoptExit{}, Malformed, 0, false, false, fmt.Errorf("jit: native verify could not restore property snapshot")
	}
	copy(locals, originalLocals)
	if expectedErr != nil || expectedReason != Executed {
		return expectedExit, expectedReason, 0, false, true, expectedErr
	}

	nativeExit, nativeReason, yields, nativeErr := t.ExecuteNativeBudgetDetailedWithSafepoint(locals, budget, poll)
	if nativeErr != nil || nativeReason != Executed {
		return nativeExit, nativeReason, yields, false, true, nativeErr
	}
	matched := ok && expectedErr == nil && expectedReason == Executed && SameDeoptExit(expectedExit, nativeExit) &&
		sameTraceValues(locals, expectedLocals) && matchNativePropertyValues(snapshots)
	if matched {
		return nativeExit, nativeReason, yields, true, true, nil
	}
	if expectedErr == nil && expectedReason == Executed && restoreNativePropertyValues(snapshots, true) {
		copy(locals, expectedLocals)
		return expectedExit, Executed, yields, true, false, nil
	}
	if !restoreNativePropertyValues(snapshots, false) {
		return DeoptExit{}, Malformed, yields, true, false, fmt.Errorf("jit: native verify could not restore original properties")
	}
	copy(locals, originalLocals)
	return DeoptExit{}, GuardFailed, yields, true, false, nil
}

func (t *TraceProgram) snapshotNativePropertyWrites(locals []engine.Value) ([]nativePropertySnapshot, bool) {
	plan := t.program.nativePlan
	snapshots := make([]nativePropertySnapshot, 0, len(plan.properties))
	for i := range plan.properties {
		input := &plan.properties[i]
		if !input.write || input.sourceLocal < 0 || input.sourceLocal >= len(locals) {
			continue
		}
		object := locals[input.sourceLocal]
		duplicate := false
		for j := range snapshots {
			if snapshots[j].object == object && snapshots[j].name == input.name {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		number, ok := input.guard.loadNumber(object, input.name)
		if !ok {
			return nil, false
		}
		snapshots = append(snapshots, nativePropertySnapshot{
			object: object, name: input.name, guard: &input.guard, original: number,
		})
	}
	return snapshots, len(snapshots) != 0
}

func captureNativePropertyValues(snapshots []nativePropertySnapshot, expected bool) bool {
	for i := range snapshots {
		number, ok := snapshots[i].guard.loadNumber(snapshots[i].object, snapshots[i].name)
		if !ok {
			return false
		}
		if expected {
			snapshots[i].expected = number
		}
	}
	return true
}

// restoreNativePropertyValues restores snapshot property values (the verify
// path). It records the value of each property before restoring it and rolls
// back the already-restored properties if a later restore fails, so a partial
// restore is never observable.
func restoreNativePropertyValues(snapshots []nativePropertySnapshot, expected bool) bool {
	current := make([]float64, len(snapshots))
	var restored []int
	for i := range snapshots {
		value := snapshots[i].original
		if expected {
			value = snapshots[i].expected
		}
		number, ok := snapshots[i].guard.loadNumber(snapshots[i].object, snapshots[i].name)
		if !ok {
			return false
		}
		current[i] = number
		if !snapshots[i].guard.storeNumber(snapshots[i].object, snapshots[i].name, value) {
			for _, j := range restored {
				_ = snapshots[j].guard.storeNumber(snapshots[j].object, snapshots[j].name, current[j])
			}
			return false
		}
		restored = append(restored, i)
	}
	return true
}

func matchNativePropertyValues(snapshots []nativePropertySnapshot) bool {
	for i := range snapshots {
		number, ok := snapshots[i].guard.loadNumber(snapshots[i].object, snapshots[i].name)
		if !ok || math.Float64bits(number) != math.Float64bits(snapshots[i].expected) {
			return false
		}
	}
	return true
}

func sameTraceValues(a, b []engine.Value) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] == nil || b[i] == nil || a[i].Type() != b[i].Type() {
			return false
		}
		if a[i].Type() == engine.TypeNumber {
			af, _ := a[i].Float()
			bf, _ := b[i].Float()
			if math.IsNaN(af) && math.IsNaN(bf) {
				continue
			}
			if math.Float64bits(af) != math.Float64bits(bf) {
				return false
			}
			continue
		}
		if a[i].IsObject() {
			if a[i] != b[i] {
				return false
			}
			continue
		}
		if a[i].String() != b[i].String() {
			return false
		}
	}
	return true
}

// commitNativeTraceFrame applies the R1-5 two-phase commit protocol to a
// native trace frame at a semantic exit or budget yield. Phase 1 validates
// every dirty property against its guard; phase 2 stores the frame's deferred
// values, snapshots the originals first and rolls back already-stored
// properties if a store fails, so a partial commit is never observable. A
// failure returns (false, nil): the caller resumes Tier 0 at the loop header
// with the committed locals and no partial writes, exactly like a yield.
func (t *TraceProgram) commitNativeTraceFrame(locals []engine.Value, propertyObjects []engine.Value, frame *jitnative.Frame) (bool, error) {
	plan := t.program.nativePlan
	originals := make([]float64, len(plan.properties))
	var stored []int
	for i := range plan.properties {
		input := &plan.properties[i]
		if input.write && frame.Status&(uint64(1)<<input.frameLocal) != 0 {
			number, ok := input.guard.loadNumber(propertyObjects[i], input.name)
			if !ok {
				return false, nil
			}
			originals[i] = number
			if !input.guard.storeNumber(propertyObjects[i], input.name, frame.Locals[input.frameLocal]) {
				for _, j := range stored {
					rollback := &plan.properties[j]
					_ = rollback.guard.storeNumber(propertyObjects[j], rollback.name, originals[j])
				}
				return false, nil
			}
			stored = append(stored, i)
		}
	}
	for slot, written := range t.written {
		if written && frame.Status&(uint64(1)<<slot) != 0 {
			locals[slot] = engine.Number(nativeFrameLocal(frame, slot, t.program.NumParams))
		}
	}
	return true, nil
}

func (t *TraceProgram) nativeTraceInputsMatch(locals []engine.Value, propertyObjects []engine.Value, frame *jitnative.Frame) bool {
	plan := t.program.nativePlan
	for slot := 0; slot < t.program.NumLocals; slot++ {
		if plan.numberLocals&(uint64(1)<<slot) == 0 {
			continue
		}
		if locals[slot] == nil || locals[slot].Type() != engine.TypeNumber {
			return false
		}
		number, _ := locals[slot].Float()
		if math.Float64bits(number) != math.Float64bits(nativeFrameLocal(frame, slot, t.program.NumParams)) {
			return false
		}
	}
	for i := range plan.properties {
		input := &plan.properties[i]
		if input.sourceLocal < 0 || input.sourceLocal >= len(locals) || locals[input.sourceLocal] != propertyObjects[i] {
			return false
		}
		number, ok := input.guard.loadNumber(propertyObjects[i], input.name)
		if !ok || math.Float64bits(number) != math.Float64bits(frame.Locals[input.frameLocal]) {
			return false
		}
	}
	return true
}

func setNativeFrameLocal(frame *jitnative.Frame, slot, numParams int, value float64) {
	if slot >= 1 && slot <= numParams {
		frame.Args[slot-1] = value
		return
	}
	frame.Locals[slot] = value
}

func nativeFrameLocal(frame *jitnative.Frame, slot, numParams int) float64 {
	if slot >= 1 && slot <= numParams {
		return frame.Args[slot-1]
	}
	return frame.Locals[slot]
}
