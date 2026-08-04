package globals

// Web API：fetch + Request / Response / Headers / FormData（开发计划 3.1）。
//
// 实现要点：
//   - fetch 用 Go net/http 在 goroutine 发请求，经 PostTask 回 JS 线程，
//     resolve 一个 Response 对象（用全局 Promise 构造器）。
//   - Response.body 是 ReadableStream（构造时推入响应体并关闭），
//     text()/json()/arrayBuffer() 基于已缓冲的响应体。
//   - Headers/FormData 用有序键值对列表（保持插入顺序，键名不区分大小写）。

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine"
)

// FetchConfig 配置 fetch 全局（当前无可用选项）。
type FetchConfig struct{}

// NewFetch 注册全局 fetch / Request / Response / Headers / FormData。
func NewFetch(ctx engine.Context, cfg FetchConfig) error {
	_ = ctx.Global().Set("Headers", engine.NewFunction("Headers", func(args []engine.Value) (engine.Value, error) {
		return newHeadersInstance(args), nil
	}))
	_ = ctx.Global().Set("Request", engine.NewFunction("Request", func(args []engine.Value) (engine.Value, error) {
		return newRequestInstance(ctx, args), nil
	}))
	_ = ctx.Global().Set("Response", engine.NewFunction("Response", func(args []engine.Value) (engine.Value, error) {
		return newResponseInstance(ctx, args), nil
	}))
	_ = ctx.Global().Set("FormData", engine.NewFunction("FormData", func(args []engine.Value) (engine.Value, error) {
		return newFormDataInstance(), nil
	}))
	_ = ctx.Global().Set("fetch", engine.NewFunction("fetch", func(args []engine.Value) (engine.Value, error) {
		return doFetch(ctx, args)
	}))
	return nil
}

// --- Headers --------------------------------------------------------------

// hdrPair 是有序头键值对。
type hdrPair struct{ key, val string }

// headerState 是 Headers 实例的内部状态。
type headerState struct {
	pairs []hdrPair
}

// headerIndex 在 pairs 中找第一个匹配 key（不区分大小写）。
func (h *headerState) find(key string) int {
	for i, p := range h.pairs {
		if strings.EqualFold(p.key, key) {
			return i
		}
	}
	return -1
}

