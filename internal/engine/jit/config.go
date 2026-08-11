package jit

import (
	"fmt"
	"io"
)

type Mode uint8

const (
	Off Mode = iota
	Quick
	Auto
)

func ParseMode(s string) (Mode, error) {
	switch s {
	case "", "off":
		return Off, nil
	case "quick":
		return Quick, nil
	case "auto":
		return Auto, nil
	default:
		return Off, fmt.Errorf("invalid JIT mode %q (want off, quick, or auto)", s)
	}
}

type DumpMode uint8

const (
	DumpOff DumpMode = iota
	DumpIR
	DumpASM
)

func ParseDumpMode(s string) (DumpMode, error) {
	switch s {
	case "", "off":
		return DumpOff, nil
	case "ir":
		return DumpIR, nil
	case "asm":
		return DumpASM, nil
	default:
		return DumpOff, fmt.Errorf("invalid JIT dump mode %q (want off, ir, or asm)", s)
	}
}

func (m DumpMode) String() string {
	switch m {
	case DumpIR:
		return "ir"
	case DumpASM:
		return "asm"
	default:
		return "off"
	}
}

func (m Mode) String() string {
	switch m {
	case Quick:
		return "quick"
	case Auto:
		return "auto"
	default:
		return "off"
	}
}

// Safepoint runs on the Go side whenever a budgeted JIT executor yields. When
// InterpreterSafepoints is enabled it also runs at interpreted loop
// backedges. Returning an error interrupts execution without treating it as a
// compilation or machine-code failure.
type Safepoint func() error

type Config struct {
	Mode                  Mode
	Threshold             uint32
	BackedgeThreshold     uint32
	TraceBudget           uint32
	CodeCacheBytes        uint64
	Verify                bool
	Stats                 bool
	Dump                  DumpMode
	DumpWriter            io.Writer
	Safepoint             Safepoint
	InterpreterSafepoints bool
}

type RejectionReason struct {
	Tier   string
	Reason string
	Count  uint64
}

type DeoptStat struct {
	Function   string
	BackedgePC int
	ExitID     int
	ResumePC   int
	Count      uint64
}

func (c Config) Normalized() Config {
	if c.Threshold == 0 {
		c.Threshold = 1000
	}
	if c.BackedgeThreshold == 0 {
		c.BackedgeThreshold = 10000
	}
	if c.TraceBudget == 0 {
		c.TraceBudget = 65536
	}
	if c.CodeCacheBytes == 0 {
		c.CodeCacheBytes = 4 << 20
	}
	return c
}

type Stats struct {
	Mode                     Mode
	Threshold                uint32
	BackedgeThreshold        uint32
	TraceBudget              uint32
	CodeCacheLimit           uint64
	Calls                    uint64
	Backedges                uint64
	Candidates               uint64
	CompileNanos             uint64
	Compiled                 uint64
	Rejected                 uint64
	// RejectionCacheHits counts hot-path compile attempts that were skipped
	// because the structured rejection cache (per-template leaf state or per
	// (template, backedge) trace state) already recorded a stable rejection.
	// It is the observable proof that unsupported shapes are not re-compiled
	// on every backedge (R3-7).
	RejectionCacheHits       uint64
	Executed                 uint64
	GuardFailures            uint64
	QuickGuardDisabled       uint64
	TraceGuardDisabled       uint64
	NativeGuardDisabled      uint64
	NativeTraceGuardDisabled uint64
	CalleeGuardDisabled      uint64
	Errors                   uint64
	NativeCompiled           uint64
	NativeCompileNanos       uint64
	NativeRejected           uint64
	NativeExecuted           uint64
	NativeYields             uint64
	NativeCodeBytes          uint64
	NativeEvictions          uint64
	BackgroundQueued         uint64
	BackgroundCompleted      uint64
	BackgroundDiscarded      uint64
	CalleeSpecialized        uint64
	CalleeInlined            uint64
	CalleeExecuted           uint64
	CalleeGuardFailures      uint64
	CalleePICAdds            uint64
	CalleePICHits            uint64
	TracesCompiled           uint64
	TraceCompileNanos        uint64
	TracesRejected           uint64
	TracesExecuted           uint64
	TraceYields              uint64
	NativeTracesCompiled     uint64
	NativeTraceCompileNanos  uint64
	NativeTracesRejected     uint64
	NativeTracesExecuted     uint64
	NativeTraceYields        uint64
	SafepointPolls           uint64
	Interruptions            uint64
	NoopCallSites            uint64
	MethodCallSites          uint64
	ArrayPushSites           uint64
	ArrayPushYields          uint64
	ClosureUpvalueSites      uint64
	ClosureUpvalueYields     uint64
	VerifyChecks             uint64
	VerifyFailures           uint64
	// R4-3/R4-4 property PIC diagnostics. PropertyPICAdds counts admissions
	// beyond the two-way baseline (third/fourth shape); PropertyPICHits counts
	// successful guarded loads through the entries; PropertyPICRejections
	// counts fast rejections (not an own data Number property: accessor,
	// Proxy, prototype, deleted or non-Number); PropertyPICOverflows counts
	// misses at/over the adaptive limit (stable fallback); PropertyPICCoolDowns
	// counts cool-down resets after repeated over-cap misses.
	PropertyPICAdds       uint64
	PropertyPICHits       uint64
	PropertyPICRejections uint64
	PropertyPICOverflows  uint64
	PropertyPICCoolDowns  uint64
	RejectionReasons      []RejectionReason
	DeoptExits            []DeoptStat
	LastError             string
	LastNativeError       string
}
