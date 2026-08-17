import path from 'node:path';
import { createSSRApp } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { loadSFC } from './load-sfc.ts';

const appFile = path.join(import.meta.dir, 'src', 'App.vue');

export async function renderDemoPage(): Promise<{ html: string; css: string; body: string }> {
  const loaded = await loadSFC(appFile);
  const body = await renderToString(createSSRApp(loaded.default));
  const html = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Aluka Vue 3 双通道校验台</title>
  <link rel="stylesheet" href="/styles.css">
  <style>${loaded.css}</style>
</head>
<body>
  <div id="app">${body}</div>
</body>
</html>`;
  return { html, css: loaded.css, body };
}
