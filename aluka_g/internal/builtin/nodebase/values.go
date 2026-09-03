// Go 切片到 JS 值切片的转换。

package nodebase

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// stringsToValues 把字符串切片转成 engine.Value 数组。
func StringsToValues(ss []string) []engine.Value {
	out := make([]engine.Value, len(ss))
	for i, s := range ss {
		out[i] = engine.Str(s)
	}
	return out
}
