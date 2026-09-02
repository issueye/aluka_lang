// Web Cryptography API（crypto.subtle）：encrypt/decrypt/sign/verify/deriveBits/exportKey 与算法参数解析。

package builtin

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

// webCryptoResolve 构造一个已 resolve 的 Promise。
func webCryptoResolve(ctx engine.Context, v engine.Value) (engine.Value, error) {
	promiseCtor, err := ctx.Global().Get("Promise")
	if err != nil || !promiseCtor.IsFunction() {
		return engine.Undefined(), fmt.Errorf("webcrypto: global Promise not available")
	}
	executor := engine.NewFunction("executor", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			if f, ok := args[0].AsFunction(); ok {
				if _, err := f.Call([]engine.Value{v}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		}
		return engine.Undefined(), nil
	})
	pf, ok := promiseCtor.AsFunction()
	if !ok {
		return engine.Undefined(), fmt.Errorf("webcrypto: Promise not callable")
	}
	return pf.Call([]engine.Value{executor})
}

// webCryptoReject 构造一个已 reject 的 Promise。
func webCryptoReject(ctx engine.Context, msg string) (engine.Value, error) {
	promiseCtor, err := ctx.Global().Get("Promise")
	if err != nil || !promiseCtor.IsFunction() {
		return engine.Undefined(), fmt.Errorf("webcrypto: global Promise not available")
	}
	executor := engine.NewFunction("executor", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 1 {
			if f, ok := args[1].AsFunction(); ok {
				if _, err := f.Call([]engine.Value{engine.Str(msg)}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		}
		return engine.Undefined(), nil
	})
	pf, ok := promiseCtor.AsFunction()
	if !ok {
		return engine.Undefined(), fmt.Errorf("webcrypto: Promise not callable")
	}
	return pf.Call([]engine.Value{executor})
}

// registerWebCryptoExtra 在全局 crypto.subtle 上补充加密算法方法。
func registerWebCryptoExtra(ctx engine.Context) {
	gv, err := ctx.Global().Get("crypto")
	if err != nil || !gv.IsObject() {
		return
	}
	gobj, _ := gv.AsObject()
	sv, err := gobj.Get("subtle")
	if err != nil || !sv.IsObject() {
		return
	}
	sobj, _ := sv.AsObject()
	_ = sobj.Set("encrypt", engine.NewFunction("encrypt", webCryptoEncrypt(ctx)))
	_ = sobj.Set("decrypt", engine.NewFunction("decrypt", webCryptoDecrypt(ctx)))
	_ = sobj.Set("sign", engine.NewFunction("sign", webCryptoSign(ctx)))
	_ = sobj.Set("verify", engine.NewFunction("verify", webCryptoVerify(ctx)))
	_ = sobj.Set("exportKey", engine.NewFunction("exportKey", webCryptoExportKey(ctx)))
	_ = sobj.Set("deriveBits", engine.NewFunction("deriveBits", webCryptoDeriveBits(ctx)))
}

// webCryptoKeyFromArg 读取 CryptoKey（algorithm.name/usages/extractable/_keyData）。
func webCryptoKeyFromArg(v engine.Value) (algName string, usages []string, keyData []byte, ok bool) {
	o, oo := v.AsObject()
	if !oo {
		return "", nil, nil, false
	}
	if a, err := o.Get("algorithm"); err == nil {
		if ao, aok := a.AsObject(); aok {
			if n, err2 := ao.Get("name"); err2 == nil && !n.IsUndefined() {
				algName = n.String()
			}
		}
	}
	if u, err := o.Get("usages"); err == nil {
		if ua, uok := u.(*engine.ArrayValue); uok {
			for _, e := range ua.Elems() {
				usages = append(usages, e.String())
			}
		}
	}
	if k, err := o.Get("_keyData"); err == nil {
		if b, bok := engine.AsBuffer(k); bok {
			return algName, usages, b, true
		}
	}
	return algName, usages, nil, false
}

// webCryptoAlgParams 解析算法参数（字符串或 {name, iv, additionalData}）。
func webCryptoAlgParams(v engine.Value) (name string, iv, aad []byte) {
	if v.Type() == engine.TypeString {
		return v.String(), nil, nil
	}
	if o, ok := v.AsObject(); ok {
		if n, err := o.Get("name"); err == nil && !n.IsUndefined() {
			name = n.String()
		}
		if ivv, err := o.Get("iv"); err == nil {
			if b, ok2 := webCryptoData(ivv); ok2 {
				iv = b
			}
		}
		if adv, err := o.Get("additionalData"); err == nil {
			if b, ok2 := webCryptoData(adv); ok2 {
				aad = b
			}
		}
	}
	return name, iv, aad
}

// webCryptoData 提取 Buffer/TypedArray/ArrayBuffer 的字节。
func webCryptoData(v engine.Value) ([]byte, bool) {
	if b, ok := engine.AsBuffer(v); ok {
		return b, true
	}
	if ta, ok := engine.AsTypedArray(v); ok {
		return ta.Bytes(), true
	}
	if ab, ok := engine.AsArrayBuffer(v); ok {
		return ab, true
	}
	return nil, false
}

// webCryptoEncrypt：AES-GCM/AES-CBC/AES-CTR。
func webCryptoEncrypt(ctx engine.Context) engine.Func {
	return func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return webCryptoReject(ctx, "encrypt: algorithm, key and data required")
		}
		algName, iv, aad := webCryptoAlgParams(args[0])
		keyAlg, usages, keyData, ok := webCryptoKeyFromArg(args[1])
		if !ok {
			return webCryptoReject(ctx, "encrypt: invalid key")
		}
		if algName != "" && keyAlg != algName {
			return webCryptoReject(ctx, "encrypt: algorithm and key algorithm mismatch")
		}
		if !stringContains(usages, "encrypt") {
			return webCryptoReject(ctx, "encrypt: key usages do not include 'encrypt'")
		}
		data, ok := webCryptoData(args[2])
		if !ok {
			data = []byte(args[2].String())
		}
		switch keyAlg {
		case "AES-GCM":
			block, err := aes.NewCipher(keyData)
			if err != nil {
				return webCryptoReject(ctx, "encrypt: "+err.Error())
			}
			aead, err := cipher.NewGCM(block)
			if err != nil {
				return webCryptoReject(ctx, "encrypt: "+err.Error())
			}
			if len(iv) != aead.NonceSize() {
				return webCryptoReject(ctx, "encrypt: AES-GCM iv must be 12 bytes")
			}
			out := aead.Seal(nil, iv, data, aad)
			return webCryptoResolve(ctx, globals.NewBufferInstance(out))
		case "AES-CBC":
			block, err := aes.NewCipher(keyData)
			if err != nil {
				return webCryptoReject(ctx, "encrypt: "+err.Error())
			}
			if len(iv) != block.BlockSize() {
				return webCryptoReject(ctx, "encrypt: AES-CBC iv must be 16 bytes")
			}
			padded := pad(data, block.BlockSize())
			out := make([]byte, len(padded))
			cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
			return webCryptoResolve(ctx, globals.NewBufferInstance(out))
		case "AES-CTR":
			block, err := aes.NewCipher(keyData)
			if err != nil {
				return webCryptoReject(ctx, "encrypt: "+err.Error())
			}
			if len(iv) != block.BlockSize() {
				return webCryptoReject(ctx, "encrypt: AES-CTR counter must be 16 bytes")
			}
			out := make([]byte, len(data))
			cipher.NewCTR(block, iv).XORKeyStream(out, data)
			return webCryptoResolve(ctx, globals.NewBufferInstance(out))
		default:
			return webCryptoReject(ctx, "encrypt: unsupported algorithm "+keyAlg)
		}
	}
}

