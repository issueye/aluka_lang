// M4-5 diff：node:http —— Server/ClientRequest/ServerResponse/Agent 基础、
// headers 方法、timeout、abort、trailers、upgrade。
const http = require('node:http');
const results = {};

// 1. 模块与 Server 表面。
{
  results.mSurface = [
    typeof http.createServer, typeof http.request, typeof http.get,
    typeof http.Agent, typeof http.IncomingMessage, typeof http.ServerResponse,
    typeof http.globalAgent, typeof http.METHODS, typeof http.STATUS_CODES,
  ].join(',');
  const server = http.createServer();
  results.serverSurface = [
    typeof server.listen, typeof server.close, typeof server.address,
    typeof server.setTimeout, typeof server.closeAllConnections,
    typeof server.closeIdleConnections, typeof server.getConnections,
    server.listening, server.timeout, server.keepAliveTimeout,
    server.maxHeadersCount, server.headersTimeout, server.requestTimeout,
    server.maxRequestsPerSocket,
  ].join(',');
  server.close();
}

// 2. ServerResponse 方法面。
{
  const server = http.createServer((req, res) => {
    results.resSurface = [
      typeof res.writeHead, typeof res.write, typeof res.end,
      typeof res.setHeader, typeof res.getHeader, typeof res.getHeaders,
      typeof res.removeHeader, typeof res.hasHeader, typeof res.setTimeout,
      typeof res.addTrailers, typeof res.flushHeaders, typeof res.writeContinue,
      typeof res.cork, typeof res.uncork, res.writableEnded,
    ].join(',');
    res.setHeader('X-Multi', ['a', 'b']);
    results.resHeaders = JSON.stringify(res.getHeaders());
    res.statusCode = 202;
    res.setHeader('X-Before', 'v1');
    res.end('res-body');
    res.on('finish', () => {
      results.resFinished = [res.writableEnded, res.statusCode].join(',');
      server.close(runSeq2);
    });
  });
  server.listen(0, '127.0.0.1', () => {
    const port = server.address().port;
    http.get({ host: '127.0.0.1', port, path: '/r' }, (res) => {
      let body = '';
      res.on('data', (c) => { body += c; });
      res.on('end', () => {
        results.get1 = [res.statusCode, body].join(':');
      });
    });
  });
}
let closeCount = 0;

// 3. GET 请求头回显 + POST body。
function runSeq2() {
  const server = http.createServer((req, res) => {
    const info = {
      method: req.method,
      url: req.url,
      headers: { ct: req.headers['content-type'], xh: req.headers['x-hdr'], cl: req.headers['content-length'] },
    };
    let body = '';
    req.on('data', (c) => { body += c; });
    req.on('end', () => {
      res.writeHead(200, { 'Content-Type': 'text/plain' });
      res.end(JSON.stringify(info) + '|body=' + body);
    });
  });
  server.listen(0, '127.0.0.1', () => {
    const port = server.address().port;
    const results2 = {};
    let done = 0;
    const after = () => { if (++done === 2) { results.echo = results2; server.close(runSeq3); } };
    // GET
    http.get({ host: '127.0.0.1', port, path: '/p?a=1', headers: { 'X-Hdr': 'xyz' } }, (res) => {
      let b = '';
      res.on('data', (c) => { b += c; });
      res.on('end', () => { results2.get = b; after(); });
    });
    // POST
    const req = http.request({ host: '127.0.0.1', port, path: '/post', method: 'POST', headers: { 'Content-Type': 'application/json' } }, (res) => {
      let b = '';
      res.on('data', (c) => { b += c; });
      res.on('end', () => { results2.post = b; after(); });
    });
    req.write('{"k":1}');
    req.end();
  });
}

