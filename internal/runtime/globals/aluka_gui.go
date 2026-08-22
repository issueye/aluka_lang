package globals

import (
	"encoding/json"
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/gui"
)

// alukaRegisterGUI 注册 Aluka.gui 桌面运行时 API。
func alukaRegisterGUI(ctx engine.Context, aluka engine.Object) {
	guiObj := engine.NewObject()

	// 1. app 对象
	appObj := engine.NewObject()
	app := gui.GetApp()

	// app.on(event, handler) → 返回取消订阅函数
	_ = appObj.Set("on", engine.NewFunction("on", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("app.on requires event and callback")
		}
		evt := args[0].String()
		fn, ok := args[1].AsFunction()
		if !ok {
			return nil, fmt.Errorf("app.on handler must be a function")
		}

		dispose := app.On(evt, func(data interface{}) {
			release := ctx.AddRef()
			ctx.PostTask(func() {
				defer release()
				_, _ = fn.Call([]engine.Value{jsonToEngine(data)})
				ctx.FlushMicrotasks()
			})
		})
		// 返回取消订阅函数（disposer）供 JS 调用。
		return engine.NewFunction("dispose", func(callArgs []engine.Value) (engine.Value, error) {
			dispose()
			return engine.Undefined(), nil
		}), nil
	}))

	// app.quit()
	_ = appObj.Set("quit", engine.NewFunction("quit", func(args []engine.Value) (engine.Value, error) {
		app.Quit()
		return engine.Undefined(), nil
	}))

	// 退出即终止进程：GUI 应用语义下 quit 后不应因残留定时器/任务而悬挂
	// （宿主进程残留还会占用全局热键，导致下次启动 RegisterHotKey 失败）。
	// 覆盖全部退出路径：app.quit()、托盘菜单、最后一个窗口关闭。
	app.On("quit", func(data interface{}) {
		if stopper, ok := ctx.(interface{ Stop() }); ok {
			stopper.Stop()
		}
	})

	// app.run()
	// 持有 JS 上下文活跃句柄直到 GUI 循环退出：既保证 ready 事件的
	// 回投任务不被事件循环空闲判定丢弃（竞态），又让应用退出后进程随之结束
	_ = appObj.Set("run", engine.NewFunction("run", func(args []engine.Value) (engine.Value, error) {
		release := ctx.AddRef()
		go func() {
			defer release()
			_ = app.Run()
		}()
		return engine.Undefined(), nil
	}))

	// app.registerRPC(name, handler)
	_ = appObj.Set("registerRPC", engine.NewFunction("registerRPC", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("app.registerRPC requires method name and handler")
		}
		name := args[0].String()
		fn, ok := args[1].AsFunction()
		if !ok {
			return nil, fmt.Errorf("app.registerRPC handler must be a function")
		}

		gui.RegisterRPCMethod(name, func(paramsRaw json.RawMessage) (interface{}, error) {
			resChan := make(chan struct {
				val interface{}
				err error
			}, 1)

			ctx.PostTask(func() {
				var jsArg engine.Value = engine.Undefined()
				if len(paramsRaw) > 0 {
					var rawObj interface{}
					_ = json.Unmarshal(paramsRaw, &rawObj)
					jsArg = jsonToEngine(rawObj)
				}
				res, err := fn.Call([]engine.Value{jsArg})
				if err != nil {
					resChan <- struct {
						val interface{}
						err error
					}{nil, err}
				} else {
					resChan <- struct {
						val interface{}
						err error
					}{valueToJSONHelper(res), nil}
				}
				ctx.FlushMicrotasks()
			})

			r := <-resChan
			return r.val, r.err
		})
		return engine.Undefined(), nil
	}))

	// app.unregisterRPC(name)
	_ = appObj.Set("unregisterRPC", engine.NewFunction("unregisterRPC", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("app.unregisterRPC requires method name")
		}
		gui.UnregisterRPCMethod(args[0].String())
		return engine.Undefined(), nil
	}))

	// app.getWindows() → 返回窗口句柄数组
	_ = appObj.Set("getWindows", engine.NewFunction("getWindows", func(args []engine.Value) (engine.Value, error) {
		wins := app.Windows()
		arr := make([]engine.Value, 0, len(wins))
		for _, w := range wins {
			arr = append(arr, wrapWindowInstance(ctx, w))
		}
		return engine.NewArray(arr), nil
	}))

	// app.getWindowById(id) → 窗口句柄或 undefined
	_ = appObj.Set("getWindowById", engine.NewFunction("getWindowById", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("app.getWindowById requires window id")
		}
		idf, ok := args[0].Float()
		if !ok {
			return engine.Undefined(), nil
		}
		w := app.GetWindowByID(uint64(idf))
		if w == nil {
			return engine.Undefined(), nil
		}
		return wrapWindowInstance(ctx, w), nil
	}))

	_ = guiObj.Set("app", appObj)

	// 2. Window 构造函数 / 工厂函数
	_ = guiObj.Set("createWindow", engine.NewFunction("createWindow", func(args []engine.Value) (engine.Value, error) {
		var opts gui.WindowOptions
		if len(args) > 0 {
			if o, ok := args[0].AsObject(); ok {
				opts = parseWindowOptions(o)
			}
		}

		win, err := gui.NewWindow(opts)
		if err != nil {
			return nil, err
		}

		return wrapWindowInstance(ctx, win), nil
	}))

	// 3. dialog 原生弹窗 (支持 Promise 异步非阻塞)
	dialogObj := engine.NewObject()

	_ = dialogObj.Set("showMessageBox", engine.NewFunction("showMessageBox", func(args []engine.Value) (engine.Value, error) {
		opts := gui.DialogOptions{Type: "info"}
		if len(args) > 0 {
			if o, ok := args[0].AsObject(); ok {
				opts = parseDialogOptions(o, "info")
			}
		}
		return newDialogPromise(ctx, app, opts, dialogResultMessage)
	}))

	_ = dialogObj.Set("showOpenDialog", engine.NewFunction("showOpenDialog", func(args []engine.Value) (engine.Value, error) {
		opts := gui.DialogOptions{Type: "openFile"}
		if len(args) > 0 {
			if o, ok := args[0].AsObject(); ok {
				opts = parseDialogOptions(o, "openFile")
			}
		}
		return newDialogPromise(ctx, app, opts, dialogResultFiles)
	}))

	_ = dialogObj.Set("showSaveDialog", engine.NewFunction("showSaveDialog", func(args []engine.Value) (engine.Value, error) {
		opts := gui.DialogOptions{Type: "saveFile"}
		if len(args) > 0 {
			if o, ok := args[0].AsObject(); ok {
				opts = parseDialogOptions(o, "saveFile")
			}
		}
		return newDialogPromise(ctx, app, opts, dialogResultSavePath)
	}))

	_ = guiObj.Set("dialog", dialogObj)

	// 4. shell 系统文件/文件夹操作（打开 / 在文件管理器中显示），返回 Promise<{ok,error?}>
	shellObj := engine.NewObject()
	_ = shellObj.Set("openPath", engine.NewFunction("openPath", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("shell.openPath requires path")
		}
		return newShellPromise(ctx, gui.OpenPath, args[0].String())
	}))
	_ = shellObj.Set("showItemInFolder", engine.NewFunction("showItemInFolder", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("shell.showItemInFolder requires path")
		}
		return newShellPromise(ctx, gui.ShowItemInFolder, args[0].String())
	}))
	_ = shellObj.Set("openExternal", engine.NewFunction("openExternal", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("shell.openExternal requires url")
		}
		return newShellPromise(ctx, gui.OpenExternal, args[0].String())
	}))
	_ = guiObj.Set("shell", shellObj)

	// 5. createTray 系统托盘
	_ = guiObj.Set("createTray", engine.NewFunction("createTray", func(args []engine.Value) (engine.Value, error) {
		var opts gui.TrayOptions
		if len(args) > 0 {
			if o, ok := args[0].AsObject(); ok {
				if icon, err := o.Get("icon"); err == nil && icon != nil && !icon.IsUndefined() {
					opts.Icon = icon.String()
				}
				if tip, err := o.Get("tooltip"); err == nil && tip != nil && !tip.IsUndefined() {
					opts.Tooltip = tip.String()
				}
				if menuVal, err := o.Get("menu"); err == nil && menuVal != nil && menuVal.IsObject() {
					if menuArr, ok := menuVal.AsObject(); ok {
						opts.Menu = parseMenuItems(ctx, menuArr)
					}
				}
			}
		}
		tray, err := gui.NewTray(opts)
		if err != nil {
			return nil, err
		}
		return wrapTrayInstance(ctx, tray), nil
	}))

	// 5. globalShortcut 全局快捷键
	shortcutObj := engine.NewObject()
	_ = shortcutObj.Set("register", engine.NewFunction("register", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("globalShortcut.register requires accelerator and callback")
		}
		accel := args[0].String()
		fn, ok := args[1].AsFunction()
		if !ok {
			return nil, fmt.Errorf("globalShortcut.register callback must be a function")
		}
		if err := gui.GlobalShortcutRegister(accel, func() {
			release := ctx.AddRef()
			ctx.PostTask(func() {
				defer release()
				_, _ = fn.Call(nil)
				ctx.FlushMicrotasks()
			})
		}); err != nil {
			return nil, err
		}
		return engine.Undefined(), nil
	}))
	_ = shortcutObj.Set("unregister", engine.NewFunction("unregister", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			gui.GlobalShortcutUnregister(args[0].String())
		}
		return engine.Undefined(), nil
	}))
	_ = shortcutObj.Set("unregisterAll", engine.NewFunction("unregisterAll", func(args []engine.Value) (engine.Value, error) {
		gui.GlobalShortcutUnregisterAll()
		return engine.Undefined(), nil
	}))
	_ = guiObj.Set("globalShortcut", shortcutObj)

	// 6. setAssetDir(dir)
	// 打包产物模式（--web-dir 内嵌资源）下自动忽略：开发用相对路径
	// 在用户机器上不存在，若覆盖虚拟协议会导致全部请求 404
	_ = guiObj.Set("setAssetDir", engine.NewFunction("setAssetDir", func(args []engine.Value) (engine.Value, error) {
		if gui.EmbeddedAssetsActive() {
			return engine.Undefined(), nil
		}
		if len(args) > 0 {
			dir := args[0].String()
			gui.SetAssetProvider(&gui.LocalDirectoryAssetProvider{BaseDir: dir})
		}
		return engine.Undefined(), nil
	}))

	_ = aluka.Set("gui", guiObj)
}

