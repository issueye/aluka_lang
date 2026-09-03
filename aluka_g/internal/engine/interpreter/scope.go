// Package interpreter implements an AST-walking JS interpreter.
package interpreter

import "github.com/aluka-lang/aluka/internal/engine"

// Scope represents a lexical environment (variable bindings with parent chain).
type Scope struct {
	vars   map[string]engine.Value
	parent *Scope
	global *Scope
}

// NewScope creates a new root scope.
func NewScope() *Scope {
	s := &Scope{vars: make(map[string]engine.Value)}
	s.global = s
	return s
}

// NewChild creates a child scope that inherits from this scope.
func (s *Scope) NewChild() *Scope {
	return &Scope{
		vars:   make(map[string]engine.Value),
		parent: s,
		global: s.global,
	}
}

// Get looks up a variable, walking the scope chain.
// Returns (value, true) if found, (Undefined, false) if not.
func (s *Scope) Get(name string) (engine.Value, bool) {
	cur := s
	for cur != nil {
		if v, ok := cur.vars[name]; ok {
			return v, true
		}
		cur = cur.parent
	}
	return engine.Undefined(), false
}

// Set assigns to an existing variable, walking the scope chain.
// Returns false if the variable is not found (caller may declare on global).
func (s *Scope) Set(name string, v engine.Value) bool {
	cur := s
	for cur != nil {
		if _, ok := cur.vars[name]; ok {
			cur.vars[name] = v
			return true
		}
		cur = cur.parent
	}
	return false
}

// Declare creates a new binding in this scope.
func (s *Scope) Declare(name string, v engine.Value) {
	s.vars[name] = v
}

// HasOwn checks if this scope directly owns a binding.
func (s *Scope) HasOwn(name string) bool {
	_, ok := s.vars[name]
	return ok
}

// Global returns the global scope.
func (s *Scope) Global() *Scope { return s.global }
