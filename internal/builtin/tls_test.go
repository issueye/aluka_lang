package builtin

// node:tls 端到端测试：TLS 服务器 + 客户端（自签名证书）往返。

import (
	"fmt"
	"testing"
)

// TestTLSServerClient 验证 TLS 服务器 + 客户端收发。
func TestTLSServerClient(t *testing.T) {
	env := newHTTPEnv(t)
	keyPEM, certPEM := genSelfSignedCert(t)

	code := fmt.Sprintf(`
var tls = require('node:tls');
var server = tls.createServer({ key: %s, cert: %s }, function(sock) {
  sock.on('data', function(d) { sock.write('tls:' + d); });
});
server.listen(0, function() {
  var port = server.address().port;
  var client = tls.connect({ host: '127.0.0.1', port: port, rejectUnauthorized: false }, function() {
    client.write('hello');
  });
  client.on('data', function(d) {
    globalThis.__echo = d;
    client.end();
    server.close(function() { globalThis.__closed = true; });
  });
});
`, "`"+keyPEM+"`", "`"+certPEM+"`")

	if err := env.runWithLoop(t, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__echo"); got != "tls:hello" {
		t.Errorf("echo = %q, want tls:hello", got)
	}
	if got := env.globalGet("__closed"); got != "true" {
		t.Errorf("closed = %q, want true", got)
	}
}
