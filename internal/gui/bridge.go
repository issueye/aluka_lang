package gui

import (
	"encoding/json"
	"fmt"
	"sync"
)

// BridgeMessage 前端与后端通信的数据包结构。
type BridgeMessage struct {
	Type string          `json:"type"` // "event" | "rpc_call" | "rpc_res" | "dialog" | "window_action"
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// GenerateBridgeScript 生成自动注入到前端 WebView 中的 window.aluka 客户端代码。
// frameless 为真时启用无边框拖拽（--aluka-draggable / -webkit-app-region /
// data-aluka-drag 三种声明方式）与边缘缩放。
func GenerateBridgeScript(windowID uint64, frameless bool) string {
	framelessJS := "false"
	if frameless {
		framelessJS = "true"
	}
	return fmt.Sprintf(`(function() {
  if (window.aluka) return;

  var windowID = %d;
  var frameless = %s;
  var eventListeners = {};
  var pendingRPC = {};
  var rpcCounter = 0;

  function postToHost(msg) {
    if (window.chrome && window.chrome.webview && window.chrome.webview.postMessage) {
      window.chrome.webview.postMessage(JSON.stringify(msg));
    } else if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.aluka) {
      window.webkit.messageHandlers.aluka.postMessage(JSON.stringify(msg));
    }
  }

  // 后端广播分发入口
  window.aluka_dispatch = function(type, payload) {
    if (type === 'event') {
      var handlers = eventListeners[payload.name] || [];
      for (var i = 0; i < handlers.length; i++) {
        handlers[i](payload.data);
      }
    } else if (type === 'rpc_res') {
      var p = pendingRPC[payload.id];
      if (p) {
        delete pendingRPC[payload.id];
        if (payload.error) {
          p.reject(new Error(payload.error));
        } else {
          p.resolve(payload.result);
        }
      }
    }
  };

  // ---------- 无边框拖拽 / 边缘缩放 ----------
  // 拖拽区判定（自目标向上遍历，三种声明方式任选其一，命中即停止）：
  //   1. --aluka-draggable: drag|no-drag  （CSS 自定义属性，Aluka 风格写法）
  //   2. -webkit-app-region: drag|no-drag （Electron 语义）
  //   3. data-aluka-drag / ="no-drag"     （Aluka 属性写法）
  function inDragRegion(el) {
    for (var n = el; n && n.nodeType === 1; n = n.parentElement) {
      if (n.getAttribute) {
        var attr = n.getAttribute('data-aluka-drag');
        if (attr === 'no-drag') return false;
        if (attr !== null) return true;
      }
      var s = window.getComputedStyle(n);
      var d = s.getPropertyValue('--aluka-draggable');
      if (d === 'drag') return true;
      if (d === 'no-drag') return false;
      var r = s.getPropertyValue('-webkit-app-region') || s.appRegion || '';
      if (r === 'drag') return true;
      if (r === 'no-drag') return false;
    }
    return false;
  }

  var EDGE = 6; // 边缘缩放热区（px）
  var CURSORS = {
    left: 'ew-resize', right: 'ew-resize', top: 'ns-resize', bottom: 'ns-resize',
    topLeft: 'nwse-resize', bottomRight: 'nwse-resize',
    topRight: 'nesw-resize', bottomLeft: 'nesw-resize'
  };
  function edgeAt(e) {
    var x = e.clientX, y = e.clientY;
    var l = x < EDGE, r = x > window.innerWidth - EDGE;
    var t = y < EDGE, b = y > window.innerHeight - EDGE;
    if (!l && !r && !t && !b) return null;
    var d = '';
    if (t) d += 'top'; else if (b) d += 'bottom';
    if (l) d += 'left'; else if (r) d += 'right';
    return d;
  }

  if (frameless) {
    document.addEventListener('mousemove', function(e) {
      var d = edgeAt(e);
      document.documentElement.style.cursor = d ? (CURSORS[d] || 'default') : '';
    });
    document.addEventListener('mousedown', function(e) {
      if (e.button !== 0) return;
      var d = edgeAt(e);
      if (d) {
        postToHost({ type: 'window_action', name: 'startResize', data: d });
        return;
      }
      if (inDragRegion(e.target)) {
        postToHost({ type: 'window_action', name: 'startDrag' });
      }
    });
  }

  window.aluka = {
    windowID: windowID,

    // 1. 窗口控制 API
    window: {
      minimize: function() { postToHost({ type: 'window_action', name: 'minimize' }); },
      maximize: function() { postToHost({ type: 'window_action', name: 'maximize' }); },
      unmaximize: function() { postToHost({ type: 'window_action', name: 'unmaximize' }); },
      toggleMaximize: function() { postToHost({ type: 'window_action', name: 'toggleMaximize' }); },
      close: function() { postToHost({ type: 'window_action', name: 'close' }); },
      hide: function() { postToHost({ type: 'window_action', name: 'hide' }); },
      show: function() { postToHost({ type: 'window_action', name: 'show' }); },
      center: function() { postToHost({ type: 'window_action', name: 'center' }); },
      setTitle: function(title) { postToHost({ type: 'window_action', name: 'setTitle', data: title }); },
      setSize: function(w, h) { postToHost({ type: 'window_action', name: 'setSize', data: [w, h] }); }
    },

    // 2. 原生系统对话框 API (返回 Promise)
    dialog: {
      showOpenDialog: function(opts) {
        return new Promise(function(resolve, reject) {
          var id = 'dlg_' + (++rpcCounter);
          pendingRPC[id] = { resolve: resolve, reject: reject };
          postToHost({ type: 'dialog', id: id, name: 'openFile', data: opts || {} });
        });
      },
      showSaveDialog: function(opts) {
        return new Promise(function(resolve, reject) {
          var id = 'dlg_' + (++rpcCounter);
          pendingRPC[id] = { resolve: resolve, reject: reject };
          postToHost({ type: 'dialog', id: id, name: 'saveFile', data: opts || {} });
        });
      },
      showMessageBox: function(opts) {
        return new Promise(function(resolve, reject) {
          var id = 'dlg_' + (++rpcCounter);
          pendingRPC[id] = { resolve: resolve, reject: reject };
          postToHost({ type: 'dialog', id: id, name: 'message', data: opts || {} });
        });
      }
    },

    // 3. 双向事件总线
    events: {
      on: function(event, handler) {
        if (!eventListeners[event]) eventListeners[event] = [];
        eventListeners[event].push(handler);
      },
      off: function(event, handler) {
        if (!eventListeners[event]) return;
        eventListeners[event] = eventListeners[event].filter(function(h) { return h !== handler; });
      },
      emit: function(event, data) {
        postToHost({ type: 'event', name: event, data: data });
      }
    },

    // 4. 远程过程调用 (RPC)
    rpc: {
      call: function(method, params) {
        return new Promise(function(resolve, reject) {
          var id = 'rpc_' + (++rpcCounter);
          pendingRPC[id] = { resolve: resolve, reject: reject };
          postToHost({ type: 'rpc_call', id: id, name: method, data: params });
        });
      }
    }
  };
})();`, windowID, framelessJS)
}

// RPCRegistry 主进程注册给前端调用的 RPC 方法集合。
type RPCRegistry struct {
	mu      sync.RWMutex
	methods map[string]func(params json.RawMessage) (interface{}, error)
}

var globalRPCRegistry = &RPCRegistry{
	methods: make(map[string]func(params json.RawMessage) (interface{}, error)),
}

// RegisterRPCMethod 注册可供前端调用的 RPC 方法。
func RegisterRPCMethod(name string, handler func(params json.RawMessage) (interface{}, error)) {
	globalRPCRegistry.mu.Lock()
	defer globalRPCRegistry.mu.Unlock()
	globalRPCRegistry.methods[name] = handler
}

// HandleWebMessage 处理来自 WebView 前端的消息分发。
func (w *Window) HandleWebMessage(rawMessage string) {
	var msg BridgeMessage
	if err := json.Unmarshal([]byte(rawMessage), &msg); err != nil {
		return
	}

	switch msg.Type {
	case "window_action":
		switch msg.Name {
		case "minimize":
			w.Minimize()
		case "maximize":
			w.Maximize()
		case "unmaximize":
			w.Unmaximize()
		case "toggleMaximize":
			if w.IsMaximized() {
				w.Unmaximize()
			} else {
				w.Maximize()
			}
		case "close":
			w.Close()
		case "hide":
			w.Hide()
		case "show":
			w.Show()
		case "center":
			w.Center()
		case "setTitle":
			var title string
			_ = json.Unmarshal(msg.Data, &title)
			w.SetTitle(title)
		case "setSize":
			var size [2]int
			_ = json.Unmarshal(msg.Data, &size)
			w.SetSize(size[0], size[1])
		case "startDrag":
			// 无边框拖拽：进入系统原生移动循环
			if dr, ok := w.native.(interface{ StartDragMove() }); ok {
				GetApp().PostAction(dr.StartDragMove)
			}
		case "startResize":
			// 无边框边缘缩放：data 为方向（left/topRight/bottom…）
			var dir string
			_ = json.Unmarshal(msg.Data, &dir)
			if rz, ok := w.native.(interface{ StartResize(dir string) }); ok {
				GetApp().PostAction(func() { rz.StartResize(dir) })
			}
		}

	case "event":
		var data interface{}
		_ = json.Unmarshal(msg.Data, &data)
		w.Emit(msg.Name, data)

	case "dialog":
		go func() {
			var opts DialogOptions
			_ = json.Unmarshal(msg.Data, &opts)
			opts.Type = msg.Name

			app := GetApp()
			btnIndex, files, err := app.ShowDialog(opts)

			var resPayload string
			if err != nil {
				resPayload = fmt.Sprintf(`{"id":%q,"error":%q}`, msg.ID, err.Error())
			} else {
				if msg.Name == "openFile" || msg.Name == "saveFile" {
					filesJSON, _ := json.Marshal(files)
					resPayload = fmt.Sprintf(`{"id":%q,"result":%s}`, msg.ID, string(filesJSON))
				} else {
					resPayload = fmt.Sprintf(`{"id":%q,"result":%d}`, msg.ID, btnIndex)
				}
			}
			w.ExecuteScript(fmt.Sprintf("if(window.aluka_dispatch){window.aluka_dispatch('rpc_res', %s);}", resPayload))
		}()

	case "rpc_call":
		go func() {
			globalRPCRegistry.mu.RLock()
			handler, ok := globalRPCRegistry.methods[msg.Name]
			globalRPCRegistry.mu.RUnlock()

			if !ok {
				resPayload := fmt.Sprintf(`{"id":%q,"error":"method %s not found"}`, msg.ID, msg.Name)
				w.ExecuteScript(fmt.Sprintf("if(window.aluka_dispatch){window.aluka_dispatch('rpc_res', %s);}", resPayload))
				return
			}

			result, err := handler(msg.Data)
			var resPayload string
			if err != nil {
				resPayload = fmt.Sprintf(`{"id":%q,"error":%q}`, msg.ID, err.Error())
			} else {
				resJSON, _ := json.Marshal(result)
				resPayload = fmt.Sprintf(`{"id":%q,"result":%s}`, msg.ID, string(resJSON))
			}
			w.ExecuteScript(fmt.Sprintf("if(window.aluka_dispatch){window.aluka_dispatch('rpc_res', %s);}", resPayload))
		}()
	}
}
