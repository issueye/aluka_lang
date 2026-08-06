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
		if len(args) < 3 {
			return promiseRejectValue(ctx, "importKey: format, keyData and algorithm required")
		}
		format := args[0].String()
		var keyData []byte
		if b, ok := engine.AsBuffer(args[1]); ok {
			keyData = b
		} else {
			keyData = []byte(args[1].String())
		}
		algName := webAlgoName(args[2])
		extractable := false
		if len(args) > 3 {
			if b, ok := args[3].Bool(); ok {
				extractable = b
			}
		}
		usages := webKeyUsages(args, 4)
		switch format {
		case "raw", "pkcs8", "spki", "jwk":
		default:
			return promiseRejectValue(ctx, "importKey: unsupported format "+format)
		}
		return promiseResolveValue(ctx, newCryptoKey("secret", extractable, algName, usages, keyData))
	}))
	_ = subtle.Set("generateKey", engine.NewFunction("generateKey", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 1 {
			return promiseRejectValue(ctx, "generateKey: algorithm required")
		}
		algName, length := webAlgoAndLength(args[0])
		extractable := false
		if len(args) > 1 {
			if b, ok := args[1].Bool(); ok {
				extractable = b
			}
		}
		usages := webKeyUsages(args, 2)
		if length <= 0 {
			length = 256
		}
		keyData := make([]byte, length/8)
		_, _ = rand.Read(keyData)
		return promiseResolveValue(ctx, newCryptoKey("secret", extractable, algName, usages, keyData))
	}))

	_ = crypto.Set("subtle", subtle)
	return ctx.Global().Set("crypto", crypto)
}

// newCryptoKey 构造 CryptoKey 对象。
func newCryptoKey(typeStr string, extractable bool, algorithmName string, usages []string, keyData []byte) engine.Value {
	key := engine.NewObject()
	_ = key.Set("type", engine.Str(typeStr))
	_ = key.Set("extractable", engine.Boolean(extractable))
	algo := engine.NewObject()
	_ = algo.Set("name", engine.Str(algorithmName))
	_ = key.Set("algorithm", algo)
	usagesVals := make([]engine.Value, len(usages))
	for i, u := range usages {
		usagesVals[i] = engine.Str(u)
	}
	_ = key.Set("usages", engine.NewArray(usagesVals))
	_ = key.Set("_keyData", NewBufferInstance(keyData))
	return key
}

// webAlgoName 从算法参数提取名字（对象 {name} 或字符串）。
func webAlgoName(v engine.Value) string {
	if v.Type() == engine.TypeString {
		return v.String()
	}
	if o, ok := v.AsObject(); ok {
		if name, err := o.Get("name"); err == nil && !name.IsUndefined() {
			return name.String()
		}
	}
	return ""
}

// webAlgoAndLength 提取算法名与长度。
func webAlgoAndLength(v engine.Value) (string, int) {
	name := webAlgoName(v)
	length := 0
	if o, ok := v.AsObject(); ok {
		if l, err := o.Get("length"); err == nil {
			if n, ok := l.Int(); ok {
				length = n
			}
		}
	}
	return name, length
}

// webKeyUsages 从参数数组提取 key usages。
func webKeyUsages(args []engine.Value, i int) []string {
	if i >= len(args) {
		return nil
	}
	if a, ok := args[i].(*engine.ArrayValue); ok {
		out := make([]string, 0, len(a.Elems()))
		for _, e := range a.Elems() {
			out = append(out, e.String())
		}
		return out
	}
	return nil
}

// randomUUID 生成 UUID v4。
func randomUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
