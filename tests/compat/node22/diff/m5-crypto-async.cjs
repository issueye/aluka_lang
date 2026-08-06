// M5-1d diff：node:crypto 异步回调版（pbkdf2/hkdf/scrypt/randomInt/randomFill/checkPrime）。
const crypto = require('node:crypto');
const results = {};

crypto.pbkdf2('password', 'salt', 2, 20, 'sha1', (err, dk) => {
  results.pbkdf2 = !err && dk.toString('hex');
  crypto.hkdf('sha256', Buffer.from('0b'.repeat(22), 'hex'),
    Buffer.from('000102030405060708090a0b0c', 'hex'),
    Buffer.from('f0f1f2f3f4f5f6f7f8f9', 'hex'), 42, (err2, key) => {
      results.hkdf = !err2 && Buffer.from(key).toString('hex');
      crypto.randomInt(1, 100, (err3, n) => {
        results.randomInt = !err3 && n >= 1 && n < 100;
        const buf = Buffer.alloc(8);
        crypto.randomFill(buf, (err4, filled) => {
          results.randomFill = !err4 && filled === buf && buf.length === 8;
          crypto.checkPrime(97n, (err5, prime) => {
            results.checkPrime = !err5 && prime;
            crypto.checkPrime(561n, (err6, prime2) => {
              results.checkPrimeComposite = !err6 && !prime2;
              crypto.scrypt('password', 'NaCl', 16, { N: 16, r: 8, p: 1 }, (err7, dk2) => {
                results.scrypt = !err7 && dk2.length === 16;
                process.stdout.write(JSON.stringify(results));
              });
            });
          });
        });
      });
    });
});
