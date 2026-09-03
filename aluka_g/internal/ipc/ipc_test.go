package ipc

import (
	"testing"
	"time"
)

// TestAIPHeaderCodec 验证 16 字节头部编解码
func TestAIPHeaderCodec(t *testing.T) {
	orig := Header{
		Magic:      MagicNumber,
		Version:    ProtoVersion,
		Flags:      FlagCompressed | FlagBinaryRaw,
		MsgType:    OpRPCRequest,
		SequenceID: 1024,
		PayloadLen: 65536,
	}

	buf := EncodeHeader(orig)
	if len(buf) != HeaderLength {
		t.Fatalf("Expected header len %d, got %d", HeaderLength, len(buf))
	}

	decoded, err := DecodeHeader(buf)
	if err != nil {
		t.Fatalf("DecodeHeader failed: %v", err)
	}

	if decoded.Magic != orig.Magic || decoded.Version != orig.Version ||
		decoded.Flags != orig.Flags || decoded.MsgType != orig.MsgType ||
		decoded.SequenceID != orig.SequenceID || decoded.PayloadLen != orig.PayloadLen {
		t.Fatalf("Header mismatch: got %+v, want %+v", decoded, orig)
	}
}

// TestAIPRPCAndEvents 验证 Server/Client 完整的 RPC 与事件交互
func TestAIPRPCAndEvents(t *testing.T) {
	server, err := NewServer("tcp:127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()

	// 注册 RPC 方法
	server.RegisterMethod("add", func(params interface{}) (interface{}, error) {
		arr, ok := params.([]interface{})
		if !ok || len(arr) < 2 {
			return 0, nil
		}
		a := arr[0].(float64)
		b := arr[1].(float64)
		return a + b, nil
	})

	server.RegisterMethod("getUser", func(params interface{}) (interface{}, error) {
		return map[string]interface{}{
			"id":   "u_1001",
			"name": "Aluka Developer",
		}, nil
	})

	// 客户端连接
	client, err := Connect("tcp:" + server.Addr().String())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	// 1. 测试 RPC 调用: add(100, 200)
	res, err := client.Call("add", []interface{}{100, 200}, 5*time.Second)
	if err != nil {
		t.Fatalf("Call 'add' failed: %v", err)
	}
	if val, ok := res.(float64); !ok || val != 300 {
		t.Fatalf("Expected add result 300, got %v", res)
	}

	// 2. 测试 RPC 调用: getUser
	userRes, err := client.Call("getUser", nil, 5*time.Second)
	if err != nil {
		t.Fatalf("Call 'getUser' failed: %v", err)
	}
	userMap, ok := userRes.(map[string]interface{})
	if !ok || userMap["id"] != "u_1001" || userMap["name"] != "Aluka Developer" {
		t.Fatalf("Unexpected user: %v", userRes)
	}

	// 3. 测试事件广播
	eventReceived := make(chan string, 1)
	client.On("notify", func(data interface{}) {
		if s, ok := data.(string); ok {
			eventReceived <- s
		}
	})

	time.Sleep(10 * time.Millisecond)
	server.Broadcast("notify", "Hello Aluka IPC")

	select {
	case msg := <-eventReceived:
		if msg != "Hello Aluka IPC" {
			t.Fatalf("Unexpected event message: %s", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Timed out waiting for broadcast event")
	}
}
