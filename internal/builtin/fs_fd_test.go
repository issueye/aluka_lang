package builtin

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
)

// Opening a descriptor must keep its owning *os.File alive until closeSync.
// Otherwise Go's finalizer can close the raw fd before Node code gets to it.
func TestFSOpenSyncKeepsDescriptorAlive(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := newCtx(t)
	fs, err := NewFS(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "session.jsonl")
	fd := callMethod(t, fs, "openSync", engine.Str(path), engine.Str("wx"))
	callMethod(t, fs, "writeFileSync", fd, engine.Str("alive\n"))
	if _, err := os.Stat(fd.String()); err == nil {
		t.Fatalf("writeFileSync created a file named after fd %s", fd.String())
	}

	// The returned fd is the only JS-visible handle. Force Go finalizers to run
	// before closeSync to reproduce the EBADF seen by pi session persistence.
	runtime.GC()
	runtime.GC()

	if obj, ok := fs.AsObject(); ok {
		closeFn, getErr := obj.Get("closeSync")
		if getErr != nil {
			t.Fatal(getErr)
		}
		fn, ok := closeFn.AsFunction()
		if !ok {
			t.Fatal("fs.closeSync is not a function")
		}
		if _, err := fn.Call([]engine.Value{fd}); err != nil {
			t.Fatalf("closeSync after GC: %v", err)
		}
	} else {
		t.Fatal("fs module is not an object")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "alive\n" {
		t.Fatalf("file content = %q", data)
	}
}
