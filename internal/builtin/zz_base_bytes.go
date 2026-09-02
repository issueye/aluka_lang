// 跨模块共享 helper（分包基座）。

package builtin

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// cryptoBytes 把参数转为字节（Buffer 或字符串）。
func cryptoBytes(v engine.Value) ([]byte, error) {
	if b, ok := engine.AsBytes(v); ok {
		return b, nil
	}
	return []byte(v.String()), nil
}