// newShellPromise 在后台 goroutine 执行系统文件/文件夹操作，避免阻塞 JS 线程；
// 结果统一以 {ok:boolean, error?:string} resolve（不 reject，调用侧无需 try/catch）。
func newShellPromise(ctx engine.Context, op func(string) error, target string) (engine.Value, error) {
	executor := engine.NewFunction("executor", func(execArgs []engine.Value) (engine.Value, error) {
		if len(execArgs) < 1 {
			return engine.Undefined(), nil
		}
		resolve := execArgs[0]

		release := ctx.AddRef()
		go func() {
			err := op(target)
			ctx.PostTask(func() {
				defer release()
				res := map[string]interface{}{"ok": err == nil}
				if err != nil {
					res["error"] = err.Error()
				}
				if rf, ok := resolve.AsFunction(); ok {
					_, _ = rf.Call([]engine.Value{jsonToEngine(res)})
				}
				ctx.FlushMicrotasks()
			})
		}()
		return engine.Undefined(), nil
	})
	return newPromise(ctx, executor)
}

type dialogResultKind int

const (
	dialogResultMessage dialogResultKind = iota
	dialogResultFiles
	dialogResultSavePath
)

func newDialogPromise(ctx engine.Context, app *gui.App, opts gui.DialogOptions, kind dialogResultKind) (engine.Value, error) {
	opts = gui.NormalizeDialogOptions(opts)
	executor := engine.NewFunction("executor", func(execArgs []engine.Value) (engine.Value, error) {
		if len(execArgs) < 2 {
			return engine.Undefined(), nil
		}
		resolve := execArgs[0]
		reject := execArgs[1]

		release := ctx.AddRef()
		go func() {
			btnIndex, files, err := app.ShowDialog(opts)
			ctx.PostTask(func() {
				defer release()
				if err != nil {
					if rf, ok := reject.AsFunction(); ok {
						errObj := engine.NewObject()
						_ = errObj.Set("message", engine.Str(err.Error()))
						_, _ = rf.Call([]engine.Value{errObj})
					}
					ctx.FlushMicrotasks()
					return
				}
				if rf, ok := resolve.AsFunction(); ok {
					switch kind {
					case dialogResultFiles:
						arr := make([]engine.Value, 0, len(files))
						for _, f := range files {
							arr = append(arr, engine.Str(f))
						}
						_, _ = rf.Call([]engine.Value{engine.NewArray(arr)})
					case dialogResultSavePath:
						if len(files) == 0 {
							_, _ = rf.Call([]engine.Value{engine.Null()})
						} else {
							_, _ = rf.Call([]engine.Value{engine.Str(files[0])})
						}
					default:
						_, _ = rf.Call([]engine.Value{engine.Number(float64(btnIndex))})
					}
				}
				ctx.FlushMicrotasks()
			})
		}()
		return engine.Undefined(), nil
	})
	return newPromise(ctx, executor)
}

