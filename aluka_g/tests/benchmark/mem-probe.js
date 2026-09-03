// 内存基线探针：各负载下 process.memoryUsage()
function report(label) {
  const m = process.memoryUsage();
  console.log(`${label}: heapUsed=${(m.heapUsed/1048576).toFixed(1)}MB heapTotal=${(m.heapTotal/1048576).toFixed(1)}MB rss=${(m.rss/1048576).toFixed(1)}MB`);
}
report("baseline");
// 大数组
const a = [];
for (let i = 0; i < 200000; i++) a.push({ idx: i, tag: "item" + i });
report("after-200K-array");
// 字符串拼接
let s = "";
for (let i = 0; i < 50000; i++) s += "chunk" + i;
report("after-50K-concat");
// 大量短生命周期对象
let keep = [];
for (let i = 0; i < 200000; i++) { const o = { x: i, y: [i] }; if (i % 100 === 0) keep.push(o); }
report("after-200K-obj");
