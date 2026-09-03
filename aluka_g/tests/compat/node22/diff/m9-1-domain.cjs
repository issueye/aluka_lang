// M9-1: node:domain —— deprecated (DEP0003) legacy error-routing module.
// 差分约定：输出归一化为 result: <值> 行；DeprecationWarning 由 run-diff.sh 过滤。
// 注意：Node 的 bind 回调抛错后会让 domain 保持 enter 状态（共享 stack 污染），
// 本用例如实反映该行为，各段用独立 domain 观察。
const d = require('domain');
const { EventEmitter } = require('events');
const out = [];

// --- API surface ---
out.push('result: create:' + typeof d.create + ',Domain:' + typeof d.Domain + ',createDomain:' + typeof d.createDomain);
out.push('result: active-initial-null:' + (d.active === null));
const dom = d.create();
out.push('result: methods:' + ['run', 'bind', 'intercept', 'enter', 'exit', 'add', 'remove'].map(m => typeof dom[m]).join(','));
out.push('result: members:' + JSON.stringify(dom.members));
out.push('result: run-ret:' + dom.run(() => 42));
out.push('result: run-after-undef:' + (d.active === undefined));

// --- bind: thrown error propagates to caller; Node quirk leaves domain entered ---
const dom2 = d.create();
const bf = dom2.bind(() => { throw new Error('bind-boom'); });
try { bf(); } catch (e) { out.push('result: bind-caught:' + e.message); }
out.push('result: bind-quirk-active:' + (d.active === dom2));

// --- intercept: non-Error first arg → skipped; cb gets remaining args ---
const dom3 = d.create();
let got = [];
const inf = dom3.intercept((...a) => got.push(a));
inf(123, 'hello');
out.push('result: intercept-nonerr:' + JSON.stringify(got));
out.push('result: intercept-active-dom2:' + (d.active === dom2));

// --- intercept: Error first arg → routed to domain 'error' handler, cb skipped ---
dom3.on('error', (e) => out.push('result: intercept-domerr:' + e.message));
got = [];
inf(new Error('intercept-boom'));
out.push('result: intercept-cb-skip:' + (got.length === 0));

// --- enter/exit lifecycle ---
const dom4 = d.create();
out.push('result: enter-before-null:' + (d.active === null));
dom4.enter();
out.push('result: enter-in:' + (d.active === dom4) + ':' + (process.domain === dom4));
dom4.exit();
out.push('result: enter-after-dom2:' + (d.active === dom2));

// --- add/remove members ---
const ee = new EventEmitter();
out.push('result: m0:' + dom4.members.length);
dom4.add(ee);
out.push('result: m1:' + dom4.members.length + ':' + (ee.domain === dom4));
dom4.remove(ee);
out.push('result: m2:' + dom4.members.length + ':' + (ee.domain === null));

// --- emitter 'error' routed to domain via domain.add ---
dom4.on('error', (e) => out.push('result: ee-domerr:' + e.message));
const ee2 = new EventEmitter();
dom4.add(ee2);
ee2.emit('error', new Error('emitter-boom'));
out.push('result: after-emitter-error');

console.log(out.join('\n'));