func parseDialogOptions(o engine.Object, defaultType string) gui.DialogOptions {
	opts := gui.DialogOptions{Type: defaultType}
	getStr := func(key string, dst *string) {
		if v, err := o.Get(key); err == nil && v != nil && !v.IsUndefined() {
			*dst = v.String()
		}
	}
	getBool := func(key string, dst *bool) {
		if v, err := o.Get(key); err == nil && v != nil && !v.IsUndefined() {
			b, _ := v.Bool()
			*dst = b
		}
	}
	getInt := func(key string, dst *int) {
		if v, err := o.Get(key); err == nil && v != nil {
			if f, ok := v.Float(); ok {
				*dst = int(f)
			}
		}
	}
	getStr("title", &opts.Title)
	getStr("message", &opts.Message)
	getStr("type", &opts.Type)
	getStr("defaultPath", &opts.DefaultPath)
	getBool("directory", &opts.Directory)
	getBool("multiple", &opts.Multiple)
	getInt("defaultId", &opts.DefaultID)
	getInt("cancelId", &opts.CancelID)

	if v, err := o.Get("buttons"); err == nil && v != nil && v.IsObject() {
		if arr, ok := v.AsObject(); ok {
			for _, k := range arr.Keys() {
				if item, err := arr.Get(k); err == nil && item != nil {
					opts.Buttons = append(opts.Buttons, item.String())
				}
			}
		}
	}
	if v, err := o.Get("properties"); err == nil && v != nil && v.IsObject() {
		if arr, ok := v.AsObject(); ok {
			for _, k := range arr.Keys() {
				if item, err := arr.Get(k); err == nil && item != nil {
					opts.Properties = append(opts.Properties, item.String())
				}
			}
		}
	}
	if v, err := o.Get("filters"); err == nil && v != nil && v.IsObject() {
		if arr, ok := v.AsObject(); ok {
			for _, k := range arr.Keys() {
				item, err := arr.Get(k)
				if err != nil || item == nil || !item.IsObject() {
					continue
				}
				fo, ok := item.AsObject()
				if !ok {
					continue
				}
				var f gui.FileFilter
				if n, err := fo.Get("name"); err == nil && n != nil {
					f.Name = n.String()
				}
				if ex, err := fo.Get("extensions"); err == nil && ex != nil && ex.IsObject() {
					if exArr, ok := ex.AsObject(); ok {
						for _, ek := range exArr.Keys() {
							if e, err := exArr.Get(ek); err == nil && e != nil {
								f.Extensions = append(f.Extensions, e.String())
							}
						}
					}
				}
				opts.Filters = append(opts.Filters, f)
			}
		}
	}
	return gui.NormalizeDialogOptions(opts)
}

