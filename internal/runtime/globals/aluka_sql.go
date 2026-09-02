package globals

// Aluka.SQL — SQLite + Postgres 统一查询 API（Phase 4 WBS 4.17/4.18）。
//
// 两种调用形式等价：
//
//	Aluka.SQL("SELECT * FROM users WHERE id = ?", [1]).all()
//	Aluka.SQL`SELECT * FROM users WHERE id = ${1}`.all()
//
// 后端选择（首次查询时惰性建立）：
//   - DATABASE_URL 以 postgres:// 或 postgresql:// 开头 → pgx（Postgres）
//   - 否则 → SQLite（SQLITE_PATH 指定文件，默认 :memory: 零配置）
//
// 查询对象方法（均返回 Promise）：
//   all()    → 全部行，每行为 {列名: 值}
//   values() → 全部行，每行为值数组
//   get()    → 首行对象或 null
//   run()    → { changes, lastInsertId }
//
// tagged template 形式下插值自动生成占位符（SQLite 用 ?，Postgres 用 $N）。
// 函数形式由调用方写占位符（SQLite 用 ?，Postgres 用 $N，二者在 Postgres
// 后端均受支持）。

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"

	// 注册驱动："sqlite"（modernc.org/sqlite）与 "pgx"（jackc/pgx stdlib）。
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

var (
	sqlOnce sync.Once
	sqlDB   *sql.DB
	sqlErr  error
)

// ResetSQLSingleton 关闭并清空 SQL 连接单例，使调用方可切换 SQLITE_PATH /
// DATABASE_URL 后重新建连。仅供测试使用（生产路径下连接随进程生命周期）。
func ResetSQLSingleton() {
	if sqlDB != nil {
		_ = sqlDB.Close()
	}
	sqlOnce = sync.Once{}
	sqlDB = nil
	sqlErr = nil
}

// alukaRegisterSQL 注册 Aluka.SQL。
func alukaRegisterSQL(ctx engine.Context, aluka engine.Value) {
	ao, _ := aluka.AsObject()
	_ = ao.Set("SQL", engine.NewFunction("SQL", func(args []engine.Value) (engine.Value, error) {
		q, err := newSQLQuery(ctx, args)
		if err != nil {
			return nil, err
		}
		return q.buildQueryValue(), nil
	}))
}

// sqlQuery 表示一个待执行的 SQL 查询。
type sqlQuery struct {
	ctx     engine.Context
	tagged  bool           // tagged template 形式（parts 拼接 + 插值占位符）
	parts   []string       // tagged：cooked quasis
	params  []engine.Value // 查询参数
	sqlText string         // 函数形式：SQL 原文
}

// newSQLQuery 从调用参数构造查询。首参为数组（TemplateStringsArray）时判定为
// tagged template 形式；否则为函数形式（SQL 字符串 + 可选参数数组）。
func newSQLQuery(ctx engine.Context, args []engine.Value) (*sqlQuery, error) {
	q := &sqlQuery{ctx: ctx}
	if len(args) == 0 {
		q.sqlText = ""
		return q, nil
	}
	if ao, ok := args[0].AsObject(); ok {
		if lv, err := ao.Get("length"); err == nil {
			if n, isInt := lv.Int(); isInt && n >= 0 && n <= 4096 {
				// 数组首参 → tagged template 形式（TemplateStringsArray）。
				q.tagged = true
				for i := 0; i < n; i++ {
					v, _ := ao.Get(strconv.Itoa(i))
					q.parts = append(q.parts, v.String())
				}
				q.params = args[1:]
				return q, nil
			}
		}
	}
	q.sqlText = args[0].String()
	if len(args) > 1 {
		if arr, ok := args[1].AsObject(); ok {
			if lv, err := arr.Get("length"); err == nil {
				if n, isInt := lv.Int(); isInt && n >= 0 && n <= 1<<20 {
					for i := 0; i < n; i++ {
						v, _ := arr.Get(strconv.Itoa(i))
						q.params = append(q.params, v)
					}
				}
			}
		} else {
			q.params = args[1:]
		}
	}
	return q, nil
}

