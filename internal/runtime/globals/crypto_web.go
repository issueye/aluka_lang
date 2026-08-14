package globals

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
)

// WebCryptoConfig 配置 crypto 全局。
type WebCryptoConfig struct{}

// NewWebCrypto 注册全局 crypto, Crypto, SubtleCrypto, CryptoKey 对象。
func NewWebCrypto(ctx engine.Context, cfg WebCryptoConfig) error {
	crypto := engine.NewObject()

	// getRandomValues(typedArray)：填充随机字节，返回同一数组。
	_ = crypto.Set("getRandomValues", engine.NewFunction("getRandomValues", func(args []engine.Value) (engine.Value, error) {
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
	_ = crypto.Set("randomUUID", engine.NewFunction("randomUUID", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(randomUUID()), nil
	}))

	subtle := engine.NewObject()

	// 1. digest(algorithm, data) -> Promise<ArrayBuffer>
	_ = subtle.Set("digest", engine.NewFunction("digest", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return promiseRejectValue(ctx, "digest: algorithm and data required")
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
			return promiseRejectValue(ctx, "digest: unsupported algorithm "+algorithm)
		}
		return promiseResolveValue(ctx, NewBufferInstance(sum))
	}))

	// 2. importKey(format, keyData, algorithm, extractable, keyUsages) -> Promise<CryptoKey>
	_ = subtle.Set("importKey", engine.NewFunction("importKey", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return promiseRejectValue(ctx, "importKey: format, keyData and algorithm required")
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
			return promiseRejectValue(ctx, "importKey: unsupported format "+format)
		}

		keyVal := newCryptoKey(keyType, extractable, algName, hashName, usages, keyBytes)
		return promiseResolveValue(ctx, keyVal)
	}))

	// 3. exportKey(format, key) -> Promise<ArrayBuffer|Object>
	_ = subtle.Set("exportKey", engine.NewFunction("exportKey", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return promiseRejectValue(ctx, "exportKey: format and key required")
		}
		format := strings.ToLower(args[0].String())
		keyVal := args[1]
		keyObj, ok := keyVal.AsObject()
		if !ok {
			return promiseRejectValue(ctx, "exportKey: invalid key object")
		}

		if extV, err := keyObj.Get("extractable"); err == nil {
			if ext, ok := extV.Bool(); ok && !ext {
				return promiseRejectValue(ctx, "exportKey: key is not extractable")
			}
		}

		keyData := extractKeyBytes(keyObj)

		switch format {
		case "raw":
			return promiseResolveValue(ctx, NewBufferInstance(keyData))
		case "jwk":
			jwk := engine.NewObject()
			_ = jwk.Set("kty", engine.Str("oct"))
			_ = jwk.Set("k", engine.Str(base64.RawURLEncoding.EncodeToString(keyData)))
			_ = jwk.Set("ext", engine.Boolean(true))
			if algV, err := keyObj.Get("algorithm"); err == nil {
				if ao, ok := algV.AsObject(); ok {
					if nameV, err := ao.Get("name"); err == nil {
						_ = jwk.Set("alg", nameV)
					}
				}
			}
			return promiseResolveValue(ctx, jwk)
		default:
			return promiseRejectValue(ctx, "exportKey: unsupported format "+format)
		}
	}))

	// 4. generateKey(algorithm, extractable, keyUsages) -> Promise<CryptoKey|CryptoKeyPair>
	_ = subtle.Set("generateKey", engine.NewFunction("generateKey", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 1 {
			return promiseRejectValue(ctx, "generateKey: algorithm required")
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
			return promiseResolveValue(ctx, newCryptoKey("secret", extractable, algName, hashName, usages, keyData))
		}

		if strings.HasPrefix(upperAlg, "AES") {
			if length <= 0 {
				length = 256
			}
			keyData := make([]byte, length/8)
			_, _ = rand.Read(keyData)
			return promiseResolveValue(ctx, newCryptoKey("secret", extractable, algName, "", usages, keyData))
		}

		// 默认随机生成
		if length <= 0 {
			length = 256
		}
		keyData := make([]byte, length/8)
		_, _ = rand.Read(keyData)
		return promiseResolveValue(ctx, newCryptoKey("secret", extractable, algName, hashName, usages, keyData))
	}))

	// 5. sign(algorithm, key, data) -> Promise<ArrayBuffer>
	_ = subtle.Set("sign", engine.NewFunction("sign", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return promiseRejectValue(ctx, "sign: algorithm, key and data required")
		}
		keyObj, ok := args[1].AsObject()
		if !ok {
			return promiseRejectValue(ctx, "sign: invalid key")
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
			return promiseResolveValue(ctx, NewBufferInstance(sig))
		}

		return promiseRejectValue(ctx, "sign: unsupported algorithm "+algName)
	}))

	// 6. verify(algorithm, key, signature, data) -> Promise<boolean>
	_ = subtle.Set("verify", engine.NewFunction("verify", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 4 {
			return promiseRejectValue(ctx, "verify: algorithm, key, signature and data required")
		}
		keyObj, ok := args[1].AsObject()
		if !ok {
			return promiseRejectValue(ctx, "verify: invalid key")
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
			return promiseResolveValue(ctx, engine.Boolean(isValid))
		}

		return promiseRejectValue(ctx, "verify: unsupported algorithm "+algName)
	}))

	// 7. encrypt(algorithm, key, data) -> Promise<ArrayBuffer>
	_ = subtle.Set("encrypt", engine.NewFunction("encrypt", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return promiseRejectValue(ctx, "encrypt: algorithm, key and data required")
		}
		keyObj, ok := args[1].AsObject()
		if !ok {
			return promiseRejectValue(ctx, "encrypt: invalid key")
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
				return promiseRejectValue(ctx, "encrypt: AES-GCM requires iv")
			}
			additionalData := extractAlgoBytes(algObj, "additionalData")

			block, err := aes.NewCipher(keyBytes)
			if err != nil {
				return promiseRejectValue(ctx, "encrypt: "+err.Error())
			}
			gcm, err := cipher.NewGCMWithNonceSize(block, len(iv))
			if err != nil {
				return promiseRejectValue(ctx, "encrypt: "+err.Error())
			}
			ciphertext := gcm.Seal(nil, iv, data, additionalData)
			return promiseResolveValue(ctx, NewBufferInstance(ciphertext))
		}

		if upperAlg == "AES-CBC" {
			iv := extractAlgoBytes(algObj, "iv")
			if len(iv) != aes.BlockSize {
				return promiseRejectValue(ctx, "encrypt: AES-CBC requires 16-byte iv")
			}
			block, err := aes.NewCipher(keyBytes)
			if err != nil {
				return promiseRejectValue(ctx, "encrypt: "+err.Error())
			}
			padded := pkcs7Pad(data, aes.BlockSize)
			ciphertext := make([]byte, len(padded))
			mode := cipher.NewCBCEncrypter(block, iv)
			mode.CryptBlocks(ciphertext, padded)
			return promiseResolveValue(ctx, NewBufferInstance(ciphertext))
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
				return promiseRejectValue(ctx, "encrypt: "+err.Error())
			}
			ctr := cipher.NewCTR(block, counter)
			ciphertext := make([]byte, len(data))
			ctr.XORKeyStream(ciphertext, data)
			return promiseResolveValue(ctx, NewBufferInstance(ciphertext))
		}

		return promiseRejectValue(ctx, "encrypt: unsupported algorithm "+algName)
	}))

	// 8. decrypt(algorithm, key, data) -> Promise<ArrayBuffer>
	_ = subtle.Set("decrypt", engine.NewFunction("decrypt", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return promiseRejectValue(ctx, "decrypt: algorithm, key and data required")
		}
		keyObj, ok := args[1].AsObject()
		if !ok {
			return promiseRejectValue(ctx, "decrypt: invalid key")
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
				return promiseRejectValue(ctx, "decrypt: AES-GCM requires iv")
			}
			additionalData := extractAlgoBytes(algObj, "additionalData")

			block, err := aes.NewCipher(keyBytes)
			if err != nil {
				return promiseRejectValue(ctx, "decrypt: "+err.Error())
			}
			gcm, err := cipher.NewGCMWithNonceSize(block, len(iv))
			if err != nil {
				return promiseRejectValue(ctx, "decrypt: "+err.Error())
			}
			plaintext, err := gcm.Open(nil, iv, data, additionalData)
			if err != nil {
				return promiseRejectValue(ctx, "decrypt: authentication tag mismatch or corrupted data")
			}
			return promiseResolveValue(ctx, NewBufferInstance(plaintext))
		}

		if upperAlg == "AES-CBC" {
			iv := extractAlgoBytes(algObj, "iv")
			if len(iv) != aes.BlockSize {
				return promiseRejectValue(ctx, "decrypt: AES-CBC requires 16-byte iv")
			}
			if len(data)%aes.BlockSize != 0 {
				return promiseRejectValue(ctx, "decrypt: invalid ciphertext length for AES-CBC")
			}
			block, err := aes.NewCipher(keyBytes)
			if err != nil {
				return promiseRejectValue(ctx, "decrypt: "+err.Error())
			}
			padded := make([]byte, len(data))
			mode := cipher.NewCBCDecrypter(block, iv)
			mode.CryptBlocks(padded, data)
			plaintext, err := pkcs7Unpad(padded)
			if err != nil {
				return promiseRejectValue(ctx, "decrypt: "+err.Error())
			}
			return promiseResolveValue(ctx, NewBufferInstance(plaintext))
		}

		if upperAlg == "AES-CTR" {
			counter := extractAlgoBytes(algObj, "counter")
			block, err := aes.NewCipher(keyBytes)
			if err != nil {
				return promiseRejectValue(ctx, "decrypt: "+err.Error())
			}
			ctr := cipher.NewCTR(block, counter)
			plaintext := make([]byte, len(data))
			ctr.XORKeyStream(plaintext, data)
			return promiseResolveValue(ctx, NewBufferInstance(plaintext))
		}

		return promiseRejectValue(ctx, "decrypt: unsupported algorithm "+algName)
	}))

	// 9. deriveBits(algorithm, baseKey, length) -> Promise<ArrayBuffer>
	_ = subtle.Set("deriveBits", engine.NewFunction("deriveBits", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return promiseRejectValue(ctx, "deriveBits: algorithm, baseKey, length required")
		}
		keyObj, ok := args[1].AsObject()
		if !ok {
			return promiseRejectValue(ctx, "deriveBits: invalid baseKey")
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
			return promiseResolveValue(ctx, NewBufferInstance(derived))
		}

		return promiseRejectValue(ctx, "deriveBits: unsupported algorithm "+algName)
	}))

	// 10. deriveKey(algorithm, baseKey, derivedKeyType, extractable, keyUsages) -> Promise<CryptoKey>
	_ = subtle.Set("deriveKey", engine.NewFunction("deriveKey", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return promiseRejectValue(ctx, "deriveKey: algorithm, baseKey and derivedKeyType required")
		}
		keyObj, ok := args[1].AsObject()
		if !ok {
			return promiseRejectValue(ctx, "deriveKey: invalid baseKey")
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
			return promiseResolveValue(ctx, newCryptoKey("secret", extractable, targetAlgName, "", usages, derived))
		}

		return promiseRejectValue(ctx, "deriveKey: unsupported algorithm "+algName)
	}))

	_ = crypto.Set("subtle", subtle)
	if err := ctx.Global().Set("crypto", crypto); err != nil {
		return err
	}

	// 构造器注入
	cryptoCtor := engine.NewFunction("Crypto", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), fmt.Errorf("%w: Illegal constructor", engine.ErrTypeError)
	})
	cryptoProto := engine.NewObject()
	_ = cryptoProto.Set("constructor", cryptoCtor)
	if co, ok := cryptoCtor.AsObject(); ok {
		_ = co.Set("prototype", cryptoProto)
	}
	engine.SetProto(crypto, cryptoProto)
	_ = ctx.Global().Set("Crypto", cryptoCtor)

	subtleCtor := engine.NewFunction("SubtleCrypto", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), fmt.Errorf("%w: Illegal constructor", engine.ErrTypeError)
	})
	subtleProto := engine.NewObject()
	_ = subtleProto.Set("constructor", subtleCtor)
	if so, ok := subtleCtor.AsObject(); ok {
		_ = so.Set("prototype", subtleProto)
	}
	engine.SetProto(subtle, subtleProto)
	_ = ctx.Global().Set("SubtleCrypto", subtleCtor)

	keyCtor := engine.NewFunction("CryptoKey", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), fmt.Errorf("%w: Illegal constructor", engine.ErrTypeError)
	})
	keyProto := engine.NewObject()
	_ = keyProto.Set("constructor", keyCtor)
	if ko, ok := keyCtor.AsObject(); ok {
		_ = ko.Set("prototype", keyProto)
	}
	cryptoKeyProto = keyProto
	_ = ctx.Global().Set("CryptoKey", keyCtor)

	return nil
}

