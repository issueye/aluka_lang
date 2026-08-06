package builtin

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
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"hash"
	"io"
	"math/big"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
	"golang.org/x/crypto/hkdf"
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
		data, err := cryptoBytes(args[1])
		if err != nil {
			return engine.Undefined(), err
		}
		_, _ = h.Write(data)
		sum := h.Sum(nil)
		if len(args) > 2 && !args[2].IsUndefined() && !args[2].IsNull() {
			switch encoding := args[2].String(); encoding {
			case "buffer":
				return globals.NewBufferInstance(sum), nil
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
		key, err := cryptoBytes(args[1])
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
		password, err := cryptoBytes(args[0])
		if err != nil {
			return engine.Undefined(), err
		}
		salt, err := cryptoBytes(args[1])
		if err != nil {
			return engine.Undefined(), err
		}
		iterations := intArg(args, 2, 0)
		keylen := intArg(args, 3, 0)
		digest := "sha1"
		if len(args) > 4 {
			digest = args[4].String()
		}
		out, err := pbkdf2Key(password, salt, iterations, keylen, digest)
		if err != nil {
			return engine.Undefined(), err
		}
		return globals.NewBufferInstance(out), nil
	}))
	_ = m.Set("pbkdf2", engine.NewFunction("pbkdf2", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 5 {
			return engine.Undefined(), fmt.Errorf("pbkdf2: password, salt, iterations, keylen, digest, callback required")
		}
		password, err := cryptoBytes(args[0])
		if err != nil {
			return engine.Undefined(), err
		}
		salt, err := cryptoBytes(args[1])
		if err != nil {
			return engine.Undefined(), err
		}
		iterations := intArg(args, 2, 0)
		keylen := intArg(args, 3, 0)
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
							_, _ = f.Call([]engine.Value{engine.Str(err.Error())})
						} else {
							_, _ = f.Call([]engine.Value{engine.Null(), globals.NewBufferInstance(out)})
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
		return globals.NewBufferInstance(dk), nil
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
							_, _ = f.Call([]engine.Value{engine.Str(err.Error())})
						} else {
							_, _ = f.Call([]engine.Value{engine.Null(), globals.NewBufferInstance(dk)})
						}
					}
				}
			})
		}()
		return engine.Undefined(), nil
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
		return globals.NewBufferInstance(buf), nil
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
			max = int64(intArg(args, 0, 0))
		case 2:
			min = int64(intArg(args, 0, 0))
			max = int64(intArg(args, 1, 0))
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
							_, _ = f.Call([]engine.Value{engine.Str(err.Error())})
						} else {
							_, _ = f.Call([]engine.Value{engine.Null(), engine.Number(float64(v))})
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
						_, _ = f.Call([]engine.Value{engine.Str(err.Error())})
					} else {
						_, _ = f.Call([]engine.Value{engine.Null(), first})
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
		key, err := cryptoBytes(args[0])
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
		ikm, err := cryptoBytes(args[1])
		if err != nil {
			return engine.Undefined(), err
		}
		salt, err := cryptoBytes(args[2])
		if err != nil {
			return engine.Undefined(), err
		}
		info, err := cryptoBytes(args[3])
		if err != nil {
			return engine.Undefined(), err
		}
		keylen := intArg(args, 4, 0)
		out, err := hkdfKey(digest, ikm, salt, info, keylen)
		if err != nil {
			return engine.Undefined(), err
		}
		return globals.NewBufferInstance(out), nil
	}))
	_ = m.Set("hkdf", engine.NewFunction("hkdf", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 6 {
			return engine.Undefined(), fmt.Errorf("hkdf: digest, ikm, salt, info, keylen, callback required")
		}
		digest := args[0].String()
		ikm, err := cryptoBytes(args[1])
		if err != nil {
			return engine.Undefined(), err
		}
		salt, err := cryptoBytes(args[2])
		if err != nil {
			return engine.Undefined(), err
		}
		info, err := cryptoBytes(args[3])
		if err != nil {
			return engine.Undefined(), err
		}
		keylen := intArg(args, 4, 0)
		cb := args[5]
		release := ctx.AddRef()
		go func() {
			out, err := hkdfKey(digest, ikm, salt, info, keylen)
			ctx.PostTask(func() {
				defer release()
				if cb.IsFunction() {
					if f, ok := cb.AsFunction(); ok {
						if err != nil {
							_, _ = f.Call([]engine.Value{engine.Str(err.Error())})
						} else {
							_, _ = f.Call([]engine.Value{engine.Null(), globals.NewBufferInstance(out)})
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
						_, _ = f.Call([]engine.Value{engine.Str(err.Error())})
					} else {
						_, _ = f.Call([]engine.Value{engine.Null(), res})
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
		return engine.NewArray(stringsToValues(names)), nil
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
			data, err = cryptoBytes(args[0])
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

// --- 新增辅助：random/hkdf/KeyObject/Sign/Verify --------------------------

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
	offset = intArg(args, 1, 0)
	if offset < 0 || offset > len(target) {
		return nil, 0, 0, false
	}
	size = len(target) - offset
	if len(args) > 2 {
		size = intArg(args, 2, size)
	}
	if size < 0 || offset+size > len(target) {
		return nil, 0, 0, false
	}
	return target, offset, size, true
}

// hkdfKey 计算 HKDF（RFC 5869）。digest: sha256/sha1/sha384/sha512/md5。
func hkdfKey(digest string, ikm, salt, info []byte, keylen int) ([]byte, error) {
	if keylen <= 0 {
		return nil, fmt.Errorf("hkdf: keylen must be positive")
	}
	h, err := newDigest(digest)
	if err != nil {
		return nil, err
	}
	reader := hkdf.New(func() hash.Hash { return cloneHash(h, digest) }, ikm, salt, info)
	out := make([]byte, keylen)
	if _, err := io.ReadFull(reader, out); err != nil {
		return nil, fmt.Errorf("hkdf: %w", err)
	}
	return out, nil
}

// newSecretKeyObject 构造 secret 类型 KeyObject。
func newSecretKeyObject(key []byte) engine.Value {
	ko := engine.NewObject()
	_ = ko.Set("type", engine.Str("secret"))
	_ = ko.Set("symmetricKeySize", engine.IntValue(len(key)))
	_ = ko.Set("export", engine.NewFunction("export", func(args []engine.Value) (engine.Value, error) {
		return globals.NewBufferInstance(key), nil
	}))
	return ko
}

// newAsymKeyObject 构造带 PEM 的 RSA 公/私钥 KeyObject。
func newAsymKeyObject(typeStr string, pemBytes []byte) engine.Value {
	ko := engine.NewObject()
	_ = ko.Set("type", engine.Str(typeStr))
	_ = ko.Set("asymmetricKeyType", engine.Str("rsa"))
	_ = ko.Set("__alukaKeyPEM", engine.Str(string(pemBytes)))
	_ = ko.Set("export", engine.NewFunction("export", func(args []engine.Value) (engine.Value, error) {
		return globals.NewBufferInstance(pemBytes), nil
	}))
	return ko
}

// digestHashID 把算法字符串映射到 crypto.Hash（RSA 签名用）。
func digestHashID(algorithm string) (crypto.Hash, bool) {
	switch strings.ToLower(algorithm) {
	case "sha1", "sha-1", "rsa-sha1", "rsa-sha1sign":
		return crypto.SHA1, true
	case "sha256", "sha-256", "rsa-sha256":
		return crypto.SHA256, true
	case "sha384", "sha-384", "rsa-sha384":
		return crypto.SHA384, true
	case "sha512", "sha-512", "rsa-sha512":
		return crypto.SHA512, true
	case "md5", "rsa-md5":
		return crypto.MD5, true
	default:
		return 0, false
	}
}

// newSignVerifyObject 构造 Sign/Verify 对象。
// isVerify=true 时 verify(key, signature)；否则 sign(key)。
func newSignVerifyObject(isVerify bool, algorithm string, h crypto.Hash) engine.Value {
	obj := engine.NewObject()
	digestHash, _ := newDigest(algorithm)
	_ = obj.Set("update", engine.NewFunction("update", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			data, err := cryptoBytes(args[0])
			if err != nil {
				return engine.Undefined(), err
			}
			_, _ = digestHash.Write(data)
		}
		return obj, nil
	}))
	if isVerify {
		_ = obj.Set("verify", engine.NewFunction("verify", func(args []engine.Value) (engine.Value, error) {
			if len(args) < 2 {
				return engine.Boolean(false), nil
			}
			pub, ok := x509PublicKeyArg(args[0])
			if !ok {
				return engine.Boolean(false), nil
			}
			rpub, ok2 := pub.(*rsa.PublicKey)
			if !ok2 {
				return engine.Boolean(false), nil
			}
			sig, err := cryptoBytes(args[1])
			if err != nil {
				return engine.Undefined(), err
			}
			err = rsa.VerifyPKCS1v15(rpub, h, digestHash.Sum(nil), sig)
			return engine.Boolean(err == nil), nil
		}))
	} else {
		_ = obj.Set("sign", engine.NewFunction("sign", func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 {
				return engine.Undefined(), fmt.Errorf("sign: private key required")
			}
			priv, ok := x509PrivateKeyArg(args[0])
			if !ok {
				return engine.Undefined(), fmt.Errorf("%w: sign: invalid private key", engine.ErrTypeError)
			}
			sig, err := rsa.SignPKCS1v15(rand.Reader, priv, h, digestHash.Sum(nil))
			if err != nil {
				return engine.Undefined(), err
			}
			return globals.NewBufferInstance(sig), nil
		}))
	}
	return obj
}

// stringsToValues 在 child_process.go 中定义（字符串切片 → engine.Value 切片）。
// crypto.go 的 getCiphers 复用它。

// --- Web Crypto subtle 增强（encrypt/decrypt/sign/verify/exportKey/deriveBits）---
// 全局 crypto.subtle 由 globals.NewWebCrypto 构造（digest/importKey/generateKey）。
// 这里在不修改 globals 包的前提下，动态为 subtle 补充其余核心方法。

// webCryptoResolve 构造一个已 resolve 的 Promise。
func webCryptoResolve(ctx engine.Context, v engine.Value) (engine.Value, error) {
	promiseCtor, err := ctx.Global().Get("Promise")
	if err != nil || !promiseCtor.IsFunction() {
		return engine.Undefined(), fmt.Errorf("webcrypto: global Promise not available")
	}
	executor := engine.NewFunction("executor", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			if f, ok := args[0].AsFunction(); ok {
				_, _ = f.Call([]engine.Value{v})
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
				_, _ = f.Call([]engine.Value{engine.Str(msg)})
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

// stringContains 字符串切片包含判断。
func stringContains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// --- 摘要 / HMAC ----------------------------------------------------------

// newDigest 按算法创建 hash.Hash。
func newDigest(algorithm string) (hash.Hash, error) {
	switch algorithm {
	case "md5":
		return md5.New(), nil
	case "sha1":
		return sha1.New(), nil
	case "sha256":
		return sha256.New(), nil
	case "sha384":
		return sha512.New384(), nil
	case "sha512":
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("createHash: unsupported algorithm %q", algorithm)
	}
}

// cloneHash 返回同算法的新 hash（用于 HMAC 每次迭代）。
func cloneHash(h hash.Hash, algorithm string) hash.Hash {
	n, err := newDigest(algorithm)
	if err != nil {
		return h
	}
	return n
}

// newDigestObject 构造 Hash/Hmac 对象（update/digest 链式）。
func newDigestObject(h hash.Hash, algorithm string) engine.Value {
	obj := engine.NewObject()
	_ = obj.Set("algorithm", engine.Str(algorithm))

	_ = obj.Set("update", engine.NewFunction("update", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			data, err := cryptoBytes(args[0])
			if err != nil {
				return engine.Undefined(), err
			}
			h.Write(data)
		}
		return obj, nil
	}))

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
			return engine.Str(base64Encode(sum)), nil
		default:
			return globals.NewBufferInstance(sum), nil
		}
	}))

	return obj
}

// --- 对称加密（CBC/ECB/CTR/GCM） ----------------------------------------------

// cipherState 是 Cipher/Decipher 的内部状态（累积数据 + 最终处理）。
type cipherState struct {
	block   cipher.Block
	iv      []byte
	decrypt bool
	mode    string // cbc / ecb / ctr / gcm
	aead    cipher.AEAD
	authTag []byte // gcm：encrypt 计算出的 tag（getAuthTag）；decrypt 为 setAuthTag 设置
	buf     []byte
}

// newCipherObject 构造 Cipher/Decipher 对象。
func newCipherObject(ctx engine.Context, args []engine.Value, decrypt bool) (engine.Value, error) {
	if len(args) < 3 {
		return engine.Undefined(), fmt.Errorf("createCipheriv: algorithm, key, iv required")
	}
	algorithm := args[0].String()
	key, err := cryptoBytes(args[1])
	if err != nil {
		return engine.Undefined(), err
	}
	var iv []byte
	if len(args) > 2 && !args[2].IsNull() && !args[2].IsUndefined() {
		iv, err = cryptoBytes(args[2])
		if err != nil {
			return engine.Undefined(), err
		}
	}
	alg := strings.ToLower(algorithm)
	keyLen := -1
	switch alg {
	case "aes-128-cbc", "aes-128-ecb", "aes-128-ctr", "aes-128-gcm":
		keyLen = 16
	case "aes-192-cbc":
		keyLen = 24
	case "aes-256-cbc", "aes-256-ecb", "aes-256-ctr", "aes-256-gcm":
		keyLen = 32
	default:
		return engine.Undefined(), fmt.Errorf("createCipheriv: unsupported algorithm %q", algorithm)
	}
	if len(key) != keyLen {
		return engine.Undefined(), fmt.Errorf("createCipheriv: key must be %d bytes", keyLen)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return engine.Undefined(), err
	}
	state := &cipherState{block: block, iv: iv, decrypt: decrypt}
	switch {
	case strings.HasSuffix(alg, "-cbc"):
		state.mode = "cbc"
		if len(iv) != block.BlockSize() {
			return engine.Undefined(), fmt.Errorf("createCipheriv: iv must be %d bytes", block.BlockSize())
		}
	case strings.HasSuffix(alg, "-ecb"):
		state.mode = "ecb"
	case strings.HasSuffix(alg, "-ctr"):
		state.mode = "ctr"
		if len(iv) != block.BlockSize() {
			return engine.Undefined(), fmt.Errorf("createCipheriv: iv must be %d bytes", block.BlockSize())
		}
	case strings.HasSuffix(alg, "-gcm"):
		state.mode = "gcm"
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return engine.Undefined(), err
		}
		state.aead = aead
		if len(iv) != aead.NonceSize() {
			return engine.Undefined(), fmt.Errorf("createCipheriv: iv must be %d bytes", aead.NonceSize())
		}
	default:
		return engine.Undefined(), fmt.Errorf("createCipheriv: unsupported algorithm %q", algorithm)
	}

	obj := engine.NewObject()
	_ = obj.Set("update", engine.NewFunction("update", func(uargs []engine.Value) (engine.Value, error) {
		if len(uargs) > 0 {
			data, err := cryptoBytes(uargs[0])
			if err != nil {
				return engine.Undefined(), err
			}
			state.buf = append(state.buf, data...)
		}
		return globals.NewBufferInstance(nil), nil
	}))
	_ = obj.Set("final", engine.NewFunction("final", func(uargs []engine.Value) (engine.Value, error) {
		out, err := state.finalize()
		if err != nil {
			return engine.Undefined(), err
		}
		return globals.NewBufferInstance(out), nil
	}))
	if state.mode == "gcm" {
		_ = obj.Set("getAuthTag", engine.NewFunction("getAuthTag", func(uargs []engine.Value) (engine.Value, error) {
			if len(state.authTag) == 0 {
				return engine.Undefined(), fmt.Errorf("getAuthTag: no auth tag available (call final first)")
			}
			return globals.NewBufferInstance(state.authTag), nil
		}))
		_ = obj.Set("setAuthTag", engine.NewFunction("setAuthTag", func(uargs []engine.Value) (engine.Value, error) {
			if len(uargs) == 0 {
				return engine.Undefined(), fmt.Errorf("setAuthTag: tag argument required")
			}
			tag, err := cryptoBytes(uargs[0])
			if err != nil {
				return engine.Undefined(), err
			}
			state.authTag = tag
			return obj, nil
		}))
	}
	return obj, nil
}

