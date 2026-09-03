// M4-3 diff：node:dgram —— UDP4 echo、message/rinfo、bind/send/close、
// address、connect/disconnect 表面。
const dgram = require('node:dgram');
const results = {};

// 1. 模块表面。
results.surface = [
  typeof dgram.createSocket, typeof dgram.Socket,
].join(',');

// 2. UDP4 echo（本机 loopback）。
const server = dgram.createSocket('udp4');
const client = dgram.createSocket('udp4');
const serverMsgs = [];
const clientMsgs = [];

server.on('message', (msg, rinfo) => {
  serverMsgs.push({
    text: msg.toString(),
    addr: rinfo.address,
    family: rinfo.family,
    hasPort: typeof rinfo.port === 'number',
    size: rinfo.size,
  });
  // echo 回发。
  server.send(msg, rinfo.port, rinfo.address, (err) => {
    serverMsgs[serverMsgs.length - 1].sendCb = err === null;
  });
});

server.on('listening', () => {
  const sAddr = server.address();
  results.serverAddress = [sAddr.address, sAddr.family, typeof sAddr.port].join(',');
  const port = sAddr.port;
  client.send('ping', port, '127.0.0.1', (err) => {
    results.sendCb = err === null;
    results.sendRet = null; // send 返回值不比较
  });
});

client.on('message', (msg, rinfo) => {
  clientMsgs.push({
    text: msg.toString(),
    addr: rinfo.address,
    hasPort: typeof rinfo.port === 'number',
  });
  client.close();
  server.close(() => {
    results.serverMessages = JSON.stringify(serverMsgs);
    results.clientMessages = JSON.stringify(clientMsgs);
    // 3. connect/disconnect + 错误面。
    results.sockSurface = [
      typeof server.bind, typeof server.send, typeof server.close,
      typeof server.address, typeof server.connect, typeof server.disconnect,
      typeof server.addMembership, typeof server.dropMembership,
      typeof server.setBroadcast, typeof server.setTTL, typeof server.ref,
      typeof server.unref,
    ].join(',');
    results.errors = [
      dgram.ERR_SOCKET_ALREADY_BOUND !== undefined,
      typeof dgram.createSocket('udp6'),
    ].join(',');
    process.stdout.write(JSON.stringify(results));
  });
});

server.bind(0, '127.0.0.1');
