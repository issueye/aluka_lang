package engine

import "testing"

func TestNumericOwnProperty(t *testing.T) {
	obj := NewObject()
	if err := obj.Set("x", Number(3.5)); err != nil {
		t.Fatal(err)
	}
	n, shape, slot, ok := NumericOwnProperty(obj, "x")
	if !ok || n != 3.5 || shape == 0 || slot != 0 {
		t.Fatalf("number=%v shape=%d slot=%d ok=%v", n, shape, slot, ok)
	}
	if err := obj.Set("x", Str("changed")); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := NumericOwnProperty(obj, "x"); ok {
		t.Fatal("non-number property accepted")
	}
}

func TestGuardedSetNumericOwnProperty(t *testing.T) {
	obj := NewObject()
	if err := obj.Set("x", Number(1)); err != nil {
		t.Fatal(err)
	}
	_, shape, slot, ok := NumericOwnProperty(obj, "x")
	if !ok || !GuardedSetNumericOwnProperty(obj, "x", shape, slot, -0.0) {
		t.Fatal("guarded numeric property write failed")
	}
	value, err := obj.Get("x")
	if err != nil {
		t.Fatal(err)
	}
	number, _ := value.Float()
	if number != 0 || !GuardedSetNumericOwnProperty(obj, "x", shape, slot, 4.5) {
		t.Fatalf("value=%v", value)
	}
	if err := obj.Set("x", Str("changed")); err != nil {
		t.Fatal(err)
	}
	if GuardedSetNumericOwnProperty(obj, "x", shape, slot, 9) {
		t.Fatal("non-number property accepted by guarded write")
	}
	if GuardedSetNumericOwnProperty(obj, "missing", shape, slot, 9) {
		t.Fatal("mismatched property slot accepted by guarded write")
	}
}
