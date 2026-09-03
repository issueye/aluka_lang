// M5-1a diff：node:crypto 确定性哈希/HMAC/PBKDF2/HKDF/random 结构。
// 输出归一化 JSON；随机 API 只断言结构与范围（不输出随机值）。
const crypto = require('node:crypto');
const results = {};

// createHash 多算法已知向量（RFC 1321/6234/3174）。
results.sha256 = crypto.createHash('sha256').update('abc').digest('hex');
results.sha1 = crypto.createHash('sha1').update('abc').digest('hex');
results.sha384 = crypto.createHash('sha384').update('abc').digest('hex');
results.sha512 = crypto.createHash('sha512').update('abc').digest('hex');
results.md5 = crypto.createHash('md5').update('abc').digest('hex');
results.chained = crypto.createHash('sha256').update('a').update('b').update('c').digest('hex');
results.digestBuffer = Buffer.isBuffer(crypto.createHash('sha256').update('abc').digest());
results.digestBase64 = crypto.createHash('sha256').update('abc').digest('base64');

// 一次性 hash（Node 21.7+）：Node 22 实测默认返回 hex 字符串。
results.oneshotDefault = crypto.hash('sha256', Buffer.from('abc'));
results.oneshotHex = crypto.hash('sha256', Buffer.from('abc'), 'hex');
results.oneshotBuffer = Buffer.isBuffer(crypto.hash('sha256', Buffer.from('abc'), 'buffer'));
results.oneshotBase64 = crypto.hash('sha256', Buffer.from('abc'), 'base64');

// Hmac。
results.hmac256 = crypto.createHmac('sha256', 'key').update('The quick brown fox jumps over the lazy dog').digest('hex');
results.hmac1 = crypto.createHmac('sha1', Buffer.from('key')).update('hello').digest('hex');

// getHashes 覆盖（Node 全列表由 gaps.md 跟踪）。
results.getHashes = ['md5', 'sha1', 'sha256', 'sha384', 'sha512']
  .filter((h) => crypto.getHashes().includes(h)).sort();

// pbkdf2Sync（RFC 6070 向量）。
results.pbkdf2_1 = crypto.pbkdf2Sync('password', 'salt', 1, 20, 'sha1').toString('hex');
results.pbkdf2_2 = crypto.pbkdf2Sync('password', 'salt', 2, 20, 'sha1').toString('hex');
results.pbkdf2_sha256 = crypto.pbkdf2Sync('password', 'salt', 2, 32, 'sha256').toString('hex');

// hkdfSync（RFC 5869 test case 1）；Node 22 返回 ArrayBuffer，用 Buffer.from 归一化。
const ikm = Buffer.from('0b'.repeat(22), 'hex');
const salt = Buffer.from('000102030405060708090a0b0c', 'hex');
const info = Buffer.from('f0f1f2f3f4f5f6f7f8f9', 'hex');
results.hkdf = Buffer.from(crypto.hkdfSync('sha256', ikm, salt, info, 42)).toString('hex');

// timingSafeEqual。
const eq = Buffer.from('secret-data');
results.tse = crypto.timingSafeEqual(eq, Buffer.from('secret-data'));
results.tseDiff = crypto.timingSafeEqual(eq, Buffer.from('SECRET-DATA'));
try { crypto.timingSafeEqual(eq, Buffer.from('short')); results.tseLen = 'no-throw'; }
catch (e) { results.tseLen = e.name; }

// randomInt 结构（只断言类型/范围，不输出随机值）。
results.randomIntType = typeof crypto.randomInt(10);
results.randomIntRange = crypto.randomInt(5, 10) >= 5 && crypto.randomInt(5, 10) < 10;
results.randomIntMin = crypto.randomInt(10, 20) >= 10;
try { crypto.randomInt(10, 10); results.randomIntBad = 'no-throw'; }
catch (e) { results.randomIntBad = e.name; }

// randomBytes 结构。
const rb = crypto.randomBytes(32);
results.randomBytes = Buffer.isBuffer(rb) + ':' + rb.length;

// randomUUID 结构（v4）。
const u1 = crypto.randomUUID();
results.uuid = u1.length + ':' + (u1[14] === '4') + ':' + ['8', '9', 'a', 'b'].includes(u1[19]);

// randomFillSync / randomFill。
const fb = Buffer.alloc(16, 0);
crypto.randomFillSync(fb);
results.randomFill = fb.length === 16 && Buffer.isBuffer(fb);
const fb2 = Buffer.alloc(4);
crypto.randomFillSync(fb2, 1, 2);
results.randomFillOffset = fb2[0] === 0 && fb2[3] === 0;

// createSecretKey（KeyObject）。
const sk = crypto.createSecretKey(Buffer.from('0123456789abcdef'));
results.secretKey = sk.type + ':' + sk.symmetricKeySize + ':' + sk.export().toString('hex');

// getCiphers 覆盖（子集，Node 全列表由 gaps.md 跟踪）。
results.ciphers = ['aes-128-cbc', 'aes-192-cbc', 'aes-256-cbc', 'aes-128-ctr', 'aes-128-gcm', 'aes-256-gcm']
  .filter((c) => crypto.getCiphers().includes(c)).sort();

// webcrypto / getRandomValues 面。
results.webcrypto = typeof crypto.webcrypto === 'object' && typeof crypto.webcrypto.subtle === 'object';
results.getRandomValuesFn = typeof crypto.getRandomValues === 'function';
const grv = crypto.getRandomValues(Buffer.alloc(8));
results.getRandomValues = grv.length === 8;

process.stdout.write(JSON.stringify(results) + '\n');
