// M4-2 diff：node:net —— 模块表面、isIP/isIPv4/isIPv6、BlockList、
// SocketAddress、TCP echo Server/Socket 事件时序。
const net = require('node:net');
const results = {};

// 1. 模块表面 + isIP 系列。
{
  results.mSurface = [
    typeof net.createServer, typeof net.connect, typeof net.createConnection,
    typeof net.isIP, typeof net.isIPv4, typeof net.isIPv6,
    typeof net.BlockList, typeof net.SocketAddress,
  ].join(',');
  results.isip = [
    net.isIP('1.2.3.4'), net.isIP('::1'), net.isIP('999.1.1.1'), net.isIP(''),
  ].join(',');
  results.isv4 = [net.isIPv4('1.2.3.4'), net.isIPv4('::1'), net.isIPv4('abc')].join(',');
  results.isv6 = [net.isIPv6('::1'), net.isIPv6('1.2.3.4'), net.isIPv6('fe80::1')].join(',');
}

// 2. BlockList。
{
  const bl = new net.BlockList();
  bl.addAddress('1.2.3.4');
  bl.addSubnet('10.0.0.0', 8);
  bl.addRange('192.168.1.1', '192.168.1.10');
  results.blockList = [
    typeof bl.addAddress, typeof bl.removeAddress, typeof bl.addSubnet,
    typeof bl.addRange, typeof bl.removeSubnet, typeof bl.removeRange,
    typeof bl.check, bl.check('1.2.3.4'), bl.check('10.5.5.5'),
    bl.check('192.168.1.5'), bl.check('8.8.8.8'),
  ].join(',');
  results.blockListRules = JSON.stringify(bl.rules);
}

// 3. SocketAddress。
{
  const sa = new net.SocketAddress({ address: '127.0.0.1', port: 80, family: 'IPv4' });
  results.socketAddress = [
    sa.address, sa.port, sa.family, sa.flowlabel,
  ].join(',');
  const sa6 = new net.SocketAddress({ address: '::1', port: 443, family: 'IPv6' });
  results.socketAddress6 = [sa6.address, sa6.port, sa6.family].join(',');
}

// 4. Server 表面 + listening/close 事件。
{
  const server = net.createServer();
  results.serverSurface = [
    typeof server.listen, typeof server.close, typeof server.address,
    typeof server.getConnections, typeof server.ref, typeof server.unref,
    server.listening,
  ].join(',');
  const ev = [];
  server.on('listening', () => ev.push('listening'));
  server.on('close', () => ev.push('close'));
  server.listen(0, '127.0.0.1', () => {
    results.serverAfterListen = [
      server.listening,
      typeof server.address().port,
      server.address().family,
    ].join(',');
    server.close(() => {
      ev.push('closeCb');
      results.serverEvents = ev.join(',');
      results.serverAfterClose = server.listening;
      runSeq5();
    });
  });
}

// 5. echo Server/Socket + 事件时序。
function runSeq5() {
  const server = net.createServer((sock) => {
    sock.on('data', (c) => { sock.write(c); });
    sock.on('end', () => { sock.end(); });
  });
  const serverEv = [];
  server.on('connection', (sock) => {
    serverEv.push('connection:' + (typeof sock.write === 'function'));
  });
  server.listen(0, '127.0.0.1', () => {
    const port = server.address().port;
    const clientEv = [];
    const socket = net.connect(port, '127.0.0.1', () => {
      clientEv.push('connectCb');
      socket.write('ping', () => { clientEv.push('writeCb'); });
    });
    socket.on('connect', () => clientEv.push('connect'));
    socket.on('data', (c) => {
      clientEv.push('data:' + c);
      results.socketAddr = [
        socket.remoteAddress, socket.remotePort === port,
        typeof socket.localPort, typeof socket.address().port,
      ].join(',');
      socket.end();
    });
    socket.on('end', () => clientEv.push('end'));
    socket.on('close', () => {
      clientEv.push('close');
      // 服务端连接在客户端 FIN 后收 end/close。
      setTimeout(() => {
        results.serverEvents2 = serverEv.join(',');
        server.close(() => {
          results.clientEvents = clientEv.join(',');
          process.stdout.write(JSON.stringify(results));
        });
      }, 80);
    });
  });
}