// webCryptoDecrypt：AES-GCM/AES-CBC/AES-CTR。
func webCryptoDecrypt(ctx engine.Context) engine.Func {
	return func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return webCryptoReject(ctx, "decrypt: algorithm, key and data required")
		}
		algName, iv, aad := webCryptoAlgParams(args[0])
		keyAlg, usages, keyData, ok := webCryptoKeyFromArg(args[1])
		if !ok {
			return webCryptoReject(ctx, "decrypt: invalid key")
		}
		if algName != "" && keyAlg != algName {
			return webCryptoReject(ctx, "decrypt: algorithm and key algorithm mismatch")
		}
		if !stringContains(usages, "decrypt") {
			return webCryptoReject(ctx, "decrypt: key usages do not include 'decrypt'")
		}
		data, ok := webCryptoData(args[2])
		if !ok {
			data = []byte(args[2].String())
		}
		switch keyAlg {
		case "AES-GCM":
			block, err := aes.NewCipher(keyData)
			if err != nil {
				return webCryptoReject(ctx, "decrypt: "+err.Error())
			}
			aead, err := cipher.NewGCM(block)
			if err != nil {
				return webCryptoReject(ctx, "decrypt: "+err.Error())
			}
			if len(iv) != aead.NonceSize() {
				return webCryptoReject(ctx, "decrypt: AES-GCM iv must be 12 bytes")
			}
			plain, err := aead.Open(nil, iv, data, aad)
			if err != nil {
				return webCryptoReject(ctx, "decrypt: authentication failed")
			}
			return webCryptoResolve(ctx, globals.NewBufferInstance(plain))
		case "AES-CBC":
			block, err := aes.NewCipher(keyData)
			if err != nil {
				return webCryptoReject(ctx, "decrypt: "+err.Error())
			}
			if len(iv) != block.BlockSize() {
				return webCryptoReject(ctx, "decrypt: AES-CBC iv must be 16 bytes")
			}
			if len(data) == 0 || len(data)%block.BlockSize() != 0 {
				return webCryptoReject(ctx, "decrypt: invalid ciphertext length")
			}
			out := make([]byte, len(data))
			cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
			return webCryptoResolve(ctx, globals.NewBufferInstance(unpad(out, block.BlockSize())))
		case "AES-CTR":
			block, err := aes.NewCipher(keyData)
			if err != nil {
				return webCryptoReject(ctx, "decrypt: "+err.Error())
			}
			if len(iv) != block.BlockSize() {
				return webCryptoReject(ctx, "decrypt: AES-CTR counter must be 16 bytes")
			}
			out := make([]byte, len(data))
			cipher.NewCTR(block, iv).XORKeyStream(out, data)
			return webCryptoResolve(ctx, globals.NewBufferInstance(out))
		default:
			return webCryptoReject(ctx, "decrypt: unsupported algorithm "+keyAlg)
		}
	}
}

