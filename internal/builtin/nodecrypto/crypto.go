package nodecrypto

// node:crypto 内置模块——加密（开发计划 2.19）。
//
// 提供：
//   - createHash(algorithm)：md5/sha1/sha256/sha512（update/digest 链式）
//   - createHmac(algorithm, key)：HMAC 变体
//   - createCipheriv / createDecipheriv：aes-128-cbc / aes-256-cbc
//   - pbkdf2Sync / pbkdf2：PBKDF2（RFC 2898）
//   - scryptSync / scrypt：基于 golang.org/x/crypto/scrypt
//   - randomBytes：返回 Buffer
//
// 基于 Go crypto 标准库 + golang.org/x/crypto（scrypt）。

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/subtle"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"hash"
	"math/big"

	"github.com/aluka-lang/aluka/internal/builtin/nodebase"
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gbuffer"
	"golang.org/x/crypto/scrypt"
)

// NewCrypto 构造 node:crypto 模块的导出对象。
func NewCrypto(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// X509Certificate + createPrivateKey（Node 22 语义）。
	registerX509(m)

	// --- getHashes / hash ---
	// undici and other Node packages use these convenience APIs for one-shot
	// integrity checks. Keep the list deterministic and limited to algorithms
	// implemented by this runtime.
	_ = m.Set("getHashes", engine.NewFunction("getHashes", func(args []engine.Value) (engine.Value, error) {
		return engine.NewArray([]engine.Value{
			engine.Str("md5"), engine.Str("sha1"), engine.Str("sha256"),
			engine.Str("sha384"), engine.Str("sha512"),
		}), nil
	}))
	_ = m.Set("hash", engine.NewFunction("hash", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("hash: algorithm and data required")
		}
		h, err := newDigest(args[0].String())
		if err != nil {
			return engine.Undefined(), err
		}
		data, err := nodebase.CryptoBytes(args[1])
		if err != nil {
			return engine.Undefined(), err
		}
		_, _ = h.Write(data)
		sum := h.Sum(nil)
		if len(args) > 2 && !args[2].IsUndefined() && !args[2].IsNull() {
			switch encoding := args[2].String(); encoding {
			case "buffer":
				return gbuffer.NewBufferInstance(sum), nil
			case "hex":
				return engine.Str(hex.EncodeToString(sum)), nil
			case "base64":
				return engine.Str(base64Encode(sum)), nil
			default:
				return engine.Undefined(), fmt.Errorf("hash: unsupported output encoding %q", encoding)
			}
		}
		// Node 22 实测：不传 outputEncoding 时返回 hex 字符串。
		return engine.Str(hex.EncodeToString(sum)), nil
	}))

	// --- createHash ---
	_ = m.Set("createHash", engine.NewFunction("createHash", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("createHash: algorithm required")
		}
		h, err := newDigest(args[0].String())
		if err != nil {
			return engine.Undefined(), err
		}
		return newDigestObject(h, args[0].String()), nil
	}))

	// --- createHmac ---
	_ = m.Set("createHmac", engine.NewFunction("createHmac", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("createHmac: algorithm and key required")
		}
		algorithm := args[0].String()
		key, err := nodebase.CryptoBytes(args[1])
		if err != nil {
			return engine.Undefined(), err
		}
		h, err := newDigest(algorithm)
		if err != nil {
			return engine.Undefined(), err
		}
		mac := hmac.New(func() hash.Hash { return cloneHash(h, algorithm) }, key)
		return newDigestObject(mac, algorithm), nil
	}))

	// --- createCipheriv / createDecipheriv ---
	_ = m.Set("createCipheriv", engine.NewFunction("createCipheriv", func(args []engine.Value) (engine.Value, error) {
		return newCipherObject(ctx, args, false)
	}))
	_ = m.Set("createDecipheriv", engine.NewFunction("createDecipheriv", func(args []engine.Value) (engine.Value, error) {
		return newCipherObject(ctx, args, true)
	}))

	// --- pbkdf2Sync / pbkdf2 ---
	_ = m.Set("pbkdf2Sync", engine.NewFunction("pbkdf2Sync", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 4 {
			return engine.Undefined(), fmt.Errorf("pbkdf2Sync: password, salt, iterations, keylen required")
		}
		password, err := nodebase.CryptoBytes(args[0])
		if err != nil {
			return engine.Undefined(), err
		}
		salt, err := nodebase.CryptoBytes(args[1])
		if err != nil {
			return engine.Undefined(), err
		}
		iterations := nodebase.IntArg(args, 2, 0)
		keylen := nodebase.IntArg(args, 3, 0)
		digest := "sha1"
		if len(args) > 4 {
			digest = args[4].String()
		}
		out, err := pbkdf2Key(password, salt, iterations, keylen, digest)
		if err != nil {
			return engine.Undefined(), err
		}
		return gbuffer.NewBufferInstance(out), nil
	}))
	_ = m.Set("pbkdf2", engine.NewFunction("pbkdf2", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 5 {
			return engine.Undefined(), fmt.Errorf("pbkdf2: password, salt, iterations, keylen, digest, callback required")
		}
		password, err := nodebase.CryptoBytes(args[0])
		if err != nil {
			return engine.Undefined(), err
		}
		salt, err := nodebase.CryptoBytes(args[1])
		if err != nil {
			return engine.Undefined(), err
		}
		iterations := nodebase.IntArg(args, 2, 0)
		keylen := nodebase.IntArg(args, 3, 0)
		digest := args[4].String()
		cb := args[5]
		release := ctx.AddRef()
		go func() {
			out, err := pbkdf2Key(password, salt, iterations, keylen, digest)
			ctx.PostTask(func() {
				defer release()
				if cb.IsFunction() {
					if f, ok := cb.AsFunction(); ok {
						if err != nil {
							if _, err := f.Call([]engine.Value{engine.Str(err.Error())}); err != nil {
								interpreter.ReportUncaught(nil, err)
							}
						} else {
							if _, err := f.Call([]engine.Value{engine.Null(), gbuffer.NewBufferInstance(out)}); err != nil {
								interpreter.ReportUncaught(nil, err)
							}
						}
					}
				}
			})
		}()
		return engine.Undefined(), nil
	}))

	// --- scryptSync / scrypt ---
	_ = m.Set("scryptSync", engine.NewFunction("scryptSync", func(args []engine.Value) (engine.Value, error) {
		password, salt, keylen, N, r, p, err := parseScryptArgs(args)
		if err != nil {
			return engine.Undefined(), err
		}
		dk, err := scrypt.Key(password, salt, N, r, p, keylen)
		if err != nil {
			return engine.Undefined(), fmt.Errorf("scrypt: %w", err)
		}
		return gbuffer.NewBufferInstance(dk), nil
	}))
	_ = m.Set("scrypt", engine.NewFunction("scrypt", func(args []engine.Value) (engine.Value, error) {
		password, salt, keylen, N, r, p, err := parseScryptArgs(args)
		if err != nil {
			return engine.Undefined(), err
		}
		// 回调：最后一个函数参数。
		var cb engine.Value
		for i := len(args) - 1; i >= 0; i-- {
			if args[i].IsFunction() {
				cb = args[i]
				break
			}
		}
		release := ctx.AddRef()
		go func() {
			dk, err := scrypt.Key(password, salt, N, r, p, keylen)
			ctx.PostTask(func() {
				defer release()
				if cb != nil && cb.IsFunction() {
					if f, ok := cb.AsFunction(); ok {
						if err != nil {
							if _, err := f.Call([]engine.Value{engine.Str(err.Error())}); err != nil {
								interpreter.ReportUncaught(nil, err)
							}
						} else {
							if _, err := f.Call([]engine.Value{engine.Null(), gbuffer.NewBufferInstance(dk)}); err != nil {
								interpreter.ReportUncaught(nil, err)
							}
						}
					}
				}
			})
		}()
		return engine.Undefined(), nil
	}))

	// --- randomBytes ---
	_ = m.Set("randomBytes", engine.NewFunction("randomBytes", func(args []engine.Value) (engine.Value, error) {
		size := nodebase.IntArg(args, 0, 0)
		if size <= 0 || size > 1<<20 {
			return engine.Undefined(), fmt.Errorf("randomBytes: invalid size %d", size)
		}
		buf := make([]byte, size)
		if _, err := rand.Read(buf); err != nil {
			return engine.Undefined(), err
		}
		return gbuffer.NewBufferInstance(buf), nil
	}))

	// --- randomInt([min,] max[, callback]) ---
	_ = m.Set("randomInt", engine.NewFunction("randomInt", func(args []engine.Value) (engine.Value, error) {
		n := len(args)
		var cb engine.Value
		if n > 0 && args[n-1].IsFunction() {
			cb = args[n-1]
			n--
		}
		var min, max int64
		switch n {
		case 1:
			max = int64(nodebase.IntArg(args, 0, 0))
		case 2:
			min = int64(nodebase.IntArg(args, 0, 0))
			max = int64(nodebase.IntArg(args, 1, 0))
		default:
			return engine.Undefined(), fmt.Errorf("randomInt: expected max or (min, max)")
		}
		if max <= min {
			return engine.Undefined(), fmt.Errorf("%w: randomInt: (max - min) must be positive", engine.ErrRangeError)
		}
		if uint64(max-min) >= 1<<48 {
			return engine.Undefined(), fmt.Errorf("%w: randomInt: (max - min) must be less than 2**48", engine.ErrRangeError)
		}
		if cb != nil && cb.IsFunction() {
			release := ctx.AddRef()
			go func() {
				v, err := randomIntRange(min, max)
				ctx.PostTask(func() {
					defer release()
					if f, ok := cb.AsFunction(); ok {
						if err != nil {
							if _, err := f.Call([]engine.Value{engine.Str(err.Error())}); err != nil {
								interpreter.ReportUncaught(nil, err)
							}
						} else {
							if _, err := f.Call([]engine.Value{engine.Null(), engine.Number(float64(v))}); err != nil {
								interpreter.ReportUncaught(nil, err)
							}
						}
					}
				})
			}()
			return engine.Undefined(), nil
		}
		v, err := randomIntRange(min, max)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Number(float64(v)), nil
	}))

	// --- randomUUID() → UUID v4 ---
	_ = m.Set("randomUUID", engine.NewFunction("randomUUID", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(randomUUID4()), nil
	}))

	// --- randomFillSync / randomFill ---
	_ = m.Set("randomFillSync", engine.NewFunction("randomFillSync", func(args []engine.Value) (engine.Value, error) {
		target, offset, size, ok := randomFillArgs(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("randomFillSync: invalid buffer or bounds")
		}
		if _, err := rand.Read(target[offset : offset+size]); err != nil {
			return engine.Undefined(), err
		}
		return args[0], nil
	}))
	_ = m.Set("randomFill", engine.NewFunction("randomFill", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 || !args[len(args)-1].IsFunction() {
			return engine.Undefined(), fmt.Errorf("randomFill: callback required")
		}
		cb := args[len(args)-1]
		callArgs := args[:len(args)-1]
		target, offset, size, ok := randomFillArgs(callArgs)
		if !ok {
			return engine.Undefined(), fmt.Errorf("randomFill: invalid buffer or bounds")
		}
		first := callArgs[0]
		release := ctx.AddRef()
		go func() {
			_, err := rand.Read(target[offset : offset+size])
			ctx.PostTask(func() {
				defer release()
				if f, ok := cb.AsFunction(); ok {
					if err != nil {
						if _, err := f.Call([]engine.Value{engine.Str(err.Error())}); err != nil {
							interpreter.ReportUncaught(nil, err)
						}
					} else {
						if _, err := f.Call([]engine.Value{engine.Null(), first}); err != nil {
							interpreter.ReportUncaught(nil, err)
						}
					}
				}
			})
		}()
		return engine.Undefined(), nil
	}))

	// --- timingSafeEqual(a, b) ---
	_ = m.Set("timingSafeEqual", engine.NewFunction("timingSafeEqual", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("timingSafeEqual: two buffers required")
		}
		a, aok := engine.AsBuffer(args[0])
		b, bok := engine.AsBuffer(args[1])
		if !aok || !bok {
			return engine.Undefined(), fmt.Errorf("%w: timingSafeEqual: arguments must be Buffer, TypedArray, or DataView", engine.ErrTypeError)
		}
		if len(a) != len(b) {
			return engine.Undefined(), fmt.Errorf("%w: timingSafeEqual: input buffers must have the same length", engine.ErrRangeError)
		}
		return engine.Boolean(subtle.ConstantTimeCompare(a, b) == 1), nil
	}))

	// --- createSecretKey(key) → KeyObject{type:'secret'} ---
	_ = m.Set("createSecretKey", engine.NewFunction("createSecretKey", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("createSecretKey: key argument required")
		}
		key, err := nodebase.CryptoBytes(args[0])
		if err != nil {
			return engine.Undefined(), err
		}
		return newSecretKeyObject(key), nil
	}))

	// --- hkdfSync / hkdf（RFC 5869）---
	_ = m.Set("hkdfSync", engine.NewFunction("hkdfSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 5 {
			return engine.Undefined(), fmt.Errorf("hkdfSync: digest, ikm, salt, info, keylen required")
		}
		digest := args[0].String()
		ikm, err := nodebase.CryptoBytes(args[1])
		if err != nil {
			return engine.Undefined(), err
		}
		salt, err := nodebase.CryptoBytes(args[2])
		if err != nil {
			return engine.Undefined(), err
		}
		info, err := nodebase.CryptoBytes(args[3])
		if err != nil {
			return engine.Undefined(), err
		}
		keylen := nodebase.IntArg(args, 4, 0)
		out, err := hkdfKey(digest, ikm, salt, info, keylen)
		if err != nil {
			return engine.Undefined(), err
		}
		return gbuffer.NewBufferInstance(out), nil
	}))
	_ = m.Set("hkdf", engine.NewFunction("hkdf", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 6 {
			return engine.Undefined(), fmt.Errorf("hkdf: digest, ikm, salt, info, keylen, callback required")
		}
		digest := args[0].String()
		ikm, err := nodebase.CryptoBytes(args[1])
		if err != nil {
			return engine.Undefined(), err
		}
		salt, err := nodebase.CryptoBytes(args[2])
		if err != nil {
			return engine.Undefined(), err
		}
		info, err := nodebase.CryptoBytes(args[3])
		if err != nil {
			return engine.Undefined(), err
		}
		keylen := nodebase.IntArg(args, 4, 0)
		cb := args[5]
		release := ctx.AddRef()
		go func() {
			out, err := hkdfKey(digest, ikm, salt, info, keylen)
			ctx.PostTask(func() {
				defer release()
				if cb.IsFunction() {
					if f, ok := cb.AsFunction(); ok {
						if err != nil {
							if _, err := f.Call([]engine.Value{engine.Str(err.Error())}); err != nil {
								interpreter.ReportUncaught(nil, err)
							}
						} else {
							if _, err := f.Call([]engine.Value{engine.Null(), gbuffer.NewBufferInstance(out)}); err != nil {
								interpreter.ReportUncaught(nil, err)
							}
						}
					}
				}
			})
		}()
		return engine.Undefined(), nil
	}))

	// --- checkPrimeSync / checkPrime（big.Int ProbablyPrime）---
	checkPrimeFn := func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		var bi *big.Int
		if b, ok := engine.BigIntValue(args[0]); ok {
			bi = b
		} else if n, ok := args[0].Int(); ok {
			bi = big.NewInt(int64(n))
		} else {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(bi.ProbablyPrime(64)), nil
	}
	_ = m.Set("checkPrimeSync", engine.NewFunction("checkPrimeSync", checkPrimeFn))
	_ = m.Set("checkPrime", engine.NewFunction("checkPrime", func(args []engine.Value) (engine.Value, error) {
		cbIdx := len(args) - 1
		if cbIdx < 0 || !args[cbIdx].IsFunction() {
			return engine.Undefined(), fmt.Errorf("checkPrime: callback required")
		}
		cb := args[cbIdx]
		cand := args[:cbIdx]
		release := ctx.AddRef()
		go func() {
			res, err := checkPrimeFn(cand)
			ctx.PostTask(func() {
				defer release()
				if f, ok := cb.AsFunction(); ok {
					if err != nil {
						if _, err := f.Call([]engine.Value{engine.Str(err.Error())}); err != nil {
							interpreter.ReportUncaught(nil, err)
						}
					} else {
						if _, err := f.Call([]engine.Value{engine.Null(), res}); err != nil {
							interpreter.ReportUncaught(nil, err)
						}
					}
				}
			})
		}()
		return engine.Undefined(), nil
	}))

	// --- getCiphers：返回本实现支持的对称算法（Node 全列表由 gaps.md 跟踪）---
	_ = m.Set("getCiphers", engine.NewFunction("getCiphers", func(args []engine.Value) (engine.Value, error) {
		names := []string{"aes-128-cbc", "aes-192-cbc", "aes-256-cbc", "aes-128-ecb",
			"aes-256-ecb", "aes-128-ctr", "aes-256-ctr", "aes-128-gcm", "aes-256-gcm"}
		return engine.NewArray(nodebase.StringsToValues(names)), nil
	}))

	// --- createSign / createVerify（RSA PKCS1v15）---
	_ = m.Set("createSign", engine.NewFunction("createSign", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("createSign: algorithm required")
		}
		algorithm := args[0].String()
		digestHash, ok := digestHashID(algorithm)
		if !ok {
			return engine.Undefined(), fmt.Errorf("createSign: unsupported algorithm %q", algorithm)
		}
		return newSignVerifyObject(false, algorithm, digestHash), nil
	}))
	_ = m.Set("createVerify", engine.NewFunction("createVerify", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("createVerify: algorithm required")
		}
		algorithm := args[0].String()
		digestHash, ok := digestHashID(algorithm)
		if !ok {
			return engine.Undefined(), fmt.Errorf("createVerify: unsupported algorithm %q", algorithm)
		}
		return newSignVerifyObject(true, algorithm, digestHash), nil
	}))

	// --- createPublicKey(key)：RSA 公钥 PEM / KeyObject / 私钥 → KeyObject ---
	_ = m.Set("createPublicKey", engine.NewFunction("createPublicKey", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("createPublicKey: key argument required")
		}
		var data []byte
		if o, ok := args[0].AsObject(); ok {
			if p, e := o.Get("__alukaKeyPEM"); e == nil && !p.IsUndefined() {
				data = []byte(p.String())
			}
		}
		if data == nil {
			var err error
			data, err = nodebase.CryptoBytes(args[0])
			if err != nil {
				return engine.Undefined(), err
			}
		}
		// 公钥 PEM 直接采用。
		if _, err := parsePublicKeyPEM(data); err == nil {
			ko := engine.NewObject()
			_ = ko.Set("type", engine.Str("public"))
			_ = ko.Set("asymmetricKeyType", engine.Str("rsa"))
			_ = ko.Set("__alukaKeyPEM", engine.Str(string(data)))
			return ko, nil
		}
		// 私钥 PEM → 派生公钥。
		if priv, err := parsePrivateKeyPEM(data); err == nil {
			pubDER, derr := x509.MarshalPKIXPublicKey(&priv.PublicKey)
			if derr != nil {
				return engine.Undefined(), derr
			}
			pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
			ko := engine.NewObject()
			_ = ko.Set("type", engine.Str("public"))
			_ = ko.Set("asymmetricKeyType", engine.Str("rsa"))
			_ = ko.Set("__alukaKeyPEM", engine.Str(string(pubPEM)))
			return ko, nil
		}
		return engine.Undefined(), fmt.Errorf("%w: createPublicKey: invalid key", engine.ErrTypeError)
	}))

	// --- generateKeyPairSync(type, options)：RSA 密钥对 ---
	_ = m.Set("generateKeyPairSync", engine.NewFunction("generateKeyPairSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("generateKeyPairSync: type required")
		}
		typ := args[0].String()
		if typ != "rsa" {
			return engine.Undefined(), fmt.Errorf("generateKeyPairSync: unsupported type %q", typ)
		}
		modulusLength := 2048
		if len(args) > 1 && args[1].IsObject() {
			if o, ok := args[1].AsObject(); ok {
				if v, e := o.Get("modulusLength"); e == nil && !v.IsUndefined() {
					if n, ok2 := v.Int(); ok2 {
						modulusLength = n
					}
				}
			}
		}
		priv, err := rsa.GenerateKey(rand.Reader, modulusLength)
		if err != nil {
			return engine.Undefined(), err
		}
		privDER, err := x509.MarshalPKCS8PrivateKey(priv)
		if err != nil {
			return engine.Undefined(), err
		}
		pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
		if err != nil {
			return engine.Undefined(), err
		}
		privPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})
		pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
		out := engine.NewObject()
		_ = out.Set("publicKey", newAsymKeyObject("public", pubPEM))
		_ = out.Set("privateKey", newAsymKeyObject("private", privPEM))
		return out, nil
	}))

	// --- getRandomValues：别名到全局 crypto.getRandomValues（未注册时回退）---
	if gv, err := ctx.Global().Get("crypto"); err == nil && gv.IsObject() {
		if gobj, ok := gv.AsObject(); ok {
			if rv, err2 := gobj.Get("getRandomValues"); err2 == nil && rv.IsFunction() {
				_ = m.Set("getRandomValues", rv)
			}
		}
		_ = m.Set("webcrypto", gv)
	}
	mo, _ := m.AsObject()
	if rv, err := mo.Get("getRandomValues"); err != nil || !rv.IsFunction() {
		_ = m.Set("getRandomValues", engine.NewFunction("getRandomValues", func(args []engine.Value) (engine.Value, error) {
			if len(args) > 0 {
				if b, ok := engine.AsBuffer(args[0]); ok {
					_, _ = rand.Read(b)
					return args[0], nil
				}
				if ta, ok := engine.AsTypedArray(args[0]); ok {
					_, _ = rand.Read(ta.Bytes())
					return args[0], nil
				}
			}
			return engine.Undefined(), fmt.Errorf("getRandomValues: expects a typed array")
		}))
	}

	// 增强全局 crypto.subtle（encrypt/decrypt/sign/verify/exportKey/deriveBits）。
	registerWebCryptoExtra(ctx)

	return m, nil
}

// stringsToValues 在 child_process.go 中定义（字符串切片 → engine.Value 切片）。
// crypto.go 的 getCiphers 复用它。

// 其余 node:crypto 实现按职责分文件：
//   - crypto_hash.go      摘要 / HMAC / Sign·Verify 对象
//   - crypto_cipher.go    对称加解密（CBC/ECB/CTR/GCM）与分组填充
//   - crypto_kdf.go       pbkdf2 / scrypt / hkdf 与 KeyObject
//   - crypto_random.go    randomBytes/randomInt/randomUUID 与编码 helper
//   - crypto_webcrypto.go crypto.subtle（Web Cryptography API）