// newHeadersInstance 构造 Headers 对象。
func newHeadersInstance(args []engine.Value) engine.Value {
	obj := engine.NewObject()
	state := &headerState{}

	// 初始化。
	if len(args) > 0 && !args[0].IsUndefined() && !args[0].IsNull() {
		init := args[0]
		switch {
		case init.Type() == engine.TypeString:
			for _, part := range strings.Split(init.String(), "\n") {
				if idx := strings.Index(part, ":"); idx > 0 {
					state.pairs = append(state.pairs, hdrPair{
						strings.TrimSpace(part[:idx]), strings.TrimSpace(part[idx+1:]),
					})
				}
			}
		default:
			if a, ok := init.(*engine.ArrayValue); ok {
				for _, e := range a.Elems() {
					if pair, ok := e.(*engine.ArrayValue); ok && len(pair.Elems()) >= 2 {
						state.pairs = append(state.pairs, hdrPair{pair.Elems()[0].String(), pair.Elems()[1].String()})
					}
				}
			} else if o, ok := init.AsObject(); ok {
				for _, k := range o.Keys() {
					if v, err := o.Get(k); err == nil {
						state.pairs = append(state.pairs, hdrPair{k, v.String()})
					}
				}
			}
		}
	}

	_ = obj.Set("get", engine.NewFunction("get", func(a []engine.Value) (engine.Value, error) {
		if len(a) > 0 {
			if i := state.find(a[0].String()); i >= 0 {
				return engine.Str(state.pairs[i].val), nil
			}
		}
		return engine.Null(), nil
	}))
	_ = obj.Set("getSetCookie", engine.NewFunction("getSetCookie", func(a []engine.Value) (engine.Value, error) {
		vals := make([]engine.Value, 0)
		if len(a) > 0 {
			key := a[0].String()
			for _, p := range state.pairs {
				if strings.EqualFold(p.key, key) {
					vals = append(vals, engine.Str(p.val))
				}
			}
		}
		return engine.NewArray(vals), nil
	}))
	_ = obj.Set("set", engine.NewFunction("set", func(a []engine.Value) (engine.Value, error) {
		if len(a) >= 2 {
			key := a[0].String()
			// 删除同名后追加。
			out := state.pairs[:0]
			for _, p := range state.pairs {
				if !strings.EqualFold(p.key, key) {
					out = append(out, p)
				}
			}
			state.pairs = append(out, hdrPair{key, a[1].String()})
		}
		return engine.Undefined(), nil
	}))
	_ = obj.Set("append", engine.NewFunction("append", func(a []engine.Value) (engine.Value, error) {
		if len(a) >= 2 {
			state.pairs = append(state.pairs, hdrPair{a[0].String(), a[1].String()})
		}
		return engine.Undefined(), nil
	}))
	_ = obj.Set("has", engine.NewFunction("has", func(a []engine.Value) (engine.Value, error) {
		if len(a) > 0 && state.find(a[0].String()) >= 0 {
			return engine.Boolean(true), nil
		}
		return engine.Boolean(false), nil
	}))
	_ = obj.Set("delete", engine.NewFunction("delete", func(a []engine.Value) (engine.Value, error) {
		if len(a) > 0 {
			key := a[0].String()
			out := state.pairs[:0]
			for _, p := range state.pairs {
				if !strings.EqualFold(p.key, key) {
					out = append(out, p)
				}
			}
			state.pairs = out
		}
		return engine.Undefined(), nil
	}))
	_ = obj.Set("forEach", engine.NewFunction("forEach", func(a []engine.Value) (engine.Value, error) {
		if len(a) > 0 && a[0].IsFunction() {
			if f, ok := a[0].AsFunction(); ok {
				for _, p := range state.pairs {
					_, _ = f.Call([]engine.Value{engine.Str(p.val), engine.Str(p.key), obj})
				}
			}
		}
		return engine.Undefined(), nil
	}))
	_ = obj.Set("keys", engine.NewFunction("keys", func(a []engine.Value) (engine.Value, error) {
		keys := make([]engine.Value, 0, len(state.pairs))
		for _, p := range state.pairs {
			keys = append(keys, engine.Str(p.key))
		}
		return engine.NewArray(keys), nil
	}))
	_ = obj.Set("values", engine.NewFunction("values", func(a []engine.Value) (engine.Value, error) {
		vals := make([]engine.Value, 0, len(state.pairs))
		for _, p := range state.pairs {
			vals = append(vals, engine.Str(p.val))
		}
		return engine.NewArray(vals), nil
	}))
	_ = obj.Set("entries", engine.NewFunction("entries", func(a []engine.Value) (engine.Value, error) {
		entries := make([]engine.Value, 0, len(state.pairs))
		for _, p := range state.pairs {
			entries = append(entries, engine.NewArray([]engine.Value{engine.Str(p.key), engine.Str(p.val)}))
		}
		return engine.NewArray(entries), nil
	}))
	_ = obj.Set("toString", engine.NewFunction("toString", func(a []engine.Value) (engine.Value, error) {
		var b strings.Builder
		for _, p := range state.pairs {
			b.WriteString(p.key)
			b.WriteString(": ")
			b.WriteString(p.val)
			b.WriteString("\r\n")
		}
		return engine.Str(b.String()), nil
	}))

	return obj
}

// headersToGo 把 Headers 值转成 Go http.Header。
func headersToGo(v engine.Value) http.Header {
	h := make(http.Header)
	if v == nil || v.IsUndefined() || v.IsNull() {
		return h
	}
	if o, ok := v.AsObject(); ok {
		for _, k := range o.Keys() {
			if val, err := o.Get(k); err == nil {
				h.Set(k, val.String())
			}
		}
	}
	return h
}

// --- Request --------------------------------------------------------------

