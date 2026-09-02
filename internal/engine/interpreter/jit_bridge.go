package interpreter

import (
	"fmt"
	"io"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

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
				v.noteAdaptiveFailure()
				return engine.Undefined(), false, nil
			}
			if v.jitConfig.Stats {
				v.jitStats.NativeExecuted++
				v.noteAdaptiveBenefit()
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
		v.noteAdaptiveFailure()
		if v.jitConfig.Stats {
			v.jitStats.Errors++
		}
		return engine.Undefined(), false, nil
	}
	if reason == jit.Executed {
		resetQuickGuardFailures(state)
		if v.jitConfig.Stats {
			v.jitStats.Executed++
			v.noteAdaptiveBenefit()
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
		object, ok := receiver.AsObject()
		if !ok {
			continue
		}
		methodValue, ok := engine.GuardedMethodLookup(object, frame.tmpl.Constants[nameIndex].String())
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

// --- R4-2: numeric-upvalue closure plans -----------------------------------
//
// closurePlan is the parsed body of a numeric-upvalue closure. The grammar is
// exactly what the bytecode compiler emits for numeric upvalue read/write
// statements; anything else (locals, this, method calls, conditionals,
// assignments of arbitrary expressions) is rejected and the closure stays on
// Tier 0. Evaluating the plan against a float64 cache of the captured
// upvalues is bit-identical to executing the closure body, because every
// statement reads and writes the cache in order and the arithmetic ops mirror
// Tier 0's Number semantics (plain IEEE-754 add/sub/mul/div, math.Pow,
// math.Mod with NaN for `% 0`).

// traceBoundOperand parses the loop bound operand at instruction index
// boundIndex of the canonical loop head (LoadLocal bound / PushInt /
// PushNegInt / PushConst Number). Returns (boundLocal, boundConst, isLocal,
// ok); on ok == false the shape is not a supported range loop.
func traceBoundOperand(tmpl *bytecode.FuncTemplate, op func(int) bytecode.Opcode, arg func(int) uint32, boundIndex, excludedLocal int) (int, float64, bool, bool) {
	switch op(boundIndex) {
	case bytecode.OpLoadLocal:
		boundLocal := int(arg(boundIndex))
		if boundLocal < 0 || boundLocal >= tmpl.NumLocals || boundLocal == excludedLocal {
			return 0, 0, false, false
		}
		return boundLocal, 0, true, true
	case bytecode.OpPushInt:
		return -1, float64(arg(boundIndex)), false, true
	case bytecode.OpPushNegInt:
		return -1, -float64(arg(boundIndex)), false, true
	case bytecode.OpPushConst:
		constantIndex := int(arg(boundIndex))
		if constantIndex < 0 || constantIndex >= len(tmpl.Constants) ||
			tmpl.Constants[constantIndex].Type() != engine.TypeNumber {
			return 0, 0, false, false
		}
		boundConst, _ := tmpl.Constants[constantIndex].Float()
		return -1, boundConst, false, true
	default:
		return 0, 0, false, false
	}
}

func (v *VM) tryQuickTrace(frame *vmFrame, startPC, backedgePC int) (int, bool, error) {
	if v.insnsEnabled || frame == nil || frame.tmpl == nil || frame.jitTraceFailedPC == backedgePC {
		// R4-8: the same frame must not retry a trace version that already
		// failed a guard at this backedge (deopt recovery); interpreting the
		// loop is the oracle-equivalent continuation. Only the failed backedge
		// is blocked: a different loop in the same frame can still try.
		if frame != nil && frame.jitTraceFailedPC == backedgePC && v.jitConfig.Stats {
			v.jitStats.TraceFrameRetriesBlocked++
		}
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
		if v.jitConfig.Stats {
			// R3-7: the stable rejection cache serves this backedge; no
			// recompile attempt and no matcher/compiler cost.
			v.jitStats.RejectionCacheHits++
		}
		return 0, false, nil
	}
	if state.program == nil && state.arrayPush == nil && state.closureIncrement == nil &&
		state.arrayIndex == nil && state.arrayBatch == nil {
		if state.backedges < ^uint32(0) {
			state.backedges++
		}
		if state.backedges < v.backedgeThreshold() {
			return 0, false, nil
		}
		compileStart := time.Now()
		// The Go-side shape matchers run before the compile-time candidate
		// scan: the R4-5/R4-6 array loops contain OpGetElem / OpSetElemTop,
		// which the general trace compiler does not lower, so they are only
		// reachable through the exact matchers (everything else stays on the
		// scan -> rejection-cache path).
		if arrayPush := v.matchArrayPushTrace(frame, startPC, backedgePC); arrayPush != nil {
			state.arrayPush = arrayPush
			if v.jitConfig.Stats {
				elapsed := uint64(time.Since(compileStart))
				v.jitStats.TraceCompileNanos += elapsed
				v.spendCompileBudget(elapsed)
				v.jitStats.TracesCompiled++
				v.jitStats.ArrayPushSites++
			}
		} else if arrayIndex := v.matchArrayIndexTrace(frame, startPC, backedgePC); arrayIndex != nil {
			state.arrayIndex = arrayIndex
			if v.jitConfig.Stats {
				elapsed := uint64(time.Since(compileStart))
				v.jitStats.TraceCompileNanos += elapsed
				v.spendCompileBudget(elapsed)
				v.jitStats.TracesCompiled++
				v.jitStats.ArrayIndexSites++
			}
		} else if arrayBatch := v.matchArrayBatchWriteTrace(frame, startPC, backedgePC); arrayBatch != nil {
			state.arrayBatch = arrayBatch
			if v.jitConfig.Stats {
				elapsed := uint64(time.Since(compileStart))
				v.jitStats.TraceCompileNanos += elapsed
				v.spendCompileBudget(elapsed)
				v.jitStats.TracesCompiled++
				v.jitStats.ArrayBatchSites++
			}
		} else if closureIncrement := v.matchClosureIncrementTrace(frame, startPC, backedgePC); closureIncrement != nil {
			state.closureIncrement = closureIncrement
			if v.jitConfig.Stats {
				elapsed := uint64(time.Since(compileStart))
				v.jitStats.TraceCompileNanos += elapsed
				v.spendCompileBudget(elapsed)
				v.jitStats.TracesCompiled++
				v.jitStats.ClosureUpvalueSites++
			}
		} else {
			// R5-4: the cumulative compile budget denies the general trace
			// compile too; the loop stays on Tier 0 and keeps advancing.
			if !v.compileAdmitted() {
				return 0, false, nil
			}
			// R3-7 compile-time candidate filter: reject ranges with try/catch
			// regions or unsupported opcodes before the full trace compiler run.
			if scanErr := jit.RejectTraceReason(frame.tmpl, startPC, backedgePC); scanErr != nil {
				v.recordJITRejection("trace", scanErr)
				state.rejected = true
				state.reason = scanErr.Error()
				v.jitStats.LastError = state.reason
				if v.jitConfig.Stats {
					v.jitStats.TracesRejected++
				}
				v.noteAdaptiveFailure()
				return 0, false, nil
			}
			program, err := jit.CompileTraceWithGuards(
				frame.tmpl, startPC, backedgePC,
				v.traceNoopCallGuards(frame, startPC, backedgePC),
				v.traceMethodGuards(frame, startPC, backedgePC))
			if v.jitConfig.Stats {
				elapsed := uint64(time.Since(compileStart))
				v.jitStats.TraceCompileNanos += elapsed
				v.spendCompileBudget(elapsed)
			}
			if err != nil {
				v.recordJITRejection("trace", err)
				state.rejected = true
				state.reason = err.Error()
				v.jitStats.LastError = err.Error()
				if v.jitConfig.Stats {
					v.jitStats.TracesRejected++
				}
				v.noteAdaptiveFailure()
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
				if !v.compileAdmitted() {
					return 0, false, nil
				}
				nativeStart := time.Now()
				if v.jitConfig.Dump == jit.DumpASM {
					err = program.CompileNativeForDump()
				} else {
					err = program.CompileNative()
				}
				if v.jitConfig.Stats {
					elapsed := uint64(time.Since(nativeStart))
					v.jitStats.NativeTraceCompileNanos += elapsed
					v.spendCompileBudget(elapsed)
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
				v.noteAdaptiveBenefit()
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
			frame.jitTraceFailedPC = backedgePC
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
				v.noteAdaptiveBenefit()
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
			frame.jitTraceFailedPC = backedgePC
			v.noteTraceGuardFailure(state)
			if v.jitConfig.Stats {
				v.jitStats.GuardFailures++
			}
		}
		return 0, false, nil
	}
	if state.arrayIndex != nil {
		exitPC, reason, err := v.executeArrayIndexTrace(state.arrayIndex, locals)
		if err != nil {
			return 0, false, err
		}
		switch reason {
		case jit.Executed:
			resetQuickTraceGuardFailures(state)
			if v.jitConfig.Stats {
				v.jitStats.TracesExecuted++
				v.noteAdaptiveBenefit()
			}
			return exitPC, true, nil
		case jit.Yielded:
			resetQuickTraceGuardFailures(state)
			if v.jitConfig.Stats {
				v.jitStats.TraceYields++
				v.jitStats.ArrayIndexYields++
			}
			return exitPC, true, nil
		case jit.GuardFailed:
			frame.jitTraceFailedPC = backedgePC
			v.noteTraceGuardFailure(state)
			if v.jitConfig.Stats {
				v.jitStats.GuardFailures++
			}
		}
		return 0, false, nil
	}
	if state.arrayBatch != nil {
		exitPC, reason, err := v.executeArrayBatchWriteTrace(state.arrayBatch, locals)
		if err != nil {
			return 0, false, err
		}
		switch reason {
		case jit.Executed:
			resetQuickTraceGuardFailures(state)
			if v.jitConfig.Stats {
				v.jitStats.TracesExecuted++
				v.noteAdaptiveBenefit()
			}
			return exitPC, true, nil
		case jit.Yielded:
			resetQuickTraceGuardFailures(state)
			if v.jitConfig.Stats {
				v.jitStats.TraceYields++
				v.jitStats.ArrayBatchYields++
			}
			return exitPC, true, nil
		case jit.GuardFailed:
			frame.jitTraceFailedPC = backedgePC
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
				v.noteAdaptiveBenefit()
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
				// R4-8: the native trace entry guard failed; the bridge then
				// re-executes the same trace in Quick (the R4-3 property-PIC
				// learning path: Quick may absorb the new shape). The counter
				// makes this duplicate bridge work observable so the cost can
				// be tracked against the absorption wins.
				v.jitStats.NativeTraceQuickFallbacks++
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
		v.noteAdaptiveFailure()
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
			v.noteAdaptiveBenefit()
		}
		return v.resumeTraceExit(key, exit)
	case jit.Yielded:
		resetQuickTraceGuardFailures(state)
		if v.jitConfig.Stats {
			v.jitStats.TraceYields++
		}
		return exit.ResumePC, true, nil
	case jit.GuardFailed:
		frame.jitTraceFailedPC = backedgePC
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
	// F1 Native 自递归（机器码显式栈）只对"upvalue == 当前闭包"（quickCallSelf）
	// 安全：机器码把每个直接形态 OpSelfCall 硬编码为自递归（JMP entry 0）。
	// quickCallBound 时 upvalue 是另一个闭包，直接形态语义是"调用该 upvalue"，
	// 必须留在 Quick（callTarget 机制）+ 身份 guard 路径，Native 自递归会错调。
	// 执行门禁见下方 native 路径：program.UsesNativeSelfCall() 为 false 的
	// 程序（如内联成功的 bound callee）不在此限制内。
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
			v.noteAdaptiveBenefit()
		}
		return engine.Undefined(), true, nil
	}
	// 自递归模式（机器码把 OpSelfCall 硬编码为 JMP entry 0）仅在 quickCallSelf
	// 身份确认后执行；bound/unknown 状态下 UsesNativeSelfCall() 为 true 的
	// 程序必须留在 Quick（callTarget + 身份 guard）。内联后的程序（无自调用
	// 站点，UsesNativeSelfCall()==false）不受限。
	if v.jitConfig.Mode == jit.Auto && program.HasNative() &&
		(!program.UsesNativeSelfCall() || state.callKind == quickCallSelf) {
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
				v.noteAdaptiveFailure()
				return engine.Undefined(), false, nil
			}
			if v.jitConfig.Stats {
				v.jitStats.NativeExecuted++
				v.noteAdaptiveBenefit()
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
		v.noteAdaptiveFailure()
		return engine.Undefined(), false, nil
	}
	switch reason {
	case jit.Executed:
		resetQuickGuardFailures(state)
		if v.jitConfig.Stats {
			v.jitStats.Executed++
			v.noteAdaptiveBenefit()
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
