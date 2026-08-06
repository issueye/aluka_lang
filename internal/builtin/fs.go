package builtin

// node:fs 内置模块——文件系统操作。
// 本文件实现同步 API（*Sync 后缀）。异步 API（回调/Promise）在后续阶段补充。
// 基于 Go os/io 标准库。

import (
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
	"github.com/fsnotify/fsnotify"
)

// NewFS 构造 node:fs 模块的导出对象。
func NewFS(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// --- 读 API ---

	_ = m.Set("readFileSync", engine.NewFunction("readFileSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("readFileSync: path required")
		}
		path := args[0].String()
		data, err := os.ReadFile(path)
		if err != nil {
			return engine.Undefined(), err
		}
		// 第二参数：编码或 options。'utf8'/'utf-8' → 返回字符串，否则返回 Buffer/字符串。
		encoding := ""
		if len(args) > 1 {
			if args[1].Type() == engine.TypeString {
				encoding = args[1].String()
			} else if opts, ok := args[1].AsObject(); ok {
				if e, err := opts.Get("encoding"); err == nil {
					encoding = e.String()
				}
			}
		}
		if encoding == "utf8" || encoding == "utf-8" {
			return engine.Str(string(data)), nil
		}
		// 无编码：返回 base64 编码的字符串（简化版 Buffer 替代）。
		return engine.Str(base64.StdEncoding.EncodeToString(data)), nil
	}))

	// --- 写 API ---

	_ = m.Set("writeFileSync", engine.NewFunction("writeFileSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("writeFileSync: path and data required")
		}
		path := args[0].String()
		var data []byte
		switch args[1].Type() {
		case engine.TypeString:
			data = []byte(args[1].String())
		default:
			data = []byte(args[1].String())
		}
		err := os.WriteFile(path, data, 0644)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Undefined(), nil
	}))

	_ = m.Set("appendFileSync", engine.NewFunction("appendFileSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("appendFileSync: path and data required")
		}
		path := args[0].String()
		data := []byte(args[1].String())
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return engine.Undefined(), err
		}
		defer f.Close()
		_, err = f.Write(data)
		return engine.Undefined(), err
	}))

	// --- 文件信息 ---

	_ = m.Set("existsSync", engine.NewFunction("existsSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		_, err := os.Stat(args[0].String())
		return engine.Boolean(err == nil), nil
	}))

	_ = m.Set("statSync", engine.NewFunction("statSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("statSync: path required")
		}
		info, err := os.Stat(args[0].String())
		if err != nil {
			return engine.Undefined(), err
		}
		return statToObj(info), nil
	}))

	_ = m.Set("lstatSync", engine.NewFunction("lstatSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("lstatSync: path required")
		}
		info, err := os.Lstat(args[0].String())
		if err != nil {
			return engine.Undefined(), err
		}
		return statToObj(info), nil
	}))

	// --- 目录操作 ---

	_ = m.Set("mkdirSync", engine.NewFunction("mkdirSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("mkdirSync: path required")
		}
		path := args[0].String()
		// 递归创建（options.recursive 或第二参数为对象含 recursive）
		recursive := false
		if len(args) > 1 {
			if opts, ok := args[1].AsObject(); ok {
				if r, err := opts.Get("recursive"); err == nil {
					if b, ok := r.Bool(); ok {
						recursive = b
					}
				}
			}
		}
		var err error
		if recursive {
			err = os.MkdirAll(path, 0755)
		} else {
			err = os.Mkdir(path, 0755)
		}
		return engine.Undefined(), err
	}))

	_ = m.Set("mkdirpSync", engine.NewFunction("mkdirpSync", func(args []engine.Value) (engine.Value, error) {
		// alias for mkdirSync with recursive
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		return engine.Undefined(), os.MkdirAll(args[0].String(), 0755)
	}))

	_ = m.Set("rmdirSync", engine.NewFunction("rmdirSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		return engine.Undefined(), os.Remove(args[0].String())
	}))

	_ = m.Set("rmSync", engine.NewFunction("rmSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		path := args[0].String()
		recursive := false
		if len(args) > 1 {
			if opts, ok := args[1].AsObject(); ok {
				if r, err := opts.Get("recursive"); err == nil {
					if b, ok := r.Bool(); ok {
						recursive = b
					}
				}
			}
		}
		if recursive {
			return engine.Undefined(), os.RemoveAll(path)
		}
		return engine.Undefined(), os.Remove(path)
	}))

	_ = m.Set("readdirSync", engine.NewFunction("readdirSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.NewArray(nil), fmt.Errorf("readdirSync: path required")
		}
		path := args[0].String()
		withFileTypes := false
		if len(args) > 1 {
			if opts, ok := args[1].AsObject(); ok {
				if wfd, err := opts.Get("withFileTypes"); err == nil {
					if b, ok := wfd.Bool(); ok {
						withFileTypes = b
					}
				}
			}
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return engine.Undefined(), err
		}
		var result []engine.Value
		for _, entry := range entries {
			if withFileTypes {
				dirent := engine.NewObject()
				_ = dirent.Set("name", engine.Str(entry.Name()))
				if entry.IsDir() {
					_ = dirent.Set("isDirectory", engine.Boolean(true))
				} else {
					_ = dirent.Set("isDirectory", engine.Boolean(false))
				}
				result = append(result, dirent)
			} else {
				result = append(result, engine.Str(entry.Name()))
			}
		}
		return engine.NewArray(result), nil
	}))

	// --- 文件操作 ---

	_ = m.Set("unlinkSync", engine.NewFunction("unlinkSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		return engine.Undefined(), os.Remove(args[0].String())
	}))

	_ = m.Set("renameSync", engine.NewFunction("renameSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("renameSync: oldPath and newPath required")
		}
		return engine.Undefined(), os.Rename(args[0].String(), args[1].String())
	}))

	_ = m.Set("copyFileSync", engine.NewFunction("copyFileSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("copyFileSync: src and dest required")
		}
		data, err := os.ReadFile(args[0].String())
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Undefined(), os.WriteFile(args[1].String(), data, 0644)
	}))

	_ = m.Set("realpathSync", engine.NewFunction("realpathSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		abs, err := filepath.Abs(args[0].String())
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Str(abs), nil
	}))

	// fs.globSync(pattern[, options])：glob 匹配（Node 22 语义，遍历顺序一致）。
	_ = m.Set("globSync", engine.NewFunction("globSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.NewArray(nil), nil
		}
		var patterns []*globPattern
		nocase := runtime.GOOS == "windows" // Node glob nocase: isWindows || isMacOS
		if arr, ok := args[0].(*engine.ArrayValue); ok {
			for _, k := range arr.Keys() {
				if k == "length" {
					continue
				}
				iv, _ := arr.Get(k)
				patterns = append(patterns, globCompilePattern(iv.String(), nocase)...)
			}
		} else {
			patterns = globCompilePattern(args[0].String(), nocase)
		}
		opts := engine.Undefined()
		if len(args) > 1 {
			opts = args[1]
		}
		root, excludeFn, withFileTypes, err := globParseOptions(opts)
		if err != nil {
			return engine.Undefined(), err
		}
		g := newGlobEngine(root, nocase, excludeFn)
		results := g.globSyncRun(patterns)
		return engine.NewArray(globToResults(g, results, withFileTypes, root)), nil
	}))

	// fs.glob(pattern[, options], callback)：异步版本（回调风格）。
	_ = m.Set("glob", engine.NewFunction("glob", func(args []engine.Value) (engine.Value, error) {
		var pattern engine.Value = engine.Undefined()
		var opts engine.Value = engine.Undefined()
		cb := engine.Undefined()
		for _, a := range args {
			if a.IsFunction() {
				cb = a
			} else if pattern.IsUndefined() {
				pattern = a
			} else {
				opts = a
			}
		}
		if pattern.IsUndefined() {
			return engine.Undefined(), fmt.Errorf("glob: pattern required")
		}
		if cb.IsUndefined() {
			return engine.Undefined(), fmt.Errorf("glob: callback required")
		}
		release := ctx.AddRef()
		go func() {
			defer release()
			ctx.PostTask(func() {
				defer func() {
					_ = cb
				}()
				f, ok := cb.AsFunction()
				if !ok {
					return
				}
				var patterns []*globPattern
				nocase := runtime.GOOS == "windows"
				if arr, ok := pattern.(*engine.ArrayValue); ok {
					for _, k := range arr.Keys() {
						if k == "length" {
							continue
						}
						iv, _ := arr.Get(k)
						patterns = append(patterns, globCompilePattern(iv.String(), nocase)...)
					}
				} else {
					patterns = globCompilePattern(pattern.String(), nocase)
				}
				root, excludeFn, withFileTypes, err := globParseOptions(opts)
				if err != nil {
					_, _ = f.Call([]engine.Value{makeErrorValue(ctx, err)})
					return
				}
				g := newGlobEngine(root, nocase, excludeFn)
				results := g.globSyncRun(patterns)
				arr := engine.NewArray(globToResults(g, results, withFileTypes, root))
				_, _ = f.Call([]engine.Value{engine.Null(), arr})
			})
		}()
		return engine.Undefined(), nil
	}))

	// fs.cpSync(src, dest[, options])：复制文件或目录（recursive 时复制整棵树）。
	_ = m.Set("cpSync", engine.NewFunction("cpSync", func(args []engine.Value) (engine.Value, error) {
		return fsCpSyncImpl(args)
	}))

	// fs.cp(src, dest[, options], callback)：异步版本。
	_ = m.Set("cp", engine.NewFunction("cp", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 3 {
			return engine.Undefined(), fmt.Errorf("cp: src, dest and callback required")
		}
		src := args[0].String()
		dest := args[1].String()
		var optsVal engine.Value = engine.Undefined()
		cb := engine.Undefined()
		for _, a := range args[2:] {
			if a.IsFunction() {
				cb = a
			} else {
				optsVal = a
			}
		}
		if cb.IsUndefined() {
			return engine.Undefined(), fmt.Errorf("cp: callback required")
		}
		opts := parseFsCpOptions(optsVal)
		release := ctx.AddRef()
		go func() {
			defer release()
			ctx.PostTask(func() {
				if f, ok := cb.AsFunction(); ok {
					if err := fsCpCopy(src, dest, opts, true); err != nil {
						_, _ = f.Call([]engine.Value{makeErrorValue(ctx, err)})
					} else {
						_, _ = f.Call([]engine.Value{engine.Null()})
					}
				}
			})
		}()
		return engine.Undefined(), nil
	}))

	// --- 常量 ---
	constants := engine.NewObject()
	_ = constants.Set("F_OK", engine.IntValue(0))
	_ = constants.Set("R_OK", engine.IntValue(4))
	_ = constants.Set("W_OK", engine.IntValue(2))
	_ = constants.Set("X_OK", engine.IntValue(1))
	_ = m.Set("constants", constants)

	addFSStreamsAndWatch(ctx, m)

	return m, nil
}