// newRequestInstance 构造 Request 对象。
func newRequestInstance(ctx engine.Context, args []engine.Value) engine.Value {
	req := engine.NewObject()
	urlStr := ""
	method := "GET"
	var headers engine.Value
	bodyStr := ""

	if len(args) > 0 {
		if args[0].Type() == engine.TypeString {
			urlStr = args[0].String()
		} else if o, ok := args[0].AsObject(); ok {
			if v, err := o.Get("url"); err == nil && !v.IsUndefined() {
				urlStr = v.String()
			}
		}
	}
	if len(args) > 1 && args[1].IsObject() {
		if o, ok := args[1].AsObject(); ok {
			if v, err := o.Get("method"); err == nil && !v.IsUndefined() && v.String() != "" {
				method = v.String()
			}
			if v, err := o.Get("headers"); err == nil && !v.IsUndefined() {
				headers = v
			}
			if v, err := o.Get("body"); err == nil && !v.IsUndefined() && !v.IsNull() {
				bodyStr = v.String()
			}
		}
	}

	_ = req.Set("url", engine.Str(urlStr))
	_ = req.Set("method", engine.Str(method))
	_ = req.Set("headers", newHeadersInstance([]engine.Value{headers}))
	if bodyStr != "" {
		_ = req.Set("body", engine.Str(bodyStr))
	} else {
		_ = req.Set("body", engine.Null())
	}
	_ = req.Set("bodyUsed", engine.Boolean(false))

	_ = req.Set("clone", engine.NewFunction("clone", func(a []engine.Value) (engine.Value, error) {
		return newRequestInstance(ctx, []engine.Value{req, engine.NewObject()}), nil
	}))
	return req
}

// --- Response -------------------------------------------------------------

// newResponseInstance 构造 Response 对象（body 为字符串或 undefined）。
func newResponseInstance(ctx engine.Context, args []engine.Value) engine.Value {
	return buildResponse(ctx, args, 0, "", engine.Undefined())
}

// buildResponse 构造 Response：body + status/statusText/headers。
func buildResponse(ctx engine.Context, args []engine.Value, status int, statusText string, headerArg engine.Value) engine.Value {
	res := engine.NewObject()

	bodyStr := ""
	if len(args) > 0 && !args[0].IsUndefined() && !args[0].IsNull() {
		bodyStr = args[0].String()
	}
	initStatus := status
	initStatusText := statusText
	var initHeaders engine.Value
	if len(args) > 1 && args[1].IsObject() {
		if o, ok := args[1].AsObject(); ok {
			if v, err := o.Get("status"); err == nil && !v.IsUndefined() {
				if n, ok := v.Int(); ok {
					initStatus = n
				}
			}
			if v, err := o.Get("statusText"); err == nil && !v.IsUndefined() {
				initStatusText = v.String()
			}
			if v, err := o.Get("headers"); err == nil && !v.IsUndefined() {
				initHeaders = v
			}
		}
	}
	if initStatus == 0 {
		initStatus = 200
	}
	if initStatusText == "" {
		initStatusText = http.StatusText(initStatus)
	}
	// headerArg 仅在显式传入时覆盖（Undefined 表示未提供）。
	if headerArg != nil && !headerArg.IsUndefined() && !headerArg.IsNull() {
		initHeaders = headerArg
	}

	_ = res.Set("status", engine.IntValue(initStatus))
	_ = res.Set("statusText", engine.Str(initStatusText))
	_ = res.Set("ok", engine.Boolean(initStatus >= 200 && initStatus < 300))
	_ = res.Set("headers", newHeadersInstance([]engine.Value{initHeaders}))
	_ = res.Set("bodyUsed", engine.Boolean(false))
	_ = res.Set("url", engine.Str(""))

	// body 属性：ReadableStream（推入 body 后关闭）。
	if bodyStr != "" {
		bodyStream, _ := newReadableStream(ctx, []engine.Value{engine.NewObjectFrom(map[string]engine.Value{
			"start": engine.NewFunction("start", func(a []engine.Value) (engine.Value, error) {
				if len(a) > 0 {
					if c, ok := a[0].AsObject(); ok {
						if e, err := c.Get("enqueue"); err == nil && e.IsFunction() {
							if f, ok := e.AsFunction(); ok {
								_, _ = f.Call([]engine.Value{engine.Str(bodyStr)})
							}
						}
						if cl, err := c.Get("close"); err == nil && cl.IsFunction() {
							if f, ok := cl.AsFunction(); ok {
								_, _ = f.Call(nil)
							}
						}
					}
				}
				return engine.Undefined(), nil
			}),
		})})
		_ = res.Set("body", bodyStream)
	} else {
		_ = res.Set("body", engine.Null())
	}

	// text() → Promise<string>
	_ = res.Set("text", engine.NewFunction("text", func(a []engine.Value) (engine.Value, error) {
		_ = res.Set("bodyUsed", engine.Boolean(true))
		return promiseResolveValue(ctx, engine.Str(bodyStr))
	}))
	// arrayBuffer() → Promise<Buffer>
	_ = res.Set("arrayBuffer", engine.NewFunction("arrayBuffer", func(a []engine.Value) (engine.Value, error) {
		_ = res.Set("bodyUsed", engine.Boolean(true))
		return promiseResolveValue(ctx, NewBufferInstance([]byte(bodyStr)))
	}))
	// json() → Promise<parsed>
	_ = res.Set("json", engine.NewFunction("json", func(a []engine.Value) (engine.Value, error) {
		_ = res.Set("bodyUsed", engine.Boolean(true))
		jsonGlobal, err := ctx.Global().Get("JSON")
		if err != nil || !jsonGlobal.IsObject() {
			return promiseRejectValue(ctx, "JSON not available")
		}
		jo, _ := jsonGlobal.AsObject()
		parseFn, err := jo.Get("parse")
		if err != nil || !parseFn.IsFunction() {
			return promiseRejectValue(ctx, "JSON.parse not available")
		}
		if f, ok := parseFn.AsFunction(); ok {
			parsed, perr := f.Call([]engine.Value{engine.Str(bodyStr)})
			if perr != nil {
				return promiseRejectValue(ctx, perr.Error())
			}
			return promiseResolveValue(ctx, parsed)
		}
		return promiseRejectValue(ctx, "JSON.parse failed")
	}))
	_ = res.Set("clone", engine.NewFunction("clone", func(a []engine.Value) (engine.Value, error) {
		return buildResponse(ctx, []engine.Value{engine.Str(bodyStr)}, initStatus, initStatusText, initHeaders), nil
	}))

	return res
}

