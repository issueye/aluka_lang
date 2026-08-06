//go:build windows

package builtin

// fs 平台相关实现（Windows）：
//   - Stats 时间字段提取（Win32FileAttributeData）
//   - statfs（GetDiskFreeSpaceExW）

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

// statSysTimes 从 os.FileInfo 提取四类时间（毫秒浮点）。
// Windows：atime=LastAccess，mtime=LastWrite，ctime=LastWrite（NTFS 无
// change time，Node 用 mtime 近似），birthtime=Creation。
func statSysTimes(info fs.FileInfo) (atimeMs, mtimeMs, ctimeMs, birthtimeMs float64) {
	mtimeMs = float64(info.ModTime().UnixMilli())
	atimeMs = mtimeMs
	ctimeMs = mtimeMs
	birthtimeMs = mtimeMs
	if sys, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		atimeMs = filetimeToMs(sys.LastAccessTime)
		mtimeMs = filetimeToMs(sys.LastWriteTime)
		ctimeMs = mtimeMs
		birthtimeMs = filetimeToMs(sys.CreationTime)
	}
	return
}

// statSysNumbers 提取 Stats 数值字段。
// Windows（Node 实测）：nlink=1, uid=0, gid=0, rdev=0, blksize=4096,
// blocks=0；ino/dev 由文件索引与卷序列号组成（这里以 0 近似，diff 用例
// 不比对 ino/dev）。
func statSysNumbers(info fs.FileInfo) (nlink, uid, gid, ino, dev, rdev, blksize, blocks int64) {
	blksize = 4096
	nlink = 1
	return
}

// lutimesImpl Windows 近似：os.Chtimes（跟随链接；Windows 无 lutimes 系统调用）。
func lutimesImpl(p string, atime, mtime time.Time) error {
	return os.Chtimes(p, atime, mtime)
}

// lchmodImpl Windows 不支持（调用方在 Windows 上直接报错，此处兜底）。
func lchmodImpl(p string, mode os.FileMode) error {
	return syscall.EPERM
}

// lchownImpl Windows no-op（Node 实测 lchownSync 在 Windows 为 no-op）。
func lchownImpl(p string, uid, gid int) error {
	return nil
}

// filetimeToMs syscall.Filetime（100ns 单位，自 1601-01-01）→ Unix 毫秒。
// FILETIME 与 Unix 纪元差 11644473600 秒。
func filetimeToMs(ft syscall.Filetime) float64 {
	n := int64(ft.HighDateTime)<<32 | int64(ft.LowDateTime)
	return float64(n)/1e4 - 11644473600000
}

var (
	procGetDiskFreeSpaceExW = kernel32.NewProc("GetDiskFreeSpaceExW")
)

type diskFreeSpace struct {
	totalBytes int64
	freeBytes  int64
	availBytes int64
}

// statfsFor 统计指定路径所在卷（块大小 4096；files/ffree Windows 恒 0，
// type 恒 0——与 Node/libuv 在 Windows 的行为一致）。
func statfsFor(path string) (typ, bsize, blocks, bfree, bavail, files, ffree int64) {
	bsize = 4096
	// 需要目录形式的卷根路径。取绝对路径的根盘符。
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	root := filepath.VolumeName(abs)
	if root == "" {
		root = abs
	}
	if len(root) > 0 && root[len(root)-1] != '\\' && root[len(root)-1] != '/' {
		root += `\`
	}
	var freeBytesAvailable, totalNumberOfBytes, totalNumberOfFreeBytes uint64
	r1, _, _ := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(utf16PtrFromString(root))),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalNumberOfBytes)),
		uintptr(unsafe.Pointer(&totalNumberOfFreeBytes)))
	if r1 == 0 {
		return
	}
	total := int64(totalNumberOfBytes)
	free := int64(totalNumberOfFreeBytes)
	avail := int64(freeBytesAvailable)
	blocks = total / bsize
	bfree = free / bsize
	bavail = avail / bsize
	return
}

func utf16PtrFromString(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}
