// 参数安全取值与索引夹取。

package gbase

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// argInt 安全取第 i 个整数参数（越界或非数字返回 def）。
func ArgInt(args []engine.Value, i int, def int) int {
	if i < len(args) {
		if n, ok := args[i].Int(); ok {
			return n
		}
	}
	return def
}

// clampIdx 把索引限制在 [lo, hi] 范围。
func ClampIdx(idx, lo, hi int) int {
	if idx < lo {
		return lo
	}
	if idx > hi {
		return hi
	}
	return idx
}
