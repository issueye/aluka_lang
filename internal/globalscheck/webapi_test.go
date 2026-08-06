package globals

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// newWebAPITestContext 创建带 URL/Abort/Event 全局的 VM 测试上下文。
func newWebAPITestContext(t *testing.T) engine.Context {
	t.Helper()
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	t.Cleanup(func() { ctx.Close() })
	if err := NewURL(ctx, URLConfig{}); err != nil {
		t.Fatalf("NewURL: %v", err)
	}
	if err := NewAbort(ctx, AbortConfig{}); err != nil {
		t.Fatalf("NewAbort: %v", err)
	}
	if err := NewEvent(ctx, EventConfig{}); err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	_ = ctx.Global().Set("globalThis", ctx.Global())
	return ctx
}

// webGet 执行代码并读取全局键字符串值。
func webGet(t *testing.T, ctx engine.Context, code, key string) string {
	t.Helper()
	if _, err := ctx.Eval(code, "webapi_test.js"); err != nil {
		t.Fatalf("Eval: %v", err)
	}
	v, _ := ctx.Global().Get(key)
	return v.String()
}

// TestURLProperties 验证 WHATWG URL 属性。
func TestURLProperties(t *testing.T) {
	ctx := newWebAPITestContext(t)
	got := webGet(t, ctx, `
var u = new URL('http://user:pass@example.com:8080/path/to?q=1&r=2#frag');
globalThis.__r = u.protocol + '|' + u.hostname + '|' + u.port + '|' + u.host + '|' +
  u.pathname + '|' + u.search + '|' + u.hash + '|' + u.origin + '|' + u.username;
`, "__r")
	if got != "http:|example.com|8080|example.com:8080|/path/to|?q=1&r=2|#frag|http://example.com:8080|user" {
		t.Errorf("URL props = %q", got)
	}
}

func TestDOMExceptionAndSubclass(t *testing.T) {
	ctx := newWebAPITestContext(t)
	if err := NewDOMException(ctx, DOMExceptionConfig{}); err != nil {
		t.Fatalf("NewDOMException: %v", err)
	}
	got := webGet(t, ctx, `
class CustomDOMException extends DOMException {
  get reason() { return 'custom'; }
}
var direct = new DOMException('stopped', 'AbortError');
var custom = new CustomDOMException('bad', 'NetworkError');
globalThis.__r = direct.name + '|' + direct.message + '|' + direct.code + '|' +
  (direct instanceof Error) + '|' + custom.reason + '|' + (custom instanceof DOMException);
`, "__r")
	if got != "AbortError|stopped|20|true|custom|true" {
		t.Errorf("DOMException = %q", got)
	}
}

// TestURLRelativeBase 验证相对解析。
func TestURLRelativeBase(t *testing.T) {
	ctx := newWebAPITestContext(t)
	got := webGet(t, ctx, `
var u = new URL('/foo/bar', 'http://example.com/base/');
globalThis.__r = u.href;
`, "__r")
	if got != "http://example.com/foo/bar" {
		t.Errorf("relative = %q, want http://example.com/foo/bar", got)
	}
}

// TestURLSearchParams 验证 URLSearchParams 增删改查。
func TestURLSearchParams(t *testing.T) {
	ctx := newWebAPITestContext(t)
	got := webGet(t, ctx, `
var sp = new URLSearchParams('a=1&b=2&a=3');
sp.append('c', '4');
globalThis.__r = sp.get('a') + '|' + sp.getAll('a').length + '|' + sp.has('b') + '|' +
  sp.size + '|' + sp.toString();
`, "__r")
	if got != "1|2|true|4|a=1&a=3&b=2&c=4" {
		t.Errorf("URLSearchParams = %q", got)
	}
	got = webGet(t, ctx, `
var sp = new URLSearchParams();
sp.set('x', '1');
sp.set('x', '2');
sp.delete('y');
globalThis.__r = sp.get('x') + '|' + sp.size;
`, "__r")
	if got != "2|1" {
		t.Errorf("URLSearchParams set/delete = %q", got)
	}
	got = webGet(t, ctx, `
var sp = new URLSearchParams('b=2&a=1&b=3');
sp.sort();
globalThis.__r = sp.toString();
`, "__r")
	if got != "a=1&b=2&b=3" {
		t.Errorf("URLSearchParams sort = %q", got)
	}
}

