package interpreter

import (
	"hash/fnv"
	"math"
	"math/rand"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// fuzzDeoptRand derives a deterministic PRNG from fuzz bytes (mirrors the jit
// package helper; test helpers are not exported across packages).
func fuzzDeoptRand(data []byte) *rand.Rand {
	h := fnv.New64a()
	_, _ = h.Write(data)
	return rand.New(rand.NewSource(int64(h.Sum64())))
}

func fuzzDeoptValue(rng *rand.Rand) engine.Value {
	switch rng.Intn(6) {
	case 0:
		return engine.Number(rng.NormFloat64())
	case 1:
		return engine.Number(math.NaN())
	case 2:
		return engine.Str("boom" + string(rune('a'+rng.Intn(4))))
	case 3:
		return engine.Undefined()
	case 4:
		return engine.Null()
	default:
		return engine.NewObject()
	}
}

// fuzzDeoptExit derives a random DeoptExit: arbitrary ID (negative included),
// ResumePC anywhere in a 16-bit space, local slots that may repeat or exceed
// the frame, a stack depth that may disagree with the values slice, and a
// pending exception that may be nil, a Number (NaN included), a String or an
// Object. The VM recovery path must turn every combination into a controlled
// error or a valid resume — never a panic, OOB write or bogus PC execution.
func fuzzDeoptExit(rng *rand.Rand) jit.DeoptExit {
	exit := jit.DeoptExit{
		ID:         rng.Intn(8) - 1,
		ResumePC:   int(rng.Uint32() & 0xFFFF),
		StackDepth: rng.Intn(12),
	}
	if rng.Intn(2) == 0 {
		for i := 0; i < rng.Intn(6); i++ {
			exit.LocalSlots = append(exit.LocalSlots, uint16(rng.Uint32()))
		}
	}
	if rng.Intn(2) == 0 {
		for i := 0; i < rng.Intn(10); i++ {
			exit.StackValues = append(exit.StackValues, fuzzDeoptValue(rng))
		}
	}
	switch rng.Intn(5) {
	case 0:
		exit.PendingException = nil
	case 1:
		exit.PendingException = engine.Number(rng.NormFloat64())
	case 2:
		exit.PendingException = engine.Number(math.NaN())
	case 3:
		exit.PendingException = engine.Str("pending")
	default:
		exit.PendingException = engine.NewObject()
	}
	return exit
}

// FuzzResumeTraceExit fuzzes the VM deopt/exception recovery entry point with
// random DeoptExit state. resumeTraceExit must either restore the recorded
// stack and return the ResumePC or return a controlled error (malformed
// stack, or a *jsThrow carrying the original pending exception); it must
// never panic, never write out of bounds and never resume at an unvalidated
// PC. The VM is fresh per input so no case can corrupt another.
func FuzzResumeTraceExit(f *testing.F) {
	f.Add([]byte("exception-exit-seed"))
	f.Add([]byte{0x00, 0xFF})
	f.Add([]byte("stack-values-seed"))
	f.Fuzz(func(t *testing.T, data []byte) {
		rng := fuzzDeoptRand(data)
		vm, err := NewVM()
		if err != nil {
			t.Fatal(err)
		}
		defer vm.Close()
		exit := fuzzDeoptExit(rng)
		key := quickTraceKey{tmpl: nil, backedgePC: int(rng.Uint32() & 0xFFFF)}
		resumePC, ok, err := vm.resumeTraceExit(key, exit)
		if err != nil {
			return // controlled error (malformed stack or *jsThrow)
		}
		if !ok {
			t.Fatalf("resumeTraceExit returned ok=false without an error")
		}
		if exit.PendingException != nil {
			t.Fatalf("exception exit must surface a *jsThrow, got ok resume")
		}
		if resumePC != exit.ResumePC {
			t.Fatalf("resume PC = %d, want recorded %d", resumePC, exit.ResumePC)
		}
	})
}
