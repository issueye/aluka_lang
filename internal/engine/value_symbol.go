// Symbol 值：well-known symbol、Symbol.for 全局注册表与 symbol 属性键编码。

package engine

import (
	"fmt"
	"strings"
	"sync"
)

// SymbolValue is a JS Symbol primitive. Symbols are unique, immutable values
// used as property keys (especially for well-known protocols like Symbol.iterator).
type SymbolValue struct {
	desc string
	id   uint64
}

var symbolCounter uint64

var symbolKeys = struct {
	sync.RWMutex
	values map[string]*SymbolValue
}{values: make(map[string]*SymbolValue)}

// NewSymbol creates a new unique symbol with the given description.
func NewSymbol(desc string) *SymbolValue {
	symbolCounter++
	return &SymbolValue{desc: desc, id: symbolCounter}
}

// SymbolIterator returns the well-known Symbol.iterator symbol.
var SymbolIterator = &SymbolValue{desc: "Symbol.iterator", id: 0}

// SymbolAsyncIterator returns the well-known Symbol.asyncIterator symbol.
var SymbolAsyncIterator = &SymbolValue{desc: "Symbol.asyncIterator", id: 1}

// SymbolHasInstance is the well-known Symbol.hasInstance symbol.
var SymbolHasInstance = &SymbolValue{desc: "Symbol.hasInstance", id: 2}

// SymbolToPrimitive is the well-known Symbol.toPrimitive symbol.
var SymbolToPrimitive = &SymbolValue{desc: "Symbol.toPrimitive", id: 3}

// SymbolToStringTag is the well-known Symbol.toStringTag symbol.
var SymbolToStringTag = &SymbolValue{desc: "Symbol.toStringTag", id: 4}

// SymbolMatch is the well-known Symbol.match symbol (String.prototype.match dispatch).
var SymbolMatch = &SymbolValue{desc: "Symbol.match", id: 5}

// SymbolReplace is the well-known Symbol.replace symbol.
var SymbolReplace = &SymbolValue{desc: "Symbol.replace", id: 6}

// SymbolSearch is the well-known Symbol.search symbol.
var SymbolSearch = &SymbolValue{desc: "Symbol.search", id: 7}

// SymbolSplit is the well-known Symbol.split symbol.
var SymbolSplit = &SymbolValue{desc: "Symbol.split", id: 8}

// SymbolSpecies is the well-known Symbol.species symbol.
var SymbolSpecies = &SymbolValue{desc: "Symbol.species", id: 9}

// symbolRegistry implements the global Symbol registry for Symbol.for()/keyFor().
var symbolRegistry = struct {
	entries map[string]*SymbolValue
	order   []string
}{
	entries: make(map[string]*SymbolValue),
}

// SymbolFor returns the shared symbol registered under the given key, creating
// a new one if none exists yet. Implements the global Symbol registry.
func SymbolFor(key string) *SymbolValue {
	if s, ok := symbolRegistry.entries[key]; ok {
		return s
	}
	symbolCounter++
	s := &SymbolValue{desc: key, id: symbolCounter}
	symbolRegistry.entries[key] = s
	symbolRegistry.order = append(symbolRegistry.order, key)
	return s
}

// KeyFor returns the registry key under which the symbol was registered via
// SymbolFor, or ("", false) if the symbol is not in the global registry.
func (s *SymbolValue) KeyFor() (string, bool) {
	for k, v := range symbolRegistry.entries {
		if v == s {
			return k, true
		}
	}
	return "", false
}

func (s *SymbolValue) Type() ValueType { return TypeSymbol }

func (s *SymbolValue) String() string {
	if s.desc == "" {
		return "Symbol()"
	}
	return "Symbol(" + s.desc + ")"
}

func (s *SymbolValue) Int() (int, bool) { return 0, false }

func (s *SymbolValue) Float() (float64, bool) { return 0, false }

func (s *SymbolValue) Bool() (bool, bool) { return true, true }

func (s *SymbolValue) IsUndefined() bool { return false }

func (s *SymbolValue) IsNull() bool { return false }

func (s *SymbolValue) IsObject() bool { return false }

func (s *SymbolValue) IsFunction() bool { return false }

func (s *SymbolValue) AsObject() (Object, bool) { return nil, false }

func (s *SymbolValue) AsFunction() (Function, bool) { return nil, false }

// SymbolKey returns the string key used to store a symbol-keyed property.
// Since our object implementation uses string keys, we map symbols to unique
// internal strings.
func (s *SymbolValue) SymbolKey() string {
	key := fmt.Sprintf("\x00symbol:%d:%s", s.id, s.desc)
	symbolKeys.Lock()
	symbolKeys.values[key] = s
	symbolKeys.Unlock()
	return key
}

// SymbolFromKey returns the Symbol represented by an internal property key.
func SymbolFromKey(key string) (*SymbolValue, bool) {
	symbolKeys.RLock()
	sym, ok := symbolKeys.values[key]
	symbolKeys.RUnlock()
	return sym, ok
}

// IsSymbolKey reports whether key is an internal symbol property key.
func IsSymbolKey(key string) bool { return strings.HasPrefix(key, "\x00symbol:") }

// IsWellKnown returns true if this is a well-known symbol (id < 100).
func (s *SymbolValue) IsWellKnown() bool { return s.id < 100 }
