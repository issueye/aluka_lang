//go:build !windows

package gui

type stubApp struct {
	app *App
}

func createNativeApp(app *App) NativeApp {
	return &stubApp{app: app}
}

func (a *stubApp) Run() error                                           { return nil }
func (a *stubApp) Quit()                                                {}
func (a *stubApp) PostAction(fn func())                                 { go fn() }
func (a *stubApp) ShowDialog(opts DialogOptions) (int, []string, error) { return 0, nil, nil }
func (a *stubApp) CreateTray(opts TrayOptions) (NativeTray, error)      { return &stubTray{}, nil }

type stubTray struct{}

func (t *stubTray) SetIcon(icon string)       {}
func (t *stubTray) SetTooltip(tooltip string) {}
func (t *stubTray) SetMenu(items []MenuItem)  {}
func (t *stubTray) Destroy()                  {}

type stubWindow struct {
	opts   WindowOptions
	parent *Window
}

func createNativeWindow(opts WindowOptions, parent *Window) (NativeWindow, error) {
	return &stubWindow{opts: opts, parent: parent}, nil
}

func (w *stubWindow) Show()                           {}
func (w *stubWindow) Hide()                           {}
func (w *stubWindow) Close()                          {}
func (w *stubWindow) Destroy()                        {}
func (w *stubWindow) Center()                         {}
func (w *stubWindow) SetTitle(title string)           {}
func (w *stubWindow) SetSize(width, height int)       {}
func (w *stubWindow) GetSize() (int, int)             { return w.opts.Width, w.opts.Height }
func (w *stubWindow) SetPosition(x, y int)            {}
func (w *stubWindow) GetPosition() (int, int)         { return w.opts.X, w.opts.Y }
func (w *stubWindow) SetMinSize(width, height int)    {}
func (w *stubWindow) SetMaxSize(width, height int)    {}
func (w *stubWindow) SetResizable(resizable bool)     {}
func (w *stubWindow) SetAlwaysOnTop(alwaysOnTop bool) {}
func (w *stubWindow) SetFullscreen(fullscreen bool)   {}
func (w *stubWindow) IsFullscreen() bool              { return false }
func (w *stubWindow) Minimize()                       {}
func (w *stubWindow) Maximize()                       {}
func (w *stubWindow) Unmaximize()                     {}
func (w *stubWindow) IsMaximized() bool               { return false }
func (w *stubWindow) Navigate(url string)             {}
func (w *stubWindow) SetHTML(html string)             {}
func (w *stubWindow) ExecuteScript(js string)         {}
func (w *stubWindow) OpenDevTools()                   {}
