//go:build darwin

package gui

import (
	"fmt"
)

func clipboardReadText() (string, error) {
	if err := ensureObjC(); err != nil {
		return "", err
	}
	pb := objcCall0(objcClass("NSPasteboard"), "generalPasteboard")
	if pb == 0 {
		return "", fmt.Errorf("gui: NSPasteboard generalPasteboard returned nil")
	}
	typeStr := nsString("public.utf8-plain-text")
	res := objcCall1(pb, "stringForType:", typeStr)
	if res == 0 {
		// 尝试旧类型
		typeStr = nsString("NSStringPboardType")
		res = objcCall1(pb, "stringForType:", typeStr)
	}
	if res == 0 {
		return "", nil
	}
	return nsToGo(res), nil
}

func clipboardWriteText(text string) error {
	if err := ensureObjC(); err != nil {
		return err
	}
	pb := objcCall0(objcClass("NSPasteboard"), "generalPasteboard")
	if pb == 0 {
		return fmt.Errorf("gui: NSPasteboard generalPasteboard returned nil")
	}
	objcCall0(pb, "clearContents")
	typeStr := nsString("public.utf8-plain-text")
	objcCall(pb, sel("setString:forType:"), nsString(text), typeStr, 0, 0)
	return nil
}
