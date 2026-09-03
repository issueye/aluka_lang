package globals

// Aluka 全局对象（开发计划 Phase 4，API 兼容 Bun；同时注册 Bun 别名）。
//
// 实现的核心 API：
//   - Aluka.version/platform/arch/main/cwd/origin/nanoseconds
//   - Aluka.env（与 process.env 同源）
//   - Aluka.sleep(ms)/sleepSync(ms)
//   - Aluka.gc()
//   - Aluka.file(path) → BunFile（text/json/arrayBuffer/size）
//   - Aluka.write(path, data)
//   - Aluka.stdout/stderr/stdin
//   - Aluka.serve({port, fetch}) → {port, url, stop}（Go net/http + PostTask）

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals/galuka"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gbase"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gbuffer"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gcrypto"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gencoding"
)

// AlukaConfig 配置 Aluka 全局。
type AlukaConfig struct{}

// NewAluka 注册全局 Aluka 对象（并注册 Bun 别名以兼容 Bun 代码）。
func NewAluka(ctx engine.Context, cfg AlukaConfig) error {
	aluka := engine.NewObject()

	// --- 基本信息 ---
	_ = aluka.Set("version", engine.Str("0.1.0-aluka"))
	_ = aluka.Set("platform", engine.Str(gbase.PlatformName()))
	_ = aluka.Set("arch", engine.Str(gbase.ArchName()))
	_ = aluka.Set("cwd", engine.NewFunction("cwd", func(args []engine.Value) (engine.Value, error) {
		wd, err := os.Getwd()
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Str(wd), nil
	}))
	_ = aluka.Set("origin", engine.Str("http://localhost:3000"))
	_ = aluka.Set("main", engine.Str(""))
	_ = aluka.Set("nanoseconds", engine.NewFunction("nanoseconds", func(args []engine.Value) (engine.Value, error) {
		return engine.IntValue(int(time.Now().UnixNano())), nil
	}))

	// --- env（与 process.env 同源） ---
	envObj := engine.NewObject()
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			_ = envObj.Set(kv[:i], engine.Str(kv[i+1:]))
		}
	}
	_ = aluka.Set("env", envObj)

	// --- sleep / sleepSync ---
	_ = aluka.Set("sleep", engine.NewFunction("sleep", func(args []engine.Value) (engine.Value, error) {
		ms := gbase.ArgInt(args, 0, 0)
		executor := engine.NewFunction("executor", func(ea []engine.Value) (engine.Value, error) {
			if len(ea) == 0 {
				return engine.Undefined(), nil
			}
			resolve := ea[0]
			release := ctx.AddRef()
			time.AfterFunc(time.Duration(ms)*time.Millisecond, func() {
				ctx.PostTask(func() {
					defer release()
					if f, ok := resolve.AsFunction(); ok {
						if _, err := f.Call(nil); err != nil {
							interpreter.ReportUncaught(ctx, err)
						}
					}
				})
			})
			return engine.Undefined(), nil
		})
		return gbase.NewPromise(ctx, executor)
	}))
	_ = aluka.Set("sleepSync", engine.NewFunction("sleepSync", func(args []engine.Value) (engine.Value, error) {
		ms := gbase.ArgInt(args, 0, 0)
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return engine.Undefined(), nil
	}))

	// --- gc ---
	_ = aluka.Set("gc", engine.NewFunction("gc", func(args []engine.Value) (engine.Value, error) {
		engine.GC([]engine.Value{ctx.Global()})
		return engine.Undefined(), nil
	}))

	// --- file / write ---
	_ = aluka.Set("file", engine.NewFunction("file", func(args []engine.Value) (engine.Value, error) {
		path := ""
		if len(args) > 0 {
			path = args[0].String()
		}
		return newAlukaFile(ctx, path), nil
	}))
	_ = aluka.Set("write", engine.NewFunction("write", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("Aluka.write: path and data required")
		}
		path := args[0].String()
		data := []byte(args[1].String())
		if b, ok := engine.AsBuffer(args[1]); ok {
			data = b
		}
		return engine.Undefined(), os.WriteFile(path, data, 0644)
	}))

	// --- stdout / stderr / stdin ---
	_ = aluka.Set("stdout", newAlukaFile(ctx, ""))
	_ = aluka.Set("stderr", engine.NewObject())
	_ = aluka.Set("stdin", engine.NewObject())

	// --- serve ---
	_ = aluka.Set("serve", engine.NewFunction("serve", func(args []engine.Value) (engine.Value, error) {
		return alukaServe(ctx, args)
	}))

	// --- Phase 4 扩展 API ---
	galuka.RegisterShell(ctx, aluka)
	gcrypto.RegisterAlukaPassword(ctx, aluka)
	gcrypto.RegisterAlukaHash(ctx, aluka)
	galuka.RegisterCompress(ctx, aluka)
	galuka.RegisterUtil(ctx, aluka)
	gencoding.RegisterAlukaEncoding(ctx, aluka)
	galuka.RegisterSpawn(ctx, aluka)
	galuka.RegisterSQL(ctx, aluka)
	galuka.RegisterRedis(ctx, aluka)
	galuka.RegisterS3(ctx, aluka)
	galuka.RegisterIPC(ctx, aluka)
	galuka.RegisterGUI(ctx, aluka)

	if err := ctx.Global().Set("Aluka", aluka); err != nil {
		return err
	}
	// Bun 兼容别名。
	return ctx.Global().Set("Bun", aluka)
}

