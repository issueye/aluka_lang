package globals

// Aluka.password / Aluka.hash（Phase 4 WBS 4.10 / 4.11）。
//
//   - Aluka.password.hash(password, {algorithm, cost}) → Promise<string>
//     实现为 scrypt（golang.org/x/crypto），格式 aluka-scrypt$N$salt$hash（hex）。
//   - Aluka.password.verify(password, encoded) → Promise<boolean>
//   - Aluka.hash(data, seed?) → BigInt（FNV-1a 64，替代 wyhash）
//   - Aluka.hash.sha1/sha256/sha512(data) → hex 字符串

import (
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"hash/fnv"
	"math/big"
	"strconv"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
	"golang.org/x/crypto/scrypt"
)

const alukaScryptPrefix = "aluka-scrypt$"

// alukaRegisterPassword 注册 Aluka.password。
func alukaRegisterPassword(ctx engine.Context, aluka engine.Value) {
	ao, _ := aluka.AsObject()
	pw := engine.NewObject()

	_ = pw.Set("hash", engine.NewFunction("hash", func(args []engine.Value) (engine.Value, error) {
		password := ""
		if len(args) > 0 {
			password = args[0].String()
		}
		cost := 32768 // scrypt N
		if len(args) > 1 && args[1].IsObject() {
			if o, ok := args[1].AsObject(); ok {
				if v, err := o.Get("cost"); err == nil && !v.IsUndefined() {
					if n, ok := v.Int(); ok && n > 0 {
						cost = n
					}
				}
			}
		}
		executor := engine.NewFunction("executor", func(ea []engine.Value) (engine.Value, error) {
			if len(ea) < 2 {
				return engine.Undefined(), nil
			}
			resolve, reject := ea[0], ea[1]
			release := ctx.AddRef()
			go func() {
				encoded, err := scryptHash(password, cost)
				ctx.PostTask(func() {
					defer release()
					if err != nil {
						callResolve(reject, engine.Str("Aluka.password.hash: "+err.Error()))
						return
					}
					callResolve(resolve, engine.Str(encoded))
				})
			}()
			return engine.Undefined(), nil
		})
		return newPromise(ctx, executor)
	}))

	_ = pw.Set("verify", engine.NewFunction("verify", func(args []engine.Value) (engine.Value, error) {
		password := ""
		encoded := ""
		if len(args) > 0 {
			password = args[0].String()
		}
		if len(args) > 1 {
			encoded = args[1].String()
		}
		executor := engine.NewFunction("executor", func(ea []engine.Value) (engine.Value, error) {
			if len(ea) == 0 {
				return engine.Undefined(), nil
			}
			resolve := ea[0]
			release := ctx.AddRef()
			go func() {
				ok := scryptVerify(password, encoded)
				ctx.PostTask(func() {
					defer release()
					callResolve(resolve, engine.Boolean(ok))
				})
			}()
			return engine.Undefined(), nil
		})
		return newPromise(ctx, executor)
	}))

	_ = ao.Set("password", pw)
}

// scryptHash 用 scrypt 派生密钥并编码为 aluka-scrypt$N$salt$hash。
func scryptHash(password string, cost int) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk, err := scrypt.Key([]byte(password), salt, cost, 8, 1, 64)
	if err != nil {
		return "", err
	}
	return alukaScryptPrefix + strconv.Itoa(cost) + "$" + hex.EncodeToString(salt) + "$" + hex.EncodeToString(dk), nil
}

// scryptVerify 校验密码与编码字符串。
func scryptVerify(password, encoded string) bool {
	if !strings.HasPrefix(encoded, alukaScryptPrefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(encoded, alukaScryptPrefix), "$")
	if len(parts) != 3 {
		return false
	}
	cost, err := strconv.Atoi(parts[0])
	if err != nil || cost <= 0 {
		return false
	}
	salt, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(parts[2])
	if err != nil {
		return false
	}
	dk, err := scrypt.Key([]byte(password), salt, cost, 8, 1, 64)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(dk, want) == 1
}

// alukaRegisterHash 注册 Aluka.hash（FNV-1a 64）与 sha1/sha256/sha512。
func alukaRegisterHash(ctx engine.Context, aluka engine.Value) {
	ao, _ := aluka.AsObject()
	hashFn := engine.NewFunction("hash", func(args []engine.Value) (engine.Value, error) {
		data := []byte("")
		if len(args) > 0 {
			data = argBytes(args, 0)
		}
		h := fnv.New64a()
		if len(args) > 1 && !args[1].IsUndefined() && !args[1].IsNull() {
			if n, ok := args[1].Int(); ok {
				var sb [8]byte
				binary.LittleEndian.PutUint64(sb[:], uint64(n))
				_, _ = h.Write(sb[:])
			}
		}
		_, _ = h.Write(data)
		return engine.BigInt(new(big.Int).SetUint64(h.Sum64())), nil
	})
	// hash.sha1 / sha256 / sha512 → hex。
	hashFo, _ := hashFn.AsObject()
	_ = hashFo.Set("sha1", engine.NewFunction("sha1", func(args []engine.Value) (engine.Value, error) {
		sum := sha1.Sum(argBytes(args, 0))
		return engine.Str(hex.EncodeToString(sum[:])), nil
	}))
	_ = hashFo.Set("sha256", engine.NewFunction("sha256", func(args []engine.Value) (engine.Value, error) {
		sum := sha256.Sum256(argBytes(args, 0))
		return engine.Str(hex.EncodeToString(sum[:])), nil
	}))
	_ = hashFo.Set("sha512", engine.NewFunction("sha512", func(args []engine.Value) (engine.Value, error) {
		sum := sha512.Sum512(argBytes(args, 0))
		return engine.Str(hex.EncodeToString(sum[:])), nil
	}))
	_ = ao.Set("hash", hashFn)
}
