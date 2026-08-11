package engine

import "testing"

func TestOwnDataProperty(t *testing.T) {
	proto := NewObject()
	if err := proto.Set("inherited", Number(1)); err != nil {
		t.Fatal(err)
	}
	obj := NewObject()
	SetProto(obj, proto)
	if err := obj.Set("own", Str("value")); err != nil {
		t.Fatal(err)
	}
	if value, ok := OwnDataProperty(obj, "own"); !ok || value != Str("value") {
		t.Fatalf("own property = %v, %t", value, ok)
	}
	if _, ok := OwnDataProperty(obj, "inherited"); ok {
		t.Fatal("prototype property accepted as own data property")
	}
	SetAccessor(obj, "accessor", Undefined(), Undefined())
	if _, ok := OwnDataProperty(obj, "accessor"); ok {
		t.Fatal("accessor accepted as own data property")
	}
	if _, ok := OwnDataProperty(NewArray(nil), "length"); ok {
		t.Fatal("non-plain object accepted as own data property")
	}
}

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

// TestGuardedMethodLookup covers the R1-6 method primitive: plain
// object-value chains resolve methods without invoking accessors, Proxy traps
// or other user code; accessor properties are returned as values (never
// executed); non-plain receivers and non-plain prototype links fall back.
func TestGuardedMethodLookup(t *testing.T) {
	proto := NewObject()
	if err := proto.Set("protoMethod", Str("from-proto")); err != nil {
		t.Fatal(err)
	}
	obj := NewObject()
	if err := obj.Set("ownMethod", Str("from-own")); err != nil {
		t.Fatal(err)
	}
	getter := NewFunction("accGetter", func(args []Value) (Value, error) { return Str("executed"), nil })
	SetAccessor(obj, "acc", getter, nil)
	plainObj := obj.(*objectValue)
	plainProto := proto.(*objectValue)
	plainObj.proto = plainProto

	if v, ok := GuardedMethodLookup(obj, "ownMethod"); !ok || v != Str("from-own") {
		t.Fatalf("own method lookup = %v, %v", v, ok)
	}
	// Prototype-chain resolution on plain object values.
	if v, ok := GuardedMethodLookup(obj, "protoMethod"); !ok || v != Str("from-proto") {
		t.Fatalf("prototype method lookup = %v, %v", v, ok)
	}
	// An accessor is returned as a value, never invoked.
	if v, ok := GuardedMethodLookup(obj, "acc"); !ok {
		t.Fatalf("accessor lookup = %v, %v", v, ok)
	} else if _, isAccessor := v.(*AccessorValue); !isAccessor {
		t.Fatalf("accessor value not preserved: %T", v)
	}
	if _, ok := GuardedMethodLookup(obj, "missing"); ok {
		t.Fatal("missing property must not resolve")
	}
	// Function objects unbox to their plain object value, whose data lookup is
	// side-effect free, so method lookup on them is allowed.
	fn := NewFunction("f", func(args []Value) (Value, error) { return Undefined(), nil })
	fnObj, _ := fn.AsObject()
	if v, ok := GuardedMethodLookup(fnObj, "name"); !ok || v != Str("f") {
		t.Fatalf("function-object method lookup = %v, %v", v, ok)
	}
	// A plain object whose prototype is not a plain object value (embedded
	// type such as an array) must fall back instead of running user code.
	weird := NewObject().(*objectValue)
	weird.proto = NewArray(nil)
	if _, ok := GuardedMethodLookup(weird, "length"); ok {
		t.Fatal("non-plain prototype link must fall back")
	}
}
