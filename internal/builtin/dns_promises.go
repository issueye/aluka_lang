package builtin

// node:dns/promises 内置模块——Promise 版 DNS API。
//
// 设计：与 node:dns.promises 子对象共享同一导出对象（Node 中
// `require('node:dns/promises') === require('node:dns').promises`）。
// 所有方法返回真正的 Promise（全局 Promise 构造器 + executor）。
// 实现基于 Go net 标准库；Go 未提供的记录类型（CAA/NAPTR/TLSA/SOA）
// 按 Node 返回结构返回空/近似值，并在 knownDifference 记录。

import (
	"fmt"
	"net"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
)

// newDNSPromises 构造 dns.promises 导出对象（完整 Promise API 面）。
func newDNSPromises(ctx engine.Context) engine.Value {
	p := engine.NewObject()
	registerDNSConstants(p)
	_ = p.Set("lookup", engine.NewFunction("lookup", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("dns.promises.lookup: requires hostname")
		}
		hostname := args[0].String()
		// Node 语义：lookup 返回 { address, family } 对象。
		return promiseLookup(ctx, hostname, func(addrs []string) (engine.Value, error) {
			if len(addrs) == 0 {
				return engine.Undefined(), fmt.Errorf("ENOTFOUND %s", hostname)
			}
			o := engine.NewObject()
			_ = o.Set("address", engine.Str(addrs[0]))
			_ = o.Set("family", engine.IntValue(ipFamily(addrs[0])))
			return o, nil
		})
	}))

	_ = p.Set("lookupService", engine.NewFunction("lookupService", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("dns.promises.lookupService: requires address and port")
		}
		address := args[0].String()
		port := args[1].String()
		return promiseResolve(ctx, func() (engine.Value, error) {
			host, err := net.LookupAddr(address)
			if err != nil || len(host) == 0 {
				return engine.Undefined(), fmt.Errorf("ENOTFOUND %s", address)
			}
			res := engine.NewObject()
			_ = res.Set("hostname", engine.Str(trimTrailingDot(host[0])))
			// service 简化：常见端口映射（http/https）外返回端口字符串。
			_ = res.Set("service", engine.Str(portServiceName(port)))
			return res, nil
		})
	}))

	// resolve(hostname[, rrtype])：rrtype 决定返回结构（默认 'A' 数组）。
	_ = p.Set("resolve", engine.NewFunction("resolve", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("dns.promises.resolve: requires hostname")
		}
		hostname := args[0].String()
		rrtype := "A"
		if len(args) > 1 {
			rrtype = args[1].String()
		}
		return promiseResolve(ctx, func() (engine.Value, error) {
			return dnsResolveByType(hostname, rrtype)
		})
	}))

	_ = p.Set("resolve4", engine.NewFunction("resolve4", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("dns.promises.resolve4: requires hostname")
		}
		return promiseResolve(ctx, func() (engine.Value, error) { return dnsResolveIP(args[0].String(), 4) })
	}))
	_ = p.Set("resolve6", engine.NewFunction("resolve6", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("dns.promises.resolve6: requires hostname")
		}
		return promiseResolve(ctx, func() (engine.Value, error) { return dnsResolveIP(args[0].String(), 6) })
	}))
	_ = p.Set("resolveAny", engine.NewFunction("resolveAny", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("dns.promises.resolveAny: requires hostname")
		}
		// 近似：返回 A 记录对象数组（Node 返回混合类型数组）。
		return promiseResolve(ctx, func() (engine.Value, error) { return dnsResolveAny(args[0].String()) })
	}))

	// Go net 不直接提供 CAA/NAPTR/TLSA/SOA 查询；按 Node 结构返回空数组/空对象。
	for _, name := range []string{"resolveCaa", "resolveNaptr", "resolveTlsa"} {
		nameCopy := name
		_ = p.Set(nameCopy, engine.NewFunction(nameCopy, func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 {
				return engine.Undefined(), fmt.Errorf("dns.promises.%s: requires hostname", nameCopy)
			}
			// Node 对未知/无记录也 resolve 空数组（ENODATA 时为空数组而非 reject）。
			return promiseResolve(ctx, func() (engine.Value, error) { return engine.NewArray(nil), nil })
		}))
	}

	_ = p.Set("resolveCname", engine.NewFunction("resolveCname", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("dns.promises.resolveCname: requires hostname")
		}
		return promiseResolve(ctx, func() (engine.Value, error) {
			cname, err := net.LookupCNAME(args[0].String())
			if err != nil {
				return engine.NewArray(nil), nil
			}
			return engine.NewArray([]engine.Value{engine.Str(trimTrailingDot(cname))}), nil
		})
	}))

	_ = p.Set("resolveMx", engine.NewFunction("resolveMx", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("dns.promises.resolveMx: requires hostname")
		}
		return promiseResolve(ctx, func() (engine.Value, error) {
			mxs, err := net.LookupMX(args[0].String())
			if err != nil {
				return engine.NewArray(nil), nil
			}
			vals := make([]engine.Value, 0, len(mxs))
			for _, mx := range mxs {
				o := engine.NewObject()
				_ = o.Set("exchange", engine.Str(trimTrailingDot(mx.Host)))
				_ = o.Set("priority", engine.IntValue(int(mx.Pref)))
				vals = append(vals, o)
			}
			return engine.NewArray(vals), nil
		})
	}))

	_ = p.Set("resolveNs", engine.NewFunction("resolveNs", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("dns.promises.resolveNs: requires hostname")
		}
		return promiseResolve(ctx, func() (engine.Value, error) {
			nss, err := net.LookupNS(args[0].String())
			if err != nil {
				return engine.NewArray(nil), nil
			}
			vals := make([]engine.Value, 0, len(nss))
			for _, ns := range nss {
				vals = append(vals, engine.Str(trimTrailingDot(ns.Host)))
			}
			return engine.NewArray(vals), nil
		})
	}))

	_ = p.Set("resolvePtr", engine.NewFunction("resolvePtr", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("dns.promises.resolvePtr: requires hostname")
		}
		return promiseResolve(ctx, func() (engine.Value, error) {
			ptrs, err := net.LookupAddr(args[0].String())
			if err != nil {
				return engine.NewArray(nil), nil
			}
			vals := make([]engine.Value, 0, len(ptrs))
			for _, p := range ptrs {
				vals = append(vals, engine.Str(trimTrailingDot(p)))
			}
			return engine.NewArray(vals), nil
		})
	}))

	_ = p.Set("resolveSoa", engine.NewFunction("resolveSoa", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("dns.promises.resolveSoa: requires hostname")
		}
		// Go 无 SOA 查询 API；Node 对无记录 reject ENODATA，对存在记录返回对象。
		// 返回近似空对象并在文档记录 knownDifference。
		return promiseResolve(ctx, func() (engine.Value, error) { return engine.NewObject(), nil })
	}))

	_ = p.Set("resolveSrv", engine.NewFunction("resolveSrv", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("dns.promises.resolveSrv: requires hostname")
		}
		return promiseResolve(ctx, func() (engine.Value, error) {
			_, srvs, err := net.LookupSRV("", "", args[0].String())
			if err != nil {
				return engine.NewArray(nil), nil
			}
			vals := make([]engine.Value, 0, len(srvs))
			for _, s := range srvs {
				o := engine.NewObject()
				_ = o.Set("name", engine.Str(trimTrailingDot(s.Target)))
				_ = o.Set("port", engine.IntValue(int(s.Port)))
				_ = o.Set("priority", engine.IntValue(int(s.Priority)))
				_ = o.Set("weight", engine.IntValue(int(s.Weight)))
				vals = append(vals, o)
			}
			return engine.NewArray(vals), nil
		})
	}))

	_ = p.Set("resolveTxt", engine.NewFunction("resolveTxt", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("dns.promises.resolveTxt: requires hostname")
		}
		return promiseResolve(ctx, func() (engine.Value, error) {
			txts, err := net.LookupTXT(args[0].String())
			if err != nil {
				return engine.NewArray(nil), nil
			}
			vals := make([]engine.Value, 0, len(txts))
			for _, t := range txts {
				vals = append(vals, engine.NewArray([]engine.Value{engine.Str(t)}))
			}
			return engine.NewArray(vals), nil
		})
	}))

	_ = p.Set("reverse", engine.NewFunction("reverse", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("dns.promises.reverse: requires ip")
		}
		return promiseResolve(ctx, func() (engine.Value, error) {
			names, err := net.LookupAddr(args[0].String())
			if err != nil {
				return engine.NewArray(nil), nil
			}
			vals := make([]engine.Value, 0, len(names))
			for _, n := range names {
				vals = append(vals, engine.Str(trimTrailingDot(n)))
			}
			return engine.NewArray(vals), nil
		})
	}))

	// getServers/setServers：Go net 无法枚举系统 resolver，维护进程内列表。
	var serversState engine.Value = engine.NewArray(nil)
	_ = p.Set("getServers", engine.NewFunction("getServers", func(args []engine.Value) (engine.Value, error) {
		return serversState, nil
	}))
	_ = p.Set("setServers", engine.NewFunction("setServers", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			serversState = args[0]
		}
		return engine.Undefined(), nil
	}))
	_ = p.Set("setDefaultResultOrder", engine.NewFunction("setDefaultResultOrder", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			defaultResultOrder = args[0].String()
		}
		return engine.Undefined(), nil
	}))
	_ = p.Set("getDefaultResultOrder", engine.NewFunction("getDefaultResultOrder", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(defaultResultOrder), nil
	}))

	// Resolver 类：实例方法与 promises 模块共享实现。
	resolverProto := engine.NewObject()
	for _, name := range []string{
		"resolve", "resolve4", "resolve6", "resolveAny", "resolveCaa", "resolveCname",
		"resolveMx", "resolveNaptr", "resolveNs", "resolvePtr", "resolveSoa",
		"resolveSrv", "resolveTlsa", "resolveTxt", "reverse",
	} {
		// 复用 promises 模块上的同名方法作为原型方法。
		mName := name
		if fn, err := p.Get(name); err == nil {
			_ = resolverProto.Set(mName, fn)
		}
	}
	_ = resolverProto.Set("getServers", engine.NewFunction("getServers", func(args []engine.Value) (engine.Value, error) {
		return serversState, nil
	}))
	_ = resolverProto.Set("setServers", engine.NewFunction("setServers", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			serversState = args[0]
		}
		return engine.Undefined(), nil
	}))
	_ = resolverProto.Set("cancel", engine.NewFunction("cancel", func(args []engine.Value) (engine.Value, error) {
		return engine.Undefined(), nil
	}))

	resolverCtor := engine.NewFunction("Resolver", func(args []engine.Value) (engine.Value, error) {
		inst := engine.NewObject()
		for _, k := range resolverProto.Keys() {
			if v, err := resolverProto.Get(k); err == nil {
				_ = inst.Set(k, v)
			}
		}
		return inst, nil
	})
	if co, ok := resolverCtor.AsObject(); ok {
		_ = co.Set("prototype", resolverProto)
	}
	_ = p.Set("Resolver", resolverCtor)

	return p
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
						if _, err := f.Call([]engine.Value{makeDNSError(ctx, "ENOTFOUND", hostname)}); err != nil {
							interpreter.ReportUncaught(nil, err)
						}
					}
					return
				}
				result, cerr := convert(addrs)
				if cerr != nil {
					if f, ok := reject.AsFunction(); ok {
						if _, err := f.Call([]engine.Value{makeDNSError(ctx, "ENOTFOUND", hostname)}); err != nil {
							interpreter.ReportUncaught(nil, err)
						}
					}
					return
				}
				if f, ok := resolve.AsFunction(); ok {
					if _, err := f.Call([]engine.Value{result}); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
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

// dnsResolveByType 按 rrtype 返回 resolve 结构。
func dnsResolveByType(hostname, rrtype string) (engine.Value, error) {
	switch rrtype {
	case "A":
		return dnsResolveIP(hostname, 4)
	case "AAAA":
		return dnsResolveIP(hostname, 6)
	case "ANY":
		return dnsResolveAny(hostname)
	case "CNAME":
		cname, err := net.LookupCNAME(hostname)
		if err != nil {
			return engine.NewArray(nil), nil
		}
		return engine.NewArray([]engine.Value{engine.Str(trimTrailingDot(cname))}), nil
	case "MX":
		mxs, err := net.LookupMX(hostname)
		if err != nil {
			return engine.NewArray(nil), nil
		}
		vals := make([]engine.Value, 0, len(mxs))
		for _, mx := range mxs {
			o := engine.NewObject()
			_ = o.Set("exchange", engine.Str(trimTrailingDot(mx.Host)))
			_ = o.Set("priority", engine.IntValue(int(mx.Pref)))
			vals = append(vals, o)
		}
		return engine.NewArray(vals), nil
	case "NS":
		nss, err := net.LookupNS(hostname)
		if err != nil {
			return engine.NewArray(nil), nil
		}
		vals := make([]engine.Value, 0, len(nss))
		for _, ns := range nss {
			vals = append(vals, engine.Str(trimTrailingDot(ns.Host)))
		}
		return engine.NewArray(vals), nil
	case "PTR":
		ptrs, err := net.LookupAddr(hostname)
		if err != nil {
			return engine.NewArray(nil), nil
		}
		vals := make([]engine.Value, 0, len(ptrs))
		for _, p := range ptrs {
			vals = append(vals, engine.Str(trimTrailingDot(p)))
		}
		return engine.NewArray(vals), nil
	case "SRV":
		_, srvs, err := net.LookupSRV("", "", hostname)
		if err != nil {
			return engine.NewArray(nil), nil
		}
		vals := make([]engine.Value, 0, len(srvs))
		for _, s := range srvs {
			o := engine.NewObject()
			_ = o.Set("name", engine.Str(trimTrailingDot(s.Target)))
			_ = o.Set("port", engine.IntValue(int(s.Port)))
			_ = o.Set("priority", engine.IntValue(int(s.Priority)))
			_ = o.Set("weight", engine.IntValue(int(s.Weight)))
			vals = append(vals, o)
		}
		return engine.NewArray(vals), nil
	case "TXT":
		txts, err := net.LookupTXT(hostname)
		if err != nil {
			return engine.NewArray(nil), nil
		}
		vals := make([]engine.Value, 0, len(txts))
		for _, t := range txts {
			vals = append(vals, engine.NewArray([]engine.Value{engine.Str(t)}))
		}
		return engine.NewArray(vals), nil
	case "CAA", "NAPTR", "TLSA":
		// Go net 不提供；Node 对无记录返回空数组。
		return engine.NewArray(nil), nil
	case "SOA":
		return engine.NewObject(), nil
	default:
		return engine.NewArray(nil), nil
	}
}

// dnsResolveIP 按 IP 版本返回地址数组。
func dnsResolveIP(hostname string, version int) (engine.Value, error) {
	addrs, err := net.LookupHost(hostname)
	if err != nil {
		return engine.NewArray(nil), nil
	}
	vals := make([]engine.Value, 0, len(addrs))
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil {
			continue
		}
		if version == 4 && ip.To4() != nil {
			vals = append(vals, engine.Str(a))
		} else if version == 6 && ip.To4() == nil {
			vals = append(vals, engine.Str(a))
		}
	}
	return engine.NewArray(vals), nil
}

