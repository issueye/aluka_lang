package module

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/aluka-lang/aluka/internal/engine"
)

// Loader loads and caches modules. It supports both CommonJS (require) and
// ESM (import/export) module formats.
type Loader struct {
	ctx      engine.Context
	resolver *Resolver

	mu    sync.Mutex
	cache map[string]engine.Value // resolved path → module.exports value

	// bcCache 是字节码磁盘缓存（1C.14），命中时跳过 parse+compile。
	bcCache bytecodeCache
}

// NewLoader creates a module loader bound to the given context.
func NewLoader(ctx engine.Context) *Loader {
	return &Loader{
		ctx:      ctx,
		resolver: NewResolver(),
		cache:    make(map[string]engine.Value),
	}
}

// SetNoCache 禁用字节码缓存（对应 --no-cache）。
func (l *Loader) SetNoCache(disabled bool) {
	l.bcCache.disabled = disabled
}

// Run is the entry point for executing a file as the main module.
// It determines the module type (ESM or CJS) from the file extension and
// package.json, then loads and executes the module.
func (l *Loader) Run(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("module: cannot resolve path %q: %w", path, err)
	}

	mt := l.resolver.ModuleType(absPath)
	switch mt {
	case "module":
		return l.loadESMFile(absPath)
	case "json":
		return l.loadJSONFile(absPath)
	default:
		return l.loadCJSFile(absPath)
	}
}

// require is the CJS require function for a given parent module path.
// It resolves the specifier, checks the cache, and loads the module.
func (l *Loader) require(specifier, parentPath string) (engine.Value, error) {
	resolved, err := l.resolver.Resolve(specifier, parentPath)
	if err != nil {
		return engine.Undefined(), err
	}

	absPath, err := filepath.Abs(resolved)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("module: cannot resolve path: %w", err)
	}

	l.mu.Lock()
	if cached, ok := l.cache[absPath]; ok {
		l.mu.Unlock()
		return cached, nil
	}
	l.mu.Unlock()

	mt := l.resolver.ModuleType(absPath)
	switch mt {
	case "module":
		return l.loadESM(absPath)
	case "json":
		return l.loadJSON(absPath)
	default:
		return l.loadCJS(absPath)
	}
}

// loadJSON loads a .json file by parsing it and returning the resulting value.
func (l *Loader) loadJSON(absPath string) (engine.Value, error) {
	l.mu.Lock()
	if cached, ok := l.cache[absPath]; ok {
		l.mu.Unlock()
		return cached, nil
	}
	l.mu.Unlock()

	data, err := os.ReadFile(absPath)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("module: cannot read %q: %w", absPath, err)
	}

	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return engine.Undefined(), fmt.Errorf("module: invalid JSON in %q: %w", absPath, err)
	}

	result := jsonToValue(v)
	l.mu.Lock()
	l.cache[absPath] = result
	l.mu.Unlock()
	return result, nil
}

// loadJSONFile is like loadJSON but discards the return value (for Run).
func (l *Loader) loadJSONFile(absPath string) error {
	_, err := l.loadJSON(absPath)
	return err
}

// jsonToValue converts a Go value (from encoding/json) to an engine.Value.
func jsonToValue(v interface{}) engine.Value {
	switch val := v.(type) {
	case nil:
		return engine.Null()
	case bool:
		return engine.Boolean(val)
	case float64:
		return engine.Number(val)
	case string:
		return engine.Str(val)
	case []interface{}:
		elems := make([]engine.Value, len(val))
		for i, e := range val {
			elems[i] = jsonToValue(e)
		}
		return engine.NewArray(elems)
	case map[string]interface{}:
		obj := engine.NewObject()
		for k, e := range val {
			_ = obj.Set(k, jsonToValue(e))
		}
		return obj
	default:
		return engine.Undefined()
	}
}

// makeRequireFunc creates a JS require function for the given module path.
func (l *Loader) makeRequireFunc(modulePath string) engine.Function {
	return engine.NewFunction("require", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("require: missing module specifier")
		}
		spec := args[0].String()
		return l.require(spec, modulePath)
	})
}

// makeImportFunc creates a JS dynamic-import function for the given module
// path. It implements ES2020 dynamic import(): always returns a Promise that
// resolves to the module's namespace (exports) or rejects on load failure.
//
// 实现说明：动态 import 复用 require() 的同步加载链路，再用全局 Promise
// 把结果包装成已 settled 的 Promise。通过 engine.Function.Call 调用
// Promise.resolve / Promise.reject 静态方法，避免依赖 interpreter 包。
func (l *Loader) makeImportFunc(modulePath string) engine.Function {
	return engine.NewFunction("__import", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return l.rejectImport(fmt.Errorf("import: missing module specifier"))
		}
		spec := args[0].String()
		exports, err := l.require(spec, modulePath)
		if err != nil {
			return l.rejectImport(err)
		}
		return l.resolveImport(exports)
	})
}

// resolveImport wraps a value in a resolved Promise via the global Promise.resolve.
func (l *Loader) resolveImport(v engine.Value) (engine.Value, error) {
	promiseCtor, err := l.ctx.Global().Get("Promise")
	if err != nil || !promiseCtor.IsFunction() {
		// 回退：若全局无 Promise（不应发生），直接返回原值。
		return v, nil
	}
	// Promise 构造器同时也是对象，取其 resolve/reject 静态方法。
	if ctorObj, ok := promiseCtor.AsObject(); ok {
		resolveFn, err := ctorObj.Get("resolve")
		if err == nil && resolveFn.IsFunction() {
			if rf, ok := resolveFn.AsFunction(); ok {
				return rf.Call([]engine.Value{v})
			}
		}
	}
	return v, nil
}

// rejectImport wraps an error in a rejected Promise via the global Promise.reject.
func (l *Loader) rejectImport(err error) (engine.Value, error) {
	promiseCtor, e := l.ctx.Global().Get("Promise")
	if e != nil || !promiseCtor.IsFunction() {
		// 回退：让错误同步抛出。
		return engine.Undefined(), err
	}
	if ctorObj, ok := promiseCtor.AsObject(); ok {
		rejectFn, e := ctorObj.Get("reject")
		if e == nil && rejectFn.IsFunction() {
			if rf, ok := rejectFn.AsFunction(); ok {
				// 用字符串包装错误消息（后续可改为构造 Error 对象）。
				errVal := engine.Str(err.Error())
				return rf.Call([]engine.Value{errVal})
			}
		}
	}
	return engine.Undefined(), err
}
