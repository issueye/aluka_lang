// 语料提取：解析 Vue demo 依赖闭包中的正则字面量，写入
// internal/engine/regex/testdata/corpus.txt（pattern \t flags 每行一条）。
// 供双引擎对拍测试（parity_test.go）使用；依赖升级后重跑刷新。
//   node tools/extract-regex-corpus.mjs
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';

const ROOT = path.resolve(import.meta.dirname, '..');
const DEMO = path.join(ROOT, 'demo/web-bundle-vue-demo');
const require = createRequire(path.join(DEMO, 'package.json'));
const { parse } = require('@babel/parser');
const RELATIVE_FILES = [
  'node_modules/@vue/compiler-sfc/dist/compiler-sfc.cjs.js',
  'node_modules/@vue/compiler-core/dist/compiler-core.cjs.js',
  'node_modules/@vue/compiler-dom/dist/compiler-dom.cjs.js',
  'node_modules/@vue/compiler-ssr/dist/compiler-ssr.cjs.js',
  'node_modules/@babel/parser/lib/index.js',
  'node_modules/postcss/lib/postcss.mjs',
  'node_modules/magic-string/dist/magic-string.cjs.js',
  'node_modules/source-map-js/source-map.js',
  'node_modules/entities/lib/esm/index.js',
  'node_modules/estree-walker/dist/esm/estree-walker.js',
];
const FILES = RELATIVE_FILES.map((file) => path.join(DEMO, file));
const missing = FILES.filter((file) => !fs.existsSync(file));
if (missing.length > 0) {
  throw new Error(
    `regex corpus sources are missing:\n${missing.map((file) => `  ${path.relative(ROOT, file)}`).join('\n')}`,
  );
}

const seen = new Set();
const out = [];
function collectRegexLiterals(root) {
  const stack = [root];
  while (stack.length > 0) {
    const value = stack.pop();
    if (!value || typeof value !== 'object') continue;
    if (value.type === 'RegExpLiteral') {
      const line = `${value.pattern}\t${value.flags || '-'}`;
      if (!seen.has(line)) {
        seen.add(line);
        out.push(line);
      }
      continue;
    }
    for (const [key, child] of Object.entries(value)) {
      if (key === 'loc' || key === 'start' || key === 'end' || key === 'extra') continue;
      if (Array.isArray(child)) {
        for (let i = child.length - 1; i >= 0; i--) stack.push(child[i]);
      } else if (child && typeof child === 'object') {
        stack.push(child);
      }
    }
  }
}

for (const file of FILES) {
  const source = fs.readFileSync(file, 'utf8');
  const ast = parse(source, {
    sourceType: 'unambiguous',
    errorRecovery: true,
    plugins: ['jsx', 'typescript'],
  });
  collectRegexLiterals(ast.program);
}

out.sort();
const target = path.join(ROOT, 'internal/engine/regex/testdata/corpus.txt');
fs.writeFileSync(target, `${out.join('\n')}\n`);
console.log(`corpus: ${out.length} patterns -> ${path.relative(ROOT, target)}`);
