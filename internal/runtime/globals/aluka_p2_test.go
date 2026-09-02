package globals

// Phase 4 P2 外部服务驱动测试：Aluka.SQL（SQLite 全量离线 + Postgres env 门控）、
// Aluka.Redis（构造/URL 解析 + 活服务 env 门控）、Aluka.S3（httptest 伪服务验证
// SigV4 签名与各方法）。

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/runtime/globals/galuka"
)

// resetSQLOnceForTest 重置 SQL 单例（关闭旧连接），使每个测试可用独立的 SQLITE_PATH。
func resetSQLOnceForTest() { galuka.ResetSQLSingleton() }

// TestAlukaSQLSQLite 验证 SQLite 后端完整 CRUD（函数调用形式）。
func TestAlukaSQLSQLite(t *testing.T) {
	resetSQLOnceForTest()
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "sqlite_test.db"))
	t.Cleanup(resetSQLOnceForTest)
	ctx := newAlukaTestEnv(t)
	code := `
Aluka.SQL("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, age INTEGER)").run()
  .then(function(r) {
    globalThis.__create = typeof r.changes === "number";
    return Aluka.SQL("INSERT INTO users (name, age) VALUES (?, ?)", ["alice", 30]).run();
  })
  .then(function(r) {
    globalThis.__insert = (r.lastInsertId === 1) && (r.changes === 1);
    return Aluka.SQL("INSERT INTO users (name, age) VALUES (?, ?)", ["bob", 25]).run();
  })
  .then(function() {
    return Aluka.SQL("SELECT * FROM users ORDER BY id").all();
  })
  .then(function(rows) {
    globalThis.__count = rows.length;
    globalThis.__first = rows[0].name + ":" + rows[0].age;
    return Aluka.SQL("SELECT name FROM users WHERE id = ?", [2]).get();
  })
  .then(function(row) {
    globalThis.__row = row.name;
    return Aluka.SQL("SELECT age FROM users ORDER BY id").values();
  })
  .then(function(vals) {
    globalThis.__vals = vals[0][0] + "," + vals[1][0];
  });
`
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	checks := map[string]string{
		"__create": "true",
		"__insert": "true",
		"__count":  "2",
		"__first":  "alice:30",
		"__row":    "bob",
		"__vals":   "30,25",
	}
	for k, want := range checks {
		if got := webGlobalGet(ctx, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

// TestAlukaSQLTagged 验证 tagged template 形式：插值自动生成占位符。
func TestAlukaSQLTagged(t *testing.T) {
	resetSQLOnceForTest()
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "sqlite_tagged.db"))
	t.Cleanup(resetSQLOnceForTest)
	ctx := newAlukaTestEnv(t)
	code := "var name = 'carol', age = 40;\n" +
		"Aluka.SQL`CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT, age INTEGER)`.run()\n" +
		"  .then(function() { return Aluka.SQL`INSERT INTO t (name, age) VALUES (${name}, ${age})`.run(); })\n" +
		"  .then(function(r) { globalThis.__ins = (r.changes === 1); return Aluka.SQL`SELECT * FROM t WHERE name = ${name}`.get(); })\n" +
		"  .then(function(row) { globalThis.__name = row.name; globalThis.__age = row.age; });\n"
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	checks := map[string]string{
		"__ins":  "true",
		"__name": "carol",
		"__age":  "40",
	}
	for k, want := range checks {
		if got := webGlobalGet(ctx, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

// TestAlukaSQLNullAndTypes 验证 NULL → null、字节 → 字符串等类型转换。
func TestAlukaSQLNullAndTypes(t *testing.T) {
	resetSQLOnceForTest()
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "sqlite_types.db"))
	t.Cleanup(resetSQLOnceForTest)
	ctx := newAlukaTestEnv(t)
	code := `
Aluka.SQL("CREATE TABLE t (id INTEGER PRIMARY KEY, val TEXT, flag INTEGER)").run()
  .then(function() { return Aluka.SQL("INSERT INTO t (val, flag) VALUES (NULL, 1)").run(); })
  .then(function() { return Aluka.SQL("SELECT val, flag FROM t").get(); })
  .then(function(row) {
    globalThis.__null = (row.val === null);
    globalThis.__flag = row.flag;
  });
`
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__null"); got != "true" {
		t.Errorf("__null = %q, want true", got)
	}
	if got := webGlobalGet(ctx, "__flag"); got != "1" {
		t.Errorf("__flag = %q, want 1", got)
	}
}

// TestAlukaSQLPostgres 验证 Postgres 后端（需真实服务器）。
func TestAlukaSQLPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live Postgres test")
	}
	resetSQLOnceForTest()
	t.Setenv("DATABASE_URL", dsn)
	ctx := newAlukaTestEnv(t)
	code := `
Aluka.SQL("CREATE TABLE IF NOT EXISTS aluka_test (id SERIAL PRIMARY KEY, name TEXT)").run()
  .then(function() { return Aluka.SQL("INSERT INTO aluka_test (name) VALUES ($1)", ["pg"]).run(); })
  .then(function(r) { globalThis.__changes = r.changes; return Aluka.SQL` + "`SELECT name FROM aluka_test WHERE name = ${'pg'}`" + `.get(); })
  .then(function(row) { globalThis.__name = row.name; return Aluka.SQL("DELETE FROM aluka_test").run(); });
`
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__name"); got != "pg" {
		t.Errorf("__name = %q, want pg", got)
	}
}

