package globals

// Aluka.Redis — Redis 客户端（Phase 4 WBS 4.19，基于 go-redis/v9，RESP2）。
//
//	Aluka.Redis()                              // 回退 REDIS_URL env（默认 localhost:6379）
//	Aluka.Redis("redis://:pass@host:6379/0")
//	Aluka.Redis({ url, hostname, port, password, db })
//
// 客户端方法（均返回 Promise）：
//	connect()              连接测试（PING），返回 "OK"
//	get(key)               字符串值或 null
//	set(key, value)        返回 "OK"
//	del(...keys)           删除数量
//	hget(hash, field)      字段值或 null
//	hset(hash, field, value) 返回 1
//	close()                返回 "OK"

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/redis/go-redis/v9"
)

// alukaRegisterRedis 注册 Aluka.Redis。
func alukaRegisterRedis(ctx engine.Context, aluka engine.Value) {
	ao, _ := aluka.AsObject()
	_ = ao.Set("Redis", engine.NewFunction("Redis", func(args []engine.Value) (engine.Value, error) {
		opts, err := parseRedisOptions(args)
		if err != nil {
			return nil, err
		}
		client := redis.NewClient(opts)
		return buildRedisClient(ctx, client), nil
	}))
}

// parseRedisOptions 从参数与环境变量构造 go-redis 配置。
func parseRedisOptions(args []engine.Value) (*redis.Options, error) {
	if len(args) > 0 && args[0] != nil {
		if args[0].Type() == engine.TypeString {
			opts, err := redis.ParseURL(args[0].String())
			if err != nil {
				return nil, fmt.Errorf("Aluka.Redis: invalid URL: %w", err)
			}
			return opts, nil
		}
		if ao, ok := args[0].AsObject(); ok {
			if v, _ := ao.Get("url"); !v.IsUndefined() {
				if u := v.String(); u != "" {
					opts, err := redis.ParseURL(u)
					if err != nil {
						return nil, fmt.Errorf("Aluka.Redis: invalid url: %w", err)
					}
					return opts, nil
				}
			}
			opts := &redis.Options{Addr: "localhost:6379"}
			if v, _ := ao.Get("hostname"); !v.IsUndefined() && v.String() != "" {
				port := "6379"
				if p, _ := ao.Get("port"); !p.IsUndefined() && p.String() != "" {
					port = p.String()
				}
				opts.Addr = v.String() + ":" + port
			}
			if v, _ := ao.Get("password"); !v.IsUndefined() && v.String() != "" {
				opts.Password = v.String()
			}
			if v, _ := ao.Get("db"); !v.IsUndefined() && v.String() != "" {
				if n, err := strconv.Atoi(v.String()); err == nil {
					opts.DB = n
				}
			}
			return opts, nil
		}
	}
	if u := os.Getenv("REDIS_URL"); u != "" {
		opts, err := redis.ParseURL(u)
		if err != nil {
			return nil, fmt.Errorf("Aluka.Redis: invalid REDIS_URL: %w", err)
		}
		return opts, nil
	}
	return &redis.Options{Addr: "localhost:6379"}, nil
}

// buildRedisClient 构造 Redis 客户端对象（方法均返回 Promise）。
func buildRedisClient(ctx engine.Context, client *redis.Client) engine.Value {
	obj := engine.NewObject()
	method := func(name string) engine.Value {
		return engine.NewFunction(name, func(args []engine.Value) (engine.Value, error) {
			executor := engine.NewFunction("executor", func(ea []engine.Value) (engine.Value, error) {
				if len(ea) == 0 {
					return engine.Undefined(), nil
				}
				resolve := ea[0]
				var reject engine.Value
				if len(ea) > 1 {
					reject = ea[1]
				}
				release := ctx.AddRef()
				go func() {
					result, err := doRedis(client, name, args)
					ctx.PostTask(func() {
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
			return newPromise(ctx, executor)
		})
	}
	for _, name := range []string{"connect", "get", "set", "del", "hget", "hset", "close"} {
		_ = obj.Set(name, method(name))
	}
	return obj
}

// doRedis 执行 Redis 命令（在 Go goroutine 中调用）。
func doRedis(client *redis.Client, method string, args []engine.Value) (engine.Value, error) {
	cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch method {
	case "connect":
		if err := client.Ping(cctx).Err(); err != nil {
			return engine.Undefined(), err
		}
		return engine.Str("OK"), nil
	case "get":
		val, err := client.Get(cctx, redisStr(args, 0)).Result()
		if err == redis.Nil {
			return engine.Null(), nil
		}
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Str(val), nil
	case "set":
		if err := client.Set(cctx, redisStr(args, 0), redisStr(args, 1), 0).Err(); err != nil {
			return engine.Undefined(), err
		}
		return engine.Str("OK"), nil
	case "del":
		n, err := client.Del(cctx, redisStrs(args)...).Result()
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Number(float64(n)), nil
	case "hget":
		val, err := client.HGet(cctx, redisStr(args, 0), redisStr(args, 1)).Result()
		if err == redis.Nil {
			return engine.Null(), nil
		}
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Str(val), nil
	case "hset":
		n, err := client.HSet(cctx, redisStr(args, 0), redisStr(args, 1), redisStr(args, 2)).Result()
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Number(float64(n)), nil
	case "close":
		if err := client.Close(); err != nil {
			return engine.Undefined(), err
		}
		return engine.Str("OK"), nil
	}
	return engine.Undefined(), fmt.Errorf("Aluka.Redis: unknown method %s", method)
}

// redisStr 取第 i 个参数并转为字符串。
func redisStr(args []engine.Value, i int) string {
	if len(args) <= i || args[i] == nil {
		return ""
	}
	return args[i].String()
}

// redisStrs 取全部参数转为字符串切片。
func redisStrs(args []engine.Value) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a != nil {
			out = append(out, a.String())
		}
	}
	return out
}
