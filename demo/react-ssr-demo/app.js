// Aluka React SSR + Tailwind CSS JIT HTTP 服务入口
import http from 'node:http';
import { React } from './src/react.js';
import { renderToString } from './src/server.js';
import { compileTailwind } from './src/tailwind.js';
import { App } from './src/components/App.jsx';

const PORT = 3001;

const server = http.createServer((req, res) => {
  const url = req.url || '/';

  // 1. CSS 端点
  if (url === '/style.css') {
    const vnode = <App />;
    const bodyHtml = renderToString(vnode);
    const css = compileTailwind([bodyHtml]);
    res.writeHead(200, { 'Content-Type': 'text/css; charset=utf-8' });
    res.end(css);
    return;
  }

  // 2. JSON API 端点
  if (url === '/api/render') {
    const vnode = <App />;
    const bodyHtml = renderToString(vnode);
    res.writeHead(200, { 'Content-Type': 'application/json; charset=utf-8' });
    res.end(JSON.stringify({
      status: 'ok',
      engine: 'Aluka Pure Go VM',
      framework: 'React 18 SSR',
      htmlLength: bodyHtml.length,
      timestamp: Date.now()
    }, null, 2));
    return;
  }

  // 3. 根路由 SSR 渲染完整 HTML
  const vnode = <App />;
  const bodyHtml = renderToString(vnode);
  const css = compileTailwind([bodyHtml]);

  const html = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Aluka • React 18 SSR + Tailwind CSS</title>
  <style>
${css}
  </style>
</head>
<body class="bg-slate-950">
  <div id="root">${bodyHtml}</div>
</body>
</html>`;

  res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
  res.end(html);
});

server.listen(PORT, () => {
  console.log(`🚀 React SSR Server running on http://localhost:${PORT}`);
  console.log(`📡 Endpoints:`);
  console.log(`   - GET /           (Full React SSR + Tailwind CSS)`);
  console.log(`   - GET /api/render (SSR JSON Metadata)`);
  console.log(`   - GET /style.css  (Tailwind JIT CSS Bundle)`);
});
