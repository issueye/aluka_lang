package globals

// Aluka.peek / deepEquals / deepAssign / which / escapeHTML / isTerminal /
// dns.lookup（Phase 4 WBS 4.13 / 4.16）。

import (
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// alukaRegisterUtil 注册工具函数。
func alukaRegisterUtil(ctx engine.Context, aluka engine.Value) {
	ao, _ := aluka.AsObject()

	// peek(value)：Promise 已定值则返回其值；rejected 返回 promise 自身；
	// 未定值或非 promise 时返回 undefined / 原值。
	_ = ao.Set("peek", engine.NewFunction("peek", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		v := args[0]
		if pv, ok := v.(interface {
			State() int
			Result() engine.Value
		}); ok {
			switch pv.State() {
			case 1: // fulfilled
				return pv.Result(), nil
			case 2: // rejected
				return v, nil
			default:
				return engine.Undefined(), nil
			}
		}
		return v, nil
	}))

	// deepEquals(a, b)：深比较。
	_ = ao.Set("deepEquals", engine.NewFunction("deepEquals", func(args []engine.Value) (engine.Value, error) {
		var a, b engine.Value
		if len(args) > 0 {
			a = args[0]
		}
		if len(args) > 1 {
			b = args[1]
		}
		return engine.Boolean(alukaDeepEquals(a, b, 0)), nil
	}))

	// deepAssign(target, ...sources)：深合并。
	_ = ao.Set("deepAssign", engine.NewFunction("deepAssign", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		target := args[0]
		alukaDeepAssign(ctx, target, args[1:])
		return target, nil
	}))

	// which(cmd)：在 PATH 中查找命令，找不到返回 null。
	_ = ao.Set("which", engine.NewFunction("which", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Null(), nil
		}
		path, err := exec.LookPath(args[0].String())
		if err != nil {
			return engine.Null(), nil
		}
		return engine.Str(path), nil
	}))

	// escapeHTML(str)：转义 HTML 特殊字符。
	_ = ao.Set("escapeHTML", engine.NewFunction("escapeHTML", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		return engine.Str(alukaEscapeHTML(args[0].String())), nil
	}))

	// isTerminal(fd?)：判断 fd（默认 1）是否为终端。
	_ = ao.Set("isTerminal", engine.NewFunction("isTerminal", func(args []engine.Value) (engine.Value, error) {
		fd := 1
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				fd = n
			}
		}
		return engine.Boolean(alukaIsTerminal(fd)), nil
	}))

	// dns.lookup(host) → Promise<string>（第一个解析地址）。
	dnsObj := engine.NewObject()
	_ = dnsObj.Set("lookup", engine.NewFunction("lookup", func(args []engine.Value) (engine.Value, error) {
		host := ""
		if len(args) > 0 {
			host = args[0].String()
		}
		executor := engine.NewFunction("executor", func(ea []engine.Value) (engine.Value, error) {
			if len(ea) < 2 {
				return engine.Undefined(), nil
			}
			resolve, reject := ea[0], ea[1]
			release := ctx.AddRef()
			go func() {
				addrs, err := net.LookupHost(host)
				ctx.PostTask(func() {
					defer release()
					if err != nil {
						callResolve(reject, engine.Str("Aluka.dns.lookup: "+err.Error()))
						return
					}
					if len(addrs) == 0 {
						callResolve(reject, engine.Str("Aluka.dns.lookup: no addresses"))
						return
					}
					callResolve(resolve, engine.Str(addrs[0]))
				})
			}()
			return engine.Undefined(), nil
		})
		return newPromise(ctx, executor)
	}))
	_ = ao.Set("dns", dnsObj)
}

