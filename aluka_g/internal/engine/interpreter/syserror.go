package interpreter

// Node 风格系统错误映射：Go 的 *os.PathError / *os.LinkError / *fs.PathError
// → Node 的 SystemError 语义（code/errno/path/syscall + 规范 message）。
// 供 goErrorToJSValue 使用，让 fs 等模块的系统错误携带 Node 兼容字段。

import (
	"errors"
	"io/fs"
	"os"
	"runtime"
	"syscall"
)

// asPathError 从错误链中提取 *os.PathError（含 LinkError 与 fs.PathError）。
func asPathError(err error) (*os.PathError, bool) {
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

// errnoEntry 描述一个 errno 到 Node 码的映射。
type errnoEntry struct {
	match error
	code  string
	desc  string
	win   int // libuv errno（Windows）
	unix  int // errno（POSIX 负数）
}

var errnoTable = []errnoEntry{
	{match: os.ErrNotExist, code: "ENOENT", desc: "no such file or directory", win: -4058, unix: -2},
	{match: os.ErrPermission, code: "EACCES", desc: "permission denied", win: -4092, unix: -13},
	{match: os.ErrExist, code: "EEXIST", desc: "file already exists", win: -4075, unix: -17},
	{match: os.ErrInvalid, code: "EINVAL", desc: "invalid argument", win: -4071, unix: -22},
	{match: os.ErrClosed, code: "EBADF", desc: "bad file descriptor", win: -4083, unix: -9},
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

// nodeErrnoInfo 返回 errno 对应的 Node code、描述与 libuv errno 数值。
func nodeErrnoInfo(err error) (code, desc string, errno int) {
	for _, e := range errnoTable {
		if errors.Is(err, e.match) {
			if runtime.GOOS == "windows" {
				return e.code, e.desc, e.win
			}
			return e.code, e.desc, e.unix
		}
	}
	return "", "", 0
}