// displaySQL 返回 query 属性展示的 SQL 文本。
func (q *sqlQuery) displaySQL() string {
	if q.tagged {
		return q.buildTaggedSQL(isPostgresBackend())
	}
	return q.sqlText
}

// buildTaggedSQL 按后端将 quasis 用占位符拼接。
func (q *sqlQuery) buildTaggedSQL(pg bool) string {
	var b strings.Builder
	b.WriteString(q.parts[0])
	for i := 1; i < len(q.parts); i++ {
		if pg {
			b.WriteString("$" + strconv.Itoa(i))
		} else {
			b.WriteByte('?')
		}
		b.WriteString(q.parts[i])
	}
	return b.String()
}

// buildSQL 返回最终执行的 SQL 与参数（函数形式下 Postgres 后端将 ? 转为 $N）。
func (q *sqlQuery) buildSQL(pg bool) string {
	if q.tagged {
		return q.buildTaggedSQL(pg)
	}
	if pg {
		return rewritePostgresPlaceholders(q.sqlText)
	}
	return q.sqlText
}

// rewritePostgresPlaceholders 将 SQL 中字符串字面量外的 ? 依次改写为 $N。
func rewritePostgresPlaceholders(sql string) string {
	var b strings.Builder
	n := 0
	i := 0
	for i < len(sql) {
		ch := sql[i]
		switch ch {
		case '\'', '"':
			// 复制整个字符串字面量。
			quote := ch
			b.WriteByte(ch)
			i++
			for i < len(sql) && sql[i] != quote {
				if sql[i] == '\\' && i+1 < len(sql) {
					b.WriteByte(sql[i])
					i++
				}
				b.WriteByte(sql[i])
				i++
			}
			if i < len(sql) {
				b.WriteByte(sql[i])
				i++
			}
		case '?':
			n++
			b.WriteString("$" + strconv.Itoa(n))
			i++
		default:
			b.WriteByte(ch)
			i++
		}
	}
	return b.String()
}

// buildQueryValue 构造查询对象 { query, all, get, run, values }。
func (q *sqlQuery) buildQueryValue() engine.Value {
	obj := engine.NewObject()
	_ = obj.Set("query", engine.Str(q.displaySQL()))
	_ = obj.Set("all", q.methodFn("all"))
	_ = obj.Set("get", q.methodFn("get"))
	_ = obj.Set("run", q.methodFn("run"))
	_ = obj.Set("values", q.methodFn("values"))
	return obj
}

// methodFn 构造单个查询方法（返回 Promise）。
func (q *sqlQuery) methodFn(mode string) engine.Value {
	return engine.NewFunction(mode, func(args []engine.Value) (engine.Value, error) {
		params := q.params
		if len(params) == 0 && len(args) > 0 {
			// 运行时覆盖参数：首参为数组则展开。
			if arr, ok := args[0].AsObject(); ok {
				if lv, err := arr.Get("length"); err == nil {
					if n, isInt := lv.Int(); isInt {
						params = make([]engine.Value, 0, n)
						for i := 0; i < n; i++ {
							v, _ := arr.Get(strconv.Itoa(i))
							params = append(params, v)
						}
					}
				}
			} else {
				params = args
			}
		}
		paramVals := jsParamsToGo(params)
		pg := isPostgresBackend()
		sqlText := q.buildSQL(pg)

		executor := engine.NewFunction("executor", func(ea []engine.Value) (engine.Value, error) {
			if len(ea) == 0 {
				return engine.Undefined(), nil
			}
			resolve := ea[0]
			var reject engine.Value
			if len(ea) > 1 {
				reject = ea[1]
			}
			release := q.ctx.AddRef()
			go func() {
				result, err := runSQLQuery(q.ctx, mode, sqlText, paramVals)
				q.ctx.PostTask(func() {
					defer release()
					if err != nil {
						if reject != nil {
							callReject(reject, err.Error())
						}
						return
					}
					callResolve(resolve, result)
				})
			}()
			return engine.Undefined(), nil
		})
		return newPromise(q.ctx, executor)
	})
}

// sqlOpen 惰性建立数据库连接（包级单例）。
func sqlOpen() (*sql.DB, error) {
	sqlOnce.Do(func() {
		sqlDB, sqlErr = openSQLDB()
	})
	return sqlDB, sqlErr
}