// newAlukaFile 构造 BunFile 对象。
// Bun 语义：text/json/arrayBuffer 每次调用实时读盘（write 后可读到新内容），
// size 惰性 stat（访问时取文件当前大小）。
func newAlukaFile(ctx engine.Context, path string) engine.Value {
	file := engine.NewObject()
	// readAll 惰性读取文件内容；空 path（如 stdout）视为无数据。
	readAll := func() []byte {
		if path == "" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		return data
	}
	engine.SetAccessor(file, "size", engine.NewFunction("size", func(args []engine.Value) (engine.Value, error) {
		if path == "" {
			return engine.IntValue(0), nil
		}
		if st, err := os.Stat(path); err == nil {
			return engine.IntValue(int(st.Size())), nil
		}
		return engine.IntValue(0), nil
	}), nil)
	_ = file.Set("type", engine.Str(""))
	_ = file.Set("text", engine.NewFunction("text", func(args []engine.Value) (engine.Value, error) {
		return gbase.ResolveValue(ctx, engine.Str(string(readAll())))
	}))
	_ = file.Set("arrayBuffer", engine.NewFunction("arrayBuffer", func(args []engine.Value) (engine.Value, error) {
		return gbase.ResolveValue(ctx, gbuffer.NewBufferInstance(readAll()))
	}))
	_ = file.Set("json", engine.NewFunction("json", func(args []engine.Value) (engine.Value, error) {
		jsonGlobal, err := ctx.Global().Get("JSON")
		if err != nil {
			return gbase.RejectValue(ctx, "JSON not available")
		}
		jo, _ := jsonGlobal.AsObject()
		if parseFn, err := jo.Get("parse"); err == nil && parseFn.IsFunction() {
			if f, ok := parseFn.AsFunction(); ok {
				parsed, perr := f.Call([]engine.Value{engine.Str(string(readAll()))})
				if perr != nil {
					return gbase.RejectValue(ctx, perr.Error())
				}
				return gbase.ResolveValue(ctx, parsed)
			}
		}
		return gbase.RejectValue(ctx, "JSON.parse failed")
	}))

	// write(chunk)：写入文件（追加模式简化）。
	_ = file.Set("write", engine.NewFunction("write", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 && path != "" {
			chunk := []byte(args[0].String())
			return engine.Undefined(), os.WriteFile(path, chunk, 0644)
		}
		return engine.Undefined(), nil
	}))

	return file
}

// --- serve ----------------------------------------------------------------

// alukaServe 实现 Aluka.serve。
func alukaServe(ctx engine.Context, args []engine.Value) (engine.Value, error) {
	port := 3000
	hostname := ""
	var handler engine.Value
	if len(args) > 0 && args[0].IsObject() {
		if o, ok := args[0].AsObject(); ok {
			if v, err := o.Get("port"); err == nil && !v.IsUndefined() {
				if n, ok := v.Int(); ok {
					port = n
				}
			}
			if v, err := o.Get("hostname"); err == nil && !v.IsUndefined() && v.String() != "" {
				hostname = v.String()
			}
			if v, err := o.Get("fetch"); err == nil && v.IsFunction() {
				handler = v
			}
		}
	}
	if handler == nil {
		return engine.Undefined(), fmt.Errorf("Aluka.serve: requires a fetch handler")
	}

	server := engine.NewObject()
	release := ctx.AddRef()

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		alukaServeRequest(ctx, handler, w, r)
	})}

	// 同步 Listen 以立即暴露实际端口（port:0 时由 OS 分配），
	// Serve 在后台 goroutine 阻塞直到 stop()。
	addr := fmt.Sprintf("%s:%d", hostname, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		release()
		return engine.Undefined(), fmt.Errorf("Aluka.serve: %w", err)
	}
	actualPort := 3000
	if tcpAddr, ok := ln.Addr().(*net.TCPAddr); ok {
		actualPort = tcpAddr.Port
	}
	_ = server.Set("port", engine.IntValue(actualPort))
	_ = server.Set("url", engine.Str(fmt.Sprintf("http://localhost:%d", actualPort)))
	go func() {
		_ = srv.Serve(ln)
	}()

	// stop() → Promise。
	_ = server.Set("stop", engine.NewFunction("stop", func(sa []engine.Value) (engine.Value, error) {
		executor := engine.NewFunction("executor", func(ea []engine.Value) (engine.Value, error) {
			if len(ea) > 0 {
				resolve := ea[0]
				go func() {
					_ = srv.Close()
					ctx.PostTask(func() {
						release()
						if f, ok := resolve.AsFunction(); ok {
							if _, err := f.Call(nil); err != nil {
								interpreter.ReportUncaught(nil, err)
							}
						}
					})
				}()
			}
			return engine.Undefined(), nil
		})
		return gbase.NewPromise(ctx, executor)
	}))

	return server, nil
}

