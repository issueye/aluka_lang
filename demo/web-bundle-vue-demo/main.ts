import { ref, h, createApp, renderToString } from './vue.ts';
import Counter from './components/Counter.vue';
import StatCard from './components/StatCard.vue';

export { renderToString };
export { Counter };

const stats = ref('');

async function loadStats() {
  const data = await import('./lib/heavy-data.ts');
  stats.value = data.summary('root');
  return stats.value;
}

const App = {
  setup() {
    return { stats, loadStats };
  },
  render(ctx) {
    return h('main', { className: 'app' }, [
      h('h1', null, 'Vue SFC Web Bundle'),
      h('p', { className: 'subtitle' }, '.vue 单文件组件：template 构建期编译 + 组合式 API'),
      h(Counter),
      h('button', { className: 'primary', onClick: ctx.loadStats }, '加载动态 chunk'),
      ctx.stats.value === ''
        ? h('p', { className: 'hint' }, '点击按钮按需加载 chunk-*.js')
        : h(StatCard, { text: ctx.stats.value }),
    ]);
  },
};

// Node 验证入口：无 DOM 也能拿到渲染结果与响应式状态。
let ctx = null;

function appCtx() {
  if (ctx === null) ctx = App.setup();
  return ctx;
}

export function renderApp() {
  return renderToString(App.render(appCtx()));
}

export function loadStatsOnce() {
  return appCtx().loadStats();
}

// 浏览器环境自动挂载（Node 导入产物时跳过 DOM）。
if (typeof document !== 'undefined') {
  createApp(App, appCtx()).mount('#app');
}
