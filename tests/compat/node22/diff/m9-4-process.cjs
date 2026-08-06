// M9-4: process.report / process.permission —— 方法面差分。
// report 的 getReport/writeReport 提供稳定的键面（动态值不做逐字段对比）；
// writeReport 落盘到固定文件名（Node 提示行走 stderr，run-diff.sh 一并捕获）。
// process.permission 无差分用例：Node 默认（无 --permission）为 undefined，
// aluka 提供 has() 方法面且恒 false（见 docs/adr/permissions-report.md 的
// knownDifference），直接对比会因存在性不同而失败。
const fs = require('fs');
const out = [];

// --- process.report：属性面 ---
const r = process.report;
out.push('result: report-type:' + typeof r);
out.push('result: report-keys:' + Object.keys(r).sort().join(','));
out.push('result: getReport-type:' + typeof r.getReport);
out.push('result: writeReport-type:' + typeof r.writeReport);
out.push('result: compact:' + r.compact);
out.push('result: excludeEnv:' + r.excludeEnv);
out.push('result: excludeNetwork:' + r.excludeNetwork);
out.push('result: signal:' + r.signal);
out.push('result: reportOnFatalError:' + r.reportOnFatalError);
out.push('result: reportOnUncaughtException:' + r.reportOnUncaughtException);
out.push('result: directory-type:' + typeof r.directory);
out.push('result: filename-type:' + typeof r.filename);

// --- getReport：稳定键面 ---
const gr = r.getReport();
out.push('result: header-type:' + typeof gr.header);
out.push('result: reportVersion:' + gr.header.reportVersion);
out.push('result: workers-array:' + Array.isArray(gr.workers));
out.push('result: header-keys:' + Object.keys(gr.header).sort().join(','));

// --- writeReport：固定文件名落盘 ---
const f = r.writeReport('m9-report.json');
out.push('result: writeReport-ret:' + f);
out.push('result: writeReport-exists:' + fs.existsSync(f));
out.push('result: writeReport-size:' + (fs.statSync(f).size > 0));
fs.unlinkSync(f);

console.log(out.join('\n'));
