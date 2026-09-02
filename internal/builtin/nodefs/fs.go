package nodefs

// node:fs 内置模块——文件系统操作。
// 本文件实现同步 API（*Sync 后缀）。异步 API（回调/Promise）在后续阶段补充。
// 基于 Go os/io 标准库。

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"

	"github.com/aluka-lang/aluka/internal/builtin/nodebase"
	"github.com/aluka-lang/aluka/internal/builtin/nodeglob"
	"github.com/aluka-lang/aluka/internal/builtin/nodeos"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gbuffer"

	"github.com/aluka-lang/aluka/internal/engine"
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
		var data []byte
		if fd, ok := args[0].Int(); ok && args[0].Type() == engine.TypeNumber {
			f := osFileFromFD(fd)
			if f == nil {
				return engine.Undefined(), fdOpError("read", fd, os.ErrInvalid)
			}
			var err error
			data, err = io.ReadAll(f)
			if err != nil {
				return engine.Undefined(), fdOpError("read", fd, err)
			}
		} else {
			path := args[0].String()
			var err error
			data, err = os.ReadFile(path)
			if err != nil {
				return engine.Undefined(), fsReadError(path, err)
			}
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
		if encoding == "utf8" || encoding == "utf-8" || encoding == "utf16le" {
			return engine.Str(string(data)), nil
		}
		if encoding == "base64" {
			return engine.Str(base64.StdEncoding.EncodeToString(data)), nil
		}
		if encoding == "hex" {
			return engine.Str(fmt.Sprintf("%x", data)), nil
		}
		// 无编码：返回 Buffer。
		return gbuffer.NewBufferInstance(data), nil
	}))

	// --- 写 API ---

	_ = m.Set("writeFileSync", engine.NewFunction("writeFileSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("writeFileSync: path and data required")
		}
		data := fsDataBytes(args[1])
		if fd, ok := args[0].Int(); ok && args[0].Type() == engine.TypeNumber {
			f := osFileFromFD(fd)
			if f == nil {
				return engine.Undefined(), fdOpError("write", fd, os.ErrInvalid)
			}
			for len(data) > 0 {
				n, err := f.Write(data)
				if err != nil {
					return engine.Undefined(), fdOpError("write", fd, err)
				}
				if n == 0 {
					return engine.Undefined(), fdOpError("write", fd, io.ErrShortWrite)
				}
				data = data[n:]
			}
			return engine.Undefined(), nil
		}
		path := args[0].String()
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
		data := fsDataBytes(args[1])
		if fd, ok := args[0].Int(); ok && args[0].Type() == engine.TypeNumber {
			f := osFileFromFD(fd)
			if f == nil {
				return engine.Undefined(), fdOpError("write", fd, os.ErrInvalid)
			}
			_, err := f.Write(data)
			return engine.Undefined(), fdOpError("write", fd, err)
		}
		path := args[0].String()
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
		return statToObj(ctx, info), nil
	}))

	_ = m.Set("lstatSync", engine.NewFunction("lstatSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("lstatSync: path required")
		}
		info, err := os.Lstat(args[0].String())
		if err != nil {
			return engine.Undefined(), err
		}
		return statToObj(ctx, info), nil
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
		path := args[0].String()
		return engine.Undefined(), fsRmdirError(path, os.Remove(path))
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
				result = append(result, makeFsDirent(entry.Name(), path, entry.Type()))
			} else {
				result = append(result, engine.Str(entry.Name()))
			}
		}
		return engine.NewArray(result), nil
	}))

	// --- 文件操作 ---

	_ = m.Set("accessSync", engine.NewFunction("accessSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		_, err := os.Stat(args[0].String())
		return engine.Undefined(), err
	}))

	_ = m.Set("unlinkSync", engine.NewFunction("unlinkSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		return engine.Undefined(), os.Remove(args[0].String())
	}))

	_ = m.Set("truncateSync", engine.NewFunction("truncateSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		size := int64(0)
		if len(args) > 1 {
			if n, ok := args[1].Float(); ok {
				size = int64(n)
			}
		}
		return engine.Undefined(), os.Truncate(args[0].String(), size)
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

	realpathSync := engine.NewFunction("realpathSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Str(""), nil
		}
		abs, err := filepath.Abs(args[0].String())
		if err != nil {
			return engine.Undefined(), err
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Str(real), nil
	})
	if realpathObj, ok := realpathSync.AsObject(); ok {
		_ = realpathObj.Set("native", realpathSync)
	}
	_ = m.Set("realpathSync", realpathSync)

	// fs.mkdtempSync(prefix[, options])：创建唯一临时目录（prefix + 6 随机字符）。
	_ = m.Set("mkdtempSync", engine.NewFunction("mkdtempSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("mkdtempSync: prefix required")
		}
		dir, err := fsMakeTempDir(args[0].String())
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Str(dir), nil
	}))

	// fs.mkdtemp(prefix[, options], callback)。
	_ = m.Set("mkdtemp", engine.NewFunction("mkdtemp", func(args []engine.Value) (engine.Value, error) {
		var prefix string
		cb := engine.Undefined()
		for _, a := range args {
			if a.IsFunction() {
				cb = a
			} else if prefix == "" {
				prefix = a.String()
			}
		}
		if cb.IsUndefined() {
			return engine.Undefined(), fmt.Errorf("mkdtemp: callback required")
		}
		release := ctx.AddRef()
		go func() {
			defer release()
			ctx.PostTask(func() {
				if f, ok := cb.AsFunction(); ok {
					dir, err := fsMakeTempDir(prefix)
					if err != nil {
						if _, err := f.Call([]engine.Value{nodebase.MakeErrorValue(ctx, err)}); err != nil {
							interpreter.ReportUncaught(nil, err)
						}
					} else {
						if _, err := f.Call([]engine.Value{engine.Null(), engine.Str(dir)}); err != nil {
							interpreter.ReportUncaught(nil, err)
						}
					}
				}
			})
		}()
		return engine.Undefined(), nil
	}))

	// fs.globSync(pattern[, options])：glob 匹配（Node 22 语义，遍历顺序一致）。
	_ = m.Set("globSync", engine.NewFunction("globSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.NewArray(nil), nil
		}
		var patterns []*nodeglob.Pattern
		nocase := runtime.GOOS == "windows" // Node glob nocase: isWindows || isMacOS
		if arr, ok := args[0].(*engine.ArrayValue); ok {
			for _, k := range arr.Keys() {
				if k == "length" {
					continue
				}
				iv, _ := arr.Get(k)
				patterns = append(patterns, nodeglob.CompilePattern(iv.String(), nocase)...)
			}
		} else {
			patterns = nodeglob.CompilePattern(args[0].String(), nocase)
		}
		opts := engine.Undefined()
		if len(args) > 1 {
			opts = args[1]
		}
		root, excludeFn, withFileTypes, err := nodeglob.ParseOptions(opts)
		if err != nil {
			return engine.Undefined(), err
		}
		g := nodeglob.NewEngine(root, nocase, excludeFn)
		results := g.SyncRun(patterns)
		return engine.NewArray(nodeglob.ToResults(g, results, withFileTypes, root)), nil
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
				var patterns []*nodeglob.Pattern
				nocase := runtime.GOOS == "windows"
				if arr, ok := pattern.(*engine.ArrayValue); ok {
					for _, k := range arr.Keys() {
						if k == "length" {
							continue
						}
						iv, _ := arr.Get(k)
						patterns = append(patterns, nodeglob.CompilePattern(iv.String(), nocase)...)
					}
				} else {
					patterns = nodeglob.CompilePattern(pattern.String(), nocase)
				}
				root, excludeFn, withFileTypes, err := nodeglob.ParseOptions(opts)
				if err != nil {
					if _, err := f.Call([]engine.Value{nodebase.MakeErrorValue(ctx, err)}); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
					return
				}
				g := nodeglob.NewEngine(root, nocase, excludeFn)
				results := g.SyncRun(patterns)
				arr := engine.NewArray(nodeglob.ToResults(g, results, withFileTypes, root))
				if _, err := f.Call([]engine.Value{engine.Null(), arr}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
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
						if _, err := f.Call([]engine.Value{nodebase.MakeErrorValue(ctx, err)}); err != nil {
							interpreter.ReportUncaught(nil, err)
						}
					} else {
						if _, err := f.Call([]engine.Value{engine.Null()}); err != nil {
							interpreter.ReportUncaught(nil, err)
						}
					}
				}
			})
		}()
		return engine.Undefined(), nil
	}))

	// --- 回调版异步 API（fsAsync 模式）---
	addFSCallbacks(ctx, m)

	// --- 常量 ---
	_ = m.Set("constants", nodeos.FSConstantsObject())

	// fs.promises：Promise 版本（node:fs/promises，同一对象身份）。
	_ = m.Set("promises", getFSPromises(ctx))

	addFSStreamsAndWatch(ctx, m)
	addFSFD(ctx, m)

	return m, nil
}

