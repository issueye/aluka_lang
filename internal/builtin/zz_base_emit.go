// 跨模块共享 helper（分包基座）。

package builtin

import (
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// emitEvent 在对象上触发事件（通过 emit 方法）。
func emitEvent(obj engine.Value, event string, args ...engine.Value) {
	if o, ok := obj.AsObject(); ok {
		if emitFn, err := o.Get("emit"); err == nil && emitFn.IsFunction() {
			f, _ := emitFn.AsFunction()
			allArgs := append([]engine.Value{engine.Str(event)}, args...)
			if _, err := f.Call(allArgs); err != nil {
				interpreter.ReportUncaught(nil, err)
			}
		}
	}
}
