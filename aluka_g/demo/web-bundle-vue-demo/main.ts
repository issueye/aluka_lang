import { createSSRApp, ref, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import Counter from './components/Counter.vue';
import StatCard from './components/StatCard.vue';

export { renderToString };
export { Counter, StatCard };

const stats = ref('');

async function loadStats() {
  const data = await import('./lib/heavy-data.ts');
  stats.value = data.summary('root');
  return stats.value;
}

// 真实 vue 组件：选项式 setup + 渲染函数（render 的 _ctx 是实例代理，
// ref 自动解包——与 SFC 编译产物 render(_ctx) 的调用约定一致）。
const App = {
  setup() {
    return { stats, loadStats };
  },
  render(ctx) {
    return h('main', { class: 'app' }, [
      h('h1', null, 'Vue 3 Web Bundle'),
      h('p', { class: 'subtitle' }, '真实 vue@3.5.13 npm 包 + aluka SFC 编译器'),
      h(Counter),
      h('button', { class: 'primary', onClick: ctx.loadStats }, '加载动态 chunk'),
      ctx.stats === ''
        ? h('p', { class: 'hint' }, '点击按钮按需加载 chunk-*.js')
        : h(StatCard, { text: ctx.stats }),
    ]);
  },
};

export function createAppRoot() {
  return createSSRApp(App);
}

// Node 验证入口：无 DOM，用真实 vue/server-renderer 做 SSR 断言。
export async function renderApp() {
  return renderToString(createAppRoot());
}

export async function loadStatsOnce() {
  return loadStats();
}

// 浏览器环境自动挂载（Node 导入产物时跳过 DOM）。
if (typeof document !== 'undefined') {
  createAppRoot().mount('#app');
}
