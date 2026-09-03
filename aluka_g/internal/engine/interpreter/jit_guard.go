// guard 失败与 deopt 出口：trace exit 栈恢复、各层 guard 计数与禁用、Native 结果校验、安全点轮询。

package interpreter

import (
	"fmt"
	"math"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

const jitGuardFailureLimit = 2

func (v *VM) restoreTraceExitStack(exit jit.DeoptExit) error {
	if exit.StackDepth != len(exit.StackValues) {
		return fmt.Errorf("jit: deopt exit stack has %d values, want %d", len(exit.StackValues), exit.StackDepth)
	}
	v.appendValues(exit.StackValues)
	return nil
}

// resumeTraceExit restores a trace semantic exit into the VM. Normal exits
// restore the operand stack and resume at the recorded bytecode boundary. An
// exception exit (PendingException != nil) instead discards the trace's
// operand stack — JS exception-unwinding semantics — and returns a *jsThrow
// carrying the original thrown value, which the VM main loop feeds to
// handleThrow so the existing try/catch/finally machinery consumes it. The
// exit is recorded for deopt statistics in both cases.
func (v *VM) resumeTraceExit(key quickTraceKey, exit jit.DeoptExit) (int, bool, error) {
	if exit.PendingException != nil {
		v.recordTraceDeopt(key, exit)
		return 0, false, &jsThrow{val: exit.PendingException}
	}
	if key.tmpl == nil {
		return 0, false, fmt.Errorf("jit: deopt exit has no bytecode template")
	}
	codeLen := len(key.tmpl.Code)
	if codeLen%bytecode.InstrSize != 0 || exit.ResumePC < 0 ||
		exit.ResumePC%bytecode.InstrSize != 0 || exit.ResumePC > codeLen-bytecode.InstrSize {
		return 0, false, fmt.Errorf("jit: invalid deopt resume PC %d for bytecode length %d", exit.ResumePC, codeLen)
	}
	v.recordTraceDeopt(key, exit)
	if err := v.restoreTraceExitStack(exit); err != nil {
		return 0, false, err
	}
	return exit.ResumePC, true, nil
}

func (v *VM) noteJITGuardFailure(state *quickJITState) {
	if state == nil || state.rejected {
		return
	}
	// R5-3: a guard failure on compiled code is a negative feedback signal
	// (deopt); consecutive failures cool the effective threshold.
	v.noteAdaptiveFailure()
	// R4-3: a failed execution that absorbed a beyond-baseline shape is the
	// confirmation step of polymorphic learning (the guard grew); reset the
	// failure chain so a multi-property site can absorb one shape per guard
	// without being rejected mid-learning. Unabsorbable sites (accessor,
	// Proxy, prototype, over-cap churn) never admit and still disable after
	// jitGuardFailureLimit consecutive failures.
	if state.program != nil && state.program.TakePICAbsorptions() > 0 {
		state.guardFailures = 0
	}
	if state.guardFailures < jitGuardFailureLimit {
		state.guardFailures++
	}
	if state.guardFailures >= jitGuardFailureLimit {
		state.rejected = true
		v.dropNative(state)
		if v.jitConfig.Stats {
			v.jitStats.QuickGuardDisabled++
		}
	}
}

func (v *VM) noteTraceGuardFailure(state *quickTraceState) {
	if state == nil || state.rejected {
		return
	}
	v.noteAdaptiveFailure()
	// R4-3: see noteJITGuardFailure — a trace with several property guards
	// (e.g. writeBoth writes o.a and o.b) needs one confirmation observation
	// per guard; each admission resets the chain instead of accumulating
	// toward the rejection limit.
	if state.program != nil && state.program.TakePICAbsorptions() > 0 {
		state.guardFailures = 0
	}
	if state.guardFailures < jitGuardFailureLimit {
		state.guardFailures++
	}
	if state.guardFailures >= jitGuardFailureLimit {
		state.rejected = true
		v.dropNativeTrace(state)
		if v.jitConfig.Stats {
			v.jitStats.TraceGuardDisabled++
		}
	}
}

func (v *VM) noteNativeGuardFailure(state *quickJITState) {
	if state == nil || state.nativeDisabled || state.rejected {
		return
	}
	v.noteAdaptiveFailure()
	if state.nativeGuardFailures < jitGuardFailureLimit {
		state.nativeGuardFailures++
	}
	if state.nativeGuardFailures >= jitGuardFailureLimit {
		state.nativeDisabled = true
		v.dropNative(state)
		if v.jitConfig.Stats {
			v.jitStats.NativeGuardDisabled++
		}
	}
}

func (v *VM) noteNativeTraceGuardFailure(state *quickTraceState) {
	if state == nil || state.nativeDisabled || state.rejected {
		return
	}
	v.noteAdaptiveFailure()
	if state.nativeGuardFailures < jitGuardFailureLimit {
		state.nativeGuardFailures++
	}
	if state.nativeGuardFailures >= jitGuardFailureLimit {
		state.nativeDisabled = true
		v.dropNativeTrace(state)
		if v.jitConfig.Stats {
			v.jitStats.NativeTraceGuardDisabled++
		}
	}
}

func (v *VM) noteCalleeGuardFailure(state *quickJITState) {
	if state == nil || state.calleeDisabled || state.rejected {
		return
	}
	v.noteAdaptiveFailure()
	if state.calleeGuardFailures < jitGuardFailureLimit {
		state.calleeGuardFailures++
	}
	if state.calleeGuardFailures >= jitGuardFailureLimit {
		state.calleeDisabled = true
		v.dropNative(state)
		if v.jitConfig.Stats {
			v.jitStats.CalleeGuardDisabled++
		}
	}
}

func resetJITGuardFailures(state *quickJITState) {
	if state != nil {
		state.guardFailures = 0
		state.nativeGuardFailures = 0
	}
}

func resetQuickGuardFailures(state *quickJITState) {
	if state != nil {
		state.guardFailures = 0
	}
}

func resetTraceGuardFailures(state *quickTraceState) {
	if state != nil {
		state.guardFailures = 0
		state.nativeGuardFailures = 0
	}
}

func resetQuickTraceGuardFailures(state *quickTraceState) {
	if state != nil {
		state.guardFailures = 0
	}
}

func (v *VM) verifyNativeResult(program *jit.Program, thisVal engine.Value, args []engine.Value, native engine.Value) bool {
	if !v.jitConfig.Verify {
		return true
	}
	quick, reason, err := program.Execute(thisVal, args)
	v.jitStats.VerifyChecks++
	if err != nil || reason != jit.Executed || !sameJITValue(native, quick) {
		v.jitStats.VerifyFailures++
		return false
	}
	return true
}

func sameJITValue(a, b engine.Value) bool {
	if a == nil || b == nil || a.Type() != b.Type() {
		return false
	}
	if a.Type() == engine.TypeNumber {
		af, aok := a.Float()
		bf, bok := b.Float()
		return aok && bok && (math.IsNaN(af) && math.IsNaN(bf) || math.Float64bits(af) == math.Float64bits(bf))
	}
	return a.String() == b.String()
}

func sameJITLocals(a, b []engine.Value) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameJITValue(a[i], b[i]) {
			return false
		}
	}
	return true
}

func (v *VM) pollJITSafepoint() error {
	if v.jitConfig.Stats {
		v.jitStats.SafepointPolls++
	}
	if v.oomEnabled && engine.OOMTriggered() {
		engine.ConsumeOOM()
		if v.jitConfig.Stats {
			v.jitStats.Interruptions++
		}
		return engine.OOMError()
	}
	if v.jitConfig.Safepoint != nil {
		if err := v.jitConfig.Safepoint(); err != nil {
			if v.jitConfig.Stats {
				v.jitStats.Interruptions++
			}
			return err
		}
	}
	return nil
}
