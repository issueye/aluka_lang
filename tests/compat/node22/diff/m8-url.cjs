// M8-2 diff：全局 URL / URLSearchParams。
// 注：URLPattern 为 aluka 计划内全局补充项（node22 无全局 URLPattern），
// 不在 diff 对比范围；duplicate-key 顺序为 knownDifference（aluka 聚合同名键，
// node 保持插入序），此处用唯一键断言顺序。
const r = {};

// --- URL 基础属性 ---
const u = new URL('https://user:pass@example.com:8080/a/b/c?q=1&r=2#frag');
r.href = u.href;
r.origin = u.origin;
r.protocol = u.protocol;
r.username = u.username;
r.password = u.password;
r.host = u.host;
r.hostname = u.hostname;
r.port = u.port;
r.pathname = u.pathname;
r.search = u.search;
r.hash = u.hash;
r.toString = u.toString();
r.toJSON = u.toJSON();

// 相对解析
const rel = new URL('/foo', 'https://example.com/base/');
r.relHref = rel.href;
const rel2 = new URL('../x', 'https://example.com/a/b/c');
r.rel2Href = rel2.href;

// searchParams 绑定
const u2 = new URL('https://e.com/p?old=1&mid=2');
u2.searchParams.set('new', '3');
r.bindSearch = u2.search;
r.bindHref = u2.href;
u2.searchParams.delete('old');
r.bindDelete = u2.href;
u2.searchParams.sort();
r.bindSort = u2.search;
u2.searchParams.append('z', '9');
r.bindAppend = u2.search;

// URL 构造器/静态面
r.canParse = typeof URL.canParse;
r.canParseBad = typeof URL.canParse === 'function' ? URL.canParse('not a url') : 'no-fn';
r.isInstance = new URL('http://x') instanceof URL;
r.ctorProp = new URL('http://x').constructor === URL;

// --- URLSearchParams ---
const sp = new URLSearchParams('a=1&b=2&c=3&empty=');
r.spGet = sp.get('a');
r.spGetMissing = sp.get('nope');
r.spGetAllDup = JSON.stringify(new URLSearchParams('a=1&a=2').getAll('a'));
r.spHas = sp.has('b');
r.spSize = sp.size;
r.spString = sp.toString();
sp.append('d', 'x y');
r.spAppendString = sp.toString();
sp.set('c', '9');
r.spSetString = sp.toString();
r.spEntries = JSON.stringify([...sp].length);
r.spForEachOrder = (() => { const o = []; sp.forEach((v, k) => o.push(k + '=' + v)); return o.join(','); })();
r.spKeys = Array.from(sp.keys()).join(',');
r.spValues = Array.from(sp.values()).join(',');

// 构造参数形式
r.spFromObj = new URLSearchParams({ x: 1, y: 'two' }).toString();
r.spFromPairs = new URLSearchParams([['k', 'v1'], ['k', 'v2'], ['m', 'n']]).toString();
const sp2 = new URLSearchParams('b=2&a=1&c=3');
sp2.sort();
r.spSort = sp2.toString();
r.spHashOrder = new URLSearchParams('#a=1&b=2').toString();
r.spDelAfter = (() => { const s = new URLSearchParams('a=1&b=2&c=3'); s.delete('b'); return s.toString(); })();

// 编码语义
r.spEncode = new URLSearchParams('s=hello world&q=a+b&slash=/&space=%20').toString();
const sp3 = new URLSearchParams();
sp3.set('emoji', '😀');
r.spEmoji = sp3.toString();
const sp4 = new URLSearchParams();
sp4.set('amp', 'a&b');
r.spAmp = sp4.toString();

// URL + searchParams 迭代器（数组展开 / for-of）
const u3 = new URL('https://e.com/x?a=1&b=2');
let iter = [];
for (const [k, v] of u3.searchParams) iter.push(k + '=' + v);
r.searchParamsIterable = iter.join(',');
r.spSpread = (() => { const s = new URLSearchParams('x=1&y=2'); return [...s].map((p) => p.join(':')).join('|'); })();

const sorted = {};
Object.keys(r).sort().forEach((k) => { sorted[k] = r[k]; });
console.log(JSON.stringify(sorted));