// finalize 处理累积数据（按模式加解密）。
func (c *cipherState) finalize() ([]byte, error) {
	bs := c.block.BlockSize()
	switch c.mode {
	case "cbc":
		if c.decrypt {
			if len(c.buf) == 0 || len(c.buf)%bs != 0 {
				return nil, fmt.Errorf("cipher: data length must be multiple of block size")
			}
			out := make([]byte, len(c.buf))
			cipher.NewCBCDecrypter(c.block, c.iv).CryptBlocks(out, c.buf)
			return unpad(out, bs), nil
		}
		padded := pad(c.buf, bs)
		out := make([]byte, len(padded))
		cipher.NewCBCEncrypter(c.block, c.iv).CryptBlocks(out, padded)
		return out, nil
	case "ecb":
		if c.decrypt {
			if len(c.buf) == 0 || len(c.buf)%bs != 0 {
				return nil, fmt.Errorf("cipher: data length must be multiple of block size")
			}
			out := make([]byte, len(c.buf))
			for i := 0; i < len(c.buf); i += bs {
				c.block.Decrypt(out[i:i+bs], c.buf[i:i+bs])
			}
			return unpad(out, bs), nil
		}
		padded := pad(c.buf, bs)
		out := make([]byte, len(padded))
		for i := 0; i < len(padded); i += bs {
			c.block.Encrypt(out[i:i+bs], padded[i:i+bs])
		}
		return out, nil
	case "ctr":
		out := make([]byte, len(c.buf))
		cipher.NewCTR(c.block, c.iv).XORKeyStream(out, c.buf)
		return out, nil
	case "gcm":
		if c.decrypt {
			if len(c.authTag) == 0 {
				return nil, fmt.Errorf("cipher: setAuthTag must be called before final for AES-GCM")
			}
			sealed := make([]byte, 0, len(c.buf)+len(c.authTag))
			sealed = append(sealed, c.buf...)
			sealed = append(sealed, c.authTag...)
			out, err := c.aead.Open(nil, c.iv, sealed, nil)
			if err != nil {
				return nil, fmt.Errorf("cipher: GCM authentication failed")
			}
			return out, nil
		}
		sealed := c.aead.Seal(nil, c.iv, c.buf, nil)
		tagSize := c.aead.Overhead()
		tag := make([]byte, tagSize)
		copy(tag, sealed[len(sealed)-tagSize:])
		c.authTag = tag
		return sealed[:len(sealed)-tagSize], nil
	default:
		return nil, fmt.Errorf("cipher: unknown mode %q", c.mode)
	}
}

