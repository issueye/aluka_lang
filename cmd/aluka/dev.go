// `aluka dev` 子命令（M3-3）：web 构建 + 静态开发服务器。
//
//	aluka dev [--host 127.0.0.1] [--port 3000] [--outdir dist] [--minify] <entry>
//
// 首次构建后启动静态服务（SPA fallback 到 index.html），并在后台 watch
// 源文件变更全量重建；重建成功后向 /__aluka/reload 的 SSE 客户端广播
// reload 事件，失败时 /__aluka/health 返回最近错误并继续服务旧产物。
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aluka-lang/aluka/internal/cli"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/project"
	alukart "github.com/aluka-lang/aluka/internal/project/aluka"
	"github.com/aluka-lang/aluka/internal/runtime/module"
)

type devOptions struct {
	host      string
	port      int
	outdir    string
	minify    bool
	entry     string
	cliOutdir bool
	cliMinify bool
}

func cmdDev(args []string) error {
	o := devOptions{host: "127.0.0.1", port: 3000, outdir: "dist"}
	fs := cli.NewFlagSet("aluka dev: ")
	fs.StrictUnknown = true
	fs.String("host", "Development server host", &o.host)
	fs.Var(cli.FuncValue{Fn: func(v string) error {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid port %q", v)
		}
		o.port = n
		return nil
	}}, "port", "Development server port")
	fs.String("outdir", "Output directory", &o.outdir)
	fs.Bool("minify", "Minify web output", &o.minify)
	pos, err := fs.Parse(args)
	if err != nil {
		return err
	}
	o.cliOutdir = cliFlagPresent(args, "outdir")
	o.cliMinify = cliFlagPresent(args, "minify")
	if !o.cliOutdir {
		o.outdir = ""
	}
	if len(pos) != 1 {
		return fmt.Errorf("aluka dev: expected one entry file")
	}
	o.entry = pos[0]
	vm, err := interpreter.NewVM()
	if err != nil {
		return err
	}
	buildOpts := buildOptions{
		target: "web", outdir: o.outdir, minify: o.minify, treeShake: true,
		cliOutdir: o.cliOutdir, cliMinify: o.cliMinify,
	}
	wopts := toWebOptions(buildOpts)
	if err := project.ApplyConfig(alukart.New(vm), o.entry, &wopts); err != nil {
		return err
	}
	o.outdir = wopts.OutDir
	o.minify = wopts.Minify
	if o.outdir == "" {
		o.outdir = "dist"
	}
	if err := os.MkdirAll(o.outdir, 0o755); err != nil {
		return err
	}

	srv := newDevServer(o)
	// 首次构建失败直接退出（无产物可服务）；后续失败只更新 health。
	if err := srv.rebuild(); err != nil {
		return err
	}
	go srv.watchLoop()

	addr := o.host + ":" + strconv.Itoa(o.port)
	fmt.Printf("dev server listening on http://%s\n", addr)
	return http.ListenAndServe(addr, srv.mux)
}

// devServer 聚合 dev 模式状态：静态服务 + watch 重建 + SSE 广播。
type devServer struct {
	opts       devOptions
	mux        *http.ServeMux
	mu         sync.RWMutex
	lastErr    string
	clients    map[chan string]struct{}
	written    map[string]bool
	watchExtra []string
}

func newDevServer(o devOptions) *devServer {
	s := &devServer{
		opts:    o,
		mux:     http.NewServeMux(),
		clients: map[chan string]struct{}{},
		written: map[string]bool{},
	}
	s.mux.HandleFunc("/__aluka/health", s.handleHealth)
	s.mux.HandleFunc("/__aluka/reload", s.handleReload)
	s.mux.Handle("/", http.HandlerFunc(s.handleStatic))
	return s
}

func (s *devServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": s.lastErr == "", "error": s.lastErr})
}

