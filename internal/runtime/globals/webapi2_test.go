package globals

// Phase 3 Web API 测试：crypto.subtle / URLPattern / MessageChannel。

import (
	"testing"
)

// TestWebCryptoSubtle 验证 crypto.subtle.digest（SHA-256 向量）。
func TestWebCryptoSubtle(t *testing.T) {
	ctx := newFetchTestEnv(t)
	err := fetchRun(t, ctx, `
crypto.subtle.digest('SHA-256', Buffer.from('abc')).then(function(d) {
  globalThis.__sha = d.toString('hex');
});
globalThis.__uuid = crypto.randomUUID().length;
globalThis.__rng = crypto.getRandomValues(Buffer.alloc(16)).length;
const typed = new Uint8Array(16);
globalThis.__rngTyped = crypto.getRandomValues(typed) === typed && typed.length === 16;
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := webGlobalGet(ctx, "__sha"); got != want {
		t.Errorf("sha256 = %q, want vector", got)
	}
	if got := webGlobalGet(ctx, "__uuid"); got != "36" {
		t.Errorf("uuid = %q, want 36", got)
	}
	if got := webGlobalGet(ctx, "__rng"); got != "16" {
		t.Errorf("rng = %q, want 16", got)
	}
	if got := webGlobalGet(ctx, "__rngTyped"); got != "true" {
		t.Errorf("typed array rng = %q, want true", got)
	}
}

// TestWebCryptoDigestByteLength 回归测试（P1-3）：digest 结果暴露
// byteLength/length/数字索引（ArrayBuffer 兼容）。
func TestWebCryptoDigestByteLength(t *testing.T) {
	ctx := newFetchTestEnv(t)
	err := fetchRun(t, ctx, `
crypto.subtle.digest('SHA-256', Buffer.from('a')).then(function(d) {
  globalThis.__bl = d.byteLength + '|' + d.length + '|' + (d[0] > 0);
});
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__bl"); got != "32|32|true" {
		t.Errorf("digest byteLength = %q, want 32|32|true", got)
	}
}

// TestURLPattern 验证 URLPattern 匹配与参数提取。
func TestURLPattern(t *testing.T) {
	ctx := newFetchTestEnv(t)
	err := fetchRun(t, ctx, `
var p = new URLPattern('/users/:id');
globalThis.__test = p.test('/users/42') + ':' + p.test('/other');
var m = p.exec('/users/99');
globalThis.__groups = m.pathname.groups.id;
var p2 = new URLPattern({ pathname: '/files/*' });
globalThis.__star = p2.test('/files/a/b/c');
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__test"); got != "true:false" {
		t.Errorf("test = %q, want true:false", got)
	}
	if got := webGlobalGet(ctx, "__groups"); got != "99" {
		t.Errorf("groups = %q, want 99", got)
	}
	if got := webGlobalGet(ctx, "__star"); got != "true" {
		t.Errorf("star = %q, want true", got)
	}
}

// TestMessageChannel 验证 MessageChannel 双向消息。
func TestMessageChannel(t *testing.T) {
	ctx := newFetchTestEnv(t)
	err := fetchRun(t, ctx, `
var mc = new MessageChannel();
var got = [];
mc.port2.onmessage = function(e) { got.push('p2:' + e.data); };
mc.port1.onmessage = function(e) { got.push('p1:' + e.data); };
mc.port1.postMessage('a');
mc.port2.postMessage('b');
setTimeout(function() { globalThis.__msg = got.join(','); }, 50);
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__msg"); got != "p2:a,p1:b" {
		t.Errorf("message = %q, want p2:a,p1:b", got)
	}
}