// dnsResolveAny 返回混合记录对象数组（近似：仅 A/AAAA）。
func dnsResolveAny(hostname string) (engine.Value, error) {
	addrs, err := net.LookupHost(hostname)
	if err != nil {
		return engine.NewArray(nil), nil
	}
	vals := make([]engine.Value, 0, len(addrs))
	for _, a := range addrs {
		o := engine.NewObject()
		_ = o.Set("type", engine.Str("A"))
		_ = o.Set("address", engine.Str(a))
		vals = append(vals, o)
	}
	return engine.NewArray(vals), nil
}

// trimTrailingDot 去掉 FQDN 结尾的 "."（Node 返回不带结尾点的记录）。
func trimTrailingDot(s string) string {
	if len(s) > 0 && s[len(s)-1] == '.' {
		return s[:len(s)-1]
	}
	return s
}

// portServiceName 简化端口→服务名映射。
func portServiceName(port string) string {
	switch port {
	case "80":
		return "http"
	case "443":
		return "https"
	case "21":
		return "ftp"
	case "25":
		return "smtp"
	case "22":
		return "ssh"
	case "53":
		return "domain"
	default:
		return port
	}
}

// dnsErrorCodes 是 node:dns / node:dns/promises 的错误码常量（Node 22 全集）。
// Node 语义：每个常量是 errno 字符串（如 NODATA → 'ENODATA'）。
var dnsErrorCodes = map[string]string{
	"ADDRGETNETWORKPARAMS": "EADDRGETNETWORKPARAMS",
	"BADFAMILY":            "EBADFAMILY",
	"BADFLAGS":             "EBADFLAGS",
	"BADHINTS":             "EBADHINTS",
	"BADNAME":              "EBADNAME",
	"BADQUERY":             "EBADQUERY",
	"BADRESP":              "EBADRESP",
	"BADSTR":               "EBADSTR",
	"CANCELLED":            "ECANCELLED",
	"CONNREFUSED":          "ECONNREFUSED",
	"DESTRUCTION":          "EDESTRUCTION",
	"EOF":                  "EOF",
	"FILE":                 "EFILE",
	"FORMERR":              "EFORMERR",
	"LOADIPHLPAPI":         "ELOADIPHLPAPI",
	"NODATA":               "ENODATA",
	"NOMEM":                "ENOMEM",
	"NONAME":               "ENONAME",
	"NOTFOUND":             "ENOTFOUND",
	"NOTIMP":               "ENOTIMP",
	"NOTINITIALIZED":       "ENOTINITIALIZED",
	"REFUSED":              "EREFUSED",
	"SERVFAIL":             "ESERVFAIL",
	"TIMEOUT":              "ETIMEOUT",
}

// registerDNSConstants 注册 DNS 错误码常量（Node 语义：字符串 errno）。
func registerDNSConstants(m engine.Object) {
	for code, val := range dnsErrorCodes {
		_ = m.Set(code, engine.Str(val))
	}
}

// ipFamily 返回地址的 IP 版本（4/6）。
func ipFamily(addr string) int {
	ip := net.ParseIP(addr)
	if ip == nil {
		return 0
	}
	if ip.To4() != nil {
		return 4
	}
	return 6
}
