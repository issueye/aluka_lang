// 语料提取：扫描 Vue demo 依赖闭包中的正则字面量，写入
// internal/engine/regex/testdata/corpus.txt（pattern \t flags 每行一条）。
// 供双引擎对拍测试（parity_test.go）使用；依赖升级后重跑刷新。
//   node tools/extract-regex-corpus.mjs
import fs from 'node:fs';
import path from 'node:path';

const ROOT = path.resolve(import.meta.dirname, '..');
const FILES = [
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
].map((f) => path.join(ROOT, 'demo/web-bundle-vue-demo', f));

const seen = new Set();
const out = [];
for (const f of FILES.filter((p) => fs.existsSync(p))) {
  const src = fs.readFileSync(f, 'utf8');
  // 启发式：出现在 = ( , [ return ? : ! && || 之后的 /re/flags；
  // 排除 /*__PURE__*/ 类注释（body 以 * 开头）与过长/多行形态。
  const re =
    /(?:=|\(|,|\[|\breturn\b|&&|\|\||\?|:|!)\s*\/((?:\\.|\[(?:\\.|[^\]\\\n])*\]|[^/\\\[\n])+)\/([a-z]*)/g;
  let m;
  while ((m = re.exec(src))) {
    const body = m[1];
    const flags = m[2] || '';
    if (body.startsWith('*') || body.length > 160) continue;
    if (body.includes('\n')) continue;
    const line = body + '\t' + (flags || '-');
    if (seen.has(line)) continue;
    seen.add(line);
    out.push(line);
  }
}
const target = path.join(ROOT, 'internal/engine/regex/testdata/corpus.txt');
fs.mkdirSync(path.dirname(target), { recursive: true });
fs.writeFileSync(target, out.join('\n') + '\n');
console.log(`corpus: ${out.length} patterns -> ${path.relative(ROOT, target)}`);
