package bytecode

import (
	"fmt"

	"github.com/aluka-lang/aluka/internal/engine"
)

// FuncTemplate is the compiled form of a function (or the top-level program).
// The VM instantiates a call frame per activation using this template.
type FuncTemplate struct {
	Name      string
	NumParams int
	NumLocals int // total slots = NumParams + declared locals (incl. temporaries)
	IsVarArgs bool

	// Code is the flat instruction stream. Fixed-width (InstrSize bytes each).
	Code []byte

	// Constants referenced by PUSH_CONST / GET_PROP name indices / etc.
	Constants []engine.Value

	// Names maps name-const indices → string identifiers (for globals/props).
	// Shared with Constants when a constant is itself a string; kept separate
	// only for clarity. Indices below refer into Constants for strings too.
	// (We index strings via Constants; Names is reserved for future use.)

	// Upvalue captures describing how this function closes over outer locals.
	Upvalues []UpvalueCapture

	// TryTable holds try/catch/finally regions for exception handling.
	// Referenced by OpTryEnter/OpTryExit/OpTryExitFinally operands.
	TryTable []TryEntry

	// SourceFile / Line table for error reporting (best-effort).
	SourceFile string
	LineStarts []LineEntry // sorted by PC; sparse
}

// TryEntry describes one try/catch/finally region.
type TryEntry struct {
	StartPC    int  // PC of OpTryEnter
	HasCatch   bool
	HasFinally bool
	CatchPC    int  // PC to jump to when an exception is thrown
	FinallyPC  int  // PC of the finally block (run on both normal and exceptional exit)
}

// UpvalueCapture describes one upvalue slot in a closure.
type UpvalueCapture struct {
	IsLocal   bool // true: capture from immediately-enclosing function's local
	Index     int  // local slot (if IsLocal) or outer upvalue index
}

// LineEntry maps a PC offset to a source line number.
type LineEntry struct {
	PC   int
	Line int
}

// Module is the compiled output of a single source file.
type Module struct {
	Functions []*FuncTemplate // [0] is the top-level program
}

// NewModule creates an empty module.
func NewModule() *Module { return &Module{} }

// AddFunction appends a function template and returns its index.
func (m *Module) AddFunction(fn *FuncTemplate) int {
	idx := len(m.Functions)
	m.Functions = append(m.Functions, fn)
	return idx
}

// === Instruction encoding =================================================

// Encode writes an instruction (opcode + 3-byte big-endian operand) and
// returns the starting pc. The operand is treated as an unsigned 24-bit value.
func Encode(code *[]byte, op Opcode, operand uint32) int {
	pc := len(*code)
	*code = append(*code, byte(op))
	*code = append(*code, byte(operand>>16), byte(operand>>8), byte(operand))
	return pc
}

// PatchOperand overwrites the 3-byte operand at the given pc, preserving the
// opcode. Used for back-patching jump targets once they are known.
func PatchOperand(code []byte, pc int, operand uint32) {
	if pc+3 >= len(code) {
		panic(fmt.Sprintf("bytecode: PatchOperand out of range pc=%d len=%d", pc, len(code)))
	}
	code[pc+1] = byte(operand >> 16)
	code[pc+2] = byte(operand >> 8)
	code[pc+3] = byte(operand)
}

// Decode reads the opcode and operand at pc. Returns (op, operand, nextPC).
func Decode(code []byte, pc int) (Opcode, uint32, int) {
	op := Opcode(code[pc])
	var operand uint32
	if pc+3 < len(code) {
		operand = uint32(code[pc+1])<<16 | uint32(code[pc+2])<<8 | uint32(code[pc+3])
	}
	return op, operand, pc + InstrSize
}

// EncodeSigned encodes a signed offset operand as its bits in uint32.
func EncodeSigned(code *[]byte, op Opcode, offset int) int {
	return Encode(code, op, uint32(offset))
}

// SignedOffset interprets a jump operand as a signed 24-bit value.
func SignedOperand(operand uint32) int {
	v := int(operand)
	// Sign-extend 24-bit → int
	if v&0x800000 != 0 {
		v |= ^0xFFFFFF
	}
	return v
}

// === Constants ============================================================

// AddConst appends a value to the function's constant pool, returning its
// index. Deduplication is NOT performed (callers may intern strings if needed).
func (f *FuncTemplate) AddConst(v engine.Value) int {
	idx := len(f.Constants)
	f.Constants = append(f.Constants, v)
	return idx
}

// AddStringConst interns a string into the constant pool, returning its index.
// Reuses an existing identical string constant when present (common case for
// property names), which keeps the pool small and aids any future IC work.
func (f *FuncTemplate) AddStringConst(s string) int {
	for i, c := range f.Constants {
		if c.Type() == engine.TypeString && c.String() == s {
			return i
		}
	}
	return f.AddConst(engine.Str(s))
}

// LineFor returns the best-known source line for a given pc, or 0 if unknown.
func (f *FuncTemplate) LineFor(pc int) int {
	line := 0
	for _, e := range f.LineStarts {
		if e.PC > pc {
			break
		}
		line = e.Line
	}
	return line
}
