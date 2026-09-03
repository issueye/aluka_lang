// M6-2 diff：node:v8 —— 堆统计 / serialize+deserialize / Serializer/Deserializer 面。
const v8 = require('node:v8');
const results = {};

// surface
results.exports = Object.keys(v8).sort();
results.cachedDataVersionTag = typeof v8.cachedDataVersionTag();
results.setFlagsFromStringFn = typeof v8.setFlagsFromString;
results.writeHeapSnapshotFn = typeof v8.writeHeapSnapshot;
results.takeCoverageFn = typeof v8.takeCoverage;
results.stopCoverageFn = typeof v8.stopCoverage;
results.queryObjectsFn = typeof v8.queryObjects;
results.isStringOneByteFn = typeof v8.isStringOneByteRepresentation;
results.setHeapSnapshotNearHeapLimitFn = typeof v8.setHeapSnapshotNearHeapLimit;
results.promiseHooksType = typeof v8.promiseHooks.createHook;
results.startupSnapshotType = typeof v8.startupSnapshot.addSerializeCallback;
results.GCProfilerFn = typeof v8.GCProfiler;

// 堆统计：键集与值类型
{
  const hs = v8.getHeapStatistics();
  results.heapKeys = Object.keys(hs).sort();
  results.heapValsAreNumbers = Object.values(hs).every((v) => typeof v === 'number');
  const spaces = v8.getHeapSpaceStatistics();
  results.spaceCount = spaces.length;
  results.spaceFirstKeys = Object.keys(spaces[0]).sort();
  results.spaceNamesAreStrings = spaces.every((s) => typeof s.space_name === 'string');
  const hc = v8.getHeapCodeStatistics();
  results.codeKeys = Object.keys(hc).sort();
  results.codeValsAreNumbers = Object.values(hc).every((v) => typeof v === 'number');
}

// serialize / deserialize（JSON 简化：往返可读）
{
  const buf = v8.serialize({ a: 1, b: 'x', c: [1, 2], d: true, e: null });
  results.serializeIsBuffer = Buffer.isBuffer(buf);
  results.serializeLen = buf.length > 0;
  const obj = v8.deserialize(buf);
  results.roundtripA = obj.a;
  results.roundtripB = obj.b;
  results.roundtripC = JSON.stringify(obj.c);
  results.roundtripD = obj.d;
  results.roundtripENull = obj.e === null;
  results.deserializeNum = v8.deserialize(v8.serialize(42));
  results.deserializeArr = JSON.stringify(v8.deserialize(v8.serialize([1, 'two', 3])));
}

// Serializer / Deserializer 方法面
{
  const s = new v8.Serializer();
  results.serWriteHeader = typeof s.writeHeader;
  results.serWriteValue = typeof s.writeValue;
  results.serWriteUint32 = typeof s.writeUint32;
  results.serWriteDouble = typeof s.writeDouble;
  results.serReleaseBuffer = typeof s.releaseBuffer;
  const d = new v8.Deserializer(new Uint8Array([1, 2, 3]));
  results.deserReadHeader = typeof d.readHeader;
  results.deserReadValue = typeof d.readValue;
  results.deserReadUint32 = typeof d.readUint32;
}

// getHeapSnapshot 方法面（Node：HeapSnapshotStream——Readable 流）
{
  const snap = v8.getHeapSnapshot();
  results.snapIsObject = typeof snap === 'object' && snap !== null;
  results.snapOnFn = typeof snap.on;
  results.snapPipeFn = typeof snap.pipe;
}

setTimeout(() => {
  const sorted = {};
  Object.keys(results).sort().forEach((k) => { sorted[k] = results[k]; });
  process.stdout.write(JSON.stringify(sorted));
}, 20);
