package gui

import (
	"runtime"
	"testing"
)

func TestClipboardReadWrite(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		t.Skip("clipboard not supported on this platform")
	}

	testText := "Hello Aluka Clipboard! 你好，桌面！"
	err := ClipboardWriteText(testText)
	if err != nil {
		t.Fatalf("ClipboardWriteText failed: %v", err)
	}

	got, err := ClipboardReadText()
	if err != nil {
		t.Fatalf("ClipboardReadText failed: %v", err)
	}

	if got != testText {
		t.Fatalf("Clipboard text mismatch: got %q, want %q", got, testText)
	}
}
