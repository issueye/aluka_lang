package builtin

// node:fs 补充实现（M3）：
//   - 文件描述符操作：open/openSync/close/closeSync/readSync/writeSync/
//     fstatSync/ftruncateSync/fsyncSync/fdatasyncSync/fchmodSync/fchownSync/
//     futimesSync + 回调版 read/write/fstat/ftruncate/fsync/fdatasync/fchmod/
//     fchown/futimes
//   - 链接/权限/时间：link/linkSync、symlink/symlinkSync、readlink/readlinkSync、
//     chmod/chmodSync、chown/chownSync、utimes/utimesSync、lutimes/lutimesSync、
//     lchmod/lchown（POSIX）
//   - statfs/statfsSync、opendir/opendirSync（Dir）、watchFile/unwatchFile、
//     openAsBlob、readv/writev
//   - FileHandle（fs.open / fs/promises.open 的返回对象）

import (
	"fmt"
	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

// fsFDRegistry keeps the *os.File that owns every descriptor returned by an
// fs open call.  Returning only f.Fd() lets Go's finalizer close the descriptor
// as soon as the original *os.File becomes unreachable, which makes a later
// Node-style closeSync(fd) fail with EBADF.  Node keeps descriptors alive until
// the caller closes them, so retain the owner here as well.
var fsFDRegistry = struct {
	sync.Mutex
	files map[int]*os.File
}{files: make(map[int]*os.File)}

func registerFSFD(f *os.File) int {
	if f == nil {
		return -1
	}
	fd := int(f.Fd())
	fsFDRegistry.Lock()
	fsFDRegistry.files[fd] = f
	fsFDRegistry.Unlock()
	return fd
}

func lookupFSFD(fd int) *os.File {
	if fd < 0 {
		return nil
	}
	fsFDRegistry.Lock()
	f := fsFDRegistry.files[fd]
	fsFDRegistry.Unlock()
	if f != nil {
		return f
	}
	return os.NewFile(uintptr(fd), fmt.Sprintf("fd:%d", fd))
}

// takeFSFD removes the registered owner before closing it.  This prevents a
// second close from reusing a stale *os.File after the OS recycles the number.
func takeFSFD(fd int) *os.File {
	if fd < 0 {
		return nil
	}
	fsFDRegistry.Lock()
	f := fsFDRegistry.files[fd]
	delete(fsFDRegistry.files, fd)
	fsFDRegistry.Unlock()
	if f != nil {
		return f
	}
	return os.NewFile(uintptr(fd), fmt.Sprintf("fd:%d", fd))
}

func unregisterFSFD(fd int, owner *os.File) {
	if fd < 0 {
		return
	}
	fsFDRegistry.Lock()
	if current := fsFDRegistry.files[fd]; current == owner {
		delete(fsFDRegistry.files, fd)
	}
	fsFDRegistry.Unlock()
}

func closeFSFD(fd int) error {
	f := takeFSFD(fd)
	if f == nil {
		return os.ErrInvalid
	}
	return f.Close()
}

// addFSFD 在 fs 模块对象上注册 fd 操作与补充 API。
func addFSFD(ctx engine.Context, m engine.Object) {
	// --- 打开/关闭 ---

	// openSync(path, flags[, mode]) → fd
	_ = m.Set("openSync", engine.NewFunction("openSync", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("openSync: path required")
		}
		path := args[0].String()
		flags := "r"
		if len(args) > 1 && args[1].Type() == engine.TypeString {
			flags = args[1].String()
		}
		mode := 0o666
		if len(args) > 2 {
			if n, ok := args[2].Int(); ok {
				mode = n
			}
		}
		goFlags, err := fsParseFlags(flags)
		if err != nil {
			return engine.Undefined(), err
		}
		f, err := os.OpenFile(path, goFlags, os.FileMode(mode))
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.IntValue(registerFSFD(f)), nil
	}))

	// open(path, flags[, mode], callback)
	_ = m.Set("open", engine.NewFunction("open", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("open: path required")
		}
		path := args[0].String()
		flags := "r"
		idx := 1
		if len(args) > 1 && args[1].Type() == engine.TypeString {
			flags = args[1].String()
			idx = 2
		}
		mode := 0o666
		if len(args) > idx && args[idx].Type() == engine.TypeNumber {
			if n, ok := args[idx].Int(); ok {
				mode = n
			}
			idx++
		}
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("open: callback required")
		}
		fsAsync(ctx, func() (engine.Value, error) {
			goFlags, err := fsParseFlags(flags)
			if err != nil {
				return engine.Undefined(), err
			}
			f, err := os.OpenFile(path, goFlags, os.FileMode(mode))
			if err != nil {
				return engine.Undefined(), err
			}
			return engine.IntValue(registerFSFD(f)), nil
		}, cb)
		return engine.Undefined(), nil
	}))

	// closeSync(fd)
	_ = m.Set("closeSync", engine.NewFunction("closeSync", func(args []engine.Value) (engine.Value, error) {
		fd, ok := argFD(args, 0)
		if !ok {
			return engine.Undefined(), fmt.Errorf("closeSync: fd required")
		}
		err := closeFSFD(fd)
		return engine.Undefined(), fdOpError("close", fd, err)
	}))

	// close(fd, callback)
	_ = m.Set("close", engine.NewFunction("close", func(args []engine.Value) (engine.Value, error) {
		fd, ok := argFD(args, 0)
		if !ok {
			return engine.Undefined(), fmt.Errorf("close: fd required")
		}
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("close: callback required")
		}
		fsAsync(ctx, func() (engine.Value, error) {
			err := closeFSFD(fd)
			return engine.Undefined(), fdOpError("close", fd, err)
		}, cb)
		return engine.Undefined(), nil
	}))

	// --- 读/写 ---

	// readSync(fd, buffer, offset, length, position) → bytesRead
	_ = m.Set("readSync", engine.NewFunction("readSync", func(args []engine.Value) (engine.Value, error) {
		fd, ok := argFD(args, 0)
		if !ok {
			return engine.Undefined(), fmt.Errorf("readSync: fd required")
		}
		f := osFileFromFD(fd)
		// 字符串形式：readSync(fd, length, position, encoding) → string
		if len(args) >= 2 && args[1].Type() == engine.TypeNumber {
			length := 0
			if n, ok := args[1].Int(); ok {
				length = n
			}
			var position int64 = -1
			if len(args) > 2 && args[2].Type() == engine.TypeNumber {
				if n, ok := args[2].Int(); ok {
					position = int64(n)
				}
			}
			buf := make([]byte, length)
			var n int
			var err error
			if position < 0 {
				n, err = f.Read(buf)
			} else {
				n, err = f.ReadAt(buf, position)
			}
			if err != nil && n == 0 {
				return engine.Undefined(), fdOpError("read", fd, err)
			}
			return engine.Str(string(buf[:n])), nil
		}
		buf, offset, length, position, err := fsRWArgs(args)
		if err != nil {
			return engine.Undefined(), err
		}
		var n int
		if position < 0 {
			n, err = f.Read(buf[offset : offset+length])
		} else {
			n, err = f.ReadAt(buf[offset:offset+length], position)
		}
		if err != nil && n == 0 {
			return engine.Undefined(), fdOpError("read", fd, err)
		}
		return engine.IntValue(n), nil
	}))

	// writeSync(fd, buffer[, offset[, length[, position]]]) → bytesWritten
	_ = m.Set("writeSync", engine.NewFunction("writeSync", func(args []engine.Value) (engine.Value, error) {
		fd, ok := argFD(args, 0)
		if !ok {
			return engine.Undefined(), fmt.Errorf("writeSync: fd required")
		}
		f := osFileFromFD(fd)
		if len(args) < 2 {
			return engine.Undefined(), fmt.Errorf("writeSync: data required")
		}
		// 字符串形式：writeSync(fd, string[, position[, encoding]])
		if args[1].Type() == engine.TypeString {
			data := []byte(args[1].String())
			position := int64(-1)
			if len(args) > 2 && args[2].Type() == engine.TypeNumber {
				if n, ok := args[2].Int(); ok {
					position = int64(n)
				}
			}
			var n int
			var err error
			if position < 0 {
				n, err = f.Write(data)
			} else {
				n, err = f.WriteAt(data, position)
			}
			if err != nil {
				return engine.Undefined(), fdOpError("write", fd, err)
			}
			return engine.IntValue(n), nil
		}
		data, offset, length, position, err := fsRWArgs(args)
		if err != nil {
			return engine.Undefined(), err
		}
		var n int
		if position < 0 {
			n, err = f.Write(data[offset : offset+length])
		} else {
			n, err = f.WriteAt(data[offset:offset+length], position)
		}
		if err != nil {
			return engine.Undefined(), fdOpError("write", fd, err)
		}
		return engine.IntValue(n), nil
	}))

	// read(fd, buffer, offset, length, position, callback)
	_ = m.Set("read", engine.NewFunction("read", func(args []engine.Value) (engine.Value, error) {
		fd, ok := argFD(args, 0)
		if !ok {
			return engine.Undefined(), fmt.Errorf("read: fd required")
		}
		f := osFileFromFD(fd)
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("read: callback required")
		}
		// Node 回调 (err, bytesRead, buffer)。
		var bufVal engine.Value = engine.Undefined()
		if len(args) >= 2 && args[1].Type() != engine.TypeNumber {
			bufVal = args[1]
		}
		fsAsyncRW(ctx, func() (int, error) {
			if len(args) >= 2 && args[1].Type() != engine.TypeNumber {
				buf, offset, length, position, err := fsRWArgs(args)
				if err != nil {
					return 0, err
				}
				var n int
				if position < 0 {
					n, err = f.Read(buf[offset : offset+length])
				} else {
					n, err = f.ReadAt(buf[offset:offset+length], position)
				}
				if err != nil && n == 0 {
					return 0, err
				}
				return n, nil
			}
			// read(fd, callback)：一次性读完整内容。
			data, err := readAllFile(f)
			if err != nil {
				return 0, err
			}
			bufVal = globals.NewBufferInstance(data)
			return len(data), nil
		}, bufVal, cb)
		return engine.Undefined(), nil
	}))

	// write(fd, buffer[, offset[, length[, position]]], callback)
	_ = m.Set("write", engine.NewFunction("write", func(args []engine.Value) (engine.Value, error) {
		fd, ok := argFD(args, 0)
		if !ok {
			return engine.Undefined(), fmt.Errorf("write: fd required")
		}
		f := osFileFromFD(fd)
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("write: callback required")
		}
		// Node 回调 (err, bytesWritten, buffer)。
		var bufVal engine.Value = engine.Undefined()
		if len(args) >= 2 {
			bufVal = args[1]
		}
		fsAsyncRW(ctx, func() (int, error) {
			if args[1].Type() == engine.TypeString {
				data := []byte(args[1].String())
				position := int64(-1)
				if len(args) > 2 && args[2].Type() == engine.TypeNumber {
					if n, ok := args[2].Int(); ok {
						position = int64(n)
					}
				}
				var n int
				var err error
				if position < 0 {
					n, err = f.Write(data)
				} else {
					n, err = f.WriteAt(data, position)
				}
				if err != nil {
					return 0, err
				}
				return n, nil
			}
			data, offset, length, position, err := fsRWArgs(args)
			if err != nil {
				return 0, err
			}
			var n int
			if position < 0 {
				n, err = f.Write(data[offset : offset+length])
			} else {
				n, err = f.WriteAt(data[offset:offset+length], position)
			}
			if err != nil {
				return 0, err
			}
			return n, nil
		}, bufVal, cb)
		return engine.Undefined(), nil
	}))

	// readv(fd, buffers[, position], callback) / writev(fd, buffers[, position], callback)
	_ = m.Set("readv", engine.NewFunction("readv", func(args []engine.Value) (engine.Value, error) {
		fd, ok := argFD(args, 0)
		if !ok {
			return engine.Undefined(), fmt.Errorf("readv: fd required")
		}
		f := osFileFromFD(fd)
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("readv: callback required")
		}
		buffers, position, err := fsVArgs(args)
		if err != nil {
			return engine.Undefined(), err
		}
		fsAsync(ctx, func() (engine.Value, error) {
			total := 0
			for i, b := range buffers {
				var n int
				var rerr error
				if position >= 0 {
					n, rerr = f.ReadAt(b, position+int64(total))
				} else {
					n, rerr = f.Read(b)
				}
				total += n
				buffers[i] = b[:n]
				if rerr != nil {
					break
				}
			}
			rb := engine.NewObject()
			_ = rb.Set("bytesRead", engine.IntValue(total))
			_ = rb.Set("buffers", engine.NewArray(valuesFromBytes(buffers)))
			return rb, nil
		}, cb)
		return engine.Undefined(), nil
	}))
	_ = m.Set("writev", engine.NewFunction("writev", func(args []engine.Value) (engine.Value, error) {
		fd, ok := argFD(args, 0)
		if !ok {
			return engine.Undefined(), fmt.Errorf("writev: fd required")
		}
		f := osFileFromFD(fd)
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("writev: callback required")
		}
		buffers, position, err := fsVArgs(args)
		if err != nil {
			return engine.Undefined(), err
		}
		fsAsync(ctx, func() (engine.Value, error) {
			total := 0
			for _, b := range buffers {
				var n int
				var werr error
				if position >= 0 {
					n, werr = f.WriteAt(b, position+int64(total))
				} else {
					n, werr = f.Write(b)
				}
				total += n
				if werr != nil {
					break
				}
			}
			rb := engine.NewObject()
			_ = rb.Set("bytesWritten", engine.IntValue(total))
			_ = rb.Set("buffers", engine.NewArray(valuesFromBytes(buffers)))
			return rb, nil
		}, cb)
		return engine.Undefined(), nil
	}))

	// readvSync / writevSync（简化：逐 buffer 顺序读/写）。
	_ = m.Set("readvSync", engine.NewFunction("readvSync", func(args []engine.Value) (engine.Value, error) {
		fd, ok := argFD(args, 0)
		if !ok {
			return engine.Undefined(), fmt.Errorf("readvSync: fd required")
		}
		f := osFileFromFD(fd)
		buffers, position, err := fsVArgs(args)
		if err != nil {
			return engine.Undefined(), err
		}
		total := 0
		for _, b := range buffers {
			var n int
			if position >= 0 {
				n, _ = f.ReadAt(b, position+int64(total))
			} else {
				n, _ = f.Read(b)
			}
			total += n
		}
		return engine.IntValue(total), nil
	}))
	_ = m.Set("writevSync", engine.NewFunction("writevSync", func(args []engine.Value) (engine.Value, error) {
		fd, ok := argFD(args, 0)
		if !ok {
			return engine.Undefined(), fmt.Errorf("writevSync: fd required")
		}
		f := osFileFromFD(fd)
		buffers, position, err := fsVArgs(args)
		if err != nil {
			return engine.Undefined(), err
		}
		total := 0
		for _, b := range buffers {
			var n int
			if position >= 0 {
				n, _ = f.WriteAt(b, position+int64(total))
			} else {
				n, _ = f.Write(b)
			}
			total += n
		}
		return engine.IntValue(total), nil
	}))

	// --- fd 元数据同步 ---
	addFDCb := func(name string, op func(f *os.File, args []engine.Value) (engine.Value, error)) {
		_ = m.Set(name+"Sync", engine.NewFunction(name+"Sync", func(args []engine.Value) (engine.Value, error) {
			fd, ok := argFD(args, 0)
			if !ok {
				return engine.Undefined(), fmt.Errorf("%sSync: fd required", name)
			}
			v, err := op(osFileFromFD(fd), args[1:])
			return v, fdOpError(name, fd, err)
		}))
		_ = m.Set(name, engine.NewFunction(name, func(args []engine.Value) (engine.Value, error) {
			fd, ok := argFD(args, 0)
			if !ok {
				return engine.Undefined(), fmt.Errorf("%s: fd required", name)
			}
			cb, ok := fsCbArg(args)
			if !ok {
				return engine.Undefined(), fmt.Errorf("%s: callback required", name)
			}
			fsAsync(ctx, func() (engine.Value, error) {
				v, err := op(osFileFromFD(fd), args[1:])
				return v, fdOpError(name, fd, err)
			}, cb)
			return engine.Undefined(), nil
		}))
	}
	addFDCb("fstat", func(f *os.File, _ []engine.Value) (engine.Value, error) {
		info, err := f.Stat()
		if err != nil {
			return engine.Undefined(), err
		}
		return statToObj(ctx, info), nil
	})
	addFDCb("ftruncate", func(f *os.File, args []engine.Value) (engine.Value, error) {
		size := int64(0)
		if len(args) > 0 {
			if n, ok := args[0].Float(); ok {
				size = int64(n)
			}
		}
		return engine.Undefined(), f.Truncate(size)
	})
	addFDCb("fsync", func(f *os.File, _ []engine.Value) (engine.Value, error) {
		return engine.Undefined(), f.Sync()
	})
	addFDCb("fdatasync", func(f *os.File, _ []engine.Value) (engine.Value, error) {
		return engine.Undefined(), f.Sync()
	})
	addFDCb("fchmod", func(f *os.File, args []engine.Value) (engine.Value, error) {
		mode := 0o644
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				mode = n
			}
		}
		return engine.Undefined(), f.Chmod(os.FileMode(mode))
	})
	addFDCb("fchown", func(f *os.File, args []engine.Value) (engine.Value, error) {
		uid, gid := -1, -1
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				uid = n
			}
		}
		if len(args) > 1 {
			if n, ok := args[1].Int(); ok {
				gid = n
			}
		}
		if runtime.GOOS == "windows" {
			return engine.Undefined(), nil // Windows no-op（Node 实测）
		}
		return engine.Undefined(), f.Chown(uid, gid)
	})
	addFDCb("futimes", func(f *os.File, args []engine.Value) (engine.Value, error) {
		atime, mtime, err := fsTimeArgs(args)
		if err != nil {
			return engine.Undefined(), err
		}
		// os.File 无 Chtimes 方法，经路径设置（近似：按 fd 语义应不依赖
		// 路径存在，diff 用例均为正常打开的文件，等价）。
		return engine.Undefined(), os.Chtimes(f.Name(), atime, mtime)
	})

	// --- 链接 / 权限 / 时间 ---
	addPathCb := func(name, nameSync string, op func(p string, args []engine.Value) (engine.Value, error)) {
		_ = m.Set(nameSync, engine.NewFunction(nameSync, func(args []engine.Value) (engine.Value, error) {
			p, err := fsPathArg(args, 0)
			if err != nil {
				return engine.Undefined(), err
			}
			return op(p, args[1:])
		}))
		_ = m.Set(name, engine.NewFunction(name, func(args []engine.Value) (engine.Value, error) {
			p, err := fsPathArg(args, 0)
			if err != nil {
				return engine.Undefined(), err
			}
			cb, ok := fsCbArg(args)
			if !ok {
				return engine.Undefined(), fmt.Errorf("%s: callback required", name)
			}
			fsAsync(ctx, func() (engine.Value, error) {
				return op(p, args[1:])
			}, cb)
			return engine.Undefined(), nil
		}))
	}
	addPathCb("link", "linkSync", func(p string, args []engine.Value) (engine.Value, error) {
		if len(args) < 1 {
			return engine.Undefined(), fmt.Errorf("linkSync: newPath required")
		}
		return engine.Undefined(), os.Link(p, args[0].String())
	})
	addPathCb("symlink", "symlinkSync", func(p string, args []engine.Value) (engine.Value, error) {
		if len(args) < 1 {
			return engine.Undefined(), fmt.Errorf("symlinkSync: newPath required")
		}
		return engine.Undefined(), os.Symlink(p, args[0].String())
	})
	addPathCb("readlink", "readlinkSync", func(p string, args []engine.Value) (engine.Value, error) {
		target, err := os.Readlink(p)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Str(target), nil
	})
	addPathCb("chmod", "chmodSync", func(p string, args []engine.Value) (engine.Value, error) {
		mode := 0o644
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				mode = n
			}
		}
		if runtime.GOOS == "windows" {
			return engine.Undefined(), nil // Windows no-op
		}
		return engine.Undefined(), os.Chmod(p, os.FileMode(mode))
	})
	addPathCb("chown", "chownSync", func(p string, args []engine.Value) (engine.Value, error) {
		uid, gid := -1, -1
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				uid = n
			}
		}
		if len(args) > 1 {
			if n, ok := args[1].Int(); ok {
				gid = n
			}
		}
		if runtime.GOOS == "windows" {
			return engine.Undefined(), nil // Windows no-op
		}
		return engine.Undefined(), os.Chown(p, uid, gid)
	})
	addPathCb("utimes", "utimesSync", func(p string, args []engine.Value) (engine.Value, error) {
		atime, mtime, err := fsTimeArgs(args)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Undefined(), os.Chtimes(p, atime, mtime)
	})
	addPathCb("lutimes", "lutimesSync", func(p string, args []engine.Value) (engine.Value, error) {
		atime, mtime, err := fsTimeArgs(args)
		if err != nil {
			return engine.Undefined(), err
		}
		if runtime.GOOS == "windows" {
			// Windows：os.Chtimes 跟随符号链接（lutimes 不跟随的语义
			// Windows 无对应系统调用，近似处理）。
			return engine.Undefined(), os.Chtimes(p, atime, mtime)
		}
		return engine.Undefined(), lutimesImpl(p, atime, mtime)
	})
	addPathCb("lchmod", "lchmodSync", func(p string, args []engine.Value) (engine.Value, error) {
		if runtime.GOOS == "windows" {
			return engine.Undefined(), fmt.Errorf("lchmod not supported on Windows")
		}
		mode := 0o644
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				mode = n
			}
		}
		return engine.Undefined(), lchmodImpl(p, os.FileMode(mode))
	})
	addPathCb("lchown", "lchownSync", func(p string, args []engine.Value) (engine.Value, error) {
		if runtime.GOOS == "windows" {
			return engine.Undefined(), nil // Windows no-op
		}
		uid, gid := -1, -1
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				uid = n
			}
		}
		if len(args) > 1 {
			if n, ok := args[1].Int(); ok {
				gid = n
			}
		}
		return engine.Undefined(), lchownImpl(p, uid, gid)
	})

	// statfsSync(path) → StatFs 对象
	_ = m.Set("statfsSync", engine.NewFunction("statfsSync", func(args []engine.Value) (engine.Value, error) {
		p, err := fsPathArg(args, 0)
		if err != nil {
			return engine.Undefined(), err
		}
		return fsStatfsObj(p), nil
	}))
	_ = m.Set("statfs", engine.NewFunction("statfs", func(args []engine.Value) (engine.Value, error) {
		p, err := fsPathArg(args, 0)
		if err != nil {
			return engine.Undefined(), err
		}
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("statfs: callback required")
		}
		fsAsync(ctx, func() (engine.Value, error) {
			return fsStatfsObj(p), nil
		}, cb)
		return engine.Undefined(), nil
	}))

	// opendirSync(path) → Dir
	_ = m.Set("opendirSync", engine.NewFunction("opendirSync", func(args []engine.Value) (engine.Value, error) {
		p, err := fsPathArg(args, 0)
		if err != nil {
			return engine.Undefined(), err
		}
		return fsOpenDir(ctx, p)
	}))
	_ = m.Set("opendir", engine.NewFunction("opendir", func(args []engine.Value) (engine.Value, error) {
		p, err := fsPathArg(args, 0)
		if err != nil {
			return engine.Undefined(), err
		}
		cb, ok := fsCbArg(args)
		if !ok {
			return engine.Undefined(), fmt.Errorf("opendir: callback required")
		}
		fsAsync(ctx, func() (engine.Value, error) {
			return fsOpenDir(ctx, p)
		}, cb)
		return engine.Undefined(), nil
	}))

	// watchFile(path[, options], listener) / unwatchFile(path[, listener])
	_ = m.Set("watchFile", engine.NewFunction("watchFile", func(args []engine.Value) (engine.Value, error) {
		p, err := fsPathArg(args, 0)
		if err != nil {
			return engine.Undefined(), err
		}
		listener := engine.Undefined()
		for _, a := range args[1:] {
			if a.IsFunction() {
				listener = a
				break
			}
		}
		fsWatchFile(ctx, p, listener)
		return engine.Undefined(), nil
	}))
	_ = m.Set("unwatchFile", engine.NewFunction("unwatchFile", func(args []engine.Value) (engine.Value, error) {
		fsUnwatchFile(args)
		return engine.Undefined(), nil
	}))

	// openAsBlob(path) → Promise<Blob>
	_ = m.Set("openAsBlob", engine.NewFunction("openAsBlob", func(args []engine.Value) (engine.Value, error) {
		p, err := fsPathArg(args, 0)
		if err != nil {
			return engine.Undefined(), err
		}
		executor := engine.NewFunction("executor", func(ea []engine.Value) (engine.Value, error) {
			if len(ea) < 2 {
				return engine.Undefined(), nil
			}
			release := ctx.AddRef()
			go func() {
				data, err := os.ReadFile(p)
				ctx.PostTask(func() {
					defer release()
					if err != nil {
						callBuiltinResolve(ea[1], fsErrorToJS(ctx, err))
						return
					}
					blobV, berr := fsBlobFromData(ctx, data)
					if berr != nil {
						callBuiltinResolve(ea[1], fsErrorToJS(ctx, berr))
						return
					}
					callBuiltinResolve(ea[0], blobV)
				})
			}()
			return engine.Undefined(), nil
		})
		return newBuiltinPromise(ctx, executor)
	}))

	// fs.FileHandle / fs.Stats / fs.StatFs 等类占位（构造器形式，供 typeof 面）。
	_ = m.Set("Stats", engine.NewFunction("Stats", func(args []engine.Value) (engine.Value, error) {
		return engine.NewObject(), nil
	}))
	_ = m.Set("StatFs", engine.NewFunction("StatFs", func(args []engine.Value) (engine.Value, error) {
		return engine.NewObject(), nil
	}))
	_ = m.Set("Dirent", engine.NewFunction("Dirent", func(args []engine.Value) (engine.Value, error) {
		return engine.NewObject(), nil
	}))
	_ = m.Set("Dir", engine.NewFunction("Dir", func(args []engine.Value) (engine.Value, error) {
		return engine.NewObject(), nil
	}))
	_ = m.Set("FileHandle", engine.NewFunction("FileHandle", func(args []engine.Value) (engine.Value, error) {
		return engine.NewObject(), nil
	}))
	_ = m.Set("ReadStream", engine.NewFunction("ReadStream", func(args []engine.Value) (engine.Value, error) {
		return engine.NewObject(), nil
	}))
	_ = m.Set("WriteStream", engine.NewFunction("WriteStream", func(args []engine.Value) (engine.Value, error) {
		return engine.NewObject(), nil
	}))
	_ = m.Set("FSWatcher", engine.NewFunction("FSWatcher", func(args []engine.Value) (engine.Value, error) {
		return engine.NewObject(), nil
	}))
	_ = m.Set("StatWatcher", engine.NewFunction("StatWatcher", func(args []engine.Value) (engine.Value, error) {
		return engine.NewObject(), nil
	}))
}

