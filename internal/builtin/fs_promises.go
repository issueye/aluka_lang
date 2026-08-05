package builtin

// node:fs/promises 内置模块（fs 的 Promise 版本，开发计划 3.16 补全）。
//
// 实现：真异步——Go goroutine 执行 fs 操作，完成后经 ctx.PostTask 回 JS
// 线程 resolve/reject Promise。

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

// NewFSPromises 构造 node:fs/promises 模块导出对象。
func NewFSPromises(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()

	// readFile(path[, encoding]) → Promise<Buffer|string>
	_ = m.Set("readFile", engine.NewFunction("readFile", func(args []engine.Value) (engine.Value, error) {
		path, encoding := fsPathArgs(args)
		return fsPromise(ctx, args, func() (engine.Value, error) {
			data, err := os.ReadFile(path)
			if err != nil {
				return engine.Undefined(), err
			}
			if encoding != "" {
				return engine.Str(string(data)), nil
			}
			return globals.NewBufferInstance(data), nil
		})
	}))

	// writeFile(path, data) → Promise
	_ = m.Set("writeFile", engine.NewFunction("writeFile", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("writeFile: path and data required")
		}
		path := args[0].String()
		data := fsDataBytes(args[1])
		return fsPromise(ctx, args, func() (engine.Value, error) {
			return engine.Undefined(), os.WriteFile(path, data, 0644)
		})
	}))

	// appendFile(path, data) → Promise
	_ = m.Set("appendFile", engine.NewFunction("appendFile", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("appendFile: path and data required")
		}
		path := args[0].String()
		data := fsDataBytes(args[1])
		return fsPromise(ctx, args, func() (engine.Value, error) {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return engine.Undefined(), err
			}
			defer f.Close()
			_, err = f.Write(data)
			return engine.Undefined(), err
		})
	}))

	// mkdir(path[, options]) → Promise
	_ = m.Set("mkdir", engine.NewFunction("mkdir", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("mkdir: path required")
		}
		path := args[0].String()
		recursive := false
		if len(args) > 1 && args[1].IsObject() {
			if o, ok := args[1].AsObject(); ok {
				if v, err := o.Get("recursive"); err == nil {
					if b, ok := v.Bool(); ok {
						recursive = b
					}
				}
			}
		}
		return fsPromise(ctx, args, func() (engine.Value, error) {
			if recursive {
				return engine.Undefined(), os.MkdirAll(path, 0755)
			}
			return engine.Undefined(), os.Mkdir(path, 0755)
		})
	}))

	// readdir(path) → Promise<string[]>
	_ = m.Set("readdir", engine.NewFunction("readdir", func(args []engine.Value) (engine.Value, error) {
		path := ""
		if len(args) > 0 {
			path = args[0].String()
		}
		return fsPromise(ctx, args, func() (engine.Value, error) {
			entries, err := os.ReadDir(path)
			if err != nil {
				return engine.Undefined(), err
			}
			vals := make([]engine.Value, len(entries))
			for i, e := range entries {
				vals[i] = engine.Str(e.Name())
			}
			return engine.NewArray(vals), nil
		})
	}))

	// stat(path) → Promise<Stats>
	_ = m.Set("stat", engine.NewFunction("stat", func(args []engine.Value) (engine.Value, error) {
		path := ""
		if len(args) > 0 {
			path = args[0].String()
		}
		return fsPromise(ctx, args, func() (engine.Value, error) {
			info, err := os.Stat(path)
			if err != nil {
				return engine.Undefined(), err
			}
			return statToObj(info), nil
		})
	}))

	// unlink(path) → Promise
	_ = m.Set("unlink", engine.NewFunction("unlink", func(args []engine.Value) (engine.Value, error) {
		path := ""
		if len(args) > 0 {
			path = args[0].String()
		}
		return fsPromise(ctx, args, func() (engine.Value, error) {
			return engine.Undefined(), os.Remove(path)
		})
	}))

	// rm(path[, options]) → Promise
	_ = m.Set("rm", engine.NewFunction("rm", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("rm: path required")
		}
		path := args[0].String()
		recursive := false
		if len(args) > 1 && args[1].IsObject() {
			if o, ok := args[1].AsObject(); ok {
				if v, err := o.Get("recursive"); err == nil {
					if b, ok := v.Bool(); ok {
						recursive = b
					}
				}
			}
		}
		return fsPromise(ctx, args, func() (engine.Value, error) {
			if recursive {
				return engine.Undefined(), os.RemoveAll(path)
			}
			return engine.Undefined(), os.Remove(path)
		})
	}))

	// rename(oldPath, newPath) → Promise
	_ = m.Set("rename", engine.NewFunction("rename", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("rename: paths required")
		}
		oldPath, newPath := args[0].String(), args[1].String()
		return fsPromise(ctx, args, func() (engine.Value, error) {
			return engine.Undefined(), os.Rename(oldPath, newPath)
		})
	}))

	// copyFile(src, dest) → Promise
	_ = m.Set("copyFile", engine.NewFunction("copyFile", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("copyFile: src and dest required")
		}
		src, dst := args[0].String(), args[1].String()
		return fsPromise(ctx, args, func() (engine.Value, error) {
			return engine.Undefined(), copyFile(src, dst)
		})
	}))

	// access(path) → Promise
	_ = m.Set("access", engine.NewFunction("access", func(args []engine.Value) (engine.Value, error) {
		path := ""
		if len(args) > 0 {
			path = args[0].String()
		}
		return fsPromise(ctx, args, func() (engine.Value, error) {
			_, err := os.Stat(path)
			return engine.Undefined(), err
		})
	}))

	addFSPromisesExtras(ctx, m)

	return m, nil
}

