package globals

// Web API：fetch + Request / Response / Headers / FormData（开发计划 3.1）。
//
// 实现要点：
//   - fetch 用 Go net/http 在 goroutine 发请求，经 PostTask 回 JS 线程，
//     在响应头到达后 resolve 一个 Response 对象（用全局 Promise 构造器）。
//   - fetch Response.body 是实时 ReadableStream，网络数据按读取批次入队；
//     text()/json()/arrayBuffer() 等待流结束后聚合响应体。
//   - Headers/FormData 用有序键值对列表（保持插入顺序，键名不区分大小写）。

import (
	"fmt"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"io"
	"net/http"
	"sort"
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
	respCtor, _ := ctx.Global().Get("Response")
	if f, ok := respCtor.AsFunction(); ok {
		fo, _ := f.AsObject()
		// Response.json(data, init?)：JSON.stringify + Content-Type: application/json。
		_ = fo.Set("json", engine.NewFunction("json", func(a []engine.Value) (engine.Value, error) {
			var body engine.Value = engine.Str("")
			if len(a) > 0 && !a[0].IsUndefined() {
				jsonGlobal, err := ctx.Global().Get("JSON")
				if err != nil || !jsonGlobal.IsObject() {
					return engine.Undefined(), fmt.Errorf("Response.json: JSON not available")
				}
				jo, _ := jsonGlobal.AsObject()
				sf, err := jo.Get("stringify")
				if err != nil || !sf.IsFunction() {
					return engine.Undefined(), fmt.Errorf("Response.json: JSON.stringify not available")
				}
				if sf, ok := sf.AsFunction(); ok {
					v, verr := sf.Call([]engine.Value{a[0]})
					if verr != nil {
						return engine.Undefined(), verr
					}
					body = v
				}
			}
			var init engine.Value = engine.Undefined()
			if len(a) > 1 {
				init = a[1]
			}
			res := buildResponse(ctx, []engine.Value{body, init}, 0, "", engine.Undefined(), false)
			// 未显式提供 Content-Type 时补默认值。
			if ro, ok := res.AsObject(); ok {
				if h, ok := ro.Get("headers"); ok == nil {
					if ho, ok := h.AsObject(); ok {
						hasCT := false
						if hf, err := ho.Get("has"); err == nil && hf.IsFunction() {
							if hfn, ok := hf.AsFunction(); ok {
								if r, err := hfn.Call([]engine.Value{engine.Str("Content-Type")}); err == nil {
									if b, ok := r.Bool(); ok {
										hasCT = b
									}
								}
							}
						}
						if !hasCT {
							if sf, err := ho.Get("set"); err == nil && sf.IsFunction() {
								if sfn, ok := sf.AsFunction(); ok {
									if _, err := sfn.Call([]engine.Value{engine.Str("Content-Type"), engine.Str("application/json")}); err != nil {
										interpreter.ReportUncaught(nil, err)
									}
								}
							}
						}
					}
				}
			}
			return res, nil
		}))
		// Response.redirect(url, status?)：302 Location 响应。
		_ = fo.Set("redirect", engine.NewFunction("redirect", func(a []engine.Value) (engine.Value, error) {
			status := 302
			if len(a) > 1 {
				if n, ok := a[1].Int(); ok {
					status = n
				}
			}
			url := ""
			if len(a) > 0 {
				url = a[0].String()
			}
			return buildResponse(ctx, []engine.Value{
				engine.Null(),
				engine.NewObjectFrom(map[string]engine.Value{
					"status":  engine.IntValue(status),
					"headers": engine.NewObjectFrom(map[string]engine.Value{"Location": engine.Str(url)}),
				}),
			}, 0, "", engine.Undefined(), true), nil
		}))
		// Response.error()：status 0、ok false 的异常响应。
		_ = fo.Set("error", engine.NewFunction("error", func(a []engine.Value) (engine.Value, error) {
			return buildResponse(ctx, []engine.Value{engine.Null(), engine.NewObjectFrom(map[string]engine.Value{
				"status": engine.IntValue(0),
			})}, 0, "", engine.Undefined(), true), nil
		}))
	}
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

// merged 返回按名称去重合并后的键值对：同名（不区分大小写）的值以 ", "
// 连接（WHATWG Headers 语义），保持首次出现顺序。
func (h *headerState) merged() []hdrPair {
	var out []hdrPair
	for _, p := range h.pairs {
		found := false
		for i := range out {
			if strings.EqualFold(out[i].key, p.key) {
				out[i].val += ", " + p.val
				found = true
				break
			}
		}
		if !found {
			out = append(out, p)
		}
	}
	return out
}

// sortedMerged 返回 merged 结果按名称字典序排序后的序列（迭代语义：
// keys/values/entries/forEach/iterator 均按排序后的名称产出，WHATWG 规范）。
func (h *headerState) sortedMerged() []hdrPair {
	out := h.merged()
	sort.SliceStable(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
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
						strings.ToLower(strings.TrimSpace(part[:idx])), strings.TrimSpace(part[idx+1:]),
					})
				}
			}
		default:
			if a, ok := init.(*engine.ArrayValue); ok {
				for _, e := range a.Elems() {
					if pair, ok := e.(*engine.ArrayValue); ok && len(pair.Elems()) >= 2 {
						state.pairs = append(state.pairs, hdrPair{strings.ToLower(pair.Elems()[0].String()), pair.Elems()[1].String()})
					}
				}
			} else if o, ok := init.AsObject(); ok {
				for _, k := range o.Keys() {
					if v, err := o.Get(k); err == nil {
						state.pairs = append(state.pairs, hdrPair{strings.ToLower(k), v.String()})
					}
				}
			}
		}
	}

	_ = obj.Set("get", engine.NewFunction("get", func(a []engine.Value) (engine.Value, error) {
		if len(a) > 0 {
			key := a[0].String()
			// 同名值以 ", " 合并（WHATWG 语义）。
			var vals []string
			for _, p := range state.pairs {
				if strings.EqualFold(p.key, key) {
					vals = append(vals, p.val)
				}
			}
			if len(vals) > 0 {
				return engine.Str(strings.Join(vals, ", ")), nil
			}
		}
		return engine.Null(), nil
	}))
	_ = obj.Set("getSetCookie", engine.NewFunction("getSetCookie", func(a []engine.Value) (engine.Value, error) {
		// Node 语义：仅返回 Set-Cookie 的原始值序列（忽略 name 参数）。
		vals := make([]engine.Value, 0)
		for _, p := range state.pairs {
			if strings.EqualFold(p.key, "set-cookie") {
				vals = append(vals, engine.Str(p.val))
			}
		}
		return engine.NewArray(vals), nil
	}))
	_ = obj.Set("set", engine.NewFunction("set", func(a []engine.Value) (engine.Value, error) {
		if len(a) >= 2 {
			key := strings.ToLower(a[0].String())
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
			state.pairs = append(state.pairs, hdrPair{strings.ToLower(a[0].String()), a[1].String()})
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
				for _, p := range state.sortedMerged() {
					if _, err := f.Call([]engine.Value{engine.Str(p.val), engine.Str(p.key), obj}); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				}
			}
		}
		return engine.Undefined(), nil
	}))
	// 内部属性：全部键值对（供 Go 侧 Aluka.serve 写响应头用）。
	{
		pairsArr := make([]engine.Value, 0, len(state.pairs))
		for _, p := range state.pairs {
			pairsArr = append(pairsArr, engine.NewArray([]engine.Value{engine.Str(p.key), engine.Str(p.val)}))
		}
		_ = obj.Set("_pairs", engine.NewArray(pairsArr))
	}
	_ = obj.Set("keys", engine.NewFunction("keys", func(a []engine.Value) (engine.Value, error) {
		keys := make([]engine.Value, 0)
		for _, p := range state.sortedMerged() {
			keys = append(keys, engine.Str(p.key))
		}
		return engine.NewArray(keys), nil
	}))
	_ = obj.Set("values", engine.NewFunction("values", func(a []engine.Value) (engine.Value, error) {
		vals := make([]engine.Value, 0)
		for _, p := range state.sortedMerged() {
			vals = append(vals, engine.Str(p.val))
		}
		return engine.NewArray(vals), nil
	}))
	_ = obj.Set("entries", engine.NewFunction("entries", func(a []engine.Value) (engine.Value, error) {
		entries := make([]engine.Value, 0)
		for _, p := range state.sortedMerged() {
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
	// [Symbol.iterator]()：产出 [name, value] 对（按名称排序，WHATWG 语义）。
	_ = obj.Set(engine.SymbolIterator.SymbolKey(), engine.NewFunction("[Symbol.iterator]", func(a []engine.Value) (engine.Value, error) {
		iterObj := engine.NewObject()
		idx := 0
		merged := state.sortedMerged()
		next := engine.NewFunction("next", func(na []engine.Value) (engine.Value, error) {
			result := engine.NewObject()
			if idx >= len(merged) {
				_ = result.Set("value", engine.Undefined())
				_ = result.Set("done", engine.Boolean(true))
				return result, nil
			}
			p := merged[idx]
			idx++
			_ = result.Set("value", engine.NewArray([]engine.Value{engine.Str(p.key), engine.Str(p.val)}))
			_ = result.Set("done", engine.Boolean(false))
			return result, nil
		})
		_ = iterObj.Set("next", next)
		_ = iterObj.Set(engine.SymbolIterator.SymbolKey(), engine.NewFunction("[Symbol.iterator]", func(na []engine.Value) (engine.Value, error) {
			return iterObj, nil
		}))
		return iterObj, nil
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
		if forEach, err := o.Get("forEach"); err == nil && forEach.IsFunction() {
			visited := false
			collector := engine.NewFunction("", func(args []engine.Value) (engine.Value, error) {
				if len(args) >= 2 {
					h.Add(args[1].String(), args[0].String())
					visited = true
				}
				return engine.Undefined(), nil
			})
			if f, ok := forEach.AsFunction(); ok {
				if _, err := f.Call([]engine.Value{collector}); err == nil && visited {
					return h
				}
			}
		}
		for _, k := range o.Keys() {
			if val, err := o.Get(k); err == nil {
				if strings.HasPrefix(k, "\x00symbol:") || val.IsFunction() {
					continue
				}
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
	hdrs := headers
	if hdrs == nil {
		hdrs = engine.Undefined()
	}
	_ = req.Set("headers", newHeadersInstance([]engine.Value{hdrs}))
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
	return buildResponse(ctx, args, 0, "", engine.Undefined(), false)
}

// buildResponse 构造 Response：body + status/statusText/headers。
func buildResponse(ctx engine.Context, args []engine.Value, status int, statusText string, headerArg engine.Value, statusSet bool) engine.Value {
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
	if initStatus == 0 && !statusSet {
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
	hdrs := initHeaders
	if hdrs == nil {
		hdrs = engine.Undefined()
	}
	_ = res.Set("headers", newHeadersInstance([]engine.Value{hdrs}))
	_ = res.Set("bodyUsed", engine.Boolean(false))
	_ = res.Set("url", engine.Str(""))
	// 内部同步 body（供 Go 侧 Aluka.serve 等直接读取，避免 Promise this 绑定问题）。
	_ = res.Set("_body", engine.Str(bodyStr))

	// body 属性：ReadableStream（推入 body 后关闭）。
	if bodyStr != "" {
		bodyStream, _ := newReadableStream(ctx, []engine.Value{engine.NewObjectFrom(map[string]engine.Value{
			"start": engine.NewFunction("start", func(a []engine.Value) (engine.Value, error) {
				if len(a) > 0 {
					if c, ok := a[0].AsObject(); ok {
						if e, err := c.Get("enqueue"); err == nil && e.IsFunction() {
							if f, ok := e.AsFunction(); ok {
								if _, err := f.Call([]engine.Value{NewBufferInstance([]byte(bodyStr))}); err != nil {
									interpreter.ReportUncaught(nil, err)
								}
							}
						}
						if cl, err := c.Get("close"); err == nil && cl.IsFunction() {
							if f, ok := cl.AsFunction(); ok {
								if _, err := f.Call(nil); err != nil {
									interpreter.ReportUncaught(nil, err)
								}
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
		return buildResponse(ctx, []engine.Value{engine.Str(bodyStr)}, initStatus, initStatusText, initHeaders, true), nil
	}))

	return res
}

// fetchBodyWaiter 保存等待完整响应体的 text/json/arrayBuffer Promise。
type fetchBodyWaiter struct {
	kind    string
	resolve engine.Value
	reject  engine.Value
}

// fetchBodyState 在 JS 事件循环线程中接收网络分块，同时为完整正文方法缓存数据。
type fetchBodyState struct {
	ctx     engine.Context
	res     engine.Object
	stream  engine.Object
	data    []byte
	done    bool
	err     string
	used    bool
	waiters []fetchBodyWaiter
}

func newStreamingFetchResponse(ctx engine.Context, status int, statusText string, headers engine.Value, url string) (engine.Value, *fetchBodyState) {
	resValue := buildResponse(ctx, []engine.Value{engine.Null()}, status, statusText, headers, true)
	res, _ := resValue.AsObject()
	streamValue, _ := newReadableStream(ctx, nil)
	stream, _ := streamValue.AsObject()
	body := &fetchBodyState{ctx: ctx, res: res, stream: stream}

	_ = res.Set("url", engine.Str(url))
	_ = res.Set("body", streamValue)
	_ = res.Set("text", engine.NewFunction("text", func(a []engine.Value) (engine.Value, error) {
		return body.consume("text")
	}))
	_ = res.Set("arrayBuffer", engine.NewFunction("arrayBuffer", func(a []engine.Value) (engine.Value, error) {
		return body.consume("arrayBuffer")
	}))
	_ = res.Set("json", engine.NewFunction("json", func(a []engine.Value) (engine.Value, error) {
		return body.consume("json")
	}))
	_ = res.Set("clone", engine.NewFunction("clone", func(a []engine.Value) (engine.Value, error) {
		if body.used {
			return engine.Undefined(), fmt.Errorf("Response.clone: body has already been consumed")
		}
		if !body.done {
			return engine.Undefined(), fmt.Errorf("Response.clone: streaming body is not complete")
		}
		return buildResponse(ctx, []engine.Value{engine.Str(string(body.data))}, status, statusText, headers, true), nil
	}))

	// Reading the exposed stream also marks the owning Response body as used.
	if getReader, err := stream.Get("getReader"); err == nil && getReader.IsFunction() {
		_ = stream.Set("getReader", engine.NewFunction("getReader", func(a []engine.Value) (engine.Value, error) {
			body.markUsed()
			if f, ok := getReader.AsFunction(); ok {
				return f.Call(a)
			}
			return engine.Undefined(), nil
		}))
	}
	if asyncIterator, err := stream.Get(engine.SymbolAsyncIterator.SymbolKey()); err == nil && asyncIterator.IsFunction() {
		_ = stream.Set(engine.SymbolAsyncIterator.SymbolKey(), engine.NewFunction("[Symbol.asyncIterator]", func(a []engine.Value) (engine.Value, error) {
			body.markUsed()
			if f, ok := asyncIterator.AsFunction(); ok {
				return f.Call(a)
			}
			return engine.Undefined(), nil
		}))
	}

	return resValue, body
}

func (b *fetchBodyState) markUsed() bool {
	if b.used {
		return false
	}
	b.used = true
	_ = b.res.Set("bodyUsed", engine.Boolean(true))
	return true
}

func (b *fetchBodyState) consume(kind string) (engine.Value, error) {
	if !b.markUsed() {
		return promiseRejectValue(b.ctx, "Body has already been consumed")
	}
	return newPromise(b.ctx, engine.NewFunction("executor", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), nil
		}
		waiter := fetchBodyWaiter{kind: kind, resolve: args[0], reject: args[1]}
		if b.done {
			b.settle(waiter)
		} else {
			b.waiters = append(b.waiters, waiter)
		}
		return engine.Undefined(), nil
	}))
}

func (b *fetchBodyState) append(chunk []byte) {
	if b.done || len(chunk) == 0 {
		return
	}
	b.data = append(b.data, chunk...)
	_ = b.res.Set("_body", engine.Str(string(b.data)))
	if enqueue, err := b.stream.Get("enqueue"); err == nil && enqueue.IsFunction() {
		if f, ok := enqueue.AsFunction(); ok {
			if _, err := f.Call([]engine.Value{NewBufferInstance(chunk)}); err != nil {
				interpreter.ReportUncaught(nil, err)
			}
		}
	}
}

func (b *fetchBodyState) finish(err error) {
	if b.done {
		return
	}
	b.done = true
	if err != nil {
		b.err = "fetch: " + err.Error()
	}
	if closeFn, getErr := b.stream.Get("close"); getErr == nil && closeFn.IsFunction() {
		if f, ok := closeFn.AsFunction(); ok {
			if _, err := f.Call(nil); err != nil {
				interpreter.ReportUncaught(nil, err)
			}
		}
	}
	waiters := b.waiters
	b.waiters = nil
	for _, waiter := range waiters {
		b.settle(waiter)
	}
}

func (b *fetchBodyState) settle(waiter fetchBodyWaiter) {
	if b.err != "" {
		callReject(waiter.reject, b.err)
		return
	}
	switch waiter.kind {
	case "arrayBuffer":
		callResolve(waiter.resolve, NewBufferInstance(append([]byte(nil), b.data...)))
	case "json":
		jsonGlobal, err := b.ctx.Global().Get("JSON")
		if err != nil || !jsonGlobal.IsObject() {
			callReject(waiter.reject, "JSON not available")
			return
		}
		jsonObject, _ := jsonGlobal.AsObject()
		parse, err := jsonObject.Get("parse")
		if err != nil || !parse.IsFunction() {
			callReject(waiter.reject, "JSON.parse not available")
			return
		}
		if f, ok := parse.AsFunction(); ok {
			parsed, parseErr := f.Call([]engine.Value{engine.Str(string(b.data))})
			if parseErr != nil {
				callReject(waiter.reject, parseErr.Error())
				return
			}
			callResolve(waiter.resolve, parsed)
			return
		}
		callReject(waiter.reject, "JSON.parse failed")
	default:
		callResolve(waiter.resolve, engine.Str(string(b.data)))
	}
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
	_ = fd.Set("keys", engine.NewFunction("keys", func(a []engine.Value) (engine.Value, error) {
		out := make([]engine.Value, 0, len(entries))
		for _, e := range entries {
			out = append(out, engine.Str(e.key))
		}
		return engine.NewArray(out), nil
	}))
	_ = fd.Set("values", engine.NewFunction("values", func(a []engine.Value) (engine.Value, error) {
		out := make([]engine.Value, 0, len(entries))
		for _, e := range entries {
			out = append(out, engine.Str(e.val))
		}
		return engine.NewArray(out), nil
	}))
	// [Symbol.iterator]()：产出 [key, value] 对。
	_ = fd.Set(engine.SymbolIterator.SymbolKey(), engine.NewFunction("[Symbol.iterator]", func(a []engine.Value) (engine.Value, error) {
		iterObj := engine.NewObject()
		idx := 0
		next := engine.NewFunction("next", func(na []engine.Value) (engine.Value, error) {
			result := engine.NewObject()
			if idx >= len(entries) {
				_ = result.Set("value", engine.Undefined())
				_ = result.Set("done", engine.Boolean(true))
				return result, nil
			}
			e := entries[idx]
			idx++
			_ = result.Set("value", engine.NewArray([]engine.Value{engine.Str(e.key), engine.Str(e.val)}))
			_ = result.Set("done", engine.Boolean(false))
			return result, nil
		})
		_ = iterObj.Set("next", next)
		_ = iterObj.Set(engine.SymbolIterator.SymbolKey(), engine.NewFunction("[Symbol.iterator]", func(na []engine.Value) (engine.Value, error) {
			return iterObj, nil
		}))
		return iterObj, nil
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
	redirectMode := "follow"

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
			if v, err := o.Get("redirect"); err == nil && !v.IsUndefined() && v.String() != "" {
				redirectMode = v.String()
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
			switch redirectMode {
			case "manual":
				// 返回 3xx 原始响应（Go 的 ErrUseLastResponse 语义）。
				client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
					return http.ErrUseLastResponse
				}
			case "error":
				client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
					return fmt.Errorf("fetch: redirect to %s rejected", req.URL)
				}
			}
			resp, err := client.Do(req)
			if err != nil {
				ctx.PostTask(func() {
					defer release()
					callResolve(reject, engine.Str("fetch: "+err.Error()))
				})
				return
			}
			defer resp.Body.Close()
			bodyReady := make(chan *fetchBodyState, 1)
			ctx.PostTask(func() {
				res, body := newStreamingFetchResponse(ctx, resp.StatusCode, resp.Status, httpHeaderToEngine(resp.Header), urlStr)
				callResolve(resolve, res)
				bodyReady <- body
			})
			body := <-bodyReady

			buf := make([]byte, 32*1024)
			for {
				n, readErr := resp.Body.Read(buf)
				if n > 0 {
					chunk := append([]byte(nil), buf[:n]...)
					ctx.PostTask(func() { body.append(chunk) })
				}
				if readErr != nil {
					ctx.PostTask(func() {
						defer release()
						if readErr == io.EOF {
							body.finish(nil)
						} else {
							body.finish(readErr)
						}
					})
					return
				}
			}
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
							if _, err := f.Call([]engine.Value{NewBufferInstance([]byte(bodyStr))}); err != nil {
								interpreter.ReportUncaught(nil, err)
							}
						}
					}
					if cl, err := c.Get("close"); err == nil && cl.IsFunction() {
						if f, ok := cl.AsFunction(); ok {
							if _, err := f.Call(nil); err != nil {
								interpreter.ReportUncaught(nil, err)
							}
						}
					}
				}
			}
			return engine.Undefined(), nil
		}),
	})})
	return stream
}
