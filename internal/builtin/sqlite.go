package builtin

// node:sqlite 内置模块——同步 SQLite 数据库（Node 22 DatabaseSync 兼容）。
//
// 对齐 Node 语义：
//   - new DatabaseSync(path) 打开数据库（"node:sqlite" 22.5+）
//   - db.exec(sql) 执行无返回语句；db.prepare(sql) → StatementSync
//   - StatementSync.run()/get()/all()/iterate()，支持位置与命名参数
//   - 值类型：null/number/bigint/string/Buffer；setReadBigInts 切换 INTEGER 读法
//   - 单连接模型（SetMaxOpenConns(1)），保证 BEGIN IMMEDIATE 事务同步语义
//
// 实现采用 database/sql + modernc.org/sqlite（纯 Go，CGO 禁用）。

import (
	"database/sql"
	"fmt"
	"math"
	"math/big"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"

	// 注册 SQLite 驱动（modernc.org/sqlite，纯 Go）。
	_ "modernc.org/sqlite"
)

// sqliteDbState 是 DatabaseSync 实例的 Go 状态（闭包捕获，随 JS 对象 GC）。
type sqliteDbState struct {
	db *sql.DB
}

// sqliteStmtState 是 StatementSync 实例的 Go 状态。
type sqliteStmtState struct {
	stmt        *sql.Stmt
	sourceSQL   string
	readBigInts bool
}

// NewSQLite 构造 node:sqlite 模块导出对象。
func NewSQLite(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// DatabaseSync 构造器：new DatabaseSync(path)。
	ctor := engine.NewFunction("DatabaseSync", func(args []engine.Value) (engine.Value, error) {
		path := ""
		if len(args) > 0 {
			path = args[0].String()
		}
		db, err := sql.Open("sqlite", path)
		if err != nil {
			return engine.Undefined(), fmt.Errorf("node:sqlite: open %q: %w", path, err)
		}
		if err := db.Ping(); err != nil {
			db.Close()
			return engine.Undefined(), fmt.Errorf("node:sqlite: cannot open %q: %w", path, err)
		}
		// 单连接：同步事务（BEGIN/COMMIT 在同一连接上执行）。
		db.SetMaxOpenConns(1)
		state := &sqliteDbState{db: db}
		return newDatabaseSyncInstance(state), nil
	})
	ctorObj, _ := ctor.AsObject()
	proto := engine.NewObject()
	_ = proto.Set("constructor", ctor)
	_ = ctorObj.Set("prototype", proto)
	_ = m.Set("DatabaseSync", ctor)
	return m, nil
}

// newDatabaseSyncInstance 构造 DatabaseSync 实例对象。
func newDatabaseSyncInstance(state *sqliteDbState) engine.Value {
	obj := engine.NewObject()

	_ = obj.Set("exec", engine.NewFunction("exec", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("node:sqlite: exec requires SQL string")
		}
		if _, err := state.db.Exec(args[0].String()); err != nil {
			return engine.Undefined(), fmt.Errorf("node:sqlite: %w", err)
		}
		return engine.Undefined(), nil
	}))

	_ = obj.Set("prepare", engine.NewFunction("prepare", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("node:sqlite: prepare requires SQL string")
		}
		sqlText := args[0].String()
		stmt, err := state.db.Prepare(sqlText)
		if err != nil {
			return engine.Undefined(), fmt.Errorf("node:sqlite: %w", err)
		}
		return newStatementSyncInstance(&sqliteStmtState{stmt: stmt, sourceSQL: sqlText}), nil
	}))

	_ = obj.Set("close", engine.NewFunction("close", func(args []engine.Value) (engine.Value, error) {
		_ = state.db.Close()
		return engine.Undefined(), nil
	}))

	// isOpen 只读属性（Node 语义：DatabaseSync.isOpen，close 后为 false）。
	engine.SetAccessor(obj, "isOpen", engine.NewFunction("isOpen", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(state.db.Ping() == nil), nil
	}), nil)

	return obj
}