// addFSCallbacks 注册回调版异步 fs API（M3：fs 三面之 callback）。
func addFSCallbacks(ctx engine.Context, m engine.Object) {
	// readFile(path[, options], callback)
	_ = m.Set("readFile", engine.NewFunction("readFile", func(args []engine.Value) (engine.Value, error) {
		path, encoding := fsPathArgs(args)
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("readFile: callback required")
		}
		fsAsync(ctx, func() (engine.Value, error) {
			data, err := os.ReadFile(path)
			if err != nil {
				return engine.Undefined(), fsReadError(path, err)
			}
			if encoding != "" {
				return engine.Str(string(data)), nil
			}
			return gbuffer.NewBufferInstance(data), nil
		}, cb)
		return engine.Undefined(), nil
	}))

	// writeFile(path, data[, options], callback)
	_ = m.Set("writeFile", engine.NewFunction("writeFile", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("writeFile: path and data required")
		}
		path := args[0].String()
		data := fsDataBytes(args[1])
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("writeFile: callback required")
		}
		fsAsync(ctx, func() (engine.Value, error) {
			return engine.Undefined(), os.WriteFile(path, data, 0644)
		}, cb)
		return engine.Undefined(), nil
	}))

	// appendFile(path, data[, options], callback)
	_ = m.Set("appendFile", engine.NewFunction("appendFile", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("appendFile: path and data required")
		}
		path := args[0].String()
		data := fsDataBytes(args[1])
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("appendFile: callback required")
		}
		fsAsync(ctx, func() (engine.Value, error) {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return engine.Undefined(), err
			}
			defer f.Close()
			_, err = f.Write(data)
			return engine.Undefined(), err
		}, cb)
		return engine.Undefined(), nil
	}))

	// stat(path, callback) / lstat(path, callback)
	_ = m.Set("stat", engine.NewFunction("stat", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("stat: path required")
		}
		path := args[0].String()
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("stat: callback required")
		}
		fsAsync(ctx, func() (engine.Value, error) {
			info, err := os.Stat(path)
			if err != nil {
				return engine.Undefined(), err
			}
			return statToObj(ctx, info), nil
		}, cb)
		return engine.Undefined(), nil
	}))

	_ = m.Set("lstat", engine.NewFunction("lstat", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("lstat: path required")
		}
		path := args[0].String()
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("lstat: callback required")
		}
		fsAsync(ctx, func() (engine.Value, error) {
			info, err := os.Lstat(path)
			if err != nil {
				return engine.Undefined(), err
			}
			return statToObj(ctx, info), nil
		}, cb)
		return engine.Undefined(), nil
	}))

	// mkdir(path[, options], callback)
	_ = m.Set("mkdir", engine.NewFunction("mkdir", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("mkdir: path required")
		}
		path := args[0].String()
		recursive := false
		if len(args) > 1 && args[1].IsObject() {
			if o, ok := args[1].AsObject(); ok {
				if v, err := o.Get("recursive"); err == nil {
					if b, ok2 := v.Bool(); ok2 {
						recursive = b
					}
				}
			}
		}
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("mkdir: callback required")
		}
		fsAsync(ctx, func() (engine.Value, error) {
			if recursive {
				return engine.Undefined(), os.MkdirAll(path, 0755)
			}
			return engine.Undefined(), os.Mkdir(path, 0755)
		}, cb)
		return engine.Undefined(), nil
	}))

	// readdir(path[, options], callback)
	_ = m.Set("readdir", engine.NewFunction("readdir", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("readdir: path required")
		}
		path := args[0].String()
		withFileTypes := false
		if len(args) > 1 {
			if opts, ok := args[1].AsObject(); ok {
				if wfd, err := opts.Get("withFileTypes"); err == nil {
					if b, ok2 := wfd.Bool(); ok2 {
						withFileTypes = b
					}
				}
			}
		}
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("readdir: callback required")
		}
		fsAsync(ctx, func() (engine.Value, error) {
			entries, err := os.ReadDir(path)
			if err != nil {
				return engine.Undefined(), err
			}
			vals := make([]engine.Value, 0, len(entries))
			for _, e := range entries {
				if withFileTypes {
					vals = append(vals, makeFsDirent(e.Name(), path, e.Type()))
				} else {
					vals = append(vals, engine.Str(e.Name()))
				}
			}
			return engine.NewArray(vals), nil
		}, cb)
		return engine.Undefined(), nil
	}))

	// 通用 no-value 回调包装器（unlink/rmdir/access）。
	addFSCbNoValue := func(name string, op func(args []engine.Value) error) {
		_ = m.Set(name, engine.NewFunction(name, func(args []engine.Value) (engine.Value, error) {
			cb, ok := fsCbArg(args)
			if !ok {
				return engine.Undefined(), fmt.Errorf("%s: callback required", name)
			}
			fsAsync(ctx, func() (engine.Value, error) {
				return engine.Undefined(), op(args)
			}, cb)
			return engine.Undefined(), nil
		}))
	}
	addFSCbNoValue("unlink", func(args []engine.Value) error { return os.Remove(args0(args)) })
	addFSCbNoValue("rmdir", func(args []engine.Value) error {
		p := args0(args)
		return fsRmdirError(p, os.Remove(p))
	})
	addFSCbNoValue("access", func(args []engine.Value) error { _, err := os.Stat(args0(args)); return err })

	// exists(path, callback)：回调参数 (exists)。
	_ = m.Set("exists", engine.NewFunction("exists", func(args []engine.Value) (engine.Value, error) {
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("exists: callback required")
		}
		path := args0(args)
		release := ctx.AddRef()
		go func() {
			_, err := os.Stat(path)
			ctx.PostTask(func() {
				defer release()
				if f, ok2 := cb.AsFunction(); ok2 {
					if _, err := f.Call([]engine.Value{engine.Boolean(err == nil)}); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
				}
			})
		}()
		return engine.Undefined(), nil
	}))

	// rm(path[, options], callback)
	_ = m.Set("rm", engine.NewFunction("rm", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("rm: path required")
		}
		path := args[0].String()
		recursive := false
		if len(args) > 1 && args[1].IsObject() {
			if o, ok := args[1].AsObject(); ok {
				if v, err := o.Get("recursive"); err == nil {
					if b, ok2 := v.Bool(); ok2 {
						recursive = b
					}
				}
			}
		}
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("rm: callback required")
		}
		fsAsync(ctx, func() (engine.Value, error) {
			if recursive {
				return engine.Undefined(), os.RemoveAll(path)
			}
			return engine.Undefined(), os.Remove(path)
		}, cb)
		return engine.Undefined(), nil
	}))

	// rename(oldPath, newPath, callback)
	_ = m.Set("rename", engine.NewFunction("rename", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("rename: paths required")
		}
		oldPath, newPath := args[0].String(), args[1].String()
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("rename: callback required")
		}
		fsAsync(ctx, func() (engine.Value, error) {
			return engine.Undefined(), os.Rename(oldPath, newPath)
		}, cb)
		return engine.Undefined(), nil
	}))

	// copyFile(src, dest[, mode], callback)
	_ = m.Set("copyFile", engine.NewFunction("copyFile", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("copyFile: src and dest required")
		}
		src, dst := args[0].String(), args[1].String()
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("copyFile: callback required")
		}
		fsAsync(ctx, func() (engine.Value, error) {
			return engine.Undefined(), copyFile(src, dst)
		}, cb)
		return engine.Undefined(), nil
	}))

	// realpath(path[, options], callback)
	_ = m.Set("realpath", engine.NewFunction("realpath", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("realpath: path required")
		}
		path := args[0].String()
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("realpath: callback required")
		}
		fsAsync(ctx, func() (engine.Value, error) {
			real, err := filepath.EvalSymlinks(path)
			if err != nil {
				return engine.Undefined(), err
			}
			abs, err := filepath.Abs(real)
			if err != nil {
				return engine.Undefined(), err
			}
			return engine.Str(abs), nil
		}, cb)
		return engine.Undefined(), nil
	}))

	// truncate(path[, len], callback)
	_ = m.Set("truncate", engine.NewFunction("truncate", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("truncate: path required")
		}
		path := args[0].String()
		size := int64(0)
		if len(args) > 1 && args[1].Type() == engine.TypeNumber {
			if n, ok := args[1].Float(); ok {
				size = int64(n)
			}
		}
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("truncate: callback required")
		}
		fsAsync(ctx, func() (engine.Value, error) {
			return engine.Undefined(), os.Truncate(path, size)
		}, cb)
		return engine.Undefined(), nil
	}))
}