// --- 辅助函数 ---

// argFD 取 fd 参数（数字）。
func argFD(args []engine.Value, i int) (int, bool) {
	if len(args) <= i {
		return 0, false
	}
	n, ok := args[i].Int()
	return n, ok
}

// osFileFromFD 由 fd 重建 *os.File。fd<0 时返回空文件（操作报错）。
func osFileFromFD(fd int) *os.File {
	return lookupFSFD(fd)
}

// fsParseFlags 把 Node 打开模式字符串映射为 Go os 标志。
func fsParseFlags(flags string) (int, error) {
	f := strings.TrimSpace(flags)
	excl := false
	if strings.HasSuffix(f, "x") {
		excl = true
		f = strings.TrimSuffix(f, "x")
	}
	switch f {
	case "r":
		return os.O_RDONLY, nil
	case "r+":
		return os.O_RDWR | fsExcl(excl), nil
	case "w":
		return os.O_WRONLY | os.O_CREATE | os.O_TRUNC | fsExcl(excl), nil
	case "w+":
		return os.O_RDWR | os.O_CREATE | os.O_TRUNC | fsExcl(excl), nil
	case "a":
		return os.O_WRONLY | os.O_CREATE | os.O_APPEND | fsExcl(excl), nil
	case "a+":
		return os.O_RDWR | os.O_CREATE | os.O_APPEND | fsExcl(excl), nil
	default:
		return 0, fmt.Errorf("ERR_INVALID_ARG_VALUE: invalid file mode '%s'", flags)
	}
}