// isPostgresBackend 判断当前是否 Postgres 后端（与 openSQLDB 的选择逻辑一致）。
func isPostgresBackend() bool {
	dsn := os.Getenv("DATABASE_URL")
	return strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://")
}

// openSQLDB 按环境变量选择并打开数据库。
func openSQLDB() (*sql.DB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return nil, err
		}
		if err := db.Ping(); err != nil {
			return nil, fmt.Errorf("postgres connect: %w", err)
		}
		return db, nil
	}
	path := os.Getenv("SQLITE_PATH")
	if dsn != "" {
		path = strings.TrimPrefix(dsn, "sqlite:")
	}
	if path == "" {
		path = ":memory:"
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// SQLite 单写者：限制连接池为 1；:memory: 数据库按连接隔离，必须共用同一连接。
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}
	return db, nil
}

// runSQLQuery 执行查询并转换为 JS 值。
func runSQLQuery(ctx engine.Context, mode, sqlText string, params []any) (engine.Value, error) {
	db, err := sqlOpen()
	if err != nil {
		return engine.Undefined(), err
	}
	if mode == "run" {
		res, err := db.Exec(sqlText, params...)
		if err != nil {
			return engine.Undefined(), err
		}
		changes, _ := res.RowsAffected()
		lastID, _ := res.LastInsertId()
		obj := engine.NewObject()
		_ = obj.Set("changes", engine.Number(float64(changes)))
		if lastID > 0 {
			_ = obj.Set("lastInsertId", engine.Number(float64(lastID)))
		}
		return obj, nil
	}
	rows, err := db.Query(sqlText, params...)
	if err != nil {
		return engine.Undefined(), err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return engine.Undefined(), err
	}
	asArray := mode == "values"
	var rowVals []engine.Value
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return engine.Undefined(), err
		}
		rowVals = append(rowVals, sqlRowToJS(cols, vals, asArray))
	}
	if err := rows.Err(); err != nil {
		return engine.Undefined(), err
	}
	switch mode {
	case "get":
		if len(rowVals) == 0 {
			return engine.Null(), nil
		}
		return rowVals[0], nil
	default: // all / values
		return engine.NewArray(rowVals), nil
	}
}

// sqlRowToJS 将一行转为 JS 对象或值数组。
func sqlRowToJS(cols []string, vals []any, asArray bool) engine.Value {
	if asArray {
		arr := make([]engine.Value, len(vals))
		for i, v := range vals {
			arr[i] = sqlValueToJS(v)
		}
		return engine.NewArray(arr)
	}
	obj := engine.NewObject()
	for i, v := range vals {
		_ = obj.Set(cols[i], sqlValueToJS(v))
	}
	return obj
}

// sqlValueToJS 将 database/sql 扫描值转为 JS 值。
func sqlValueToJS(v any) engine.Value {
	switch t := v.(type) {
	case nil:
		return engine.Null()
	case bool:
		return engine.Boolean(t)
	case int64:
		return engine.Number(float64(t))
	case float64:
		return engine.Number(t)
	case string:
		return engine.Str(t)
	case []byte:
		return engine.Str(string(t))
	case time.Time:
		return engine.Str(t.Format(time.RFC3339))
	default:
		return engine.Str(fmt.Sprintf("%v", t))
	}
}

// jsParamsToGo 将 JS 参数值转为 database/sql 参数。
func jsParamsToGo(params []engine.Value) []any {
	out := make([]any, 0, len(params))
	for _, p := range params {
		out = append(out, jsValueToGo(p))
	}
	return out
}

// jsValueToGo 将单个 JS 值转为 Go 值。
func jsValueToGo(v engine.Value) any {
	if v == nil || v.IsNull() || v.IsUndefined() {
		return nil
	}
	switch v.Type() {
	case engine.TypeBoolean:
		b, _ := v.Bool()
		return b
	case engine.TypeNumber:
		f, ok := v.Float()
		if !ok {
			return nil
		}
		// 整数优先转 int64，避免浮点精度问题。
		if f == float64(int64(f)) && f >= -9e15 && f <= 9e15 {
			return int64(f)
		}
		return f
	case engine.TypeBigInt:
		return v.String()
	default:
		return v.String()
	}
}
