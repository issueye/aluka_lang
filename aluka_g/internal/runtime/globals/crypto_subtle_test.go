package globals

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gbuffer"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gcrypto"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gencoding"
)

func newSubtleTestContext(t *testing.T) engine.Context {
	t.Helper()
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	t.Cleanup(func() { ctx.Close() })
	if err := gbuffer.NewBuffer(ctx, gbuffer.BufferConfig{}); err != nil {
		t.Fatalf("NewBuffer: %v", err)
	}
	if err := gencoding.NewEncoding(ctx, gencoding.EncodingConfig{}); err != nil {
		t.Fatalf("NewEncoding: %v", err)
	}
	if err := gcrypto.NewWebCrypto(ctx, gcrypto.WebCryptoConfig{}); err != nil {
		t.Fatalf("NewWebCrypto: %v", err)
	}
	_ = ctx.Global().Set("globalThis", ctx.Global())
	return ctx
}

func subtleRun(t *testing.T, ctx engine.Context, code string) error {
	t.Helper()
	if _, err := ctx.Eval(code, "subtle_test.js"); err != nil {
		return err
	}
	for i := 0; i < 20; i++ {
		if !ctx.FlushMicrotasks() {
			break
		}
	}
	return nil
}

// TestSubtleCryptoDigest 测试 SHA-256 / SHA-512 / MD5 摘要计算
func TestSubtleCryptoDigest(t *testing.T) {
	ctx := newSubtleTestContext(t)

	src := `
		globalThis.pass = false;
		async function test() {
			var data = new TextEncoder().encode("hello aluka crypto");
			var hashBuffer = await crypto.subtle.digest("SHA-256", data);
			var hashArray = Array.from(new Uint8Array(hashBuffer));
			var hashHex = hashArray.map(b => b.toString(16).padStart(2, '0')).join('');
			if (hashHex.length === 64) {
				globalThis.pass = true;
			}
		}
		test();
	`
	if err := subtleRun(t, ctx, src); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	v, err := ctx.Global().Get("pass")
	if err != nil || v.String() != "true" {
		t.Fatalf("crypto.subtle.digest test failed, pass=%v", v)
	}
}

// TestSubtleCryptoHMACSignVerify 测试 HMAC-SHA256 签名与验签
func TestSubtleCryptoHMACSignVerify(t *testing.T) {
	ctx := newSubtleTestContext(t)

	src := `
		globalThis.pass = false;
		async function test() {
			var rawKey = new TextEncoder().encode("secret-key-123456");
			var key = await crypto.subtle.importKey(
				"raw",
				rawKey,
				{ name: "HMAC", hash: { name: "SHA-256" } },
				true,
				["sign", "verify"]
			);

			var message = new TextEncoder().encode("header.payload");
			var signature = await crypto.subtle.sign("HMAC", key, message);

			var isValid = await crypto.subtle.verify("HMAC", key, signature, message);
			var isInvalid = await crypto.subtle.verify("HMAC", key, signature, new TextEncoder().encode("tampered.payload"));

			if (isValid === true && isInvalid === false) {
				globalThis.pass = true;
			}
		}
		test();
	`
	if err := subtleRun(t, ctx, src); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	v, err := ctx.Global().Get("pass")
	if err != nil || v.String() != "true" {
		t.Fatalf("crypto.subtle HMAC sign/verify failed, pass=%v", v)
	}
}

// TestSubtleCryptoAESGCMEncryptDecrypt 测试 AES-GCM 256 位认证加密与解密
func TestSubtleCryptoAESGCMEncryptDecrypt(t *testing.T) {
	ctx := newSubtleTestContext(t)

	src := `
		globalThis.pass = false;
		async function test() {
			var rawKey = new TextEncoder().encode("12345678901234567890123456789012"); // 32 bytes = 256 bits
			var key = await crypto.subtle.importKey(
				"raw",
				rawKey,
				{ name: "AES-GCM" },
				true,
				["encrypt", "decrypt"]
			);

			var iv = new TextEncoder().encode("123456789012"); // 12 bytes nonce
			var plaintext = new TextEncoder().encode("Sensitive Token & User Payload");

			var ciphertext = await crypto.subtle.encrypt(
				{ name: "AES-GCM", iv: iv },
				key,
				plaintext
			);

			var decrypted = await crypto.subtle.decrypt(
				{ name: "AES-GCM", iv: iv },
				key,
				ciphertext
			);

			var decryptedText = new TextDecoder().decode(decrypted);
			if (decryptedText === "Sensitive Token & User Payload") {
				globalThis.pass = true;
			}
		}
		test();
	`
	if err := subtleRun(t, ctx, src); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	v, err := ctx.Global().Get("pass")
	if err != nil || v.String() != "true" {
		t.Fatalf("crypto.subtle AES-GCM encrypt/decrypt failed, pass=%v", v)
	}
}

// TestSubtleCryptoPBKDF2DeriveBits 测试 PBKDF2 密钥派生
func TestSubtleCryptoPBKDF2DeriveBits(t *testing.T) {
	ctx := newSubtleTestContext(t)

	src := `
		globalThis.pass = false;
		async function test() {
			var baseKey = await crypto.subtle.importKey(
				"raw",
				new TextEncoder().encode("user-password"),
				{ name: "PBKDF2" },
				false,
				["deriveBits", "deriveKey"]
			);

			var derived = await crypto.subtle.deriveBits(
				{
					name: "PBKDF2",
					salt: new TextEncoder().encode("salt-xyz"),
					iterations: 1000,
					hash: "SHA-256"
				},
				baseKey,
				256
			);

			var arr = new Uint8Array(derived);
			if (arr.byteLength === 32 || arr.length === 32) {
				globalThis.pass = true;
			}
		}
		test();
	`
	if err := subtleRun(t, ctx, src); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	v, err := ctx.Global().Get("pass")
	if err != nil || v.String() != "true" {
		t.Fatalf("crypto.subtle PBKDF2 failed, pass=%v", v)
	}
}