// TestURLSearchParamsBind 验证 searchParams 修改同步 URL。
func TestURLSearchParamsBind(t *testing.T) {
	ctx := newWebAPITestContext(t)
	got := webGet(t, ctx, `
var u = new URL('http://example.com/p?old=1');
u.searchParams.set('new', '2');
globalThis.__r = u.search + '|' + u.href;
`, "__r")
	if got != "?old=1&new=2|http://example.com/p?old=1&new=2" {
		t.Errorf("bind = %q", got)
	}
}

// TestAbortController 验证中断信号。
func TestAbortController(t *testing.T) {
	ctx := newWebAPITestContext(t)
	got := webGet(t, ctx, `
var ctrl = new AbortController();
var sig = ctrl.signal;
var fired = false;
sig.addEventListener('abort', function() { fired = true; });
ctrl.abort('my-reason');
globalThis.__r = sig.aborted + '|' + fired + '|' + sig.reason;
`, "__r")
	if got != "true|true|my-reason" {
		t.Errorf("abort = %q", got)
	}
	// onabort 属性 + 多次 abort 不重复触发。
	got = webGet(t, ctx, `
var ctrl = new AbortController();
var n = 0;
ctrl.signal.onabort = function() { n++; };
ctrl.abort();
ctrl.abort();
globalThis.__r = n;
`, "__r")
	if got != "1" {
		t.Errorf("onabort count = %q, want 1", got)
	}
}

// TestEventTarget 验证事件目标。
func TestEventTarget(t *testing.T) {
	ctx := newWebAPITestContext(t)
	got := webGet(t, ctx, `
var et = new EventTarget();
var out = [];
et.addEventListener('ping', function(e) { out.push(e.type); });
et.addEventListener('ping', function() { out.push('second'); }, { once: true });
et.dispatchEvent(new Event('ping'));
et.dispatchEvent(new Event('ping'));
globalThis.__r = out.join(',');
`, "__r")
	if got != "ping,second,ping" {
		t.Errorf("event target = %q, want ping,second,ping", got)
	}
	// handleEvent 对象 + removeEventListener。
	got = webGet(t, ctx, `
var et = new EventTarget();
var hits = 0;
var handler = { handleEvent: function() { hits++; } };
et.addEventListener('go', handler);
et.dispatchEvent(new Event('go'));
et.removeEventListener('go', handler);
et.dispatchEvent(new Event('go'));
globalThis.__r = hits;
`, "__r")
	if got != "1" {
		t.Errorf("handleEvent = %q, want 1", got)
	}
}

// TestEventPreventDefault 验证 preventDefault。
func TestEventPreventDefault(t *testing.T) {
	ctx := newWebAPITestContext(t)
	got := webGet(t, ctx, `
var et = new EventTarget();
et.addEventListener('cancel', function(e) { e.preventDefault(); });
var ev = new Event('cancel', { cancelable: true });
var result = et.dispatchEvent(ev);
globalThis.__r = result + '|' + ev.defaultPrevented;
`, "__r")
	if got != "false|true" {
		t.Errorf("preventDefault = %q", got)
	}
}

// TestCustomEvent 验证自定义事件 detail。
func TestCustomEvent(t *testing.T) {
	ctx := newWebAPITestContext(t)
	got := webGet(t, ctx, `
var ce = new CustomEvent('data', { detail: { n: 42 } });
globalThis.__r = ce.type + '|' + ce.detail.n;
`, "__r")
	if got != "data|42" {
		t.Errorf("custom event = %q", got)
	}
}
