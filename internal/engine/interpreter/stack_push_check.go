//go:build vmstackcheck

package interpreter

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine/bytecode"

	"github.com/aluka-lang/aluka/internal/engine"
)

// push（vmstackcheck 构建）：在帧预留边界外越界即 panic，用于校验
// ComputeMaxStack 的 soundness。配合 -tags vmstackcheck 跑全量套件 + jitdiff：
// 任何 panic 都表示某函数的 MaxStack 被低估，须修 ComputeMaxStack 而非本断言。
// 生产构建用 stack_push.go 的无分支版本。
//
// frame.stackLimit = frame.base + NumLocals + MaxStack 是该帧承诺的上界；
// push 越过它即说明 MaxStack 不 sound。
func (v *VM) push(val engine.Value) {
	if len(v.stack) >= v.stackLimit() {
		f := v.cur()
		op, operand, _ := bytecode.Decode(f.tmpl.Code, f.pc)
		panic(fmt.Sprintf("vmstackcheck: stack exceeded MaxStack under-estimate: fn=%q base=%d NumLocals=%d MaxStack=%d len=%d pc=%d op=%s(%d)",
			f.tmpl.Name, f.base, f.tmpl.NumLocals, f.tmpl.MaxStack, len(v.stack), f.pc, op, operand))
	}
	v.stack = append(v.stack, val)
}

// pushSafe（vmstackcheck 构建）：与生产版一致，自带容量检查。
func (v *VM) pushSafe(val engine.Value) {
	if len(v.stack) == cap(v.stack) {
		v.ensureStack(1)
	}
	v.stack = append(v.stack, val)
}
