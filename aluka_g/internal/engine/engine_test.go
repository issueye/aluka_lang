package engine

import (
	"strings"
	"testing"
)

// TestStubEngineEvalArithmetic 验证桩引擎能求值基本算术表达式。
func TestStubEngineEvalArithmetic(t *testing.T) {
	cases := []struct {
		code string
		want float64
	}{
		{"1 + 1", 2},
		{"2 * 3", 6},
		{"10 - 4", 6},
		{"20 / 4", 5},
		{"2 + 3 * 4", 14}, // 优先级
		{"(2 + 3) * 4", 20},
		{"7 % 3", 1},
	}
	eng := NewStubEngine()
	ctx, _ := eng.NewContext()
	defer ctx.Close()

	for _, c := range cases {
		v, err := ctx.Eval(c.code, "test.js")
		if err != nil {
			t.Errorf("Eval(%q) error: %v", c.code, err)
			continue
		}
		n, ok := v.Float()
		if !ok {
			t.Errorf("Eval(%q) = %v, want number", c.code, v)
			continue
		}
		if n != c.want {
			t.Errorf("Eval(%q) = %v, want %v", c.code, n, c.want)
		}
	}
}

// TestStubEngineStringConcat 验证字符串拼接。
func TestStubEngineStringConcat(t *testing.T) {
	eng := NewStubEngine()
	ctx, _ := eng.NewContext()
	defer ctx.Close()

	v, err := ctx.Eval(`"Hello, " + "World"`, "test.js")
	if err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	if v.String() != "Hello, World" {
		t.Errorf("got %q, want %q", v.String(), "Hello, World")
	}
}

// TestStubEngineConsoleLog 验证 console.log 调用链路（通过注册 Func 验证）。
func TestStubEngineConsoleLog(t *testing.T) {
	eng := NewStubEngine()
	ctx, _ := eng.NewContext()
	defer ctx.Close()

	var captured []string
	console := NewObject()
	_ = console.Set("log", NewFunction("log", func(args []Value) (Value, error) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = a.String()
		}
		captured = append(captured, strings.Join(parts, " "))
		return Undefined(), nil
	}))
	_ = ctx.Global().Set("console", console)

	_, err := ctx.Eval(`console.log(1 + 1)`, "test.js")
	if err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	if len(captured) != 1 || captured[0] != "2" {
		t.Errorf("captured = %v, want [2]", captured)
	}
}

// TestStubEngineArrayLiteral 验证数组字面量。
func TestStubEngineArrayLiteral(t *testing.T) {
	eng := NewStubEngine()
	ctx, _ := eng.NewContext()
	defer ctx.Close()

	v, err := ctx.Eval(`[1, 2, 3]`, "test.js")
	if err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	arr, ok := v.(*ArrayValue)
	if !ok {
		t.Fatalf("got %T, want *ArrayValue", v)
	}
	if len(arr.elems) != 3 {
		t.Fatalf("len = %d, want 3", len(arr.elems))
	}
}

// TestStubEngineObjectLiteral 验证对象字面量。
func TestStubEngineObjectLiteral(t *testing.T) {
	eng := NewStubEngine()
	ctx, _ := eng.NewContext()
	defer ctx.Close()

	v, err := ctx.Eval(`{ a: 1, b: "hello" }`, "test.js")
	if err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	obj, ok := v.AsObject()
	if !ok {
		t.Fatalf("not object: %v", v)
	}
	a, _ := obj.Get("a")
	if n, _ := a.Float(); n != 1 {
		t.Errorf("a = %v, want 1", a)
	}
	b, _ := obj.Get("b")
	if b.String() != "hello" {
		t.Errorf("b = %v, want hello", b)
	}
}

// TestStubEngineMemberAccess 验证成员访问与索引。
func TestStubEngineMemberAccess(t *testing.T) {
	eng := NewStubEngine()
	ctx, _ := eng.NewContext()
	defer ctx.Close()

	// 注册 obj = { x: 10, arr: [1, 2, 3] }
	obj := NewObject()
	_ = obj.Set("x", IntValue(10))
	_ = obj.Set("arr", NewArray([]Value{IntValue(1), IntValue(2), IntValue(3)}))
	_ = ctx.Global().Set("obj", obj)

	cases := []struct {
		code string
		want float64
	}{
		{"obj.x", 10},
		{"obj.arr[0]", 1},
		{"obj.arr[1]", 2},
		{"obj.arr[2]", 3},
	}
	for _, c := range cases {
		v, err := ctx.Eval(c.code, "test.js")
		if err != nil {
			t.Errorf("Eval(%q) error: %v", c.code, err)
			continue
		}
		n, _ := v.Float()
		if n != c.want {
			t.Errorf("Eval(%q) = %v, want %v", c.code, n, c.want)
		}
	}
}

// TestStubEngineComparisons 验证比较运算。
func TestStubEngineComparisons(t *testing.T) {
	eng := NewStubEngine()
	ctx, _ := eng.NewContext()
	defer ctx.Close()

	cases := []struct {
		code string
		want bool
	}{
		{"1 < 2", true},
		{"2 < 1", false},
		{"1 == 1", true},
		{"1 === 1", true},
		{"1 === '1'", false},
		{"1 == '1'", true}, // 宽松相等
		{"true && false", false},
		{"true || false", true},
		{"!false", true},
	}
	for _, c := range cases {
		v, err := ctx.Eval(c.code, "test.js")
		if err != nil {
			t.Errorf("Eval(%q) error: %v", c.code, err)
			continue
		}
		b, _ := v.Bool()
		if b != c.want {
			t.Errorf("Eval(%q) = %v, want %v", c.code, b, c.want)
		}
	}
}

// TestValueConversions 验证 Value 类型的转换方法。
func TestValueConversions(t *testing.T) {
	if !Undefined().IsUndefined() {
		t.Error("Undefined() failed")
	}
	if !Null().IsNull() {
		t.Error("Null() failed")
	}
	if b, _ := Boolean(true).Bool(); !b {
		t.Error("Boolean(true) failed")
	}
	if n, _ := Number(42).Float(); n != 42 {
		t.Error("Number(42) failed")
	}
	if s := Str("hi").String(); s != "hi" {
		t.Error("Str(hi) failed")
	}
}

// TestObjectOperations 验证 Object 的 Get/Set/Keys。
func TestObjectOperations(t *testing.T) {
	o := NewObject()
	_ = o.Set("a", IntValue(1))
	_ = o.Set("b", Str("two"))
	keys := o.Keys()
	if len(keys) != 2 || keys[0] != "a" || keys[1] != "b" {
		t.Errorf("Keys = %v, want [a b]", keys)
	}
	v, _ := o.Get("a")
	if n, _ := v.Float(); n != 1 {
		t.Errorf("Get(a) = %v, want 1", v)
	}
}

// TestArrayOperations 验证 Array 的索引访问与 length 同步。
func TestArrayOperations(t *testing.T) {
	a := NewArray([]Value{IntValue(10), IntValue(20)})
	lengthVal, _ := a.Get("length")
	if n, _ := lengthVal.Int(); n != 2 {
		t.Errorf("length = %d, want 2", n)
	}
	_ = a.Set("3", IntValue(40))
	lengthVal, _ = a.Get("length")
	if n, _ := lengthVal.Int(); n != 4 {
		t.Errorf("after set length = %d, want 4", n)
	}
	v, _ := a.Get("3")
	if n, _ := v.Float(); n != 40 {
		t.Errorf("arr[3] = %v, want 40", v)
	}
}
