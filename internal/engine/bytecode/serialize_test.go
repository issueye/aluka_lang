package bytecode

import (
	"bytes"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
)

// 本文件验证 bytecode.Module 序列化/反序列化的往返正确性（round-trip）。
// 覆盖：空 Module、带常量池（number/string/bigint）的 FuncTemplate、
// TryTable、Upvalues、LineStarts、ClassTemplate。

func TestSerializeEmptyModule(t *testing.T) {
	mod := &Module{
		Functions: []*FuncTemplate{
			{Name: "<main>", NumLocals: 1, SourceFile: "test.js"},
		},
	}
	var buf bytes.Buffer
	if err := Serialize(&buf, mod); err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	got, err := Deserialize(&buf)
	if err != nil {
		t.Fatalf("Deserialize: %v", err)
	}
	if len(got.Functions) != 1 {
		t.Fatalf("func count = %d, want 1", len(got.Functions))
	}
	if got.Functions[0].Name != "<main>" {
		t.Errorf("name = %q, want <main>", got.Functions[0].Name)
	}
	if got.Functions[0].NumLocals != 1 {
		t.Errorf("NumLocals = %d, want 1", got.Functions[0].NumLocals)
	}
}

func TestSerializeConstants(t *testing.T) {
	// 构造含三种常量类型的 FuncTemplate。
	mod := &Module{
		Functions: []*FuncTemplate{
			{
				Name: "f",
				Constants: []engine.Value{
					engine.Number(3.14),
					engine.Str("hello"),
					engine.BigIntFromInt(9999999999),
					engine.Number(0),
					engine.Str(""), // 空字符串
				},
			},
		},
	}
	var buf bytes.Buffer
	if err := Serialize(&buf, mod); err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	got, err := Deserialize(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Deserialize: %v", err)
	}
	consts := got.Functions[0].Constants
	if len(consts) != 5 {
		t.Fatalf("const count = %d, want 5", len(consts))
	}
	// number
	if consts[0].Type() != engine.TypeNumber {
		t.Errorf("const[0] type = %s, want number", consts[0].Type())
	}
	f, _ := consts[0].Float()
	if f != 3.14 {
		t.Errorf("const[0] = %v, want 3.14", f)
	}
	// string
	if consts[1].String() != "hello" {
		t.Errorf("const[1] = %q, want hello", consts[1].String())
	}
	// bigint
	if consts[2].Type() != engine.TypeBigInt {
		t.Errorf("const[2] type = %s, want bigint", consts[2].Type())
	}
	if consts[2].String() != "9999999999" {
		t.Errorf("const[2] = %q, want 9999999999", consts[2].String())
	}
	// 零值
	if f, _ := consts[3].Float(); f != 0 {
		t.Errorf("const[3] = %v, want 0", f)
	}
	if consts[4].String() != "" {
		t.Errorf("const[4] = %q, want empty", consts[4].String())
	}
}

func TestSerializeCodeAndTryTable(t *testing.T) {
	// 构造含 Code 字节流、TryTable、Upvalues、LineStarts 的 FuncTemplate。
	mod := &Module{
		Functions: []*FuncTemplate{
			{
				Name:       "g",
				NumParams:  2,
				NumLocals:  5,
				IsVarArgs:  true,
				IsGenerator: true,
				IsAsync:    false,
				Code:       []byte{1, 0, 0, 0, 2, 0, 0, 0, 3, 0, 0, 0},
				Upvalues: []UpvalueCapture{
					{IsLocal: true, Index: 0},
					{IsLocal: false, Index: 1},
				},
				TryTable: []TryEntry{
					{StartPC: 0, CatchPC: 8, FinallyPC: 16, HasCatch: true, HasFinally: false},
				},
				LineStarts: []LineEntry{
					{PC: 0, Line: 1},
					{PC: 4, Line: 5},
				},
			},
		},
	}
	var buf bytes.Buffer
	if err := Serialize(&buf, mod); err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	got, err := Deserialize(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Deserialize: %v", err)
	}
	fn := got.Functions[0]
	if fn.NumParams != 2 || fn.NumLocals != 5 {
		t.Errorf("params/locals = %d/%d, want 2/5", fn.NumParams, fn.NumLocals)
	}
	if !fn.IsVarArgs || !fn.IsGenerator || fn.IsAsync {
		t.Errorf("flags: varargs=%v gen=%v async=%v", fn.IsVarArgs, fn.IsGenerator, fn.IsAsync)
	}
	if len(fn.Code) != 12 || fn.Code[0] != 1 || fn.Code[8] != 3 {
		t.Errorf("Code mismatch: %v", fn.Code)
	}
	if len(fn.Upvalues) != 2 || !fn.Upvalues[0].IsLocal || fn.Upvalues[1].IsLocal {
		t.Errorf("Upvalues mismatch: %v", fn.Upvalues)
	}
	if len(fn.TryTable) != 1 || fn.TryTable[0].CatchPC != 8 || !fn.TryTable[0].HasCatch {
		t.Errorf("TryTable mismatch: %v", fn.TryTable)
	}
	if len(fn.LineStarts) != 2 || fn.LineStarts[1].Line != 5 {
		t.Errorf("LineStarts mismatch: %v", fn.LineStarts)
	}
}

func TestSerializeClassTemplate(t *testing.T) {
	mod := &Module{
		Classes: []*ClassTemplate{
			{
				Name:     "Foo",
				HasSuper: true,
				CtorIdx:  1,
				Methods: []ClassMethodTemplate{
					{Name: "bar", Static: true, Kind: MethodKindNormal, TmplIdx: 2},
					{Name: "baz", Static: false, Kind: MethodKindGetter, TmplIdx: 3},
				},
			},
		},
	}
	var buf bytes.Buffer
	if err := Serialize(&buf, mod); err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	got, err := Deserialize(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Deserialize: %v", err)
	}
	if len(got.Classes) != 1 {
		t.Fatalf("class count = %d, want 1", len(got.Classes))
	}
	cls := got.Classes[0]
	if cls.Name != "Foo" || !cls.HasSuper || cls.CtorIdx != 1 {
		t.Errorf("class header mismatch: %+v", cls)
	}
	if len(cls.Methods) != 2 {
		t.Fatalf("method count = %d, want 2", len(cls.Methods))
	}
	if cls.Methods[0].Name != "bar" || !cls.Methods[0].Static {
		t.Errorf("method[0] mismatch: %+v", cls.Methods[0])
	}
	if cls.Methods[1].Kind != MethodKindGetter {
		t.Errorf("method[1] kind = %v, want getter", cls.Methods[1].Kind)
	}
}

func TestSerializeBadMagic(t *testing.T) {
	// 错误的 magic 应返回错误。
	_, err := Deserialize(bytes.NewReader([]byte("XXXXXXXXXXXX")))
	if err == nil {
		t.Error("expected error for bad magic")
	}
}

func TestSerializeVersionMismatch(t *testing.T) {
	// 正确 magic 但版本号不匹配应返回错误。
	data := append([]byte("ALUKABC1"), []byte{99, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}...)
	_, err := Deserialize(bytes.NewReader(data))
	if err == nil {
		t.Error("expected error for version mismatch")
	}
}
