package builtin

// node:https 端到端测试：自签名证书 TLS 服务器 + 客户端闭环。
// 证书在 Go 侧生成（crypto/x509 + rsa），PEM 字符串注入 JS 模板字符串。

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"testing"
	"time"
)

// genSelfSignedCert 生成自签名证书（返回 key/cert PEM 字符串）。
func genSelfSignedCert(t *testing.T) (string, string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	return keyPEM, certPEM
}

// TestHTTPSRoundTrip 验证 HTTPS 服务器 + 客户端（跳过自签名校验）。
func TestHTTPSRoundTrip(t *testing.T) {
	env := newHTTPEnv(t)
	keyPEM, certPEM := genSelfSignedCert(t)

	code := fmt.Sprintf(`
var https = require('node:https');
var server = https.createServer({
  key: %s,
  cert: %s
}, function(req, res) {
  res.writeHead(200, {'Content-Type': 'text/plain'});
  res.end('secure hello');
});
server.listen(0, function() {
  var port = server.address().port;
  https.get({ host: '127.0.0.1', port: port, path: '/secure', rejectUnauthorized: false }, function(res) {
    var body = '';
    res.on('data', function(c) { body += c; });
    res.on('end', function() {
      globalThis.__status = res.statusCode;
      globalThis.__body = body;
      server.close();
    });
  });
});
`, "`"+keyPEM+"`", "`"+certPEM+"`")

	if err := env.runWithLoop(t, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__status"); got != "200" {
		t.Errorf("status = %q, want 200", got)
	}
	if got := env.globalGet("__body"); got != "secure hello" {
		t.Errorf("body = %q, want secure hello", got)
	}
}
