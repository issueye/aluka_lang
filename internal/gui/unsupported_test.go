//go:build !windows && !darwin

package gui

import (
	"runtime"
	"strings"
	"testing"
)

func TestNativeGUIUnsupported(t *testing.T) {
	_, err := createNativeWindow(WindowOptions{Title: "x", Width: 100, Height: 100}, nil)
	if err == nil {
		t.Fatal("createNativeWindow: expected platform error")
	}
	if !strings.Contains(err.Error(), runtime.GOOS) {
		t.Fatalf("createNativeWindow error %q should name platform %s", err, runtime.GOOS)
	}
	app := createNativeApp(&App{})
	if err := app.Run(); err == nil {
		t.Fatal("App.Run: expected platform error")
	}
	if _, err := app.CreateTray(TrayOptions{}); err == nil {
		t.Fatal("CreateTray: expected platform error")
	}
	if _, _, err := app.ShowDialog(DialogOptions{Title: "x"}); err == nil {
		t.Fatal("ShowDialog: expected platform error")
	}
}
