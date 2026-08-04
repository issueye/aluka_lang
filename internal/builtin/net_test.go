package builtin

// node:net 端到端测试：TCP echo 服务器 + 客户端完整闭环。
// 复用 http_test.go 的 httpTestEnv（VM + 定时器 + loader 注入 require）。

import (
	"testing"
)

// TestNetTCPEcho 验证 TCP 服务器 + 客户端收发。
func TestNetTCPEcho(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var net = require('node:net');
var server = net.createServer(function(sock) {
  sock.on('data', function(d) {
    sock.write('echo:' + d);
  });
});
server.listen(0, function() {
  var port = server.address().port;
  globalThis.__port = port;
  var client = net.connect({ host: '127.0.0.1', port: port }, function() {
    client.write('ping');
  });
  client.on('data', function(d) {
    globalThis.__echo = d;
    client.end();
    server.close(function() { globalThis.__closed = true; });
  });
});
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	port := env.globalGet("__port")
	if port == "undefined" || port == "0" {
		t.Errorf("port = %q, want real port", port)
	}
	if got := env.globalGet("__echo"); got != "echo:ping" {
		t.Errorf("echo = %q, want echo:ping", got)
	}
	if got := env.globalGet("__closed"); got != "true" {
		t.Errorf("closed = %q, want true", got)
	}
}

// TestNetMultipleMessages 验证连续多次收发。
func TestNetMultipleMessages(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var net = require('node:net');
var server = net.createServer(function(sock) {
  sock.on('data', function(d) {
    sock.write(d.toUpperCase());
  });
});
server.listen(0, function() {
  var port = server.address().port;
  var client = net.connect({ port: port }, function() {
    client.write('a');
  });
  var out = '';
  var count = 0;
  client.on('data', function(d) {
    out += d;
    count++;
    if (count >= 2) {
      globalThis.__out = out;
      client.destroy();
      server.close();
    } else {
      client.write('b');
    }
  });
});
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__out"); got != "AB" {
		t.Errorf("multi = %q, want AB", got)
	}
}