// handleReload 维持 SSE 连接：订阅事件 channel，向客户端写 event 行。
func (s *devServer) handleReload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}
	ch := make(chan string, 4)
	s.mu.Lock()
	s.clients[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, ch)
		s.mu.Unlock()
	}()
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-ch:
			fmt.Fprintf(w, "event: %s\ndata: {}\n\n", event)
			flusher.Flush()
		}
	}
}

// broadcast 向全部 SSE 客户端非阻塞发送事件。
func (s *devServer) broadcast(event string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.clients {
		select {
		case ch <- event:
		default: // 客户端阻塞时丢弃，避免拖慢重建
		}
	}
}

// handleStatic 服务产物目录；目录请求与未命中路径回退 index.html（SPA）。
// 回退直接 ServeFile：经 FileServer 改写 /index.html 会被 301 到 ./。
func (s *devServer) handleStatic(w http.ResponseWriter, r *http.Request) {
	clean := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(r.URL.Path, "/")))
	requestPath := filepath.Join(s.opts.outdir, clean)
	if r.URL.Path == "/" || strings.HasSuffix(r.URL.Path, "/") {
		requestPath = filepath.Join(s.opts.outdir, "index.html")
	}
	if info, statErr := os.Stat(requestPath); statErr == nil && !info.IsDir() {
		http.ServeFile(w, r, requestPath)
		return
	}
	fallback := filepath.Join(s.opts.outdir, "index.html")
	if info, statErr := os.Stat(fallback); statErr == nil && !info.IsDir() {
		http.ServeFile(w, r, fallback)
		return
	}
	http.NotFound(w, r)
}

// rebuild 执行一次全量构建并写出产物；成功时清空错误并广播 reload。
func (s *devServer) rebuild() error {
	vm, err := interpreter.NewVM()
	if err != nil {
		s.setErr(err)
		return err
	}
	rt := alukart.New(vm)
	wopts := toWebOptions(buildOptions{
		target: "web", outdir: s.opts.outdir, minify: s.opts.minify, treeShake: true,
		cliOutdir: s.opts.cliOutdir, cliMinify: s.opts.cliMinify,
	})
	if err := project.ApplyConfig(rt, s.opts.entry, &wopts); err != nil {
		s.setErr(err)
		return err
	}
	if wopts.OutDir == "" {
		wopts.OutDir = s.opts.outdir
	}
	bundled, err := project.BuildWeb(rt, module.NewResolver(), s.opts.entry, wopts)
	if err != nil {
		s.setErr(err)
		return err
	}
	s.mu.Lock()
	s.watchExtra = bundled.Watch
	s.mu.Unlock()
	writeOpts := project.Options{OutDir: s.opts.outdir, Plugins: wopts.Plugins}
	if err := project.WriteAssets(s.opts.entry, bundled.Assets, writeOpts, s.written); err != nil {
		s.setErr(err)
		return err
	}
	s.setErr(nil)
	s.broadcast("reload")
	return nil
}

func (s *devServer) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		s.lastErr = ""
	} else {
		s.lastErr = err.Error()
	}
}

// watchLoop 轮询源文件快照，变更后全量重建；失败保持旧产物继续服务。
func (s *devServer) watchLoop() {
	skipDir := s.opts.outdir
	s.mu.RLock()
	extra := append([]string(nil), s.watchExtra...)
	s.mu.RUnlock()
	snapshot := watchSnapshot(s.opts.entry, skipDir, extra...)
	for {
		time.Sleep(300 * time.Millisecond)
		s.mu.RLock()
		extra = append([]string(nil), s.watchExtra...)
		s.mu.RUnlock()
		next := watchSnapshot(s.opts.entry, skipDir, extra...)
		if reflect.DeepEqual(snapshot, next) {
			continue
		}
		snapshot = next
		if err := s.rebuild(); err != nil {
			fmt.Fprintln(os.Stderr, "dev:", err)
		} else {
			fmt.Println("dev: rebuilt")
		}
	}
}
