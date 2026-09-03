#!/usr/bin/env node
// gen-manifest.mjs — M0：从官方 all.json（冻结快照）生成四类机器可读 manifest。
//
// 用法：node tools/gen-manifest.mjs
// 输出：
//   manifest/entry-names.json   —— 57 个公开入口名（供探针使用）
//   manifest/modules.json       —— 57 入口、导出、类、原型方法、属性、事件、常量
//   manifest/globals.json       —— Node 全局与 Web API surface
//   manifest/errors.json        —— ERR_*、errno、错误类与参数条件
//   manifest/cli.json           —— CLI flags、环境变量、退出码和平台条件
//
// 数据源：data/all.json（v22.23.1 冻结快照）+ data/snapshot.json。
// 每项记录 name/kind/module/global/added/stability/platform/status/tests/knownDifference。
// 本工具只建立清单与官方 surface；status 由 gen-coverage.mjs 依据探针结果回填。

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(__dirname, '..');
const DATA = path.join(ROOT, 'data');
const MANIFEST = path.join(ROOT, 'manifest');

// ---------------------------------------------------------------------------
// 工具函数
// ---------------------------------------------------------------------------

function readJSON(p) {
  return JSON.parse(fs.readFileSync(p, 'utf8'));
}

// 取 API 项的 added 版本：meta.added[0] 或 introduced_in。
function firstAdded(item) {
  if (item.meta && Array.isArray(item.meta.added) && item.meta.added.length > 0) {
    return item.meta.added[0];
  }
  if (item.introduced_in) return item.introduced_in;
  return '';
}

// 取 API 项的稳定性（0=Deprecated 1=Experimental 2=Stable 3=Legacy；null=未标注）。
function stabilityOf(item, fallback) {
  if (typeof item.stability === 'number') return item.stability;
  if (item.meta && typeof item.meta.stability === 'number') return item.meta.stability;
  return typeof fallback === 'number' ? fallback : null;
}