// alukaDeepEquals 深比较两个值。
func alukaDeepEquals(a, b engine.Value, depth int) bool {
	if depth > 64 {
		return a == b
	}
	aNil, bNil := a == nil, b == nil
	if aNil || bNil {
		return aNil && bNil
	}
	// undefined / null。
	if a.IsUndefined() || a.IsNull() || b.IsUndefined() || b.IsNull() {
		return (a.IsUndefined() && b.IsUndefined()) || (a.IsNull() && b.IsNull())
	}
	// 基本类型（非对象）。
	if !a.IsObject() || !b.IsObject() {
		if a.IsObject() != b.IsObject() {
			return false
		}
		switch a.Type() {
		case engine.TypeNumber, engine.TypeBoolean:
			af, _ := a.Float()
			bf, _ := b.Float()
			return af == bf
		case engine.TypeString:
			return a.String() == b.String()
		default:
			return a == b
		}
	}
	// 数组。
	aa, aIsArr := a.(*engine.ArrayValue)
	bb, bIsArr := b.(*engine.ArrayValue)
	if aIsArr || bIsArr {
		if !aIsArr || !bIsArr {
			return false
		}
		ae, be := aa.Elems(), bb.Elems()
		if len(ae) != len(be) {
			return false
		}
		for i := range ae {
			if !alukaDeepEquals(ae[i], be[i], depth+1) {
				return false
			}
		}
		return true
	}
	ao, _ := a.AsObject()
	bo, _ := b.AsObject()
	if ao == nil || bo == nil {
		return false
	}
	ak, bk := ao.Keys(), bo.Keys()
	if len(ak) != len(bk) {
		return false
	}
	bkSet := make(map[string]bool, len(bk))
	for _, k := range bk {
		bkSet[k] = true
	}
	for _, k := range ak {
		if !bkSet[k] {
			return false
		}
		av, _ := ao.Get(k)
		bv, _ := bo.Get(k)
		if !alukaDeepEquals(av, bv, depth+1) {
			return false
		}
	}
	return true
}

// alukaDeepAssign 深合并 sources 到 target（对象递归、数组/基本类型覆盖）。
func alukaDeepAssign(ctx engine.Context, target engine.Value, sources []engine.Value) {
	if target == nil {
		return
	}
	to, ok := target.AsObject()
	if !ok {
		return
	}
	for _, src := range sources {
		if src == nil || !src.IsObject() {
			continue
		}
		so, ok := src.AsObject()
		if !ok {
			continue
		}
		for _, k := range so.Keys() {
			v, _ := so.Get(k)
			if v != nil && v.IsObject() {
				if _, isArr := v.(*engine.ArrayValue); isArr {
					_ = to.Set(k, alukaDeepClone(v, 0))
					continue
				}
				// 对象：target 已有同名对象则递归合并。
				if existing, err := to.Get(k); err == nil && existing != nil && existing.IsObject() {
					if _, isArr := existing.(*engine.ArrayValue); !isArr {
						alukaDeepAssign(ctx, existing, []engine.Value{v})
						continue
					}
				}
				_ = to.Set(k, alukaDeepClone(v, 0))
			} else {
				_ = to.Set(k, v)
			}
		}
	}
}

// alukaDeepClone 深拷贝对象/数组。
func alukaDeepClone(v engine.Value, depth int) engine.Value {
	if depth > 64 || v == nil {
		return v
	}
	if a, ok := v.(*engine.ArrayValue); ok {
		elems := make([]engine.Value, 0, len(a.Elems()))
		for _, e := range a.Elems() {
			elems = append(elems, alukaDeepClone(e, depth+1))
		}
		return engine.NewArray(elems)
	}
	if o, ok := v.AsObject(); ok {
		clone := engine.NewObject()
		for _, k := range o.Keys() {
			if iv, err := o.Get(k); err == nil {
				_ = clone.Set(k, alukaDeepClone(iv, depth+1))
			}
		}
		return clone
	}
	return v
}

// alukaEscapeHTML 转义 HTML 特殊字符（& 先转）。
func alukaEscapeHTML(s string) string {
	var sb strings.Builder
	sb.Grow(len(s) + 32)
	for _, r := range s {
		switch r {
		case '&':
			sb.WriteString("&amp;")
		case '<':
			sb.WriteString("&lt;")
		case '>':
			sb.WriteString("&gt;")
		case '"':
			sb.WriteString("&quot;")
		case '\'':
			sb.WriteString("&#39;")
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// alukaIsTerminal 判断 fd 是否指向终端（字符设备）。
func alukaIsTerminal(fd int) bool {
	var f *os.File
	switch fd {
	case 0:
		f = os.Stdin
	case 1:
		f = os.Stdout
	case 2:
		f = os.Stderr
	default:
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
