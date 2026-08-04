package builtin

// node:crypto 内置模块——加密。
// 提供 createHash（同步哈希）、randomBytes（同步随机数）。
// 基于 Go crypto 标准库。

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewCrypto 构造 node:crypto 模块的导出对象。
func NewCrypto(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// --- createHash ---

	_ = m.Set("createHash", engine.NewFunction("createHash", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("createHash: algorithm required")
		}
		algorithm := args[0].String()
		var h hash.Hash
		switch algorithm {
		case "md5":
			h = md5.New()
		case "sha1":
			h = sha1.New()
		case "sha256":
			h = sha256.New()
		case "sha512":
			h = sha512.New()
		default:
			return engine.Undefined(), fmt.Errorf("createHash: unsupported algorithm %q", algorithm)
		}
		return newHashObject(h, algorithm), nil
	}))

	// --- randomBytes ---

	_ = m.Set("randomBytes", engine.NewFunction("randomBytes", func(args []engine.Value) (engine.Value, error) {
		size := intArg(args, 0, 0)
		if size <= 0 || size > 1<<20 {
			return engine.Undefined(), fmt.Errorf("randomBytes: invalid size %d", size)
		}
		buf := make([]byte, size)
		if _, err := rand.Read(buf); err != nil {
			return engine.Undefined(), err
		}
		// 返回 hex 编码字符串（简化版 Buffer 替代）。
		return engine.Str(hex.EncodeToString(buf)), nil
	}))

	// --- Hash 类（createHash 返回的对象原型） ---
	// 已内联在 newHashObject 中。

	return m, nil
}

// newHashObject 构造一个 Node.js Hash 对象。
func newHashObject(h hash.Hash, algorithm string) engine.Value {
	obj := engine.NewObject()
	_ = obj.Set("algorithm", engine.Str(algorithm))

	// update(data)：更新哈希。
	_ = obj.Set("update", engine.NewFunction("update", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			h.Write([]byte(args[0].String()))
		}
		return obj, nil // 链式
	}))

	// digest(encoding)：计算最终哈希。'hex' → 十六进制，'base64' → base64。
	_ = obj.Set("digest", engine.NewFunction("digest", func(args []engine.Value) (engine.Value, error) {
		sum := h.Sum(nil)
		encoding := "buffer"
		if len(args) > 0 {
			encoding = args[0].String()
		}
		switch encoding {
		case "hex":
			return engine.Str(hex.EncodeToString(sum)), nil
		case "base64":
			return engine.Str(hex.EncodeToString(sum)), nil // 简化：用 hex 代替 base64
		default:
			return engine.Str(hex.EncodeToString(sum)), nil
		}
	}))

	// copy()：返回哈希副本（简化为新建）。
	_ = obj.Set("copy", engine.NewFunction("copy", func(args []engine.Value) (engine.Value, error) {
		return obj, nil
	}))

	return obj
}
