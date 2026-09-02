// 跨领域共享 helper（分包基座）。

package globals

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// bytesOf 从 Buffer / TypedArray / ArrayBuffer 提取底层字节。
func bytesOf(v engine.Value) ([]byte, bool) {
	if b, ok := engine.AsBuffer(v); ok {
		return b, true
	}
	if b, ok := engine.AsArrayBuffer(v); ok {
		return b, true
	}
	if t, ok := engine.AsTypedArray(v); ok {
		return t.Bytes(), true
	}
	return nil, false
}
