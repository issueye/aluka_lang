//go:build darwin

package gui

func abiCall0(fn uintptr) uintptr
func abiCall1(fn, a1 uintptr) uintptr

// objcMsgSend 调用 objc_msgSend(obj, sel, a1, a2, a3, a4)。
func objcMsgSend(obj, sel, a1, a2, a3, a4 uintptr) uintptr

func objcMsgSendF1(obj, sel uintptr, f float64) uintptr

// objcMsgSendRect 调用带 NSRect（4 个 double）再跟最多 3 个整数参数的消息。
func objcMsgSendRect(obj, sel uintptr, x, y, w, h float64, a1, a2, a3 uintptr) uintptr
