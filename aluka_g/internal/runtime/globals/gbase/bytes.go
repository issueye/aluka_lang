// TypedArray/字符串到字节切片（Web API 的入参归一）。

package gbase

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// bytesOf 从 Buffer / TypedArray / ArrayBuffer 提取底层字节。
func BytesOf(v engine.Value) ([]byte, bool) {
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
