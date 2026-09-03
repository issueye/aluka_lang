// probe/modules.cjs — M0 模块探针：导出身份 + 属性 descriptor + 函数 length + 类原型 + Symbol 标签。
// 同一脚本在 Node 与 Aluka 双跑，输出归一化 JSON 单行（键排序、下划线内部导出剔除）。
// 用法：node probe/modules.cjs | aluka probe/modules.cjs
'use strict';

const fs = require('node:fs');
const path = require('node:path');

const ENTRY_FILE = path.join(__dirname, '..', 'manifest', 'entry-names.json');

function ownNames(obj) {
  return Object.getOwnPropertyNames(obj).filter((n) => !n.startsWith('_'));
}

// 导出项身份：类型、函数 length/name、类原型方法、Symbol.toStringTag。
function probeExport(exp) {
  const out = { type: typeof exp };
  if (typeof exp === 'function') {
    out.length = exp.length;
    out.name = exp.name;
    const proto = exp.prototype;
    if (proto && typeof proto === 'object') {
      const methods = ownNames(proto).filter((n) => n !== 'constructor');
      if (methods.length > 0) out.protoMethods = methods;
      if (typeof Symbol !== 'undefined' && proto[Symbol.toStringTag]) {
        out.tag = String(proto[Symbol.toStringTag]);
      }
    }
  } else if (exp !== null && typeof exp === 'object') {
    out.hasToStringTag =
      typeof Symbol !== 'undefined' && !!exp[Symbol.toStringTag];
  }
  return out;
}

function probeModule(name) {
  const result = { loads: false };
  let mod = null;
  try {
    mod = require(name);
  } catch (e1) {
    try {
      mod = require('node:' + name);
    } catch (e2) {
      result.error = String((e1 && e1.code) || e1);
    }
  }
  if (mod !== undefined && mod !== null) {
    result.loads = true;
    result.exports = ownNames(mod);
    result.descriptors = {};
    result.items = {};
    for (const n of result.exports) {
      const d = Object.getOwnPropertyDescriptor(mod, n);
      result.descriptors[n] = d ? [d.writable ? 1 : 0, d.enumerable ? 1 : 0, d.configurable ? 1 : 0] : null;
      result.items[n] = probeExport(mod[n]);
    }
  }
  return result;
}

function main() {
  let entries = [];
  try {
    entries = JSON.parse(fs.readFileSync(ENTRY_FILE, 'utf8')).entries;
  } catch (e) {
    process.stderr.write('probe/modules.cjs: cannot read ' + ENTRY_FILE + ': ' + e.message + '\n');
    process.exit(1);
  }
  const modules = {};
  for (const name of entries) {
    modules[name] = probeModule(name);
  }
  process.stdout.write(JSON.stringify({ probe: 'modules', modules }));
}

main();
