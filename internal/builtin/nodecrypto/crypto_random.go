// 随机数与编码 helper：randomInt/randomUUID/randomFill 参数解析与 base64 编码。

package nodecrypto

import (
	"crypto/rand"
	"fmt"

	"github.com/aluka-lang/aluka/internal/builtin/nodebase"
	"github.com/aluka-lang/aluka/internal/engine"
)

// randomIntRange 返回 [min, max) 内均匀随机整数（48 位拒绝采样，无模偏差）。
func randomIntRange(min, max int64) (int64, error) {
	span := uint64(max - min)
	lim := (uint64(1) << 48) - (uint64(1)<<48)%span
	var b [6]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			return 0, err
		}
		v := uint64(b[0])<<40 | uint64(b[1])<<32 | uint64(b[2])<<24 | uint64(b[3])<<16 | uint64(b[4])<<8 | uint64(b[5])
		if v < lim {
			return min + int64(v%span), nil
		}
	}
}

// randomUUID4 生成 RFC 4122 version 4 UUID。
func randomUUID4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// randomFillArgs 解析 randomFill[Sync] 的 (buffer[, offset[, size]])。
func randomFillArgs(args []engine.Value) (target []byte, offset, size int, ok bool) {
	if len(args) == 0 {
		return nil, 0, 0, false
	}
	if b, ok2 := engine.AsBuffer(args[0]); ok2 {
		target = b
	} else if ta, ok2 := engine.AsTypedArray(args[0]); ok2 {
		target = ta.Bytes()
	} else {
		return nil, 0, 0, false
	}
	offset = nodebase.IntArg(args, 1, 0)
	if offset < 0 || offset > len(target) {
		return nil, 0, 0, false
	}
	size = len(target) - offset
	if len(args) > 2 {
		size = nodebase.IntArg(args, 2, size)
	}
	if size < 0 || offset+size > len(target) {
		return nil, 0, 0, false
	}
	return target, offset, size, true
}

// stringContains 字符串切片包含判断。
func stringContains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// base64Encode 简易 base64 编码（避免额外 import 冲突）。
func base64Encode(data []byte) string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var b []byte
	for i := 0; i < len(data); i += 3 {
		var n uint32
		rem := len(data) - i
		switch {
		case rem >= 3:
			n = uint32(data[i])<<16 | uint32(data[i+1])<<8 | uint32(data[i+2])
		case rem == 2:
			n = uint32(data[i])<<16 | uint32(data[i+1])<<8
		default:
			n = uint32(data[i]) << 16
		}
		b = append(b, chars[(n>>18)&0x3F], chars[(n>>12)&0x3F])
		if rem >= 3 {
			b = append(b, chars[(n>>6)&0x3F], chars[n&0x3F])
		} else if rem == 2 {
			b = append(b, chars[(n>>6)&0x3F], '=')
		} else {
			b = append(b, '=', '=')
		}
	}
	return string(b)
}
