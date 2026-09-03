// Vue 3 SSR + Tailwind CSS 示例项目自动化验证脚本
import fs from 'node:fs';
import path from 'node:path';

import { reactive, ref, computed } from './src/reactivity.js';
import { createSSRApp } from './src/vdom.js';
import { App } from './src/components/App.js';
import { parseSFC, compileTemplate } from './src/compiler.js';
import { generateTailwindCSS } from './src/tailwind.js';

console.log('=== 1. 验证响应式核心 (Reactivity) ===');
const count = ref(5);
const double = computed(() => count.value * 2);
console.log('count:', count.value, 'double:', double.value);
if (double.value !== 10) {
  throw new Error('Computed failed: expected 10, got ' + double.value);
}
count.value = 8;
console.log('after count=8 -> double:', double.value);
if (double.value !== 16) {
  throw new Error('Computed reactivity failed: expected 16, got ' + double.value);
}
console.log('✅ Reactivity test passed!');

console.log('\n=== 2. 验证服务端渲染 (SSR App) ===');
const app = createSSRApp(App);
const appHtml = await app.renderToString();
console.log('SSR HTML Output:\n', appHtml.slice(0, 160) + '...');
if (!appHtml.includes('Vue 3 + Tailwind CSS') || !appHtml.includes('响应式计算状态')) {
  throw new Error('SSR Render failed: HTML did not contain expected content');
}
console.log('✅ SSR App test passed!');

console.log('\n=== 3. 验证 Tailwind CSS JIT 即时编译生成 ===');
const tailwindCSS = generateTailwindCSS(appHtml);
console.log('Generated Tailwind CSS sample:\n', tailwindCSS.slice(0, 300) + '\n...');
if (!tailwindCSS.includes('.bg-slate-950') || !tailwindCSS.includes('.text-sky-400') || !tailwindCSS.includes('.rounded-2xl')) {
  throw new Error('Tailwind JIT failed: Missing expected utility classes');
}
console.log('✅ Tailwind CSS JIT generation test passed!');

console.log('\n=== 4. 验证 SFC 单文件组件动态编译 (Compiler) ===');
let sfcPath = path.resolve('demo/vue3-ssr-demo/src/components/UserCard.vue');
if (!fs.existsSync(sfcPath)) {
  sfcPath = path.resolve('src/components/UserCard.vue');
}
const sfcSource = fs.readFileSync(sfcPath, 'utf8');
const descriptor = parseSFC(sfcSource);
console.log('SFC Parsed: template content length =', descriptor.template.content.length);

const renderFn = compileTemplate(descriptor.template.content);
const testState = reactive({
  user: {
    name: 'Aluka Hero',
    role: 'Runtime Developer',
    bio: 'Pure Go engine with Tailwind CSS JIT'
  }
});
const vnode = renderFn(testState);
console.log('SFC VNode rendered:', vnode.type, vnode.props, vnode.children);
if (!vnode.children.includes('Aluka Hero')) {
  throw new Error('SFC Render failed: expected children to contain Aluka Hero');
}
console.log('✅ SFC Compiler test passed!');

console.log('\n🎉 All Vue 3 + Tailwind CSS checks passed successfully on Aluka Runtime!');
