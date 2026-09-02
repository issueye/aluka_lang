// 跨领域共享 helper（分包基座）。

package globals

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// argBytes 从参数取字节（Buffer 或字符串）。
func argBytes(args []engine.Value, i int) []byte {
	if len(args) <= i || args[i] == nil {
		return nil
	}
	if b, ok := engine.AsBuffer(args[i]); ok {
		return b
	}
	return []byte(args[i].String())
}
