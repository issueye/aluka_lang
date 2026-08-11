package jit

import (
	"hash/fnv"
	"math"
	"math/rand"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	jitnative "github.com/aluka-lang/aluka/internal/engine/jit/native"
)

// fuzzRand derives a deterministic PRNG from the fuzz input bytes. The same
// bytes always produce the same program, so every fuzz failure is
// reproducible from the saved input alone (no wall clock, global random state
// or unstable concurrency is involved).
func fuzzRand(data []byte) *rand.Rand {
	h := fnv.New64a()
	_, _ = h.Write(data)
	return rand.New(rand.NewSource(int64(h.Sum64())))
}

// fuzzValue returns a random engine.Value from the supported domain
// (Number incl. NaN/-0/Infinity, Boolean, null, undefined, String, Object).
func fuzzValue(rng *rand.Rand) engine.Value {
	switch rng.Intn(7) {
	case 0:
		switch rng.Intn(5) {
		case 0:
			return engine.Number(math.NaN())
		case 1:
			return engine.Number(math.Inf(1))
		case 2:
			return engine.Number(math.Copysign(0, -1))
		default:
			return engine.Number(rng.NormFloat64())
		}
	case 1:
		return engine.Boolean(rng.Intn(2) == 0)
	case 2:
		return engine.Null()
	case 3:
		return engine.Undefined()
	case 4:
		return engine.Str(randString(rng, 1+rng.Intn(8)))
	default:
		return engine.NewObject()
	}
}

func randString(rng *rand.Rand, n int) string {
	const alphabet = "abcXYZ012_-"
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return string(b)
}

// fuzzInstr derives one IR instruction, including invalid opcodes (op values
// beyond the table) so the verifier's unknown-opcode rejection is exercised.
func fuzzInstr(rng *rand.Rand) Instr {
	return Instr{
		Op:      Op(rng.Intn(300)),
		Operand: rng.Uint32() & 0xFFFF,
		Value:   rng.NormFloat64(),
		Name:    randString(rng, 1+rng.Intn(6)),
	}
}

// fuzzProgram derives a random Program: random opcodes/operands/jump targets,
// optional deopt map (with random exit counts and depths), optional exception
// map (truncated/extended), optional call/method guard tables and random
// locals count. All sizes are capped so no input can exhaust memory.
func fuzzProgram(rng *rand.Rand) *Program {
	p := &Program{
		NumParams: rng.Intn(9),
		NumLocals: 1 + rng.Intn(33),
		Code:      make([]Instr, 1+rng.Intn(32)),
	}
	for i := range p.Code {
		p.Code[i] = fuzzInstr(rng)
	}
	if rng.Intn(2) == 0 {
		nExits := 1 + rng.Intn(4)
		p.traceExitDepths = make([]uint8, nExits)
		for i := range p.traceExitDepths {
			switch rng.Intn(3) {
			case 0:
				p.traceExitDepths[i] = ^uint8(0) // unset
			case 1:
				p.traceExitDepths[i] = uint8(rng.Intn(12)) // shallow or too deep
			default:
				p.traceExitDepths[i] = uint8(rng.Intn(9))
			}
		}
		switch rng.Intn(3) {
		case 0: // nil exception map (no exception exits)
		case 1: // aligned map
			p.traceExceptionExits = make([]bool, nExits)
			for i := range p.traceExceptionExits {
				p.traceExceptionExits[i] = rng.Intn(2) == 0
			}
		default: // truncated or extended map
			p.traceExceptionExits = make([]bool, 1+rng.Intn(nExits+2))
			for i := range p.traceExceptionExits {
				p.traceExceptionExits[i] = rng.Intn(2) == 0
			}
		}
		if rng.Intn(2) == 0 {
			p.traceCallGuards = make([]traceCallGuard, rng.Intn(3))
		}
		if rng.Intn(2) == 0 {
			p.traceMethodGuards = make([]traceMethodGuard, rng.Intn(3))
		}
	}
	return p
}

// fuzzTemplate derives a random bytecode function template: possibly
// misaligned or truncated code, random constants, and capped local/param
// counts. It is the fuzz input for the trace compiler.
func fuzzTemplate(rng *rand.Rand) *bytecode.FuncTemplate {
	numLocals := 1 + rng.Intn(16)
	numParams := rng.Intn(numLocals)
	constants := make([]engine.Value, rng.Intn(8))
	for i := range constants {
		switch rng.Intn(3) {
		case 0:
			constants[i] = engine.Number(rng.NormFloat64())
		case 1:
			constants[i] = engine.Str(randString(rng, 1+rng.Intn(6)))
		default:
			constants[i] = fuzzValue(rng)
		}
	}
	code := make([]byte, rng.Intn(96))
	_, _ = rng.Read(code)
	return &bytecode.FuncTemplate{
		NumParams: numParams, NumLocals: numLocals,
		ArgumentsSlot: numLocals, NoArgumentsObject: true,
		Constants: constants, Code: code,
	}
}

// fuzzTraceRange derives a start/backedge pair that may be unaligned, out of
// order or out of range.
func fuzzTraceRange(rng *rand.Rand, codeLen int) (int, int) {
	start := rng.Intn(codeLen + 4)
	backedge := start + rng.Intn(48) - 8
	if backedge < 0 {
		backedge = 0
	}
	return start, backedge
}