func parseWindowOptions(o engine.Object) gui.WindowOptions {
	var opts gui.WindowOptions
	getStr := func(key string, dst *string) {
		if v, err := o.Get(key); err == nil && v != nil && !v.IsUndefined() {
			*dst = v.String()
		}
	}
	getInt := func(key string, dst *int) {
		if v, err := o.Get(key); err == nil && v != nil {
			if f, ok := v.Float(); ok {
				*dst = int(f)
			}
		}
	}
	getBoolPtr := func(key string, dst **bool) {
		if v, err := o.Get(key); err == nil && v != nil && !v.IsUndefined() {
			b, _ := v.Bool()
			*dst = &b
		}
	}
	getBool := func(key string, dst *bool) {
		if v, err := o.Get(key); err == nil && v != nil && !v.IsUndefined() {
			b, _ := v.Bool()
			*dst = b
		}
	}

	getStr("title", &opts.Title)
	getStr("url", &opts.URL)
	getStr("html", &opts.HTML)
	getStr("backgroundEffect", &opts.BackgroundEffect)
	getInt("width", &opts.Width)
	getInt("height", &opts.Height)
	getInt("x", &opts.X)
	getInt("y", &opts.Y)
	getInt("minWidth", &opts.MinWidth)
	getInt("minHeight", &opts.MinHeight)
	getInt("maxWidth", &opts.MaxWidth)
	getInt("maxHeight", &opts.MaxHeight)
	getBoolPtr("frame", &opts.Frame)
	getBoolPtr("resizable", &opts.Resizable)
	getBool("center", &opts.Center)
	getBool("hidden", &opts.Hidden)
	getBool("transparent", &opts.Transparent)
	getBool("alwaysOnTop", &opts.AlwaysOnTop)
	getBool("devTools", &opts.DevTools)
	getBool("maximized", &opts.Maximized)
	getBool("minimized", &opts.Minimized)
	getStr("preloadScript", &opts.PreloadScript)
	// 兼容设计稿字段名 preload
	if opts.PreloadScript == "" {
		getStr("preload", &opts.PreloadScript)
	}
	if v, err := o.Get("opacity"); err == nil && v != nil && !v.IsUndefined() {
		if f, ok := v.Float(); ok {
			opts.Opacity = f
		}
	}
	return opts
}

