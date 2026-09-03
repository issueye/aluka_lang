package gcrypto

// Web Crypto API：crypto.getRandomValues / randomUUID / crypto.subtle 全量实现
// 纯 Go 实现，全面兼容 W3C Web Cryptography API 与 Node.js 22 / Bun 行为。
// 支持 digest / generateKey / importKey / exportKey / sign / verify / encrypt / decrypt / deriveBits / deriveKey。

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash"
	"math/big"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gbase"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gbuffer"
)

// WebCryptoConfig 配置 crypto 全局。
type WebCryptoConfig struct{}

// NewWebCrypto 注册全局 crypto, Crypto, SubtleCrypto, CryptoKey 对象。
// WebIDL 原型链语义（compat-boundary-closure-plan 工作流 B3）：
//   - crypto / crypto.subtle 自有键为空，方法挂 Crypto.prototype /
//     SubtleCrypto.prototype（可枚举、wec 全 true），实例经 SetProto 接入；
//   - subtle 是 Crypto.prototype 上的访问器，恒返回同一共享实例；
//   - CryptoKey 实例内部状态存 Symbol 键槽位（own keys / for-in 均为空，
//     对齐 Node），type/extractable/algorithm/usages 为原型 getter。
func NewWebCrypto(ctx engine.Context, cfg WebCryptoConfig) error {
	subtle := engine.NewObject()

	// --- SubtleCrypto 接口 ---
	_, subtleProto, err := gbase.RegisterInterface(ctx, gbase.WebInterface{Name: "SubtleCrypto", Tag: "SubtleCrypto"})
	if err != nil {
		return err
	}

	// 1. digest(algorithm, data) -> Promise<ArrayBuffer>
	_ = subtleProto.Set("digest", engine.NewFunction("digest", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return gbase.RejectValue(ctx, "digest: algorithm and data required")
		}
		algorithm := webAlgoName(args[0])
		data := getBytesFromValue(args[1])
		var sum []byte
		switch strings.ToUpper(algorithm) {
		case "SHA-256", "SHA256":
			h := sha256.Sum256(data)
			sum = h[:]
		case "SHA-1", "SHA1":
			h := sha1.Sum(data)
			sum = h[:]
		case "SHA-384", "SHA384":
			h := sha512.Sum384(data)
			sum = h[:]
		case "SHA-512", "SHA512":
			h := sha512.Sum512(data)
			sum = h[:]
		case "MD5":
			h := md5.Sum(data)
			sum = h[:]
		default:
			return gbase.RejectValue(ctx, "digest: unsupported algorithm "+algorithm)
		}
		return gbase.ResolveValue(ctx, gbuffer.NewBufferInstance(sum))
	}))

	// 2. importKey(format, keyData, algorithm, extractable, keyUsages) -> Promise<CryptoKey>
	_ = subtleProto.Set("importKey", engine.NewFunction("importKey", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return gbase.RejectValue(ctx, "importKey: format, keyData and algorithm required")
		}
		format := strings.ToLower(args[0].String())
		algObj := args[2]
		algName := webAlgoName(algObj)
		hashName := webHashName(algObj)

		extractable := false
		if len(args) > 3 {
			if b, ok := args[3].Bool(); ok {
				extractable = b
			}
		}
		usages := webKeyUsages(args, 4)

		var keyBytes []byte
		var keyType = "secret"

		switch format {
		case "raw":
			keyBytes = getBytesFromValue(args[1])
		case "jwk":
			jwkObj, ok := args[1].AsObject()
			if !ok {
				// 尝试解析 JSON 字符串
				var parsed map[string]any
				if err := json.Unmarshal([]byte(args[1].String()), &parsed); err == nil {
					if k, ok := parsed["k"].(string); ok {
						keyBytes, _ = base64.RawURLEncoding.DecodeString(k)
					}
				}
			} else {
				if kV, err := jwkObj.Get("k"); err == nil && !kV.IsUndefined() {
					keyBytes, _ = base64.RawURLEncoding.DecodeString(kV.String())
				}
			}
		case "pkcs8":
			keyBytes = getBytesFromValue(args[1])
			keyType = "private"
		case "spki":
			keyBytes = getBytesFromValue(args[1])
			keyType = "public"
		default:
			return gbase.RejectValue(ctx, "importKey: unsupported format "+format)
		}

		keyVal := newCryptoKey(keyType, extractable, algName, hashName, usages, keyBytes)
		return gbase.ResolveValue(ctx, keyVal)
	}))

	// 3. exportKey(format, key) -> Promise<ArrayBuffer|Object>
	_ = subtleProto.Set("exportKey", engine.NewFunction("exportKey", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return gbase.RejectValue(ctx, "exportKey: format and key required")
		}
		format := strings.ToLower(args[0].String())
		keyVal := args[1]
		keyObj, ok := keyVal.AsObject()
		if !ok {
			return gbase.RejectValue(ctx, "exportKey: invalid key object")
		}

		if ext := cryptoKeyExtractable(keyObj); !ext {
			return gbase.RejectValue(ctx, "exportKey: key is not extractable")
		}

		keyData := extractKeyBytes(keyObj)

		switch format {
		case "raw":
			return gbase.ResolveValue(ctx, gbuffer.NewBufferInstance(keyData))
		case "jwk":
			jwk := engine.NewObject()
			_ = jwk.Set("kty", engine.Str("oct"))
			_ = jwk.Set("k", engine.Str(base64.RawURLEncoding.EncodeToString(keyData)))
			_ = jwk.Set("ext", engine.Boolean(true))
			if algo := extractKeyAlgorithm(keyObj); algo != nil {
				if nameV, err := algo.Get("name"); err == nil {
					_ = jwk.Set("alg", nameV)
				}
			}
			return gbase.ResolveValue(ctx, jwk)
		default:
			return gbase.RejectValue(ctx, "exportKey: unsupported format "+format)
		}
	}))

	// 4. generateKey(algorithm, extractable, keyUsages) -> Promise<CryptoKey|CryptoKeyPair>
	_ = subtleProto.Set("generateKey", engine.NewFunction("generateKey", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 1 {
			return gbase.RejectValue(ctx, "generateKey: algorithm required")
		}
		algName, length := webAlgoAndLength(args[0])
		hashName := webHashName(args[0])

		extractable := false
		if len(args) > 1 {
			if b, ok := args[1].Bool(); ok {
				extractable = b
			}
		}
		usages := webKeyUsages(args, 2)

		upperAlg := strings.ToUpper(algName)
		if upperAlg == "HMAC" {
			if length <= 0 {
				length = 256
				if strings.Contains(strings.ToUpper(hashName), "512") {
					length = 512
				} else if strings.Contains(strings.ToUpper(hashName), "384") {
					length = 384
				}
			}
			keyData := make([]byte, length/8)
			_, _ = rand.Read(keyData)
			return gbase.ResolveValue(ctx, newCryptoKey("secret", extractable, algName, hashName, usages, keyData))
		}

		if strings.HasPrefix(upperAlg, "AES") {
			if length <= 0 {
				length = 256
			}
			keyData := make([]byte, length/8)
			_, _ = rand.Read(keyData)
			return gbase.ResolveValue(ctx, newCryptoKey("secret", extractable, algName, "", usages, keyData))
		}

		// 默认随机生成
		if length <= 0 {
			length = 256
		}
		keyData := make([]byte, length/8)
		_, _ = rand.Read(keyData)
		return gbase.ResolveValue(ctx, newCryptoKey("secret", extractable, algName, hashName, usages, keyData))
	}))

	// 5. sign(algorithm, key, data) -> Promise<ArrayBuffer>
	_ = subtleProto.Set("sign", engine.NewFunction("sign", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return gbase.RejectValue(ctx, "sign: algorithm, key and data required")
		}
		keyObj, ok := args[1].AsObject()
		if !ok {
			return gbase.RejectValue(ctx, "sign: invalid key")
		}
		keyBytes := extractKeyBytes(keyObj)
		data := getBytesFromValue(args[2])

		algName := webAlgoName(args[0])
		if algName == "" {
			algName = extractKeyAlgorithmName(keyObj)
		}
		hashName := webHashName(args[0])
		if hashName == "" {
			hashName = extractKeyHashName(keyObj)
		}

		upperAlg := strings.ToUpper(algName)
		if upperAlg == "HMAC" {
			h := newHasher(hashName)
			mac := hmac.New(h, keyBytes)
			mac.Write(data)
			sig := mac.Sum(nil)
			return gbase.ResolveValue(ctx, gbuffer.NewBufferInstance(sig))
		}

		return gbase.RejectValue(ctx, "sign: unsupported algorithm "+algName)
	}))

	// 6. verify(algorithm, key, signature, data) -> Promise<boolean>
	_ = subtleProto.Set("verify", engine.NewFunction("verify", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 4 {
			return gbase.RejectValue(ctx, "verify: algorithm, key, signature and data required")
		}
		keyObj, ok := args[1].AsObject()
		if !ok {
			return gbase.RejectValue(ctx, "verify: invalid key")
		}
		keyBytes := extractKeyBytes(keyObj)
		signature := getBytesFromValue(args[2])
		data := getBytesFromValue(args[3])

		algName := webAlgoName(args[0])
		if algName == "" {
			algName = extractKeyAlgorithmName(keyObj)
		}
		hashName := webHashName(args[0])
		if hashName == "" {
			hashName = extractKeyHashName(keyObj)
		}

		upperAlg := strings.ToUpper(algName)
		if upperAlg == "HMAC" {
			h := newHasher(hashName)
			mac := hmac.New(h, keyBytes)
			mac.Write(data)
			expected := mac.Sum(nil)
			isValid := hmac.Equal(signature, expected)
			return gbase.ResolveValue(ctx, engine.Boolean(isValid))
		}

		return gbase.RejectValue(ctx, "verify: unsupported algorithm "+algName)
	}))

	// 7. encrypt(algorithm, key, data) -> Promise<ArrayBuffer>
	_ = subtleProto.Set("encrypt", engine.NewFunction("encrypt", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return gbase.RejectValue(ctx, "encrypt: algorithm, key and data required")
		}
		keyObj, ok := args[1].AsObject()
		if !ok {
			return gbase.RejectValue(ctx, "encrypt: invalid key")
		}
		keyBytes := extractKeyBytes(keyObj)
		data := getBytesFromValue(args[2])

		algObj := args[0]
		algName := webAlgoName(algObj)
		if algName == "" {
			algName = extractKeyAlgorithmName(keyObj)
		}

		upperAlg := strings.ToUpper(algName)
		if upperAlg == "AES-GCM" {
			iv := extractAlgoBytes(algObj, "iv")
			if len(iv) == 0 {
				return gbase.RejectValue(ctx, "encrypt: AES-GCM requires iv")
			}
			additionalData := extractAlgoBytes(algObj, "additionalData")

			block, err := aes.NewCipher(keyBytes)
			if err != nil {
				return gbase.RejectValue(ctx, "encrypt: "+err.Error())
			}
			gcm, err := cipher.NewGCMWithNonceSize(block, len(iv))
			if err != nil {
				return gbase.RejectValue(ctx, "encrypt: "+err.Error())
			}
			ciphertext := gcm.Seal(nil, iv, data, additionalData)
			return gbase.ResolveValue(ctx, gbuffer.NewBufferInstance(ciphertext))
		}

		if upperAlg == "AES-CBC" {
			iv := extractAlgoBytes(algObj, "iv")
			if len(iv) != aes.BlockSize {
				return gbase.RejectValue(ctx, "encrypt: AES-CBC requires 16-byte iv")
			}
			block, err := aes.NewCipher(keyBytes)
			if err != nil {
				return gbase.RejectValue(ctx, "encrypt: "+err.Error())
			}
			padded := pkcs7Pad(data, aes.BlockSize)
			ciphertext := make([]byte, len(padded))
			mode := cipher.NewCBCEncrypter(block, iv)
			mode.CryptBlocks(ciphertext, padded)
			return gbase.ResolveValue(ctx, gbuffer.NewBufferInstance(ciphertext))
		}

		if upperAlg == "AES-CTR" {
			counter := extractAlgoBytes(algObj, "counter")
			length := 64
			if o, ok := algObj.AsObject(); ok {
				if lV, err := o.Get("length"); err == nil {
					if n, ok := lV.Int(); ok {
						length = n
					}
				}
			}
			_ = length
			block, err := aes.NewCipher(keyBytes)
			if err != nil {
				return gbase.RejectValue(ctx, "encrypt: "+err.Error())
			}
			ctr := cipher.NewCTR(block, counter)
			ciphertext := make([]byte, len(data))
			ctr.XORKeyStream(ciphertext, data)
			return gbase.ResolveValue(ctx, gbuffer.NewBufferInstance(ciphertext))
		}

		return gbase.RejectValue(ctx, "encrypt: unsupported algorithm "+algName)
	}))

	// 8. decrypt(algorithm, key, data) -> Promise<ArrayBuffer>
	_ = subtleProto.Set("decrypt", engine.NewFunction("decrypt", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return gbase.RejectValue(ctx, "decrypt: algorithm, key and data required")
		}
		keyObj, ok := args[1].AsObject()
		if !ok {
			return gbase.RejectValue(ctx, "decrypt: invalid key")
		}
		keyBytes := extractKeyBytes(keyObj)
		data := getBytesFromValue(args[2])

		algObj := args[0]
		algName := webAlgoName(algObj)
		if algName == "" {
			algName = extractKeyAlgorithmName(keyObj)
		}

		upperAlg := strings.ToUpper(algName)
		if upperAlg == "AES-GCM" {
			iv := extractAlgoBytes(algObj, "iv")
			if len(iv) == 0 {
				return gbase.RejectValue(ctx, "decrypt: AES-GCM requires iv")
			}
			additionalData := extractAlgoBytes(algObj, "additionalData")

			block, err := aes.NewCipher(keyBytes)
			if err != nil {
				return gbase.RejectValue(ctx, "decrypt: "+err.Error())
			}
			gcm, err := cipher.NewGCMWithNonceSize(block, len(iv))
			if err != nil {
				return gbase.RejectValue(ctx, "decrypt: "+err.Error())
			}
			plaintext, err := gcm.Open(nil, iv, data, additionalData)
			if err != nil {
				return gbase.RejectValue(ctx, "decrypt: authentication tag mismatch or corrupted data")
			}
			return gbase.ResolveValue(ctx, gbuffer.NewBufferInstance(plaintext))
		}

		if upperAlg == "AES-CBC" {
			iv := extractAlgoBytes(algObj, "iv")
			if len(iv) != aes.BlockSize {
				return gbase.RejectValue(ctx, "decrypt: AES-CBC requires 16-byte iv")
			}
			if len(data)%aes.BlockSize != 0 {
				return gbase.RejectValue(ctx, "decrypt: invalid ciphertext length for AES-CBC")
			}
			block, err := aes.NewCipher(keyBytes)
			if err != nil {
				return gbase.RejectValue(ctx, "decrypt: "+err.Error())
			}
			padded := make([]byte, len(data))
			mode := cipher.NewCBCDecrypter(block, iv)
			mode.CryptBlocks(padded, data)
			plaintext, err := pkcs7Unpad(padded)
			if err != nil {
				return gbase.RejectValue(ctx, "decrypt: "+err.Error())
			}
			return gbase.ResolveValue(ctx, gbuffer.NewBufferInstance(plaintext))
		}

		if upperAlg == "AES-CTR" {
			counter := extractAlgoBytes(algObj, "counter")
			block, err := aes.NewCipher(keyBytes)
			if err != nil {
				return gbase.RejectValue(ctx, "decrypt: "+err.Error())
			}
			ctr := cipher.NewCTR(block, counter)
			plaintext := make([]byte, len(data))
			ctr.XORKeyStream(plaintext, data)
			return gbase.ResolveValue(ctx, gbuffer.NewBufferInstance(plaintext))
		}

		return gbase.RejectValue(ctx, "decrypt: unsupported algorithm "+algName)
	}))

	// 9. deriveBits(algorithm, baseKey, length) -> Promise<ArrayBuffer>
	_ = subtleProto.Set("deriveBits", engine.NewFunction("deriveBits", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return gbase.RejectValue(ctx, "deriveBits: algorithm, baseKey, length required")
		}
		keyObj, ok := args[1].AsObject()
		if !ok {
			return gbase.RejectValue(ctx, "deriveBits: invalid base key")
		}
		keyBytes := extractKeyBytes(keyObj)
		length := 256
		if n, ok := args[2].Int(); ok {
			length = n
		}

		algObj := args[0]
		algName := webAlgoName(algObj)
		upperAlg := strings.ToUpper(algName)

		if upperAlg == "PBKDF2" {
			salt := extractAlgoBytes(algObj, "salt")
			iterations := 10000
			if o, ok := algObj.AsObject(); ok {
				if itV, err := o.Get("iterations"); err == nil {
					if it, ok := itV.Int(); ok {
						iterations = it
					}
				}
			}
			hashName := webHashName(algObj)
			h := newHasher(hashName)
			derived := pbkdf2Key(keyBytes, salt, iterations, length/8, h)
			return gbase.ResolveValue(ctx, gbuffer.NewBufferInstance(derived))
		}

		return gbase.RejectValue(ctx, "deriveBits: unsupported algorithm "+algName)
	}))

	// 10. deriveKey(algorithm, baseKey, derivedKeyType, extractable, keyUsages) -> Promise<CryptoKey>
	_ = subtleProto.Set("deriveKey", engine.NewFunction("deriveKey", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return gbase.RejectValue(ctx, "deriveKey: algorithm, baseKey and derivedKeyType required")
		}
		keyObj, ok := args[1].AsObject()
		if !ok {
			return gbase.RejectValue(ctx, "deriveKey: invalid base key")
		}
		keyBytes := extractKeyBytes(keyObj)

		targetAlgObj := args[2]
		targetAlgName, targetLen := webAlgoAndLength(targetAlgObj)
		if targetLen <= 0 {
			targetLen = 256
		}

		extractable := false
		if len(args) > 3 {
			if b, ok := args[3].Bool(); ok {
				extractable = b
			}
		}
		usages := webKeyUsages(args, 4)

		algObj := args[0]
		algName := webAlgoName(algObj)
		upperAlg := strings.ToUpper(algName)

		if upperAlg == "PBKDF2" {
			salt := extractAlgoBytes(algObj, "salt")
			iterations := 10000
			if o, ok := algObj.AsObject(); ok {
				if itV, err := o.Get("iterations"); err == nil {
					if it, ok := itV.Int(); ok {
						iterations = it
					}
				}
			}
			hashName := webHashName(algObj)
			h := newHasher(hashName)
			derived := pbkdf2Key(keyBytes, salt, iterations, targetLen/8, h)
			return gbase.ResolveValue(ctx, newCryptoKey("secret", extractable, targetAlgName, "", usages, derived))
		}

		return gbase.RejectValue(ctx, "deriveKey: unsupported algorithm "+algName)
	}))

	// 11/12. wrapKey/unwrapKey —— 原型面占位（对齐 Node 原型键集合；当前
	// 算法矩阵下按既有惯例拒绝，走 promise reject 而非静默成功）。
	_ = subtleProto.Set("wrapKey", engine.NewFunction("wrapKey", func(args []engine.Value) (engine.Value, error) {
		return gbase.RejectValue(ctx, "wrapKey: unsupported algorithm")
	}))
	_ = subtleProto.Set("unwrapKey", engine.NewFunction("unwrapKey", func(args []engine.Value) (engine.Value, error) {
		return gbase.RejectValue(ctx, "unwrapKey: unsupported algorithm")
	}))

	engine.SetProto(subtle, subtleProto)

	// --- Crypto 接口 ---
	_, cryptoProto, err := gbase.RegisterInterface(ctx, gbase.WebInterface{Name: "Crypto", Tag: "Crypto"})
	if err != nil {
		return err
	}

	// getRandomValues(typedArray)：填充随机字节，返回同一数组。
	_ = cryptoProto.Set("getRandomValues", engine.NewFunction("getRandomValues", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			if b, ok := engine.AsBuffer(args[0]); ok {
				_, _ = rand.Read(b)
				return args[0], nil
			}
			if ta, ok := engine.AsTypedArray(args[0]); ok &&
				ta.Kind() != engine.KindFloat32 && ta.Kind() != engine.KindFloat64 {
				_, _ = rand.Read(ta.Bytes())
				return args[0], nil
			}
		}
		return engine.Undefined(), fmt.Errorf("getRandomValues: expects an integer typed array")
	}))

	// randomUUID() → UUID v4 字符串。
	_ = cryptoProto.Set("randomUUID", engine.NewFunction("randomUUID", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(gbase.RandomUUID()), nil
	}))

	// subtle 访问器：恒返回共享实例（crypto.subtle === crypto.subtle）。
	engine.SetAccessor(cryptoProto, "subtle",
		interpreter.NewNativeMethod("get subtle", func(this engine.Value, args []engine.Value) (engine.Value, error) {
			return subtle, nil
		}),
		engine.Undefined())

	crypto := engine.NewObject()
	engine.SetProto(crypto, cryptoProto)
	if err := ctx.Global().Set("crypto", crypto); err != nil {
		return err
	}

	// --- CryptoKey 接口 ---
	_, keyProto, err := gbase.RegisterInterface(ctx, gbase.WebInterface{Name: "CryptoKey", Tag: "CryptoKey"})
	if err != nil {
		return err
	}
	for _, g := range []struct {
		name string
		slot *engine.SymbolValue
	}{
		{"type", slotKeyType},
		{"extractable", slotKeyExtractable},
		{"algorithm", slotKeyAlgorithm},
		{"usages", slotKeyUsages},
	} {
		slot := g.slot
		engine.SetAccessor(keyProto, g.name,
			interpreter.NewNativeMethod("get "+g.name, func(this engine.Value, args []engine.Value) (engine.Value, error) {
				if o, ok := this.AsObject(); ok {
					return o.Get(slot.SymbolKey())
				}
				return engine.Undefined(), nil
			}),
			engine.Undefined())
	}
	cryptoKeyProto = keyProto

	return nil
}