// webCryptoSign：HMAC-SHA256（importKey 未保留 hash 细节，默认 SHA-256）。
func webCryptoSign(ctx engine.Context) engine.Func {
	return func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return webCryptoReject(ctx, "sign: algorithm, key and data required")
		}
		keyAlg, usages, keyData, ok := webCryptoKeyFromArg(args[1])
		if !ok {
			return webCryptoReject(ctx, "sign: invalid key")
		}
		if !stringContains(usages, "sign") {
			return webCryptoReject(ctx, "sign: key usages do not include 'sign'")
		}
		data, ok := webCryptoData(args[2])
		if !ok {
			data = []byte(args[2].String())
		}
		switch keyAlg {
		case "HMAC":
			mac := hmac.New(sha256.New, keyData)
			_, _ = mac.Write(data)
			return webCryptoResolve(ctx, globals.NewBufferInstance(mac.Sum(nil)))
		default:
			return webCryptoReject(ctx, "sign: unsupported algorithm "+keyAlg)
		}
	}
}

// webCryptoVerify：HMAC-SHA256 校验。
func webCryptoVerify(ctx engine.Context) engine.Func {
	return func(args []engine.Value) (engine.Value, error) {
		if len(args) < 4 {
			return webCryptoReject(ctx, "verify: algorithm, key, signature and data required")
		}
		keyAlg, usages, keyData, ok := webCryptoKeyFromArg(args[1])
		if !ok {
			return webCryptoReject(ctx, "verify: invalid key")
		}
		if !stringContains(usages, "verify") {
			return webCryptoReject(ctx, "verify: key usages do not include 'verify'")
		}
		sig, ok := webCryptoData(args[2])
		if !ok {
			sig = []byte(args[2].String())
		}
		data, ok := webCryptoData(args[3])
		if !ok {
			data = []byte(args[3].String())
		}
		switch keyAlg {
		case "HMAC":
			mac := hmac.New(sha256.New, keyData)
			_, _ = mac.Write(data)
			return webCryptoResolve(ctx, engine.Boolean(subtle.ConstantTimeCompare(mac.Sum(nil), sig) == 1))
		default:
			return webCryptoReject(ctx, "verify: unsupported algorithm "+keyAlg)
		}
	}
}

