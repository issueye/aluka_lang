package builtin

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewVMModule exposes node:vm's callable surface without claiming support for
// dynamic code generation. Packages such as jiti inspect this module during
// startup even when they never evaluate extension code.
func NewVMModule(_ engine.Context) (engine.Value, error) {
	mod := engine.NewObject()
	unavailable := func(name string) engine.Value {
		return engine.NewFunction(name, func(args []engine.Value) (engine.Value, error) {
			return engine.Undefined(), fmt.Errorf("%w: node:vm %s is unavailable", engine.ErrTypeError, name)
		})
	}
	for _, name := range []string{"runInThisContext", "runInContext", "runInNewContext", "compileFunction", "Script", "SourceTextModule", "SyntheticModule"} {
		_ = mod.Set(name, unavailable(name))
	}
	_ = mod.Set("createContext", engine.NewFunction("createContext", func(args []engine.Value) (engine.Value, error) {
		if len(args) > 0 && args[0].IsObject() {
			return args[0], nil
		}
		return engine.NewObject(), nil
	}))
	_ = mod.Set("isContext", engine.NewFunction("isContext", func(args []engine.Value) (engine.Value, error) {
		return engine.Boolean(false), nil
	}))
	return mod, nil
}
