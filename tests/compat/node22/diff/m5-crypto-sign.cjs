// M5-1c diff：node:crypto 非对称签名/验签 + KeyObject + checkPrimeSync。
// 签名值随密钥随机，只断言结构；验签结果确定性。
const crypto = require('node:crypto');
const results = {};

// generateKeyPairSync + createSign/createVerify 往返。
const { publicKey, privateKey } = crypto.generateKeyPairSync('rsa', { modulusLength: 2048 });
results.kp = publicKey.type + ':' + privateKey.type + ':' + publicKey.asymmetricKeyType;

const sign = crypto.createSign('sha256');
sign.update('data to be signed');
sign.update(' and more');
const signature = sign.sign(privateKey);
results.sig = Buffer.isBuffer(signature) && signature.length > 0;

const verify = crypto.createVerify('sha256');
verify.update('data to be signed');
verify.update(' and more');
results.verifyOk = verify.verify(publicKey, signature);

const verify2 = crypto.createVerify('sha256');
verify2.update('tampered');
results.verifyBad = verify2.verify(publicKey, signature);

// createPublicKey（从私钥派生公钥，仍可验签）。
const pub2 = crypto.createPublicKey(privateKey);
const v3 = crypto.createVerify('sha256');
v3.update('data to be signed');
v3.update(' and more');
results.verifyPub2 = v3.verify(pub2, signature);

// createSecretKey。
const sk = crypto.createSecretKey(Buffer.from('0123456789abcdef'));
results.sk = sk.type + ':' + sk.symmetricKeySize + ':' + sk.export().toString('hex');

// checkPrimeSync（bigint 入参）。
results.prime13 = crypto.checkPrimeSync(13n);
results.prime561 = crypto.checkPrimeSync(561n);
results.prime97 = crypto.checkPrimeSync(97n);
results.prime1 = crypto.checkPrimeSync(1n);

process.stdout.write(JSON.stringify(results) + '\n');
