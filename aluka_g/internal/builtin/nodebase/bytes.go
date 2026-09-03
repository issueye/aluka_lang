// JS 值到字节切片的转换（TypedArray/Buffer/字符串）。

package nodebase

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// cryptoBytes 把参数转为字节（Buffer 或字符串）。
func CryptoBytes(v engine.Value) ([]byte, error) {
	if b, ok := engine.AsBytes(v); ok {
		return b, nil
	}
	return []byte(v.String()), nil
}
