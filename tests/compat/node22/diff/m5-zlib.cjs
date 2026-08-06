// M5-3a diff：node:zlib 压缩往返 + crc32 已知向量 + constants 子集。
// 压缩字节不跨运行时比较（Go/OpenSSL 默认输出可能不同），只比较往返结果。
const zlib = require('node:zlib');
const results = {};

const data = Buffer.from('The quick brown fox jumps over the lazy dog. '.repeat(4));

// 往返。
results.gzip = zlib.gunzipSync(zlib.gzipSync(data)).toString();
results.deflate = zlib.inflateSync(zlib.deflateSync(data)).toString();
results.deflateRaw = zlib.inflateRawSync(zlib.deflateRawSync(data)).toString();
results.brotli = zlib.brotliDecompressSync(zlib.brotliCompressSync(data)).toString();
results.zstd = zlib.zstdDecompressSync(zlib.zstdCompressSync(data)).toString();
results.unzipGzip = zlib.unzipSync(zlib.gzipSync(data)).toString();
results.unzipDeflate = zlib.unzipSync(zlib.deflateSync(data)).toString();

// 压缩结果结构。
const gz = zlib.gzipSync(data);
const def = zlib.deflateSync(data);
const raw = zlib.deflateRawSync(data);
results.gzMagic = gz[0] === 0x1f && gz[1] === 0x8b;
results.defIsBuffer = Buffer.isBuffer(def);
results.rawSmaller = raw.length < def.length;

// 解压已知 zlib 流（固定字节，标准 'hello' 的 deflate：789c...）。
const knownZlib = Buffer.from('789ccb48cdc9c90700062c0215', 'hex');
results.knownZlib = zlib.inflateSync(knownZlib).toString();
results.knownUnzip = zlib.unzipSync(knownZlib).toString();
const knownGzip = Buffer.from('1f8b080000000000000acb48cdc9c95728cf2fca49010085114a0d0b000000', 'hex');
results.knownGzip = zlib.gunzipSync(knownGzip).toString();
const knownRaw = Buffer.from('cb48cdc9c90700', 'hex');
results.knownRaw = zlib.inflateRawSync(knownRaw).toString();

// crc32 已知向量。
results.crcEmpty = zlib.crc32('');
results.crcAbc = zlib.crc32('abc');
results.crcHello = zlib.crc32('hello');
results.crcCheck = zlib.crc32('123456789');
results.crcBuffer = zlib.crc32(Buffer.from('hello'));

// constants 子集（与 Node 实测一致）。
const cs = zlib.constants;
results.constants = [
  cs.Z_OK, cs.Z_STREAM_END, cs.Z_NEED_DICT,
  cs.Z_NO_FLUSH, cs.Z_PARTIAL_FLUSH, cs.Z_SYNC_FLUSH, cs.Z_FULL_FLUSH, cs.Z_FINISH, cs.Z_BLOCK, cs.Z_TREES,
  cs.Z_NO_COMPRESSION, cs.Z_BEST_SPEED, cs.Z_BEST_COMPRESSION, cs.Z_DEFAULT_COMPRESSION,
  cs.Z_FILTERED, cs.Z_HUFFMAN_ONLY, cs.Z_RLE, cs.Z_FIXED, cs.Z_DEFAULT_STRATEGY,
  cs.BROTLI_MODE_GENERIC, cs.BROTLI_OPERATION_FINISH, cs.BROTLI_PARAM_QUALITY,
  cs.BROTLI_MIN_QUALITY, cs.BROTLI_MAX_QUALITY, cs.BROTLI_DEFAULT_QUALITY,
  cs.BROTLI_MIN_WINDOW_BITS, cs.BROTLI_MAX_WINDOW_BITS, cs.BROTLI_DEFAULT_WINDOW,
  cs.BROTLI_DECODER_RESULT_ERROR, cs.BROTLI_DECODER_RESULT_SUCCESS,
  cs.ZSTD_c_compressionLevel, cs.ZSTD_c_strategy,
].join(',');

process.stdout.write(JSON.stringify(results) + '\n');
