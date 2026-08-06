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
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
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
		return globals.NewBufferInstance(sum), nil
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

	return m, nil
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

// --- 对称加密（CBC） ------------------------------------------------------

// cipherState 是 Cipher/Decipher 的内部状态（累积数据 + 最终处理）。
type cipherState struct {
	block   cipher.Block
	iv      []byte
	decrypt bool
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
	iv, err := cryptoBytes(args[2])
	if err != nil {
		return engine.Undefined(), err
	}
	var block cipher.Block
	switch algorithm {
	case "aes-128-cbc", "aes-256-cbc":
		if len(key) != 16 && len(key) != 32 {
			return engine.Undefined(), fmt.Errorf("createCipheriv: aes key must be 16 or 32 bytes")
		}
		block, err = aes.NewCipher(key)
		if err != nil {
			return engine.Undefined(), err
		}
		if len(iv) != block.BlockSize() {
			return engine.Undefined(), fmt.Errorf("createCipheriv: iv must be %d bytes", block.BlockSize())
		}
	default:
		return engine.Undefined(), fmt.Errorf("createCipheriv: unsupported algorithm %q", algorithm)
	}
	state := &cipherState{block: block, iv: iv, decrypt: decrypt}

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
	return obj, nil
}

// finalize 处理累积数据（加密：PKCS7 padding + CBC；解密：CBC + 去 padding）。
func (c *cipherState) finalize() ([]byte, error) {
	bs := c.block.BlockSize()
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
