// Package bytecode defines the instruction set and compiled module structure
// for the stack-based VM (Phase 1B).
//
// Design:
//   - Stack-based evaluation: most operands live on the value stack.
//   - Fixed-width instructions: every instruction is 4 bytes
//     [ opcode:1 ][ operand:3 (big-endian uint24) ]
//     No-operand instructions leave the 3 operand bytes as zero. Fixed width
//     keeps the dispatch loop branch-light and lets us precompute jump
//     targets without re-decoding.
//   - Constants pool holds heavy values (strings, numbers, nested function
//     templates); small ints can be pushed inline via PUSH_INT (range 0..2^24-1).
//   - Locals live in stack slots indexed from the current frame's base.
//   - Closures capture upvalues pointing to outer locals (or chained upvalues).
package bytecode

// Opcode is a single bytecode operation.
type Opcode uint8

// Instruction width in bytes: 1 opcode + 3 operand bytes.
const InstrSize = 4

// Opcodes are grouped by category. The numeric values are stable and must not
// be reordered once the VM is shipped (serialized bytecode may rely on them).
const (
	OpNop Opcode = iota

	// --- Literals & stack ---
	OpPushUndefined
	OpPushNull
	OpPushTrue
	OpPushFalse
	OpPushConst  // A: const pool index
	OpPushInt    // A: inline non-negative int (0..2^24-1)
	OpPushNegInt // A: inline int (push -(A)); for negative small ints
	OpPop
	OpDup
	OpSwap

	// --- Variables ---
	OpLoadLocal   // A: slot index
	OpStoreLocal  // A: slot index
	OpLoadGlobal  // A: name const index
	OpStoreGlobal // A: name const index

	// --- Upvalues (closures) ---
	OpLoadUpvalue  // A: upvalue index
	OpStoreUpvalue // A: upvalue index
	OpMakeClosure  // A: function-template index; emits numUpvalues Capture ops after

	// --- Binary arithmetic & bitwise ---
	OpAdd
	OpSub
	OpMul
	OpDiv
	OpMod
	OpPow
	OpBitAnd
	OpBitOr
	OpBitXor
	OpShl
	OpShr
	OpUShr

	// --- Unary ---
	OpNeg
	OpNot // logical not (ToBoolean)
	OpBitNot
	OpTypeof

	// --- Comparisons ---
	OpEq       // ==
	OpStrictEq // ===
	OpStrictNe // !==
	OpNe       // !=
	OpLt
	OpLe
	OpGt
	OpGe

	// --- Control flow (operand = relative byte offset, signed) ---
	OpJmp            // unconditional; A: signed offset
	OpJmpTruePop     // pop + jump if true; A: signed offset
	OpJmpFalsePop    // pop + jump if false; A: signed offset
	OpJmpTrueKeep    // jump if true WITHOUT popping (for &&)
	OpJmpFalseKeep   // jump if false WITHOUT popping (for ||)
	OpJmpNullishKeep // jump if null/undefined WITHOUT popping (for ??)

	// --- Functions ---
	OpCall        // A: numArgs
	OpCallMethod  // A: numArgs (callee.method(...args), `this` = receiver)
	OpNew         // A: numArgs
	OpReturn      // return top of stack
	OpReturnUndef // return undefined

	// --- Objects & arrays ---
	OpNewObject
	OpNewArray   // A: element count (elements on stack)
	OpGetProp    // A: name-const index; obj on stack → value
	OpSetProp    // A: name-const index; pops value then obj (assignment context)
	OpSetPropObj // A: name-const index; for { key: val } literals: pops value then obj, pushes obj back
	OpSetPropTop // A: name-const index; for obj.prop = expr: pops value then obj (no push)
	OpGetElem    // obj, key on stack → value
	OpSetElem    // obj, key, value on stack (assignment context)
	OpSetElemTop // pops value, key, obj (no push)
	OpDelProp           // A: name-const index; pops obj, deletes own prop, pushes bool
	OpSetPropComputedObj // for { [expr]: val }: pops value then key then obj (peek), pushes obj back

	// --- Spread (ES2015) ---
	OpBuildArray     // push a new empty array onto the stack
	OpArrayPush      // pop value, append to array (peek)
	OpArraySpread    // pop array value, append all its elements to array (peek)
	OpCallArgs       // pop argsArray + callee, call with spread args
	OpCallMethodArgs // A: name-const index; pop argsArray + receiver, call method
	OpNewArgs        // pop argsArray + callee, new with spread args
	OpSpreadObject   // pop value, copy own enumerable props into object (peek)

	// --- Unary extras ---
	OpUnaryPlus    // ToNumber coercion
	OpTypeofGlobal // A: name-const index; typeof on possibly-undefined global

	// --- Try / catch (try-table driven) ---
	OpTryEnter       // A: try-table index; pushes an exception handler frame
	OpTryExit        // A: try-table index; pops the handler frame (normal exit)
	OpTryExitFinally // A: try-table index; finally block finished

	// --- throw / instanceof / in ---
	OpThrow
	OpInstanceof
	OpIn

	// --- Iteration support ---
	OpForInNext // A: jump offset to exit when exhausted

	// --- Class (ES2015) ---
	OpMakeClass        // A: class-template index; pops optional superclass, pushes ctor
	OpGetProto         // pop obj, push its [[Prototype]] (or null)
	OpCallThis         // A: numArgs; pop fn+args, call with this = current frame's slot 0
	OpConstructThis    // A: numArgs; pop ctor+args, call as constructor with this = slot 0
	OpCallThisArgs     // pop argsArray + fn, call with this = slot 0 (spread args)
	OpConstructThisArgs // pop argsArray + ctor, call as constructor with this = slot 0 (spread)

	// --- Iterator protocol (ES2015) ---
	OpGetIterator // pop iterable, push iterator object (with .next() method)
	OpYield       // pop value; yield it from generator. Resume value is pushed on resume.

	// --- Async/await (ES2017) ---
	OpAwait // pop value; suspend async function until value's promise settles. Push resolved value on resume.

	OpEnd // sentinel marking end of code (for safety)
)