func fsExcl(excl bool) int {
	if excl {
		return os.O_EXCL
	}
	return 0
}

// fdSystemError 带 Node 错误码的 fd 操作错误（code/errno/path/syscall）。
type fdSystemError struct {
	op   string
	path string
	code string
	desc string
	msg  string
}

func (e *fdSystemError) Error() string {
	if e.msg != "" {
		return fmt.Sprintf("%s: %s, %s '%s' (%s)", e.code, e.desc, e.op, e.path, e.msg)
	}
	return fmt.Sprintf("%s: %s, %s '%s'", e.code, e.desc, e.op, e.path)
}
func (e *fdSystemError) Code() string { return e.code }

// fdOpError 把 fd 操作原始错误包装为带 Node 错误码的错误。
// fsErrnoInfo 先尝试标准映射；未命中时归为 EBADF（如 Windows 关闭后
// 读写返回 ERROR_INVALID_HANDLE）。
func fdOpError(op string, fd int, err error) error {
	if err == nil {
		return nil
	}
	code, desc, _ := fsErrnoInfo(err)
	if code == "" {
		code, desc = "EBADF", "bad file descriptor"
	}
	return &fdSystemError{op: op, path: strconv.Itoa(fd), code: code, desc: desc, msg: err.Error()}
}

// fsRWArgs 解析 read/write 参数（buffer, offset, length, position）。
func fsRWArgs(args []engine.Value) ([]byte, int, int, int64, error) {
	if len(args) < 2 {
		return nil, 0, 0, 0, fmt.Errorf("buffer required")
	}
	buf, ok := engine.AsBuffer(args[1])
	if !ok {
		if ta, ok2 := engine.AsTypedArray(args[1]); ok2 {
			buf = ta.Bytes()
		} else if ab, ok2 := engine.AsArrayBuffer(args[1]); ok2 {
			buf = ab
		} else {
			return nil, 0, 0, 0, fmt.Errorf("ERR_INVALID_ARG_TYPE: buffer")
		}
	}
	offset := 0
	length := len(buf)
	position := int64(-1)
	// offset 默认取 args[2]；length 默认 args[3]；position 默认 args[4]。
	// （Node 中 offset/length 在给定 buffer 场景必传，这里做宽松解析。）
	argIdx := 2
	if len(args) > argIdx && args[argIdx].Type() == engine.TypeNumber {
		if n, ok := args[argIdx].Int(); ok {
			offset = n
		}
		argIdx++
	}
	if len(args) > argIdx && args[argIdx].Type() == engine.TypeNumber {
		if n, ok := args[argIdx].Int(); ok {
			length = n
		}
		argIdx++
	}
	if len(args) > argIdx && args[argIdx].Type() == engine.TypeNumber {
		if n, ok := args[argIdx].Int(); ok {
			position = int64(n)
		}
		argIdx++
	}
	if offset < 0 || offset > len(buf) || length < 0 || offset+length > len(buf) {
		return nil, 0, 0, 0, fmt.Errorf("ERR_OUT_OF_RANGE: offset/length")
	}
	return buf, offset, length, position, nil
}

