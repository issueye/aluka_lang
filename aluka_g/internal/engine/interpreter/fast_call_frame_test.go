package interpreter

import (
	"bytes"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/bytecode"
)

func TestVMFastCallFrameReservesAllLocals(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string
	}{
		{
			name: "no arguments",
			code: `function f() { var a = 11; var b = 31; return a + b; } f()`,
			want: "42",
		},
		{
			name: "partial arguments",
			code: `function f(a, b, c) { var last = c; return a + b + (last || 0); } f(10, 20)`,
			want: "30",
		},
		{
			name: "all arguments",
			code: `function f(a, b, c) { var last = c; return a + b + last; } f(10, 20, 12)`,
			want: "42",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vmEvalStr(t, tc.code); got != tc.want {
				t.Fatalf("VM.Eval = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestVMFastCallFrameCleanupAfterError(t *testing.T) {
	got, err := vmEvalStrErr(t, `
		function fail() { var last = 1; throw new Error("boom"); }
		try { fail(); } catch (e) { e.message }
	`)
	if err != nil {
		t.Fatalf("VM.Eval: %v", err)
	}
	if got != "boom" {
		t.Fatalf("caught error = %q, want %q", got, "boom")
	}
}

func TestVMSerializedFastCallFrame(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}
	mod, err := vm.Compile(`
		function f(a, b) { var first = a; var last = b; return first + last; }
		f(19, 23)
	`, "serialized-fast-call.js")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	var buf bytes.Buffer
	if err := bytecode.Serialize(&buf, mod); err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	decoded, err := bytecode.Deserialize(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Deserialize: %v", err)
	}
	if len(decoded.Functions) < 2 {
		t.Fatalf("decoded function count = %d, want at least 2", len(decoded.Functions))
	}
	fn := decoded.Functions[1]
	if fn.NumLocals <= fn.NumParams {
		t.Fatalf("decoded locals=%d params=%d, want hidden/local slots", fn.NumLocals, fn.NumParams)
	}

	got, err := vm.RunModule(decoded)
	if err != nil {
		t.Fatalf("RunModule: %v", err)
	}
	if got.String() != "42" {
		t.Fatalf("RunModule = %q, want %q", got.String(), "42")
	}
}
