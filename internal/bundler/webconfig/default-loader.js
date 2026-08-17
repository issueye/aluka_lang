// Aluka web 配置发现脚本（可被 ALUKA_WEB_CONFIG 整文件替换）。
// 用目录扫描 + 对象形态识别项目打包配置，不在 Go 里写死 vite/vue 文件名。
"use strict";

const fs = require("node:fs");
const path = require("node:path");

const CONFIG_FILE = /\.config\.(js|ts|mjs|cjs|mts|cts)$/i;
const PROJECT_HOOK = /^aluka\.config\./i;
const SHAPE_KEYS = [
  "base",
  "publicPath",
  "build",
  "resolve",
  "outputDir",
  "outDir",
  "assetsDir",
  "alias",
  "define",
  "vueCompiler",
  "configureWebpack",
];
// 读源码粗判，避免执行 jest/eslint/babel 等无关 *.config.*。
const PEEK_HINTS = [
  /\bdefineConfig\b/,
  /\bpublicPath\b/,
  /\bconfigureWebpack\b/,
  /\boutputDir\b/,
  /\boutDir\b/,
  /\bassetsDir\b/,
  /\bvueCompiler\b/,
  /\bpublicBase\b/,
  /\bfrom\s+['"]vite['"]/,
  /\brequire\(\s*['"]vite['"]\s*\)/,
  /@vitejs\/plugin-vue/,
];

function isConfigFile(name) {
  return CONFIG_FILE.test(name);
}

function firstDefined(obj, keys) {
  if (!obj || typeof obj !== "object") {
    return undefined;
  }
  for (let i = 0; i < keys.length; i++) {
    const v = obj[keys[i]];
    if (v !== undefined && v !== null) {
      return v;
    }
  }
  return undefined;
}

function looksLikeBundlerConfig(obj) {
  if (!obj || typeof obj !== "object" || Array.isArray(obj)) {
    return false;
  }
  return SHAPE_KEYS.some((k) => obj[k] != null);
}

function sourceLooksLikeBundlerConfig(src) {
  if (typeof src !== "string") {
    return false;
  }
  for (let i = 0; i < PEEK_HINTS.length; i++) {
    if (PEEK_HINTS[i].test(src)) {
      return true;
    }
  }
  return false;
}

function resolveAliasPath(root, value) {
  const s = String(value);
  if (path.isAbsolute(s)) {
    return s;
  }
  return path.resolve(root, s);
}

function flattenAlias(alias, root) {
  const out = {};
  if (!alias) {
    return out;
  }
  if (Array.isArray(alias)) {
    for (const item of alias) {
      if (item && typeof item.find === "string" && item.replacement != null) {
        out[item.find] = resolveAliasPath(root, item.replacement);
      }
    }
    return out;
  }
  for (const key of Object.keys(alias)) {
    const v = alias[key];
    if (typeof v === "string") {
      out[key] = resolveAliasPath(root, v);
    }
  }
  return out;
}

function flattenDefine(define) {
  const out = {};
  if (!define || typeof define !== "object") {
    return out;
  }
  for (const key of Object.keys(define)) {
    const v = define[key];
    if (v === undefined) {
      continue;
    }
    out[key] = typeof v === "string" ? v : JSON.stringify(v);
  }
  return out;
}

function unwrap(mod) {
  if (mod && typeof mod === "object" && "default" in mod && mod.default !== undefined) {
    return mod.default;
  }
  return mod;
}

function invokeIfFn(raw, ctx) {
  if (typeof raw === "function") {
    return raw(ctx);
  }
  return raw;
}

function inferVueCompiler(raw) {
  const explicit = firstDefined(raw, ["vueCompiler"]) ?? firstDefined(raw && raw.build, ["vueCompiler"]);
  if (explicit != null) {
    return String(explicit);
  }
  const plugins = raw && raw.plugins;
  if (!Array.isArray(plugins)) {
    return undefined;
  }
  for (let i = 0; i < plugins.length; i++) {
    const p = plugins[i];
    const name = p && p.name;
    if (name === "vue" || name === "vite:vue") {
      return "official";
    }
  }
  return undefined;
}

function normalize(raw, source, root) {
  const build = raw && raw.build && typeof raw.build === "object" ? raw.build : {};
  const resolve = raw && raw.resolve && typeof raw.resolve === "object" ? raw.resolve : {};
  const out = { source: source || "" };
  const base = firstDefined(raw, ["base", "publicPath"]);
  if (base != null) {
    out.base = String(base);
  }
  const outDir = firstDefined(build, ["outDir"]) ?? firstDefined(raw, ["outputDir", "outDir"]);
  if (outDir != null) {
    out.outDir = String(outDir);
  }
  const assetsDir = firstDefined(build, ["assetsDir"]) ?? firstDefined(raw, ["assetsDir"]);
  out.assetsDir = assetsDir != null ? String(assetsDir) : "assets";
  if (build.minify != null) {
    out.minify = !!build.minify;
  } else if (raw && raw.minify != null) {
    out.minify = !!raw.minify;
  }
  const vueCompiler = inferVueCompiler(raw);
  if (vueCompiler != null) {
    out.vueCompiler = vueCompiler;
  }
  const alias = flattenAlias(resolve.alias || raw.alias, root || ".");
  if (Object.keys(alias).length) {
    out.alias = alias;
  }
  const define = flattenDefine(raw.define);
  if (Object.keys(define).length) {
    out.define = define;
  }
  return out;
}

function listConfigFiles(root) {
  let names;
  try {
    names = fs.readdirSync(root);
  } catch (e) {
    return [];
  }
  return names.filter((n) => isConfigFile(n) && n.charAt(0) !== ".");
}

function requireConfig(root, name) {
  const full = path.join(root, name);
  return unwrap(require(full));
}

function shouldLoadConfigFile(root, name) {
  if (PROJECT_HOOK.test(name)) {
    return true;
  }
  let src;
  try {
    src = fs.readFileSync(path.join(root, name), "utf8");
  } catch (e) {
    return false;
  }
  return sourceLooksLikeBundlerConfig(src);
}

function flattenPlugins(list) {
  const out = [];
  function walk(x) {
    if (x == null || x === false) {
      return;
    }
    if (Array.isArray(x)) {
      for (let i = 0; i < x.length; i++) {
        walk(x[i]);
      }
      return;
    }
    out.push(x);
  }
  walk(list);
  return out;
}

function loadRawConfig(root) {
  const ctx = { root: root, command: "build", mode: "production" };
  const files = listConfigFiles(root);
  const hooks = files.filter((n) => PROJECT_HOOK.test(n)).sort();
  if (hooks.length) {
    const raw = invokeIfFn(requireConfig(root, hooks[0]), ctx);
    if (raw && typeof raw.then === "function") {
      throw new Error("aluka webconfig: async config is not supported in " + hooks[0]);
    }
    return { raw: raw && typeof raw === "object" ? raw : {}, source: hooks[0] };
  }
  const others = files.filter((n) => !PROJECT_HOOK.test(n) && shouldLoadConfigFile(root, n)).sort();
  for (const name of others) {
    let raw;
    try {
      raw = invokeIfFn(requireConfig(root, name), ctx);
    } catch (err) {
      throw new Error("aluka webconfig: failed to load " + name + ": " + (err && err.message ? err.message : err));
    }
    if (raw && typeof raw.then === "function") {
      throw new Error("aluka webconfig: async config is not supported in " + name);
    }
    if (looksLikeBundlerConfig(raw)) {
      return { raw: raw, source: name };
    }
  }
  return null;
}

function loadWebConfig(root) {
  const found = loadRawConfig(root);
  if (!found) {
    return {};
  }
  return normalize(found.raw, found.source, root);
}

function loadWebConfigJSON(root) {
  return JSON.stringify(loadWebConfig(root));
}

function loadWebSession(root) {
  const found = loadRawConfig(root);
  if (!found) {
    return { json: "{}", plugins: [] };
  }
  return {
    json: JSON.stringify(normalize(found.raw, found.source, root)),
    plugins: flattenPlugins(found.raw && found.raw.plugins),
  };
}

module.exports = { loadWebConfig, loadWebConfigJSON, loadWebSession, normalize };