// pad 应用 PKCS7 填充。
func pad(data []byte, bs int) []byte {
	n := bs - len(data)%bs
	out := make([]byte, len(data)+n)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(n)
	}
	return out
}

// unpad 去除 PKCS7 填充。
func unpad(data []byte, bs int) []byte {
	if len(data) == 0 {
		return data
	}
	n := int(data[len(data)-1])
	if n < 1 || n > bs || n > len(data) {
		return data
	}
	return data[:len(data)-n]
}

// --- PBKDF2（纯 Go 实现） -------------------------------------------------

// pbkdf2Key 计算 PBKDF2（RFC 2898）。digest: sha1/sha256/sha512。
func pbkdf2Key(password, salt []byte, iterations, keyLen int, digest string) ([]byte, error) {
	if iterations <= 0 {
		return nil, fmt.Errorf("pbkdf2: iterations must be positive")
	}
	if keyLen <= 0 {
		return nil, fmt.Errorf("pbkdf2: keylen must be positive")
	}
	var h func() hash.Hash
	switch digest {
	case "sha1":
		h = sha1.New
	case "sha256":
		h = sha256.New
	case "sha512":
		h = sha512.New
	default:
		return nil, fmt.Errorf("pbkdf2: unsupported digest %q", digest)
	}
	prf := hmac.New(h, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen
	var buf [4]byte
	dk := make([]byte, 0, numBlocks*hashLen)
	u := make([]byte, hashLen)
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		binary.BigEndian.PutUint32(buf[:], uint32(block))
		prf.Write(buf[:])
		t := prf.Sum(dk)
		copy(u, t[len(t)-hashLen:])
		for n := 2; n <= iterations; n++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for x := range u {
				t[len(t)-hashLen+x] ^= u[x]
			}
		}
		dk = t
	}
	return dk[:keyLen], nil
}

