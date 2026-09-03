// M5-2 diff：Web Crypto（全局 crypto.subtle）确定性算法矩阵。
// digest/hmac/aes-gcm/aes-cbc/exportKey/deriveBits；加解密固定 key+iv → 确定性。
// 注：aluka 的 global crypto.subtle 增强由 node:crypto 模块加载触发，故先 require。
require('node:crypto');
const results = {};

(async function () {
  // digest 已知向量。
  results.digest = Buffer.from(await crypto.subtle.digest('SHA-256', Buffer.from('abc'))).toString('hex');
  results.digest512 = Buffer.from(await crypto.subtle.digest('SHA-512', Buffer.from('abc'))).toString('hex');

  // importKey / exportKey（raw）。
  const rawKey = Buffer.from('2b7e151628aed2a6abf7158809cf4f3c', 'hex');
  const gcmKey = await crypto.subtle.importKey('raw', rawKey, 'AES-GCM', true, ['encrypt', 'decrypt']);
  results.keyType = gcmKey.type + ':' + gcmKey.algorithm.name + ':' + gcmKey.extractable + ':' + gcmKey.usages.join(',');
  results.exportRaw = Buffer.from(await crypto.subtle.exportKey('raw', gcmKey)).toString('hex');

  // AES-GCM 加解密（12 字节 iv → 确定性密文）。
  const gcmIv = Buffer.from('000000000000000000000002', 'hex');
  const ct = await crypto.subtle.encrypt({ name: 'AES-GCM', iv: gcmIv }, gcmKey, Buffer.from('hello webcrypto'));
  results.gcmCt = Buffer.from(ct).toString('hex');
  const pt = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: gcmIv }, gcmKey, ct);
  results.gcmRoundtrip = Buffer.from(pt).toString('utf8');

  // AES-CBC 加解密（16 字节 iv）。
  const cbcKey = await crypto.subtle.importKey('raw', rawKey, 'AES-CBC', false, ['encrypt', 'decrypt']);
  const cbcIv = Buffer.from('000102030405060708090a0b0c0d0e0f', 'hex');
  const cbcCt = await crypto.subtle.encrypt({ name: 'AES-CBC', iv: cbcIv }, cbcKey, Buffer.from('cbc payload'));
  results.cbcCt = Buffer.from(cbcCt).toString('hex');
  const cbcPt = await crypto.subtle.decrypt({ name: 'AES-CBC', iv: cbcIv }, cbcKey, cbcCt);
  results.cbcRoundtrip = Buffer.from(cbcPt).toString('utf8');

  // HMAC sign/verify（HMAC-SHA256 确定性）。
  const hmacKey = await crypto.subtle.importKey('raw', Buffer.from('secret-hmac-key'),
    { name: 'HMAC', hash: 'SHA-256' }, false, ['sign', 'verify']);
  const mac = await crypto.subtle.sign('HMAC', hmacKey, Buffer.from('message'));
  results.hmac = Buffer.from(mac).toString('hex');
  const ok = await crypto.subtle.verify('HMAC', hmacKey, mac, Buffer.from('message'));
  const bad = await crypto.subtle.verify('HMAC', hmacKey, mac, Buffer.from('tampered'));
  results.hmacVerify = ok + ':' + bad;

  // PBKDF2 deriveBits。
  const pbkdf2Key = await crypto.subtle.importKey('raw', Buffer.from('password'), 'PBKDF2', false, ['deriveBits']);
  const bits = await crypto.subtle.deriveBits({
    name: 'PBKDF2', hash: 'SHA-256', salt: Buffer.from('salt'), iterations: 1000,
  }, pbkdf2Key, 256);
  results.pbkdf2Bits = Buffer.from(bits).toString('hex');
  results.pbkdf2Len = bits.byteLength;

  // generateKey 结构。
  const gen = await crypto.subtle.generateKey({ name: 'AES-GCM', length: 256 }, true, ['encrypt']);
  results.gen = gen.type + ':' + gen.algorithm.name + ':' + gen.extractable + ':' + gen.usages.join(',');
})().catch((e) => {
  results.fatal = String(e && (e.message || e));
}).then(() => {
  process.stdout.write(JSON.stringify(results) + '\n');
});
