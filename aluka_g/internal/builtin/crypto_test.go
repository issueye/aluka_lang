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

// TestCryptoRandomIntHkdfTimingSafe 验证 M5 新增的 randomInt/hkdf/timingSafeEqual。
func TestCryptoRandomIntHkdfTimingSafe(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var crypto = require('node:crypto');
// randomInt：范围 [min, max)，多次取值均在界内。
var okRange = true;
for (var i = 0; i < 20; i++) {
  var v = crypto.randomInt(5, 10);
  if (v < 5 || v >= 10) okRange = false;
}
// hkdfSync（RFC 5869 test case 1）。
var ikm = Buffer.from('0b'.repeat(22), 'hex');
var salt = Buffer.from('000102030405060708090a0b0c', 'hex');
var info = Buffer.from('f0f1f2f3f4f5f6f7f8f9', 'hex');
var hkdf = Buffer.from(crypto.hkdfSync('sha256', ikm, salt, info, 42)).toString('hex');
// timingSafeEqual。
var eq = Buffer.from('secret-data');
var tse = crypto.timingSafeEqual(eq, Buffer.from('secret-data')) + ':' +
  crypto.timingSafeEqual(eq, Buffer.from('SECRET-DATA'));
// randomUUID v4 结构。
var u = crypto.randomUUID();
var uuid = u.length === 36 && u[14] === '4';
globalThis.__r = okRange + ':' + hkdf + ':' + tse + ':' + uuid;
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "true:3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865:true:false:true"
	if got := env.globalGet("__r"); got != want {
		t.Errorf("randomInt/hkdf/tse/uuid = %q", got)
	}
}

// TestCryptoSecretKeySignVerify 验证 createSecretKey 与 RSA 签名/验签往返。
func TestCryptoSecretKeySignVerify(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var crypto = require('node:crypto');
var sk = crypto.createSecretKey(Buffer.from('0123456789abcdef'));
var skInfo = sk.type + ':' + sk.symmetricKeySize + ':' + sk.export().toString('hex');
var pair = crypto.generateKeyPairSync('rsa', { modulusLength: 2048 });
var sign = crypto.createSign('sha256');
sign.update('data to be signed');
var signature = sign.sign(pair.privateKey);
var verify = crypto.createVerify('sha256');
verify.update('data to be signed');
var ok = verify.verify(pair.publicKey, signature);
var bad = false;
try {
  var v2 = crypto.createVerify('sha256');
  v2.update('tampered');
  bad = v2.verify(pair.publicKey, signature);
} catch (e) { bad = 'err'; }
globalThis.__r = skInfo + ':' + (Buffer.isBuffer(signature) && signature.length > 0) + ':' + ok + ':' + bad;
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "secret:16:30313233343536373839616263646566:true:true:false" {
		t.Errorf("secretKey/sign/verify = %q", got)
	}
}

// TestCryptoGcmCipher 验证 createCipheriv AES-GCM 加解密往返与 authTag。
func TestCryptoGcmCipher(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var crypto = require('node:crypto');
var key = Buffer.from('2b7e151628aed2a6abf7158809cf4f3c', 'hex');
var iv = Buffer.from('000000000000000000000002', 'hex');
var c = crypto.createCipheriv('aes-128-gcm', key, iv);
var ct = Buffer.concat([c.update('hello gcm world'), c.final()]);
var tag = c.getAuthTag();
var d = crypto.createDecipheriv('aes-128-gcm', key, iv);
d.setAuthTag(tag);
var dec = Buffer.concat([d.update(ct), d.final()]).toString();
globalThis.__r = dec + ':' + ct.length + ':' + tag.length;
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "hello gcm world:15:16" {
		t.Errorf("gcm cipher = %q", got)
	}
}

// TestWebCryptoSubtleAesGcmHmac 验证 crypto.subtle 增强（AES-GCM/HMAC/PBKDF2）。
// global subtle 的补充方法由 node:crypto 加载触发。
func TestWebCryptoSubtleAesGcmHmac(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
require('node:crypto'); // 触发 global crypto.subtle 的增强方法注册
crypto.subtle.importKey('raw', Buffer.from('2b7e151628aed2a6abf7158809cf4f3c', 'hex'),
  'AES-GCM', true, ['encrypt', 'decrypt']).then(function(key) {
  var iv = Buffer.from('000000000000000000000002', 'hex');
  return crypto.subtle.encrypt({ name: 'AES-GCM', iv: iv }, key, Buffer.from('hello webcrypto'));
}).then(function(ct) {
  globalThis.__ct = Buffer.from(ct).toString('hex');
  return crypto.subtle.importKey('raw', Buffer.from('secret-hmac-key'),
    { name: 'HMAC', hash: 'SHA-256' }, false, ['sign', 'verify']);
}).then(function(hmacKey) {
  return crypto.subtle.sign('HMAC', hmacKey, Buffer.from('message'));
}).then(function(mac) {
  globalThis.__mac = Buffer.from(mac).toString('hex');
});
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// AES-GCM（固定 key+iv）密文与 HMAC-SHA256 均为确定性已知答案。
	if got := env.globalGet("__ct"); got != "96dbc2e0ec0abbcf7a1439033de4570240fe161c58b5be6642c935cb26452e" {
		t.Errorf("subtle aes-gcm ct = %q", got)
	}
	if got := env.globalGet("__mac"); got != "c04bcb18d17592d2fb41b666f7e137776842759533b68f94443c5f7745433f95" {
		t.Errorf("subtle hmac = %q", got)
	}
}