// TestAlukaRedisClient 验证客户端构造（离线）。
func TestAlukaRedisClient(t *testing.T) {
	ctx := newAlukaTestEnv(t)
	code := `
var r = Aluka.Redis();
globalThis.__obj = typeof r === "object" && typeof r.get === "function" && typeof r.set === "function" && typeof r.connect === "function";
var r2 = Aluka.Redis("redis://:pass@example.com:7000/3");
globalThis.__r2 = typeof r2 === "object";
`
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, k := range []string{"__obj", "__r2"} {
		if got := webGlobalGet(ctx, k); got != "true" {
			t.Errorf("%s = %q, want true", k, got)
		}
	}
}

// TestAlukaRedisLive 验证命令级操作（需真实 Redis 服务器）。
func TestAlukaRedisLive(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set; skipping live Redis test")
	}
	ctx := newAlukaTestEnv(t)
	code := fmt.Sprintf(`
var r = Aluka.Redis(%q);
r.connect().then(function(v) { globalThis.__connect = v; return r.set("k", "v"); })
  .then(function(v) { globalThis.__set = v; return r.get("k"); })
  .then(function(v) { globalThis.__get = v; return r.hset("h", "f", "x"); })
  .then(function(n) { globalThis.__hset = n; return r.hget("h", "f"); })
  .then(function(v) { globalThis.__hget = v; return r.del("k", "h"); })
  .then(function(n) { globalThis.__del = n; });
`, url)
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	checks := map[string]string{
		"__connect": "OK",
		"__set":     "OK",
		"__get":     "v",
		"__hset":    "1",
		"__hget":    "x",
		"__del":     "2",
	}
	for k, want := range checks {
		if got := webGlobalGet(ctx, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

// TestAlukaS3 用 httptest 伪 S3 服务验证 SigV4 签名与各方法。
func TestAlukaS3(t *testing.T) {
	var gotAuth, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		switch {
		case r.Method == http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			gotBody = string(body)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/bucket/data.txt":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("hello s3"))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`<?xml version="1.0"?><ListBucketResult>` +
				`<Contents><Key>data/a.txt</Key><Size>3</Size>` +
				`<LastModified>2026-01-01T00:00:00Z</LastModified><ETag>"abc"</ETag></Contents>` +
				`</ListBucketResult>`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	ctx := newAlukaTestEnv(t)
	code := fmt.Sprintf(`
var s = Aluka.S3({ accessKeyId: "AKID", secretAccessKey: "SECRET", region: "us-east-1", endpoint: %q, bucket: "bucket" });
globalThis.__obj = typeof s === "object" && typeof s.put === "function" && typeof s.get === "function";
s.put("data.txt", "hello s3").then(function(v) { globalThis.__put = v; return s.get("data.txt"); })
  .then(function(f) { globalThis.__size = f.size; globalThis.__ct = f.contentType; return f.text(); })
  .then(function(t) { globalThis.__text = t; return s.exists("data.txt"); })
  .then(function(e) { globalThis.__exists = e; return s.list("data"); })
  .then(function(items) { globalThis.__list = items.length + ":" + items[0].key; return s.delete("data.txt"); })
  .then(function(v) { globalThis.__del = v; });
`, srv.URL)
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__obj"); got != "true" {
		t.Errorf("__obj = %q, want true", got)
	}
	if got := webGlobalGet(ctx, "__put"); got != "OK" {
		t.Errorf("__put = %q, want OK", got)
	}
	if got := webGlobalGet(ctx, "__size"); got != "8" {
		t.Errorf("__size = %q, want 8", got)
	}
	if got := webGlobalGet(ctx, "__ct"); got != "text/plain" {
		t.Errorf("__ct = %q, want text/plain", got)
	}
	if got := webGlobalGet(ctx, "__text"); got != "hello s3" {
		t.Errorf("__text = %q, want hello s3", got)
	}
	if got := webGlobalGet(ctx, "__exists"); got != "true" {
		t.Errorf("__exists = %q, want true", got)
	}
	if got := webGlobalGet(ctx, "__list"); got != "1:data/a.txt" {
		t.Errorf("__list = %q, want 1:data/a.txt", got)
	}
	if got := webGlobalGet(ctx, "__del"); got != "OK" {
		t.Errorf("__del = %q, want OK", got)
	}
	// SigV4 签名头校验。
	if !strings.HasPrefix(gotAuth, "AWS4-HMAC-SHA256 Credential=AKID/") {
		t.Errorf("Authorization = %q, want AWS4-HMAC-SHA256 credential", gotAuth)
	}
	if gotPath != "/bucket/data.txt" && gotPath != "/bucket/data/a.txt" {
		t.Errorf("unexpected request path %q", gotPath)
	}
	if gotBody != "hello s3" {
		t.Errorf("PUT body = %q, want hello s3", gotBody)
	}
}
