// 抛出 DOMException（Web API 的标准错误路径）。

package gbase

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// throwDOMException 构造并抛出全局 DOMException 实例（Node 语义：Web API
// 抛出带 name/code 的 DOMException，而非普通 Error）。
func ThrowDOMException(ctx engine.Context, name, message string) error {
	if ctor, err := ctx.Global().Get("DOMException"); err == nil && ctor.IsFunction() {
		if f, ok := ctor.AsFunction(); ok {
			if inst, cerr := f.Call([]engine.Value{engine.Str(message), engine.Str(name)}); cerr == nil {
				return interpreter.ThrowJSValue(inst)
			}
		}
	}
	return fmt.Errorf("%s: %s", name, message)
}