// args0 取 args 的第一个字符串参数。
func args0(args []engine.Value) string {
	if len(args) > 0 {
		return args[0].String()
	}
	return ""
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
								if _, err := f.Call([]engine.Value{
									engine.Str("change"),
									engine.Str(eventType),
									engine.Str(filepath.Base(ev.Name)),
								}); err != nil {
									interpreter.ReportUncaught(nil, err)
								}
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
								if _, err := f.Call([]engine.Value{engine.Str("error"), engine.Str(werr.Error())}); err != nil {
									interpreter.ReportUncaught(nil, err)
								}
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
					if _, err := f.Call([]engine.Value{engine.Str("change"), listener}); err != nil {
						interpreter.ReportUncaught(nil, err)
					}
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
							val = gbuffer.NewBufferInstance(chunk)
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

// fsMakeTempDir 创建临时目录。Node 的 mkdtemp prefix 可含目录部分
// （如 /tmp/foo-），Go 的 os.MkdirTemp 不允许 pattern 含分隔符，需拆分。
func fsMakeTempDir(prefix string) (string, error) {
	dir := filepath.Dir(prefix)
	pattern := filepath.Base(prefix)
	if dir == "." || dir == "" {
		return os.MkdirTemp("", pattern)
	}
	return os.MkdirTemp(dir, pattern)
}

// fsReadError 规范化读错误：Windows 上读取目录返回 ERROR_INVALID_FUNCTION，
// 与 Node 的 EISDIR 语义不一致，这里改写为 EISDIR（仅在实际为目录时）。
func fsReadError(path string, err error) error {
	if err == nil || errors.Is(err, syscall.EISDIR) || errors.Is(err, os.ErrNotExist) {
		return err
	}
	if info, serr := os.Stat(path); serr == nil && info.IsDir() {
		return &os.PathError{Op: "read", Path: path, Err: syscall.EISDIR}
	}
	return err
}

// fsRmdirError 规范化 rmdir 错误：Windows 上删除非空目录返回的 errno 同时
// 匹配 os.ErrExist（EEXIST）与 syscall.ENOTEMPTY，errnoTable 按顺序先命中
// EEXIST。这里在目录非空时改用 Code 错误强制 ENOTEMPTY（Node 语义）。
func fsRmdirError(path string, err error) error {
	if err == nil {
		return err
	}
	if entries, serr := os.ReadDir(path); serr == nil && len(entries) > 0 {
		return nodebase.NewFSCodeError("ENOTEMPTY", fmt.Sprintf("ENOTEMPTY: directory not empty, rmdir '%s'", path))
	}
	return err
}

// fsErrorToJS 把 Go 错误转成带 Node code/errno/path/syscall 的 JS Error
// （供回调/异步错误使用；同步 API 直接返回错误由 interpreter 层转换）。
func fsErrorToJS(ctx engine.Context, err error) engine.Value {
	ev := nodebase.MakeErrorValue(ctx, err)
	obj, _ := ev.AsObject()
	if obj == nil {
		return ev
	}
	// 系统错误：*os.PathError / *os.LinkError / *fs.PathError。
	if pe, ok := asPathErrorGo(err); ok {
		code, desc, errnoNum := fsErrnoInfo(pe.Err)
		if code != "" {
			_ = obj.Set("code", engine.Str(code))
			_ = obj.Set("errno", engine.IntValue(errnoNum))
			op := pe.Op
			if op == "" {
				op = "syscall"
			}
			_ = obj.Set("message", engine.Str(fmt.Sprintf("%s: %s, %s '%s'", code, desc, op, pe.Path)))
			_ = obj.Set("path", engine.Str(pe.Path))
			_ = obj.Set("syscall", engine.Str(op))
			return ev
		}
	}
	if ce, ok := err.(interface{ Code() string }); ok {
		_ = obj.Set("code", engine.Str(ce.Code()))
	}
	return ev
}

// asPathErrorGo 从错误链提取 *os.PathError（与 interpreter 层逻辑一致）。
func asPathErrorGo(err error) (*os.PathError, bool) {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe, true
	}
	var fe *fs.PathError
	if errors.As(err, &fe) {
		return &os.PathError{Op: fe.Op, Path: fe.Path, Err: fe.Err}, true
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return &os.PathError{Op: le.Op, Path: le.New, Err: le.Err}, true
	}
	return nil, false
}

