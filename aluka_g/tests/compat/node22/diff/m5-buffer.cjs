// M5-5 diff：node:buffer isUtf8/isAscii/transcode + read/write 家族核对。
const { isUtf8, isAscii, transcode } = require('node:buffer');
const results = {};

// isUtf8 / isAscii。
results.isUtf8Good = isUtf8(Buffer.from('hello'));
results.isUtf8Bad = isUtf8(Buffer.from([0xc3, 0x28]));
results.isUtf8Empty = isUtf8(Buffer.alloc(0));
results.isUtf8CJK = isUtf8(Buffer.from('你好'));
results.isAsciiGood = isAscii(Buffer.from('ASCII'));
results.isAsciiBad = isAscii(Buffer.from([0xff, 0x00]));
results.isAsciiEmpty = isAscii(Buffer.alloc(0));

// transcode（utf8/utf16le/latin1/ascii 矩阵）。
const u16 = Buffer.from([0x48, 0x00, 0x65, 0x00, 0x6c, 0x00, 0x6c, 0x00, 0x6f, 0x00]);
results.trUtf16ToUtf8 = transcode(u16, 'utf16le', 'utf8').toString('utf8');
results.trUtf8ToUtf16 = transcode(Buffer.from('hi'), 'utf8', 'utf16le').toString('hex');
results.trLatin1ToUtf8 = transcode(Buffer.from([0xff]), 'latin1', 'utf8').toString('hex');
results.trAsciiToUtf16 = transcode(Buffer.from('ab'), 'ascii', 'utf16le').toString('hex');
results.trUtf16ToLatin1 = transcode(u16, 'utf16le', 'latin1').toString('ascii');
// Node 实测 transcode 不支持 hex/base64（抛错），仅 utf8/utf16le/latin1/ascii。
try { transcode(u16, 'utf16le', 'hex'); results.trHex = 'no-throw'; }
catch (e) { results.trHex = 'throws'; }

// read/write 家族（含 BigInt 与 swap，M2 已实现）。
const b = Buffer.alloc(20);
b.writeUInt16LE(0x1234, 0);
b.writeUInt16BE(0x1234, 2);
b.writeUInt32LE(0xdeadbeef, 4);
b.writeInt8(-1, 8);
b.writeBigUInt64BE(0x0102030405060708n, 9);
results.r1 = b.readUInt16LE(0);
results.r2 = b.readUInt16BE(2);
results.r3 = b.readUInt32LE(4);
results.r4 = b.readInt8(8);
results.r5 = b.readBigUInt64BE(9).toString();

const b2 = Buffer.alloc(8);
b2.writeDoubleLE(3.14159, 0);
results.d1 = b2.readDoubleLE(0).toFixed(5);

const b3 = Buffer.alloc(4);
b3.writeFloatBE(1.5, 0);
results.f1 = b3.readFloatBE(0);

const b4 = Buffer.alloc(8);
b4.writeBigInt64LE(-12345678901234n, 0);
results.bi = b4.readBigInt64LE(0).toString();

const b5 = Buffer.from([0x01, 0x02, 0x03, 0x04]);
b5.swap16();
results.swap16 = b5.toString('hex');

results.methods = [
  'readUInt8', 'readUInt16LE', 'readUInt16BE', 'readUInt32LE', 'readUInt32BE',
  'readInt8', 'readInt16LE', 'readInt16BE', 'readInt32LE', 'readInt32BE',
  'readFloatLE', 'readFloatBE', 'readDoubleLE', 'readDoubleBE',
  'readBigUInt64LE', 'readBigUInt64BE', 'readBigInt64LE', 'readBigInt64BE',
  'writeUInt8', 'writeUInt16LE', 'writeUInt16BE', 'writeUInt32LE', 'writeUInt32BE',
  'writeInt8', 'writeInt16LE', 'writeInt16BE', 'writeInt32LE', 'writeInt32BE',
  'writeFloatLE', 'writeFloatBE', 'writeDoubleLE', 'writeDoubleBE',
  'writeBigUInt64LE', 'writeBigUInt64BE', 'writeBigInt64LE', 'writeBigInt64BE',
  'swap16', 'swap32', 'swap64', 'indexOf', 'includes', 'equals',
].filter((m) => typeof Buffer.alloc(1)[m] === 'function');

process.stdout.write(JSON.stringify(results) + '\n');
