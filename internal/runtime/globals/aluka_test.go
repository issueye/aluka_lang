package globals

// Phase 4 Aluka API 测试：基础信息、hash/password/compress、deepEquals、
// CSV/TOML/YAML、$ shell、spawnSync、serve 闭环（fetch → Aluka.serve）。

import (
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
)

// newAlukaTestEnv 创建带全部 Web API + Aluka 全局的 VM 上下文。
func newAlukaTestEnv(t *testing.T) engine.Context {
	t.Helper()
	ctx := newFetchTestEnv(t)
	if err := NewAluka(ctx, AlukaConfig{}); err != nil {
		t.Fatal(err)
	}
	return ctx
}

// TestAlukaBasics 验证基本信息与环境访问。
func TestAlukaBasics(t *testing.T) {
	ctx := newAlukaTestEnv(t)
	code := `
globalThis.__v = typeof Aluka.version === 'string';
globalThis.__alias = (Aluka === Bun);
globalThis.__p = typeof Aluka.platform === 'string';
globalThis.__a = typeof Aluka.arch === 'string';
globalThis.__env = typeof Aluka.env === 'object';
globalThis.__cwd = Aluka.cwd().length > 0;
globalThis.__ns = typeof Aluka.nanoseconds() === 'number';
Aluka.gc();
globalThis.__gc = true;
`
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, k := range []string{"__v", "__alias", "__p", "__a", "__env", "__cwd", "__ns", "__gc"} {
		if got := webGlobalGet(ctx, k); got != "true" {
			t.Errorf("%s = %q, want true", k, got)
		}
	}
}

// TestAlukaFileWrite 验证 Aluka.write 与 Aluka.file。
func TestAlukaFileWrite(t *testing.T) {
	ctx := newAlukaTestEnv(t)
	path := t.TempDir() + "/aluka_write.txt"
	code := fmt.Sprintf(`
Aluka.write(%q, "hello file");
globalThis.__f = Aluka.file(%q);
globalThis.__size = __f.size;
__f.text().then(function(t) { globalThis.__text = t; });
`, path, path)
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__size"); got != "10" {
		t.Errorf("size = %q, want 10", got)
	}
	if got := webGlobalGet(ctx, "__text"); got != "hello file" {
		t.Errorf("text = %q, want hello file", got)
	}
}

// TestAlukaHash 验证 hash（bigint）与 sha1/sha256/sha512。
func TestAlukaHash(t *testing.T) {
	ctx := newAlukaTestEnv(t)
	code := `
globalThis.__t = typeof Aluka.hash("x");
globalThis.__sha = Aluka.hash.sha256("abc");
globalThis.__s1 = Aluka.hash.sha1("abc").length;
globalThis.__s5 = Aluka.hash.sha512("abc").length;
`
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__t"); got != "bigint" {
		t.Errorf("hash type = %q, want bigint", got)
	}
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := webGlobalGet(ctx, "__sha"); got != want {
		t.Errorf("sha256 = %q, want %q", got, want)
	}
	if got := webGlobalGet(ctx, "__s1"); got != "40" {
		t.Errorf("sha1 len = %q, want 40", got)
	}
	if got := webGlobalGet(ctx, "__s5"); got != "128" {
		t.Errorf("sha512 len = %q, want 128", got)
	}
}

// TestAlukaPassword 验证 password.hash/verify 往返。
func TestAlukaPassword(t *testing.T) {
	ctx := newAlukaTestEnv(t)
	code := `
Aluka.password.hash("secret123").then(function(h) {
  globalThis.__prefix = h.startsWith("aluka-scrypt$");
  Aluka.password.verify("secret123", h).then(function(ok) {
    globalThis.__ok = ok;
    Aluka.password.verify("wrong", h).then(function(bad) {
      globalThis.__bad = bad;
    });
  });
});
`
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__prefix"); got != "true" {
		t.Errorf("hash prefix = %q", got)
	}
	if got := webGlobalGet(ctx, "__ok"); got != "true" {
		t.Errorf("verify ok = %q, want true", got)
	}
	if got := webGlobalGet(ctx, "__bad"); got != "false" {
		t.Errorf("verify bad = %q, want false", got)
	}
}

