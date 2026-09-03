package interpreter

import (
	"strings"
	"testing"
)

// TestJSThrowErrorUsesMessageNotObjectGraph：throw Error 时 Go error 文本
// 应是 "Error: …" / stack，而不是把附加的巨型自有属性序列化进去。
func TestJSThrowErrorUsesMessageNotObjectGraph(t *testing.T) {
	_, err := vmEvalTest(t, `
		const payload = { blob: "PAYLOAD_MARKER" };
		const e = new Error("boom");
		e.payload = payload;
		throw e;
	`)
	if err == nil {
		t.Fatal("expected throw")
	}
	msg := err.Error()
	if !strings.Contains(msg, "boom") {
		t.Fatalf("error %q: want boom", msg)
	}
	if strings.Contains(msg, "PAYLOAD_MARKER") {
		t.Fatalf("error %q dumped extra object graph", msg)
	}
}

// TestJSThrowPlainObjectDoesNotInspect：throw 普通对象不得走全量 inspect。
func TestJSThrowPlainObjectDoesNotInspect(t *testing.T) {
	_, err := vmEvalTest(t, `throw { blob: "PLAIN_PAYLOAD_MARKER", nested: { x: 1 } };`)
	if err == nil {
		t.Fatal("expected throw")
	}
	msg := err.Error()
	if strings.Contains(msg, "PLAIN_PAYLOAD_MARKER") {
		t.Fatalf("error %q dumped thrown object", msg)
	}
	if want := "[object Object]"; msg != want {
		t.Fatalf("error %q, want %q", msg, want)
	}
}

// TestJSThrowErrorMessageTruncated：超长 message 必须截断，避免 fmt.Errorf 复制 OOM。
func TestJSThrowErrorMessageTruncated(t *testing.T) {
	_, err := vmEvalTest(t, `throw new Error("x".repeat(40000));`)
	if err == nil {
		t.Fatal("expected throw")
	}
	msg := err.Error()
	if len(msg) > 20*1024 {
		t.Fatalf("exception message still too long: %d", len(msg))
	}
	if !strings.Contains(msg, "truncated") {
		t.Fatalf("error len=%d: expected truncation marker", len(msg))
	}
}
