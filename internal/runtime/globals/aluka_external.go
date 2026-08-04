package globals

// Aluka.SQL / Redis / S3（Phase 4 WBS 4.17-4.20，P2 部分）。
//
// 本实现为 API 骨架（stub）：对象与常用方法存在以保持 Bun 代码可解析，
// 调用时返回 rejected Promise 并给出明确提示。真实驱动（pgx /
// modernc.org/sqlite / go-redis / aws-sdk-go-v2）依赖外部服务，后续按需引入。

import "github.com/aluka-lang/aluka/internal/engine"

const alukaExternalErr = " not implemented in this build (Aluka Phase 4 P2: external service driver pending)"

// alukaRegisterExternal 注册外部服务 API stub。
func alukaRegisterExternal(ctx engine.Context, aluka engine.Value) {
	ao, _ := aluka.AsObject()

	// Aluka.SQL(sql, params?) → 查询对象（all/get/run/values → rejected）。
	_ = ao.Set("SQL", engine.NewFunction("SQL", func(args []engine.Value) (engine.Value, error) {
		q := engine.NewObject()
		sql := ""
		if len(args) > 0 {
			sql = args[0].String()
		}
		_ = q.Set("query", engine.Str(sql))
		rejectFn := func(name string) engine.Value {
			return engine.NewFunction(name, func(a []engine.Value) (engine.Value, error) {
				return promiseRejectValue(ctx, "Aluka.SQL."+name+alukaExternalErr)
			})
		}
		_ = q.Set("all", rejectFn("all"))
		_ = q.Set("get", rejectFn("get"))
		_ = q.Set("run", rejectFn("run"))
		_ = q.Set("values", rejectFn("values"))
		return q, nil
	}))

	// Aluka.Redis(options?) → 客户端（get/set/connect → rejected）。
	_ = ao.Set("Redis", engine.NewFunction("Redis", func(args []engine.Value) (engine.Value, error) {
		client := engine.NewObject()
		rejectFn := func(name string) engine.Value {
			return engine.NewFunction(name, func(a []engine.Value) (engine.Value, error) {
				return promiseRejectValue(ctx, "Aluka.Redis."+name+alukaExternalErr)
			})
		}
		for _, name := range []string{"connect", "get", "set", "del", "hget", "hset", "close"} {
			_ = client.Set(name, rejectFn(name))
		}
		return client, nil
	}))

	// Aluka.S3(options?) → 客户端（get/put/list → rejected）。
	_ = ao.Set("S3", engine.NewFunction("S3", func(args []engine.Value) (engine.Value, error) {
		client := engine.NewObject()
		rejectFn := func(name string) engine.Value {
			return engine.NewFunction(name, func(a []engine.Value) (engine.Value, error) {
				return promiseRejectValue(ctx, "Aluka.S3."+name+alukaExternalErr)
			})
		}
		for _, name := range []string{"get", "put", "delete", "list", "exists"} {
			_ = client.Set(name, rejectFn(name))
		}
		return client, nil
	}))
}