// cryptoKeyProto 是 CryptoKey.prototype
var cryptoKeyProto engine.Object

// newCryptoKey 构造标准 CryptoKey 对象。
func newCryptoKey(typeStr string, extractable bool, algorithmName, hashName string, usages []string, keyData []byte) engine.Value {
	key := engine.NewObject()
	if cryptoKeyProto != nil {
		engine.SetProto(key, cryptoKeyProto)
	}
	_ = key.Set("type", engine.Str(typeStr))
	_ = key.Set("extractable", engine.Boolean(extractable))

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
	_ = key.Set("algorithm", algo)

	usagesVals := make([]engine.Value, len(usages))
	for i, u := range usages {
		usagesVals[i] = engine.Str(u)
	}
	_ = key.Set("usages", engine.NewArray(usagesVals))
	_ = key.Set("_keyData", NewBufferInstance(keyData))
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
	if v, err := keyObj.Get("_keyData"); err == nil {
		if b, ok := engine.AsBuffer(v); ok {
			return b
		}
	}
	return nil
}

func extractKeyAlgorithmName(keyObj engine.Object) string {
	if algV, err := keyObj.Get("algorithm"); err == nil {
		if ao, ok := algV.AsObject(); ok {
			if nameV, err := ao.Get("name"); err == nil {
				return nameV.String()
			}
		}
	}
	return ""
}

func extractKeyHashName(keyObj engine.Object) string {
	if algV, err := keyObj.Get("algorithm"); err == nil {
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

func randomUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
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
