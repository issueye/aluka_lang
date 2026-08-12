//go:build !vmstackcheck

package interpreter

import "github.com/aluka-lang/aluka/internal/engine"

// push（生产构建）：裸 append。帧入口的 ensureFrameStack 已按 tmpl.MaxStack
// 预留该帧全部操作数栈槽，帧内 push 永不扩容，故无容量检查、无 ensureStack
// 调用、无分支——体积最小，可被内联进 run 的分派循环（gcflags -m 验证）。
// 调用方必须处于已预留栈空间的帧内（run 主循环）；run 之外用 pushSafe。
func (v *VM) push(val engine.Value) {
	v.stack = append(v.stack, val)
}

// pushSafe 自带容量检查与 ensureStack（扩容并重绑 upvalue 槽指针），供 run
// 主循环之外、无栈预留保证的调用方使用（throw 抛异常值、async/generator
// resume 值）。这些路径 per-throw/resume 触发，开销可忽略。
func (v *VM) pushSafe(val engine.Value) {
	if len(v.stack) == cap(v.stack) {
		v.ensureStack(1)
	}
	v.stack = append(v.stack, val)
}
