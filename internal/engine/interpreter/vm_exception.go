// VM 异常与 try 语义：throw 传播、handler 查找、try/finally 出口动作与异常规范化。

package interpreter

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// vmTryHandler tracks an active try/catch/finally region.
type vmTryHandler struct {
	entry *bytecode.TryEntry
	exc   engine.Value // pending exception (nil means none)
	phase int          // 0=in try, 1=in catch, 2=in finally

	// completion 记录 return/break/continue 穿过本 handler 的待办完成
	// （进入 finally 前挂起，OpTryExitFinally 后恢复）。nil 表示正常完成。
	completion *vmCompletion
}

// vmCompletionKind 是 try 展开（exitTry）处理的完成类型。
type vmCompletionKind uint8

const (
	compReturn vmCompletionKind = iota // return：值待返回
	compJump                           // break/continue：跳转到目标 PC
)

// vmCompletion 描述一次穿过 try/finally 区域的 return 或跳转。
type vmCompletion struct {
	kind  vmCompletionKind
	value engine.Value // compReturn：返回值
	pc    int          // compJump：跳转目标
}

// tryExitAction 是 exitTry 的返回结果。
type tryExitAction uint8

const (
	// exitContinue：pc 已设置（进入 finally 或跳转完成），run 循环继续。
	exitContinue tryExitAction = iota
	// exitRethrow：异常需要重新抛出。
	exitRethrow
	// exitReturn：compReturn 已完全解析，调用方执行 doReturn。
	exitReturn
)

// jsThrow wraps a JS exception value as a Go error so it can propagate through
// Go's error return values while preserving the original JS value.
type jsThrow struct {
	val engine.Value
}

func (e *jsThrow) Error() string { return engine.FormatException(e.val) }

// ThrowJSValue 构造一个携带 JS 值的抛出错误（供内置模块实现 Node 语义，
// 如 EventEmitter 的 emit('error') 无监听器时抛出原始值）。经 VM 调用链
// normalizeException 还原为 JS 值，可被 try/catch 捕获。
func ThrowJSValue(val engine.Value) error {
	return &jsThrow{val: val}
}

// handleThrow processes a thrown exception (value or Go error). It searches
// ONLY the current frame's try-stack for a matching handler. If found, it
// jumps to the catch/finally and resumes execution. If not, it returns a
// *jsThrow so the caller (run → callClosure → invoke → doCall → OpCall) can
// propagate it to the outer frame, which will call handleThrow again.
func (v *VM) handleThrow(exc interface{}) (engine.Value, error) {
	excVal := v.normalizeException(exc)
	_, jumped := v.findHandlerInFrame(excVal)
	if jumped {
		// Handler found in the current frame; resume execution.
		return v.run()
	}
	// No handler in the current frame: wrap and return.
	return engine.Undefined(), &jsThrow{val: excVal}
}

// findHandlerInFrame searches the current frame's try-stack for a handler that
// can catch the exception. If found, it sets the phase, stores the exception,
// sets the PC to catch/finally, and returns (handler, true). If not found,
// returns (nil, false).
func (v *VM) findHandlerInFrame(excVal engine.Value) (*vmTryHandler, bool) {
	frame := v.cur()
	for i := len(frame.tryStack) - 1; i >= 0; i-- {
		h := frame.tryStack[i]
		if h.phase >= 1 {
			// Already in catch (phase 1) or finally (phase 2); a re-thrown
			// exception must propagate to an OUTER handler, not re-enter this one.
			// Pop this handler so the search continues to enclosing handlers.
			frame.tryStack = frame.tryStack[:i]
			continue
		}
		// This is the handler to use. Pop handlers above it.
		frame.tryStack = frame.tryStack[:i+1]
		if h.entry.HasCatch {
			h.phase = 1
			h.exc = excVal
			// Push the exception value; the catch code does OpStoreLocal into the param.
			// 异常分发在 run 主循环之外，无栈预留保证，用 pushSafe。
			v.pushSafe(excVal)
			frame.pc = h.entry.CatchPC
			return h, true
		}
		if h.entry.HasFinally {
			h.phase = 2
			h.exc = excVal
			frame.pc = h.entry.FinallyPC
			return h, true
		}
		// No catch and no finally: pop and continue searching.
		frame.tryStack = frame.tryStack[:i]
	}
	return nil, false
}

// jumpInsideRegion reports whether the jump target lies within the handler's
// currently-active region. Region bounds come from the compiled TryEntry:
// try 块 [StartPC, EndPC]、catch 块 [CatchPC, CatchEndPC]、finally 块
// [FinallyPC, FinallyEndPC]，均含区域末尾的 OpTryExit/OpTryExitFinally 指令
// （跳转到该指令等价于从区域正常退出，handler 交由其收尾）。
func jumpInsideRegion(h *vmTryHandler, target int) bool {
	switch h.phase {
	case 0:
		return target >= h.entry.StartPC && target <= h.entry.EndPC
	case 1:
		return target >= h.entry.CatchPC && target <= h.entry.CatchEndPC
	default: // 2（finally 内）
		return target >= h.entry.FinallyPC && target <= h.entry.FinallyEndPC
	}
}

