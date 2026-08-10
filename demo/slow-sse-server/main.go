// slow-sse-server: 延迟首包（TTFT）的 Anthropic SSE 流式响应服务器。
package main

import (
	"fmt"
	"net/http"
	"time"
)

func main() {
	http.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		fmt.Println("stream: headers flushed, sleeping before first token")
		time.Sleep(3 * time.Second)
		sse := func(e string) {
			_, _ = fmt.Fprint(w, e)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		sse("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\n")
		sse("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Println("stream: sent start blocks")
		for i := 0; i < 5; i++ {
			time.Sleep(500 * time.Millisecond)
			sse(fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"token%d \"}}\n\n", i))
			fmt.Printf("stream: sent token %d\n", i)
		}
		sse("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":6}}\n\n")
		sse("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		fmt.Println("stream: done")
	})
	fmt.Println("listening on :18321")
	_ = http.ListenAndServe("127.0.0.1:18321", nil)
}