var opNames = [...]string{
	OpNop: "NOP",

	OpPushUndefined: "PUSH_UNDEFINED",
	OpPushNull:      "PUSH_NULL",
	OpPushTrue:      "PUSH_TRUE",
	OpPushFalse:     "PUSH_FALSE",
	OpPushConst:     "PUSH_CONST",
	OpPushInt:       "PUSH_INT",
	OpPushNegInt:    "PUSH_NEG_INT",
	OpPop:           "POP",
	OpDup:           "DUP",
	OpSwap:          "SWAP",

	OpLoadLocal:   "LOAD_LOCAL",
	OpStoreLocal:  "STORE_LOCAL",
	OpLoadGlobal:  "LOAD_GLOBAL",
	OpStoreGlobal: "STORE_GLOBAL",

	OpLoadUpvalue:  "LOAD_UPVALUE",
	OpStoreUpvalue: "STORE_UPVALUE",
	OpMakeClosure:  "MAKE_CLOSURE",

	OpAdd:    "ADD",
	OpSub:    "SUB",
	OpMul:    "MUL",
	OpDiv:    "DIV",
	OpMod:    "MOD",
	OpPow:    "POW",
	OpBitAnd: "BIT_AND",
	OpBitOr:  "BIT_OR",
	OpBitXor: "BIT_XOR",
	OpShl:    "SHL",
	OpShr:    "SHR",
	OpUShr:   "USHR",

	OpNeg:    "NEG",
	OpNot:    "NOT",
	OpBitNot: "BIT_NOT",
	OpTypeof: "TYPEOF",

	OpEq:       "EQ",
	OpStrictEq: "STRICT_EQ",
	OpStrictNe: "STRICT_NE",
	OpNe:       "NE",
	OpLt:       "LT",
	OpLe:       "LE",
	OpGt:       "GT",
	OpGe:       "GE",

	OpJmp:            "JMP",
	OpJmpTruePop:     "JMP_TRUE_POP",
	OpJmpFalsePop:    "JMP_FALSE_POP",
	OpJmpTrueKeep:    "JMP_TRUE_KEEP",
	OpJmpFalseKeep:   "JMP_FALSE_KEEP",
	OpJmpNullishKeep: "JMP_NULLISH_KEEP",

	OpCall:        "CALL",
	OpCallMethod:  "CALL_METHOD",
	OpNew:         "NEW",
	OpReturn:      "RETURN",
	OpReturnUndef: "RETURN_UNDEF",

	OpNewObject:  "NEW_OBJECT",
	OpNewArray:   "NEW_ARRAY",
	OpGetProp:    "GET_PROP",
	OpSetProp:    "SET_PROP",
	OpSetPropObj: "SET_PROP_OBJ",
	OpSetPropTop: "SET_PROP_TOP",
	OpGetElem:    "GET_ELEM",
	OpSetElem:    "SET_ELEM",
	OpSetElemTop: "SET_ELEM_TOP",
	OpDelProp:           "DEL_PROP",
	OpSetPropComputedObj: "SET_PROP_COMPUTED_OBJ",

	OpBuildArray:     "BUILD_ARRAY",
	OpArrayPush:      "ARRAY_PUSH",
	OpArraySpread:    "ARRAY_SPREAD",
	OpCallArgs:       "CALL_ARGS",
	OpCallMethodArgs: "CALL_METHOD_ARGS",
	OpNewArgs:        "NEW_ARGS",
	OpSpreadObject:   "SPREAD_OBJECT",

	OpUnaryPlus:    "UNARY_PLUS",
	OpTypeofGlobal: "TYPEOF_GLOBAL",

	OpTryEnter:       "TRY_ENTER",
	OpTryExit:        "TRY_EXIT",
	OpTryExitFinally: "TRY_EXIT_FINALLY",

	OpThrow:      "THROW",
	OpInstanceof: "INSTANCEOF",
	OpIn:         "IN",

	OpForInNext: "FOR_IN_NEXT",

	OpMakeClass:         "MAKE_CLASS",
	OpGetProto:          "GET_PROTO",
	OpCallThis:          "CALL_THIS",
	OpConstructThis:     "CONSTRUCT_THIS",
	OpCallThisArgs:      "CALL_THIS_ARGS",
	OpConstructThisArgs: "CONSTRUCT_THIS_ARGS",

	OpGetIterator: "GET_ITERATOR",
	OpYield:       "YIELD",
	OpAwait:       "AWAIT",

	OpEnd: "END",
}

