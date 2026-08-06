package builtin

// node:https 内置模块——HTTPS 服务器与客户端。
//
// 实现要点：
//   - 服务器复用 node:http 的 Server（newHTTPServerWithTLS），从 options
//     {key, cert}（PEM 字符串）构造 tls.Config，listen 时用 tls.NewListener。
//   - 客户端 request/get 直接复用 node:http 的 newClientRequest——Go
//     net/http.Client 对 https:// URL 自动完成 TLS 握手。
//   - 证书校验在测试/本地场景常用自签名，客户端需跳过校验：
//     https.request 默认透传（简化），可通过选项处理。

import (
	"crypto/tls"
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewHTTPS 构造 node:https 模块的导出对象。
func NewHTTPS(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// https.Agent + globalAgent（复用 http 的 Agent 实现）。
	registerHttpAgent(m)

	// https.createServer([options][, handler])：options 需含 {key, cert}。
	_ = m.Set("createServer", engine.NewFunction("createServer", func(args []engine.Value) (engine.Value, error) {
		var handler engine.Value
		var options engine.Value
		for _, a := range args {
			if a.IsFunction() {
				handler = a
			} else if a.IsObject() {
				options = a
			}
		}
		keyPEM, certPEM := "", ""
		if o, ok := options.AsObject(); ok {
			if v, err := o.Get("key"); err == nil {
				keyPEM = v.String()
			}
			if v, err := o.Get("cert"); err == nil {
				certPEM = v.String()
			}
		}
		if keyPEM == "" || certPEM == "" {
			return engine.Undefined(), fmt.Errorf("https: createServer requires { key, cert } PEM options")
		}
		tlsCfg, err := buildTLSConfig(keyPEM, certPEM)
		if err != nil {
			return engine.Undefined(), fmt.Errorf("https: invalid key/cert: %w", err)
		}
		return newHTTPServerWithTLS(ctx, handler, tlsCfg), nil
	}))

	// https.request(options, callback) / https.get：复用 node:http 客户端
	// （协议前缀 https://，Go net/http 自动完成 TLS 握手）。
	_ = m.Set("request", engine.NewFunction("request", func(args []engine.Value) (engine.Value, error) {
		return newClientRequestProto(ctx, args, "https"), nil
	}))
	_ = m.Set("get", engine.NewFunction("get", func(args []engine.Value) (engine.Value, error) {
		req := newClientRequestProto(ctx, args, "https")
		if o, ok := req.AsObject(); ok {
			if endFn, err := o.Get("end"); err == nil && endFn.IsFunction() {
				if f, ok := endFn.AsFunction(); ok {
					_, _ = f.Call(nil)
				}
			}
		}
		return req, nil
	}))

	_ = m.Set("STATUS_CODES", httpStatusCodes())
	return m, nil
}

// buildTLSConfig 从 PEM 字符串构造 TLS 服务器配置。
func buildTLSConfig(keyPEM, certPEM string) (*tls.Config, error) {
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"http/1.1"},
	}, nil
}
