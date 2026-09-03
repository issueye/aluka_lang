// 参数安全取值：越界与类型不符时返回零值/默认值。

package nodebase

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// strArg 安全取第 i 个字符串参数（越界返回空串）。
func StrArg(args []engine.Value, i int) string {
	if i < len(args) {
		return args[i].String()
	}
	return ""
}

// intArg 安全取第 i 个整数参数（越界或非数字返回 def）。
func IntArg(args []engine.Value, i int, def int) int {
	if i < len(args) {
		if n, ok := args[i].Int(); ok {
			return n
		}
	}
	return def
}

// boolArg 安全取第 i 个布尔参数。
func BoolArg(args []engine.Value, i int) bool {
	if i < len(args) {
		if b, ok := args[i].Bool(); ok {
			return b
		}
	}
	return false
}