// --- 辅助 ----------------------------------------------------------------

// parseScryptArgs 解析 scrypt 参数：(password, salt, keylen[, options])。
// options 支持 N/r/p（Node 默认 N=16384, r=8, p=1）。
func parseScryptArgs(args []engine.Value) (password, salt []byte, keylen, N, r, p int, err error) {
	if len(args) < 3 {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("scrypt: password, salt and keylen required")
	}
	password, err = cryptoBytes(args[0])
	if err != nil {
		return nil, nil, 0, 0, 0, 0, err
	}
	salt, err = cryptoBytes(args[1])
	if err != nil {
		return nil, nil, 0, 0, 0, 0, err
	}
	keylen = intArg(args, 2, 0)
	if keylen <= 0 {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("scrypt: keylen must be positive")
	}
	N, r, p = 16384, 8, 1
	if len(args) > 3 && args[3].IsObject() {
		if o, ok := args[3].AsObject(); ok {
			for _, k := range []struct {
				key  string
				dest *int
			}{{"N", &N}, {"r", &r}, {"p", &p}} {
				if v, e := o.Get(k.key); e == nil && !v.IsUndefined() && !v.IsNull() {
					if n, ok := v.Int(); ok {
						*k.dest = n
					}
				}
			}
		}
	}
	return password, salt, keylen, N, r, p, nil
}

// cryptoBytes 把参数转为字节（Buffer 或字符串）。
func cryptoBytes(v engine.Value) ([]byte, error) {
	if b, ok := engine.AsBytes(v); ok {
		return b, nil
	}
	return []byte(v.String()), nil
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
