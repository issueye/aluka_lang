// probe/protos.cjs — B1 全局实例原型链审计探针（compat-boundary-closure-plan 工作流 B）。
// 枚举 globalThis 全部 string 键，对 typeof === 'object' 且非 null 的实例输出：
//   ownNames / enumKeys —— 自有键集合（排序；聚焦集合差异，插入序断言属 B4 范畴）
//   protoChain         —— 沿 Object.getPrototypeOf 上溯（≤8 层），每层 ctor 名 + 排序自有键
//   toStringTag        —— Object.prototype.toString 结果
//   deleteTest         —— 白名单实例上 delete 方法名后方法是否仍可达（自有属性 vs 原型方法的可观察差）
// 双跑输出归一化 JSON 单行；逐项独立 try/catch，避免一个异常中断全部。
'use strict';

// delete 可观察差的目标：name -> [取实例的 thunk, 方法名]。
// Node 语义：方法在原型上，delete 返回 true 且方法仍可达；自有属性模型则 delete 后不可达。
const DELETE_TARGETS = [
  ['crypto', () => globalThis.crypto, 'getRandomValues'],
  ['crypto.subtle', () => globalThis.crypto && globalThis.crypto.subtle, 'digest'],
  ['performance', () => globalThis.performance, 'now'],
  ['navigator', () => globalThis.navigator, 'userAgent'],
];

function sortedNames(obj) {
  try {
    return Object.getOwnPropertyNames(obj).sort();
  } catch (e) {
    return ['<error>'];
  }
}

function protoChainOf(v) {
  const chain = [];
  let p;
  try {
    p = Object.getPrototypeOf(v);
  } catch (e) {
    return chain;
  }
  for (let i = 0; i < 8 && p !== null && p !== undefined; i++) {
    let ctor = null;
    try {
      const c = p.constructor;
      ctor = typeof c === 'function' ? (c.name || '<anonymous>') : null;
    } catch (e) {
      ctor = '<throws>';
    }
    chain.push({ ctor, ownNames: sortedNames(p) });
    try {
      p = Object.getPrototypeOf(p);
    } catch (e) {
      break;
    }
  }
  return chain;
}

function probeInstance(v) {
  const out = {};
  out.ownNames = sortedNames(v);
  try {
    out.enumKeys = Object.keys(v).sort();
  } catch (e) {
    out.enumKeys = ['<error>'];
  }
  try {
    out.toStringTag = Object.prototype.toString.call(v);
  } catch (e) {
    out.toStringTag = '<error>';
  }
  out.protoChain = protoChainOf(v);
  return out;
}

function probeDelete(name, thunk, method) {
  let obj;
  try {
    obj = thunk();
  } catch (e) {
    return { skipped: true };
  }
  if (obj === null || obj === undefined) return { skipped: true };
  const before = typeof obj[method];
  let deleted;
  try {
    deleted = delete obj[method];
  } catch (e) {
    return { before, deleted: '<throws>' };
  }
  const after = typeof obj[method];
  return { before, deleted, after };
}

function main() {
  const instances = {};
  const globalNames = Object.getOwnPropertyNames(globalThis).sort();
  for (const name of globalNames) {
    let v;
    try {
      v = globalThis[name];
    } catch (e) {
      continue; // 访问即抛的 getter 不在本探针范畴
    }
    if (v === null || typeof v !== 'object') continue;
    instances[name] = probeInstance(v);
  }

  const deletes = {};
  for (const [name, thunk, method] of DELETE_TARGETS) {
    deletes[name + '.' + method] = probeDelete(name, thunk, method);
  }

  process.stdout.write(JSON.stringify({ probe: 'protos', instances, deletes }));
}

main();
