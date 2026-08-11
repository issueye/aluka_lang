package native

import (
	"reflect"
	"testing"
)

// TestFrameIsPointerFree audits the R2 hard-stop condition "Go pointers must
// never enter the Native Frame": every Frame field must be a numeric type, so
// the Go type system guarantees the struct has no pointer slots and the GC
// never scans generated-code frames.
func TestFrameIsPointerFree(t *testing.T) {
	typ := reflect.TypeOf(Frame{})
	if typ.NumField() == 0 {
		t.Fatal("Frame has no fields")
	}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !pointerFreeType(f.Type) {
			t.Fatalf("Frame field %s has type %s with possible Go pointer slots", f.Name, f.Type)
		}
	}
}

// pointerFreeType reports whether a type can never hold a Go pointer:
// numeric/boolean scalars and arrays thereof. Structs, strings, slices,
// interfaces and pointers are rejected.
func pointerFreeType(typ reflect.Type) bool {
	switch typ.Kind() {
	case reflect.Float64, reflect.Uint64, reflect.Bool, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return true
	case reflect.Array:
		return pointerFreeType(typ.Elem())
	default:
		return false
	}
}