// addFSStreamsAndWatch 补全 fs.watch / createReadStream / createWriteStream
// （Pi 的 footer-data-provider 监视 git HEAD、逐行读文件等场景）。
func addFSStreamsAndWatch(ctx engine.Context, m engine.Object) {
	// fs.watch(path[, options][, listener]) → FSWatcher（EventEmitter 风格：
	// 'change'/'error' 事件 + close()）。基于 fsnotify。
	_ = m.Set("watch", engine.NewFunction("watch", func(args []engine.Value) (engine.Value, error) {
		path := ""
		listener := engine.Undefined()
		for _, a := range args {
			switch a.Type() {
			case engine.TypeString:
				if path == "" {
					path = a.String()
				}
			case engine.TypeFunction:
				listener = a
			default:
				// options 对象（忽略）
			}
		}
		if path == "" {
			return engine.Undefined(), fmt.Errorf("watch: path required")
		}
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			return engine.Undefined(), err
		}
		if err := watcher.Add(path); err != nil {
			watcher.Close()
			return engine.Undefined(), fmt.Errorf("watch %q: %w", path, err)
		}
		// 构造 FSWatcher 实例（EventEmitter 风格）。
		instance := newEmitterLike()
		release := ctx.AddRef()
		closed := false
		go func() {
			for {
				select {
				case ev, ok := <-watcher.Events:
					if !ok {
						return
					}
					if closed {
						return
					}
					ctx.PostTask(func() {
						// 触发 'change' 事件（eventType, filename）。
						// Node 语义：eventType 为 'change' 或 'rename'。
						eventType := "change"
						if ev.Op&fsnotify.Rename != 0 {
							eventType = "rename"
						}
						if fn, err := instance.Get("emit"); err == nil && fn.IsFunction() {
							if f, ok := fn.AsFunction(); ok {
								// emit 首参为事件名，其余传给监听器 (eventType, filename)。
								_, _ = f.Call([]engine.Value{
									engine.Str("change"),
									engine.Str(eventType),
									engine.Str(filepath.Base(ev.Name)),
								})
							}
						}
					})
				case werr, ok := <-watcher.Errors:
					if !ok {
						return
					}
					if closed {
						return
					}
					ctx.PostTask(func() {
						if fn, err := instance.Get("emit"); err == nil && fn.IsFunction() {
							if f, ok := fn.AsFunction(); ok {
								_, _ = f.Call([]engine.Value{engine.Str("error"), engine.Str(werr.Error())})
							}
						}
					})
				}
			}
		}()
		_ = instance.Set("close", engine.NewFunction("close", func(args []engine.Value) (engine.Value, error) {
			if !closed {
				closed = true
				release()
				_ = watcher.Close()
			}
			return engine.Undefined(), nil
		}))
		_ = instance.Set("ref", engine.NewFunction("ref", func(args []engine.Value) (engine.Value, error) {
			return instance, nil
		}))
		_ = instance.Set("unref", engine.NewFunction("unref", func(args []engine.Value) (engine.Value, error) {
			return instance, nil
		}))
		// listener 简写：watch(path, listener) → watcher.on('change', listener)。
		if listener.IsFunction() {
			if fn, err := instance.Get("on"); err == nil && fn.IsFunction() {
				if f, ok := fn.AsFunction(); ok {
					_, _ = f.Call([]engine.Value{engine.Str("change"), listener})
				}
			}
		}
		return instance, nil
	}))

	// createReadStream(path[, options])：简化流——'data'/'end'/'error' 事件。
	_ = m.Set("createReadStream", engine.NewFunction("createReadStream", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("createReadStream: path required")
		}
		path := args[0].String()
		encoding := ""
		if len(args) > 1 {
			if o, ok := args[1].AsObject(); ok {
				if v, err := o.Get("encoding"); err == nil && !v.IsUndefined() {
					encoding = v.String()
				}
			}
		}
		stream := newEmitterLike()
		release := ctx.AddRef()
		go func() {
			defer release()
			file, err := os.Open(path)
			if err != nil {
				ctx.PostTask(func() {
					emitOn(stream, "error", engine.Str(err.Error()))
				})
				return
			}
			defer file.Close()
			buf := make([]byte, 64*1024)
			for {
				n, err := file.Read(buf)
				if n > 0 {
					chunk := make([]byte, n)
					copy(chunk, buf[:n])
					ctx.PostTask(func() {
						var val engine.Value
						if encoding != "" && encoding != "buffer" {
							val = engine.Str(string(chunk))
						} else {
							val = globals.NewBufferInstance(chunk)
						}
						emitOn(stream, "data", val)
					})
				}
				if err != nil {
					if err != io.EOF {
						ctx.PostTask(func() {
							emitOn(stream, "error", engine.Str(err.Error()))
						})
					} else {
						ctx.PostTask(func() {
							emitOn(stream, "end")
						})
					}
					break
				}
			}
		}()
		// 常用属性/方法。
		_ = stream.Set("close", engine.NewFunction("close", func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
		_ = stream.Set("destroy", engine.NewFunction("destroy", func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), nil
		}))
		_ = stream.Set("on", streamOn(stream))
		return stream, nil
	}))

	// createWriteStream(path)：简化流——write()/end() 事件。
	_ = m.Set("createWriteStream", engine.NewFunction("createWriteStream", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("createWriteStream: path required")
		}
		path := args[0].String()
		stream := newEmitterLike()
		_ = stream.Set("write", engine.NewFunction("write", func(args []engine.Value) (engine.Value, error) {
			if len(args) == 0 {
				return engine.Undefined(), nil
			}
			data := fsDataBytes(args[0])
			if err := os.WriteFile(path, data, 0644); err != nil {
				return engine.Undefined(), err
			}
			return engine.Boolean(true), nil
		}))
		_ = stream.Set("end", engine.NewFunction("end", func(args []engine.Value) (engine.Value, error) {
			if len(args) > 0 {
				data := fsDataBytes(args[0])
				if err := os.WriteFile(path, data, 0644); err != nil {
					return engine.Undefined(), err
				}
			}
			emitOn(stream, "finish")
			emitOn(stream, "close")
			return engine.Undefined(), nil
		}))
		_ = stream.Set("on", streamOn(stream))
		return stream, nil
	}))
}