// parseMenuItems 解析 JS 菜单模板（支持 click 回调与嵌套 submenu）。
func parseMenuItems(ctx engine.Context, arr engine.Object) []gui.MenuItem {
	keys := arr.Keys()
	items := make([]gui.MenuItem, 0, len(keys))
	for _, k := range keys {
		v, err := arr.Get(k)
		if err != nil || !v.IsObject() {
			continue
		}
		o, ok := v.AsObject()
		if !ok {
			continue
		}
		var item gui.MenuItem
		if t, err := o.Get("label"); err == nil && t != nil {
			item.Label = t.String()
		}
		if t, err := o.Get("type"); err == nil && t != nil && !t.IsUndefined() {
			item.Type = t.String()
		}
		if t, err := o.Get("id"); err == nil && t != nil && !t.IsUndefined() {
			item.ID = t.String()
		}
		if t, err := o.Get("shortcut"); err == nil && t != nil && !t.IsUndefined() {
			item.Shortcut = t.String()
		}
		if t, err := o.Get("checked"); err == nil && t != nil && !t.IsUndefined() {
			item.Checked, _ = t.Bool()
		}
		if t, err := o.Get("disabled"); err == nil && t != nil && !t.IsUndefined() {
			item.Disabled, _ = t.Bool()
		}
		if fn, err := o.Get("click"); err == nil && fn != nil && fn.IsFunction() {
			callback, _ := fn.AsFunction()
			item.Click = func() {
				release := ctx.AddRef()
				ctx.PostTask(func() {
					defer release()
					_, _ = callback.Call([]engine.Value{jsonToEngine(map[string]interface{}{
						"label": item.Label, "id": item.ID,
					})})
					ctx.FlushMicrotasks()
				})
			}
		}
		if sub, err := o.Get("submenu"); err == nil && sub != nil && sub.IsObject() {
			if subArr, ok := sub.AsObject(); ok {
				item.Submenu = parseMenuItems(ctx, subArr)
			}
		}
		items = append(items, item)
	}
	return items
}