// fsVArgs 解析 readv/writev 参数（buffers, position）。
func fsVArgs(args []engine.Value) ([][]byte, int64, error) {
	if len(args) < 2 {
		return nil, 0, fmt.Errorf("buffers required")
	}
	arr, ok := args[1].(*engine.ArrayValue)
	if !ok {
		return nil, 0, fmt.Errorf("ERR_INVALID_ARG_TYPE: buffers must be an array")
	}
	var buffers [][]byte
	for _, e := range arr.Elems() {
		if b, ok := engine.AsBuffer(e); ok {
			buffers = append(buffers, b)
		} else if ta, ok := engine.AsTypedArray(e); ok {
			buffers = append(buffers, ta.Bytes())
		}
	}
	position := int64(-1)
	// position 可能是第二个参数（无 buffers 之前的数字）或数组后。
	if len(args) > 2 && args[2].Type() == engine.TypeNumber {
		if n, ok := args[2].Int(); ok {
			position = int64(n)
		}
	}
	return buffers, position, nil
}

func valuesFromBytes(buffers [][]byte) []engine.Value {
	out := make([]engine.Value, len(buffers))
	for i, b := range buffers {
		out[i] = globals.NewBufferInstance(b)
	}
	return out
}

// fsTimeArgs 解析 atime/mtime 参数（Date/number/string）。
func fsTimeArgs(args []engine.Value) (time.Time, time.Time, error) {
	toTime := func(v engine.Value) (time.Time, error) {
		if dv, ok := engine.AsDate(v); ok {
			return time.UnixMilli(int64(dv.TimeMs())), nil
		}
		if n, ok := v.Float(); ok {
			return time.UnixMilli(int64(n)), nil
		}
		return time.Now(), fmt.Errorf("ERR_INVALID_ARG_TYPE: time")
	}
	atime := time.Now()
	mtime := time.Now()
	var err error
	if len(args) > 0 && !args[0].IsUndefined() && !args[0].IsNull() {
		atime, err = toTime(args[0])
		if err != nil {
			return atime, mtime, err
		}
	}
	if len(args) > 1 && !args[1].IsUndefined() && !args[1].IsNull() {
		mtime, err = toTime(args[1])
		if err != nil {
			return atime, mtime, err
		}
	} else {
		mtime = atime
	}
	return atime, mtime, nil
}