// newEmitterLike 构造最小 EventEmitter 风格对象（on/emit）。
func newEmitterLike() engine.Object {
	obj := engine.NewObject()
	_ = obj.Set("on", streamOn(obj))
	_ = obj.Set("emit", engine.NewFunction("emit", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		event := args[0].String()
		listeners, _ := obj.Get("__aluka_listeners")
		lo, _ := listeners.AsObject()
		if lo == nil {
			return engine.Boolean(false), nil
		}
		lv, _ := lo.Get(event)
		la, _ := lv.AsObject()
		if la == nil {
			return engine.Boolean(false), nil
		}
		lenV, _ := la.Get("length")
		n, _ := lenV.Int()
		for i := 0; i < n; i++ {
			fv, _ := la.Get(strconv.Itoa(i))
			if f, ok := fv.AsFunction(); ok {
				_, _ = f.Call(args[1:])
			}
		}
		return engine.Boolean(n > 0), nil
	}))
	// 初始化监听器表。
	listeners := engine.NewObject()
	_ = obj.Set("__aluka_listeners", listeners)
	return obj
}

// streamOn 返回 on(event, listener) 函数（挂在对象上）。
func streamOn(obj engine.Object) engine.Value {
	return engine.NewFunction("on", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return obj, nil
		}
		event := args[0].String()
		listeners, _ := obj.Get("__aluka_listeners")
		lo, _ := listeners.AsObject()
		lv, _ := lo.Get(event)
		if lv.IsUndefined() {
			lv = engine.NewArray(nil)
			_ = lo.Set(event, lv)
		}
		la, _ := lv.AsObject()
		lenV, _ := la.Get("length")
		n, _ := lenV.Int()
		_ = la.Set(strconv.Itoa(n), args[1])
		_ = la.Set("length", engine.IntValue(n+1))
		return obj, nil
	})
}

