package builtin

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/builtin/nodesqlite"
	"github.com/aluka-lang/aluka/internal/engine"
)

// node:sqlite DatabaseSync 端到端：CRUD + 命名参数 + 事务 + bigint 读。
func TestSQLiteDatabaseSync(t *testing.T) {
	ctx := newCtx(t)
	m, err := nodesqlite.NewSQLite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mo, _ := m.AsObject()
	ctor, err := mo.Get("DatabaseSync")
	if err != nil || !ctor.IsFunction() {
		t.Fatalf("DatabaseSync constructor missing")
	}
	bunCtor, err := mo.Get("Database")
	if err != nil || bunCtor != ctor {
		t.Fatalf("bun:sqlite Database alias missing")
	}
	cf, _ := ctor.AsFunction()
	dbVal, err := cf.Call([]engine.Value{engine.Str(":memory:")})
	if err != nil {
		t.Fatalf("new DatabaseSync: %v", err)
	}
	db, _ := dbVal.AsObject()

	// exec + prepare
	if err := callMethodErr(t, db, "exec", engine.Str("CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT, score REAL)")); err != nil {
		t.Fatal(err)
	}
	prepared, err := db.Get("prepare")
	if err != nil {
		t.Fatal(err)
	}
	pf, _ := prepared.AsFunction()
	ins, err := pf.Call([]engine.Value{engine.Str("INSERT INTO t (name, score) VALUES (?, ?)")})
	if err != nil {
		t.Fatal(err)
	}
	insObj, _ := ins.AsObject()
	runFn, _ := insObj.Get("run")
	rf, _ := runFn.AsFunction()
	res, err := rf.Call([]engine.Value{engine.Str("alice"), engine.Number(1.5)})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	resObj, _ := res.AsObject()
	if ch, _ := resObj.Get("changes"); ch.String() != "1" {
		t.Errorf("changes = %v, want 1", ch)
	}

	// get
	getFn, _ := insObj.Get("get")
	gf, _ := getFn.AsFunction()
	row, err := gf.Call([]engine.Value{engine.Str("x"), engine.Number(0)})
	_ = row
	// 用独立的 SELECT statement
	selVal, err := pf.Call([]engine.Value{engine.Str("SELECT * FROM t WHERE name = ?")})
	if err != nil {
		t.Fatal(err)
	}
	selObj, _ := selVal.AsObject()
	gf2, _ := selObj.Get("get")
	row2, err := gf2.(engine.Function).Call([]engine.Value{engine.Str("alice")})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row2.IsUndefined() {
		t.Fatal("get returned undefined")
	}
	rowObj, _ := row2.AsObject()
	if v, _ := rowObj.Get("name"); v.String() != "alice" {
		t.Errorf("name = %v, want alice", v)
	}
	if v, _ := rowObj.Get("score"); v.String() != "1.5" {
		t.Errorf("score = %v, want 1.5", v)
	}

	// 无行 → undefined
	row3, err := gf2.(engine.Function).Call([]engine.Value{engine.Str("nobody")})
	if err != nil || !row3.IsUndefined() {
		t.Errorf("no-row: got %v err %v, want undefined", row3, err)
	}

	// 事务（BEGIN IMMEDIATE + COMMIT 同步语义）
	if err := callMethodErr(t, db, "exec", engine.Str("BEGIN IMMEDIATE")); err != nil {
		t.Fatal(err)
	}
	if _, err := rf.Call([]engine.Value{engine.Str("bob"), engine.Number(2.5)}); err != nil {
		t.Fatal(err)
	}
	if err := callMethodErr(t, db, "exec", engine.Str("COMMIT")); err != nil {
		t.Fatal(err)
	}

	// Bun/better-sqlite3 transaction(fn) wrapper.
	transaction, _ := db.Get("transaction")
	tf, _ := transaction.AsFunction()
	callback := engine.NewFunction("write", func(args []engine.Value) (engine.Value, error) {
		return rf.Call([]engine.Value{engine.Str("carol"), engine.Number(3.5)})
	})
	wrapped, err := tf.Call([]engine.Value{callback})
	if err != nil {
		t.Fatal(err)
	}
	wrappedFn, _ := wrapped.AsFunction()
	if _, err := wrappedFn.Call(nil); err != nil {
		t.Fatalf("transaction wrapper: %v", err)
	}
	rowCarol, err := gf2.(engine.Function).Call([]engine.Value{engine.Str("carol")})
	if err != nil || rowCarol.IsUndefined() {
		t.Fatalf("transaction did not commit: row=%v err=%v", rowCarol, err)
	}

	// bigint 读
	bigVal, err := pf.Call([]engine.Value{engine.Str("SELECT id FROM t LIMIT 1")})
	if err != nil {
		t.Fatal(err)
	}
	bigObj, _ := bigVal.AsObject()
	setFn, _ := bigObj.Get("setReadBigInts")
	sf, _ := setFn.AsFunction()
	if _, err := sf.Call([]engine.Value{engine.Boolean(true)}); err != nil {
		t.Fatal(err)
	}
	gf3, _ := bigObj.Get("get")
	rowBig, err := gf3.(engine.Function).Call(nil)
	if err != nil {
		t.Fatal(err)
	}
	rowBigObj, _ := rowBig.AsObject()
	idV, _ := rowBigObj.Get("id")
	if idV.Type() != engine.TypeBigInt {
		t.Errorf("id type = %v, want bigint", idV.Type())
	}

	// isOpen 是 accessor 属性（Go 层 Get 返回 AccessorValue，getter 取值
	// 在 JS 层冒烟已验证：closed 后 isOpen === false）。
	if _, err := db.Get("isOpen"); err != nil {
		t.Errorf("isOpen property missing: %v", err)
	}
	if err := callMethodErr(t, db, "close"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Get("isOpen"); err != nil {
		t.Errorf("isOpen after close: %v", err)
	}
}

// callMethodErr 调用对象方法并返回错误。
func callMethodErr(t *testing.T, obj engine.Object, method string, args ...engine.Value) error {
	t.Helper()
	fn, err := obj.Get(method)
	if err != nil || !fn.IsFunction() {
		t.Fatalf("method %q not found", method)
	}
	f, _ := fn.AsFunction()
	_, err = f.Call(args)
	return err
}