// fsErrnoInfo errno → Node code/desc/errno 数值（与 interpreter 层 errnoTable
// 一致；errnoTable 顺序敏感——os.ErrExist 在 syscall.ENOTEMPTY 之前）。
func fsErrnoInfo(err error) (code, desc string, errno int) {
	for _, e := range builtinErrnoTable {
		if errors.Is(err, e.match) {
			if runtime.GOOS == "windows" {
				return e.code, e.desc, e.win
			}
			return e.code, e.desc, e.unix
		}
	}
	return "", "", 0
}

type builtinErrnoEntry struct {
	match error
	code  string
	desc  string
	win   int
	unix  int
}

var builtinErrnoTable = []builtinErrnoEntry{
	{match: os.ErrNotExist, code: "ENOENT", desc: "no such file or directory", win: -4058, unix: -2},
	{match: os.ErrPermission, code: "EACCES", desc: "permission denied", win: -4092, unix: -13},
	{match: os.ErrExist, code: "EEXIST", desc: "file already exists", win: -4075, unix: -17},
	{match: os.ErrInvalid, code: "EINVAL", desc: "invalid argument", win: -4071, unix: -22},
	{match: os.ErrClosed, code: "EBADF", desc: "bad file descriptor", win: -4083, unix: -9},
	// Windows ERROR_INVALID_HANDLE（关闭后读写）→ EBADF（Node 语义）。
	{match: syscall.Errno(6), code: "EBADF", desc: "bad file descriptor", win: -4083, unix: -9},
	{match: syscall.EISDIR, code: "EISDIR", desc: "illegal operation on a directory", win: -4069, unix: -21},
	{match: syscall.ENOTDIR, code: "ENOTDIR", desc: "not a directory", win: -4052, unix: -20},
	{match: syscall.ENOTEMPTY, code: "ENOTEMPTY", desc: "directory not empty", win: -4074, unix: -39},
	{match: syscall.EPERM, code: "EPERM", desc: "operation not permitted", win: -4048, unix: -1},
	{match: syscall.ENOSPC, code: "ENOSPC", desc: "no space left on device", win: -4081, unix: -28},
	{match: syscall.EMFILE, code: "EMFILE", desc: "too many open files", win: -4064, unix: -24},
	{match: syscall.ENAMETOOLONG, code: "ENAMETOOLONG", desc: "name too long", win: -4070, unix: -36},
	{match: syscall.EROFS, code: "EROFS", desc: "read-only file system", win: -4078, unix: -30},
	{match: syscall.ENOSYS, code: "ENOSYS", desc: "function not implemented", win: -4086, unix: -38},
	{match: syscall.EBUSY, code: "EBUSY", desc: "resource busy or locked", win: -4087, unix: -16},
}

