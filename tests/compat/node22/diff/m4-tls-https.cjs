// M4-4/M4-6 diff：node:tls 与 node:https —— 模块表面、自签名证书 HTTPS echo、
// TLS echo、Agent 表面。
const fs = require('node:fs');
const path = require('node:path');
const https = require('node:https');
const tls = require('node:tls');

const key = fs.readFileSync(path.join(__dirname, 'm4-fixtures', 'test_key.pem'), 'utf8');
const cert = fs.readFileSync(path.join(__dirname, 'm4-fixtures', 'test_cert.pem'), 'utf8');

const results = {};

// 1. 模块表面。
results.surface = [
  typeof https.createServer, typeof https.request, typeof https.get,
  typeof https.Agent, typeof https.globalAgent, typeof https.Server,
  typeof https.STATUS_CODES,
  typeof tls.createServer, typeof tls.connect, typeof tls.TLSSocket,
  typeof tls.createSecureContext, typeof tls.checkServerIdentity,
  typeof tls.getCiphers,
].join(',');
results.ciphers = Array.isArray(tls.getCiphers()) && tls.getCiphers().length > 0;
results.secureContext = (() => {
  const sc = tls.createSecureContext({ key, cert });
  return typeof sc === 'object' && sc !== null;
})();
results.agent = [
  typeof https.globalAgent, https.globalAgent.keepAlive,
  typeof new https.Agent().destroy,
].join(',');

// 2. HTTPS echo。
const server = https.createServer({ key, cert }, (req, res) => {
  res.statusCode = 201;
  res.end('https-ok');
});
server.listen(0, '127.0.0.1', () => {
  const port = server.address().port;
  https.get({ host: '127.0.0.1', port, path: '/x', rejectUnauthorized: false }, (res) => {
    let body = '';
    res.on('data', (c) => { body += c; });
    res.on('end', () => {
      results.https = [res.statusCode, body, res.httpVersion].join(':');
      server.close(runTLS);
    });
  });
});

// 3. TLS echo（socket 级）。
function runTLS() {
  const tserver = tls.createServer({ key, cert }, (sock) => {
    sock.on('data', (c) => { sock.write(c); });
    sock.on('end', () => { sock.end(); });
  });
  const ev = [];
  tserver.on('secureConnection', () => ev.push('secureConnection'));
  tserver.on('listening', () => ev.push('listening'));
  tserver.listen(0, '127.0.0.1', () => {
    const tport = tserver.address().port;
    const tclient = tls.connect({ host: '127.0.0.1', port: tport, rejectUnauthorized: false }, () => {
      tclient.write('tls-ping');
    });
    let got = '';
    tclient.on('data', (c) => { got += c; tclient.end(); });
    tclient.on('error', (e) => { results.tlsErr = e.message; tserver.close(finish); });
    tclient.on('end', () => {
      setTimeout(() => {
        results.tls = [got, typeof tclient.getProtocol].join(':');
        results.tlsServerEvents = ev.join(',');
        tserver.close(finish);
      }, 50);
    });
  });
}

function finish() {
  process.stdout.write(JSON.stringify(results));
}