// fsPromise 把 Go 异步操作包装为 Promise（goroutine + PostTask）。
func fsPromise(ctx engine.Context, args []engine.Value, op func() (engine.Value, error)) (engine.Value, error) {
	executor := engine.NewFunction("executor", func(ea []engine.Value) (engine.Value, error) {
		if len(ea) < 2 {
			return engine.Undefined(), nil
		}
		resolve, reject := ea[0], ea[1]
		release := ctx.AddRef()
		go func() {
			val, err := op()
			ctx.PostTask(func() {
				defer release()
				if err != nil {
					callBuiltinResolve(reject, builtinErrorValue(ctx, err.Error()))
				} else {
					callBuiltinResolve(resolve, val)
				}
			})
		}()
		return engine.Undefined(), nil
	})
	return newBuiltinPromise(ctx, executor)
}

// callBuiltinResolve 调用 Promise resolve/reject 函数。
func callBuiltinResolve(fn engine.Value, v engine.Value) {
	if f, ok := fn.AsFunction(); ok {
		_, _ = f.Call([]engine.Value{v})
	}
}

// fsPathArgs 提取 path 与编码参数。
func fsPathArgs(args []engine.Value) (string, string) {
	path := ""
	encoding := ""
	if len(args) > 0 {
		path = args[0].String()
	}
	if len(args) > 1 {
		switch args[1].Type() {
		case engine.TypeString:
			encoding = args[1].String()
		default:
			if o, ok := args[1].AsObject(); ok {
				if v, err := o.Get("encoding"); err == nil && !v.IsUndefined() {
					encoding = v.String()
				}
			}
		}
	}
	return path, encoding
}

// fsDataBytes 把数据参数转为字节。
func fsDataBytes(v engine.Value) []byte {
	if b, ok := engine.AsBuffer(v); ok {
		return b
	}
	return []byte(v.String())
}