// 从 textRaw/name 提取干净标识符：`fs.access(path[, mode], callback)` → fs.access。
function cleanName(item) {
  if (item.name && item.name !== '') return item.name;
  const raw = (item.textRaw || '').replace(/`/g, '');
  const m = raw.match(/^[A-Za-z_$][A-Za-z0-9_.$]*/);
  return m ? m[0] : raw.split('(')[0].trim();
}

// HTML 描述 → 纯文本（用于 knownDifference 说明等）。
function stripHtml(html) {
  return (html || '')
    .replace(/<[^>]*>/g, ' ')
    .replace(/&amp;/g, '&').replace(/&lt;/g, '<').replace(/&gt;/g, '>')
    .replace(/&#39;/g, "'").replace(/&quot;/g, '"')
    .replace(/&nbsp;/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
}

function apiItem(item, kind) {
  return {
    name: cleanName(item),
    kind,
    added: firstAdded(item),
    stability: stabilityOf(item),
    desc: stripHtml(item.desc).slice(0, 160) || '',
  };
}

// ---------------------------------------------------------------------------
// 1. 入口清单：Node builtinModules（去内部模块）+ node:test/reporters/sqlite
// ---------------------------------------------------------------------------

function computeEntryNames() {
  const builtins = require('node:module').builtinModules;
  const list = new Set();
  for (const b of builtins) {
    const bare = b.replace(/^node:/, '');
    // 下划线开头为内部模块（_http_*、_stream_*、_tls_*），不作为公开兼容合同。
    if (bare.startsWith('_')) continue;
    list.add(bare);
  }
  // 未列入 builtinModules 的公开入口（计划 §1.3）。
  list.add('test');
  list.add('test/reporters');
  list.add('sqlite');
  return [...list].sort();
}

// ---------------------------------------------------------------------------
// 2. 入口 → 官方文档页/小节映射
// ---------------------------------------------------------------------------

const ENTRY_DOC = {
  'assert': { page: 'assert' },
  'assert/strict': { page: 'assert', note: 'assert/strict 的严格语义见 assert 页面' },
  'async_hooks': { page: 'async_hooks' },
  'buffer': { page: 'buffer' },
  'child_process': { page: 'child_process' },
  'cluster': { page: 'cluster' },
  'console': { page: 'console' },
  'constants': { page: 'os', extract: false, note: 'constants 文档位于 os 页面，无独立页面；导出面 M3/M5 补' },
  'crypto': { page: 'crypto' },
  'dgram': { page: 'dgram' },
  'diagnostics_channel': { page: 'diagnostics_channel' },
  'dns': { page: 'dns' },
  'dns/promises': { page: 'dns', section: 'dns_promises_api' },
  'domain': { page: 'domain' },
  'events': { page: 'Events' },
  'fs': { page: 'fs' },
  'fs/promises': { page: 'fs', section: 'promises_api' },
  'http': { page: 'http' },
  'http2': { page: 'http/2' },
  'https': { page: 'https' },
  'inspector': { page: 'inspector' },
  'inspector/promises': { page: 'inspector', section: 'promises_api' },
  'module': { page: 'modules:_`node:module`_api' },
  'net': { page: 'net' },
  'os': { page: 'os' },
  'path': { page: 'path' },
  'path/posix': { page: 'path', note: 'API 面为 path.posix.*（在 path 页面）；独立入口的导出身份差分在 M1' },
  'path/win32': { page: 'path', note: 'API 面为 path.win32.*（在 path 页面）；独立入口的导出身份差分在 M1' },
  'perf_hooks': { page: 'performance_measurement_apis' },
  'process': { page: null, globals: true, note: 'process 文档位于 Global objects（Process 类）' },
  'punycode': { page: 'punycode' },
  'querystring': { page: 'querystring' },
  'readline': { page: 'readline' },
  'readline/promises': { page: 'readline', section: 'promises_api' },
  'repl': { page: 'repl' },
  'sqlite': { page: 'sqlite' },
  'stream': { page: 'stream' },
  'stream/consumers': { page: 'stream', note: 'stream/consumers 文档见 stream.md 附加读取；API 面在 M1 补' },
  'stream/promises': { page: 'stream', note: 'stream/promises 文档见 stream.md' },
  'stream/web': { page: 'web_streams_api' },
  'string_decoder': { page: 'string_decoder' },
  'sys': { page: 'util', note: 'node:util 兼容别名（废弃）' },
  'test': { page: 'test_runner' },
  'test/reporters': { page: 'test_runner', section: 'test_reporters' },
  'timers': { page: 'timers' },
  'timers/promises': { page: 'timers', section: 'timers_promises_api' },
  'tls': { page: 'tls_(ssl)' },
  'trace_events': { page: 'trace_events' },
  'tty': { page: 'tty' },
  'url': { page: 'url' },
  'util': { page: 'util' },
  'util/types': { page: 'util', note: 'util/types 文档在 util 页面' },
  'v8': { page: 'v8' },
  'vm': { page: 'vm' },
  'wasi': { page: 'webassembly_system_interface_(wasi)' },
  'worker_threads': { page: 'worker_threads' },
  'zlib': { page: 'zlib' },
};

// 官方 all.json 未结构化的 API 面（散文文档）——手工补齐，来源注明。
const CURATED_SURFACE = {
  // test_runner.md §test reporters：Node 22 导出 dot/junit/lcov/spec/tap 报告器。
  'test/reporters': {
    exports: ['dot', 'junit', 'lcov', 'spec', 'tap'].map((n) => ({ name: n, kind: 'class', added: 'v18.17.0', stability: 1, desc: 'test reporter (Transform stream)' })),
    events: [], constants: [], classes: [],
  },
  // stream.md §stream.consumers：把流消费为值（Promise）。
  'stream/consumers': {
    exports: ['arrayBuffer', 'blob', 'buffer', 'json', 'text'].map((n) => ({ name: n, kind: 'function', added: 'v16.7.0', stability: 1, desc: 'consume a stream as ' + n })),
    events: [], constants: [], classes: [],
  },
};

// ---------------------------------------------------------------------------
// 3. surface 提取
// ---------------------------------------------------------------------------

function extractClass(c) {
  const name = cleanName(c);
  return {
    name,
    kind: 'class',
    added: firstAdded(c),
    stability: stabilityOf(c),
    constructor: apiItem(c.signatures ? { name, textRaw: name } : c, 'constructor'),
    protoMethods: (c.methods || []).map((m) => apiItem(m, 'method')),
    protoProperties: (c.properties || []).map((p) => apiItem(p, 'property')),
    events: (c.events || []).map((e) => apiItem(e, 'event')),
    constants: (c.constants || []).map((k) => apiItem(k, 'constant')),
    desc: stripHtml(c.desc).slice(0, 160) || '',
  };
}

// 从文档节点提取模块级 surface。
// 官方 all.json 的 API 往往嵌套在小节里（fs→callback_api、http/2→core_api、
// diagnostics_channel→public_api、web_streams_api→api）。递归收集所有含 API 项的
// 小节（含 methods/classes/events/constants），纯描述性小节（如 notes、example）自然被排除。
function collectSurface(node, acc) {
  if (!node) return acc;
  for (const m of node.methods || []) acc.exports.push(apiItem(m, 'function'));
  for (const e of node.events || []) acc.events.push(apiItem(e, 'event'));
  for (const c of node.constants || []) acc.constants.push(apiItem(c, 'constant'));
  for (const c of node.classes || []) acc.classes.push(extractClass(c));
  for (const sub of node.modules || []) {
    const hasApi =
      (sub.methods || []).length > 0 ||
      (sub.events || []).length > 0 ||
      (sub.constants || []).length > 0 ||
      (sub.classes || []).length > 0;
    if (hasApi) collectSurface(sub, acc);
  }
  return acc;
}

function extractSurface(node) {
  return collectSurface(node, { exports: [], events: [], constants: [], classes: [] });
}

// process 没有模块页：从 all.json 顶层 globals（Process 类）提取类面作为导出。
function extractProcessSurface(allJson) {
  const acc = { exports: [], events: [], constants: [], classes: [] };
  for (const proc of allJson.globals || []) {
    acc.classes.push(extractClass({ ...proc, type: 'class' }));
    collectSurface(proc, acc);
  }
  return acc;
}

// 取文档节点：按 page/section 定位。
function findDocNode(allJson, entryName) {
  const map = ENTRY_DOC[entryName];
  if (!map) return null;
  if (map.page === null) return null;
  const page = allJson.modules.find((m) => m.name === map.page);
  if (!page) return null;
  if (map.section) {
    const section = (page.modules || []).find((m) => m.name === map.section);
    if (section) return { node: section, pageName: map.page, sectionName: map.section };
    // 小节缺失：回退到整页并记录 note。
    return { node: page, pageName: map.page, sectionName: map.section, fallback: true };
  }
  return { node: page, pageName: map.page, sectionName: null };
}

// ---------------------------------------------------------------------------
// 4. 生成 modules.json
// ---------------------------------------------------------------------------

function genModules(allJson, snapshot, entryNames) {
  const entries = [];
  for (const name of entryNames) {
    const doc = findDocNode(allJson, name);
    const map = ENTRY_DOC[name] || {};
    let surface = { exports: [], events: [], constants: [], classes: [] };
    let added = '';
    let stability = null;
    let docPage = map.page || '';
    let docSection = map.section || '';
    let docNote = '';
    if (map.extract === false) {
      // 明确标注不提取整页 surface 的入口（如 constants 指向 os 页面）。
    } else if (CURATED_SURFACE[name]) {
      surface = CURATED_SURFACE[name];
      docNote = map.note || '官方 all.json 未结构化，surface 手工补齐（来源：官方文档对应章节）';
    } else if (map.globals) {
      surface = extractProcessSurface(allJson);
      docPage = 'global-objects';
    } else if (doc) {
      surface = extractSurface(doc.node);
      added = firstAdded(doc.node) || firstAdded(doc.node.meta || {});
      stability = stabilityOf(doc.node);
    }
    if (docNote === '') {
      docNote = map.note || (doc && doc.fallback ? `小节 ${doc.sectionName} 未在 all.json 中独立成节，surface 取自整页` : '');
    }
    entries.push({
      name,
      kind: 'module',
      module: name,
      added,
      stability,
      platform: ['all'],
      docPage,
      docSection,
      docNote,
      status: 'L0', // 初始为未验证；由 gen-coverage.mjs 依据探针结果回填
      tests: [],
      knownDifference: '',
      exports: surface.exports,
      events: surface.events,
      constants: surface.constants,
      classes: surface.classes,
    });
  }
  return {
    meta: manifestMeta(snapshot, 'modules'),
    entryCount: entries.length,
    entries,
  };
}

// ---------------------------------------------------------------------------
// 5. 生成 globals.json
// ---------------------------------------------------------------------------

function genGlobals(allJson, snapshot) {
  const g = allJson.miscs.find((m) => m.name === 'Global objects') || {};
  const classes = [
    // 官方 Global objects 页面列出的全局类
    ...(g.classes || []).map((c) => extractClass(c)),
  ];
  // Process 类来自 all.json 顶层 globals。
  for (const proc of allJson.globals || []) {
    classes.push(extractClass({ ...proc, type: 'class' }));
  }
  const methods = (g.methods || []).map((m) => apiItem(m, 'method'));
  const globals = (g.miscs || []).map((m) => {
    const name = cleanName(m).replace(/^`|`$/g, '');
    return {
      name,
      kind: 'global',
      added: firstAdded(m),
      stability: stabilityOf(m),
      desc: stripHtml(m.desc).slice(0, 160) || '',
    };
  });
  // 计划 §4.1 要求但可能未出现在 Global objects 页面的补充全局（以官方文档为准，缺失即记录）。
  const required = [
    'globalThis', 'URLPattern', 'AbortSignal', 'MessageEvent', 'CloseEvent',
    'scheduler', 'atob', 'btoa', 'structuredClone', 'queueMicrotask',
  ];
  for (const r of required) {
    if (!globals.some((x) => x.name === r) && !methods.some((x) => x.name === r)) {
      globals.push({
        name: r,
        kind: 'global',
        added: '',
        stability: null,
        desc: '计划 §4.1 必需全局；官方 Global objects 页面未独立成节',
        docNote: '补充项',
      });
    }
  }
  return {
    meta: manifestMeta(snapshot, 'globals'),
    classes,
    methods,
    globals,
  };
}

// ---------------------------------------------------------------------------
// 6. 生成 errors.json
// ---------------------------------------------------------------------------

function genErrors(allJson, snapshot) {
  const err = allJson.miscs.find((m) => m.name === 'Errors') || {};
  const codes = [];
  // collectCodes 收集错误码。部分分组（如 openssl）是"分类标题 → 具体码"两级结构。
  const collectCodes = (node, prefix) => {
    for (const c of node.modules || []) {
      // 分类标题（无 meta.added、textRaw 不带反引号）递归进其子模块。
      const raw = (c.textRaw || '').replace(/`/g, '');
      if (!raw.includes('`') && Array.isArray(c.modules) && c.modules.length > 0 && !c.meta) {
        collectCodes(c, prefix);
        continue;
      }
      // 错误码的真实拼写来自 textRaw（`` `ABORT_ERR` ``），name 字段是小写化形式。
      const name = raw || cleanName(c);
      codes.push({
        name,
        group: prefix,
        added: firstAdded(c),
        stability: stabilityOf(c),
        desc: stripHtml(c.desc).slice(0, 160) || '',
      });
    }
  };
  const nodeJSErrors = (err.miscs || []).find((m) => m.name === 'node.js_error_codes');
  const legacyErrors = (err.miscs || []).find((m) => m.name === 'legacy_node.js_error_codes');
  const opensslErrors = (err.miscs || []).find((m) => m.name === 'openssl_error_codes');
  collectCodes(nodeJSErrors || {}, 'node.js_error_codes');
  collectCodes(legacyErrors || {}, 'legacy_node.js_error_codes');
  collectCodes(opensslErrors || {}, 'openssl_error_codes');

  // 错误类：Errors 页面的类 + 顶层 classes 中的错误类。
  const errorClassNames = ['Error', 'AssertionError', 'RangeError', 'ReferenceError', 'SyntaxError', 'SystemError', 'TypeError'];
  const classes = errorClassNames.map((n) => {
    const c = (err.classes || []).find((x) => x.name === n) || {};
    return extractClass(c);
  });

  return {
    meta: manifestMeta(snapshot, 'errors'),
    classes,
    codes,
  };
}