// exitTry 沿当前帧 try 栈处理一次"穿过 try 区域"的完成（return 或跳转）。
//
// 语义（与 ECMA-262 一致）：
//   - 目标仍在 handler 区域内（仅跳转判定，return 总是穿出）→ 保留 handler，
//     直接跳转；区域末尾的 OpTryExit 会自行收尾。
//   - handler 有 finally 且未进入 finally（phase <= 1）→ 记录 completion，
//     phase=2，pc 指向 finally 块；finally 结束（OpTryExitFinally）后恢复。
//   - handler 无 finally → 弹出丢弃（return/break 绕过 catch）。
//   - 已处于 finally（phase==2）→ 新 completion 覆盖旧值（finally 内的
//     return/break/throw 覆盖 try 的待办完成），继续向外查找。
//
// 返回值：exitContinue=pc 已设置（进入 finally 或完成跳转），exitReturn=
// compReturn 完全解析（调用方执行 doReturn）。
func (v *VM) exitTry(c *vmCompletion) tryExitAction {
	frame := v.cur()
	for i := len(frame.tryStack) - 1; i >= 0; i-- {
		h := frame.tryStack[i]
		if c.kind == compJump && jumpInsideRegion(h, c.pc) {
			// 跳转目标仍在当前 handler 区域内：保留 handler 及其下方的所有
			// handler，仅弹出其上方的（跳转从它们的区域穿出，已处理）。
			frame.tryStack = frame.tryStack[:i+1]
			frame.pc = c.pc
			return exitContinue
		}
		if !h.entry.HasFinally {
			// 无 finally：直接丢弃（return/break/continue 绕过 catch）。
			frame.tryStack = frame.tryStack[:i]
			continue
		}
		if h.phase <= 1 {
			// 记录 completion，进入 finally 块。
			h.completion = c
			h.phase = 2
			frame.tryStack = frame.tryStack[:i+1]
			frame.pc = h.entry.FinallyPC
			return exitContinue
		}
		// phase == 2：已在 finally 内，新 completion 覆盖旧的。
		h.completion = nil
		frame.tryStack = frame.tryStack[:i]
	}
	if c.kind == compJump {
		frame.pc = c.pc
		return exitContinue
	}
	return exitReturn
}

// handleTryExit is called for OpTryExit (normal exit from try or catch).
func (v *VM) handleTryExit(tryIdx int) {
	frame := v.cur()
	for i := len(frame.tryStack) - 1; i >= 0; i-- {
		h := frame.tryStack[i]
		if h.entry == &frame.tmpl.TryTable[tryIdx] {
			// If in catch (phase 1), the exception is handled — clear it.
			if h.phase == 1 {
				h.exc = nil
			}
			// If there's a finally, transition to finally phase (don't pop).
			if h.entry.HasFinally {
				h.phase = 2
				return
			}
			// No finally: pop the handler.
			frame.tryStack = frame.tryStack[:i]
			return
		}
	}
}

// handleTryExitFinally is called for OpTryExitFinally. It pops the handler
// and resumes any pending completion (return/jump) or rethrows a pending
// exception. Returns (exitContinue, nil) to keep running, (exitRethrow, exc)
// to rethrow, or (exitReturn, value) when a pending return can complete.
func (v *VM) handleTryExitFinally(tryIdx int) (tryExitAction, engine.Value) {
	frame := v.cur()
	for i := len(frame.tryStack) - 1; i >= 0; i-- {
		h := frame.tryStack[i]
		if h.entry == &frame.tmpl.TryTable[tryIdx] {
			frame.tryStack = frame.tryStack[:i]
			if h.completion != nil {
				c := h.completion
				h.completion = nil
				// 恢复被 finally 挂起的 return/break/continue：继续向外展开
				// （可能还有外层 finally 待运行）。
				if v.exitTry(c) == exitReturn {
					return exitReturn, c.value
				}
				return exitContinue, nil
			}
			if h.exc != nil {
				return exitRethrow, h.exc
			}
			return exitContinue, nil
		}
	}
	return exitContinue, nil
}

// normalizeException converts a thrown value (engine.Value or Go error) into
// an engine.Value suitable for the JS catch clause.
func (v *VM) normalizeException(exc interface{}) engine.Value {
	switch e := exc.(type) {
	case engine.Value:
		return e
	case *jsThrow:
		return e.val
	case error:
		return v.goErrorToValue(e)
	default:
		return engine.Str(fmt.Sprintf("%v", e))
	}
}

// goErrorToValue converts a Go error to a JS Value (Error object or string).
func (v *VM) goErrorToValue(err error) engine.Value {
	return v.interp.goErrorToJSValue(err)
}