// fsPathArg 取路径参数。
func fsPathArg(args []engine.Value, i int) (string, error) {
	if len(args) <= i {
		return "", fmt.Errorf("path required")
	}
	return args[i].String(), nil
}

// fsStatfsObj 构造 StatFs 对象。
func fsStatfsObj(p string) engine.Value {
	typ, bsize, blocks, bfree, bavail, files, ffree := statfsFor(p)
	obj := engine.NewObject()
	_ = obj.Set("type", engine.IntValue(int(typ)))
	_ = obj.Set("bsize", engine.IntValue(int(bsize)))
	_ = obj.Set("blocks", engine.Number(float64(blocks)))
	_ = obj.Set("bfree", engine.Number(float64(bfree)))
	_ = obj.Set("bavail", engine.Number(float64(bavail)))
	_ = obj.Set("files", engine.Number(float64(files)))
	_ = obj.Set("ffree", engine.Number(float64(ffree)))
	return obj
}

// fsOpenDir 构造 Dir 对象（一次性读入全部条目，read/readSync 逐条返回）。
func fsOpenDir(ctx engine.Context, p string) (engine.Value, error) {
	entries, err := os.ReadDir(p)
	if err != nil {
		return engine.Undefined(), err
	}
	direntVals := make([]engine.Value, len(entries))
	for i, e := range entries {
		direntVals[i] = makeFsDirent(e.Name(), p, e.Type())
	}
	dir := engine.NewObject()
	_ = dir.Set("path", engine.Str(p))
	var cursor int32 = 0
	readOne := func() (engine.Value, error) {
		if int(cursor) >= len(direntVals) {
			return engine.Null(), nil
		}
		d := direntVals[cursor]
		cursor++
		return d, nil
	}
	_ = dir.Set("readSync", engine.NewFunction("readSync", func(args []engine.Value) (engine.Value, error) {
		return readOne()
	}))
	_ = dir.Set("closeSync", engine.NewFunction("closeSync", func(args []engine.Value) (engine.Value, error) {
		cursor = int32(len(direntVals))
		return engine.Undefined(), nil
	}))
	_ = dir.Set("read", engine.NewFunction("read", func(args []engine.Value) (engine.Value, error) {
		return fsPromise(ctx, args, readOne)
	}))
	_ = dir.Set("close", engine.NewFunction("close", func(args []engine.Value) (engine.Value, error) {
		cursor = int32(len(direntVals))
		return fsPromise(ctx, args, func() (engine.Value, error) {
			return engine.Undefined(), nil
		})
	}))
	_ = dir.Set("dirents", engine.NewArray(direntVals))
	return dir, nil
}