// fsAsync 通用异步回调包装：goroutine 执行 op，完成后 PostTask 回 JS 线程
// 调用 (err, value) 回调。
func fsAsync(ctx engine.Context, op func() (engine.Value, error), cb engine.Value) {
	release := ctx.AddRef()
	go func() {
		val, err := op()
		ctx.PostTask(func() {
			defer release()
			if !cb.IsFunction() {
				return
			}
			f, _ := cb.AsFunction()
			if err != nil {
				if _, err := f.Call([]engine.Value{fsErrorToJS(ctx, err)}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			} else {
				if _, err := f.Call([]engine.Value{engine.Null(), val}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		})
	}()
}

// fsCbArg 提取最后一个函数参数作为回调。
func fsCbArg(args []engine.Value) (engine.Value, bool) {
	for i := len(args) - 1; i >= 0; i-- {
		if args[i].IsFunction() {
			return args[i], true
		}
	}
	return engine.Undefined(), false
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
				if _, err := f.Call(args[1:]); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
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
			if _, err := f.Call(callArgs); err != nil {
				interpreter.ReportUncaught(nil, err)
			}
		}
	}
}

// statToObj 将 os.FileInfo 转为 Node.js Stats 对象。
func statToObj(ctx engine.Context, info fs.FileInfo) engine.Value {
	obj := engine.NewObject()
	atimeMs, mtimeMs, ctimeMs, birthtimeMs := statSysTimes(info)
	// Node mode 含文件类型位（S_IFMT）。
	fileType := int64(0)
	if info.IsDir() {
		fileType = 0o040000 // S_IFDIR
	} else if info.Mode()&fs.ModeSymlink != 0 {
		fileType = 0o120000 // S_IFLNK
	} else if info.Mode()&fs.ModeSocket != 0 {
		fileType = 0o140000 // S_IFSOCK
	} else if info.Mode()&fs.ModeNamedPipe != 0 {
		fileType = 0o010000 // S_IFIFO
	} else if info.Mode()&fs.ModeDevice != 0 {
		fileType = 0o020000 // S_IFCHR
		if info.Mode()&fs.ModeCharDevice == 0 {
			fileType = 0o060000 // S_IFBLK
		}
	} else {
		fileType = 0o100000 // S_IFREG
	}
	mode := fileType | int64(info.Mode().Perm())
	nlink, uid, gid, ino, dev, rdev, blksize, blocks := statSysNumbers(info)
	_ = obj.Set("size", engine.IntValue(int(info.Size())))
	_ = obj.Set("mode", engine.IntValue(int(mode)))
	_ = obj.Set("mtimeMs", engine.Number(mtimeMs))
	_ = obj.Set("ctimeMs", engine.Number(ctimeMs))
	_ = obj.Set("atimeMs", engine.Number(atimeMs))
	_ = obj.Set("birthtimeMs", engine.Number(birthtimeMs))
	// 时间 Date 对象（Node 语义：mtime 等为 Date 实例）。
	_ = obj.Set("mtime", newStatDate(ctx, mtimeMs))
	_ = obj.Set("ctime", newStatDate(ctx, ctimeMs))
	_ = obj.Set("atime", newStatDate(ctx, atimeMs))
	_ = obj.Set("birthtime", newStatDate(ctx, birthtimeMs))
	_ = obj.Set("nlink", engine.IntValue(int(nlink)))
	_ = obj.Set("uid", engine.IntValue(int(uid)))
	_ = obj.Set("gid", engine.IntValue(int(gid)))
	_ = obj.Set("rdev", engine.IntValue(int(rdev)))
	_ = obj.Set("blksize", engine.IntValue(int(blksize)))
	_ = obj.Set("blocks", engine.IntValue(int(blocks)))
	_ = obj.Set("ino", engine.IntValue(int(ino)))
	_ = obj.Set("dev", engine.IntValue(int(dev)))

	// 方法形式（Node.js Stats.isFile() 是方法）
	_ = obj.Set("isFile", engine.NewFunction("isFile", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(info.Mode().IsRegular()), nil
	}))
	_ = obj.Set("isDirectory", engine.NewFunction("isDirectory", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(info.IsDir()), nil
	}))
	_ = obj.Set("isSymbolicLink", engine.NewFunction("isSymbolicLink", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(info.Mode()&fs.ModeSymlink != 0), nil
	}))
	_ = obj.Set("isBlockDevice", engine.NewFunction("isBlockDevice", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(info.Mode()&fs.ModeDevice != 0 && info.Mode()&fs.ModeCharDevice == 0), nil
	}))
	_ = obj.Set("isCharacterDevice", engine.NewFunction("isCharacterDevice", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(info.Mode()&fs.ModeDevice != 0 && info.Mode()&fs.ModeCharDevice != 0), nil
	}))
	_ = obj.Set("isFIFO", engine.NewFunction("isFIFO", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(info.Mode()&fs.ModeNamedPipe != 0), nil
	}))
	_ = obj.Set("isSocket", engine.NewFunction("isSocket", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(info.Mode()&fs.ModeSocket != 0), nil
	}))
	return obj
}

