package builtin

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewTimersModule exposes the context's global timer functions through
// node:timers. Reusing those function objects preserves timer IDs and clear
// operations across global and module-qualified calls.
func NewTimersModule(ctx engine.Context) (engine.Value, error) {
	mod := engine.NewObject()
	for _, name := range []string{
		"setTimeout", "clearTimeout",
		"setInterval", "clearInterval",
		"setImmediate", "clearImmediate",
	} {
		value, err := ctx.Global().Get(name)
		if err != nil || value == nil || value.IsUndefined() {
			return engine.Undefined(), fmt.Errorf("timers: global %s not initialized", name)
		}
		_ = mod.Set(name, value)
	}
	return mod, nil
}