// alukaServeRequest 处理一次请求（阻塞直到响应完成）。
func alukaServeRequest(ctx engine.Context, handler engine.Value, w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	done := make(chan struct{})
	ctx.PostTask(func() {
		req := engine.NewObject()
		_ = req.Set("method", engine.Str(r.Method))
		urlStr := "/"
		if r.URL != nil {
			urlStr = r.URL.RequestURI()
		}
		_ = req.Set("url", engine.Str(urlStr))
		h := engine.NewObject()
		// Go net/http 将 Host 放在 r.Host（不在 r.Header）；补进请求头供 handler 校验
		if r.Host != "" {
			_ = h.Set("Host", engine.Str(r.Host))
		}
		for k, vals := range r.Header {
			if len(vals) > 0 {
				_ = h.Set(k, engine.Str(strings.Join(vals, ", ")))
			}
		}
		_ = req.Set("headers", h)
		if len(body) > 0 {
			_ = req.Set("body", engine.Str(string(body)))
		}
		if handler.IsFunction() {
			if f, ok := handler.AsFunction(); ok {
				result, _ := f.Call([]engine.Value{req})
				alukaThen(ctx, result, func(res engine.Value) {
					alukaWriteResponse(ctx, res, w, done)
				})
				return
			}
		}
		close(done)
	})
	<-done
}

// alukaThen：value 若为 Promise（有 then）则注册回调，否则直接回调。
func alukaThen(ctx engine.Context, value engine.Value, cb func(engine.Value)) {
	if value != nil && value.IsObject() {
		if o, ok := value.AsObject(); ok {
			if t, err := o.Get("then"); err == nil && t.IsFunction() {
				if tf, ok := t.AsFunction(); ok {
					onRes := engine.NewFunction("onResolved", func(args []engine.Value) (engine.Value, error) {
						if len(args) > 0 {
							cb(args[0])
						}
						return engine.Undefined(), nil
					})
					onRej := engine.NewFunction("onRejected", func(args []engine.Value) (engine.Value, error) {
						msg := "internal error"
						if len(args) > 0 && args[0] != nil {
							msg = args[0].String()
						}
						cb(errorResponseValue(500, msg))
						return engine.Undefined(), nil
					})
					// then 必须以 value 为 this 调用（Promise.prototype.then 校验接收者），
					// 拒绝路径回写 500，避免请求悬挂。
					if _, err := interpreter.CallWithThis(tf, value, []engine.Value{onRes, onRej}); err != nil {
						interpreter.ReportUncaught(ctx, err)
					}
					return
				}
			}
		}
	}
	cb(value)
}

// errorResponseValue 构造 serve 可写回的最小错误响应对象（JSON body + 状态码）。
func errorResponseValue(status int, message string) engine.Value {
	res := engine.NewObject()
	_ = res.Set("status", engine.IntValue(status))
	hdrs := engine.NewObject()
	_ = hdrs.Set("_pairs", engine.NewArray([]engine.Value{
		engine.NewArray([]engine.Value{engine.Str("Content-Type"), engine.Str("application/json")}),
	}))
	_ = res.Set("headers", hdrs)
	_ = res.Set("_body", engine.Str(`{"error":`+strconv.Quote(message)+`}`))
	return res
}

// alukaWriteResponse 从 Response 对象写 Go 响应。
func alukaWriteResponse(ctx engine.Context, res engine.Value, w http.ResponseWriter, done chan struct{}) {
	if res == nil || res.IsUndefined() {
		w.WriteHeader(404)
		close(done)
		return
	}
	ro, ok := res.AsObject()
	if !ok {
		w.WriteHeader(500)
		close(done)
		return
	}
	status := 200
	if v, err := ro.Get("status"); err == nil {
		if n, ok := v.Int(); ok {
			status = n
		}
	}
	// 先设置 headers 再 WriteHeader。Headers 键值经内部 _pairs 读取。
	if v, err := ro.Get("headers"); err == nil {
		if o, ok := v.AsObject(); ok {
			if pv, err := o.Get("_pairs"); err == nil {
				if arr, ok := pv.(*engine.ArrayValue); ok {
					for _, e := range arr.Elems() {
						if pair, ok := e.(*engine.ArrayValue); ok && len(pair.Elems()) >= 2 {
							w.Header().Set(pair.Elems()[0].String(), pair.Elems()[1].String())
						}
					}
				}
			}
		}
	}
	w.WriteHeader(status)
	// body：读内部 _body（同步，避免 Promise.then 无 this 绑定问题）。
	if v, err := ro.Get("_body"); err == nil && !v.IsUndefined() && v.String() != "" {
		_, _ = w.Write([]byte(v.String()))
	}
	close(done)
}
