#!/usr/bin/env node
// compare-probe.mjs — 深度对比两个探针 JSON 输出，打印差异叶子路径。
// 用法：node tools/compare-probe.mjs <node.json> <aluka.json> [maxDiffs]
'use strict';
import { readFileSync } from 'node:fs';

const [nodePath, alukaPath, maxArg] = process.argv.slice(2);
const maxDiffs = Number(maxArg) || 200;
const a = JSON.parse(readFileSync(nodePath, 'utf8'));
const b = JSON.parse(readFileSync(alukaPath, 'utf8'));

const diffs = [];
function walk(x, y, p) {
  if (typeof x !== typeof y) { diffs.push(p + ': type ' + typeof x + ' vs ' + typeof y); return; }
  if (x === null || y === null) { if (x !== y) diffs.push(p); return; }
  if (typeof x !== 'object') { if (String(x) !== String(y)) diffs.push(p + ': ' + JSON.stringify(x) + ' vs ' + JSON.stringify(y)); return; }
  if (Array.isArray(x) && Array.isArray(y)) {
    if (x.length !== y.length) diffs.push(p + ': len ' + x.length + ' vs ' + y.length);
    const n = Math.min(x.length, y.length);
    for (let i = 0; i < n; i++) walk(x[i], y[i], p + '[' + i + ']');
    return;
  }
  const keys = new Set([...Object.keys(x), ...Object.keys(y)]);
  for (const k of keys) {
    if (!(k in x)) { diffs.push(p + '.' + k + ': missing in node'); continue; }
    if (!(k in y)) { diffs.push(p + '.' + k + ': missing in aluka'); continue; }
    walk(x[k], y[k], p + '.' + k);
  }
}
walk(a, b, '');
console.log(diffs.slice(0, maxDiffs).join('\n') + (diffs.length > maxDiffs ? '\n... truncated (' + diffs.length + ' total)' : ''));
console.error('total: ' + diffs.length);
