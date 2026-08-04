package builtin

// node:fs/promises 内置模块（fs 的 Promise 版本，开发计划 3.16 补全）。
//
// 实现：真异步——Go goroutine 执行 fs 操作，完成后经 ctx.PostTask 回 JS
// 线程 resolve/reject Promise。

import (
	"fmt"
	"io"
	"os"

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
					callBuiltinResolve(reject, engine.Str(err.Error()))
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
