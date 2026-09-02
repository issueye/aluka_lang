// 热点探测与编译入口：调用/回边计数、Quick 编译触发、callee 特化与闭包 upvalue 存取。

package interpreter

import (
	"fmt"
	"io"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

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
		if count.calls < v.callThreshold() {
			v.jitHotCounts[cl.tmpl] = count
			return nil
		}
		state = v.promoteJITState(cl.tmpl, count)
		cl.jitState = state
	}
	if state.calls < ^uint32(0) {
		if state.calls < v.callThreshold() {
			state.calls++
		}
	}
	v.maybeCompileJITState(state, cl.tmpl, state.calls >= v.callThreshold())
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
		if count.backedges < v.backedgeThreshold() {
			v.jitHotCounts[tmpl] = count
			return nil
		}
		state = v.promoteJITState(tmpl, count)
	}
	if state.backedges < ^uint32(0) {
		if state.backedges < v.backedgeThreshold() {
			state.backedges++
		}
	}
	v.maybeCompileJITState(state, tmpl, state.backedges >= v.backedgeThreshold())
	return state
}

func (v *VM) maybeCompileJITState(state *quickJITState, tmpl *bytecode.FuncTemplate, hot bool) {
	if state == nil || !hot || state.program != nil || state.rejected {
		if state != nil && state.rejected && v.jitConfig.Stats {
			// R3-7: the structured rejection cache serves this hot candidate;
			// no compile attempt (and no candidate recount) happens again.
			v.jitStats.RejectionCacheHits++
		}
		return
	}
	// R5-4: the cumulative compile-time budget denies new compiles once spent
	// reaches the limit; the state stays uncompiled and the interpreter
	// continues on Tier 0 / Quick (the denial is observable via BudgetDenied).
	if !v.compileAdmitted() {
		return
	}
	if v.jitConfig.Stats {
		v.jitStats.Candidates++
	}
	compileStart := time.Now()
	// R3-7 compile-time candidate filter: reject statically unsupported
	// shapes (try/catch regions, unsupported opcodes, dynamic arguments,
	// oversized frames) without running the full lowering.
	var program *jit.Program
	var err error
	if scanErr := jit.RejectLeafReason(tmpl); scanErr != nil {
		err = scanErr
	} else {
		program, err = jit.CompileLeaf(tmpl)
	}
	if err == nil && program.IsTrivialThisPropertyGetter() {
		err = fmt.Errorf("jit: cost model rejects trivial this-property getter")
		program = nil
	}
	if v.jitConfig.Stats {
		elapsed := uint64(time.Since(compileStart))
		v.jitStats.CompileNanos += elapsed
		v.spendCompileBudget(elapsed)
	}
	if err != nil {
		v.recordJITRejection("quick", err)
		state.rejected = true
		state.reason = err.Error()
		v.jitStats.LastError = state.reason
		if v.jitConfig.Stats {
			v.jitStats.Rejected++
		}
		v.noteAdaptiveFailure()
	} else {
		state.program = program
		v.dumpJITIR(state)
		if v.jitConfig.Stats {
			v.jitStats.Compiled++
		}
		if v.jitConfig.Mode == jit.Auto {
			// F1：自递归程序（RequiresSelf）也可编译 Native——机器码对
			// OpPushSelf+OpSelfCall 直接形态做显式栈自递归。非直接形态
			// （callee 来自局部变量/参数）在 compileNativeProgram 内被拒绝，
			// 此处不需要静态区分；执行侧的身份门控见 tryQuickCall。
			if len(program.Code) >= 128 {
				v.queueNativeCompile(state)
				return
			}
			if !v.compileAdmitted() {
				return
			}
			nativeStart := time.Now()
			if err := v.installNative(state); err != nil {
				v.recordJITRejection("native", err)
				if v.jitConfig.Stats {
					elapsed := uint64(time.Since(nativeStart))
					v.jitStats.NativeCompileNanos += elapsed
					v.spendCompileBudget(elapsed)
				}
				v.jitStats.LastNativeError = err.Error()
				if v.jitConfig.Stats {
					v.jitStats.NativeRejected++
				}
			} else {
				v.dumpJITASM(state)
				if v.jitConfig.Stats {
					elapsed := uint64(time.Since(nativeStart))
					v.jitStats.NativeCompileNanos += elapsed
					v.spendCompileBudget(elapsed)
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
	// R5-4: the cumulative compile budget denies the callee leaf compile too.
	if !v.compileAdmitted() {
		return
	}
	targetProgram, err := jit.CompileLeaf(target.tmpl)
	if err != nil {
		v.recordJITRejection("callee", err)
		state.callKind = quickCallUnsupported
		v.noteAdaptiveFailure()
		return
	}
	baseProgram := state.program.CloneForNative()
	// F1：内联会改变 IR 形态（自递归模式关闭），旧的机器码先行释放
	// （Close + 记账），BindCallTarget 内只清引用——否则会装入与 IR
	// 不匹配的代码，且旧代码无人 Close 造成 executable memory 泄漏。
	if state.program.HasNative() {
		v.dropNative(state)
	}
	inlined, err := state.program.BindCallTarget(targetProgram)
	if err != nil {
		_ = baseProgram.Close()
		v.recordJITRejection("callee", err)
		state.callKind = quickCallUnsupported
		v.noteAdaptiveFailure()
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
		if !v.compileAdmitted() {
			return
		}
		nativeStart := time.Now()
		if err := v.installNative(state); err != nil {
			v.recordJITRejection("native", err)
			if v.jitConfig.Stats {
				elapsed := uint64(time.Since(nativeStart))
				v.jitStats.NativeCompileNanos += elapsed
				v.spendCompileBudget(elapsed)
			}
			v.jitStats.LastNativeError = err.Error()
			if v.jitConfig.Stats {
				v.jitStats.NativeRejected++
			}
		} else {
			v.dumpJITASM(state)
			if v.jitConfig.Stats {
				elapsed := uint64(time.Since(nativeStart))
				v.jitStats.NativeCompileNanos += elapsed
				v.spendCompileBudget(elapsed)
				v.jitStats.NativeCompiled++
			}
		}
	}
}

func (v *VM) specializeAlternateCallee(state *quickJITState, target *vmClosure) (*jit.Program, bool) {
	if state == nil || target == nil || target.tmpl == nil || state.baseProgram == nil || state.altProgram != nil {
		return nil, false
	}
	if !v.compileAdmitted() {
		return nil, false
	}
	targetProgram, err := jit.CompileLeaf(target.tmpl)
	if err != nil {
		v.recordJITRejection("callee", err)
		v.noteAdaptiveFailure()
		return nil, false
	}
	program := state.baseProgram.CloneForNative()
	inlined, err := program.BindCallTarget(targetProgram)
	if err != nil {
		v.recordJITRejection("callee", err)
		_ = program.Close()
		v.noteAdaptiveFailure()
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
		if !v.compileAdmitted() {
			return program, true
		}
		nativeStart := time.Now()
		if v.jitConfig.Dump == jit.DumpASM {
			err = program.CompileNativeForDump()
		} else {
			err = program.CompileNative()
		}
		if v.jitConfig.Stats {
			elapsed := uint64(time.Since(nativeStart))
			v.jitStats.NativeCompileNanos += elapsed
			v.spendCompileBudget(elapsed)
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