// newStatementSyncInstance 构造 StatementSync 实例对象。
func newStatementSyncInstance(state *sqliteStmtState) engine.Value {
	obj := engine.NewObject()
	_ = obj.Set("sourceSQL", engine.Str(state.sourceSQL))

	// 执行参数统一转换：支持位置参数与首个参数为命名对象（{a: 1}）。
	toArgs := func(args []engine.Value) ([]any, error) {
		if len(args) == 1 {
			if obj, ok := args[0].AsObject(); ok {
				// 无 length 属性（非数组/类数组）→ 命名参数对象。
				lv, err := obj.Get("length")
				if err != nil || lv.IsUndefined() {
					keys := obj.Keys()
					if len(keys) > 0 {
						named := make([]any, 0, len(keys))
						for _, k := range keys {
							v, err := obj.Get(k)
							if err != nil {
								return nil, err
							}
							named = append(named, sql.Named(k, sqliteParamToDriver(v)))
						}
						return named, nil
					}
				}
			}
		}
		out := make([]any, len(args))
		for i, a := range args {
			out[i] = sqliteParamToDriver(a)
		}
		return out, nil
	}

	_ = obj.Set("run", engine.NewFunction("run", func(args []engine.Value) (engine.Value, error) {
		params, err := toArgs(args)
		if err != nil {
			return engine.Undefined(), err
		}
		res, err := state.stmt.Exec(params...)
		if err != nil {
			return engine.Undefined(), fmt.Errorf("node:sqlite: %w", err)
		}
		result := engine.NewObject()
		if changes, e := res.RowsAffected(); e == nil {
			_ = result.Set("changes", engine.Number(float64(changes)))
		}
		if id, e := res.LastInsertId(); e == nil {
			_ = result.Set("lastInsertRowid", engine.Number(float64(id)))
		}
		return result, nil
	}))

	_ = obj.Set("get", engine.NewFunction("get", func(args []engine.Value) (engine.Value, error) {
		params, err := toArgs(args)
		if err != nil {
			return engine.Undefined(), err
		}
		rows, err := state.stmt.Query(params...)
		if err != nil {
			return engine.Undefined(), fmt.Errorf("node:sqlite: %w", err)
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return engine.Undefined(), fmt.Errorf("node:sqlite: %w", err)
		}
		if !rows.Next() {
			return engine.Undefined(), nil
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return engine.Undefined(), fmt.Errorf("node:sqlite: %w", err)
		}
		obj, err := sqliteRowToObject(state, cols, vals)
		if err != nil {
			return engine.Undefined(), err
		}
		return obj, nil
	}))

	_ = obj.Set("all", engine.NewFunction("all", func(args []engine.Value) (engine.Value, error) {
		params, err := toArgs(args)
		if err != nil {
			return engine.Undefined(), err
		}
		rows, err := state.stmt.Query(params...)
		if err != nil {
			return engine.Undefined(), fmt.Errorf("node:sqlite: %w", err)
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return engine.Undefined(), fmt.Errorf("node:sqlite: %w", err)
		}
		var out []engine.Value
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return engine.Undefined(), fmt.Errorf("node:sqlite: %w", err)
			}
			obj, err := sqliteRowToObject(state, cols, vals)
			if err != nil {
				return engine.Undefined(), err
			}
			out = append(out, obj)
		}
		return engine.NewArray(out), nil
	}))

	// iterate(...params)：返回同步迭代器（Node 兼容，基于 all 的实现）。
	_ = obj.Set("iterate", engine.NewFunction("iterate", func(args []engine.Value) (engine.Value, error) {
		params, err := toArgs(args)
		if err != nil {
			return engine.Undefined(), err
		}
		rows, err := state.stmt.Query(params...)
		if err != nil {
			return engine.Undefined(), fmt.Errorf("node:sqlite: %w", err)
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return engine.Undefined(), fmt.Errorf("node:sqlite: %w", err)
		}
		iter := engine.NewObject()
		done := false
		_ = iter.Set("next", engine.NewFunction("next", func(args []engine.Value) (engine.Value, error) {
			res := engine.NewObject()
			if done || !rows.Next() {
				done = true
				rows.Close()
				_ = res.Set("done", engine.Boolean(true))
				return res, nil
			}
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				done = true
				rows.Close()
				return engine.Undefined(), fmt.Errorf("node:sqlite: %w", err)
			}
			obj, err := sqliteRowToObject(state, cols, vals)
			if err != nil {
				done = true
				rows.Close()
				return engine.Undefined(), err
			}
			_ = res.Set("done", engine.Boolean(false))
			_ = res.Set("value", obj)
			return res, nil
		}))
		_ = iter.Set(engine.SymbolIterator.SymbolKey(), engine.NewFunction("[Symbol.iterator]", func(args []engine.Value) (engine.Value, error) {
			return iter, nil
		}))
		return iter, nil
	}))

	_ = obj.Set("setReadBigInts", engine.NewFunction("setReadBigInts", func(args []engine.Value) (engine.Value, error) {
		state.readBigInts = len(args) > 0 && boolArg(args, 0)
		return engine.Undefined(), nil
	}))

	_ = obj.Set("columns", engine.NewFunction("columns", func(args []engine.Value) (engine.Value, error) {
		// 简化：返回列名数组（Node 返回 ColumnInfo 对象数组）。
		rows, err := state.stmt.Query()
		if err != nil {
			return engine.Undefined(), fmt.Errorf("node:sqlite: %w", err)
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return engine.Undefined(), fmt.Errorf("node:sqlite: %w", err)
		}
		out := make([]engine.Value, len(cols))
		for i, c := range cols {
			ci := engine.NewObject()
			_ = ci.Set("name", engine.Str(c))
			_ = ci.Set("type", engine.Str(""))
			out[i] = ci
		}
		return engine.NewArray(out), nil
	}))

	return obj
}

