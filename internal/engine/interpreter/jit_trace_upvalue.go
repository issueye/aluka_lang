// trace tier 的 upvalue guard：把帧捕获的数值单元暴露给 jit 包，并在编译前
// 校验"可缓存"前提（数值、非别名、单元身份稳定）。
//
// 背景：trace 编译器把 upvalue 读写降为 OpLoadUpvalueNum/OpStoreUpvalueNum，
// 入口一次性把单元数值读进缓存、循环内只动缓存、出口经两阶段提交写回一次。
// 这与 Tier 0 每次迭代直读单元的语义等价，前提是切片期间没有第三方观察或
// 修改该单元——由下面三道 guard 保证：
//
//  1. 数值性：单元当前必须持 Number（LoadNumber 失败即 guard 失败回 Tier 0）。
//  2. 非别名：单元不得指向被追踪帧自己的局部槽位。开放 upvalue 指向本帧局部
//     时，Tier 0 每次迭代读到的是不断变化的局部值，而 trace 缓存的是入口快照。
//  3. 身份稳定：编译时记下 *upvalue 指针，执行前重新比对帧当前的单元；帧重入
//     换了捕获单元（同模板不同闭包实例）时 guard 失败。
//
// 切片内不会有第三方观察：范围内不含 OpCall（除已 guard 的 noop/method 形态）
// 与 OpMakeClosure，故没有用户代码能在提交前读到旧值。

package interpreter

import (
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// traceUpvalueCell 是 jit.TraceUpvalueCell 在解释器侧的实现：直接读写捕获
// 单元（开放态写栈槽、关闭态写 closed 字段），与 Tier 0 的
// OpLoadUpvalue/OpStoreUpvalue 走同一存储。
type traceUpvalueCell struct {
	uv *upvalue
}

func (c traceUpvalueCell) LoadNumber() (float64, bool) {
	if c.uv == nil {
		return 0, false
	}
	var value engine.Value
	if c.uv.slot != nil {
		value = *c.uv.slot
	} else {
		value = c.uv.closed
	}
	if value == nil || value.Type() != engine.TypeNumber {
		return 0, false
	}
	return value.Float()
}

func (c traceUpvalueCell) StoreNumber(number float64) bool {
	if c.uv == nil {
		return false
	}
	if c.uv.slot != nil {
		*c.uv.slot = engine.Number(number)
	} else {
		c.uv.closed = engine.Number(number)
	}
	return true
}

// LoadRef 返回单元当前持有的对象值（数组、普通对象等）。trace 的
// OpLoadUpvalueRef 每次执行现读——切片内没有用户代码，与 Tier 0 的
// LOAD_UPVALUE 观察等价；非对象值返回 false，guard 拒绝该 trace。
func (c traceUpvalueCell) LoadRef() (engine.Value, bool) {
	if c.uv == nil {
		return nil, false
	}
	var value engine.Value
	if c.uv.slot != nil {
		value = *c.uv.slot
	} else {
		value = c.uv.closed
	}
	if value == nil || !value.IsObject() {
		return nil, false
	}
	return value, true
}

// traceUpvalueGuards 收集 [startPC, backedgePC] 触碰的每个 upvalue 索引的
// guard。任一索引不满足前提（越界、单元缺失、非数值、与本帧局部别名）时返回
// nil, false，调用方据此保持原有的"拒绝该 trace"行为。
func (v *VM) traceUpvalueGuards(frame *vmFrame, startPC, backedgePC int) ([]jit.TraceUpvalueGuard, bool) {
	if frame == nil || frame.tmpl == nil || frame.base < 0 {
		return nil, false
	}
	code := frame.tmpl.Code
	localsEnd := frame.base + frame.tmpl.NumLocals
	if localsEnd > len(v.stack) {
		return nil, false
	}
	locals := v.stack[frame.base:localsEnd]
	var guards []jit.TraceUpvalueGuard
	seen := make(map[int]bool, 4)
	for pc := startPC; pc <= backedgePC; pc += bytecode.InstrSize {
		op := bytecode.Opcode(code[pc])
		if op != bytecode.OpLoadUpvalue && op != bytecode.OpStoreUpvalue {
			continue
		}
		index := int(jitTraceOperand(code, pc))
		if seen[index] {
			continue
		}
		if index < 0 || index >= len(frame.upvalues) {
			return nil, false
		}
		uv := frame.upvalues[index]
		if uv == nil {
			return nil, false
		}
		// 别名检查：开放单元指向本帧局部时，Tier 0 每次迭代读到演进中的局部，
		// 而 trace 缓存入口快照——语义不等价，拒绝。
		if uv.slot != nil {
			for i := range locals {
				if uv.slot == &locals[i] {
					return nil, false
				}
			}
		}
		cell := traceUpvalueCell{uv: uv}
		if _, ok := cell.LoadNumber(); !ok {
			// 非数值单元：对象值（如模块作用域的数组）作为只读引用 guard
			// 放行——OpLoadUpvalueRef 每次执行现读当前值。其余类型拒绝。
			if _, ok := cell.LoadRef(); !ok {
				return nil, false
			}
		}
		seen[index] = true
		guards = append(guards, jit.TraceUpvalueGuard{Index: index, Cell: cell})
	}
	return guards, true
}

// traceUpvalueIdentityMatch 在执行前重新校验编译期记录的单元仍是帧当前的
// 单元。帧重入换了捕获单元（同模板的另一个闭包实例）时必须回 Tier 0。
func traceUpvalueIdentityMatch(frame *vmFrame, guards []jit.TraceUpvalueGuard) bool {
	if len(guards) == 0 {
		return true
	}
	if frame == nil {
		return false
	}
	for _, guard := range guards {
		if guard.Index < 0 || guard.Index >= len(frame.upvalues) {
			return false
		}
		cell, ok := guard.Cell.(traceUpvalueCell)
		if !ok || cell.uv != frame.upvalues[guard.Index] {
			return false
		}
	}
	return true
}

// traceUpvalueAliasesLocals 重新检查别名前提。开放单元的 slot 指针会随值栈
// 扩容重绑（ensureStack），故每次执行前都要复查，不能只依赖编译期结论。
func (v *VM) traceUpvalueAliasesLocals(frame *vmFrame, guards []jit.TraceUpvalueGuard, locals []engine.Value) bool {
	if len(guards) == 0 || frame == nil {
		return false
	}
	for _, guard := range guards {
		cell, ok := guard.Cell.(traceUpvalueCell)
		if !ok || cell.uv == nil {
			return true
		}
		if cell.uv.slot == nil {
			continue
		}
		for i := range locals {
			if cell.uv.slot == &locals[i] {
				return true
			}
		}
	}
	return false
}
