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
func GenerateBridgeScript(windowID uint64) string {
	return fmt.Sprintf(`(function() {
  if (window.aluka) return;

  var windowID = %d;
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
})();`, windowID)
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