func wrapWindowInstance(ctx engine.Context, win *gui.Window) engine.Value {
	obj := engine.NewObject()

	var winDisposers = map[string][]func(){}

	_ = obj.Set("id", engine.Number(float64(win.ID())))

	_ = obj.Set("show", engine.NewFunction("show", func(args []engine.Value) (engine.Value, error) {
		win.Show()
		return engine.Undefined(), nil
	}))

	_ = obj.Set("hide", engine.NewFunction("hide", func(args []engine.Value) (engine.Value, error) {
		win.Hide()
		return engine.Undefined(), nil
	}))

	_ = obj.Set("close", engine.NewFunction("close", func(args []engine.Value) (engine.Value, error) {
		force := false
		if len(args) > 0 {
			force, _ = args[0].Bool()
		}
		if force {
			win.Close()
		} else {
			win.TryClose()
		}
		return engine.Undefined(), nil
	}))

	_ = obj.Set("onCloseRequested", engine.NewFunction("onCloseRequested", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("window.onCloseRequested requires callback")
		}
		fn, ok := args[0].AsFunction()
		if !ok {
			return nil, fmt.Errorf("window.onCloseRequested callback must be a function")
		}
		// 立即返回 false 拦截原生关闭，避免 UI 线程同步等待 JS；
		// 真正是否关闭由回调 return true 或 close(true) 决定。
		win.OnCloseRequested(func() bool {
			release := ctx.AddRef()
			ctx.PostTask(func() {
				defer release()
				res, err := fn.Call(nil)
				allow := false
				if err == nil && res != nil && !res.IsUndefined() {
					allow, _ = res.Bool()
				}
				if allow {
					win.Close()
				}
				ctx.FlushMicrotasks()
			})
			return false
		})
		return engine.Undefined(), nil
	}))

	_ = obj.Set("center", engine.NewFunction("center", func(args []engine.Value) (engine.Value, error) {
		win.Center()
		return engine.Undefined(), nil
	}))

	_ = obj.Set("setTitle", engine.NewFunction("setTitle", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			win.SetTitle(args[0].String())
		}
		return engine.Undefined(), nil
	}))

	_ = obj.Set("setSize", engine.NewFunction("setSize", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 {
			w, _ := args[0].Float()
			h, _ := args[1].Float()
			win.SetSize(int(w), int(h))
		}
		return engine.Undefined(), nil
	}))

	_ = obj.Set("getSize", engine.NewFunction("getSize", func(args []engine.Value) (engine.Value, error) {
		w, h := win.GetSize()
		return engine.NewArray([]engine.Value{engine.Number(float64(w)), engine.Number(float64(h))}), nil
	}))

	_ = obj.Set("minimize", engine.NewFunction("minimize", func(args []engine.Value) (engine.Value, error) {
		win.Minimize()
		return engine.Undefined(), nil
	}))

	_ = obj.Set("maximize", engine.NewFunction("maximize", func(args []engine.Value) (engine.Value, error) {
		win.Maximize()
		return engine.Undefined(), nil
	}))

	_ = obj.Set("unmaximize", engine.NewFunction("unmaximize", func(args []engine.Value) (engine.Value, error) {
		win.Unmaximize()
		return engine.Undefined(), nil
	}))

	_ = obj.Set("navigate", engine.NewFunction("navigate", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			win.Navigate(args[0].String())
		}
		return engine.Undefined(), nil
	}))

	_ = obj.Set("setPosition", engine.NewFunction("setPosition", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 {
			x, _ := args[0].Float()
			y, _ := args[1].Float()
			win.SetPosition(int(x), int(y))
		}
		return engine.Undefined(), nil
	}))

	_ = obj.Set("getPosition", engine.NewFunction("getPosition", func(args []engine.Value) (engine.Value, error) {
		x, y := win.GetPosition()
		return engine.NewArray([]engine.Value{engine.Number(float64(x)), engine.Number(float64(y))}), nil
	}))

	_ = obj.Set("setMinSize", engine.NewFunction("setMinSize", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 {
			w, _ := args[0].Float()
			h, _ := args[1].Float()
			win.SetMinSize(int(w), int(h))
		}
		return engine.Undefined(), nil
	}))

	_ = obj.Set("setMaxSize", engine.NewFunction("setMaxSize", func(args []engine.Value) (engine.Value, error) {
		if len(args) >= 2 {
			w, _ := args[0].Float()
			h, _ := args[1].Float()
			win.SetMaxSize(int(w), int(h))
		}
		return engine.Undefined(), nil
	}))

	_ = obj.Set("setAlwaysOnTop", engine.NewFunction("setAlwaysOnTop", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			b, _ := args[0].Bool()
			win.SetAlwaysOnTop(b)
		}
		return engine.Undefined(), nil
	}))

	_ = obj.Set("setResizable", engine.NewFunction("setResizable", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			b, _ := args[0].Bool()
			win.SetResizable(b)
		}
		return engine.Undefined(), nil
	}))

	_ = obj.Set("setHTML", engine.NewFunction("setHTML", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			win.SetHTML(args[0].String())
		}
		return engine.Undefined(), nil
	}))
	_ = obj.Set("toggleMaximize", engine.NewFunction("toggleMaximize", func(args []engine.Value) (engine.Value, error) {
		if win.IsMaximized() {
			win.Unmaximize()
		} else {
			win.Maximize()
		}
		return engine.Undefined(), nil
	}))
	_ = obj.Set("setProgressBar", engine.NewFunction("setProgressBar", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			if f, ok := args[0].Float(); ok {
				win.SetProgressBar(f)
			}
		}
		return engine.Undefined(), nil
	}))
	_ = obj.Set("setOverlayIcon", engine.NewFunction("setOverlayIcon", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			win.SetOverlayIcon(args[0].String())
		}
		return engine.Undefined(), nil
	}))
	// setMenu(menuTemplate)：窗口菜单栏（平台支持时生效）
	_ = obj.Set("setMenu", engine.NewFunction("setMenu", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 && args[0].IsObject() {
			if menuObj, ok := args[0].AsObject(); ok {
				win.SetMenu(&gui.Menu{Items: parseMenuItems(ctx, menuObj)})
			}
		}
		return engine.Undefined(), nil
	}))
	_ = obj.Set("getTitle", engine.NewFunction("getTitle", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(win.GetTitle()), nil
	}))

	_ = obj.Set("isMaximized", engine.NewFunction("isMaximized", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(win.IsMaximized()), nil
	}))

	_ = obj.Set("isFullscreen", engine.NewFunction("isFullscreen", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(win.IsFullscreen()), nil
	}))

	_ = obj.Set("setOpacity", engine.NewFunction("setOpacity", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			if f, ok := args[0].Float(); ok {
				win.SetOpacity(f)
			}
		}
		return engine.Undefined(), nil
	}))

	_ = obj.Set("setFullscreen", engine.NewFunction("setFullscreen", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			b, _ := args[0].Bool()
			win.SetFullscreen(b)
		}
		return engine.Undefined(), nil
	}))

	_ = obj.Set("openDevTools", engine.NewFunction("openDevTools", func(args []engine.Value) (engine.Value, error) {
		win.OpenDevTools()
		return engine.Undefined(), nil
	}))

	_ = obj.Set("executeScript", engine.NewFunction("executeScript", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			win.ExecuteScript(args[0].String())
		}
		return engine.Undefined(), nil
	}))

	_ = obj.Set("on", engine.NewFunction("on", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("window.on requires event and callback")
		}
		evt := args[0].String()
		fn, ok := args[1].AsFunction()
		if !ok {
			return nil, fmt.Errorf("window.on handler must be a function")
		}

		dispose := win.On(evt, func(data interface{}) {
			release := ctx.AddRef()
			ctx.PostTask(func() {
				defer release()
				_, _ = fn.Call([]engine.Value{jsonToEngine(data)})
				ctx.FlushMicrotasks()
			})
		})
		// 按事件名归档 disposer，供 off(event) 定向注销。
		winDisposers[evt] = append(winDisposers[evt], dispose)
		return engine.Undefined(), nil
	}))

	_ = obj.Set("off", engine.NewFunction("off", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("window.off requires event name")
		}
		// 定向注销该事件名下的全部处理器；语义与回调身份由 Go 侧保证。
		for _, d := range winDisposers[args[0].String()] {
			if d != nil {
				d()
			}
		}
		delete(winDisposers, args[0].String())
		return engine.Undefined(), nil
	}))

	_ = obj.Set("emit", engine.NewFunction("emit", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("window.emit requires event name")
		}
		evt := args[0].String()
		var data interface{}
		if len(args) > 1 {
			data = valueToJSONHelper(args[1])
		}
		win.Emit(evt, data)
		return engine.Undefined(), nil
	}))

	return obj
}

