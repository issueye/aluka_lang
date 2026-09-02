// 密钥派生与密钥对象：pbkdf2/scrypt/hkdf 参数解析与 KeyObject 包装。

package nodecrypto

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"fmt"
	"hash"
	"io"

	"github.com/aluka-lang/aluka/internal/builtin/nodebase"
	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
	"golang.org/x/crypto/hkdf"
)

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

// parseScryptArgs 解析 scrypt 参数：(password, salt, keylen[, options])。
// options 支持 N/r/p（Node 默认 N=16384, r=8, p=1）。
func parseScryptArgs(args []engine.Value) (password, salt []byte, keylen, N, r, p int, err error) {
	if len(args) < 3 {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("scrypt: password, salt and keylen required")
	}
	password, err = nodebase.CryptoBytes(args[0])
	if err != nil {
		return nil, nil, 0, 0, 0, 0, err
	}
	salt, err = nodebase.CryptoBytes(args[1])
	if err != nil {
		return nil, nil, 0, 0, 0, 0, err
	}
	keylen = nodebase.IntArg(args, 2, 0)
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
