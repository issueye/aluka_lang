package globals

// Phase 3 Web API 测试：fetch/Response/Blob/ReadableStream。
// 用 Go httptest 启动本地服务器，JS fetch 请求后经事件循环 resolve。

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gbuffer"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gcrypto"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gevent"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gfetch"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gstream"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gtimers"
)

// newFetchTestEnv 创建带 Web API 全局 + 事件循环的 VM 上下文。
func newFetchTestEnv(t *testing.T) engine.Context {
	t.Helper()
	eng := interpreter.NewVMEngine()
	ctx, err := eng.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	t.Cleanup(func() { ctx.Close() })
	if err := gfetch.NewFetch(ctx, gfetch.FetchConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := gbuffer.NewBuffer(ctx, gbuffer.BufferConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := gstream.NewBlob(ctx, gstream.BlobConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := gstream.NewStream(ctx, gstream.StreamConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := gcrypto.NewWebCrypto(ctx, gcrypto.WebCryptoConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := gfetch.NewURLPattern(ctx, gfetch.URLPatternConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := gevent.NewMessageChannel(ctx, gevent.MessageConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := gfetch.NewWebSocket(ctx, gfetch.WebSocketConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := gtimers.NewTimers(ctx, gtimers.TimerConfig{}); err != nil {
		t.Fatal(err)
	}
	_ = ctx.Global().Set("globalThis", ctx.Global())
	return ctx
}

// fetchRun 执行 JS 并在事件循环中驱动直到退出或超时。
func fetchRun(t *testing.T, ctx engine.Context, code string) error {
	t.Helper()
	if _, err := ctx.Eval(code, "fetch_test.js"); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		if vm, ok := ctx.(interface{ RunLoop() }); ok {
			vm.RunLoop()
		}
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("event loop timeout")
	}
}

// webGlobalGet 读全局键。
func webGlobalGet(ctx engine.Context, key string) string {
	v, _ := ctx.Global().Get(key)
	return v.String()
}

// TestFetchJSON 验证 fetch 请求本地服务器并解析 JSON。
func TestFetchJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"hello":"world","n":42}`)
	}))
	defer srv.Close()

	ctx := newFetchTestEnv(t)
	code := fmt.Sprintf(`
fetch('%s/json')
  .then(function(res) { globalThis.__status = res.status; return res.json(); })
  .then(function(data) { globalThis.__data = data.hello + ':' + data.n; });
`, srv.URL)
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__status"); got != "200" {
		t.Errorf("status = %q, want 200", got)
	}
	if got := webGlobalGet(ctx, "__data"); got != "world:42" {
		t.Errorf("data = %q, want world:42", got)
	}
}

// TestFetchPost 验证 POST 方法 + 请求头。
func TestFetchPost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := ""
		if r.Body != nil {
			buf := make([]byte, 1024)
			n, _ := r.Body.Read(buf)
			body = string(buf[:n])
		}
		_, _ = fmt.Fprintf(w, "%s:%s:%s", r.Method, r.Header.Get("X-Token"), body)
	}))
	defer srv.Close()

	ctx := newFetchTestEnv(t)
	code := fmt.Sprintf(`
var headers = new Headers({ 'X-Token': 'abc' });
fetch('%s/api', { method: 'POST', headers: headers, body: 'payload' })
  .then(function(res) { return res.text(); })
  .then(function(t) { globalThis.__r = t; });
`, srv.URL)
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__r"); got != "POST:abc:payload" {
		t.Errorf("post = %q, want POST:abc:payload", got)
	}
}

// TestFetchStreamsResponseBody verifies that fetch resolves on headers and
// exposes network reads incrementally instead of buffering the complete body.
func TestFetchStreamsResponseBody(t *testing.T) {
	releaseSecondChunk := make(chan struct{})
	streamFinished := make(chan struct{})
	var releaseOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/release" {
			releaseOnce.Do(func() { close(releaseSecondChunk) })
			_, _ = fmt.Fprint(w, "released")
			return
		}
		_, _ = fmt.Fprint(w, "first")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-releaseSecondChunk:
		case <-time.After(2 * time.Second):
		}
		_, _ = fmt.Fprint(w, "second")
		close(streamFinished)
	}))
	defer srv.Close()

	ctx := newFetchTestEnv(t)
	code := fmt.Sprintf(`
fetch('%[1]s/stream').then(function(res) {
  globalThis.__fetchResolved = 'true';
  fetch('%[1]s/release');
  var reader = res.body.getReader();
  return reader.read().then(function(first) {
    globalThis.__first = first.value.toString();
    return reader.read();
  }).then(function(second) {
    globalThis.__second = second.value.toString();
    return reader.read();
  }).then(function(last) {
    globalThis.__done = String(last.done);
  });
});
`, srv.URL)
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	select {
	case <-streamFinished:
	default:
		t.Fatal("stream handler did not finish")
	}
	if got := webGlobalGet(ctx, "__fetchResolved"); got != "true" {
		t.Errorf("fetch resolved = %q, want true", got)
	}
	if got := webGlobalGet(ctx, "__first"); got != "first" {
		t.Errorf("first chunk = %q, want first", got)
	}
	if got := webGlobalGet(ctx, "__second"); got != "second" {
		t.Errorf("second chunk = %q, want second", got)
	}
	if got := webGlobalGet(ctx, "__done"); got != "true" {
		t.Errorf("stream done = %q, want true", got)
	}
}

// TestFetchDoesNotBlockTimers verifies that waiting for response headers does
// not monopolize the JS thread used by TUI progress and input timers.
func TestFetchDoesNotBlockTimers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		_, _ = fmt.Fprint(w, "ready")
	}))
	defer srv.Close()

	ctx := newFetchTestEnv(t)
	code := fmt.Sprintf(`
var ticks = 0;
var timer = setInterval(function() { ticks++; }, 10);
fetch('%s').then(function(res) { return res.text(); }).then(function(text) {
  clearInterval(timer);
  globalThis.__fetchTimerProgress = text + ':' + ticks;
});
`, srv.URL)
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	value := webGlobalGet(ctx, "__fetchTimerProgress")
	parts := strings.Split(value, ":")
	if len(parts) != 2 || parts[0] != "ready" {
		t.Fatalf("fetch timer result = %q, want ready:<ticks>", value)
	}
	ticks, err := strconv.Atoi(parts[1])
	if err != nil || ticks < 10 {
		t.Fatalf("timer ticks during fetch = %q, want at least 10", parts[1])
	}
}

// TestBlobBasics 验证 Blob/File。
func TestBlobBasics(t *testing.T) {
	ctx := newFetchTestEnv(t)
	err := fetchRun(t, ctx, `
var b = new Blob(['hello', ' ', 'world'], { type: 'text/plain' });
globalThis.__size = b.size;
globalThis.__type = b.type;
b.text().then(function(t) { globalThis.__text = t; });
var f = new File(['file-data'], 'a.txt');
globalThis.__fname = f.name;
globalThis.__fsize = f.size;
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__size"); got != "11" {
		t.Errorf("size = %q, want 11", got)
	}
	if got := webGlobalGet(ctx, "__type"); got != "text/plain" {
		t.Errorf("type = %q, want text/plain", got)
	}
	if got := webGlobalGet(ctx, "__text"); got != "hello world" {
		t.Errorf("text = %q, want hello world", got)
	}
	if got := webGlobalGet(ctx, "__fname"); got != "a.txt" {
		t.Errorf("fname = %q, want a.txt", got)
	}
	if got := webGlobalGet(ctx, "__fsize"); got != "9" {
		t.Errorf("fsize = %q, want 9", got)
	}
}

// TestReadableStream 验证 ReadableStream read 顺序。
func TestReadableStream(t *testing.T) {
	ctx := newFetchTestEnv(t)
	err := fetchRun(t, ctx, `
var rs = new ReadableStream({
  start: function(c) { c.enqueue('a'); c.enqueue('b'); c.close(); }
});
var reader = rs.getReader();
var out = [];
reader.read().then(function(r1) {
  out.push(r1.value);
  return reader.read();
}).then(function(r2) {
  out.push(r2.value);
  return reader.read();
}).then(function(r3) {
  out.push(String(r3.done));
  globalThis.__stream = out.join(',');
});
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__stream"); got != "a,b,true" {
		t.Errorf("stream = %q, want a,b,true", got)
	}
}

// TestResponseBasics 验证 Response 构造与属性。
func TestResponseBasics(t *testing.T) {
	ctx := newFetchTestEnv(t)
	err := fetchRun(t, ctx, `
var r = new Response('body-text', { status: 201, headers: { 'X-Test': '1' } });
globalThis.__r = r.status + ':' + r.ok + ':' + r.statusText + ':' + r.headers.get('X-Test');
r.text().then(function(t) { globalThis.__rt = t; });
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__r"); got != "201:true:Created:1" {
		t.Errorf("response = %q, want 201:true:Created:1", got)
	}
	if got := webGlobalGet(ctx, "__rt"); got != "body-text" {
		t.Errorf("text = %q, want body-text", got)
	}
}

// TestHeadersFormData 验证 Headers 与 FormData。
func TestHeadersFormData(t *testing.T) {
	ctx := newFetchTestEnv(t)
	err := fetchRun(t, ctx, `
var h = new Headers();
h.append('X-A', '1');
h.append('X-A', '2');
h.set('X-B', 'b');
globalThis.__h = h.get('X-A') + ':' + h.has('X-B') + ':' + h.get('X-C');
var fd = new FormData();
fd.append('k1', 'v1');
fd.append('k2', 'v2');
fd.set('k1', 'v1b');
globalThis.__fd = fd.get('k1') + ':' + fd.getAll('k2').length;
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// WHATWG：同名多次 append 后 get 返回 ", " 合并值（与 node 一致）。
	if got := webGlobalGet(ctx, "__h"); got != "1, 2:true:null" {
		t.Errorf("headers = %q, want 1, 2:true:null", got)
	}
	if got := webGlobalGet(ctx, "__fd"); got != "v1b:1" {
		t.Errorf("formdata = %q, want v1b:1", got)
	}
}

// TestFetchProxy 验证 fetch({ proxy }) 走 HTTP 代理而不是直连目标站。
func TestFetchProxy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "from-target")
	}))
	defer target.Close()

	proxyHit := false
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHit = true
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "from-proxy")
	}))
	defer proxy.Close()

	ctx := newFetchTestEnv(t)
	code := fmt.Sprintf(`
fetch(%q, { proxy: %q })
  .then(function(res) { return res.text(); })
  .then(function(text) { globalThis.__body = text; })
  .catch(function(err) { globalThis.__err = String(err); });
`, target.URL, proxy.URL)
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__err"); got != "" && got != "undefined" {
		t.Fatalf("fetch error: %s", got)
	}
	if !proxyHit {
		t.Fatal("proxy was not used")
	}
	if got := webGlobalGet(ctx, "__body"); got != "from-proxy" {
		t.Errorf("body = %q, want from-proxy", got)
	}
}
