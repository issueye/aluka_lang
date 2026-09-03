// Vue 3 SSR + Tailwind CSS 示例 Web 服务
import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';

import { createSSRApp } from './src/vdom.js';
import { App } from './src/components/App.js';
import { parseSFC, compileTemplate } from './src/compiler.js';
import { reactive } from './src/reactivity.js';
import { generateTailwindCSS } from './src/tailwind.js';

const PORT = 3001;

function wrapHtml(title, bodyContent) {
  // 服务端即时编译当前页面用到的所有 Tailwind CSS 类名
  const tailwindCSS = generateTailwindCSS(bodyContent);

  return `<!DOCTYPE html>
<html lang="zh-CN" class="bg-slate-950">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>${title}</title>
  <style>
${tailwindCSS}
  </style>
</head>
<body class="bg-slate-950 text-slate-100 antialiased">
  ${bodyContent}
</body>
</html>`;
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://${req.headers.host || '127.0.0.1'}`);

  // 健康检查
  if (url.pathname === '/echo/ready' || url.pathname === '/echo/portcheck') {
    res.writeHead(200, { 'Content-Type': 'text/plain' });
    res.end('ok');
    return;
  }

  // API: 响应式状态快照
  if (url.pathname === '/api/state') {
    const state = reactive({
      engine: 'Aluka (100% Pure Go)',
      nodeEnv: process.env.NODE_ENV || 'development',
      framework: 'Vue 3 SSR + Tailwind CSS JIT',
      uptime: process.uptime()
    });
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify(state));
    return;
  }

  // 查看生成的原始 Tailwind CSS 规则
  if (url.pathname === '/tailwind-css') {
    try {
      const app = createSSRApp(App);
      const appHtml = await app.renderToString();
      const css = generateTailwindCSS(appHtml);
      res.writeHead(200, { 'Content-Type': 'text/css; charset=utf-8' });
      res.end(css);
      return;
    } catch (err) {
      res.writeHead(500, { 'Content-Type': 'text/plain; charset=utf-8' });
      res.end('CSS Error: ' + err.message);
      return;
    }
  }

  // SFC 演示页面
  if (url.pathname === '/sfc') {
    try {
      let sfcPath = path.resolve('demo/vue3-ssr-demo/src/components/UserCard.vue');
      if (!fs.existsSync(sfcPath)) {
        sfcPath = path.resolve('src/components/UserCard.vue');
      }
      const sfcContent = fs.readFileSync(sfcPath, 'utf8');

      // 解析 SFC 单文件组件
      const descriptor = parseSFC(sfcContent);

      // 动态编译 template 为 render 函数
      const renderFn = compileTemplate(descriptor.template.content);

      // 响应式状态
      const state = reactive({
        user: {
          name: 'Aluka Gopher (SFC)',
          role: 'Compiled in Pure Go VM',
          bio: 'Dynamically compiled at runtime via new Function and styled with Tailwind CSS JIT.'
        }
      });
      const vnode = renderFn(state);

      const body = `
        <div class="min-h-screen bg-slate-950 text-slate-100 p-8">
          <div class="max-w-3xl mx-auto">
            <header class="border-b border-slate-800 pb-6 mb-8 flex items-center justify-between">
              <div>
                <h1 class="text-3xl font-bold tracking-tight text-sky-400">📦 Vue 3 SFC + Tailwind CSS</h1>
                <p class="text-sm text-slate-400 mt-1">Parsed from <code>UserCard.vue</code> and rendered on the server.</p>
              </div>
              <a href="/" class="px-4 py-2 bg-slate-800 hover:bg-slate-700 text-sky-400 text-xs font-semibold rounded-lg border border-slate-700">← Back to Main App</a>
            </header>
            <div class="my-8">
              <div class="${vnode.props.class || ''}">${vnode.children}</div>
            </div>
          </div>
        </div>
      `;

      res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
      res.end(wrapHtml('Vue 3 SFC + Tailwind CSS - Aluka', body));
      return;
    } catch (err) {
      res.writeHead(500, { 'Content-Type': 'text/plain; charset=utf-8' });
      res.end('SFC Compile Error: ' + err.message);
      return;
    }
  }

  // 默认主页: Vue 3 SSR + Tailwind CSS 应用
  if (url.pathname === '/') {
    try {
      const app = createSSRApp(App);
      const appHtml = await app.renderToString();

      res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
      res.end(wrapHtml('Vue 3 + Tailwind CSS SSR - Aluka', appHtml));
      return;
    } catch (err) {
      res.writeHead(500, { 'Content-Type': 'text/plain; charset=utf-8' });
      res.end('SSR Render Error: ' + (err.stack || err.message));
      return;
    }
  }

  res.writeHead(404, { 'Content-Type': 'text/plain' });
  res.end('404 Not Found');
});

server.listen(PORT, () => {
  console.log(`🚀 Vue 3 + Tailwind CSS SSR demo is running on http://127.0.0.1:${PORT}`);
});
