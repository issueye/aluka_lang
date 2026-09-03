package globals

// Phase 3 WebSocket 测试：JS 客户端连接 Go WS echo 服务器。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// TestWebSocketEcho 验证 WebSocket 连接/发送/接收/关闭。
func TestWebSocketEcho(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				break
			}
			if err := conn.WriteMessage(websocket.TextMessage, []byte("echo:"+string(data))); err != nil {
				break
			}
		}
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	ctx := newFetchTestEnv(t)
	code := `
var ws = new WebSocket('` + wsURL + `');
var log = [];
ws.onopen = function() { log.push('open'); ws.send('ping'); };
ws.onmessage = function(e) { globalThis.__echo = e.data; ws.close(); };
ws.onclose = function() { log.push('close'); globalThis.__log = log.join(','); };
`
	if err := fetchRun(t, ctx, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := webGlobalGet(ctx, "__echo"); got != "echo:ping" {
		t.Errorf("echo = %q, want echo:ping", got)
	}
	if got := webGlobalGet(ctx, "__log"); got != "open,close" {
		t.Errorf("log = %q, want open,close", got)
	}
}
