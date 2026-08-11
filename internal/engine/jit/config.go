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

	// R5-3: adaptive threshold model. When Adaptive is set, the static
	// Threshold/BackedgeThreshold become the base of a feedback loop driven by
	// counter-based runtime signals (no wall clock): every execution of a
	// compiled function or trace is a benefit event, every guard failure /
	// deopt / rejected compile is a failure event. After AdaptiveBoostEvery
	// consecutive benefit events the effective threshold drops by one half
	// (promote more eagerly); after AdaptiveCoolEvery failure events it
	// doubles (cool down). The effective threshold is
	// static << cool / >> boost, clamped to [1, saturated]. Default (Adaptive
	// false) preserves the static behavior exactly.
	Adaptive           bool
	AdaptiveBoostEvery uint32
	AdaptiveCoolEvery  uint32

	// R5-4: per-VM compile budget. Zero values keep the legacy behavior
	// (unlimited time and queue; one background compile at a time is the
	// explicit concurrency default).
	CompileBudgetNanos uint64 // cumulative compile-time budget; 0 = unlimited
	CompileQueueLimit  int    // max admitted background jobs (jitPending); 0 = unlimited
	CompileWorkers     int    // max concurrent background compiles; 0 = 1
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
	if c.Adaptive {
		if c.AdaptiveBoostEvery == 0 {
			c.AdaptiveBoostEvery = 64
		}
		if c.AdaptiveCoolEvery == 0 {
			c.AdaptiveCoolEvery = 8
		}
	}
	if c.CompileWorkers <= 0 {
		c.CompileWorkers = 1
	}
	return c
}

// R5-3 adaptive level caps: the effective threshold never drops below
// Threshold>>MaxAdaptiveBoost and never rises above Threshold<<MaxAdaptiveCool,
// so a pathological feedback signal cannot make the model unbounded.
const (
	MaxAdaptiveBoost = 4
	MaxAdaptiveCool  = 4
)

type Stats struct {
	Mode              Mode
	Threshold         uint32
	BackedgeThreshold uint32
	TraceBudget       uint32
	CodeCacheLimit    uint64
	Calls             uint64
	Backedges         uint64
	Candidates        uint64
	CompileNanos      uint64
	Compiled          uint64
	Rejected          uint64
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
	// R4-5/R4-6 array fast paths (Quick-only; machine code never receives Go
	// array pointers). ArrayIndexSites/ArrayBatchSites count matched trace
	// sites; the Yields counters count budgeted chunk yields. The Numeric
	// callback counters prove the R4-6 compiler/guard-proven purity paths
	// (NativeCallback numeric map/filter/reduce) were actually taken.
	ArrayIndexSites      uint64
	ArrayIndexYields     uint64
	ArrayBatchSites      uint64
	ArrayBatchYields     uint64
	NumericCallbackHits  uint64
	NumericCallbackFalls uint64
	// R4-8 side-exit cost observability. TraceFrameRetriesBlocked counts
	// tryQuickTrace entries that returned immediately because the same frame
	// already failed a guard at that backedge (deopt recovery must not retry
	// the failed trace version in the same frame). NativeTraceQuickFallbacks
	// counts the Quick re-execution of a native trace right after its entry
	// guard failed (the R4-3 PIC learning path); together the counters make
	// the per-backedge bridge cost of a side exit measurable.
	TraceFrameRetriesBlocked  uint64
	NativeTraceQuickFallbacks uint64
	RejectionReasons          []RejectionReason
	DeoptExits                []DeoptStat
	LastError                 string
	LastNativeError           string

	// R5-3 adaptive threshold observability. With Adaptive disabled the
	// effective thresholds equal the configured static ones and the level
	// counters stay zero, so a snapshot is comparable to the pre-R5 behavior.
	AdaptiveEnabled           bool
	AdaptiveBoost             uint64 // current boost level (threshold halvings)
	AdaptiveCool              uint64 // current cool level (threshold doublings)
	AdaptiveThreshold         uint32 // effective call threshold at snapshot
	AdaptiveBackedgeThreshold uint32 // effective backedge threshold at snapshot
	AdaptiveBenefits          uint64 // compiled executions observed
	AdaptiveFailures          uint64 // guard failures / deopts / rejected compiles observed
	// R5-4 compile budget observability. BudgetSpent accumulates across all
	// compile tiers (leaf, trace, native sync and background) whether or not a
	// limit is configured; the denied counters are non-zero only when a limit
	// actually rejected an admission.
	CompileBudgetNanos uint64 // configured cumulative compile-time limit (0 = unlimited)
	CompileQueueLimit  uint64 // configured background queue limit (0 = unlimited)
	CompileWorkers     uint64 // configured concurrent background compiles
	BudgetSpent        uint64 // cumulative compile time spent
	BudgetDenied       uint64 // compile admissions denied by the time budget
	QueueDenied        uint64 // background queue admissions denied by the queue limit
	QueueDepth         uint64 // pending background jobs at snapshot (jitPending)
	QueueDepthMax      uint64 // maximum pending background jobs observed
	// R5-7 aggregate observability (derived in VM.JITStats; kept additive so
	// existing consumers compile and print unchanged). Executions is the total
	// post-compile execution volume (quick + native, completions + budget
	// yields) used as the denominator for guard/deopt rates; Deopts is the
	// total semantic-exit deopt count (DeoptExits carries the per-exit detail
	// when Stats is enabled); CompileBenefit is Executions per compiled site
	// (Compiled+TracesCompiled; native installs are a subset of those counts,
	// not separate sites), i.e. the observed execution payoff of one
	// compilation; NativeHotEvictions counts evictions that had to drop a hot
	// unit because no cold unit could free the bytes (R5-5 heat protection
	// observability).
	Executions         uint64
	Deopts             uint64
	CompileBenefit     uint64
	NativeHotEvictions uint64
}