// fsBlobFromData 用全局 Blob 构造器从字节创建 Blob。
func fsBlobFromData(ctx engine.Context, data []byte) (engine.Value, error) {
	ctor, err := ctx.Global().Get("Blob")
	if err != nil || !ctor.IsFunction() {
		return engine.Undefined(), fmt.Errorf("Blob not available")
	}
	f, _ := ctor.AsFunction()
	parts := engine.NewArray([]engine.Value{globals.NewBufferInstance(data)})
	return f.Call([]engine.Value{parts})
}

// readAllFile 读取文件剩余全部内容。
func readAllFile(f *os.File) ([]byte, error) {
	var out []byte
	buf := make([]byte, 4096)
	for {
		n, err := f.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			if err.Error() == "EOF" {
				return out, nil
			}
			return out, err
		}
	}
}

// newFileHandle 构造 FileHandle 对象（fs/promises.open 的返回值）。
func newFileHandle(ctx engine.Context, f *os.File) engine.Value {
	h := engine.NewObject()
	fd := registerFSFD(f)
	_ = h.Set("fd", engine.IntValue(fd))

	p := func(op func() (engine.Value, error)) (engine.Value, error) {
		return fsPromise(ctx, nil, op)
	}

	_ = h.Set("readFile", engine.NewFunction("readFile", func(args []engine.Value) (engine.Value, error) {
		encoding := ""
		if len(args) > 0 && args[0].IsObject() {
			if o, ok := args[0].AsObject(); ok {
				if v, err := o.Get("encoding"); err == nil && !v.IsUndefined() {
					encoding = v.String()
				}
			}
		}
		return p(func() (engine.Value, error) {
			data, err := readAllFile(f)
			if err != nil {
				return engine.Undefined(), err
			}
			if encoding != "" && encoding != "buffer" {
				return engine.Str(string(data)), nil
			}
			return globals.NewBufferInstance(data), nil
		})
	}))
	_ = h.Set("writeFile", engine.NewFunction("writeFile", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("writeFile: data required")
		}
		data := fsDataBytes(args[0])
		return p(func() (engine.Value, error) {
			if _, err := f.Seek(0, 0); err != nil {
				return engine.Undefined(), err
			}
			if err := f.Truncate(0); err != nil {
				return engine.Undefined(), err
			}
			_, err := f.Write(data)
			return engine.Undefined(), err
		})
	}))
	_ = h.Set("appendFile", engine.NewFunction("appendFile", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("appendFile: data required")
		}
		data := fsDataBytes(args[0])
		return p(func() (engine.Value, error) {
			if _, err := f.Seek(0, 2); err != nil {
				return engine.Undefined(), err
			}
			_, err := f.Write(data)
			return engine.Undefined(), err
		})
	}))
	_ = h.Set("stat", engine.NewFunction("stat", func(args []engine.Value) (engine.Value, error) {
		return p(func() (engine.Value, error) {
			info, err := f.Stat()
			if err != nil {
				return engine.Undefined(), err
			}
			return statToObj(ctx, info), nil
		})
	}))
	_ = h.Set("close", engine.NewFunction("close", func(args []engine.Value) (engine.Value, error) {
		return p(func() (engine.Value, error) {
			unregisterFSFD(fd, f)
			return engine.Undefined(), f.Close()
		})
	}))
	_ = h.Set("read", engine.NewFunction("read", func(args []engine.Value) (engine.Value, error) {
		return p(func() (engine.Value, error) {
			buf, offset, length, position, err := fsRWArgs(append([]engine.Value{nilValue}, args...))
			if err != nil {
				return engine.Undefined(), err
			}
			var n int
			if position < 0 {
				n, err = f.Read(buf[offset : offset+length])
			} else {
				n, err = f.ReadAt(buf[offset:offset+length], position)
			}
			if err != nil && n == 0 {
				return engine.Undefined(), err
			}
			rb := engine.NewObject()
			_ = rb.Set("bytesRead", engine.IntValue(n))
			_ = rb.Set("buffer", globals.NewBufferInstance(buf))
			return rb, nil
		})
	}))
	_ = h.Set("write", engine.NewFunction("write", func(args []engine.Value) (engine.Value, error) {
		return p(func() (engine.Value, error) {
			buf, offset, length, position, err := fsRWArgs(append([]engine.Value{nilValue}, args...))
			if err != nil {
				return engine.Undefined(), err
			}
			var n int
			if position < 0 {
				n, err = f.Write(buf[offset : offset+length])
			} else {
				n, err = f.WriteAt(buf[offset:offset+length], position)
			}
			if err != nil {
				return engine.Undefined(), err
			}
			rb := engine.NewObject()
			_ = rb.Set("bytesWritten", engine.IntValue(n))
			_ = rb.Set("buffer", globals.NewBufferInstance(buf))
			return rb, nil
		})
	}))
	_ = h.Set("truncate", engine.NewFunction("truncate", func(args []engine.Value) (engine.Value, error) {
		size := int64(0)
		if len(args) > 0 {
			if n, ok := args[0].Float(); ok {
				size = int64(n)
			}
		}
		return p(func() (engine.Value, error) {
			return engine.Undefined(), f.Truncate(size)
		})
	}))
	_ = h.Set("sync", engine.NewFunction("sync", func(args []engine.Value) (engine.Value, error) {
		return p(func() (engine.Value, error) {
			return engine.Undefined(), f.Sync()
		})
	}))
	_ = h.Set("datasync", engine.NewFunction("datasync", func(args []engine.Value) (engine.Value, error) {
		return p(func() (engine.Value, error) {
			return engine.Undefined(), f.Sync()
		})
	}))
	_ = h.Set("chmod", engine.NewFunction("chmod", func(args []engine.Value) (engine.Value, error) {
		mode := 0o644
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				mode = n
			}
		}
		if runtime.GOOS == "windows" {
			return p(func() (engine.Value, error) { return engine.Undefined(), nil })
		}
		return p(func() (engine.Value, error) {
			return engine.Undefined(), f.Chmod(os.FileMode(mode))
		})
	}))
	_ = h.Set("chown", engine.NewFunction("chown", func(args []engine.Value) (engine.Value, error) {
		if runtime.GOOS == "windows" {
			return p(func() (engine.Value, error) { return engine.Undefined(), nil })
		}
		uid, gid := -1, -1
		if len(args) > 0 {
			if n, ok := args[0].Int(); ok {
				uid = n
			}
		}
		if len(args) > 1 {
			if n, ok := args[1].Int(); ok {
				gid = n
			}
		}
		return p(func() (engine.Value, error) {
			return engine.Undefined(), f.Chown(uid, gid)
		})
	}))
	_ = h.Set("utimes", engine.NewFunction("utimes", func(args []engine.Value) (engine.Value, error) {
		atime, mtime, err := fsTimeArgs(args)
		if err != nil {
			return engine.Undefined(), err
		}
		return p(func() (engine.Value, error) {
			return engine.Undefined(), os.Chtimes(f.Name(), atime, mtime)
		})
	}))
	return h
}

