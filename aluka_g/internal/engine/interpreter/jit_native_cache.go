// Native 代码缓存：后台编译队列、发布/接管、代码预算与 LRU 淘汰。

package interpreter

import (
	"fmt"
	"time"

	"github.com/aluka-lang/aluka/internal/engine/jit"
)

type nativeCompileResult struct {
	state      *quickJITState
	program    *jit.Program
	generation uint64
	duration   time.Duration
	err        error
}

func (v *VM) queueNativeCompile(state *quickJITState) {
	if state == nil || state.program == nil || state.nativeCompiling || state.nativeDisabled || state.program.HasNative() {
		return
	}
	// R5-4: the queue-length limit caps admitted background jobs (jitPending).
	// A rejected candidate stays on the Quick tier; the denial is observable
	// via QueueDenied and never blocks the interpreter.
	if v.jitConfig.CompileQueueLimit > 0 && v.jitPending >= v.jitConfig.CompileQueueLimit {
		if v.jitConfig.Stats {
			v.jitStats.QueueDenied++
		}
		return
	}
	// R5-4: the cumulative compile-time budget also gates background native
	// compiles ("budget reached -> do not start new compiles").
	if !v.compileAdmitted() {
		return
	}
	program := state.program.CloneForNative()
	if program == nil {
		return
	}
	state.nativeCompiling = true
	v.jitPending++
	if uint64(v.jitPending) > v.jitStats.QueueDepthMax {
		v.jitStats.QueueDepthMax = uint64(v.jitPending)
	}
	if v.jitConfig.Stats {
		v.jitStats.BackgroundQueued++
	}
	generation := v.jitGeneration
	retainDump := v.jitConfig.Dump == jit.DumpASM
	done := v.jitCompileDone
	v.jitCompileWG.Add(1)
	go func() {
		// R5-4: the concurrency semaphore bounds how many of these goroutines
		// actually compile at once. It is acquired inside the goroutine, so a
		// full slot pool never blocks the interpreter hot path; the release is
		// deferred, so a panicking compile still frees its slot.
		v.jitCompileSlots <- struct{}{}
		defer func() { <-v.jitCompileSlots }()
		if v.jitCompileStartHook != nil {
			v.jitCompileStartHook()
		}
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
				elapsed := uint64(result.duration)
				v.jitStats.NativeCompileNanos += elapsed
				v.spendCompileBudget(elapsed)
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
	// 后台编译期间 state.program 可能被 BindCallTarget 内联（IR 形态改变，
	// 如自递归模式关闭）：编译产物与当前 IR 不一致时丢弃，避免装入与 IR
	// 不匹配的机器码（例如旧的自递归模式代码配 hasSelfCall=false 执行，
	// RecBase 未初始化即崩溃）。
	if len(program.Code) != len(state.program.Code) ||
		program.NumLocals != state.program.NumLocals ||
		program.UsesNativeSelfCall() != state.program.UsesNativeSelfCall() {
		return fmt.Errorf("jit: stale native artifact (IR changed after compile)")
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
		// R5-5 weighted LRU: prefer cold victims (heat below the threshold)
		// over hot ones; only when every installed unit is hot is a hot unit
		// evicted (then by recency). Recency keeps its ordering role inside
		// each heat class, so the historical clock semantics remain intact
		// for cold units and the heat factor is observable via
		// NativeHotEvictions.
		victimState, victimTrace := v.lruVictim(excludedState, excludedTrace, true)
		hotVictim := false
		if victimState == nil && victimTrace == nil {
			victimState, victimTrace = v.lruVictim(excludedState, excludedTrace, false)
			hotVictim = true
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
			if hotVictim {
				v.jitStats.NativeHotEvictions++
			}
		}
	}
	v.jitNativeBytes += size
	return nil
}

// lruVictim returns the least-recently-used native unit (quickJITState or
// quickTraceState) whose native code is installed, excluding the unit being
// installed. With coldOnly the search is restricted to cold units (nativeHits
// below jitHotHeatThreshold); the caller falls back to coldOnly=false when no
// cold victim can free the requested bytes — which implies every installed
// unit is hot, so the chosen victim is a hot one.
func (v *VM) lruVictim(excludedState *quickJITState, excludedTrace *quickTraceState, coldOnly bool) (*quickJITState, *quickTraceState) {
	var victimState *quickJITState
	var victimTrace *quickTraceState
	victimUsed := ^uint64(0)
	for _, candidate := range v.jitStates {
		if candidate == excludedState || !jitStateHasNative(candidate) {
			continue
		}
		if coldOnly && candidate.nativeHits >= jitHotHeatThreshold {
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
		if coldOnly && candidate.nativeHits >= jitHotHeatThreshold {
			continue
		}
		if candidate.nativeUsed < victimUsed {
			victimState, victimTrace, victimUsed = nil, candidate, candidate.nativeUsed
		}
	}
	return victimState, victimTrace
}

func (v *VM) touchNative(state *quickJITState) {
	v.jitNativeClock++
	if v.jitNativeClock == 0 {
		v.jitNativeClock = 1
	}
	state.nativeUsed = v.jitNativeClock
	// R5-5: every native execution (and the install touch) advances the heat
	// factor that weights LRU eviction.
	state.nativeHits++
}

func (v *VM) touchNativeTrace(state *quickTraceState) {
	v.jitNativeClock++
	if v.jitNativeClock == 0 {
		v.jitNativeClock = 1
	}
	state.nativeUsed = v.jitNativeClock
	state.nativeHits++
}

func (v *VM) dropNative(state *quickJITState) {
	if !jitStateHasNative(state) {
		return
	}
	// R4-4: the native input-plan guards are released with the machine code;
	// fold their counters into the VM stats first so hits/rejections remain
	// explainable after the native tier is dropped or evicted.
	if v.jitConfig.Stats && state.program != nil {
		h, a, r, o, c := state.program.NativePlanPropertyPICStats()
		v.jitStats.PropertyPICHits += h
		v.jitStats.PropertyPICAdds += a
		v.jitStats.PropertyPICRejections += r
		v.jitStats.PropertyPICOverflows += o
		v.jitStats.PropertyPICCoolDowns += c
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
	if v.jitConfig.Stats {
		h, a, r, o, c := state.program.NativePlanPropertyPICStats()
		v.jitStats.PropertyPICHits += h
		v.jitStats.PropertyPICAdds += a
		v.jitStats.PropertyPICRejections += r
		v.jitStats.PropertyPICOverflows += o
		v.jitStats.PropertyPICCoolDowns += c
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