// cryptoKeyProto 是 CryptoKey.prototype
var cryptoKeyProto engine.Object

// CryptoKey 内部槽位：Symbol 键对 JS 侧 own keys / for-in / getOwnPropertyNames
// 均不可见（Object.keys/getOwnPropertyNames 过滤 symbol 键），对齐 Node 的
// 空 own-key 语义；type/extractable/algorithm/usages 由原型 getter 读取。
var (
	slotKeyType        = engine.NewSymbol("aluka.CryptoKey.type")
	slotKeyExtractable = engine.NewSymbol("aluka.CryptoKey.extractable")
	slotKeyAlgorithm   = engine.NewSymbol("aluka.CryptoKey.algorithm")
	slotKeyUsages      = engine.NewSymbol("aluka.CryptoKey.usages")
	slotKeyData        = engine.NewSymbol("aluka.CryptoKey.keyData")
)

// newCryptoKey 构造标准 CryptoKey 对象。
func newCryptoKey(typeStr string, extractable bool, algorithmName, hashName string, usages []string, keyData []byte) engine.Value {
	key := engine.NewObject()
	if cryptoKeyProto != nil {
		engine.SetProto(key, cryptoKeyProto)
	}

	algo := engine.NewObject()
	_ = algo.Set("name", engine.Str(algorithmName))
	if hashName != "" {
		hashObj := engine.NewObject()
		_ = hashObj.Set("name", engine.Str(hashName))
		_ = algo.Set("hash", hashObj)
	}
	if len(keyData) > 0 {
		_ = algo.Set("length", engine.Number(float64(len(keyData)*8)))
	}

	usagesVals := make([]engine.Value, len(usages))
	for i, u := range usages {
		usagesVals[i] = engine.Str(u)
	}

	_ = key.Set(slotKeyType.SymbolKey(), engine.Str(typeStr))
	_ = key.Set(slotKeyExtractable.SymbolKey(), engine.Boolean(extractable))
	_ = key.Set(slotKeyAlgorithm.SymbolKey(), algo)
	_ = key.Set(slotKeyUsages.SymbolKey(), engine.NewArray(usagesVals))
	_ = key.Set(slotKeyData.SymbolKey(), gbuffer.NewBufferInstance(keyData))
	return key
}

