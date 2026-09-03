package interpreter

import (
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
)

func TestAwaitPromiseBalancesPostTaskActivity(t *testing.T) {
	vm, err := NewVM()
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}

	promise := NewPromiseValue(vm.interp)
	release := vm.interp.AddRef()
	vm.interp.PostTask(func() {
		defer release()
		promise.Fulfill(engine.IntValue(42))
	})

	value, err := vm.AwaitPromise(promise)
	if err != nil {
		t.Fatalf("AwaitPromise: %v", err)
	}
	if got, ok := value.Int(); !ok || got != 42 {
		t.Fatalf("AwaitPromise result = %v, want 42", value)
	}

	vm.interp.loopMu.Lock()
	active := vm.interp.active
	vm.interp.loopMu.Unlock()
	if active != 0 {
		t.Fatalf("active tasks after AwaitPromise = %d, want 0", active)
	}
}
