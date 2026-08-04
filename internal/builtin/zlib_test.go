package builtin

// node:zlib 与 node:dns 端到端测试。

import (
	"testing"
)

// TestZlibGzipRoundTrip 验证 gzip/deflate 压缩解压往返（同步）。
func TestZlibGzipRoundTrip(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var zlib = require('node:zlib');
var buf = Buffer.from('hello hello hello hello');
var gz = zlib.gzipSync(buf);
var back = zlib.gunzipSync(gz);
var def = zlib.deflateSync(buf);
var infl = zlib.inflateSync(def);
globalThis.__r = (gz.length > 0) + ':' + back.toString() + ':' +
  (def.length > 0) + ':' + infl.toString() + ':' +
  Buffer.isBuffer(gz);
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "true:hello hello hello hello:true:hello hello hello hello:true" {
		t.Errorf("zlib roundtrip = %q", got)
	}
}

// TestZlibAsync 验证异步回调版压缩。
func TestZlibAsync(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var zlib = require('node:zlib');
var buf = Buffer.from('async payload');
zlib.gzip(buf, function(err, gz) {
  if (err) { globalThis.__r = 'err:' + err; return; }
  zlib.gunzip(gz, function(err2, back) {
    globalThis.__r = (err2 === null) + ':' + back.toString();
  });
});
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "true:async payload" {
		t.Errorf("zlib async = %q", got)
	}
}

// TestZlibBrotli 验证 brotli 压缩解压往返。
func TestZlibBrotli(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var zlib = require('node:zlib');
var data = Buffer.from('brotli round trip data brotli round trip data');
var comp = zlib.brotliCompressSync(data);
var back = zlib.brotliDecompressSync(comp);
globalThis.__r = (comp.length > 0) + ':' + back.toString();
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "true:brotli round trip data brotli round trip data" {
		t.Errorf("brotli = %q", got)
	}
}

// TestDNSLookup 验证 dns.lookup / resolve / promises。
func TestDNSLookup(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var dns = require('node:dns');
dns.lookup('localhost', function(err, address) {
  globalThis.__lookup = (err === null) + ':' + (address.length > 0);
  dns.resolve('localhost', function(err2, addrs) {
    globalThis.__resolve = (err2 === null) + ':' + (addrs.length > 0);
    dns.promises.lookup('localhost').then(function(a) {
      globalThis.__promise = a.length > 0;
    });
  });
});
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__lookup"); got != "true:true" {
		t.Errorf("lookup = %q, want true:true", got)
	}
	if got := env.globalGet("__resolve"); got != "true:true" {
		t.Errorf("resolve = %q, want true:true", got)
	}
	if got := env.globalGet("__promise"); got != "true" {
		t.Errorf("promises.lookup = %q, want true", got)
	}
}