// TestAlukaCompress 验证 deflate/inflate/gzip/gunzip 往返。
func TestAlukaCompress(t *testing.T) {
	ctx := newAlukaTestEnv(t)
	code := `
var d = Aluka.deflateSync("hello compress world");
globalThis.__dIsU8 = d instanceof Uint8Array;
globalThis.__dRound = Aluka.inflateSync(d).toString();
var g = Aluka.gzipSync("gzip data");
globalThis.__gRound = Aluka.gunzipSync(g).toString();
Aluka.deflate("async deflate").then(function(da) {
  globalThis.__aRound = Aluka.inflateSync(da).toString();
});
`
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__dIsU8"); got != "true" {
		t.Errorf("deflateSync instanceof Uint8Array = %q, want true", got)
	}
	if got := webGlobalGet(ctx, "__dRound"); got != "hello compress world" {
		t.Errorf("inflate roundtrip = %q", got)
	}
	if got := webGlobalGet(ctx, "__gRound"); got != "gzip data" {
		t.Errorf("gunzip roundtrip = %q", got)
	}
	if got := webGlobalGet(ctx, "__aRound"); got != "async deflate" {
		t.Errorf("async deflate roundtrip = %q", got)
	}
}

// TestAlukaUtil 验证 deepEquals/deepAssign/peek/escapeHTML。
func TestAlukaUtil(t *testing.T) {
	ctx := newAlukaTestEnv(t)
	code := `
globalThis.__eq = Aluka.deepEquals({a:1,b:{c:[1,2]}}, {a:1,b:{c:[1,2]}});
globalThis.__neq = Aluka.deepEquals({a:1}, {a:2});
var t = {a:1, b:{x:1}};
Aluka.deepAssign(t, {b:{y:2}, c:3});
globalThis.__merge = (t.b.x === 1 && t.b.y === 2 && t.c === 3);
globalThis.__esc = Aluka.escapeHTML("<a>&");
var pending = new Promise(function(){});
globalThis.__peekPending = Aluka.peek(pending) === undefined;
var done = Promise.resolve("v");
globalThis.__peekDone = Aluka.peek(done);
`
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, k := range []string{"__eq", "__neq", "__merge"} {
		want := "true"
		if k == "__neq" {
			want = "false"
		}
		if got := webGlobalGet(ctx, k); got != want {
			t.Errorf("%s = %q, want %s", k, got, want)
		}
	}
	if got := webGlobalGet(ctx, "__esc"); got != "&lt;a&gt;&amp;" {
		t.Errorf("escapeHTML = %q", got)
	}
	if got := webGlobalGet(ctx, "__peekDone"); got != "v" {
		t.Errorf("peek(done) = %q, want v", got)
	}
}

// TestAlukaEncoding 验证 CSV/TSV/TOML/YAML 编解码。
func TestAlukaEncoding(t *testing.T) {
	ctx := newAlukaTestEnv(t)
	code := `
var csv = Aluka.CSV.parse("a,b,c\n1,2,3\n");
globalThis.__csv = csv[0][1] === "b" && csv[1][2] === "3";
globalThis.__csvOut = Aluka.CSV.stringify([["a","b"],["1","2"]]).trim();
var toml = Aluka.TOML.parse("name = \"aluka\"\n[server]\nhost = \"localhost\"\n");
globalThis.__toml = toml.name === "aluka" && toml.server.host === "localhost";
var yaml = Aluka.YAML.parse("name: aluka\nitems:\n  - 1\n  - 2\n");
globalThis.__yaml = yaml.name === "aluka" && Array.isArray(yaml.items) && yaml.items[1] === 2;
var topList = Aluka.YAML.parse("- a\n- b\n");
globalThis.__topList = Array.isArray(topList) && topList[0] === "a";
`
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, k := range []string{"__csv", "__toml", "__yaml", "__topList"} {
		if got := webGlobalGet(ctx, k); got != "true" {
			t.Errorf("%s = %q, want true", k, got)
		}
	}
	if got := webGlobalGet(ctx, "__csvOut"); got != "a,b\n1,2" {
		t.Errorf("CSV.stringify = %q", got)
	}
}

// TestAlukaShell 验证 Aluka.$ 执行命令。
func TestAlukaShell(t *testing.T) {
	ctx := newAlukaTestEnv(t)
	code := `
Aluka.$("echo hello from shell").then(function(out) {
  globalThis.__out = out.stdout.trim();
  globalThis.__code = out.exitCode;
  out.text().then(function(t) { globalThis.__text = t.trim(); });
});
`
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__out"); got != "hello from shell" {
		t.Errorf("$ stdout = %q", got)
	}
	if got := webGlobalGet(ctx, "__text"); got != "hello from shell" {
		t.Errorf("$ text = %q", got)
	}
	if got := webGlobalGet(ctx, "__code"); got != "0" {
		t.Errorf("$ exitCode = %q, want 0", got)
	}
}

// TestAlukaShellTaggedTemplate 验证 Aluka.$ 标记模板形式（P1-2）。
func TestAlukaShellTaggedTemplate(t *testing.T) {
	ctx := newAlukaTestEnv(t)
	code := `
Aluka.$` + "`echo tagged-shell-out`" + `.then(function(out) {
  globalThis.__out = out.stdout.trim();
  globalThis.__code = out.exitCode;
});
`
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__out"); got != "tagged-shell-out" {
		t.Errorf("$ tagged stdout = %q", got)
	}
	if got := webGlobalGet(ctx, "__code"); got != "0" {
		t.Errorf("$ tagged exitCode = %q, want 0", got)
	}
}

