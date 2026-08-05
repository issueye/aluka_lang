package builtin

// node:crypto 测试：哈希/HMAC/AES-CBC/PBKDF2（RFC 标准向量）。

import (
	"testing"
)

// TestCryptoHash 验证 sha256/sha1/md5 hex 摘要。
func TestCryptoHash(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var crypto = require('node:crypto');
var h = crypto.createHash('sha256').update('abc').digest('hex');
var m = crypto.createHash('md5').update('abc').digest('hex');
globalThis.__r = h + ':' + m;
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// sha256("abc") 与 md5("abc") 标准向量。
	if got := env.globalGet("__r"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad:900150983cd24fb0d6963f7d28e17f72" {
		t.Errorf("hash = %q", got)
	}
}

func TestCryptoOneShotHashAndGetHashes(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var crypto = require('node:crypto');
var hashes = crypto.getHashes();
var sum = crypto.hash('sha256', new Uint8Array([97, 98, 99]), 'base64');
globalThis.__r = hashes.indexOf('sha384') >= 0 && sum;
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "ungWv48Bz+pBQUDeXa4iI7ADYaOWF3qctBD/YfIAFa0=" {
		t.Errorf("one-shot hash = %q", got)
	}
}

// TestCryptoHmac 验证 HMAC-SHA256（RFC 向量）。
func TestCryptoHmac(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var crypto = require('node:crypto');
var mac = crypto.createHmac('sha256', 'key').update('The quick brown fox jumps over the lazy dog').digest('hex');
globalThis.__r = mac;
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"
	if got := env.globalGet("__r"); got != want {
		t.Errorf("hmac = %q, want %q", got, want)
	}
}

// TestCryptoCipher 验证 AES-256-CBC 加解密往返。
func TestCryptoCipher(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var crypto = require('node:crypto');
var key = Buffer.alloc(32, 1);
var iv = Buffer.alloc(16, 2);
var c = crypto.createCipheriv('aes-256-cbc', key, iv);
c.update('secret message');
var enc = c.final();
var d = crypto.createDecipheriv('aes-256-cbc', key, iv);
d.update(enc);
var dec = d.final();
globalThis.__r = dec.toString();
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "secret message" {
		t.Errorf("cipher roundtrip = %q, want secret message", got)
	}
}

// TestCryptoPbkdf2 验证 PBKDF2-SHA1（RFC 6070 向量）。
func TestCryptoPbkdf2(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var crypto = require('node:crypto');
var dk = crypto.pbkdf2Sync('password', 'salt', 1, 20, 'sha1');
globalThis.__r = dk.toString('hex');
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "0c60c80f961f0e71f3a9b524af6012062fe037a6"
	if got := env.globalGet("__r"); got != want {
		t.Errorf("pbkdf2 = %q, want %q", got, want)
	}
}

// TestCryptoRandomBytes 验证 randomBytes 返回 Buffer。
func TestCryptoRandomBytes(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var crypto = require('node:crypto');
var b = crypto.randomBytes(16);
globalThis.__r = Buffer.isBuffer(b) + ':' + b.length;
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "true:16" {
		t.Errorf("randomBytes = %q, want true:16", got)
	}
}

// TestCryptoScrypt 验证 scryptSync（RFC 7914 向量：password/NaCl, N=1024, r=8, p=16）。
func TestCryptoScrypt(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var crypto = require('node:crypto');
var dk = crypto.scryptSync('password', 'NaCl', 64, { N: 1024, r: 8, p: 16 });
globalThis.__r = dk.toString('hex');
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "fdbabe1c9d3472007856e7190d01e9fe7c6ad7cbc8237830e77376634b3731622eaf30d92e22a3886ff109279d9830dac727afb94a83ee6d8360cbdfa2cc0640"
	if got := env.globalGet("__r"); got != want {
		t.Errorf("scrypt = %q, want RFC 7914 vector", got)
	}
}
