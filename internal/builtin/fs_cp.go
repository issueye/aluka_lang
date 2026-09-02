package builtin

// Node fs.cp/cpSync 兼容实现。
// 语义（node 22 实测）：默认 {force:true, errorOnExist:false, recursive:false}；
// 非 recursive 复制目录 → ERR_FS_EISDIR；dest 为 src 子目录 → ERR_FS_CP_EINVAL；
// force:false 跳过已存在目标；errorOnExist:true 遇已存在目标报错；filter 返回
// false 跳过条目。

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/aluka-lang/aluka/internal/engine"
)

// fsCpOptions cp 选项。
type fsCpOptions struct {
	force              bool
	errorOnExist       bool
	recursive          bool
	dereference        bool
	preserveTimestamps bool
	filter             engine.Value // 函数或 Undefined
}

func parseFsCpOptions(opts engine.Value) fsCpOptions {
	o := fsCpOptions{force: true}
	if opts.IsUndefined() {
		return o
	}
	oo, ok := opts.AsObject()
	if !ok {
		return o
	}
	// 注意：Undefined().Bool() 返回 (false, true)——缺失属性必须用
	// IsUndefined 先行排除，否则默认值会被 false 覆盖。
	if v, err := oo.Get("force"); err == nil && !v.IsUndefined() {
		if b, ok2 := v.Bool(); ok2 {
			o.force = b
		}
	}
	if v, err := oo.Get("errorOnExist"); err == nil && !v.IsUndefined() {
		if b, ok2 := v.Bool(); ok2 {
			o.errorOnExist = b
		}
	}
	if v, err := oo.Get("recursive"); err == nil && !v.IsUndefined() {
		if b, ok2 := v.Bool(); ok2 {
			o.recursive = b
		}
	}
	if v, err := oo.Get("dereference"); err == nil && !v.IsUndefined() {
		if b, ok2 := v.Bool(); ok2 {
			o.dereference = b
		}
	}
	if v, err := oo.Get("preserveTimestamps"); err == nil && !v.IsUndefined() {
		if b, ok2 := v.Bool(); ok2 {
			o.preserveTimestamps = b
		}
	}
	if v, err := oo.Get("filter"); err == nil && !v.IsUndefined() {
		if v.IsFunction() {
			o.filter = v
		}
	}
	return o
}

// fsCpFilter 调用 JS filter(src, dest)，返回是否保留。
func fsCpFilter(fn engine.Value, src, dest string) (bool, error) {
	if fn == nil || !fn.IsFunction() {
		return true, nil
	}
	f, ok := fn.AsFunction()
	if !ok {
		return true, nil
	}
	v, err := f.Call([]engine.Value{engine.Str(src), engine.Str(dest)})
	if err != nil {
		return false, err
	}
	b, _ := v.Bool()
	return b, nil
}

// fsCpSyncImpl cpSync 主体。
func fsCpSyncImpl(args []engine.Value) (engine.Value, error) {
	if len(args) < 2 {
		return engine.Undefined(), fmt.Errorf("cpSync: src and dest required")
	}
	src := args[0].String()
	dest := args[1].String()
	var opts fsCpOptions
	if len(args) > 2 {
		opts = parseFsCpOptions(args[2])
	}
	if err := fsCpCopy(src, dest, opts, true); err != nil {
		return engine.Undefined(), err
	}
	return engine.Undefined(), nil
}

// fsCpCopy 复制入口（isTop 时检查 dest 在 src 内部）。
func fsCpCopy(src, dest string, opts fsCpOptions, isTop bool) error {
	if isTop && opts.recursive {
		absSrc, err1 := filepath.Abs(src)
		absDest, err2 := filepath.Abs(dest)
		if err1 == nil && err2 == nil {
			rel, err := filepath.Rel(absSrc, absDest)
			if err == nil && rel != "." && rel != ".." && !isPathParent(rel) {
				return &fsCodeError{code: "ERR_FS_CP_EINVAL",
					msg: fmt.Sprintf("Cannot copy %s to a subdirectory of self %s", absSrc, absDest)}
			}
		}
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if !opts.recursive {
			abs, _ := filepath.Abs(src)
			return &fsCodeError{code: "ERR_FS_EISDIR",
				msg: fmt.Sprintf("Recursive option not enabled, cannot copy a directory: %s", abs)}
		}
		return fsCpCopyDir(src, dest, opts)
	}
	return fsCpCopyFile(src, dest, opts)
}

// isPathParent 判断 rel 是否以 .. 开头（目标在源之外）。
func isPathParent(rel string) bool {
	return rel == ".." || len(rel) > 2 && rel[:3] == ".."+string(filepath.Separator)
}

// fsCpCopyDir 递归复制目录树。
func fsCpCopyDir(src, dest string, opts fsCpOptions) error {
	keep, err := fsCpFilter(opts.filter, src, dest)
	if err != nil {
		return err
	}
	if !keep {
		return nil
	}
	if err := fsCpEnsureDest(src, dest, opts); err != nil {
		if err == errDestExists {
			return nil // force:false 跳过已存在目录
		}
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dest, e.Name())
		if e.IsDir() {
			if err := fsCpCopyDir(s, d, opts); err != nil {
				return err
			}
		} else {
			if err := fsCpCopyFile(s, d, opts); err != nil {
				return err
			}
		}
	}
	return nil
}

// fsCpEnsureDest 处理目标存在语义。Node 语义：errorOnExist 仅在 force:false
// 时生效（force:true 默认时忽略）；force:false 且已存在 → 跳过（不报错）。
func fsCpEnsureDest(src, dest string, opts fsCpOptions) error {
	_, err := os.Lstat(dest)
	if err == nil {
		if !opts.force {
			if opts.errorOnExist {
				return &fsCodeError{code: "", msg: fmt.Sprintf(", The file exists. %s", dest)}
			}
			// force:false 且已存在：跳过（调用方检测存在性，此处返回特殊错误）。
			return errDestExists
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	// 目标不存在：创建父目录（文件场景由 WriteFile 处理）。
	info, serr := os.Stat(src)
	if serr == nil && info.IsDir() {
		return os.MkdirAll(dest, 0755)
	}
	return nil
}

// errDestExists 表示 force:false 且目标已存在（跳过，不视为错误）。
var errDestExists = fmt.Errorf("destination exists (skip)")

// fsCpCopyFile 复制单个文件（force/errorOnExist 语义）。
func fsCpCopyFile(src, dest string, opts fsCpOptions) error {
	keep, err := fsCpFilter(opts.filter, src, dest)
	if err != nil {
		return err
	}
	if !keep {
		return nil
	}
	_, err = os.Lstat(dest)
	if err == nil {
		if !opts.force {
			if opts.errorOnExist {
				return &fsCodeError{code: "", msg: fmt.Sprintf(", The file exists. %s", dest)}
			}
			return nil // force:false 跳过已存在文件
		}
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0644)
}
