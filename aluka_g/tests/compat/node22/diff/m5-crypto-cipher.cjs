// M5-1b diff：node:crypto 对称加密（AES-CBC/GCM/CTR/ECB）。
// 已知向量 + 往返；GCM tag 与密文分开断言。
const crypto = require('node:crypto');
const results = {};

// AES-128-CBC 已知向量（NIST SP 800-38A F.2.1）。
const key = Buffer.from('2b7e151628aed2a6abf7158809cf4f3c', 'hex');
const iv = Buffer.from('000102030405060708090a0b0c0d0e0f', 'hex');
const pt = Buffer.from('6bc1bee22e409f96e93d7e117393172a', 'hex');

const c = crypto.createCipheriv('aes-128-cbc', key, iv);
const enc = Buffer.concat([c.update(pt), c.final()]);
results.cbcVector = enc.toString('hex');
const d = crypto.createDecipheriv('aes-128-cbc', key, iv);
results.cbcRoundtrip = Buffer.concat([d.update(enc), d.final()]).toString('hex');

// AES-256-CBC 往返。
const k256 = Buffer.alloc(32, 7);
const c256 = crypto.createCipheriv('aes-256-cbc', k256, iv);
const e256 = Buffer.concat([c256.update('secret message 1234567890'), c256.final()]);
const d256 = crypto.createDecipheriv('aes-256-cbc', k256, iv);
results.cbc256 = Buffer.concat([d256.update(e256), d256.final()]).toString('utf8');

// AES-192-CBC 往返。
const k192 = Buffer.alloc(24, 9);
const c192 = crypto.createCipheriv('aes-192-cbc', k192, iv);
const e192 = Buffer.concat([c192.update('aes 192 roundtrip!'), c192.final()]);
const d192 = crypto.createDecipheriv('aes-192-cbc', k192, iv);
results.cbc192 = Buffer.concat([d192.update(e192), d192.final()]).toString('utf8');

// AES-128-GCM 往返（固定 iv → 确定性；tag 独立）。
const gcmKey = Buffer.from('2b7e151628aed2a6abf7158809cf4f3c', 'hex');
const gcmIv = Buffer.from('000000000000000000000002', 'hex');
const gcmC = crypto.createCipheriv('aes-128-gcm', gcmKey, gcmIv);
const gcmCt = Buffer.concat([gcmC.update('hello gcm world'), gcmC.final()]);
const gcmTag = gcmC.getAuthTag();
results.gcmCt = gcmCt.toString('hex');
results.gcmTag = gcmTag.toString('hex');
const gcmD = crypto.createDecipheriv('aes-128-gcm', gcmKey, gcmIv);
gcmD.setAuthTag(gcmTag);
results.gcmRoundtrip = Buffer.concat([gcmD.update(gcmCt), gcmD.final()]).toString('utf8');

// AES-128-CTR 往返。
const ctrC = crypto.createCipheriv('aes-128-ctr', key, iv);
const ctrCt = Buffer.concat([ctrC.update(pt), ctrC.final()]);
const ctrD = crypto.createDecipheriv('aes-128-ctr', key, iv);
results.ctrRoundtrip = Buffer.concat([ctrD.update(ctrCt), ctrD.final()]).toString('hex');

// AES-128-ECB 往返（iv 传 null）。
const ecbC = crypto.createCipheriv('aes-128-ecb', key, null);
const ecbCt = Buffer.concat([ecbC.update(pt), ecbC.final()]);
const ecbD = crypto.createDecipheriv('aes-128-ecb', key, null);
results.ecbRoundtrip = Buffer.concat([ecbD.update(ecbCt), ecbD.final()]).toString('hex');

// 篡改 GCM tag → 解密失败。
try {
  const badD = crypto.createDecipheriv('aes-128-gcm', gcmKey, gcmIv);
  const badTag = Buffer.from(gcmTag);
  badTag[0] ^= 0xff;
  badD.setAuthTag(badTag);
  Buffer.concat([badD.update(gcmCt), badD.final()]);
  results.gcmTamper = 'no-throw';
} catch (e) {
  results.gcmTamper = e.name;
}

process.stdout.write(JSON.stringify(results) + '\n');
