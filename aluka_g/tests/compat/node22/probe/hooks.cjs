// probe/hooks.cjs — jiti 生态面探针（gap-closure-plan P7 / jiti M3）：
// Module._compile、require.extensions、require.cache 注入与重载、
// node:module.register + resolve/load hooks 链。
// 同一脚本在 Node 与 Aluka 双跑，输出归一化 JSON 单行。
// 用法：node probe/hooks.cjs | aluka probe/hooks.cjs
'use strict';

const { Module, createRequire, register } = require('node:module');
const path = require('node:path');

const out = {};

// 1) Module.prototype._compile：CJS 编译执行 + module.exports 重赋值。
{
  const m = new Module(path.join(__dirname, 'fake.cjs'));
  m._compile(
    'exports.answer = 40 + 2; module.exports = { double: function (x) { return x * 2 } };',
    path.join(__dirname, 'fake.cjs')
  );
  out.compile = {
    answer: m.exports.answer,
    double: m.exports.double(21),
    typeofCompile: typeof m._compile,
    lengthCompile: m._compile.length,
    lengthRegister: register.length,
    lengthCreateRequire: createRequire.length,
  };
}

// 2) require.extensions 自定义加载器（走 module._compile）。
{
  const r = createRequire(__filename);
  const xyz = path.join(__dirname, 'data.xyz');
  r.extensions['.xyz'] = function (mod, filename) {
    mod._compile('module.exports = { ext: "custom" };', filename);
  };
  out.extensions = {
    keys: Object.keys(r.extensions).sort(),
    loaded: r(xyz).ext,
  };
}

// 3) require.cache：注入条目生效、删除条目强制重载。
{
  const r = createRequire(__filename);
  const jsFile = path.join(__dirname, 'cache-data.json');
  const first = r(jsFile).v;
  const target = r.resolve(jsFile);
  r.cache[target] = { exports: { v: 'injected' } };
  const injected = r(jsFile).v;
  delete r.cache[target];
  const reloaded = r(jsFile).v;
  out.cache = { first, injected, reloaded };
}

// 4) node:module.register + resolve/load hooks（自定义扩展名 .foo）。
{
  const hooksPath = path.join(__dirname, 'hooks-fixture.mjs');
  try {
    register(pathToFileURL(hooksPath), pathToFileURL(__filename));
    out.register = { ok: true };
  } catch (e) {
    out.register = { ok: false, err: String(e && e.message) };
  }
}

async function main() {
  // 4b) hooks 加载 .foo 虚拟模块（resolve 改写 + load 覆盖源码）。
  try {
    const mod = await import('./data.foo');
    out.hooks = { val: mod.default };
  } catch (e) {
    out.hooks = { err: String(e && e.message) };
  }
  const normalized = {};
  for (const k of Object.keys(out).sort()) normalized[k] = out[k];
  process.stdout.write(JSON.stringify(normalized) + '\n');
}

// 与 Node 22 的 fileURLToPath 等价的最小实现（探针自包含）。
function pathToFileURL(p) {
  return 'file:///' + p.replace(/\\/g, '/');
}

main();