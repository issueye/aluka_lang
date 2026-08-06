const results = {};
const basic = require('./m7-fixtures/ts/basic.ts');
results.add = basic.add(2, 3);
results.point = new basic.Point(10).sum();
results.label = basic.label;
const decl = require('./m7-fixtures/ts/decl.ts');
results.declOk = decl.ok;
// strip-only 不支持诊断：归一化为错误 code。
function errCode(e) {
  const m = String(e && e.message);
  const idx = m.indexOf('ERR_UNSUPPORTED_TYPESCRIPT_SYNTAX');
  if (idx >= 0) return m.slice(idx, idx + 'ERR_UNSUPPORTED_TYPESCRIPT_SYNTAX'.length);
  return (e && e.code) || 'NONE';
}
try {
  require('./m7-fixtures/ts/enum.ts');
  results.enumErr = 'NO-ERROR';
} catch (e) {
  results.enumErr = errCode(e);
}
try {
  require('./m7-fixtures/ts/ns.ts');
  results.nsErr = 'NO-ERROR';
} catch (e) {
  results.nsErr = errCode(e);
}
process.stdout.write('RESULT ' + JSON.stringify(results));
