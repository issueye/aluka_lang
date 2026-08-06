package builtin

// node:dns 内置模块——域名解析（callback 风格；Promise 版见 dns_promises.go）。
//
// 实现要点：
//   - lookup/resolve 在 goroutine 执行 Go net 解析，完成后经 ctx.PostTask
//     回 JS 线程回调 (err, result)。
//   - 记录类型解析（resolve4/6/Any/Cname/...）复用 dns_promises.go 的
//     dnsResolveByType / dnsResolveIP 实现，保证主模块与 promises 一致。
//   - 错误码常量与 Node 一致（ENOTFOUND/ENODATA/... 字符串）。

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

	// dns.lookup(hostname[, options], callback) → cb(err, address, family)
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
		asyncLookup(ctx, hostname, cb, func(addrs []string) (engine.Value, int, error) {
			if len(addrs) == 0 {
				return engine.Undefined(), 0, fmt.Errorf("dns: ENOTFOUND %s", hostname)
			}
			return engine.Str(addrs[0]), ipFamily(addrs[0]), nil
		})
		return engine.Undefined(), nil
	}))

	// dns.resolve(hostname[, rrtype], callback)：rrtype 决定返回结构（默认 'A'）。
	_ = m.Set("resolve", engine.NewFunction("resolve", func(args []engine.Value) (engine.Value, error) {
		hostname, rrtype, cb, err := parseResolveArgs(args, "resolve")
		if err != nil {
			return engine.Undefined(), err
		}
		asyncResolve(ctx, cb, func() (engine.Value, error) {
			return dnsResolveByType(hostname, rrtype)
		})
		return engine.Undefined(), nil
	}))

	// dns.resolve4 / resolve6。
	_ = m.Set("resolve4", engine.NewFunction("resolve4", func(args []engine.Value) (engine.Value, error) {
		hostname, _, cb, err := parseResolveArgs(args, "resolve4")
		if err != nil {
			return engine.Undefined(), err
		}
		asyncResolve(ctx, cb, func() (engine.Value, error) { return dnsResolveIP(hostname, 4) })
		return engine.Undefined(), nil
	}))
	_ = m.Set("resolve6", engine.NewFunction("resolve6", func(args []engine.Value) (engine.Value, error) {
		hostname, _, cb, err := parseResolveArgs(args, "resolve6")
		if err != nil {
			return engine.Undefined(), err
		}
		asyncResolve(ctx, cb, func() (engine.Value, error) { return dnsResolveIP(hostname, 6) })
		return engine.Undefined(), nil
	}))

	// 其余记录类型（与 promises 模块共享解析实现）。
	for _, name := range []string{
		"resolveAny", "resolveCaa", "resolveCname", "resolveMx", "resolveNaptr",
		"resolveNs", "resolvePtr", "resolveSoa", "resolveSrv", "resolveTlsa", "resolveTxt",
	} {
		nameCopy := name
		_ = m.Set(nameCopy, engine.NewFunction(nameCopy, func(args []engine.Value) (engine.Value, error) {
			hostname, _, cb, err := parseResolveArgs(args, nameCopy)
			if err != nil {
				return engine.Undefined(), err
			}
			rrtype := resolveVariantToRRType(nameCopy)
			asyncResolve(ctx, cb, func() (engine.Value, error) {
				return dnsResolveByType(hostname, rrtype)
			})
			return engine.Undefined(), nil
		}))
	}

	// dns.lookupService(address, port, callback) → cb(err, {hostname, service})
	_ = m.Set("lookupService", engine.NewFunction("lookupService", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return engine.Undefined(), fmt.Errorf("dns.lookupService: requires address, port and callback")
		}
		address := args[0].String()
		port := args[1].String()
		cb := args[2]
		asyncResolve(ctx, cb, func() (engine.Value, error) {
			host, err := net.LookupAddr(address)
			if err != nil || len(host) == 0 {
				return engine.Undefined(), fmt.Errorf("ENOTFOUND %s", address)
			}
			res := engine.NewObject()
			_ = res.Set("hostname", engine.Str(trimTrailingDot(host[0])))
			_ = res.Set("service", engine.Str(portServiceName(port)))
			return res, nil
		})
		return engine.Undefined(), nil
	}))

	// dns.reverse(ip, callback)。
	_ = m.Set("reverse", engine.NewFunction("reverse", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("dns.reverse: requires ip and callback")
		}
		ip := args[0].String()
		cb := args[1]
		asyncResolve(ctx, cb, func() (engine.Value, error) {
			names, err := net.LookupAddr(ip)
			if err != nil {
				return engine.Undefined(), fmt.Errorf("ENOTFOUND %s", ip)
			}
			vals := make([]engine.Value, 0, len(names))
			for _, n := range names {
				vals = append(vals, engine.Str(trimTrailingDot(n)))
			}
			return engine.NewArray(vals), nil
		})
		return engine.Undefined(), nil
	}))

	// setDefaultResultOrder / getDefaultResultOrder（简化：记录顺序，默认 verbatim）。
	_ = m.Set("setDefaultResultOrder", engine.NewFunction("setDefaultResultOrder", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			defaultResultOrder = args[0].String()
		}
		return engine.Undefined(), nil
	}))
	_ = m.Set("getDefaultResultOrder", engine.NewFunction("getDefaultResultOrder", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(defaultResultOrder), nil
	}))

	// getServers/setServers：Go net 无法枚举系统 resolver，维护进程内列表。
	_ = m.Set("getServers", engine.NewFunction("getServers", func(args []engine.Value) (engine.Value, error) {
		return dnsServersState, nil
	}))
	_ = m.Set("setServers", engine.NewFunction("setServers", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			dnsServersState = args[0]
		}
		return engine.Undefined(), nil
	}))

	// dns.Resolver：callback 风格实例。
	_ = m.Set("Resolver", newDNSResolverCtor(ctx))

	// dns.promises：Promise 版 API（完整实现见 dns_promises.go）。
	// 与 node:dns/promises 共享同一对象（identity 一致）。
	_ = m.Set("promises", newDNSPromises(ctx))

	return m, nil
}