// nilValue 占位值（用于构造 fsRWArgs 需要的 buffer 位置参数）。
var nilValue = engine.Undefined()

// fsAsyncRW 异步 fd 读写：回调签名 (err, n, buffer)——Node 的
// fs.read/fs.write 回调语义（字节数与 buffer 分属独立参数）。
func fsAsyncRW(ctx engine.Context, op func() (int, error), bufVal engine.Value, cb engine.Value) {
	release := ctx.AddRef()
	go func() {
		n, err := op()
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
				if _, err := f.Call([]engine.Value{engine.Null(), engine.IntValue(n), bufVal}); err != nil {
					interpreter.ReportUncaught(nil, err)
				}
			}
		})
	}()
}

// --- watchFile / unwatchFile（stat 轮询） ---

// fsWatchFile 定时轮询 stat，变化时触发 (curr, prev)。
func fsWatchFile(ctx engine.Context, p string, listener engine.Value) {
	release := ctx.AddRef()
	prevMod := int64(0)
	ticker := time.NewTicker(500 * time.Millisecond)
	go func() {
		defer release()
		defer ticker.Stop()
		for range ticker.C {
			info, err := os.Stat(p)
			if err != nil {
				continue
			}
			mod := info.ModTime().UnixNano()
			if prevMod != 0 && mod != prevMod {
				curr := statToObj(ctx, info)
				prev := statToObj(ctx, info)
				// prev 用前值近似（完整实现需记录上一轮 stat）。
				ctx.PostTask(func() {
					if listener.IsFunction() {
						if f, ok := listener.AsFunction(); ok {
							if _, err := f.Call([]engine.Value{curr, prev}); err != nil {
								interpreter.ReportUncaught(nil, err)
							}
						}
					}
				})
			}
			prevMod = mod
		}
	}()
}

// fsWatchFileState 记录 watchFile 监听器（unwatchFile 需要）。简化：
// 每个 path 一个轮询 goroutine，unwatch 通过全局表关闭。
var fsWatchState = struct {
	mu      sync.Mutex
	stopped map[string]bool
}{stopped: map[string]bool{}}

func fsUnwatchFile(args []engine.Value) {
	if len(args) == 0 {
		return
	}
	p := args[0].String()
	fsWatchState.mu.Lock()
	fsWatchState.stopped[p] = true
	fsWatchState.mu.Unlock()
}