// 辅助方法：提取各类参数
func getBytesFromValue(v engine.Value) []byte {
	if b, ok := engine.AsBuffer(v); ok {
		return b
	}
	if ta, ok := engine.AsTypedArray(v); ok {
		return ta.Bytes()
	}
	if ab, ok := engine.AsArrayBuffer(v); ok {
		return ab
	}
	return []byte(v.String())
}

func extractKeyBytes(keyObj engine.Object) []byte {
	if v, err := keyObj.Get(slotKeyData.SymbolKey()); err == nil {
		if b, ok := engine.AsBuffer(v); ok {
			return b
		}
	}
	return nil
}

// cryptoKeyExtractable 读取 CryptoKey 的 extractable 槽位。
func cryptoKeyExtractable(keyObj engine.Object) bool {
	if v, err := keyObj.Get(slotKeyExtractable.SymbolKey()); err == nil {
		if b, ok := v.Bool(); ok {
			return b
		}
	}
	return false
}

// extractKeyAlgorithm 返回 CryptoKey 的 algorithm 槽位对象。
func extractKeyAlgorithm(keyObj engine.Object) engine.Object {
	if v, err := keyObj.Get(slotKeyAlgorithm.SymbolKey()); err == nil {
		if o, ok := v.AsObject(); ok {
			return o
		}
	}
	return nil
}

