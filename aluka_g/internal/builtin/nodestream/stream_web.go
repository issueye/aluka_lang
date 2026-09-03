package nodestream

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewStreamWeb exposes the Web Streams constructors already installed on the
// global object. Keeping one constructor identity is important for undici's
// webidl instanceof checks.
func NewStreamWeb(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()
	for _, name := range []string{"ReadableStream", "WritableStream", "TransformStream"} {
		value, err := ctx.Global().Get(name)
		if err != nil || value.IsUndefined() {
			return engine.Undefined(), fmt.Errorf("stream/web: global %s is unavailable", name)
		}
		_ = m.Set(name, value)
	}
	_ = m.Set("ReadableStreamTee", engine.NewFunction("ReadableStreamTee", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		stream, ok := args[0].AsObject()
		if !ok {
			return engine.Undefined(), fmt.Errorf("ReadableStreamTee: stream must be an object")
		}
		tee, err := stream.Get("tee")
		if err == nil && tee.IsFunction() {
			f, _ := tee.AsFunction()
			return f.Call(nil)
		}
		return engine.NewArray([]engine.Value{args[0], args[0]}), nil
	}))
	return m, nil
}
