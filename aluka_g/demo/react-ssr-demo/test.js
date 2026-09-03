// React 18 SSR + Tailwind CSS 自动化全链路测试

import { React } from './src/react.js';
import { renderToString } from './src/server.js';
import { compileTailwind } from './src/tailwind.js';
import { Header } from './src/components/Header.jsx';
import { MetricCard } from './src/components/MetricCard.jsx';
import { FeatureList } from './src/components/FeatureList.jsx';
import { App } from './src/components/App.jsx';

function assert(condition, message) {
  if (!condition) {
    throw new Error('Assertion Failed: ' + message);
  }
}

console.log('=== 1. 验证 React 核心与 JSX 编译 ===');
const badge = <span className="badge">Active</span>;
assert(badge.type === 'span', 'badge.type should be span');
assert(badge.props.className === 'badge', 'badge.props.className should be badge');
assert(badge.props.children === 'Active', 'badge.props.children should be Active');
console.log('✅ React createElement & JSX Lowering verified!');

console.log('\n=== 2. 验证 Header & MetricCard 组件渲染 ===');
const headerVNode = <Header title="Test App" subtitle="Sub" runtimeVersion="v1.0" />;
const headerHtml = renderToString(headerVNode);
assert(headerHtml.includes('Test App'), 'Header HTML must contain title');
assert(headerHtml.includes('v1.0'), 'Header HTML must contain runtime version');
console.log('Header HTML sample:');
console.log(' ', headerHtml.slice(0, 120) + '...');
console.log('✅ Component SSR rendering verified!');

console.log('\n=== 3. 验证根组件 App 完整 SSR 渲染 ===');
const appVNode = <App />;
const appHtml = renderToString(appVNode);
assert(appHtml.includes('Aluka React SSR &amp; Tailwind JIT') || appHtml.includes('Aluka React SSR & Tailwind JIT'), 'App HTML must contain main title');
assert(appHtml.includes('Pure Go VM'), 'App HTML must contain metric card text');
assert(appHtml.includes('Parser Plugin'), 'App HTML must contain feature tag');
console.log('App HTML length:', appHtml.length, 'bytes');
console.log('✅ App root SSR render verified!');

console.log('\n=== 4. 验证 Tailwind CSS JIT 生成 ===');
const css = compileTailwind([appHtml]);
assert(css.includes('.bg-slate-950'), 'Generated CSS must contain .bg-slate-950');
assert(css.includes('.text-sky-400'), 'Generated CSS must contain .text-sky-400');
assert(css.includes('.grid-cols-4'), 'Generated CSS must contain .grid-cols-4');
console.log('Tailwind CSS generated length:', css.length, 'bytes');
console.log('✅ Tailwind CSS JIT compilation verified!');

console.log('\n🎉 All React 18 SSR + JSX + Tailwind CSS checks passed successfully on Aluka Runtime!');
