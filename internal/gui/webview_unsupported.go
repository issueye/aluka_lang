//go:build !windows && !darwin

package gui

import (
	"fmt"
	"runtime"
)

type unsupportedApp struct {
	app *App
}

func platformUnsupported(op string) error {
	return fmt.Errorf("gui: %s is not supported on %s/%s yet", op, runtime.GOOS, runtime.GOARCH)
}

func createNativeApp(app *App) NativeApp {
	return &unsupportedApp{app: app}
}

func (a *unsupportedApp) Run() error {
	return platformUnsupported("App.Run")
}

func (a *unsupportedApp) Quit() {}

func (a *unsupportedApp) PostAction(fn func()) {
	if fn != nil {
		go fn()
	}
}

func (a *unsupportedApp) ShowDialog(opts DialogOptions) (int, []string, error) {
	return 0, nil, platformUnsupported("dialog")
}

func (a *unsupportedApp) CreateTray(opts TrayOptions) (NativeTray, error) {
	return nil, platformUnsupported("tray")
}

func createNativeWindow(opts WindowOptions, parent *Window) (NativeWindow, error) {
	return nil, platformUnsupported("window")
}
