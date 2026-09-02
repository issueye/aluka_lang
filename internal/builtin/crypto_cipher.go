// 对称加解密：Cipher/Decipher 对象、分组填充（PKCS#7）与 final 收尾。

package builtin

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

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