// String returns the human-readable opcode name (for disassembly/debugging).
func (op Opcode) String() string {
	if int(op) < len(opNames) && opNames[op] != "" {
		return opNames[op]
	}
	return "OP_UNKNOWN"
}

// HasOperand reports whether the instruction reads its 3-byte operand field.
// No-operand instructions ignore the operand bytes.
func (op Opcode) HasOperand() bool {
	switch op {
	case OpPushConst, OpPushInt, OpPushNegInt:
		return true
	case OpLoadLocal, OpStoreLocal, OpLoadGlobal, OpStoreGlobal:
		return true
	case OpLoadUpvalue, OpStoreUpvalue, OpMakeClosure:
		return true
	case OpJmp, OpJmpTruePop, OpJmpFalsePop, OpJmpTrueKeep, OpJmpFalseKeep, OpJmpNullishKeep:
		return true
	case OpCall, OpCallMethod, OpNew:
		return true
	case OpGetProp, OpSetProp, OpSetPropObj, OpSetPropTop:
		return true
	case OpGetElem, OpSetElem, OpSetElemTop, OpDelProp, OpSetPropComputedObj:
		return true
	case OpCallMethodArgs:
		return true
	case OpUnaryPlus, OpTypeofGlobal:
		return true
	case OpTryEnter, OpTryExit, OpTryExitFinally:
		return true
	case OpNewArray, OpForInNext:
		return true
	case OpMakeClass, OpCallThis, OpConstructThis:
		return true
	}
	return false
}
