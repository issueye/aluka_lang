// M5-3b diff：node:zlib 异步回调版往返。
const zlib = require('node:zlib');
const data = Buffer.from('async zlib payload. '.repeat(3));
const results = {};

zlib.gzip(data, (err, gz) => {
  results.gzipErr = err === null;
  zlib.gunzip(gz, (err2, back) => {
    results.gunzip = err2 === null && back.toString() === data.toString();
    zlib.deflate(data, (err3, def) => {
      results.deflateErr = err3 === null;
      zlib.inflate(def, (err4, back2) => {
        results.inflate = err4 === null && back2.toString() === data.toString();
        zlib.deflateRaw(data, (err5, raw) => {
          results.deflateRawErr = err5 === null;
          zlib.inflateRaw(raw, (err6, back3) => {
            results.inflateRaw = err6 === null && back3.toString() === data.toString();
            zlib.brotliCompress(data, (err7, comp) => {
              results.brotliCompressErr = err7 === null;
              zlib.brotliDecompress(comp, (err8, back4) => {
                results.brotli = err8 === null && back4.toString() === data.toString();
                process.stdout.write(JSON.stringify(results) + '\n');
              });
            });
          });
        });
      });
    });
  });
});