// --- FormData -------------------------------------------------------------

// fdEntry 是有序表单字段。
type fdEntry struct{ key, val string }

// newFormDataInstance 构造 FormData 对象。
func newFormDataInstance() engine.Value {
	fd := engine.NewObject()
	var entries []fdEntry

	_ = fd.Set("append", engine.NewFunction("append", func(a []engine.Value) (engine.Value, error) {
		if len(a) >= 2 {
			entries = append(entries, fdEntry{a[0].String(), a[1].String()})
		}
		return engine.Undefined(), nil
	}))
	_ = fd.Set("get", engine.NewFunction("get", func(a []engine.Value) (engine.Value, error) {
		if len(a) > 0 {
			for _, e := range entries {
				if e.key == a[0].String() {
					return engine.Str(e.val), nil
				}
			}
		}
		return engine.Null(), nil
	}))
	_ = fd.Set("getAll", engine.NewFunction("getAll", func(a []engine.Value) (engine.Value, error) {
		vals := make([]engine.Value, 0)
		if len(a) > 0 {
			for _, e := range entries {
				if e.key == a[0].String() {
					vals = append(vals, engine.Str(e.val))
				}
			}
		}
		return engine.NewArray(vals), nil
	}))
	_ = fd.Set("set", engine.NewFunction("set", func(a []engine.Value) (engine.Value, error) {
		if len(a) >= 2 {
			key := a[0].String()
			out := entries[:0]
			for _, e := range entries {
				if e.key != key {
					out = append(out, e)
				}
			}
			entries = append(out, fdEntry{key, a[1].String()})
		}
		return engine.Undefined(), nil
	}))
	_ = fd.Set("has", engine.NewFunction("has", func(a []engine.Value) (engine.Value, error) {
		if len(a) > 0 {
			for _, e := range entries {
				if e.key == a[0].String() {
					return engine.Boolean(true), nil
				}
			}
		}
		return engine.Boolean(false), nil
	}))
	_ = fd.Set("delete", engine.NewFunction("delete", func(a []engine.Value) (engine.Value, error) {
		if len(a) > 0 {
			key := a[0].String()
			out := entries[:0]
			for _, e := range entries {
				if e.key != key {
					out = append(out, e)
				}
			}
			entries = out
		}
		return engine.Undefined(), nil
	}))
	_ = fd.Set("entries", engine.NewFunction("entries", func(a []engine.Value) (engine.Value, error) {
		out := make([]engine.Value, 0, len(entries))
		for _, e := range entries {
			out = append(out, engine.NewArray([]engine.Value{engine.Str(e.key), engine.Str(e.val)}))
		}
		return engine.NewArray(out), nil
	}))

	return fd
}