// webCryptoExportKey：raw 格式导出对称密钥。
func webCryptoExportKey(ctx engine.Context) engine.Func {
	return func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return webCryptoReject(ctx, "exportKey: format and key required")
		}
		format := args[0].String()
		_, _, keyData, ok := webCryptoKeyFromArg(args[1])
		if !ok {
			return webCryptoReject(ctx, "exportKey: invalid key")
		}
		switch format {
		case "raw":
			return webCryptoResolve(ctx, globals.NewBufferInstance(keyData))
		default:
			return webCryptoReject(ctx, "exportKey: unsupported format "+format)
		}
	}
}

// webCryptoDeriveBits：PBKDF2 / HKDF。
func webCryptoDeriveBits(ctx engine.Context) engine.Func {
	return func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return webCryptoReject(ctx, "deriveBits: algorithm, baseKey and length required")
		}
		var algName, hashName string
		var salt, info []byte
		iterations := 0
		if o, ok := args[0].AsObject(); ok {
			if n, err := o.Get("name"); err == nil && !n.IsUndefined() {
				algName = n.String()
			}
			if hv, err := o.Get("hash"); err == nil && !hv.IsUndefined() {
				if ho, hok := hv.AsObject(); hok {
					if hn, err2 := ho.Get("name"); err2 == nil && !hn.IsUndefined() {
						hashName = hn.String()
					}
				} else {
					hashName = hv.String()
				}
			}
			if sv, err := o.Get("salt"); err == nil {
				if b, bok := webCryptoData(sv); bok {
					salt = b
				}
			}
			if iv, err := o.Get("info"); err == nil {
				if b, bok := webCryptoData(iv); bok {
					info = b
				}
			}
			if itv, err := o.Get("iterations"); err == nil {
				iterations, _ = itv.Int()
			}
		} else if args[0].Type() == engine.TypeString {
			algName = args[0].String()
		}
		lengthBits := intArg(args, 2, 0)
		if lengthBits <= 0 || lengthBits%8 != 0 {
			return webCryptoReject(ctx, "deriveBits: length must be a positive multiple of 8")
		}
		_, _, baseKey, ok := webCryptoKeyFromArg(args[1])
		if !ok {
			return webCryptoReject(ctx, "deriveBits: invalid baseKey")
		}
		digest := webHashToDigest(hashName)
		switch algName {
		case "PBKDF2":
			out, err := pbkdf2Key(baseKey, salt, iterations, lengthBits/8, digest)
			if err != nil {
				return webCryptoReject(ctx, "deriveBits: "+err.Error())
			}
			return webCryptoResolve(ctx, globals.NewBufferInstance(out))
		case "HKDF":
			out, err := hkdfKey(digest, baseKey, salt, info, lengthBits/8)
			if err != nil {
				return webCryptoReject(ctx, "deriveBits: "+err.Error())
			}
			return webCryptoResolve(ctx, globals.NewBufferInstance(out))
		default:
			return webCryptoReject(ctx, "deriveBits: unsupported algorithm "+algName)
		}
	}
}

// webHashToDigest 把 WebCrypto 哈希名映射到 newDigest/pbkdf2Key 的名字。
func webHashToDigest(name string) string {
	switch name {
	case "SHA-1", "SHA1":
		return "sha1"
	case "SHA-384":
		return "sha384"
	case "SHA-512":
		return "sha512"
	default:
		return "sha256"
	}
}
