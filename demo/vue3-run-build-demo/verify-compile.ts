// aluka build --compile：只依赖 vue runtime（不含 compiler-sfc），校验可执行产物能 SSR。
import { computed, createSSRApp, h, ref, version } from 'vue';
import { renderToString } from 'vue/server-renderer';

const App = {
  setup() {
    const count = ref(0);
    const doubled = computed(() => count.value * 2);
    return () =>
      h('main', { 'data-probe': 'aluka-vue3-ok', class: 'bench' }, [
        h('p', { class: 'eid' }, 'ALUKA · VUE ' + version + ' · SERIAL 3.5.13'),
        h('h1', null, '双通道校验台'),
        h('span', { class: 'count' }, String(count.value)),
        h('small', null, '×2 ' + doubled.value),
      ]);
  },
};

const html = await renderToString(createSSRApp(App));
if (!html.includes('aluka-vue3-ok') || !html.includes('双通道校验台')) {
  console.error('VERIFY_FAIL');
  console.error(html);
  process.exit(1);
}
console.log('VERIFY_OK aluka-compile vue3 ssr');
console.log(html);
