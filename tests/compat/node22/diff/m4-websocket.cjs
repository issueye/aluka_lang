// M4-8 diff：全局 WebSocket 客户端 —— 表面 + 握手/message/close 基础。
// 用原始 net 服务端做最小 WebSocket 握手与帧回显（避免外网依赖）。
const net = require('node:net');
const crypto = require('node:crypto');

const results = {};

// 1. 表面。
{
  const ws = new WebSocket('ws://127.0.0.1:1/x'); // 仅取属性（连接会失败）
  results.surface = [
    typeof WebSocket,
    ws.CONNECTING, ws.OPEN, ws.CLOSING, ws.CLOSED,
    typeof ws.send, typeof ws.close, ws.readyState, typeof ws.url,
  ].join(',');
}

// --- 最小 WS 服务端（握手 + 文本帧回显 + close 应答） ---
const GUID = '258EAFA5-E914-47DA-95CA-C5AB0DC85B11';
function wsAccept(key) {
  return crypto.createHash('sha1').update(key + GUID).digest('base64');
}
function sendTextFrame(sock, payload) {
  const b = Buffer.from(payload, 'utf8');
  let header;
  if (b.length < 126) {
    header = Buffer.from([0x81, b.length]);
  } else {
    header = Buffer.from([0x81, 126, (b.length >> 8) & 0xff, b.length & 0xff]);
  }
  sock.write(Buffer.concat([header, b]));
}
function parseClientFrame(buf) {
  if (buf.length < 2) return null;
  const opcode = buf[0] & 0x0f;
  let len = buf[1] & 0x7f;
  let off = 2;
  if (len === 126) {
    if (buf.length < 4) return null;
    len = buf.readUInt16BE(2);
    off = 4;
  }
  const masked = (buf[1] & 0x80) !== 0;
  let mask = [0, 0, 0, 0];
  if (masked) {
    if (buf.length < off + 4) return null;
    mask = buf.slice(off, off + 4);
    off += 4;
  }
  if (buf.length < off + len) return null;
  const payload = Buffer.alloc(len);
  for (let i = 0; i < len; i++) payload[i] = buf[off + i] ^ mask[i % 4];
  return { opcode, payload: payload.toString('utf8'), consumed: off + len };
}

const server = net.createServer((sock) => {
  let buf = Buffer.alloc(0);
  let handshook = false;
  sock.on('data', (chunk) => {
    buf = Buffer.concat([buf, chunk]);
    if (!handshook) {
      const idx = buf.indexOf('\r\n\r\n');
      if (idx === -1) return;
      const header = buf.slice(0, idx).toString('utf8');
      const m = header.match(/Sec-WebSocket-Key:\s*(.+)/i);
      buf = buf.slice(idx + 4);
      handshook = true;
      sock.write('HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ' + wsAccept(m[1].trim()) + '\r\n\r\n');
      sendTextFrame(sock, 'welcome');
      return;
    }
    while (buf.length > 0) {
      const fr = parseClientFrame(buf);
      if (!fr) break;
      buf = buf.slice(fr.consumed);
      if (fr.opcode === 0x1) { // text
        sendTextFrame(sock, 'echo:' + fr.payload);
      } else if (fr.opcode === 0x8) { // close
        sock.write(Buffer.from([0x88, 0]));
        sock.end();
      }
    }
  });
});

// 2. 客户端连接流程。
const ev = [];
const ws = new WebSocket('ws://127.0.0.1:' + 1 + '/'); // 端口在 listen 后更新
server.listen(0, '127.0.0.1', () => {
  const port = server.address().port;
  const client = new WebSocket('ws://127.0.0.1:' + port + '/');
  client.onopen = () => { ev.push('open'); client.send('ping'); };
  client.onmessage = (e) => {
    ev.push('message:' + e.data);
    if (e.data === 'echo:ping') {
      setTimeout(() => { client.close(); }, 30);
    }
  };
  client.onerror = () => { ev.push('error'); };
  client.onclose = () => {
    ev.push('close:' + client.readyState);
    setTimeout(() => {
      results.events = ev.join(',');
      results.binaryType = typeof client.binaryType;
      server.close(() => {
        process.stdout.write(JSON.stringify(results));
      });
    }, 30);
  };
});