func wrapTrayInstance(ctx engine.Context, tray *gui.Tray) engine.Value {
	obj := engine.NewObject()
	_ = obj.Set("id", engine.Number(float64(tray.ID())))

	_ = obj.Set("setIcon", engine.NewFunction("setIcon", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			tray.SetIcon(args[0].String())
		}
		return engine.Undefined(), nil
	}))

	_ = obj.Set("setTooltip", engine.NewFunction("setTooltip", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 {
			tray.SetTooltip(args[0].String())
		}
		return engine.Undefined(), nil
	}))

	_ = obj.Set("setMenu", engine.NewFunction("setMenu", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 && args[0].IsObject() {
			if arr, ok := args[0].AsObject(); ok {
				tray.SetMenu(parseMenuItems(ctx, arr))
			}
		}
		return engine.Undefined(), nil
	}))

	_ = obj.Set("destroy", engine.NewFunction("destroy", func(args []engine.Value) (engine.Value, error) {
		tray.Destroy()
		return engine.Undefined(), nil
	}))

	_ = obj.Set("on", engine.NewFunction("on", func(args []engine.Value) (engine.Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("tray.on requires event and callback")
		}
		evt := args[0].String()
		fn, ok := args[1].AsFunction()
		if !ok {
			return nil, fmt.Errorf("tray.on handler must be a function")
		}

		tray.On(evt, func(data interface{}) {
			release := ctx.AddRef()
			ctx.PostTask(func() {
				defer release()
				_, _ = fn.Call([]engine.Value{jsonToEngine(data)})
				ctx.FlushMicrotasks()
			})
		})
		return engine.Undefined(), nil
	}))

	return obj
}
