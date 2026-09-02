// VM 值栈与帧栈原语：容量增长、栈上限、局部变量与数值读取的快路径。

package interpreter

import (
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

// cur returns the top call frame.
func (v *VM) cur() *vmFrame {
	return &v.frames[len(v.frames)-1]
}

// ensureFrameStack 在帧局部槽预留完成后，按 tmpl.MaxStack 一次性预留该帧的
// 操作数栈空间，使帧内 push 永不扩容（cap >= base+NumLocals+MaxStack）。
// 调用前置：len(v.stack) == frame.base + NumLocals（即局部已填满）。
// MaxStack 由编译器静态算出；max(MaxStack,8) 下限兜底手搓 FuncTemplate。
// frameHeadroom 返回帧操作数栈预留槽数：编译器产出的函数用 tmpl.MaxStack
// （严格）；手搓 FuncTemplate（MaxStack==0，如 JIT 测试）退化为 8 兜底。
// ensureFrameStack 与 stackLimit（vmstackcheck）共用，保证「实际预留」与
// 「断言上界」一致。
func frameHeadroom(tmpl *bytecode.FuncTemplate) int {
	if tmpl.MaxStack == 0 {
		return 8
	}
	return tmpl.MaxStack
}

func (v *VM) ensureFrameStack(tmpl *bytecode.FuncTemplate) {
	v.ensureStack(frameHeadroom(tmpl))
}

// stackLimit 返回当前帧的操作数栈上界 base+NumLocals+frameHeadroom，供
// vmstackcheck 构建的 push 越界断言使用（与 ensureFrameStack 实际预留一致）。
// 生产构建不调用。
func (v *VM) stackLimit() int {
	f := v.cur()
	return f.base + f.tmpl.NumLocals + frameHeadroom(f.tmpl)
}

// ensureStack grows the value stack without leaving open upvalues pointing at
// the old backing array. Upvalues also retain an absolute slot index so they
// can be rebound after a grow; this matters because function-frame setup can
// append many locals in one operation, independently of push().
func (v *VM) ensureStack(extra int) {
	if extra <= 0 || len(v.stack)+extra <= cap(v.stack) {
		return
	}
	newCap := cap(v.stack) * 2
	if newCap < len(v.stack)+extra {
		newCap = len(v.stack) + extra
	}
	if newCap < 16 {
		newCap = 16
	}
	next := make([]engine.Value, len(v.stack), newCap)
	copy(next, v.stack)
	v.stack = next
	for _, frame := range v.frames {
		for _, uv := range frame.openUpvalues {
			if uv.slot != nil && uv.index >= 0 && uv.index < len(v.stack) {
				uv.slot = &v.stack[uv.index]
			}
		}
	}
}

func (v *VM) reserveUndefined(n int) {
	if n <= 0 {
		return
	}
	oldLen := len(v.stack)
	v.ensureStack(n)
	v.stack = v.stack[:oldLen+n]
	for i := oldLen; i < len(v.stack); i++ {
		v.stack[i] = engine.Undefined()
	}
}

func (v *VM) appendValues(values []engine.Value) {
	if len(values) == 0 {
		return
	}
	v.ensureStack(len(values))
	v.stack = append(v.stack, values...)
}

func (v *VM) pop() engine.Value {
	last := len(v.stack) - 1
	val := v.stack[last]
	v.stack = v.stack[:last]
	return val
}

func (v *VM) peek() engine.Value { return v.stack[len(v.stack)-1] }

// local returns a pointer to a local slot in the current frame.
func (v *VM) local(slot int) *engine.Value {
	return &v.stack[v.cur().base+slot]
}

// run executes the current top frame until it returns.
// num 从 VM 私有 slab 分配数字（免全局原子；box 一经发布不可变）。
func (v *VM) num(f float64) engine.Value {
	if v.numIdx >= len(v.numSlab) {
		v.numSlab = make([]engine.NumberBox, 4096)
		v.numIdx = 0
	}
	b := &v.numSlab[v.numIdx]
	b.V = f
	v.numIdx++
	return engine.NumberFromBox(b)
}

// fastInt64 判断 float64 是否为安全整数范围内的整数值（|x| < 2^53），
// 是则返回整型值。用于取模等运算的整数快路径。
func fastInt64(f float64) (int64, bool) {
	// 零值走慢路径：fmod 区分 -0（fmod(-0,x)=-0），int64 % 只会产出 +0
	if f == 0 {
		return 0, false
	}
	if f < 1<<53 && f > -(1<<53) {
		i := int64(f)
		if float64(i) == f {
			return i, true
		}
	}
	return 0, false
}
