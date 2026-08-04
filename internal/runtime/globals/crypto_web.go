package globals

// Web Crypto API：crypto.getRandomValues / randomUUID / crypto.subtle.digest
// （开发计划 3.5，Web Crypto subset）。
//
// digest 返回 Promise<ArrayBuffer>（简化用 Buffer 表示）。

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
)

// WebCryptoConfig 配置 crypto 全局（当前无可用选项）。
type WebCryptoConfig struct{}

// NewWebCrypto 注册全局 crypto 对象。
func NewWebCrypto(ctx engine.Context, cfg WebCryptoConfig) error {
	crypto := engine.NewObject()

	// getRandomValues(typedArray)：填充随机字节，返回同一数组。
	_ = crypto.Set("getRandomValues", engine.NewFunction("getRandomValues", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			if b, ok := engine.AsBuffer(args[0]); ok {
				_, _ = rand.Read(b)
				return args[0], nil
			}
		}
		return engine.Undefined(), fmt.Errorf("getRandomValues: expects a typed array")
	}))

	// randomUUID() → UUID v4 字符串。
	_ = crypto.Set("randomUUID", engine.NewFunction("randomUUID", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(randomUUID()), nil
	}))

	// subtle：digest + generateKey/importKey 简化。
	subtle := engine.NewObject()
	_ = subtle.Set("digest", engine.NewFunction("digest", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return promiseRejectValue(ctx, "digest: algorithm and data required")
		}
		algorithm := ""
		if a, ok := args[0].AsObject(); ok {
			if v, err := a.Get("name"); err == nil {
				algorithm = v.String()
			}
		}
		if algorithm == "" {
			algorithm = args[0].String()
		}
		data, ok := engine.AsBuffer(args[1])
		if !ok {
			data = []byte(args[1].String())
		}
		var sum []byte
		switch algorithm {
		case "SHA-256", "sha256":
			h := sha256.Sum256(data)
			sum = h[:]
		case "SHA-1", "sha1":
			h := sha1.Sum(data)
			sum = h[:]
		case "SHA-384", "sha384":
			h := sha512.Sum384(data)
			sum = h[:]
		case "SHA-512", "sha512":
			h := sha512.Sum512(data)
			sum = h[:]
		case "MD5", "md5":
			h := md5.Sum(data)
			sum = h[:]
		default:
			return promiseRejectValue(ctx, "digest: unsupported algorithm "+algorithm)
		}
		return promiseResolveValue(ctx, NewBufferInstance(sum))
	}))
	_ = subtle.Set("importKey", engine.NewFunction("importKey", func(args []engine.Value) (engine.Value, error) {
		return promiseRejectValue(ctx, "importKey: not implemented (Web Crypto subset)")
	}))
	_ = subtle.Set("generateKey", engine.NewFunction("generateKey", func(args []engine.Value) (engine.Value, error) {
		return promiseRejectValue(ctx, "generateKey: not implemented (Web Crypto subset)")
	}))

	_ = crypto.Set("subtle", subtle)
	return ctx.Global().Set("crypto", crypto)
}

// randomUUID 生成 UUID v4。
func randomUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
