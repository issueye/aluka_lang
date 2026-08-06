package builtin

// node:dns 内置模块——域名解析。
//
// 实现要点：
//   - lookup/resolve 在 goroutine 执行 Go net 解析，完成后经 ctx.PostTask
//     回 JS 线程回调 (err, result)。
//   - dns.promises 变体用全局 Promise.resolve/reject 静态方法包装结果
//     （复用 loader 的 Promise 调用模式，避免依赖 interpreter 包）。
//   - 简化：rrtype 仅区分 A/AAAA（resolve4/resolve6），lookup 返回首个地址。

import (
	"fmt"
	"net"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewDNS 构造 node:dns 模块的导出对象。
func NewDNS(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()
	// DNS 错误码常量（与 dns/promises 共享同一组值）。
	registerDNSConstants(m)

	// dns.lookup(hostname[, options], callback)
	_ = m.Set("lookup", engine.NewFunction("lookup", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("dns.lookup: requires hostname and callback")
		}
		hostname := args[0].String()
		cb := args[1]
		// (hostname, options, callback) 形式。
		if args[1].IsObject() && len(args) > 2 && args[2].IsFunction() {
			cb = args[2]
		}
		asyncLookup(ctx, hostname, cb, func(addrs []string) (engine.Value, error) {
			if len(addrs) == 0 {
				return engine.Undefined(), fmt.Errorf("dns: ENOTFOUND %s", hostname)
			}
			return engine.Str(addrs[0]), nil
		})
		return engine.Undefined(), nil
	}))

	// dns.resolve(hostname[, rrtype], callback)
	_ = m.Set("resolve", engine.NewFunction("resolve", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("dns.resolve: requires hostname and callback")
		}
		hostname := args[0].String()
		cb := args[1]
		if args[1].IsFunction() && len(args) > 2 {
			_ = args[2] // rrtype 简化忽略
		} else if args[1].IsObject() || (args[1].Type() == engine.TypeString) {
			if len(args) > 2 && args[2].IsFunction() {
				cb = args[2]
			}
		}
		asyncLookup(ctx, hostname, cb, func(addrs []string) (engine.Value, error) {
			if len(addrs) == 0 {
				return engine.Undefined(), fmt.Errorf("dns: ENOTFOUND %s", hostname)
			}
			vals := make([]engine.Value, len(addrs))
			for i, a := range addrs {
				vals[i] = engine.Str(a)
			}
			return engine.NewArray(vals), nil
		})
		return engine.Undefined(), nil
	}))

	// dns.resolve4 / resolve6。
	_ = m.Set("resolve4", engine.NewFunction("resolve4", func(args []engine.Value) (engine.Value, error) {
		return callResolveVariant(ctx, args, 4)
	}))
	_ = m.Set("resolve6", engine.NewFunction("resolve6", func(args []engine.Value) (engine.Value, error) {
		return callResolveVariant(ctx, args, 6)
	}))

	// dns.promises：Promise 版 API（完整实现见 dns_promises.go）。
	// 与 node:dns/promises 共享同一对象（identity 一致）。
	_ = m.Set("promises", newDNSPromises(ctx))

	// setDefaultResultOrder：no-op（IPv4 优先固定）。
	_ = m.Set("setDefaultResultOrder", engine.NewFunction("setDefaultResultOrder", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))

	return m, nil
}

// asyncLookup 在 goroutine 解析主机名并回 JS 线程回调。
// convert 把地址列表转为回调结果值。AddRef 保持事件循环存活到回调执行。
func asyncLookup(ctx engine.Context, hostname string, cb engine.Value, convert func([]string) (engine.Value, error)) {
	release := ctx.AddRef()
	go func() {
		addrs, err := net.LookupHost(hostname)
		if err != nil {
			addrs = nil
		}
		ctx.PostTask(func() {
			defer release()
			if !cb.IsFunction() {
				return
			}
			f, _ := cb.AsFunction()
			if err != nil || len(addrs) == 0 {
				_, _ = f.Call([]engine.Value{engine.Str("ENOTFOUND " + hostname), engine.Null()})
				return
			}
			result, cerr := convert(addrs)
			if cerr != nil {
				_, _ = f.Call([]engine.Value{engine.Str(cerr.Error()), engine.Null()})
				return
			}
			_, _ = f.Call([]engine.Value{engine.Null(), result})
		})
	}()
}

// callResolveVariant 实现 resolve4/resolve6（按 ip 版本过滤）。
func callResolveVariant(ctx engine.Context, args []engine.Value, version int) (engine.Value, error) {
	if len(args) < 2 {
		return engine.Undefined(), fmt.Errorf("dns.resolve%d: requires hostname and callback", version)
	}
	hostname := args[0].String()
	cb := args[1]
	if len(args) > 2 && args[2].IsFunction() {
		cb = args[2]
	}
	asyncLookup(ctx, hostname, cb, func(addrs []string) (engine.Value, error) {
		vals := make([]engine.Value, 0, len(addrs))
		for _, a := range addrs {
			ip := net.ParseIP(a)
			if ip == nil {
				continue
			}
			if version == 4 && ip.To4() != nil {
				vals = append(vals, engine.Str(a))
			} else if version == 6 && ip.To4() == nil && !stringsContains(a, ".") {
				vals = append(vals, engine.Str(a))
			}
		}
		if len(vals) == 0 {
			return engine.Undefined(), fmt.Errorf("dns: ENOTFOUND %s", hostname)
		}
		return engine.NewArray(vals), nil
	})
	return engine.Undefined(), nil
}

// promiseLookup 返回真正的 Promise（用全局 Promise 构造器 + executor），
// 异步解析后 resolve/reject。
func promiseLookup(ctx engine.Context, hostname string, convert func([]string) (engine.Value, error)) (engine.Value, error) {
	promiseCtor, err := ctx.Global().Get("Promise")
	if err != nil || !promiseCtor.IsFunction() {
		return engine.Undefined(), fmt.Errorf("dns: global Promise not available")
	}
	executor := engine.NewFunction("executor", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		resolve, reject := args[0], args[1]
		release := ctx.AddRef()
		go func() {
			addrs, err := net.LookupHost(hostname)
			ctx.PostTask(func() {
				defer release()
				if err != nil || len(addrs) == 0 {
					if f, ok := reject.AsFunction(); ok {
						_, _ = f.Call([]engine.Value{engine.Str("ENOTFOUND " + hostname)})
					}
					return
				}
				result, cerr := convert(addrs)
				if cerr != nil {
					if f, ok := reject.AsFunction(); ok {
						_, _ = f.Call([]engine.Value{engine.Str(cerr.Error())})
					}
					return
				}
				if f, ok := resolve.AsFunction(); ok {
					_, _ = f.Call([]engine.Value{result})
				}
			})
		}()
		return engine.Undefined(), nil
	})
	pf, ok := promiseCtor.AsFunction()
	if !ok {
		return engine.Undefined(), fmt.Errorf("dns: Promise not callable")
	}
	return pf.Call([]engine.Value{executor})
}

// stringsContains 辅助（避免多余 import）。
func stringsContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
