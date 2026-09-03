import fs from 'node:fs';
import path from 'node:path';
import { renderDemoPage } from './ssr.ts';

const styles = fs.readFileSync(path.join(import.meta.dir, 'src', 'styles.css'), 'utf8');

const server = Aluka.serve({
  port: Number(process.env.PORT || 3040),
  fetch: async (req) => {
    const url = String(req.url || '/').split('?')[0];
    if (url === '/styles.css') {
      return new Response(styles, { headers: { 'content-type': 'text/css; charset=utf-8' } });
    }
    if (url === '/health') {
      return new Response('ok');
    }
    const page = await renderDemoPage();
    return new Response(page.html, { headers: { 'content-type': 'text/html; charset=utf-8' } });
  },
});

console.log('vue3-run-build-demo listening on ' + server.url);