// parseResolveArgs 解析 resolve 系列参数（hostname[, rrtype], callback）。
func parseResolveArgs(args []engine.Value, name string) (hostname, rrtype string, cb engine.Value, err error) {
	if len(args) < 2 {
		return "", "A", engine.Undefined(), fmt.Errorf("dns.%s: requires hostname and callback", name)
	}
	hostname = args[0].String()
	rrtype = "A"
	cb = args[1]
	if !args[1].IsFunction() && len(args) > 2 && args[2].IsFunction() {
		rrtype = args[1].String()
		cb = args[2]
	}
	return hostname, rrtype, cb, nil
}

// resolveVariantToRRType 把 resolveXxx 方法名映射到 resolve 的 rrtype。
func resolveVariantToRRType(name string) string {
	switch name {
	case "resolveAny":
		return "ANY"
	case "resolveCaa":
		return "CAA"
	case "resolveCname":
		return "CNAME"
	case "resolveMx":
		return "MX"
	case "resolveNaptr":
		return "NAPTR"
	case "resolveNs":
		return "NS"
	case "resolvePtr":
		return "PTR"
	case "resolveSoa":
		return "SOA"
	case "resolveSrv":
		return "SRV"
	case "resolveTlsa":
		return "TLSA"
	case "resolveTxt":
		return "TXT"
	default:
		return "A"
	}
}

// asyncLookup 在 goroutine 解析主机名并回 JS 线程回调 (err, address, family)。
func asyncLookup(ctx engine.Context, hostname string, cb engine.Value,
	convert func([]string) (engine.Value, int, error)) {
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
				_, _ = f.Call([]engine.Value{makeDNSError(ctx, "ENOTFOUND", hostname), engine.Null()})
				return
			}
			result, family, cerr := convert(addrs)
			if cerr != nil {
				_, _ = f.Call([]engine.Value{makeDNSError(ctx, "ENOTFOUND", hostname), engine.Null()})
				return
			}
			_, _ = f.Call([]engine.Value{engine.Null(), result, engine.IntValue(family)})
		})
	}()
}

