// M6-8 diff：node:module —— createRequire / builtinModules / isBuiltin / Module。
const m = require('node:module');
const results = {};

// surface
results.isBuiltinFn = typeof m.isBuiltin;
results.createRequireFn = typeof m.createRequire;
results.ModuleFn = typeof m.Module;
results.SourceMapFn = typeof m.SourceMap;
results.registerFn = typeof m.register;
results.registerHooksFn = typeof m.registerHooks;
results.syncBuiltinESMExportsFn = typeof m.syncBuiltinESMExports;
results.enableCompileCacheFn = typeof m.enableCompileCache;
results.getCompileCacheDirFn = typeof m.getCompileCacheDir;
results.findSourceMapFn = typeof m.findSourceMap;
results.getSourceMapsSupportFn = typeof m.getSourceMapsSupport;
results.stripTypeScriptTypesFn = typeof m.stripTypeScriptTypes;
results.findPackageJSONFn = typeof m.findPackageJSON;
results.constants = JSON.stringify(m.constants);

// builtinModules
results.isArray = Array.isArray(m.builtinModules);
results.hasFs = m.builtinModules.includes('fs');
results.hasFsPromises = m.builtinModules.includes('fs/promises');
results.hasPathPosix = m.builtinModules.includes('path/posix');
results.noNodePrefix = m.builtinModules.includes('node:fs') === false;
results.hasUnderscoreHttp = m.builtinModules.includes('_http_server');
results.count = m.builtinModules.length;

// isBuiltin
results.isBuiltinFs = m.isBuiltin('fs');
results.isBuiltinNodeFs = m.isBuiltin('node:fs');
results.isBuiltinFsPromises = m.isBuiltin('fs/promises');
results.isBuiltinBare = m.isBuiltin('node:events');
results.isBuiltinNotReal = m.isBuiltin('definitely-not-a-module');
results.isBuiltinNodeNotReal = m.isBuiltin('node:definitely-not-a-module');

// createRequire：返回 require 函数且可加载内置模块
{
  const req = m.createRequire('/tmp/foo.js');
  results.reqFn = typeof req;
  const pathMod = req('node:path');
  results.requireBuiltin = typeof pathMod.join === 'function';
}

// Module 实例面
{
  const inst = new m.Module('/tmp/bar.js');
  results.modId = inst.id;
  results.modFilenameNull = inst.filename === null;
  results.modLoaded = inst.loaded;
  results.modExportsObj = typeof inst.exports === 'object' && inst.exports !== null;
  results.modRequireFn = typeof inst.require;
  results.modChildrenArray = Array.isArray(inst.children);
}

setTimeout(() => {
  const sorted = {};
  Object.keys(results).sort().forEach((k) => { sorted[k] = results[k]; });
  process.stdout.write(JSON.stringify(sorted));
}, 20);