// newStatDate 构造 Date 对象（带 Date.prototype，支持 instanceof Date 与
// toISOString 等原型方法）。
func newStatDate(ctx engine.Context, ms float64) engine.Value {
	d := engine.NewDateValue(ms)
	if ctorV, err := ctx.Global().Get("Date"); err == nil && ctorV.IsFunction() {
		if co, ok := ctorV.AsObject(); ok {
			if pv, err := co.Get("prototype"); err == nil {
				if proto, ok := pv.AsObject(); ok {
					engine.SetProto(d, proto)
				}
			}
		}
	}
	return d
}

// makeFsDirent 构造 Node.js Dirent 对象（name/parentPath + 类型方法）。
func makeFsDirent(name, parentPath string, t fs.FileMode) engine.Value {
	d := engine.NewObject()
	_ = d.Set("name", engine.Str(name))
	_ = d.Set("parentPath", engine.Str(parentPath))
	_ = d.Set("isFile", engine.NewFunction("isFile", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(t.IsRegular()), nil
	}))
	_ = d.Set("isDirectory", engine.NewFunction("isDirectory", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(t.IsDir()), nil
	}))
	_ = d.Set("isSymbolicLink", engine.NewFunction("isSymbolicLink", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(t&fs.ModeSymlink != 0), nil
	}))
	_ = d.Set("isBlockDevice", engine.NewFunction("isBlockDevice", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(t&fs.ModeDevice != 0 && t&fs.ModeCharDevice == 0), nil
	}))
	_ = d.Set("isCharacterDevice", engine.NewFunction("isCharacterDevice", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(t&fs.ModeDevice != 0 && t&fs.ModeCharDevice != 0), nil
	}))
	_ = d.Set("isFIFO", engine.NewFunction("isFIFO", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(t&fs.ModeNamedPipe != 0), nil
	}))
	_ = d.Set("isSocket", engine.NewFunction("isSocket", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(t&fs.ModeSocket != 0), nil
	}))
	return d
}
