package builtin

// node:fs 内置模块——文件系统操作。
// 本文件实现同步 API（*Sync 后缀）。异步 API（回调/Promise）在后续阶段补充。
// 基于 Go os/io 标准库。

import (
	"encoding/base64"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aluka-lang/aluka/internal/engine"
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

	// --- 常量 ---
	constants := engine.NewObject()
	_ = constants.Set("F_OK", engine.IntValue(0))
	_ = constants.Set("R_OK", engine.IntValue(4))
	_ = constants.Set("W_OK", engine.IntValue(2))
	_ = constants.Set("X_OK", engine.IntValue(1))
	_ = m.Set("constants", constants)

	return m, nil
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
