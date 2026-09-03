// M2-6 diff：Buffer/TypedArray 语义 —— 分配/编码/数值读写/slice/subarray/BigInt。
const results = {};

// 1. 分配与基础
{
  const b = Buffer.from('hello');
  results.fromString = b.toString();
  results.fromStringHex = Buffer.from('hello').toString('hex');
  results.alloc = Buffer.alloc(4).toString('hex');
  results.byteLength = Buffer.byteLength('héllo');
}

// 2. slice / subarray
{
  const b = Buffer.from('abcdef');
  const s = b.slice(1, 3);
  const sa = b.subarray(1, 3);
  results.sliceStr = s.toString();
  results.subarrayStr = sa.toString();
  // slice 与源共享内存（修改 slice 影响源）
  s[0] = 90; // 'Z'
  results.sliceShared = b.toString();
}

// 3. 数值读写（readUInt16BE / writeInt32LE 等）
{
  const b = Buffer.alloc(8);
  b.writeUInt16BE(0x1234, 0);
  b.writeUInt32LE(0xdeadbeef, 2);
  b.writeInt8(-5, 6);
  results.readU16BE = b.readUInt16BE(0).toString(16);
  results.readU32LE = b.readUInt32LE(2).toString(16);
  results.readI8 = b.readInt8(6);
  results.hex = b.toString('hex');
}

// 4. BigInt 读写
{
  const b = Buffer.alloc(8);
  b.writeBigUInt64BE(12345678901234567890n, 0);
  results.bigU64 = b.readBigUInt64BE(0).toString();
}

// 5. 编码转换
{
  results.base64 = Buffer.from('hi').toString('base64');
  results.fromBase64 = Buffer.from('aGk=', 'base64').toString();
  results.utf8 = Buffer.from([0xe4, 0xbd, 0xa0]).toString('utf8'); // 你
}

// 6. TypedArray / concat / compare
{
  const arr = new Uint8Array([1, 2, 3]);
  const b = Buffer.from(arr);
  results.fromUint8 = b.toString('hex');
  const c = Buffer.concat([Buffer.from('a'), Buffer.from('b')]);
  results.concat = c.toString();
  results.compare = Buffer.compare(Buffer.from('b'), Buffer.from('a'));
  results.isBuffer = Buffer.isBuffer(Buffer.from('x'));
}

// 7. swap / fill / includes
{
  const b = Buffer.from('ab');
  b.swap16();
  results.swap16 = b.toString('hex');
  const f = Buffer.alloc(4);
  f.fill(0x2a);
  results.fill = f.toString('hex');
  results.includes = Buffer.from('hello world').includes('world');
}

process.stdout.write(JSON.stringify(results));
