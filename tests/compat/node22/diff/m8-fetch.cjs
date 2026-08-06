// M8-1 diff：fetch / Headers / Request / Response / FormData（本地 http 服务）。
const http = require('node:http');
const r = {};

// --- Headers ---
const h = new Headers({ 'Content-Type': 'text/plain', 'X-A': '1' });
h.append('X-A', '2');
h.set('X-B', 'b');
r.hGet = h.get('content-type');
r.hGetMulti = h.get('x-a');
r.hGetMissing = h.get('nope');
r.hHas = h.has('x-b');
r.hSize = (() => { let n = 0; h.forEach(() => n++); return n; })();
r.hKeys = Array.from(h.keys()).join(',');
r.hValues = Array.from(h.values()).join(',');
r.hEntries = Array.from(h.entries()).map(([k, v]) => k + ':' + v).join('|');
r.hDelete = (() => { const x = new Headers({ A: '1', B: '2' }); x.delete('a'); return x.get('A') + '|' + x.get('B'); })();
r.hSetReplace = (() => { const x = new Headers({ A: '1' }); x.append('A', '2'); x.set('a', '3'); return x.get('A'); })();
r.hFromString = new Headers('a: 1\r\nb: 2').get('B');
r.hIterable = (() => { const x = new Headers({ Z: '1', A: '2' }); return [...x].map(([k, v]) => k + ':' + v).join('|'); })();
r.hSetCookie = (() => { const x = new Headers({ 'Set-Cookie': 'a=1' }); x.append('Set-Cookie', 'b=2'); return JSON.stringify(x.getSetCookie()); })();

// --- Response ---
const res = new Response('{"ok":true}', { status: 201, statusText: 'Created', headers: { 'X-H': 'v' } });
r.resStatus = res.status;
r.resStatusText = res.statusText;
r.resOk = res.ok;
r.resUrl = res.url;
r.resHeaders = res.headers.get('X-H');
r.resBodyUsedBefore = res.bodyUsed;
const resRedirect = Response.redirect('https://x/y', 301);
r.resRedirect = resRedirect.status + '|' + resRedirect.headers.get('Location');
r.resRedirectDefault = Response.redirect('https://x/y').status;
const resError = Response.error();
r.resError = resError.status + '|' + resError.ok;
r.resNoBody = new Response().status;
r.resNullBody = new Response(null).body;

// --- Request ---
const req = new Request('https://example.com/api', { method: 'POST', headers: { 'X-R': '1' } });
r.reqUrl = req.url;
r.reqMethod = req.method;
r.reqHeader = req.headers.get('X-R');
r.reqBodyNull = req.body === null;
const req2 = new Request('https://example.com/api', { method: 'POST', body: 'abc' });
r.reqBody = req2.bodyUsed + '|' + (req2.body !== null);

// --- FormData ---
const fd = new FormData();
fd.append('k', 'v1');
fd.append('k', 'v2');
fd.append('n', '1');
r.fdGet = fd.get('k');
r.fdGetAll = fd.getAll('k').join(',');
r.fdHas = fd.has('n');
r.fdEntries = [...fd.entries()].map(([k, v]) => k + ':' + v).join('|');
r.fdSet = (() => { const f = new FormData(); f.append('a', '1'); f.set('a', '2'); return f.get('a'); })();
r.fdDelete = (() => { const f = new FormData(); f.append('a', '1'); f.delete('a'); return f.has('a'); })();
r.fdIterable = (() => { const f = new FormData(); f.append('x', '1'); f.append('y', '2'); return [...f].map(([k, v]) => k + ':' + v).join('|'); })();
r.fdKeys = (() => { const f = new FormData(); f.append('a', '1'); f.append('b', '2'); return [...f.keys()].join(','); })();

// --- fetch 本地服务 ---
const server = http.createServer((inReq, inRes) => {
  if (inReq.url === '/json') {
    inRes.setHeader('Content-Type', 'application/json');
    inRes.end(JSON.stringify({ hello: 'world', n: 7 }));
  } else if (inReq.url === '/echo') {
    let body = '';
    inReq.on('data', (c) => { body += c; });
    inReq.on('end', () => {
      inRes.setHeader('X-Method', inReq.method);
      inRes.end(body || 'empty');
    });
  } else if (inReq.url === '/status') {
    inRes.statusCode = 404;
    inRes.end('not found');
  } else {
    inRes.end('root');
  }
});
server.listen(0, '127.0.0.1', async () => {
  const port = server.address().port;
  const base = 'http://127.0.0.1:' + port;
  try {
    const jr = await fetch(base + '/json');
    r.fetchStatus = jr.status;
    r.fetchOk = jr.ok;
    r.fetchCT = jr.headers.get('content-type');
    const j = await jr.json();
    r.fetchJson = j.hello + ':' + j.n;
    r.fetchBodyUsed = jr.bodyUsed;

    const tr = await fetch(base + '/echo', { method: 'POST', body: 'hello body' });
    r.fetchEcho = await tr.text();
    r.fetchEchoMethod = tr.headers.get('X-Method');

    const sr = await fetch(base + '/status');
    r.fetch404 = sr.status + '|' + sr.ok;

    // Response 方法（Promise）
    const rr = new Response('data');
    const rj = new Response('{"a":1}');
    r.pText = await rr.text();
    r.pJson = (await rj.json()).a;
    r.pArrayBufferLen = (await new Response('abcd').arrayBuffer()).byteLength;

    // 异步读 Response.body stream
    const br = await fetch(base + '/json');
    const reader = br.body.getReader();
    const first = await reader.read();
    r.bodyStream = first.done + '|' + (typeof first.value);
  } catch (err) {
    r.fetchError = String(err).slice(0, 80);
  } finally {
    server.close();
    const sorted = {};
    Object.keys(r).sort().forEach((k) => { sorted[k] = r[k]; });
    console.log(JSON.stringify(sorted));
  }
});
