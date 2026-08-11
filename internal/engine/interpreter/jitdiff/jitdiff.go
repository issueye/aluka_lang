// Package jitdiff implements the R1-1 / R1-2 / R1-8 generative differential
// framework for the aluka JIT.
//
// It generates small, fixed-seed JavaScript programs over a bounded value
// domain, runs each one under --jit=off / --jit=quick / --jit=auto in fresh
// VMs, and treats Tier 0 (off) as the only semantic oracle: the return value,
// exception type and message, catch behavior and the side-effect event log
// must be identical across tiers.
//
// Any mismatch is saved as a self-contained artifact (seed, generation
// parameters, generator version, source, per-tier IR dumps and JIT stats,
// plus a single-command replay) under bench/results/jitdiff/. The framework
// lives in its own package so random generation logic never leaks into the
// interpreter core, and every failure is reproducible by rerunning the
// generator with the recorded seed.
//
// The generator only emits semantics the engine implements. Unsupported JIT
// paths (String/BigInt arithmetic, loose equality on non-numbers, Proxy,
// getters/setters, throwing callbacks, Symbol values) are deliberately
// generated so the JIT must guard back to Tier 0; the differential
// comparison then proves that fallback produces identical observable
// behavior instead of misreporting such cases as JIT mismatches.
package jitdiff

import (
	"fmt"
	"strings"
)

// Version identifies the generator + harness behavior. Bump it whenever the
// generated source shapes, the value domain, the event log format or the
// comparison change; the value is recorded in every failure artifact so a
// reproduction stays tied to the exact generator that produced the mismatch.
const Version = "1"

// Params controls generation and execution. It is part of the failure
// artifact; changing it changes reproducibility, and changes that alter
// generated behavior must be paired with a Version bump.
type Params struct {
	Seed         int64  // suite seed; every case derives a per-case seed from it
	MaxExprDepth int    // expression nesting bound for expression cases
	MaxLoopBound int    // generated loop iteration counts (small values keep cases fast)
	TraceBudget  uint32 // VM trace budget used for Quick/Auto
	Verify       bool   // enable Native/Quick dual execution inside Auto
}

// Normalized returns a copy of p with defaults applied. It is deterministic
// so identical Params always produce identical behavior.
func (p Params) Normalized() Params {
	if p.MaxExprDepth <= 0 {
		p.MaxExprDepth = 3
	}
	if p.MaxLoopBound <= 0 {
		p.MaxLoopBound = 24
	}
	if p.TraceBudget == 0 {
		p.TraceBudget = 3
	}
	return p
}

// Kind classifies a generated case. It drives expected-tier bookkeeping and
// the per-kind breakdown in the suite summary.
type Kind uint8

const (
	KindExpr Kind = iota
	KindBranch
	KindLoop
	KindStrictEq
	KindLooseEq
	KindPropRead
	KindPropWrite
	KindPush
	KindClosure
	KindCall
	KindGetter
	KindCallbackThrow
	KindProxy
	KindDeoptPrefix
)

func (k Kind) String() string {
	switch k {
	case KindExpr:
		return "expr"
	case KindBranch:
		return "branch"
	case KindLoop:
		return "loop"
	case KindStrictEq:
		return "strictEq"
	case KindLooseEq:
		return "looseEq"
	case KindPropRead:
		return "propRead"
	case KindPropWrite:
		return "propWrite"
	case KindPush:
		return "push"
	case KindClosure:
		return "closure"
	case KindCall:
		return "call"
	case KindGetter:
		return "getter"
	case KindCallbackThrow:
		return "callbackThrow"
	case KindProxy:
		return "proxy"
	case KindDeoptPrefix:
		return "deoptPrefix"
	default:
		return fmt.Sprintf("kind(%d)", int(k))
	}
}

// ExpectsQuickHit reports whether this generated shape must execute Quick in
// the fixed PR corpus. Random expression cases deliberately mix unsupported
// values, while deoptPrefix currently promotes directly to Native in Auto.
func (k Kind) ExpectsQuickHit() bool {
	switch k {
	case KindBranch, KindLoop, KindStrictEq, KindPropRead, KindPropWrite,
		KindPush, KindClosure, KindCall:
		return true
	}
	return false
}

// ExpectsNativeHit reports the shapes that the current amd64 Native tier must
// execute in the fixed PR corpus. Other kinds remain valid differential and
// fallback coverage but do not claim a Native hit.
func (k Kind) ExpectsNativeHit() bool {
	switch k {
	case KindLoop, KindPropRead, KindPropWrite, KindCall, KindDeoptPrefix:
		return true
	}
	return false
}

// Case is one generated program plus the metadata needed to reproduce it.
type Case struct {
	ID          int      `json:"id"`
	Kind        Kind     `json:"kind"`
	Seed        int64    `json:"seed"`
	Params      Params   `json:"params"`
	ValueDomain string   `json:"valueDomain,omitempty"`
	Coverage    []string `json:"coverage,omitempty"`
	Body        string   `json:"body"`   // generated computation between the harness head and tail
	Source      string   `json:"source"` // full runnable program (harness + body)
	// Expected is the known off-mode event log for deterministic fixed cases
	// (negative IDs); empty for generated cases.
	Expected string `json:"expected,omitempty"`
}

func (c *Case) Name() string {
	return fmt.Sprintf("jitdiff-%d-%s", c.Seed, c.Kind)
}

// KindCount is the number of case kinds the generator can produce.
const KindCount = int(KindDeoptPrefix) + 1

// AllKinds lists every case kind in canonical order.
var AllKinds = func() []Kind {
	kinds := make([]Kind, 0, KindCount)
	for i := 0; i < KindCount; i++ {
		kinds = append(kinds, Kind(i))
	}
	return kinds
}()

func (k Kind) isOneOf(all []Kind) bool {
	for _, candidate := range all {
		if candidate == k {
			return true
		}
	}
	return false
}

// Describe returns a compact one-line description of the case for reports.
func (c *Case) Describe() string {
	return strings.ReplaceAll(c.Body, "\n", " ")
}