// FuzzVerifyProgram fuzzes Program.Verify against random opcodes, operands,
// jump targets, deopt maps, exception maps and side-effect protocol state.
// Verify must reject malformed IR with an error or accept well-formed IR;
// it must never panic, never loop (the CFG walk visits each instruction at
// most once) and never accept programs whose execution could escape bounds.
// Random programs are NOT executed here: a random jump graph could loop
// without hitting a budgeted backedge, so execution coverage is limited to
// compiler products (see FuzzCompileTrace), which always terminate.
func FuzzVerifyProgram(f *testing.F) {
	f.Add([]byte("valid-trace-seed"))
	f.Add([]byte{0x00, 0x01, 0x02, 0xFF})
	f.Add([]byte("side-effect protocol"))
	f.Fuzz(func(t *testing.T, data []byte) {
		rng := fuzzRand(data)
		p := fuzzProgram(rng)
		err := p.Verify()
		_ = err // stable error or acceptance; no panic, no hang
	})
}

// FuzzCompileTrace fuzzes CompileTraceWithGuards with random bytecode,
// possibly misaligned/out-of-range trace ranges, malformed call/method guards
// and unsupported opcodes. Successful compilations are executed under a small
// budget (bounded backedges), so malformed deopt/exception state must surface
// as errors or clean exits — never a panic, hang or OOB access.
func FuzzCompileTrace(f *testing.F) {
	tmpl := throwTraceTemplate()
	f.Add(tmpl.Code, tmpl.NumLocals, 0, 32) // legal exception-exit trace
	side := sideEffectTraceTemplate()
	f.Add(side.Code, side.NumLocals, 0, 64) // legal side-effect trace
	f.Add([]byte{0x11, 0x22}, 3, 4, 8)      // malformed / misaligned
	f.Add([]byte{}, 1, 0, 0)                // empty code
	f.Fuzz(func(t *testing.T, code []byte, numLocals, startPC, backedgePC int) {
		rng := fuzzRand([]byte(code))
		if numLocals < 1 || numLocals > 16 {
			numLocals = 1 + (numLocals % 16)
		}
		if numLocals < 1 {
			numLocals = 1
		}
		tmpl := &bytecode.FuncTemplate{
			NumParams: 0, NumLocals: numLocals,
			ArgumentsSlot: numLocals, NoArgumentsObject: true,
			Constants: []engine.Value{engine.Number(1), engine.Str("x")},
			Code:      code,
		}
		var callGuards []TraceCallGuard
		if rng.Intn(2) == 0 {
			callGuards = append(callGuards, TraceCallGuard{
				PC: startPC, SourceLocal: rng.Intn(numLocals + 2), Target: fuzzValue(rng),
			})
		}
		var methodGuards []TraceMethodGuard
		if rng.Intn(2) == 0 {
			methodGuards = append(methodGuards, TraceMethodGuard{
				PC: backedgePC, SourceLocal: rng.Intn(numLocals + 2), Target: fuzzValue(rng),
				Method: "m", Property: "p",
			})
		}
		trace, err := CompileTraceWithGuards(tmpl, startPC, backedgePC, callGuards, methodGuards)
		if err != nil {
			return // stable rejection is the expected outcome for fuzz input
		}
		// The compiler product always terminates: backedges target
		// instruction 0 and are budget-limited, other jumps stay inside the
		// lowered CFG which ends in exits.
		locals := make([]engine.Value, trace.program.NumLocals)
		for i := range locals {
			locals[i] = fuzzValue(rng)
		}
		_, _, _ = trace.ExecuteBudgetDetailedWithSafepoint(locals, 4, nil)
	})
}

// FuzzNativeLowering fuzzes the Native input planner and RX lifecycle:
// random verified programs must be rejected or planned without publishing
// machine code; real compiler products may be compiled and must release
// their RX memory on Close, with LiveExecutableMemory returning to the
// per-case baseline. Invalid machine input must never be published.
func FuzzNativeLowering(f *testing.F) {
	f.Add([]byte("native-plan-seed"))
	f.Add(encodeSideEffectSeed())
	f.Fuzz(func(t *testing.T, data []byte) {
		rng := fuzzRand(data)
		// (a) random verified programs through both planners; a failure must
		// not publish RX (compileNative only installs code after lowering
		// succeeds, and we never call CompileNative on random IR here).
		p := fuzzProgram(rng)
		if err := p.Verify(); err == nil {
			if p.traceExitDepths != nil {
				_, _, _ = lowerNativeTraceInputs(p)
			} else {
				_, _, _ = lowerNativeInputs(p)
			}
		}
		// (b) real compiler products: compile, close, and assert the global
		// executable-memory accounting returns to the case baseline.
		regions0, bytes0 := jitnative.LiveExecutableMemory()
		tmpl := fuzzTemplate(rng)
		start, backedge := fuzzTraceRange(rng, len(tmpl.Code))
		tr, err := CompileTrace(tmpl, start, backedge)
		if err == nil {
			if cerr := tr.CompileNative(); cerr == nil {
				if !tr.HasNative() {
					t.Fatalf("CompileNative succeeded but no native code installed")
				}
			}
			_ = tr.Close()
		}
		regions1, bytes1 := jitnative.LiveExecutableMemory()
		if regions1 != regions0 || bytes1 != bytes0 {
			t.Fatalf("RX leak in fuzz case: regions %d->%d bytes %d->%d", regions0, regions1, bytes0, bytes1)
		}
	})
}

// encodeSideEffectSeed returns the bytecode of the R1-5 side-effect trace as
// a stable seed byte string for the native-lowering fuzzer.
func encodeSideEffectSeed() []byte {
	code := sideEffectTraceTemplate().Code
	out := make([]byte, 0, len(code)+4)
	out = append(out, code...)
	out = append(out, byte(sideEffectTraceTemplate().NumLocals))
	return out
}