// ---------------------------------------------------------------------------
// 7. 生成 cli.json
// ---------------------------------------------------------------------------

function genCLI(allJson, snapshot) {
  const cli = allJson.miscs.find((m) => m.name === 'Command-line API') || {};
  const options = cli.miscs.find((m) => m.name === 'options') || {};
  const envMisc = cli.miscs.find((m) => m.name === 'environment_variables') || {};
  const synopsis = cli.miscs.find((m) => m.name === 'synopsis') || {};

  const flags = (options.modules || []).map((f) => ({
    name: cleanName(f).replace(/^`|`$/g, ''),
    kind: 'flag',
    added: firstAdded(f),
    stability: stabilityOf(f),
    desc: stripHtml(f.desc).slice(0, 160) || '',
  }));

  const environmentVariables = (envMisc.modules || []).map((e) => ({
    name: cleanName(e).replace(/^`|`$/g, '').split(/[=\[]/)[0],
    kind: 'environmentVariable',
    desc: stripHtml(e.desc).slice(0, 160) || '',
  }));

  // 退出码：从 synopsis 纯文本中提取 "exit code N" 上下文中的数字。
  const synopsisText = stripHtml(synopsis.desc || '');
  const exitCodes = [];
  const seen = new Set();
  for (const m of synopsisText.matchAll(/exit code[^0-9]{0,4}([0-9]+)|code '?([0-9]+)'?/gi)) {
    const code = m[1] || m[2];
    if (code && !seen.has(code)) {
      seen.add(code);
      exitCodes.push({ name: `exitCode ${code}`, kind: 'exitCode', value: code, desc: '' });
    }
  }
  if (exitCodes.length === 0) {
    // 官方文档未结构化列出退出码：记录约定值，供 M3/M7 差分验证。
    for (const c of ['0', '1', '2', '3', '4', '5', '7', '8', '9', '12', '13', '14', '15', '16', '17', '18', '20', '21', '22', '23', '24', '25', '26', '28', '30', '31', '32', '33', '34', '35', '36', '37', '38', '39', '40', '42', '43', '44', '45', '46', '47', '48', '49', '50', '51', '52', '53', '54', '55', '56', '57', '58', '59', '60', '61', '62', '63', '64', '65', '66', '67', '68', '69', '70', '71', '72', '73', '74', '75', '76', '77', '78', '79', '80', '81', '82', '83', '84', '85', '86', '87', '88', '89', '90', '91', '92', '93', '94', '95', '96', '97', '98', '99', '100', '101', '102', '103', '104', '105', '106', '107', '108', '109', '110', '111', '112', '113', '114', '115', '116', '117', '118', '119', '120', '121', '122', '123', '124', '125', '126', '127', '128', '129', '130', '131', '132', '133', '134', '135', '136', '137', '138', '139', '140', '141', '142', '143', '144', '145', '146', '147', '148', '149', '150', '151', '152', '153', '154', '155', '156', '157', '158', '159', '160', '161', '162', '163', '164', '165', '166', '167', '168', '169', '170', '171', '172', '173', '174', '175', '176', '177', '178', '179', '180', '181', '182', '183', '184', '185', '186', '187', '188', '189', '190', '191', '192', '193', '194', '195', '196', '197', '198', '199', '200', '201', '202', '203', '204', '205', '206', '207', '208', '209', '210', '211', '212', '213', '214', '215', '216', '217', '218', '219', '220', '221', '222', '223', '224', '225', '226', '227', '228', '229', '230', '231', '232', '233', '234', '235', '236', '237', '238', '239', '240', '241', '242', '243', '244', '245', '246', '247', '248', '249', '250', '251', '252', '253', '254', '255']) {
      exitCodes.push({ name: `exitCode ${c}`, kind: 'exitCode', value: c, desc: '约定退出码（官方文档未结构化列出，待 M3/M7 差分验证）' });
    }
  }

  return {
    meta: manifestMeta(snapshot, 'cli'),
    flags,
    environmentVariables,
    exitCodes,
  };
}

// ---------------------------------------------------------------------------
// 8. meta 与写盘
// ---------------------------------------------------------------------------

function manifestMeta(snapshot, kind) {
  return {
    kind,
    generator: 'tests/compat/node22/tools/gen-manifest.mjs',
    nodeRuntimeVersion: snapshot.nodeRuntimeVersion,
    nodeDocVersion: snapshot.nodeDocVersion,
    allJsonSha256: snapshot.allJsonSha256,
    platform: snapshot.platform,
    arch: snapshot.arch,
    freezeVersion: snapshot.freezeVersion,
    generatedAt: new Date().toISOString(),
  };
}

function writeJSON(file, data) {
  fs.writeFileSync(file, JSON.stringify(data, null, 2) + '\n', 'utf8');
  console.log(`written: ${path.relative(ROOT, file)} (${data.entries ? data.entries.length : ''}${data.codes ? ' codes=' + data.codes.length : ''}${data.flags ? ' flags=' + data.flags.length : ''}${data.classes ? ' classes=' + data.classes.length : ''})`);
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

function main() {
  fs.mkdirSync(MANIFEST, { recursive: true });
  const snapshot = readJSON(path.join(DATA, 'snapshot.json'));
  const allJson = readJSON(path.join(DATA, 'all.json'));

  const entryNames = computeEntryNames();
  if (entryNames.length !== 57) {
    console.warn(`WARN: 入口数=${entryNames.length}，期望 57（计划 §2.1）。若 Node 版本变更属正常。`);
  }
  writeJSON(path.join(MANIFEST, 'entry-names.json'), {
    meta: manifestMeta(snapshot, 'entry-names'),
    count: entryNames.length,
    entries: entryNames,
  });

  writeJSON(path.join(MANIFEST, 'modules.json'), genModules(allJson, snapshot, entryNames));
  writeJSON(path.join(MANIFEST, 'globals.json'), genGlobals(allJson, snapshot));
  writeJSON(path.join(MANIFEST, 'errors.json'), genErrors(allJson, snapshot));
  writeJSON(path.join(MANIFEST, 'cli.json'), genCLI(allJson, snapshot));

  console.log(`done: entries=${entryNames.length}`);
}

main();
