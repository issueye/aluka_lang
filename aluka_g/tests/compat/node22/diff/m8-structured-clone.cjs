// M8-10 diff：structuredClone + DOMException。
const r = {};

// 基本类型
r.p1 = structuredClone(42);
r.p2 = structuredClone('hi');
r.p3 = structuredClone(null) === null;
r.p4 = structuredClone(true);
r.p5 = structuredClone(undefined) === undefined;
r.p6 = typeof structuredClone(123n) === 'bigint';

// 对象/数组深拷贝
const obj = { a: 1, b: { c: [1, 2], d: 'x' }, e: [1, 2, 3] };
const cobj = structuredClone(obj);
r.objDeep = (cobj === obj) + ':' + (cobj.b === obj.b) + ':' + cobj.b.c.join(',') + ':' + JSON.stringify(cobj);
r.arrCopy = (() => { const a = [1, [2, 3]]; const c = structuredClone(a); return (c === a) + ':' + (c[1] === a[1]) + ':' + c[1].join('.'); })();

// Map / Set / Date / Buffer
r.mapClone = (() => { const m = new Map([['k', { v: 1 }]]); const c = structuredClone(m); return (c instanceof Map) + ':' + (c.get('k').v) + ':' + (c === m); })();
r.setClone = (() => { const s = new Set([1, 2]); const c = structuredClone(s); return (c instanceof Set) + ':' + [...c].join(',') + ':' + (c === s); })();
r.dateClone = (() => { const d = new Date(1234567890); const c = structuredClone(d); return (c instanceof Date) + ':' + c.getTime() + ':' + (c === d); })();
r.bufClone = (() => { const b = Buffer.from([1, 2, 3]); const c = structuredClone(b); return c.length + ':' + (c[0] + c[1] + c[2]) + ':' + (c === b); })();

// 循环引用
r.cyclic = (() => { const o = { n: 1 }; o.self = o; const c = structuredClone(o); return (c.self === c) + ':' + c.n; })();

// 不可克隆 → 抛错（node 抛 DataCloneError DOMException；aluka 抛 TypeError）
r.fnThrow = (() => { try { structuredClone(() => {}); return 'no-throw'; } catch (e) { return 'throws'; } })();

// 对象拷贝（own 属性深拷贝）
r.proto = (() => {
  const o = { own: 1, nested: { x: 2 } };
  const c = structuredClone(o);
  return (c === o) + ':' + (c.nested === o.nested) + ':' + c.own + ':' + c.nested.x;
})();

// DOMException
r.deName = new DOMException('boom', 'AbortError').name;
r.deMessage = new DOMException('boom', 'AbortError').message;
r.deCode = new DOMException('boom', 'AbortError').code;
r.deDefault = new DOMException('x').name;
r.deIsError = new DOMException('x') instanceof Error;
r.deCtor = new DOMException('x').constructor === DOMException;
r.deCodeLookup = (() => {
  const names = ['DataCloneError', 'NotFoundError', 'SyntaxError', 'NotSupportedError', 'TimeoutError', 'QuotaExceededError', 'InvalidStateError', 'SecurityError'];
  return names.map((n) => new DOMException('m', n).code).join(',');
})();
r.deIsInstance = new DOMException('m') instanceof DOMException;

const sorted = {};
Object.keys(r).sort().forEach((k) => { sorted[k] = r[k]; });
console.log(JSON.stringify(sorted));