// --- fetch ----------------------------------------------------------------

// doFetch 实现全局 fetch。
func doFetch(ctx engine.Context, args []engine.Value) (engine.Value, error) {
	if len(args) == 0 {
		return engine.Undefined(), fmt.Errorf("fetch: missing URL")
	}
	urlStr := ""
	method := "GET"
	var headersInit engine.Value
	bodyStr := ""

	// input：字符串或 Request。
	if args[0].Type() == engine.TypeString {
		urlStr = args[0].String()
	} else if o, ok := args[0].AsObject(); ok {
		if v, err := o.Get("url"); err == nil && !v.IsUndefined() {
			urlStr = v.String()
		}
		if v, err := o.Get("method"); err == nil && !v.IsUndefined() {
			method = v.String()
		}
		if v, err := o.Get("headers"); err == nil && !v.IsUndefined() {
			headersInit = v
		}
	}

	// init。
	if len(args) > 1 && args[1].IsObject() {
		if o, ok := args[1].AsObject(); ok {
			if v, err := o.Get("method"); err == nil && !v.IsUndefined() && v.String() != "" {
				method = v.String()
			}
			if v, err := o.Get("headers"); err == nil && !v.IsUndefined() {
				headersInit = v
			}
			if v, err := o.Get("body"); err == nil && !v.IsUndefined() && !v.IsNull() {
				bodyStr = v.String()
			}
		}
	}

	executor := engine.NewFunction("executor", func(eargs []engine.Value) (engine.Value, error) {
		if len(eargs) < 2 {
			return engine.Undefined(), nil
		}
		resolve, reject := eargs[0], eargs[1]
		release := ctx.AddRef()
		go func() {
			var bodyReader io.Reader
			if bodyStr != "" {
				bodyReader = strings.NewReader(bodyStr)
			}
			req, err := http.NewRequest(method, urlStr, bodyReader)
			if err != nil {
				ctx.PostTask(func() {
					defer release()
					callResolve(reject, engine.Str("fetch: "+err.Error()))
				})
				return
			}
			h := headersToGo(headersInit)
			req.Header = h
			client := &http.Client{}
			resp, err := client.Do(req)
			if err != nil {
				ctx.PostTask(func() {
					defer release()
					callResolve(reject, engine.Str("fetch: "+err.Error()))
				})
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)

			ctx.PostTask(func() {
				defer release()
				// 用真实响应体构造 Response（text()/json() 闭包捕获 body）。
				res := buildResponse(ctx, []engine.Value{engine.Str(string(body))}, resp.StatusCode, resp.Status, httpHeaderToEngine(resp.Header))
				if ro, ok := res.AsObject(); ok {
					_ = ro.Set("url", engine.Str(urlStr))
				}
				callResolve(resolve, res)
			})
		}()
		return engine.Undefined(), nil
	})
	return newPromise(ctx, executor)
}

// httpHeaderToEngine 把 Go http.Header 转成 JS 对象（fetch 响应头）。
func httpHeaderToEngine(h http.Header) engine.Value {
	obj := engine.NewObject()
	for k, vals := range h {
		if len(vals) > 0 {
			_ = obj.Set(k, engine.Str(strings.Join(vals, ", ")))
		}
	}
	return obj
}

// responseBodyStream 构造包装响应体的 ReadableStream。
func responseBodyStream(ctx engine.Context, bodyStr string) engine.Value {
	stream, _ := newReadableStream(ctx, []engine.Value{engine.NewObjectFrom(map[string]engine.Value{
		"start": engine.NewFunction("start", func(a []engine.Value) (engine.Value, error) {
			if len(a) > 0 {
				if c, ok := a[0].AsObject(); ok {
					if e, err := c.Get("enqueue"); err == nil && e.IsFunction() {
						if f, ok := e.AsFunction(); ok {
							_, _ = f.Call([]engine.Value{engine.Str(bodyStr)})
						}
					}
					if cl, err := c.Get("close"); err == nil && cl.IsFunction() {
						if f, ok := cl.AsFunction(); ok {
							_, _ = f.Call(nil)
						}
					}
				}
			}
			return engine.Undefined(), nil
		}),
	})})
	return stream
}
