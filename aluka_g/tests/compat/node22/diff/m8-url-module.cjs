// M8-2 diff：node:url 模块（legacy parse/format/resolve、fileURL 转换、
// domainToASCII/Unicode、模块导出面）。
const url = require('node:url');
const r = {};

// 导出面（URL/URLSearchParams 与全局一致，略去）
r.exports = Object.keys(url).sort().filter((k) => !['URL', 'URLSearchParams'].includes(k));

// legacy parse
// 注：search/hash 不含 '?'/'#' 为 aluka knownDifference（builtin_test 锁定），此处不对比。
const p = url.parse('http://user:pass@example.com:8080/a/b?q=1&r=2#frag');
r.pProtocol = p.protocol;
r.pHostname = p.hostname;
r.pPort = p.port;
r.pHost = p.host;
r.pPathname = p.pathname;
r.pAuth = p.auth;
r.pHref = p.href;
r.pSlashes = p.slashes;
r.pPath = p.path;
r.pQuery = p.query;
const p2 = url.parse('http://example.com:8080/a?q=1#h', true);
r.p2Query = JSON.stringify(p2.query);
r.p2Slash = p2.slashes;
const p3 = url.parse('/no/scheme?x=1');
r.p3Protocol = p3.protocol;
r.p3Host = p3.host;
r.p3Pathname = p3.pathname;

// format
r.format1 = url.format({ protocol: 'https:', hostname: 'e.com', pathname: '/x', search: '?a=1' });
r.format2 = url.format({ protocol: 'http:', hostname: 'h.com', port: '9000', pathname: '/p' });
r.format3 = url.format({ protocol: 'ftp:', hostname: 'f.com', hash: '#sec' });
r.format4 = url.format({ protocol: 'http:', auth: 'u:p', hostname: 'x.com', pathname: '/p', search: '?s=1', hash: '#h' });
r.format5 = url.format({ protocol: 'http:', host: 'custom.host:123', pathname: '/' });
r.format6 = url.format('http://str.example/path?q=1');

// resolve
r.resolve1 = url.resolve('http://a.com/x/y', '../z');
r.resolve2 = url.resolve('https://base.com/p', '/abs');
r.resolve3 = url.resolve('http://a.com/', '?q=1');

// fileURLToPath / pathToFileURL
r.ftop1 = url.fileURLToPath('file:///C:/Users/me/file.txt').replace(/\\/g, '/');
r.ftopDecoded = url.fileURLToPath('file:///C:/Users/me/a%20b.txt').replace(/\\/g, '/');
const ptu = url.pathToFileURL('C:/foo/bar.txt');
r.ptofType = typeof ptu;
r.ptofHref = ptu.href.replace(/\\/g, '/');
r.ptofProto = ptu.protocol;
const ptu2 = url.pathToFileURL('/tmp/a b.txt');
r.ptof2 = ptu2.href;

// domainToASCII / domainToUnicode
r.dta1 = url.domainToASCII('bücher.example');
r.dta2 = url.domainToASCII('example.com');
r.dtu1 = url.domainToUnicode('xn--bcher-kva.example');
r.dtuType = typeof url.domainToUnicode;
r.https = typeof url.https;
r.urlToHttpOptions = typeof url.urlToHttpOptions;

const sorted = {};
Object.keys(r).sort().forEach((k) => { sorted[k] = r[k]; });
console.log(JSON.stringify(sorted));
