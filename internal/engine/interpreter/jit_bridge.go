package interpreter

import (
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

var defaultJIT = struct {
	sync.RWMutex
	config jit.Config
}{config: jit.Config{Mode: jit.Off, Threshold: 1000}}

// SetDefaultJITConfig configures subsequently-created VMs. The CLI calls it
// before constructing contexts; existing VMs retain their own configuration.
func SetDefaultJITConfig(config jit.Config) {
	defaultJIT.Lock()
	defaultJIT.config = config.Normalized()
	defaultJIT.Unlock()
}

func defaultJITConfig() jit.Config {
	defaultJIT.RLock()
	config := defaultJIT.config
	defaultJIT.RUnlock()
	return config.Normalized()
}

type quickJITState struct {
	calls               uint32
	backedges           uint32
	program             *jit.Program
	rejected            bool
	reason              string
	nativeUsed          uint64
	callKind            quickCallKind
	callee              *vmClosure
	baseProgram         *jit.Program
	altCallee           *vmClosure
	altProgram          *jit.Program
	dumpedIR            bool
	dumpedASM           bool
	nativeCompiling     bool
	guardFailures       uint8
	nativeGuardFailures uint8
	nativeDisabled      bool
	calleeGuardFailures uint8
	calleeDisabled      bool
}

type quickCallKind uint8

const (
	quickCallUnknown quickCallKind = iota
	quickCallSelf
	quickCallBound
	quickCallUnsupported
)

type jitHotCount struct {
	calls     uint32
	backedges uint32
}

type quickTraceKey struct {
	tmpl       *bytecode.FuncTemplate
	backedgePC int
}

type quickTraceState struct {
	backedges           uint32
	program             *jit.TraceProgram
	rejected            bool
	nativeUsed          uint64
	arrayPush           *arrayPushTraceState
	closureIncrement    *closureIncrementTraceState
	guardFailures       uint8
	nativeGuardFailures uint8
	nativeDisabled      bool
	dumpedIR            bool
}

const jitGuardFailureLimit = 2

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

type closureIncrementTraceState struct {
	calleeLocal int
	indexLocal  int
	boundLocal  int
	sumLocal    int
	target      *vmClosure
	upvalue     *upvalue
	startPC     int
	exitPC      int
}

type jitRejectionKey struct {
	tier   string
	reason string
}

type jitDeoptKey struct {
	tmpl       *bytecode.FuncTemplate
	backedgePC int
	exitID     int
	resumePC   int
}

type nativeCompileResult struct {
	state      *quickJITState
	program    *jit.Program
	generation uint64
	duration   time.Duration
	err        error
}

func (v *VM) JITStats() jit.Stats {
	stats := v.jitStats
	stats.Mode = v.jitConfig.Mode
	stats.Threshold = v.jitConfig.Threshold
	stats.BackedgeThreshold = v.jitConfig.BackedgeThreshold
	stats.TraceBudget = v.jitConfig.TraceBudget
	stats.CodeCacheLimit = v.jitConfig.CodeCacheBytes
	stats.NativeCodeBytes = v.jitNativeBytes
	if len(v.jitRejections) != 0 {
		stats.RejectionReasons = make([]jit.RejectionReason, 0, len(v.jitRejections))
		for key, count := range v.jitRejections {
			stats.RejectionReasons = append(stats.RejectionReasons, jit.RejectionReason{
				Tier: key.tier, Reason: key.reason, Count: count,
			})
		}
		sort.Slice(stats.RejectionReasons, func(i, j int) bool {
			if stats.RejectionReasons[i].Tier != stats.RejectionReasons[j].Tier {
				return stats.RejectionReasons[i].Tier < stats.RejectionReasons[j].Tier
			}
			return stats.RejectionReasons[i].Reason < stats.RejectionReasons[j].Reason
		})
	}
	if len(v.jitDeopts) != 0 {
		stats.DeoptExits = make([]jit.DeoptStat, 0, len(v.jitDeopts))
		for key, count := range v.jitDeopts {
			stats.DeoptExits = append(stats.DeoptExits, jit.DeoptStat{
				Function: key.tmpl.Name, BackedgePC: key.backedgePC,
				ExitID: key.exitID, ResumePC: key.resumePC, Count: count,
			})
		}
		sort.Slice(stats.DeoptExits, func(i, j int) bool {
			a, b := stats.DeoptExits[i], stats.DeoptExits[j]
			if a.Function != b.Function {
				return a.Function < b.Function
			}
			if a.BackedgePC != b.BackedgePC {
				return a.BackedgePC < b.BackedgePC
			}
			return a.ExitID < b.ExitID
		})
	}
	return stats
}

func (v *VM) recordJITRejection(tier string, err error) {
	if !v.jitConfig.Stats || err == nil {
		return
	}
	if v.jitRejections == nil {
		v.jitRejections = make(map[jitRejectionKey]uint64)
	}
	v.jitRejections[jitRejectionKey{tier: tier, reason: err.Error()}]++
}

func (v *VM) recordTraceDeopt(key quickTraceKey, exit jit.DeoptExit) {
	if !v.jitConfig.Stats {
		return
	}
	if v.jitDeopts == nil {
		v.jitDeopts = make(map[jitDeoptKey]uint64)
	}
	v.jitDeopts[jitDeoptKey{
		tmpl: key.tmpl, backedgePC: key.backedgePC,
		exitID: exit.ID, resumePC: exit.ResumePC,
	}]++
}

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
	v.recordTraceDeopt(key, exit)
	if exit.PendingException != nil {
		return 0, false, &jsThrow{val: exit.PendingException}
	}
	if err := v.restoreTraceExitStack(exit); err != nil {
		return 0, false, err
	}
	return exit.ResumePC, true, nil
}

