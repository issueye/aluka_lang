//go:build darwin

package gui

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"
)

var (
	objcSendPtr uintptr

	objcOnce sync.Once
	objcErr  error

	selCache   sync.Map // string -> uintptr
	classCache sync.Map

	procGetClass    uintptr
	procSelRegister uintptr
	procPoolPush    uintptr
	procPoolPop     uintptr
)

func ensureObjC() error {
	objcOnce.Do(func() {
		load := func(path, sym string) (uintptr, error) {
			h, err := sysDlopen(path, rtldLazy|rtldGlobal)
			if err != nil {
				return 0, fmt.Errorf("dlopen %s: %w", path, err)
			}
			addr, err := sysDlsym(h, sym)
			if err != nil {
				return 0, fmt.Errorf("dlsym %s: %w", sym, err)
			}
			return addr, nil
		}
		var err error
		if objcSendPtr, err = load("/usr/lib/libobjc.A.dylib", "objc_msgSend"); err != nil {
			objcErr = err
			return
		}
		if procGetClass, err = load("/usr/lib/libobjc.A.dylib", "objc_getClass"); err != nil {
			objcErr = err
			return
		}
		if procSelRegister, err = load("/usr/lib/libobjc.A.dylib", "sel_registerName"); err != nil {
			objcErr = err
			return
		}
		procPoolPush, _ = load("/usr/lib/libobjc.A.dylib", "objc_autoreleasePoolPush")
		procPoolPop, _ = load("/usr/lib/libobjc.A.dylib", "objc_autoreleasePoolPop")
		for _, fw := range []string{
			"/System/Library/Frameworks/Foundation.framework/Foundation",
			"/System/Library/Frameworks/AppKit.framework/AppKit",
			"/System/Library/Frameworks/WebKit.framework/WebKit",
		} {
			if _, err := sysDlopen(fw, rtldLazy|rtldGlobal); err != nil {
				objcErr = fmt.Errorf("dlopen %s: %w", fw, err)
				return
			}
		}
	})
	return objcErr
}

func dlcall1(fn, a1 uintptr) uintptr {
	return abiCall1(fn, a1)
}

func objcClass(name string) uintptr {
	if v, ok := classCache.Load(name); ok {
		return v.(uintptr)
	}
	cstr := append([]byte(name), 0)
	id := dlcall1(procGetClass, uintptr(unsafe.Pointer(&cstr[0])))
	runtime.KeepAlive(cstr)
	classCache.Store(name, id)
	return id
}

func sel(name string) uintptr {
	if v, ok := selCache.Load(name); ok {
		return v.(uintptr)
	}
	cstr := append([]byte(name), 0)
	id := dlcall1(procSelRegister, uintptr(unsafe.Pointer(&cstr[0])))
	runtime.KeepAlive(cstr)
	selCache.Store(name, id)
	return id
}

func objcCall(obj, seln, a1, a2, a3, a4 uintptr) uintptr {
	return objcMsgSend(obj, seln, a1, a2, a3, a4)
}

func objcCall0(obj uintptr, name string) uintptr {
	return objcCall(obj, sel(name), 0, 0, 0, 0)
}

func objcCall1(obj uintptr, name string, a1 uintptr) uintptr {
	return objcCall(obj, sel(name), a1, 0, 0, 0)
}

func objcAlloc(className string) uintptr {
	return objcCall0(objcClass(className), "alloc")
}

func nsString(s string) uintptr {
	b := append([]byte(s), 0)
	id := objcCall1(objcClass("NSString"), "stringWithUTF8String:", uintptr(unsafe.Pointer(&b[0])))
	runtime.KeepAlive(b)
	return id
}

func nsURL(s string) uintptr {
	return objcCall1(objcClass("NSURL"), "URLWithString:", nsString(s))
}

func nsNumberYES() uintptr {
	return objcCall1(objcClass("NSNumber"), "numberWithBool:", 1)
}

func nsToGo(ns uintptr) string {
	if ns == 0 {
		return ""
	}
	p := objcCall0(ns, "UTF8String")
	s := goCString(p)
	runtime.KeepAlive(ns)
	return s
}

func goCString(p uintptr) string {
	if p == 0 {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Pointer(p + uintptr(n))) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(unsafe.Pointer(p)), n))
}

func withAutorelease(fn func()) {
	var pool uintptr
	if procPoolPush != 0 {
		pool = abiCall0(procPoolPush)
	}
	fn()
	if procPoolPop != 0 && pool != 0 {
		abiCall1(procPoolPop, pool)
	}
}