// copyFile 复制文件。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// addFSPromisesExtras 补全 fs/promises 常用方法（Pi 的 nodejs.ts 全量 API）。
// mkdtemp / lstat / realpath / opendir。
func addFSPromisesExtras(ctx engine.Context, m engine.Object) {
	_ = m.Set("mkdtemp", engine.NewFunction("mkdtemp", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("mkdtemp: prefix required")
		}
		prefix := args[0].String()
		return fsPromise(ctx, args, func() (engine.Value, error) {
			dir, err := os.MkdirTemp("", prefix)
			if err != nil {
				return engine.Undefined(), err
			}
			return engine.Str(dir), nil
		})
	}))

	_ = m.Set("lstat", engine.NewFunction("lstat", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("lstat: path required")
		}
		path := args[0].String()
		return fsPromise(ctx, args, func() (engine.Value, error) {
			info, err := os.Lstat(path)
			if err != nil {
				return engine.Undefined(), err
			}
			return fsStatToObject(info), nil
		})
	}))

	_ = m.Set("realpath", engine.NewFunction("realpath", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("realpath: path required")
		}
		path := args[0].String()
		return fsPromise(ctx, args, func() (engine.Value, error) {
			real, err := filepath.EvalSymlinks(path)
			if err != nil {
				return engine.Undefined(), err
			}
			abs, err := filepath.Abs(real)
			if err != nil {
				return engine.Undefined(), err
			}
			return engine.Str(abs), nil
		})
	}))

	_ = m.Set("opendir", engine.NewFunction("opendir", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("opendir: path required")
		}
		path := args[0].String()
		return fsPromise(ctx, args, func() (engine.Value, error) {
			entries, err := os.ReadDir(path)
			if err != nil {
				return engine.Undefined(), err
			}
			out := make([]engine.Value, len(entries))
			for i, e := range entries {
				d := engine.NewObject()
				_ = d.Set("name", engine.Str(e.Name()))
				_ = d.Set("isDirectory", engine.NewFunction("isDirectory", func(args []engine.Value) (engine.Value, error) {
					return engine.Boolean(e.IsDir()), nil
				}))
				_ = d.Set("isFile", engine.NewFunction("isFile", func(args []engine.Value) (engine.Value, error) {
					return engine.Boolean(e.Type().IsRegular()), nil
				}))
				_ = d.Set("isSymbolicLink", engine.NewFunction("isSymbolicLink", func(args []engine.Value) (engine.Value, error) {
					return engine.Boolean(e.Type()&os.ModeSymlink != 0), nil
				}))
				out[i] = d
			}
			dir := engine.NewObject()
			_ = dir.Set("read", engine.NewFunction("read", func(args []engine.Value) (engine.Value, error) {
				return engine.Null(), nil // 简化：一次返回全部（Dirent 对象）
			}))
			_ = dir.Set("readSync", engine.NewFunction("readSync", func(args []engine.Value) (engine.Value, error) {
				return engine.Null(), nil
			}))
			_ = dir.Set("close", engine.NewFunction("close", func(args []engine.Value) (engine.Value, error) {
				return engine.Undefined(), nil
			}))
			_ = dir.Set("closeSync", engine.NewFunction("closeSync", func(args []engine.Value) (engine.Value, error) {
				return engine.Undefined(), nil
			}))
			_ = dir.Set("path", engine.Str(path))
			_ = dir.Set("dirents", engine.NewArray(out))
			return dir, nil
		})
	}))
}

// fsStatToObject 构造 Stat 对象（复用 fs.go 的字段集合）。
func fsStatToObject(info os.FileInfo) engine.Value {
	obj := engine.NewObject()
	_ = obj.Set("size", engine.IntValue(int(info.Size())))
	_ = obj.Set("mtime", engine.Number(float64(info.ModTime().UnixMilli())))
	_ = obj.Set("mtimeMs", engine.Number(float64(info.ModTime().UnixMilli())))
	_ = obj.Set("atimeMs", engine.Number(float64(info.ModTime().UnixMilli())))
	_ = obj.Set("ctimeMs", engine.Number(float64(info.ModTime().UnixMilli())))
	_ = obj.Set("birthtimeMs", engine.Number(float64(info.ModTime().UnixMilli())))
	_ = obj.Set("mode", engine.IntValue(int(info.Mode())))
	_ = obj.Set("isFile", engine.NewFunction("isFile", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(info.Mode().IsRegular()), nil
	}))
	_ = obj.Set("isDirectory", engine.NewFunction("isDirectory", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(info.IsDir()), nil
	}))
	_ = obj.Set("isSymbolicLink", engine.NewFunction("isSymbolicLink", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(info.Mode()&os.ModeSymlink != 0), nil
	}))
	return obj
}

// builtinErrorValue 构造 JS Error 对象（带 message）。回退为字符串。
func builtinErrorValue(ctx engine.Context, msg string) engine.Value {
	if ctor, err := ctx.Global().Get("Error"); err == nil && ctor.IsFunction() {
		if f, ok := ctor.AsFunction(); ok {
			if v, cerr := f.Call([]engine.Value{engine.Str(msg)}); cerr == nil {
				return v
			}
		}
	}
	return engine.Str(msg)
}