func (v *VM) noteJITGuardFailure(state *quickJITState) {
	if state == nil || state.rejected {
		return
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

func (v *VM) jitDumpWriter() io.Writer {
	if v.jitConfig.DumpWriter != nil {
		return v.jitConfig.DumpWriter
	}
	return os.Stderr
}

func (v *VM) dumpJITIR(state *quickJITState) {
	if state == nil || state.program == nil || state.dumpedIR || v.jitConfig.Dump != jit.DumpIR {
		return
	}
	fmt.Fprintf(v.jitDumpWriter(), "JIT dump tier=quick\n%s", state.program.DumpIR())
	state.dumpedIR = true
}

func (v *VM) dumpJITASM(state *quickJITState) {
	if state == nil || state.program == nil || state.dumpedASM || v.jitConfig.Dump != jit.DumpASM {
		return
	}
	bytes := state.program.NativeDebugBytes()
	fmt.Fprintf(v.jitDumpWriter(), "JIT dump tier=native bytes=%d\n", len(bytes))
	io.WriteString(v.jitDumpWriter(), state.program.NativeDisassembly())
	state.dumpedASM = true
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

// ConfigureJIT changes this VM's JIT policy and clears compiled state. It is
// intended for embedders and tests that need per-context control.
func (v *VM) ConfigureJIT(config jit.Config) {
	v.closeJIT()
	v.jitGeneration++
	v.jitConfig = config.Normalized()
	v.jitStates = make(map[*bytecode.FuncTemplate]*quickJITState)
	v.jitHotCounts = make(map[*bytecode.FuncTemplate]jitHotCount)
	v.jitTraces = make(map[quickTraceKey]*quickTraceState)
	v.jitStats = jit.Stats{}
	v.jitNativeBytes = 0
	v.jitNativeClock = 0
	v.jitRejections = make(map[jitRejectionKey]uint64)
	v.jitDeopts = make(map[jitDeoptKey]uint64)
	v.jitCompileDone = make(chan nativeCompileResult, 16)
	v.jitPending = 0
}

func (v *VM) closeJIT() {
	for v.jitPending > 0 {
		result := <-v.jitCompileDone
		v.jitPending--
		if result.program != nil {
			_ = result.program.Close()
		}
	}
	v.jitCompileWG.Wait()
	for _, state := range v.jitStates {
		if state != nil && state.program != nil {
			_ = state.program.Close()
		}
		if state != nil && state.altProgram != nil {
			_ = state.altProgram.Close()
		}
		if state != nil && state.baseProgram != nil {
			_ = state.baseProgram.Close()
		}
	}
	for _, state := range v.jitTraces {
		if state != nil && state.program != nil {
			_ = state.program.Close()
		}
	}
	v.jitNativeBytes = 0
	v.jitNativeClock = 0
}

func (v *VM) jitStateFor(tmpl *bytecode.FuncTemplate) *quickJITState {
	if v.jitConfig.Mode == jit.Off || tmpl == nil {
		return nil
	}
	return v.jitStates[tmpl]
}

func (v *VM) promoteJITState(tmpl *bytecode.FuncTemplate, count jitHotCount) *quickJITState {
	state := v.jitStates[tmpl]
	if state == nil {
		state = &quickJITState{calls: count.calls, backedges: count.backedges}
		v.jitStates[tmpl] = state
	}
	delete(v.jitHotCounts, tmpl)
	return state
}

func (v *VM) noteJITCall(cl *vmClosure) *quickJITState {
	if v.jitConfig.Mode == jit.Off || cl == nil || cl.tmpl == nil {
		return nil
	}
	if v.jitConfig.Stats {
		v.jitStats.Calls++
	}
	state := cl.jitState
	if cl.jitGeneration != v.jitGeneration || state == nil {
		state = v.jitStateFor(cl.tmpl)
		cl.jitState = state
		cl.jitGeneration = v.jitGeneration
	}
	if state == nil {
		count := v.jitHotCounts[cl.tmpl]
		if count.calls < ^uint32(0) {
			count.calls++
		}
		if count.calls < v.jitConfig.Threshold {
			v.jitHotCounts[cl.tmpl] = count
			return nil
		}
		state = v.promoteJITState(cl.tmpl, count)
		cl.jitState = state
	}
	if state.calls < ^uint32(0) {
		if state.calls < v.jitConfig.Threshold {
			state.calls++
		}
	}
	v.maybeCompileJITState(state, cl.tmpl, state.calls >= v.jitConfig.Threshold)
	v.maybeSpecializeCallee(state, cl)
	return state
}

func (v *VM) noteJITBackedge(tmpl *bytecode.FuncTemplate) *quickJITState {
	if v.jitConfig.Mode == jit.Off || tmpl == nil {
		return nil
	}
	if v.jitConfig.Stats {
		v.jitStats.Backedges++
	}
	state := v.jitStateFor(tmpl)
	if state == nil {
		count := v.jitHotCounts[tmpl]
		if count.backedges < ^uint32(0) {
			count.backedges++
		}
		if count.backedges < v.jitConfig.BackedgeThreshold {
			v.jitHotCounts[tmpl] = count
			return nil
		}
		state = v.promoteJITState(tmpl, count)
	}
	if state.backedges < ^uint32(0) {
		if state.backedges < v.jitConfig.BackedgeThreshold {
			state.backedges++
		}
	}
	v.maybeCompileJITState(state, tmpl, state.backedges >= v.jitConfig.BackedgeThreshold)
	return state
}

func (v *VM) maybeCompileJITState(state *quickJITState, tmpl *bytecode.FuncTemplate, hot bool) {
	if state == nil || !hot || state.program != nil || state.rejected {
		return
	}
	if v.jitConfig.Stats {
		v.jitStats.Candidates++
	}
	compileStart := time.Now()
	program, err := jit.CompileLeaf(tmpl)
	if err == nil && program.IsTrivialThisPropertyGetter() {
		err = fmt.Errorf("jit: cost model rejects trivial this-property getter")
		program = nil
	}
	if v.jitConfig.Stats {
		v.jitStats.CompileNanos += uint64(time.Since(compileStart))
	}
	if err != nil {
		v.recordJITRejection("quick", err)
		state.rejected = true
		state.reason = err.Error()
		v.jitStats.LastError = state.reason
		if v.jitConfig.Stats {
			v.jitStats.Rejected++
		}
	} else {
		state.program = program
		v.dumpJITIR(state)
		if v.jitConfig.Stats {
			v.jitStats.Compiled++
		}
		if v.jitConfig.Mode == jit.Auto {
			if _, guardedCall := program.RequiresSelf(); guardedCall {
				return
			}
			if len(program.Code) >= 128 {
				v.queueNativeCompile(state)
				return
			}
			nativeStart := time.Now()
			if err := v.installNative(state); err != nil {
				v.recordJITRejection("native", err)
				if v.jitConfig.Stats {
					v.jitStats.NativeCompileNanos += uint64(time.Since(nativeStart))
				}
				v.jitStats.LastNativeError = err.Error()
				if v.jitConfig.Stats {
					v.jitStats.NativeRejected++
				}
			} else {
				v.dumpJITASM(state)
				if v.jitConfig.Stats {
					v.jitStats.NativeCompileNanos += uint64(time.Since(nativeStart))
					v.jitStats.NativeCompiled++
				}
			}
		}
	}
}

func (v *VM) maybeSpecializeCallee(state *quickJITState, cl *vmClosure) {
	if state == nil || state.program == nil || state.callKind != quickCallUnknown || cl == nil {
		return
	}
	upvalueIndex, required := state.program.RequiresSelf()
	if !required {
		return
	}
	value, ok := closureUpvalue(cl, upvalueIndex)
	if !ok {
		state.callKind = quickCallUnsupported
		return
	}
	if value == cl {
		state.callKind = quickCallSelf
		return
	}
	target, ok := value.(*vmClosure)
	if !ok || target.tmpl == nil {
		state.callKind = quickCallUnsupported
		return
	}
	targetProgram, err := jit.CompileLeaf(target.tmpl)
	if err != nil {
		v.recordJITRejection("callee", err)
		state.callKind = quickCallUnsupported
		return
	}
	baseProgram := state.program.CloneForNative()
	inlined, err := state.program.BindCallTarget(targetProgram)
	if err != nil {
		_ = baseProgram.Close()
		v.recordJITRejection("callee", err)
		state.callKind = quickCallUnsupported
		return
	}
	state.callKind = quickCallBound
	state.callee = target
	state.baseProgram = baseProgram
	if v.jitConfig.Stats {
		v.jitStats.CalleeSpecialized++
		if inlined {
			v.jitStats.CalleeInlined++
		}
	}
	if inlined && v.jitConfig.Mode == jit.Auto {
		nativeStart := time.Now()
		if err := v.installNative(state); err != nil {
			v.recordJITRejection("native", err)
			if v.jitConfig.Stats {
				v.jitStats.NativeCompileNanos += uint64(time.Since(nativeStart))
			}
			v.jitStats.LastNativeError = err.Error()
			if v.jitConfig.Stats {
				v.jitStats.NativeRejected++
			}
		} else {
			v.dumpJITASM(state)
			if v.jitConfig.Stats {
				v.jitStats.NativeCompileNanos += uint64(time.Since(nativeStart))
				v.jitStats.NativeCompiled++
			}
		}
	}
}

func (v *VM) specializeAlternateCallee(state *quickJITState, target *vmClosure) (*jit.Program, bool) {
	if state == nil || target == nil || target.tmpl == nil || state.baseProgram == nil || state.altProgram != nil {
		return nil, false
	}
	targetProgram, err := jit.CompileLeaf(target.tmpl)
	if err != nil {
		v.recordJITRejection("callee", err)
		return nil, false
	}
	program := state.baseProgram.CloneForNative()
	inlined, err := program.BindCallTarget(targetProgram)
	if err != nil {
		v.recordJITRejection("callee", err)
		_ = program.Close()
		return nil, false
	}
	state.altCallee = target
	state.altProgram = program
	if v.jitConfig.Stats {
		v.jitStats.CalleeSpecialized++
		v.jitStats.CalleePICAdds++
		if inlined {
			v.jitStats.CalleeInlined++
		}
	}
	if inlined && v.jitConfig.Mode == jit.Auto {
		nativeStart := time.Now()
		if v.jitConfig.Dump == jit.DumpASM {
			err = program.CompileNativeForDump()
		} else {
			err = program.CompileNative()
		}
		if v.jitConfig.Stats {
			v.jitStats.NativeCompileNanos += uint64(time.Since(nativeStart))
		}
		if err == nil {
			err = v.reserveNative(state, uint64(program.NativeSize()))
		}
		if err != nil {
			_ = program.Close()
			v.recordJITRejection("native-callee-pic", err)
			v.jitStats.LastNativeError = err.Error()
			if v.jitConfig.Stats {
				v.jitStats.NativeRejected++
			}
		} else {
			if v.jitConfig.Dump == jit.DumpASM {
				fmt.Fprintf(v.jitDumpWriter(), "JIT dump tier=native-callee-pic version=2 bytes=%d\n", program.NativeSize())
				io.WriteString(v.jitDumpWriter(), program.NativeDisassembly())
			}
			if v.jitConfig.Stats {
				v.jitStats.NativeCompiled++
			}
		}
	}
	return program, true
}

func closureUpvalue(cl *vmClosure, index int) (engine.Value, bool) {
	if cl == nil || index < 0 || index >= len(cl.upvalues) {
		return nil, false
	}
	uv := cl.upvalues[index]
	if uv == nil {
		return nil, false
	}
	if uv.slot != nil {
		return *uv.slot, true
	}
	return uv.closed, true
}

func storeClosureUpvalue(cl *vmClosure, index int, value engine.Value) bool {
	if cl == nil || index < 0 || index >= len(cl.upvalues) {
		return false
	}
	uv := cl.upvalues[index]
	if uv == nil {
		return false
	}
	if uv.slot != nil {
		*uv.slot = value
	} else {
		uv.closed = value
	}
	return true
}

func (v *VM) queueNativeCompile(state *quickJITState) {
	if state == nil || state.program == nil || state.nativeCompiling || state.nativeDisabled || state.program.HasNative() {
		return
	}
	program := state.program.CloneForNative()
	if program == nil {
		return
	}
	state.nativeCompiling = true
	v.jitPending++
	if v.jitConfig.Stats {
		v.jitStats.BackgroundQueued++
	}
	generation := v.jitGeneration
	retainDump := v.jitConfig.Dump == jit.DumpASM
	done := v.jitCompileDone
	v.jitCompileWG.Add(1)
	go func() {
		start := time.Now()
		var err error
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("jit: background native compile panic: %v", recovered)
			}
			done <- nativeCompileResult{
				state: state, program: program, generation: generation,
				duration: time.Since(start), err: err,
			}
			v.jitCompileWG.Done()
		}()
		if retainDump {
			err = program.CompileNativeForDump()
		} else {
			err = program.CompileNative()
		}
	}()
}

func (v *VM) pollNativeCompiles() {
	for v.jitPending > 0 {
		select {
		case result := <-v.jitCompileDone:
			v.jitPending--
			if result.state != nil {
				result.state.nativeCompiling = false
			}
			if result.generation != v.jitGeneration || result.state == nil || result.state.rejected || result.state.program == nil {
				if result.program != nil {
					_ = result.program.Close()
				}
				if v.jitConfig.Stats {
					v.jitStats.BackgroundDiscarded++
				}
				continue
			}
			if v.jitConfig.Stats {
				v.jitStats.NativeCompileNanos += uint64(result.duration)
				v.jitStats.BackgroundCompleted++
			}
			if result.err != nil {
				v.recordJITRejection("native", result.err)
				v.jitStats.LastNativeError = result.err.Error()
				if v.jitConfig.Stats {
					v.jitStats.NativeRejected++
				}
				_ = result.program.Close()
				continue
			}
			if err := v.adoptNative(result.state, result.program); err != nil {
				v.recordJITRejection("native", err)
				v.jitStats.LastNativeError = err.Error()
				if v.jitConfig.Stats {
					v.jitStats.NativeRejected++
				}
				_ = result.program.Close()
				continue
			}
			v.dumpJITASM(result.state)
			if v.jitConfig.Stats {
				v.jitStats.NativeCompiled++
			}
		default:
			return
		}
	}
}

func (v *VM) installNative(state *quickJITState) error {
	if state == nil || state.program == nil {
		return fmt.Errorf("jit: missing native candidate")
	}
	var err error
	if v.jitConfig.Dump == jit.DumpASM {
		err = state.program.CompileNativeForDump()
	} else {
		err = state.program.CompileNative()
	}
	if err != nil {
		return err
	}
	size := uint64(state.program.NativeSize())
	if err := v.reserveNative(state, size); err != nil {
		_ = state.program.Close()
		return err
	}
	return nil
}

func (v *VM) adoptNative(state *quickJITState, program *jit.Program) error {
	if state == nil || state.program == nil || program == nil {
		return fmt.Errorf("jit: missing prepared native candidate")
	}
	size := uint64(program.NativeSize())
	if err := v.reserveNative(state, size); err != nil {
		return err
	}
	if err := state.program.AdoptNativeFrom(program); err != nil {
		if size <= v.jitNativeBytes {
			v.jitNativeBytes -= size
		}
		state.nativeUsed = 0
		return err
	}
	return nil
}

func (v *VM) reserveNative(state *quickJITState, size uint64) error {
	if err := v.reserveNativeCode(size, state, nil); err != nil {
		return err
	}
	v.touchNative(state)
	return nil
}

func (v *VM) reserveNativeTrace(state *quickTraceState, size uint64) error {
	if err := v.reserveNativeCode(size, nil, state); err != nil {
		return err
	}
	v.touchNativeTrace(state)
	return nil
}

func (v *VM) reserveNativeCode(size uint64, excludedState *quickJITState, excludedTrace *quickTraceState) error {
	limit := v.jitConfig.CodeCacheBytes
	if size == 0 || size > limit {
		return fmt.Errorf("jit: native code size %d exceeds cache budget %d", size, limit)
	}
	for v.jitNativeBytes+size > limit {
		var victimState *quickJITState
		var victimTrace *quickTraceState
		victimUsed := ^uint64(0)
		for _, candidate := range v.jitStates {
			if candidate == excludedState || !jitStateHasNative(candidate) {
				continue
			}
			if candidate.nativeUsed < victimUsed {
				victimState, victimTrace, victimUsed = candidate, nil, candidate.nativeUsed
			}
		}
		for _, candidate := range v.jitTraces {
			if candidate == excludedTrace || candidate == nil || candidate.program == nil || !candidate.program.HasNative() {
				continue
			}
			if candidate.nativeUsed < victimUsed {
				victimState, victimTrace, victimUsed = nil, candidate, candidate.nativeUsed
			}
		}
		if victimState == nil && victimTrace == nil {
			return fmt.Errorf("jit: native code cache cannot free %d bytes", size)
		}
		if victimState != nil {
			v.dropNative(victimState)
		} else {
			v.dropNativeTrace(victimTrace)
		}
		if v.jitConfig.Stats {
			v.jitStats.NativeEvictions++
		}
	}
	v.jitNativeBytes += size
	return nil
}

func (v *VM) touchNative(state *quickJITState) {
	v.jitNativeClock++
	if v.jitNativeClock == 0 {
		v.jitNativeClock = 1
	}
	state.nativeUsed = v.jitNativeClock
}

func (v *VM) touchNativeTrace(state *quickTraceState) {
	v.jitNativeClock++
	if v.jitNativeClock == 0 {
		v.jitNativeClock = 1
	}
	state.nativeUsed = v.jitNativeClock
}

func (v *VM) dropNative(state *quickJITState) {
	if !jitStateHasNative(state) {
		return
	}
	size := uint64(0)
	if state.program != nil && state.program.HasNative() {
		size += uint64(state.program.NativeSize())
		_ = state.program.Close()
	}
	if state.altProgram != nil && state.altProgram.HasNative() {
		size += uint64(state.altProgram.NativeSize())
		_ = state.altProgram.Close()
	}
	if size <= v.jitNativeBytes {
		v.jitNativeBytes -= size
	} else {
		v.jitNativeBytes = 0
	}
	state.nativeUsed = 0
}

func jitStateHasNative(state *quickJITState) bool {
	return state != nil && (state.program != nil && state.program.HasNative() ||
		state.altProgram != nil && state.altProgram.HasNative())
}

func (v *VM) dropNativeTrace(state *quickTraceState) {
	if state == nil || state.program == nil || !state.program.HasNative() {
		return
	}
	size := uint64(state.program.NativeSize())
	_ = state.program.Close()
	if size <= v.jitNativeBytes {
		v.jitNativeBytes -= size
	} else {
		v.jitNativeBytes = 0
	}
	state.nativeUsed = 0
}

func (v *VM) tryQuickFrame(frame *vmFrame) (engine.Value, bool, error) {
	if v.insnsEnabled {
		return engine.Undefined(), false, nil
	}
	if v.jitPending > 0 {
		v.pollNativeCompiles()
	}
	state := frame.jitState
	if frame.jitGeneration != v.jitGeneration {
		state = v.jitStateFor(frame.tmpl)
		frame.jitState = state
		frame.jitGeneration = v.jitGeneration
	}
	if state != nil && state.rejected {
		return engine.Undefined(), false, nil
	}
	if state == nil || state.program == nil {
		state = v.noteJITBackedge(frame.tmpl)
		frame.jitState = state
		frame.jitGeneration = v.jitGeneration
	}
	if state == nil || state.program == nil || state.rejected {
		return engine.Undefined(), false, nil
	}
	if _, recursive := state.program.RequiresSelf(); recursive {
		return engine.Undefined(), false, nil
	}
	base := frame.base
	end := base + 1 + frame.tmpl.NumParams
	if base < 0 || end > len(v.stack) {
		return engine.Undefined(), false, nil
	}
	if v.jitConfig.Mode == jit.Auto && state.program.HasNative() {
		v.touchNative(state)
		result, reason, yields, err := state.program.ExecuteNativeBudgetWithSafepoint(
			v.stack[base], v.stack[base+1:end], v.jitConfig.TraceBudget, v.pollJITSafepoint)
		if v.jitConfig.Stats {
			v.jitStats.NativeYields += yields
		}
		if err != nil && reason == jit.Interrupted {
			return engine.Undefined(), false, err
		} else if err != nil {
			v.dropNative(state)
			if v.jitConfig.Stats {
				v.jitStats.Errors++
			}
		} else if reason == jit.Executed {
			if !v.verifyNativeResult(state.program, v.stack[base], v.stack[base+1:end], result) {
				v.dropNative(state)
				state.rejected = true
				return engine.Undefined(), false, nil
			}
			if v.jitConfig.Stats {
				v.jitStats.NativeExecuted++
				if state.callKind == quickCallBound {
					v.jitStats.CalleeExecuted++
				}
			}
			resetJITGuardFailures(state)
			return result, true, nil
		} else if reason == jit.GuardFailed {
			v.noteNativeGuardFailure(state)
			if v.jitConfig.Stats {
				v.jitStats.GuardFailures++
			}
		}
	}
	result, reason, err := state.program.ExecuteWithSafepoint(
		v.stack[base], v.stack[base+1:end], v.jitConfig.TraceBudget, v.pollJITSafepoint)
	if err != nil {
		if reason == jit.Interrupted {
			return engine.Undefined(), false, err
		}
		state.rejected = true
		state.program = nil
		if v.jitConfig.Stats {
			v.jitStats.Errors++
		}
		return engine.Undefined(), false, nil
	}
	if reason == jit.Executed {
		resetQuickGuardFailures(state)
		if v.jitConfig.Stats {
			v.jitStats.Executed++
		}
		return result, true, nil
	}
	if reason == jit.GuardFailed {
		v.noteJITGuardFailure(state)
		if v.jitConfig.Stats {
			v.jitStats.GuardFailures++
		}
	}
	return engine.Undefined(), false, nil
}

func (v *VM) traceNoopCallGuards(frame *vmFrame, startPC, backedgePC int) []jit.TraceCallGuard {
	if frame == nil || frame.tmpl == nil || frame.base < 0 {
		return nil
	}
	code := frame.tmpl.Code
	var guards []jit.TraceCallGuard
	for pc := startPC; pc <= backedgePC; pc += bytecode.InstrSize {
		if bytecode.Opcode(code[pc]) != bytecode.OpCall || pc-bytecode.InstrSize < startPC ||
			pc+bytecode.InstrSize > backedgePC {
			continue
		}
		arg := uint32(code[pc+1])<<16 | uint32(code[pc+2])<<8 | uint32(code[pc+3])
		if arg != 0 || bytecode.Opcode(code[pc-bytecode.InstrSize]) != bytecode.OpLoadLocal ||
			bytecode.Opcode(code[pc+bytecode.InstrSize]) != bytecode.OpPop {
			continue
		}
		loadPC := pc - bytecode.InstrSize
		slot := int(uint32(code[loadPC+1])<<16 | uint32(code[loadPC+2])<<8 | uint32(code[loadPC+3]))
		stackIndex := frame.base + slot
		if slot < 0 || slot >= frame.tmpl.NumLocals || stackIndex < 0 || stackIndex >= len(v.stack) {
			continue
		}
		target, ok := v.stack[stackIndex].(*vmClosure)
		if !ok || target.tmpl == nil {
			continue
		}
		program, err := jit.CompileLeaf(target.tmpl)
		if err != nil || !program.ReturnsUndefined() {
			continue
		}
		guards = append(guards, jit.TraceCallGuard{PC: pc, SourceLocal: slot, Target: target})
	}
	return guards
}

func (v *VM) traceMethodGuards(frame *vmFrame, startPC, backedgePC int) []jit.TraceMethodGuard {
	if frame == nil || frame.tmpl == nil || frame.base < 0 {
		return nil
	}
	code := frame.tmpl.Code
	var guards []jit.TraceMethodGuard
	for pc := startPC; pc <= backedgePC; pc += bytecode.InstrSize {
		if bytecode.Opcode(code[pc]) != bytecode.OpCallMethod || pc-bytecode.InstrSize < startPC {
			continue
		}
		arg := uint32(code[pc+1])<<16 | uint32(code[pc+2])<<8 | uint32(code[pc+3])
		if arg>>16 != 0 {
			continue
		}
		nameIndex := int(arg & 0xFFFF)
		if nameIndex < 0 || nameIndex >= len(frame.tmpl.Constants) || frame.tmpl.Constants[nameIndex].Type() != engine.TypeString ||
			bytecode.Opcode(code[pc-bytecode.InstrSize]) != bytecode.OpLoadLocal {
			continue
		}
		loadPC := pc - bytecode.InstrSize
		slot := int(uint32(code[loadPC+1])<<16 | uint32(code[loadPC+2])<<8 | uint32(code[loadPC+3]))
		stackIndex := frame.base + slot
		if slot < 0 || slot >= frame.tmpl.NumLocals || stackIndex < 0 || stackIndex >= len(v.stack) {
			continue
		}
		receiver := v.stack[stackIndex]
		methodValue, ok := engine.OwnDataProperty(receiver, frame.tmpl.Constants[nameIndex].String())
		if !ok {
			continue
		}
		target, ok := methodValue.(*vmClosure)
		if !ok || target.tmpl == nil {
			continue
		}
		program, err := jit.CompileLeaf(target.tmpl)
		if err != nil || !program.IsTrivialThisPropertyGetter() {
			continue
		}
		property := program.Code[1].Name
		guards = append(guards, jit.TraceMethodGuard{
			PC: pc, SourceLocal: slot, Target: target,
			Method: frame.tmpl.Constants[nameIndex].String(), Property: property,
		})
	}
	return guards
}

func jitTraceOperand(code []byte, pc int) uint32 {
	return uint32(code[pc+1])<<16 | uint32(code[pc+2])<<8 | uint32(code[pc+3])
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

func matchIncrementUpvalueClosure(target *vmClosure) (*upvalue, bool) {
	if target == nil || target.tmpl == nil || target.tmpl.IsAsync || target.tmpl.IsGenerator ||
		target.tmpl.IsVarArgs || target.tmpl.NumParams != 0 || len(target.tmpl.Upvalues) != 1 ||
		len(target.upvalues) != 1 || target.upvalues[0] == nil {
		return nil, false
	}
	code := target.tmpl.Code
	const instructionCount = 5
	if len(code) != instructionCount*bytecode.InstrSize &&
		(len(code) != (instructionCount+1)*bytecode.InstrSize ||
			bytecode.Opcode(code[instructionCount*bytecode.InstrSize]) != bytecode.OpReturnUndef) {
		return nil, false
	}
	op := func(index int) bytecode.Opcode { return bytecode.Opcode(code[index*bytecode.InstrSize]) }
	arg := func(index int) uint32 { return jitTraceOperand(code, index*bytecode.InstrSize) }
	if op(0) != bytecode.OpLoadUpvalue || arg(0) != 0 ||
		op(1) != bytecode.OpInc || op(2) != bytecode.OpDup ||
		op(3) != bytecode.OpStoreUpvalue || arg(3) != 0 || op(4) != bytecode.OpReturn {
		return nil, false
	}
	return target.upvalues[0], true
}

func closureTraceUpvalueAliased(trace *closureIncrementTraceState, locals []engine.Value) bool {
	if trace == nil || trace.upvalue == nil || trace.upvalue.slot == nil {
		return false
	}
	for _, slot := range []int{trace.calleeLocal, trace.indexLocal, trace.boundLocal, trace.sumLocal} {
		if slot >= 0 && slot < len(locals) && trace.upvalue.slot == &locals[slot] {
			return true
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
	uv, ok := matchIncrementUpvalueClosure(target)
	if !ok {
		return nil
	}
	trace := &closureIncrementTraceState{
		calleeLocal: calleeLocal, indexLocal: indexLocal, boundLocal: boundLocal,
		sumLocal: sumLocal, target: target, upvalue: uv, startPC: startPC, exitPC: exitPC,
	}
	if closureTraceUpvalueAliased(trace, locals) {
		return nil
	}
	if _, _, _, _, ok := trace.closureLoopNumbers(locals); !ok {
		return nil
	}
	return trace
}

func (t *closureIncrementTraceState) closureLoopNumbers(locals []engine.Value) (float64, float64, float64, float64, bool) {
	if t == nil || t.target == nil || t.upvalue == nil || t.indexLocal < 0 || t.indexLocal >= len(locals) ||
		t.boundLocal < 0 || t.boundLocal >= len(locals) || t.sumLocal < 0 || t.sumLocal >= len(locals) {
		return 0, 0, 0, 0, false
	}
	index, indexOK := locals[t.indexLocal].Float()
	bound, boundOK := locals[t.boundLocal].Float()
	sum, sumOK := locals[t.sumLocal].Float()
	upvalueValue, upvalueOK := closureUpvalue(t.target, 0)
	if !indexOK || !boundOK || !sumOK || !upvalueOK || upvalueValue == nil || upvalueValue.Type() != engine.TypeNumber {
		return 0, 0, 0, 0, false
	}
	current, _ := upvalueValue.Float()
	const maxSafeInteger = float64(1<<53 - 1)
	if math.IsNaN(index) || math.IsInf(index, 0) || math.Trunc(index) != index || index < 0 || index > maxSafeInteger ||
		math.IsNaN(bound) || math.IsInf(bound, 0) || math.Trunc(bound) != bound || bound < 0 || bound > maxSafeInteger ||
		math.IsNaN(sum) || math.IsInf(sum, 0) || math.IsNaN(current) || math.IsInf(current, 0) {
		return 0, 0, 0, 0, false
	}
	return index, bound, sum, current, true
}

func (v *VM) executeClosureIncrementTrace(trace *closureIncrementTraceState, locals []engine.Value) (int, jit.ExitReason, error) {
	if trace == nil || trace.calleeLocal < 0 || trace.calleeLocal >= len(locals) ||
		locals[trace.calleeLocal] != trace.target || len(trace.target.upvalues) != 1 ||
		trace.target.upvalues[0] != trace.upvalue || closureTraceUpvalueAliased(trace, locals) {
		return 0, jit.GuardFailed, nil
	}
	index, bound, sum, current, ok := trace.closureLoopNumbers(locals)
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
		current++
		sum += current
	}
	if !storeClosureUpvalue(trace.target, 0, engine.Number(current)) {
		return 0, jit.GuardFailed, nil
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

func (v *VM) tryQuickTrace(frame *vmFrame, startPC, backedgePC int) (int, bool, error) {
	if v.insnsEnabled || frame == nil || frame.tmpl == nil || frame.jitTraceFailed {
		return 0, false, nil
	}
	key := quickTraceKey{tmpl: frame.tmpl, backedgePC: backedgePC}
	state := frame.jitTrace
	if frame.jitTraceGeneration != v.jitGeneration || frame.jitTracePC != backedgePC {
		state = v.jitTraces[key]
		if state == nil {
			state = &quickTraceState{}
			v.jitTraces[key] = state
		}
		frame.jitTrace = state
		frame.jitTracePC = backedgePC
		frame.jitTraceGeneration = v.jitGeneration
	}
	if state.rejected {
		return 0, false, nil
	}
	if state.program == nil && state.arrayPush == nil && state.closureIncrement == nil {
		if state.backedges < ^uint32(0) {
			state.backedges++
		}
		if state.backedges < v.jitConfig.BackedgeThreshold {
			return 0, false, nil
		}
		compileStart := time.Now()
		if arrayPush := v.matchArrayPushTrace(frame, startPC, backedgePC); arrayPush != nil {
			state.arrayPush = arrayPush
			if v.jitConfig.Stats {
				v.jitStats.TraceCompileNanos += uint64(time.Since(compileStart))
				v.jitStats.TracesCompiled++
				v.jitStats.ArrayPushSites++
			}
		} else if closureIncrement := v.matchClosureIncrementTrace(frame, startPC, backedgePC); closureIncrement != nil {
			state.closureIncrement = closureIncrement
			if v.jitConfig.Stats {
				v.jitStats.TraceCompileNanos += uint64(time.Since(compileStart))
				v.jitStats.TracesCompiled++
				v.jitStats.ClosureUpvalueSites++
			}
		} else {
			program, err := jit.CompileTraceWithGuards(
				frame.tmpl, startPC, backedgePC,
				v.traceNoopCallGuards(frame, startPC, backedgePC),
				v.traceMethodGuards(frame, startPC, backedgePC))
			if v.jitConfig.Stats {
				v.jitStats.TraceCompileNanos += uint64(time.Since(compileStart))
			}
			if err != nil {
				v.recordJITRejection("trace", err)
				state.rejected = true
				v.jitStats.LastError = err.Error()
				if v.jitConfig.Stats {
					v.jitStats.TracesRejected++
				}
				return 0, false, nil
			}
			state.program = program
			if v.jitConfig.Stats {
				v.jitStats.TracesCompiled++
				v.jitStats.NoopCallSites += uint64(program.GuardedNoopCalls())
				v.jitStats.MethodCallSites += uint64(program.GuardedMethodCalls())
			}
			if v.jitConfig.Dump == jit.DumpIR && !state.dumpedIR {
				fmt.Fprintf(v.jitDumpWriter(), "JIT dump tier=trace\n%s", program.DumpIR())
				state.dumpedIR = true
			}
			if v.jitConfig.Mode == jit.Auto {
				nativeStart := time.Now()
				if v.jitConfig.Dump == jit.DumpASM {
					err = program.CompileNativeForDump()
				} else {
					err = program.CompileNative()
				}
				if v.jitConfig.Stats {
					v.jitStats.NativeTraceCompileNanos += uint64(time.Since(nativeStart))
				}
				if err == nil {
					err = v.reserveNativeTrace(state, uint64(program.NativeSize()))
				}
				if err != nil {
					_ = program.Close()
					v.recordJITRejection("native-trace", err)
					v.jitStats.LastNativeError = err.Error()
					if v.jitConfig.Stats {
						v.jitStats.NativeTracesRejected++
					}
				} else {
					if v.jitConfig.Dump == jit.DumpASM {
						fmt.Fprintf(v.jitDumpWriter(), "JIT dump tier=native-trace bytes=%d\n", program.NativeSize())
						io.WriteString(v.jitDumpWriter(), program.NativeDisassembly())
					}
					if v.jitConfig.Stats {
						v.jitStats.NativeTracesCompiled++
					}
				}
			}
		}
	}

	base := frame.base
	end := base + frame.tmpl.NumLocals
	if base < 0 || end != len(v.stack) {
		return 0, false, nil
	}
	locals := v.stack[base:end]
	if state.arrayPush != nil {
		exitPC, reason, err := v.executeArrayPushTrace(state.arrayPush, locals)
		if err != nil {
			return 0, false, err
		}
		switch reason {
		case jit.Executed:
			resetQuickTraceGuardFailures(state)
			if v.jitConfig.Stats {
				v.jitStats.TracesExecuted++
			}
			return exitPC, true, nil
		case jit.Yielded:
			resetQuickTraceGuardFailures(state)
			if v.jitConfig.Stats {
				v.jitStats.TraceYields++
				v.jitStats.ArrayPushYields++
			}
			return exitPC, true, nil
		case jit.GuardFailed:
			frame.jitTraceFailed = true
			v.noteTraceGuardFailure(state)
			if v.jitConfig.Stats {
				v.jitStats.GuardFailures++
			}
		}
		return 0, false, nil
	}
	if state.closureIncrement != nil {
		exitPC, reason, err := v.executeClosureIncrementTrace(state.closureIncrement, locals)
		if err != nil {
			return 0, false, err
		}
		switch reason {
		case jit.Executed:
			resetQuickTraceGuardFailures(state)
			if v.jitConfig.Stats {
				v.jitStats.TracesExecuted++
			}
			return exitPC, true, nil
		case jit.Yielded:
			resetQuickTraceGuardFailures(state)
			if v.jitConfig.Stats {
				v.jitStats.TraceYields++
				v.jitStats.ClosureUpvalueYields++
			}
			return exitPC, true, nil
		case jit.GuardFailed:
			frame.jitTraceFailed = true
			v.noteTraceGuardFailure(state)
			if v.jitConfig.Stats {
				v.jitStats.GuardFailures++
			}
		}
		return 0, false, nil
	}
	if v.jitConfig.Mode == jit.Auto && state.program.HasNative() {
		var expectedLocals []engine.Value
		var expectedExit jit.DeoptExit
		var expectedReason jit.ExitReason
		var expectedErr error
		verifyReadOnly := v.jitConfig.Verify && !state.program.HasPropertyWrites()
		if verifyReadOnly {
			expectedLocals = append([]engine.Value(nil), locals...)
			expectedExit, expectedReason, expectedErr = state.program.ExecuteBudgetDetailed(expectedLocals, 0)
		}
		v.touchNativeTrace(state)
		var exit jit.DeoptExit
		var reason jit.ExitReason
		var yields uint64
		var verifyChecked bool
		var verifyMatched = true
		var nativeErr error
		if v.jitConfig.Verify && state.program.HasPropertyWrites() {
			exit, reason, yields, verifyChecked, verifyMatched, nativeErr =
				state.program.ExecuteNativeBudgetVerifiedWithSafepoint(
					locals, v.jitConfig.TraceBudget, v.pollJITSafepoint)
		} else {
			exit, reason, yields, nativeErr = state.program.ExecuteNativeBudgetDetailedWithSafepoint(
				locals, v.jitConfig.TraceBudget, v.pollJITSafepoint)
		}
		if v.jitConfig.Stats {
			v.jitStats.NativeTraceYields += yields
		}
		if verifyChecked {
			v.jitStats.VerifyChecks++
			if !verifyMatched {
				v.jitStats.VerifyFailures++
				v.dropNativeTrace(state)
				if nativeErr == nil && reason == jit.Executed {
					return v.resumeTraceExit(key, exit)
				}
			}
		}
		if nativeErr != nil && reason == jit.Interrupted {
			return 0, false, nativeErr
		} else if nativeErr != nil {
			v.dropNativeTrace(state)
			v.jitStats.LastNativeError = nativeErr.Error()
			if v.jitConfig.Stats {
				v.jitStats.Errors++
			}
		} else if reason == jit.Executed {
			if verifyReadOnly {
				v.jitStats.VerifyChecks++
				if expectedErr != nil || expectedReason != jit.Executed || !jit.SameDeoptExit(expectedExit, exit) ||
					!sameJITLocals(locals, expectedLocals) {
					v.jitStats.VerifyFailures++
					v.dropNativeTrace(state)
					copy(locals, expectedLocals)
					if expectedErr == nil && expectedReason == jit.Executed {
						return v.resumeTraceExit(key, expectedExit)
					}
					return 0, false, nil
				}
			}
			if v.jitConfig.Stats {
				v.jitStats.NativeTracesExecuted++
			}
			resetTraceGuardFailures(state)
			return v.resumeTraceExit(key, exit)
		} else if reason == jit.Yielded {
			resetTraceGuardFailures(state)
			return exit.ResumePC, true, nil
		} else if reason == jit.GuardFailed {
			v.noteNativeTraceGuardFailure(state)
			if v.jitConfig.Stats {
				v.jitStats.GuardFailures++
			}
		}
	}
	exit, reason, err := state.program.ExecuteBudgetDetailedWithSafepoint(
		locals, v.jitConfig.TraceBudget, v.pollJITSafepoint)
	if err != nil {
		if reason == jit.Interrupted {
			return 0, false, err
		}
		state.rejected = true
		state.program = nil
		v.jitStats.LastError = err.Error()
		if v.jitConfig.Stats {
			v.jitStats.Errors++
		}
		return 0, false, nil
	}
	switch reason {
	case jit.Executed:
		resetQuickTraceGuardFailures(state)
		if v.jitConfig.Stats {
			v.jitStats.TracesExecuted++
		}
		return v.resumeTraceExit(key, exit)
	case jit.Yielded:
		resetQuickTraceGuardFailures(state)
		if v.jitConfig.Stats {
			v.jitStats.TraceYields++
		}
		return exit.ResumePC, true, nil
	case jit.GuardFailed:
		frame.jitTraceFailed = true
		v.noteTraceGuardFailure(state)
		if v.jitConfig.Stats {
			v.jitStats.GuardFailures++
		}
	}
	return 0, false, nil
}

func (v *VM) tryQuickCall(cl *vmClosure, thisVal engine.Value, args []engine.Value) (engine.Value, bool, error) {
	if v.insnsEnabled {
		return engine.Undefined(), false, nil
	}
	if v.jitPending > 0 {
		v.pollNativeCompiles()
	}
	var state *quickJITState
	if cl != nil && cl.jitGeneration == v.jitGeneration {
		state = cl.jitState
	}
	profile := state == nil || state.program == nil
	if !profile {
		if _, requiresCallee := state.program.RequiresSelf(); requiresCallee && state.callKind == quickCallUnknown {
			profile = true
		}
	}
	if profile {
		state = v.noteJITCall(cl)
	} else if v.jitConfig.Stats {
		v.jitStats.Calls++
	}
	if state == nil || state.program == nil {
		return engine.Undefined(), false, nil
	}
	program := state.program
	if upvalueIndex, required := program.RequiresSelf(); required {
		if state.calleeDisabled {
			return engine.Undefined(), false, nil
		}
		// Recursive quick execution collapses many JS calls into one Go entry.
		// Keep --monitor call/instruction counters exact by retaining Tier 0.
		if v.callCountEnabled || v.insnsEnabled {
			return engine.Undefined(), false, nil
		}
		if state.callKind == quickCallUnsupported || state.callKind == quickCallUnknown {
			if v.jitConfig.Stats {
				v.jitStats.GuardFailures++
			}
			return engine.Undefined(), false, nil
		}
		value, ok := closureUpvalue(cl, upvalueIndex)
		matched := false
		if ok && state.callKind == quickCallSelf {
			matched = value == cl
		} else if ok && state.callKind == quickCallBound {
			switch value {
			case engine.Value(state.callee):
				matched = true
			case engine.Value(state.altCallee):
				if state.altCallee != nil && state.altProgram != nil {
					program = state.altProgram
					matched = true
					if v.jitConfig.Stats {
						v.jitStats.CalleePICHits++
					}
				}
			default:
				if target, isClosure := value.(*vmClosure); isClosure && state.altProgram == nil {
					program, matched = v.specializeAlternateCallee(state, target)
				}
			}
		}
		if !matched {
			v.noteCalleeGuardFailure(state)
			if v.jitConfig.Stats {
				v.jitStats.GuardFailures++
				if state.callKind == quickCallBound {
					v.jitStats.CalleeGuardFailures++
				}
			}
			return engine.Undefined(), false, nil
		}
		state.calleeGuardFailures = 0
	}
	if program.ReturnsUndefined() {
		if v.jitConfig.Stats {
			v.jitStats.Executed++
		}
		return engine.Undefined(), true, nil
	}
	if v.jitConfig.Mode == jit.Auto && program.HasNative() {
		v.touchNative(state)
		result, reason, yields, err := program.ExecuteNativeBudgetWithSafepoint(
			thisVal, args, v.jitConfig.TraceBudget, v.pollJITSafepoint)
		if v.jitConfig.Stats {
			v.jitStats.NativeYields += yields
		}
		if err != nil && reason == jit.Interrupted {
			return engine.Undefined(), false, err
		} else if err != nil {
			v.dropNative(state)
			if v.jitConfig.Stats {
				v.jitStats.Errors++
			}
		} else if reason == jit.Executed {
			if !v.verifyNativeResult(program, thisVal, args, result) {
				v.dropNative(state)
				state.rejected = true
				return engine.Undefined(), false, nil
			}
			if v.jitConfig.Stats {
				v.jitStats.NativeExecuted++
				if state.callKind == quickCallBound {
					v.jitStats.CalleeExecuted++
				}
			}
			resetJITGuardFailures(state)
			return result, true, nil
		} else if reason == jit.GuardFailed {
			v.noteNativeGuardFailure(state)
			if v.jitConfig.Stats {
				v.jitStats.GuardFailures++
			}
		}
	}
	result, reason, err := program.ExecuteWithSafepoint(
		thisVal, args, v.jitConfig.TraceBudget, v.pollJITSafepoint)
	if err != nil {
		if reason == jit.Interrupted {
			return engine.Undefined(), false, err
		}
		if v.jitConfig.Stats {
			v.jitStats.Errors++
		}
		state.rejected = true
		return engine.Undefined(), false, nil
	}
	switch reason {
	case jit.Executed:
		resetQuickGuardFailures(state)
		if v.jitConfig.Stats {
			v.jitStats.Executed++
			if state.callKind == quickCallBound {
				v.jitStats.CalleeExecuted++
			}
		}
		return result, true, nil
	case jit.GuardFailed:
		v.noteJITGuardFailure(state)
		if v.jitConfig.Stats {
			v.jitStats.GuardFailures++
		}
	}
	return engine.Undefined(), false, nil
}