// asyncResolve 通用 callback 解析：在 goroutine 计算，回 JS 线程 cb(err, result)。
func asyncResolve(ctx engine.Context, cb engine.Value, compute func() (engine.Value, error)) {
	if !cb.IsFunction() {
		return
	}
	release := ctx.AddRef()
	f, _ := cb.AsFunction()
	go func() {
		result, cerr := compute()
		ctx.PostTask(func() {
			defer release()
			if cerr != nil {
				_, _ = f.Call([]engine.Value{makeDNSError(ctx, "ENOTFOUND", errHostname(cerr)), engine.Null()})
				return
			}
			_, _ = f.Call([]engine.Value{engine.Null(), result})
		})
	}()
}

// makeDNSError 构造带 code 的 DNS 错误对象。
func makeDNSError(ctx engine.Context, code, hostname string) engine.Value {
	ev := makeErrorValue(ctx, fmt.Errorf("%s %s", code, hostname))
	if obj, ok := ev.AsObject(); ok {
		_ = obj.Set("code", engine.Str(code))
		_ = obj.Set("errno", engine.Str(code))
		_ = obj.Set("hostname", engine.Str(hostname))
	}
	return ev
}

func errHostname(err error) string {
	// 简化：错误消息中不解析具体 hostname，统一用空串。
	return ""
}

// defaultResultOrder 记录 setDefaultResultOrder 的值（Node 22 默认 verbatim）。
var defaultResultOrder = "verbatim"

// dnsServersState 进程内 getServers/setServers 状态。
var dnsServersState = func() engine.Value {
	return engine.NewArray(nil)
}()

// newDNSResolverCtor 构造 callback 风格 Resolver 类。
func newDNSResolverCtor(ctx engine.Context) engine.Value {
	resolverProto := engine.NewObject()
	// resolve 系列（callback 风格）。
	resolveMethods := []string{
		"resolve", "resolve4", "resolve6", "resolveAny", "resolveCaa", "resolveCname",
		"resolveMx", "resolveNaptr", "resolveNs", "resolvePtr", "resolveSoa",
		"resolveSrv", "resolveTlsa", "resolveTxt", "reverse",
	}
	for _, name := range resolveMethods {
		nameCopy := name
		_ = resolverProto.Set(nameCopy, engine.NewFunction(nameCopy, func(args []engine.Value) (engine.Value, error) {
			if len(args) < 2 {
				return engine.Undefined(), fmt.Errorf("dns.Resolver.%s: requires hostname and callback", nameCopy)
			}
			hostname := args[0].String()
			cb := args[1]
			rrtype := "A"
			if !args[1].IsFunction() && len(args) > 2 && args[2].IsFunction() {
				rrtype = args[1].String()
				cb = args[2]
			}
			if nameCopy == "reverse" {
				asyncResolve(ctx, cb, func() (engine.Value, error) {
					names, err := net.LookupAddr(hostname)
					if err != nil {
						return engine.NewArray(nil), nil
					}
					vals := make([]engine.Value, 0, len(names))
					for _, n := range names {
						vals = append(vals, engine.Str(trimTrailingDot(n)))
					}
					return engine.NewArray(vals), nil
				})
				return engine.Undefined(), nil
			}
			t := rrtype
			if nameCopy == "resolve4" {
				t = "A"
			} else if nameCopy == "resolve6" {
				t = "AAAA"
			} else if nameCopy != "resolve" {
				t = resolveVariantToRRType(nameCopy)
			}
			asyncResolve(ctx, cb, func() (engine.Value, error) {
				return dnsResolveByType(hostname, t)
			})
			return engine.Undefined(), nil
		}))
	}
	_ = resolverProto.Set("cancel", engine.NewFunction("cancel", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))
	_ = resolverProto.Set("getServers", engine.NewFunction("getServers", func(args []engine.Value) (engine.Value, error) {
		return dnsServersState, nil
	}))
	_ = resolverProto.Set("setServers", engine.NewFunction("setServers", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			dnsServersState = args[0]
		}
		return engine.Undefined(), nil
	}))

	ctor := engine.NewFunction("Resolver", func(args []engine.Value) (engine.Value, error) {
		inst := engine.NewObject()
		engine.SetProto(inst, resolverProto) // instanceof 依赖原型链
		for _, k := range resolverProto.Keys() {
			if v, err := resolverProto.Get(k); err == nil {
				_ = inst.Set(k, v)
			}
		}
		return inst, nil
	})
	if co, ok := ctor.AsObject(); ok {
		_ = co.Set("prototype", resolverProto)
	}
	return ctor
}
