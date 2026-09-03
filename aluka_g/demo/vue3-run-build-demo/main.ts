// __APP_BUILD__ 来自 aluka.config.js 的 define（仅 web bundle 注入）。
declare const __APP_BUILD__: string;

import { createApp } from 'vue';
import App from '@/App.vue';

createApp(App).mount('#app');

if (typeof document !== 'undefined') {
  document.documentElement.dataset.build = __APP_BUILD__;
}