// sqliteParamToDriver 将 JS 参数值转换为 database/sql 绑定值。
func sqliteParamToDriver(v engine.Value) any {
	if v.IsNull() || v.IsUndefined() {
		return nil
	}
	if v.Type() == engine.TypeBigInt {
		if bi, ok := engine.BigIntValue(v); ok {
			if bi.IsInt64() {
				return bi.Int64()
			}
			return bi.String() // 超出 int64：按文本存储（近似）
		}
	}
	// 数字：整数（且未溢出 int64）按 int64 绑定（SQLite INTEGER），
	// 否则按 REAL 绑定。注意 engine.Int() 对 1.5 会截断为 1，不能直接使用。
	if f, ok := v.Float(); ok {
		if f == math.Trunc(f) && f >= -9.2e18 && f <= 9.2e18 {
			return int64(f)
		}
		return f
	}
	if b, ok := engine.AsBuffer(v); ok {
		return b
	}
	return v.String()
}

// sqliteRowToObject 将一行扫描结果转换为 JS 行对象。
func sqliteRowToObject(state *sqliteStmtState, cols []string, vals []any) (engine.Value, error) {
	obj := engine.NewObject()
	for i, c := range cols {
		v, err := sqliteDriverToValue(state, vals[i])
		if err != nil {
			return nil, err
		}
		if err := obj.Set(c, v); err != nil {
			return nil, err
		}
	}
	return obj, nil
}

// sqliteDriverToValue 将 database/sql 扫描值转换为 JS 值。
func sqliteDriverToValue(state *sqliteStmtState, v any) (engine.Value, error) {
	switch val := v.(type) {
	case nil:
		return engine.Null(), nil
	case int64:
		if state.readBigInts {
			return engine.BigIntFromInt(val), nil
		}
		return engine.Number(float64(val)), nil
	case float64:
		return engine.Number(val), nil
	case string:
		return engine.Str(val), nil
	case []byte:
		return globals.NewBufferInstance(val), nil
	case bool:
		return engine.Boolean(val), nil
	case *big.Int:
		return engine.BigInt(val), nil
	default:
		return engine.Str(fmt.Sprintf("%v", val)), nil
	}
}