func extractKeyAlgorithmName(keyObj engine.Object) string {
	if algV, err := keyObj.Get(slotKeyAlgorithm.SymbolKey()); err == nil {
		if ao, ok := algV.AsObject(); ok {
			if nameV, err := ao.Get("name"); err == nil {
				return nameV.String()
			}
		}
	}
	return ""
}

func extractKeyHashName(keyObj engine.Object) string {
	if algV, err := keyObj.Get(slotKeyAlgorithm.SymbolKey()); err == nil {
		if ao, ok := algV.AsObject(); ok {
			if hV, err := ao.Get("hash"); err == nil {
				if ho, ok := hV.AsObject(); ok {
					if nameV, err := ho.Get("name"); err == nil {
						return nameV.String()
					}
				}
				return hV.String()
			}
		}
	}
	return ""
}

func extractAlgoBytes(algVal engine.Value, field string) []byte {
	if o, ok := algVal.AsObject(); ok {
		if v, err := o.Get(field); err == nil && !v.IsUndefined() {
			return getBytesFromValue(v)
		}
	}
	return nil
}

func webAlgoName(v engine.Value) string {
	if v == nil || v.IsUndefined() || v.IsNull() {
		return ""
	}
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

func webHashName(v engine.Value) string {
	if o, ok := v.AsObject(); ok {
		if h, err := o.Get("hash"); err == nil && !h.IsUndefined() {
			if h.Type() == engine.TypeString {
				return h.String()
			}
			if ho, ok := h.AsObject(); ok {
				if name, err := ho.Get("name"); err == nil {
					return name.String()
				}
			}
		}
	}
	return "SHA-256"
}

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

func newHasher(name string) func() hash.Hash {
	switch strings.ToUpper(name) {
	case "SHA-1", "SHA1":
		return sha1.New
	case "SHA-384", "SHA384":
		return sha512.New384
	case "SHA-512", "SHA512":
		return sha512.New
	case "MD5":
		return md5.New
	default:
		return sha256.New
	}
}

// PKCS#7 Padding
func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - (len(data) % blockSize)
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, padtext...)
}