// 4. ClientRequest 方法面 + req.setTimeout。
function runSeq3() {
  const server = http.createServer((req, res) => { res.end('ok'); });
  server.listen(0, '127.0.0.1', () => {
    const port = server.address().port;
    const req = http.request({ host: '127.0.0.1', port, path: '/' }, (res) => {
      res.resume();
      res.on('end', () => {
        results.clientSurface = [
          typeof req.setHeader, typeof req.getHeader, typeof req.getHeaders,
          typeof req.removeHeader, typeof req.hasHeader, typeof req.flushHeaders,
          typeof req.setTimeout, typeof req.abort, typeof req.setNoDelay,
          typeof req.setSocketKeepAlive, typeof req.destroy,
        ].join(',');
        // req.setTimeout
        const req2 = http.request({ host: '127.0.0.1', port, path: '/' }, () => {});
        const to = [];
        req2.setTimeout(30, () => { to.push('cb'); });
        req2.on('timeout', () => { to.push('ev'); });
        req2.end();
        setTimeout(() => {
          results.reqTimeout = to.join(',');
          server.close(runSeq4);
        }, 120);
      });
    });
    req.end();
  });
}

// 5. req.abort：慢服务器 + 客户端 abort。
function runSeq4() {
  const server = http.createServer((req, res) => { setTimeout(() => res.end('late'), 500); });
  server.listen(0, '127.0.0.1', () => {
    const port = server.address().port;
    const req = http.request({ host: '127.0.0.1', port, path: '/' }, () => {});
    const ev = [];
    req.on('abort', () => { ev.push('abort'); });
    req.on('error', (e) => { ev.push('error:' + e.code); });
    req.on('close', () => { ev.push('close'); });
    req.end();
    setTimeout(() => {
      req.abort();
      setTimeout(() => {
        results.reqAbort = ev.join(',');
        server.close(runSeq5);
      }, 100);
    }, 60);
  });
}

// 6. Trailers（chunked 响应）。
function runSeq5() {
  const server = http.createServer((req, res) => {
    res.write('c1,');
    res.write('c2,');
    res.addTrailers({ 'X-Trail': 'tval' });
    res.end('end');
  });
  server.listen(0, '127.0.0.1', () => {
    const port = server.address().port;
    http.get({ host: '127.0.0.1', port, path: '/' }, (res) => {
      let b = '';
      res.on('data', (c) => { b += c; });
      res.on('end', () => {
        results.trailers = JSON.stringify(res.trailers);
        results.trailerBody = b;
        server.close(runSeq6);
      });
    });
  });
}

// 7. upgrade 事件。
function runSeq6() {
  const server = http.createServer((req, res) => { res.end('normal'); });
  server.on('upgrade', (req, socket, head) => {
    socket.write('HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: myproto\r\n\r\nup-body');
    setTimeout(() => { socket.end(); }, 40);
  });
  server.listen(0, '127.0.0.1', () => {
    const port = server.address().port;
    http.get({ host: '127.0.0.1', port, path: '/n' }, (res) => {
      res.resume();
      res.on('end', () => {
        const net = require('node:net');
        const sock = net.connect(port, '127.0.0.1', () => {
          sock.write('GET /u HTTP/1.1\r\nHost: x\r\nConnection: Upgrade\r\nUpgrade: myproto\r\n\r\n');
        });
        let data = '';
        sock.on('data', (c) => { data += c; });
        sock.on('end', () => {
          results.upgrade = [
            data.includes('101 Switching') ? '101' : 'no101',
            data.includes('up-body') ? 'got' : 'no',
          ].join(':');
          server.close(runSeq7);
        });
      });
    });
  });
}

// 8. Agent 表面 + keepAlive 请求复用（连续两个请求）。
function runSeq7() {
  const a1 = new http.Agent();
  results.agentSurface = [
    a1.keepAlive, a1.keepAliveMsecs, a1.maxSockets, a1.maxFreeSockets,
    typeof a1.destroy, typeof a1.getName, typeof a1.createConnection,
  ].join(',');
  const server = http.createServer((req, res) => { res.end('ka'); });
  server.listen(0, '127.0.0.1', () => {
    const port = server.address().port;
    const agent = new http.Agent({ keepAlive: true, maxSockets: 2 });
    const doGet = (n, cb) => {
      http.get({ host: '127.0.0.1', port, path: '/' + n, agent }, (res) => {
        res.resume();
        res.on('end', () => cb(res.statusCode));
      });
    };
    doGet('1', (s1) => {
      doGet('2', (s2) => {
        results.agentReq = s1 + ',' + s2;
        agent.destroy();
        server.close(finish);
      });
    });
  });
}

function finish() {
  process.stdout.write(JSON.stringify(results));
}
