// 摘要与签名：createHash/createHmac 的 Hash 对象、算法 ID 映射与 Sign/Verify 对象。

package builtin

import (
	"crypto"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

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