// TestAlukaSpawnSync 验证 spawnSync 同步执行。
func TestAlukaSpawnSync(t *testing.T) {
	ctx := newAlukaTestEnv(t)
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "cmd /C echo spawn-sync-out"
	} else {
		cmd = "echo spawn-sync-out"
	}
	code := fmt.Sprintf(`
var r = Aluka.spawnSync(%q);
globalThis.__out = r.stdout.toString().trim();
globalThis.__code = r.exitCode;
`, cmd)
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__out"); got != "spawn-sync-out" {
		t.Errorf("spawnSync stdout = %q", got)
	}
	if got := webGlobalGet(ctx, "__code"); got != "0" {
		t.Errorf("spawnSync exitCode = %q, want 0", got)
	}
}

// TestAlukaExternalAPI 验证 SQL/Redis/S3 的对象骨架与配置校验。
func TestAlukaExternalAPI(t *testing.T) {
	ctx := newAlukaTestEnv(t)
	code := `
var q = Aluka.SQL("SELECT 1");
globalThis.__q = typeof q === "object" && typeof q.all === "function" && typeof q.get === "function" && typeof q.run === "function" && typeof q.values === "function";
var qq = Aluka.SQL` + "`SELECT 1`" + `;
globalThis.__qt = typeof qq === "object" && typeof qq.all === "function";
var r = Aluka.Redis();
globalThis.__r = typeof r === "object" && typeof r.get === "function" && typeof r.set === "function" && typeof r.connect === "function";
var r2 = Aluka.Redis({ hostname: "example.com", port: 7000, password: "x" });
globalThis.__r2 = typeof r2 === "object";
var s = Aluka.S3();
globalThis.__s = typeof s === "object" && typeof s.put === "function" && typeof s.get === "function";
s.exists("k").catch(function(e) { globalThis.__sErr = typeof e === "string" && e.indexOf("configured") >= 0; });
`
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, k := range []string{"__q", "__qt", "__r", "__r2", "__s"} {
		if got := webGlobalGet(ctx, k); got != "true" {
			t.Errorf("%s = %q, want true", k, got)
		}
	}
	if got := webGlobalGet(ctx, "__sErr"); got != "true" {
		t.Errorf("__sErr = %q, want true (missing credentials should reject)", got)
	}
}

// TestAlukaServe 验证 serve → fetch 完整闭环。
func TestAlukaServe(t *testing.T) {
	ctx := newAlukaTestEnv(t)
	serveCode := `
globalThis.__srv = Aluka.serve({ port: 0, fetch: function(req) {
  if (req.url === "/json") return Response.json({ ok: true, name: "aluka" });
  if (req.url === "/post" && req.method === "POST") return new Response("got:" + req.body, { status: 201 });
  return new Response("not found", { status: 404 });
}});
globalThis.__port = __srv.port;
`
	if _, err := ctx.Eval(serveCode, "serve.js"); err != nil {
		t.Fatalf("eval serve: %v", err)
	}
	port := webGlobalGet(ctx, "__port")
	if port == "" || port == "undefined" || strings.Contains(port, "NaN") {
		t.Fatalf("serve port not exposed synchronously: %q", port)
	}

	// 启动事件循环（serve 的 AddRef 使其保持运行）。
	done := make(chan struct{})
	go func() {
		if vm, ok := ctx.(interface{ RunLoop() }); ok {
			vm.RunLoop()
		}
		close(done)
	}()

	base := "http://localhost:" + port
	// JSON 响应。
	resp, err := http.Get(base + "/json")
	if err != nil {
		t.Fatalf("GET /json: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("/json status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"ok":true`) {
		t.Errorf("/json body = %q", body)
	}
	// POST 响应。
	resp2, err := http.Post(base+"/post", "text/plain", strings.NewReader("abc"))
	if err != nil {
		t.Fatalf("POST /post: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != 201 || string(body2) != "got:abc" {
		t.Errorf("/post = %d %q, want 201 got:abc", resp2.StatusCode, body2)
	}
	// 404。
	resp3, err := http.Get(base + "/missing")
	if err != nil {
		t.Fatalf("GET /missing: %v", err)
	}
	_, _ = io.ReadAll(resp3.Body)
	resp3.Body.Close()
	if resp3.StatusCode != 404 {
		t.Errorf("/missing status = %d, want 404", resp3.StatusCode)
	}

	// stop 释放活跃计数 → 事件循环退出。
	if _, err := ctx.Eval(`__srv.stop();`, "stop.js"); err != nil {
		t.Fatalf("eval stop: %v", err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("event loop did not exit after stop")
	}
}
