// audit-protos.mjs — B1 审计分类：对比 node/aluka protos 探针 JSON，输出按实例归类的偏差报告。
// 用法：node tools/audit-protos.mjs <node.json> <aluka.json>
'use strict';
import { readFileSync } from 'node:fs';

const n = JSON.parse(readFileSync(process.argv[2], 'utf8')).instances;
const a = JSON.parse(readFileSync(process.argv[3], 'utf8')).instances;
const dn = JSON.parse(readFileSync(process.argv[2], 'utf8')).deletes;
const da = JSON.parse(readFileSync(process.argv[3], 'utf8')).deletes;

const names = [...new Set([...Object.keys(n), ...Object.keys(a)])].sort();

function chainSummary(chain) {
  if (!chain || chain.length === 0) return '(null proto / 不可上溯)';
  return chain.map((l) => `${l.ctor ?? '?'}{${l.ownNames.length}}`).join(' -> ');
}
function diffSets(x, y) {
  const onlyNode = x.filter((k) => !y.includes(k));
  const onlyAluka = y.filter((k) => !x.includes(k));
  return { onlyNode, onlyAluka };
}

for (const name of names) {
  const nv = n[name], av = a[name];
  if (!nv) { console.log(`### ${name}: 仅 aluka 有（node 无此全局实例）`); continue; }
  if (!av) { console.log(`### ${name}: 仅 node 有（aluka 缺失）`); continue; }
  const issues = [];
  const own = diffSets(nv.ownNames, av.ownNames);
  if (own.onlyNode.length) issues.push(`ownNames 仅 node: [${own.onlyNode.join(',')}]`);
  if (own.onlyAluka.length) issues.push(`ownNames 仅 aluka: [${own.onlyAluka.join(',')}]`);
  const enumD = diffSets(nv.enumKeys, av.enumKeys);
  if (enumD.onlyNode.length) issues.push(`enumKeys 仅 node: [${enumD.onlyNode.join(',')}]`);
  if (enumD.onlyAluka.length) issues.push(`enumKeys 仅 aluka: [${enumD.onlyAluka.join(',')}]`);
  if (nv.toStringTag !== av.toStringTag) issues.push(`toStringTag: node=${nv.toStringTag} aluka=${av.toStringTag}`);
  const nChain = chainSummary(nv.protoChain), aChain = chainSummary(av.protoChain);
  if (nChain !== aChain) issues.push(`protoChain: node=${nChain} | aluka=${aChain}`);
  // 逐层比较方法集合（对齐层数时）
  const minL = Math.min(nv.protoChain?.length ?? 0, av.protoChain?.length ?? 0);
  for (let i = 0; i < minL; i++) {
    const pd = diffSets(nv.protoChain[i].ownNames, av.protoChain[i].ownNames);
    if (pd.onlyNode.length) issues.push(`  proto[${i}](${nv.protoChain[i].ctor}) 仅 node: [${pd.onlyNode.join(',')}]`);
    if (pd.onlyAluka.length) issues.push(`  proto[${i}](${av.protoChain[i].ctor}) 仅 aluka: [${pd.onlyAluka.join(',')}]`);
  }
  if (issues.length) {
    console.log(`### ${name}`);
    for (const s of issues) console.log('  ' + s);
  }
}

console.log('\n=== deleteTest（node | aluka）===');
for (const k of [...new Set([...Object.keys(dn), ...Object.keys(da)])].sort()) {
  console.log(`${k}: node=${JSON.stringify(dn[k])} aluka=${JSON.stringify(da[k])}`);
}
