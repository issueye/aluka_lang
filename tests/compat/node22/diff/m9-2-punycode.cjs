// M9-2: node:punycode —— deprecated (DEP0040) Punycode 编解码（punycode 2.1.0）。
// 差分约定：输出归一化为 result: <值> 行；DeprecationWarning 由 run-diff.sh 过滤。
// 注意：避免对含星号（astral）字符的字符串取 .length（aluka 字符串按字节，
// Node 按 UTF-16 码元），只用 JSON.stringify 输出。
const p = require('punycode');
const out = [];

out.push('result: keys:' + Object.keys(p).sort().join(','));
out.push('result: version:' + p.version);
out.push('result: ucs2-keys:' + Object.keys(p.ucs2).sort().join(','));
out.push('result: encode:' + p.encode('mañana'));
out.push('result: decode:' + p.decode('maana-pta'));
out.push('result: encode-zh:' + p.encode('你好吗'));
out.push('result: decode-zh:' + p.decode('6qqu8ipsf'));
out.push('result: toASCII:' + p.toASCII('mañana.com'));
out.push('result: toUnicode:' + p.toUnicode('xn--maana-pta.com'));
out.push('result: toASCII-zh:' + p.toASCII('例子.测试'));
out.push('result: toUnicode-zh:' + p.toUnicode('xn--fsqu00a.xn--0zwm56d'));
out.push('result: toASCII-empty:' + JSON.stringify(p.toASCII('')));
out.push('result: toUnicode-empty:' + JSON.stringify(p.toUnicode('')));
out.push('result: toASCII-mixed:' + p.toASCII('bücher.example'));
out.push('result: toUnicode-mixed:' + p.toUnicode('xn--bcher-kva.example'));
out.push('result: toASCII-email:' + p.toASCII('user@münchen.example'));
out.push('result: toUnicode-email:' + p.toUnicode('user@xn--mnchen-3ya.example'));
out.push('result: ucs2-decode:' + JSON.stringify(p.ucs2.decode('abc')));
out.push('result: ucs2-decode-astral:' + JSON.stringify(p.ucs2.decode('😀')));
out.push('result: ucs2-encode:' + p.ucs2.encode([97, 98, 99]));
out.push('result: ucs2-encode-astral:' + JSON.stringify(p.ucs2.encode([128512])));

// 失败用例：非法输入抛 RangeError（消息与 punycode.js 一致）。
try { p.decode('!!!'); out.push('result: err-decode:NO-THROW'); } catch (e) { out.push('result: err-decode:' + e.name + ':' + e.message); }
try { p.decode('a-b'); out.push('result: err-decode2:NO-THROW'); } catch (e) { out.push('result: err-decode2:' + e.name + ':' + e.message); }
try { p.encode('\u0080'); out.push('result: err-encode:NO-THROW'); } catch (e) { out.push('result: err-encode:' + e.name + ':' + e.message); }

console.log(out.join('\n'));
