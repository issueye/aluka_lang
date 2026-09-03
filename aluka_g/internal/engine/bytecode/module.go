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
	// IsGenerator marks a `function*` — calling it returns a GeneratorValue
	// instead of executing the body immediately. The body may use OpYield.
	IsGenerator bool
	// IsAsync marks an `async function` — calling it returns a Promise.
	// The body may use OpAwait, which suspends execution until the awaited
	// promise settles, then resumes with the resolved value.
	IsAsync bool
	// IsArrow marks an arrow function — it has no own `this` binding.
	// `this` resolves lexically via an upvalue captured from the enclosing
	// function (P0-2), instead of the frame's slot 0.
	IsArrow bool

	// ArgumentsSlot is the local slot holding the `arguments` object for
	// non-arrow functions, or -1 if the function has no arguments binding
	// (arrow functions and the top-level program).
	ArgumentsSlot int

	// NewTargetSlot 是保存 `new.target` 的局部槽位（非箭头函数；
	// 经 new 调用时为构造器函数，普通调用/顶层为 undefined）。
	// 箭头函数不分配，经 upvalue 链词法解析到外层函数的槽位。
	// -1 表示未分配。
	NewTargetSlot int

	// NoArgumentsObject 标记函数体未引用 `arguments`：运行时跳过每帧
	// arguments 对象创建（O-5 调用快速路径；编译器在函数体扫描后置位）。
	NoArgumentsObject bool

	// NFESlot 是具名函数表达式（NFE）自引用槽位：`const f = function
	// factorial() { ... }` 的函数体内 `factorial` 绑定到函数自身（不可变）。
	// 运行时在帧建立时把闭包值写入该槽。-1 表示非 NFE（无槽位）。
	NFESlot int

	// NativeCallback 是 O-6 简单回调描述：箭头函数体为单表达式且参数
	// ≤2、无闭包依赖时，编译器生成该描述，数组高阶方法（map/filter/…）
	// 在 Go 侧直接执行表达式，跳过每元素完整调用链（帧 + 解释）。nil 表示
	// 非简单回调（走正常调用路径）。
	NativeCallback *NativeCallbackDesc

	// Inlinable 标记该函数可在调用点展开（小函数内联，I-1）：纯函数
	// （非 async/generator/rest/默认值/解构）、体为单表达式、无闭包捕获
	// （upvalueIndex 为空，含箭头函数的 this）、不引用 arguments、参数 ≤ 8。
	// 仅作编译期标记；调用点展开逻辑见 compileCall（未展开时走正常调用，
	// 语义不受影响）。
	Inlinable bool

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

	// MaxStack 是该函数执行期操作数栈相对 base+NumLocals 的峰值上界（由
	// ComputeMaxStack 静态分析得出，sound）。VM 在帧入口一次性预留
	// NumLocals+MaxStack 个槽，使帧内 push 永不扩容、可走无分支直写。
	MaxStack int

	// SourceFile / Line table for error reporting (best-effort).
	SourceFile string
	LineStarts []LineEntry // sorted by PC; sparse
}

// cbOpcode 是 O-6 简单回调的微指令（小栈求值表达式）。
type CBOpcode uint8

const (
	CBPushParam0 CBOpcode = iota // 压入第 1 个参数
	CBPushParam1                 // 压入第 2 个参数
	CBPushConst                  // operand = 常量池索引
	CBPushProp0                  // 压入 param0.属性（operand = 属性名常量索引）
	CBPushProp1                  // 压入 param1.属性
	CBNeg                        // 栈顶取负
	CBBinOp                      // operand = bytecode.Opcode（算术/位）
	CBCmp                        // operand = bytecode.Opcode（比较）
)

// cbInstr 是一条简单回调微指令。
type CBInstr struct {
	Op      CBOpcode
	Operand uint32
}

// NativeCallbackDesc 描述简单回调表达式（O-6）：编译器把箭头函数体
// 翻译成微指令序列，数组高阶方法在 Go 侧小栈求值，跳过每元素完整调用链。
// 覆盖：恒等/字面量/一元负/二元算术位/比较/属性读/嵌套组合（如 x=>x.v%3===0）。
type NativeCallbackDesc struct {
	ParamCount uint8 // 1 或 2
	Instrs     []CBInstr
}

// TryEntry describes one try/catch/finally region.
type TryEntry struct {
	StartPC    int // PC of OpTryEnter
	HasCatch   bool
	HasFinally bool
	CatchPC    int // PC to jump to when an exception is thrown
	FinallyPC  int // PC of the finally block (run on both normal and exceptional exit)

	// 区域边界（return/break/continue 穿过 try 时判定目标是否仍在区域内，
	// 决定是否必须先运行 finally）。EndPC/CatchEndPC/FinallyEndPC 分别是
	// try 块、catch 块、finally 块末尾的 OpTryExit/OpTryExitFinally 指令 PC。
	EndPC        int
	CatchEndPC   int
	FinallyEndPC int
}

// UpvalueCapture describes one upvalue slot in a closure.
type UpvalueCapture struct {
	IsLocal bool // true: capture from immediately-enclosing function's local
	Index   int  // local slot (if IsLocal) or outer upvalue index
}

// LineEntry maps a PC offset to a source line number.
type LineEntry struct {
	PC   int
	Line int
}

// Module is the compiled output of a single source file.
type Module struct {
	Functions []*FuncTemplate // [0] is the top-level program
	Classes   []*ClassTemplate
}

// NewModule creates an empty module.
func NewModule() *Module { return &Module{} }

// AddFunction appends a function template and returns its index.
func (m *Module) AddFunction(fn *FuncTemplate) int {
	idx := len(m.Functions)
	m.Functions = append(m.Functions, fn)
	return idx
}

// AddClass appends a class template and returns its index.
func (m *Module) AddClass(c *ClassTemplate) int {
	idx := len(m.Classes)
	m.Classes = append(m.Classes, c)
	return idx
}

// === Class templates (ES2015) ============================================

// MethodKindValue mirrors ast.MethodKind in the compiled form (kept in the
// bytecode package to avoid an ast import cycle).
type MethodKindValue int

const (
	MethodKindNormal MethodKindValue = iota
	MethodKindConstructor
	MethodKindGetter
	MethodKindSetter
)

// ClassMethodTemplate describes one member of a class (method/accessor/ctor).
type ClassMethodTemplate struct {
	Name    string // property name（计算键方法为占位名，实际键运行时求值）
	Kind    MethodKindValue
	Static  bool
	TmplIdx int // function-template index
}

// ClassTemplate is the compiled form of a class. OpMakeClass reads the
// superclass (if HasSuper) from the stack, instantiates the constructor and
// prototype, installs methods/accessors, wires up the prototype chain, and
// pushes the constructor.
type ClassTemplate struct {
	Name     string
	HasSuper bool
	CtorIdx  int // function-template index for the constructor
	Methods  []ClassMethodTemplate
	// ComputedIdx 是 Methods 中带计算键（[expr]() {}）的方法索引。
	// 编译时这些键表达式按方法顺序求值压栈，OpMakeClass 时弹出使用。
	ComputedIdx []int
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
