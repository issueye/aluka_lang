package module

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/aluka-lang/aluka/internal/engine"
)

// loadCJS loads and executes a CommonJS module.
// It sets up require/module/exports/__filename/__dirname as globals
// (with save/restore for nested requires), evaluates the source, and
// returns module.exports.
func (l *Loader) loadCJS(absPath string) (engine.Value, error) {
	// Check cache (double-check after lock released)
	l.mu.Lock()
	if cached, ok := l.cache[absPath]; ok {
		l.mu.Unlock()
		return cached, nil
	}
	l.mu.Unlock()

	src, err := os.ReadFile(absPath)
	if err != nil {
		return engine.Undefined(), fmt.Errorf("module: cannot read %q: %w", absPath, err)
	}

	// Create module and exports objects
	exports := engine.NewObject()
	moduleObj := engine.NewObject()
	_ = moduleObj.Set("exports", exports)
	_ = moduleObj.Set("id", engine.Str(absPath))
	_ = moduleObj.Set("filename", engine.Str(absPath))
	_ = moduleObj.Set("loaded", engine.Boolean(false))
	_ = moduleObj.Set("path", engine.Str(filepath.Dir(absPath)))

	// Pre-populate cache with the initial exports object to handle
	// circular dependencies (Node returns the current module.exports).
	l.mu.Lock()
	l.cache[absPath] = exports
	l.mu.Unlock()

	// Set up module-scoped globals with save/restore
	oldGlobals := l.saveGlobals(absPath)
	l.setGlobals(absPath, moduleObj, exports)

	// Evaluate the module source
	_, evalErr := l.ctx.Eval(string(src), absPath)

	// Restore globals
	l.restoreGlobals(oldGlobals)

	if evalErr != nil {
		// Remove from cache on error
		l.mu.Lock()
		delete(l.cache, absPath)
		l.mu.Unlock()
		return engine.Undefined(), fmt.Errorf("module: error in %q: %w", absPath, evalErr)
	}

	// Get the final module.exports (may have been reassigned)
	finalExports, _ := moduleObj.Get("exports")

	// Mark as loaded
	_ = moduleObj.Set("loaded", engine.Boolean(true))

	// Update cache with final exports
	l.mu.Lock()
	l.cache[absPath] = finalExports
	l.mu.Unlock()

	return finalExports, nil
}

// loadCJSFile is like loadCJS but discards the return value (for Run).
func (l *Loader) loadCJSFile(absPath string) error {
	_, err := l.loadCJS(absPath)
	return err
}

// savedGlobals holds the previous values of module-scoped globals.
type savedGlobals struct {
	require   engine.Value
	module    engine.Value
	exports   engine.Value
	filename  engine.Value
	dirname   engine.Value
	importFn  engine.Value
	hasRequire  bool
	hasModule   bool
	hasExports  bool
	hasFilename bool
	hasDirname  bool
	hasImport   bool
}

// saveGlobals saves the current values of module-scoped globals.
func (l *Loader) saveGlobals(path string) savedGlobals {
	g := l.ctx.Global()
	var s savedGlobals

	if v, err := g.Get("require"); err == nil && !v.IsUndefined() {
		s.require = v
		s.hasRequire = true
	}
	if v, err := g.Get("module"); err == nil && !v.IsUndefined() {
		s.module = v
		s.hasModule = true
	}
	if v, err := g.Get("exports"); err == nil && !v.IsUndefined() {
		s.exports = v
		s.hasExports = true
	}
	if v, err := g.Get("__filename"); err == nil && !v.IsUndefined() {
		s.filename = v
		s.hasFilename = true
	}
	if v, err := g.Get("__dirname"); err == nil && !v.IsUndefined() {
		s.dirname = v
		s.hasDirname = true
	}
	if v, err := g.Get("__import"); err == nil && !v.IsUndefined() {
		s.importFn = v
		s.hasImport = true
	}

	return s
}

// setGlobals sets the module-scoped globals for a CJS module.
func (l *Loader) setGlobals(path string, moduleObj engine.Object, exports engine.Value) {
	g := l.ctx.Global()
	requireFn := l.makeRequireFunc(path)
	_ = g.Set("require", requireFn)
	_ = g.Set("module", moduleObj)
	_ = g.Set("exports", exports)
	_ = g.Set("__filename", engine.Str(path))
	_ = g.Set("__dirname", engine.Str(filepath.Dir(path)))
	// 动态 import()：注入 __import 全局（parser 把 import(spec) lower 成 __import(spec)）。
	_ = g.Set("__import", l.makeImportFunc(path))
}

// restoreGlobals restores the previous values of module-scoped globals.
func (l *Loader) restoreGlobals(s savedGlobals) {
	g := l.ctx.Global()
	if s.hasRequire {
		_ = g.Set("require", s.require)
	} else {
		g.Delete("require")
	}
	if s.hasModule {
		_ = g.Set("module", s.module)
	} else {
		g.Delete("module")
	}
	if s.hasExports {
		_ = g.Set("exports", s.exports)
	} else {
		g.Delete("exports")
	}
	if s.hasFilename {
		_ = g.Set("__filename", s.filename)
	} else {
		g.Delete("__filename")
	}
	if s.hasDirname {
		_ = g.Set("__dirname", s.dirname)
	} else {
		g.Delete("__dirname")
	}
	if s.hasImport {
		_ = g.Set("__import", s.importFn)
	} else {
		g.Delete("__import")
	}
}
