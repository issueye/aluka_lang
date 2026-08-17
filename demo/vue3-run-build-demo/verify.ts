// aluka run：官方 vue + compiler-sfc 编译 App.vue 并 SSR。
import { renderDemoPage } from './ssr.ts';

const { body } = await renderDemoPage();
const needed = ['aluka-vue3-ok', 'data-v-c0ffee11', '双通道校验台', '计数器尚未扳动', '3.5.13'];
const missing = needed.filter((token) => !body.includes(token));
if (missing.length) {
  console.error('VERIFY_FAIL missing', missing.join(','));
  console.error(body);
  process.exit(1);
}
console.log('VERIFY_OK aluka-run vue3 ssr');
console.log(body);