// emitOn 在对象上触发事件（从 \x00listeners 表取监听器）。
func emitOn(obj engine.Object, event string, args ...engine.Value) {
	if fn, err := obj.Get("emit"); err == nil && fn.IsFunction() {
		if f, ok := fn.AsFunction(); ok {
			callArgs := append([]engine.Value{engine.Str(event)}, args...)
			_, _ = f.Call(callArgs)
		}
	}
}

// statToObj 将 os.FileInfo 转为 Node.js Stats 对象。
func statToObj(info fs.FileInfo) engine.Value {
	obj := engine.NewObject()
	_ = obj.Set("size", engine.IntValue(int(info.Size())))
	_ = obj.Set("mtime", engine.Number(float64(info.ModTime().UnixMilli())))
	_ = obj.Set("ctime", engine.Number(float64(info.ModTime().UnixMilli())))
	_ = obj.Set("atime", engine.Number(float64(info.ModTime().UnixMilli())))
	_ = obj.Set("birthtime", engine.Number(float64(info.ModTime().UnixMilli())))
	_ = obj.Set("isFile", engine.Boolean(!info.IsDir()))
	_ = obj.Set("isDirectory", engine.Boolean(info.IsDir()))
	_ = obj.Set("isSymbolicLink", engine.Boolean(info.Mode()&fs.ModeSymlink != 0))
	mode := int32(info.Mode().Perm())
	_ = obj.Set("mode", engine.IntValue(int(mode)))
	_ = obj.Set("uid", engine.IntValue(0))
	_ = obj.Set("gid", engine.IntValue(0))

	// 方法形式（Node.js Stats.isFile() 是方法）
	_ = obj.Set("isFile", engine.NewFunction("isFile", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(!info.IsDir()), nil
	}))
	_ = obj.Set("isDirectory", engine.NewFunction("isDirectory", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(info.IsDir()), nil
	}))
	_ = obj.Set("isSymbolicLink", engine.NewFunction("isSymbolicLink", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(info.Mode()&fs.ModeSymlink != 0), nil
	}))
	return obj
}