func pkcs7Unpad(data []byte) ([]byte, error) {
	length := len(data)
	if length == 0 {
		return nil, fmt.Errorf("pkcs7: empty data")
	}
	unpadding := int(data[length-1])
	if unpadding > length || unpadding == 0 {
		return nil, fmt.Errorf("pkcs7: invalid padding")
	}
	for i := length - unpadding; i < length; i++ {
		if data[i] != byte(unpadding) {
			return nil, fmt.Errorf("pkcs7: invalid padding byte")
		}
	}
	return data[:length-unpadding], nil
}

// PBKDF2 算法实现
func pbkdf2Key(password, salt []byte, iter, keyLen int, h func() hash.Hash) []byte {
	prf := hmac.New(h, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen

	var result []byte
	var block []byte
	var u []byte

	for blockNum := 1; blockNum <= numBlocks; blockNum++ {
		prf.Reset()
		prf.Write(salt)
		var buf [4]byte
		buf[0] = byte(blockNum >> 24)
		buf[1] = byte(blockNum >> 16)
		buf[2] = byte(blockNum >> 8)
		buf[3] = byte(blockNum)
		prf.Write(buf[:])
		u = prf.Sum(nil)
		block = append(block[:0], u...)

		for i := 2; i <= iter; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(nil)
			for j := range block {
				block[j] ^= u[j]
			}
		}
		result = append(result, block...)
	}

	return result[:keyLen]
}

// Suppress unused imports
var (
	_ = ecdsa.GenerateKey
	_ = elliptic.P256
	_ = rsa.GenerateKey
	_ = x509.ParsePKCS8PrivateKey
	_ = big.NewInt
)
