// 跨模块共享 helper（分包基座）。

package builtin

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// stringsToValues 把字符串切片转成 engine.Value 数组。
func stringsToValues(ss []string) []engine.Value {
	out := make([]engine.Value, len(ss))
	for i, s := range ss {
		out[i] = engine.Str(s)
	}
	return out
}
