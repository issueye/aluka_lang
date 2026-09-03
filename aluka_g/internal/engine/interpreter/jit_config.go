// JIT 桥接：默认配置、per-function JIT 状态与生命周期（配置/关闭/状态提升）。

package interpreter

import (
	"sync"

	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

var defaultJIT = struct {
	sync.RWMutex
	config jit.Config
}{config: jit.Config{Mode: jit.Auto, Threshold: 1000}}

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
	calls      uint32
	backedges  uint32
	program    *jit.Program
	rejected   bool
	reason     string
	nativeUsed uint64
	// nativeHits is the R5-5 heat factor: every native execution (and the
	// install touch) advances it. LRU eviction weights recency (nativeUsed
	// clock) with heat: cold units (nativeHits < jitHotHeatThreshold) are
	// evicted before hot ones, and within the same heat class the
	// least-recently-used unit goes first.
	nativeHits          uint64
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

type quickTraceKey struct {
	tmpl       *bytecode.FuncTemplate
	backedgePC int
}

type quickTraceState struct {
	backedges  uint32
	program    *jit.TraceProgram
	rejected   bool
	reason     string
	nativeUsed uint64
	// nativeHits: R5-5 heat factor, see quickJITState.nativeHits.
	nativeHits       uint64
	arrayPush        *arrayPushTraceState
	closureIncrement *closureIncrementTraceState
	arrayIndex       *arrayIndexTraceState
	arrayBatch       *arrayBatchWriteTraceState
	// upvalues are the compiled trace's upvalue guards. Every execution
	// re-validates cell identity and the non-aliasing precondition before
	// entering the trace (see jit_trace_upvalue.go).
	upvalues            []jit.TraceUpvalueGuard
	guardFailures       uint8
	nativeGuardFailures uint8
	nativeDisabled      bool
	dumpedIR            bool
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
	v.jitBudgetSpent = 0
	v.jitAdaptive = jitAdaptiveState{}
	v.jitCompileSlots = make(chan struct{}, v.jitConfig.CompileWorkers)
}

func (v *VM) closeJIT() {
	if v.jitCloseStartHook != nil {
		v.jitCloseStartHook()
	}
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
