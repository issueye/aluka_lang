package interpreter

import (
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

var defaultJIT = struct {
	sync.RWMutex
	config jit.Config
}{config: jit.Config{Mode: jit.Off, Threshold: 1000}}

// SetDefaultJITConfig configures subsequently-created VMs. The CLI calls it
// before constructing contexts; existing VMs retain their own configuration.
func SetDefaultJITConfig(config jit.Config) {
	defaultJIT.Lock()
	defaultJIT.config = config.Normalized()
	defaultJIT.Unlock()
}

func defaultJITConfig() jit.Config {
	defaultJIT.RLock()
	config := defaultJIT.config
	defaultJIT.RUnlock()
	return config.Normalized()
}

type quickJITState struct {
	calls      uint32
	backedges  uint32
	program    *jit.Program
	rejected   bool
	reason     string
	nativeUsed uint64
	// nativeHits is the R5-5 heat factor: every native execution (and the
	// install touch) advances it. LRU eviction weights recency (nativeUsed
	// clock) with heat: cold units (nativeHits < jitHotHeatThreshold) are
	// evicted before hot ones, and within the same heat class the
	// least-recently-used unit goes first.
	nativeHits          uint64
	callKind            quickCallKind
	callee              *vmClosure
	baseProgram         *jit.Program
	altCallee           *vmClosure
	altProgram          *jit.Program
	dumpedIR            bool
	dumpedASM           bool
	nativeCompiling     bool
	guardFailures       uint8
	nativeGuardFailures uint8
	nativeDisabled      bool
	calleeGuardFailures uint8
	calleeDisabled      bool
}

type quickCallKind uint8

const (
	quickCallUnknown quickCallKind = iota
	quickCallSelf
	quickCallBound
	quickCallUnsupported
)

type jitHotCount struct {
	calls     uint32
	backedges uint32
}

// jitAdaptiveState is the R5-3 feedback loop: counter-based, no wall clock.
// Every execution of a compiled function/trace is a benefit event; every
// guard failure / deopt / rejected compile is a failure event. After
// AdaptiveBoostEvery consecutive benefits the boost level rises (effective
// threshold halves, promoting borderline functions eagerly); after
// AdaptiveCoolEvery consecutive failures the cool level rises (effective
// threshold doubles, cooling down a VM whose compiles yield nothing). Levels
// are capped at jit.MaxAdaptiveBoost / jit.MaxAdaptiveCool.
type jitAdaptiveState struct {
	boostLevel uint8
	coolLevel  uint8
	benefits   uint64
	failures   uint64
	sinceBoost uint32
	sinceCool  uint32
}

type quickTraceKey struct {
	tmpl       *bytecode.FuncTemplate
	backedgePC int
}

type quickTraceState struct {
	backedges  uint32
	program    *jit.TraceProgram
	rejected   bool
	reason     string
	nativeUsed uint64
	// nativeHits: R5-5 heat factor, see quickJITState.nativeHits.
	nativeHits          uint64
	arrayPush           *arrayPushTraceState
	closureIncrement    *closureIncrementTraceState
	arrayIndex          *arrayIndexTraceState
	arrayBatch          *arrayBatchWriteTraceState
	guardFailures       uint8
	nativeGuardFailures uint8
	nativeDisabled      bool
	dumpedIR            bool
}

const jitGuardFailureLimit = 2

// jitHotHeatThreshold is the R5-5 heat boundary for LRU eviction: a native
// unit is "hot" once it has been touched this many times (install counts as
// one touch, every native execution adds one). Cold units are evicted before
// hot ones; among units of the same heat class the least-recently-used
// (smallest nativeUsed clock) unit is evicted, so recency ordering is
// preserved inside each class.
const jitHotHeatThreshold = 4

type arrayPushTraceState struct {
	receiverLocal int
	indexLocal    int
	boundLocal    int
	boundConst    float64
	boundIsLocal  bool
	pushTarget    engine.Value
	startPC       int
	exitPC        int
}

// arrayIndexTraceState is the R4-5 packed Number read shape:
//
//	for (; i < bound; i++) sum += array[i]
//
// The bulk executor accumulates the exact per-iteration float sequence into
// the sum local and advances the index local atomically per committed chunk,
// so a guard failure or safepoint interruption always resumes in a state Tier
// 0 can continue from.
type arrayIndexTraceState struct {
	arrayLocal   int
	sumLocal     int
	indexLocal   int
	boundLocal   int
	boundConst   float64
	boundIsLocal bool
	startPC      int
	exitPC       int
}

// arrayBatchWriteTraceState is the R4-6 safe batch write shape:
//
//	for (; i < bound; i++) array[i] = i              // key == value == loop var
//	for (; i < bound; i++) { array[j] = i; j++; }    // separate key counter
//	for (; i < bound; i++) array[j++] = i            // post-incremented key
//
// The bulk executor writes array[key+t] = value+t for the committed chunk and
// synchronizes the length property once per chunk, matching the per-iteration
// Tier 0 Set semantics (extend with holes, fill, length slot sync).
type arrayBatchWriteTraceState struct {
	arrayLocal   int
	keyLocal     int
	valueLocal   int
	indexLocal   int
	boundLocal   int
	boundConst   float64
	boundIsLocal bool
	startPC      int
	exitPC       int
}

type closureIncrementTraceState struct {
	calleeLocal int
	indexLocal  int
	boundLocal  int
	sumLocal    int
	target      *vmClosure
	// plan is the parsed numeric-upvalue body of the callee closure. It is
	// the R4-2 generalization of the single `() => ++n` shape: any sequence
	// of numeric upvalue read/write statements followed by one numeric
	// return (e.g. `() => { a++; b += a; return b; }`), including read-only
	// bodies (`() => a + b`) and in-frame (non-escaping) closures.
	plan *closurePlan
	// upvalues is the identity snapshot of the callee's upvalue list taken
	// when the trace state was built. Every execution re-checks
	// target.upvalues[i] == upvalues[i], so a second closure instance of the
	// same template with different captured cells falls back to Tier 0.
	upvalues []*upvalue
	startPC  int
	exitPC   int
}

type jitRejectionKey struct {
	tier   string
	reason string
}

type jitDeoptKey struct {
	tmpl       *bytecode.FuncTemplate
	backedgePC int
	exitID     int
	resumePC   int
}

type nativeCompileResult struct {
	state      *quickJITState
	program    *jit.Program
	generation uint64
	duration   time.Duration
	err        error
}

func (v *VM) JITStats() jit.Stats {
	stats := v.jitStats
	stats.Mode = v.jitConfig.Mode
	stats.Threshold = v.jitConfig.Threshold
	stats.BackedgeThreshold = v.jitConfig.BackedgeThreshold
	stats.TraceBudget = v.jitConfig.TraceBudget
	stats.CodeCacheLimit = v.jitConfig.CodeCacheBytes
	stats.NativeCodeBytes = v.jitNativeBytes
	// R5-3: adaptive feedback-loop snapshot. With Adaptive disabled the
	// effective thresholds equal the configured static ones and the level
	// counters stay zero.
	stats.AdaptiveEnabled = v.jitConfig.Adaptive
	stats.AdaptiveBoost = uint64(v.jitAdaptive.boostLevel)
	stats.AdaptiveCool = uint64(v.jitAdaptive.coolLevel)
	stats.AdaptiveThreshold = v.callThreshold()
	stats.AdaptiveBackedgeThreshold = v.backedgeThreshold()
	stats.AdaptiveBenefits = v.jitAdaptive.benefits
	stats.AdaptiveFailures = v.jitAdaptive.failures
	// R5-4: compile-budget snapshot. BudgetSpent accumulates whether or not a
	// limit is configured; the denied counters are non-zero only when a limit
	// rejected an admission.
	stats.CompileBudgetNanos = v.jitConfig.CompileBudgetNanos
	stats.CompileQueueLimit = uint64(v.jitConfig.CompileQueueLimit)
	stats.CompileWorkers = uint64(v.jitConfig.CompileWorkers)
	stats.BudgetSpent = v.jitBudgetSpent
	stats.QueueDepth = uint64(v.jitPending)
	// R4-4: aggregate the live property-PIC counters (function guards, native
	// input-plan guards and trace guards). Counters are cumulative, so a
	// repeated JITStats snapshot reports the same totals.
	for _, state := range v.jitStates {
		if state == nil {
			continue
		}
		for _, program := range []*jit.Program{state.program, state.altProgram, state.baseProgram} {
			hits, adds, rejects, overflows, coolDowns := program.PropertyPICStats()
			stats.PropertyPICHits += hits
			stats.PropertyPICAdds += adds
			stats.PropertyPICRejections += rejects
			stats.PropertyPICOverflows += overflows
			stats.PropertyPICCoolDowns += coolDowns
		}
	}
	for _, state := range v.jitTraces {
		if state == nil {
			continue
		}
		hits, adds, rejects, overflows, coolDowns := state.program.PropertyPICStats()
		stats.PropertyPICHits += hits
		stats.PropertyPICAdds += adds
		stats.PropertyPICRejections += rejects
		stats.PropertyPICOverflows += overflows
		stats.PropertyPICCoolDowns += coolDowns
	}
	if len(v.jitRejections) != 0 {
		stats.RejectionReasons = make([]jit.RejectionReason, 0, len(v.jitRejections))
		for key, count := range v.jitRejections {
			stats.RejectionReasons = append(stats.RejectionReasons, jit.RejectionReason{
				Tier: key.tier, Reason: key.reason, Count: count,
			})
		}
		sort.Slice(stats.RejectionReasons, func(i, j int) bool {
			if stats.RejectionReasons[i].Tier != stats.RejectionReasons[j].Tier {
				return stats.RejectionReasons[i].Tier < stats.RejectionReasons[j].Tier
			}
			return stats.RejectionReasons[i].Reason < stats.RejectionReasons[j].Reason
		})
	}
	if len(v.jitDeopts) != 0 {
		stats.DeoptExits = make([]jit.DeoptStat, 0, len(v.jitDeopts))
		for key, count := range v.jitDeopts {
			stats.DeoptExits = append(stats.DeoptExits, jit.DeoptStat{
				Function: key.tmpl.Name, BackedgePC: key.backedgePC,
				ExitID: key.exitID, ResumePC: key.resumePC, Count: count,
			})
		}
		sort.Slice(stats.DeoptExits, func(i, j int) bool {
			a, b := stats.DeoptExits[i], stats.DeoptExits[j]
			if a.Function != b.Function {
				return a.Function < b.Function
			}
			if a.BackedgePC != b.BackedgePC {
				return a.BackedgePC < b.BackedgePC
			}
			return a.ExitID < b.ExitID
		})
	}
	// R5-7 derived aggregates. Executions is the total post-compile execution
	// volume (quick + native, completions + budget yields) that serves as the
	// denominator for guard and deopt rates; CompileBenefit is Executions per
	// compiled site, i.e. how much compiled code is used per unit of compile
	// cost. Compiled and TracesCompiled count every successful site compile
	// (native installs are a subset counted again by NativeCompiled /
	// NativeTracesCompiled), so the unique site count is their sum.
	stats.Executions = stats.Executed + stats.NativeExecuted +
		stats.TracesExecuted + stats.NativeTracesExecuted +
		stats.TraceYields + stats.NativeYields + stats.NativeTraceYields
	compiledSites := stats.Compiled + stats.TracesCompiled
	if compiledSites != 0 {
		stats.CompileBenefit = stats.Executions / compiledSites
	}
	return stats
}

func (v *VM) recordJITRejection(tier string, err error) {
	if !v.jitConfig.Stats || err == nil {
		return
	}
	if v.jitRejections == nil {
		v.jitRejections = make(map[jitRejectionKey]uint64)
	}
	v.jitRejections[jitRejectionKey{tier: tier, reason: err.Error()}]++
}

func (v *VM) recordTraceDeopt(key quickTraceKey, exit jit.DeoptExit) {
	if !v.jitConfig.Stats {
		return
	}
	// R5-7 aggregate deopt counter; the per-exit detail map below stays the
	// source of truth for DeoptExits when Stats is enabled.
	v.jitStats.Deopts++
	if v.jitDeopts == nil {
		v.jitDeopts = make(map[jitDeoptKey]uint64)
	}
	v.jitDeopts[jitDeoptKey{
		tmpl: key.tmpl, backedgePC: key.backedgePC,
		exitID: exit.ID, resumePC: exit.ResumePC,
	}]++
}

func (v *VM) restoreTraceExitStack(exit jit.DeoptExit) error {
	if exit.StackDepth != len(exit.StackValues) {
		return fmt.Errorf("jit: deopt exit stack has %d values, want %d", len(exit.StackValues), exit.StackDepth)
	}
	v.appendValues(exit.StackValues)
	return nil
}

// resumeTraceExit restores a trace semantic exit into the VM. Normal exits
// restore the operand stack and resume at the recorded bytecode boundary. An
// exception exit (PendingException != nil) instead discards the trace's
// operand stack — JS exception-unwinding semantics — and returns a *jsThrow
// carrying the original thrown value, which the VM main loop feeds to
// handleThrow so the existing try/catch/finally machinery consumes it. The
// exit is recorded for deopt statistics in both cases.
func (v *VM) resumeTraceExit(key quickTraceKey, exit jit.DeoptExit) (int, bool, error) {
	if exit.PendingException != nil {
		v.recordTraceDeopt(key, exit)
		return 0, false, &jsThrow{val: exit.PendingException}
	}
	if key.tmpl == nil {
		return 0, false, fmt.Errorf("jit: deopt exit has no bytecode template")
	}
	codeLen := len(key.tmpl.Code)
	if codeLen%bytecode.InstrSize != 0 || exit.ResumePC < 0 ||
		exit.ResumePC%bytecode.InstrSize != 0 || exit.ResumePC > codeLen-bytecode.InstrSize {
		return 0, false, fmt.Errorf("jit: invalid deopt resume PC %d for bytecode length %d", exit.ResumePC, codeLen)
	}
	v.recordTraceDeopt(key, exit)
	if err := v.restoreTraceExitStack(exit); err != nil {
		return 0, false, err
	}
	return exit.ResumePC, true, nil
}

func (v *VM) noteJITGuardFailure(state *quickJITState) {
	if state == nil || state.rejected {
		return
	}
	// R5-3: a guard failure on compiled code is a negative feedback signal
	// (deopt); consecutive failures cool the effective threshold.
	v.noteAdaptiveFailure()
	// R4-3: a failed execution that absorbed a beyond-baseline shape is the
	// confirmation step of polymorphic learning (the guard grew); reset the
	// failure chain so a multi-property site can absorb one shape per guard
	// without being rejected mid-learning. Unabsorbable sites (accessor,
	// Proxy, prototype, over-cap churn) never admit and still disable after
	// jitGuardFailureLimit consecutive failures.
	if state.program != nil && state.program.TakePICAbsorptions() > 0 {
		state.guardFailures = 0
	}
	if state.guardFailures < jitGuardFailureLimit {
		state.guardFailures++
	}
	if state.guardFailures >= jitGuardFailureLimit {
		state.rejected = true
		v.dropNative(state)
		if v.jitConfig.Stats {
			v.jitStats.QuickGuardDisabled++
		}
	}
}

func (v *VM) noteTraceGuardFailure(state *quickTraceState) {
	if state == nil || state.rejected {
		return
	}
	v.noteAdaptiveFailure()
	// R4-3: see noteJITGuardFailure — a trace with several property guards
	// (e.g. writeBoth writes o.a and o.b) needs one confirmation observation
	// per guard; each admission resets the chain instead of accumulating
	// toward the rejection limit.
	if state.program != nil && state.program.TakePICAbsorptions() > 0 {
		state.guardFailures = 0
	}
	if state.guardFailures < jitGuardFailureLimit {
		state.guardFailures++
	}
	if state.guardFailures >= jitGuardFailureLimit {
		state.rejected = true
		v.dropNativeTrace(state)
		if v.jitConfig.Stats {
			v.jitStats.TraceGuardDisabled++
		}
	}
}

func (v *VM) noteNativeGuardFailure(state *quickJITState) {
	if state == nil || state.nativeDisabled || state.rejected {
		return
	}
	v.noteAdaptiveFailure()
	if state.nativeGuardFailures < jitGuardFailureLimit {
		state.nativeGuardFailures++
	}
	if state.nativeGuardFailures >= jitGuardFailureLimit {
		state.nativeDisabled = true
		v.dropNative(state)
		if v.jitConfig.Stats {
			v.jitStats.NativeGuardDisabled++
		}
	}
}

func (v *VM) noteNativeTraceGuardFailure(state *quickTraceState) {
	if state == nil || state.nativeDisabled || state.rejected {
		return
	}
	v.noteAdaptiveFailure()
	if state.nativeGuardFailures < jitGuardFailureLimit {
		state.nativeGuardFailures++
	}
	if state.nativeGuardFailures >= jitGuardFailureLimit {
		state.nativeDisabled = true
		v.dropNativeTrace(state)
		if v.jitConfig.Stats {
			v.jitStats.NativeTraceGuardDisabled++
		}
	}
}

func (v *VM) noteCalleeGuardFailure(state *quickJITState) {
	if state == nil || state.calleeDisabled || state.rejected {
		return
	}
	v.noteAdaptiveFailure()
	if state.calleeGuardFailures < jitGuardFailureLimit {
		state.calleeGuardFailures++
	}
	if state.calleeGuardFailures >= jitGuardFailureLimit {
		state.calleeDisabled = true
		v.dropNative(state)
		if v.jitConfig.Stats {
			v.jitStats.CalleeGuardDisabled++
		}
	}
}

func resetJITGuardFailures(state *quickJITState) {
	if state != nil {
		state.guardFailures = 0
		state.nativeGuardFailures = 0
	}
}

func resetQuickGuardFailures(state *quickJITState) {
	if state != nil {
		state.guardFailures = 0
	}
}

func resetTraceGuardFailures(state *quickTraceState) {
	if state != nil {
		state.guardFailures = 0
		state.nativeGuardFailures = 0
	}
}

func resetQuickTraceGuardFailures(state *quickTraceState) {
	if state != nil {
		state.guardFailures = 0
	}
}

func (v *VM) jitDumpWriter() io.Writer {
	if v.jitConfig.DumpWriter != nil {
		return v.jitConfig.DumpWriter
	}
	return os.Stderr
}

func (v *VM) dumpJITIR(state *quickJITState) {
	if state == nil || state.program == nil || state.dumpedIR || v.jitConfig.Dump != jit.DumpIR {
		return
	}
	fmt.Fprintf(v.jitDumpWriter(), "JIT dump tier=quick\n%s", state.program.DumpIR())
	state.dumpedIR = true
}

func (v *VM) dumpJITASM(state *quickJITState) {
	if state == nil || state.program == nil || state.dumpedASM || v.jitConfig.Dump != jit.DumpASM {
		return
	}
	bytes := state.program.NativeDebugBytes()
	fmt.Fprintf(v.jitDumpWriter(), "JIT dump tier=native bytes=%d\n", len(bytes))
	io.WriteString(v.jitDumpWriter(), state.program.NativeDisassembly())
	state.dumpedASM = true
}

func (v *VM) verifyNativeResult(program *jit.Program, thisVal engine.Value, args []engine.Value, native engine.Value) bool {
	if !v.jitConfig.Verify {
		return true
	}
	quick, reason, err := program.Execute(thisVal, args)
	v.jitStats.VerifyChecks++
	if err != nil || reason != jit.Executed || !sameJITValue(native, quick) {
		v.jitStats.VerifyFailures++
		return false
	}
	return true
}

func sameJITValue(a, b engine.Value) bool {
	if a == nil || b == nil || a.Type() != b.Type() {
		return false
	}
	if a.Type() == engine.TypeNumber {
		af, aok := a.Float()
		bf, bok := b.Float()
		return aok && bok && (math.IsNaN(af) && math.IsNaN(bf) || math.Float64bits(af) == math.Float64bits(bf))
	}
	return a.String() == b.String()
}

func sameJITLocals(a, b []engine.Value) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameJITValue(a[i], b[i]) {
			return false
		}
	}
	return true
}

func (v *VM) pollJITSafepoint() error {
	if v.jitConfig.Stats {
		v.jitStats.SafepointPolls++
	}
	if v.oomEnabled && engine.OOMTriggered() {
		engine.ConsumeOOM()
		if v.jitConfig.Stats {
			v.jitStats.Interruptions++
		}
		return engine.OOMError()
	}
	if v.jitConfig.Safepoint != nil {
		if err := v.jitConfig.Safepoint(); err != nil {
			if v.jitConfig.Stats {
				v.jitStats.Interruptions++
			}
			return err
		}
	}
	return nil
}

// effectiveThreshold applies the R5-3 feedback loop to a static threshold:
// effective = static >> boost with the cool level shifting back (static <<
// cool). The result is clamped to [1, saturated] so no feedback level can
// overflow or zero the threshold. With Adaptive disabled the static value is
// returned unchanged.
func (v *VM) effectiveThreshold(static uint32) uint32 {
	if !v.jitConfig.Adaptive {
		return static
	}
	a := &v.jitAdaptive
	if a.boostLevel >= a.coolLevel {
		shift := uint(a.boostLevel - a.coolLevel)
		if shift >= 31 {
			return 1
		}
		if t := static >> shift; t >= 1 {
			return t
		}
		return 1
	}
	shift := uint(a.coolLevel - a.boostLevel)
	if shift >= 20 || static > ^uint32(0)>>shift {
		return ^uint32(0)
	}
	return static << shift
}

func (v *VM) callThreshold() uint32 {
	return v.effectiveThreshold(v.jitConfig.Threshold)
}

func (v *VM) backedgeThreshold() uint32 {
	return v.effectiveThreshold(v.jitConfig.BackedgeThreshold)
}

// noteAdaptiveBenefit records one execution of a compiled function or trace.
// AdaptiveBoostEvery consecutive benefits raise the boost level (lowering the
// effective threshold by one half per level), so long hotspots with low deopt
// rates promote borderline functions more eagerly.
func (v *VM) noteAdaptiveBenefit() {
	if !v.jitConfig.Adaptive {
		return
	}
	a := &v.jitAdaptive
	a.benefits++
	a.sinceBoost++
	every := v.jitConfig.AdaptiveBoostEvery
	if every == 0 {
		every = 64
	}
	if a.sinceBoost >= every && a.boostLevel < jit.MaxAdaptiveBoost {
		a.boostLevel++
		a.sinceBoost = 0
	}
}

// noteAdaptiveFailure records one guard failure / deopt / rejected compile.
// AdaptiveCoolEvery consecutive failures raise the cool level (doubling the
// effective threshold per level), cooling down VMs whose compiles yield no
// benefit.
func (v *VM) noteAdaptiveFailure() {
	if !v.jitConfig.Adaptive {
		return
	}
	a := &v.jitAdaptive
	a.failures++
	a.sinceCool++
	every := v.jitConfig.AdaptiveCoolEvery
	if every == 0 {
		every = 8
	}
	if a.sinceCool >= every && a.coolLevel < jit.MaxAdaptiveCool {
		a.coolLevel++
		a.sinceCool = 0
	}
}

// compileAdmitted reports whether a new compile may start under the R5-4
// cumulative compile-time budget. A zero budget is unlimited (default).
// Denied admissions are observable via jit.Stats.BudgetDenied.
func (v *VM) compileAdmitted() bool {
	limit := v.jitConfig.CompileBudgetNanos
	if limit != 0 && v.jitBudgetSpent >= limit {
		if v.jitConfig.Stats {
			v.jitStats.BudgetDenied++
		}
		return false
	}
	return true
}

// spendCompileBudget accounts measured compile time against the R5-4 budget.
// It is called at every site that measures a compile, whether or not a limit
// is configured, so jit.Stats.BudgetSpent is always observable.
func (v *VM) spendCompileBudget(nanos uint64) {
	v.jitBudgetSpent += nanos
}

// ConfigureJIT changes this VM's JIT policy and clears compiled state. It is
// intended for embedders and tests that need per-context control.
func (v *VM) ConfigureJIT(config jit.Config) {
	v.closeJIT()
	v.jitGeneration++
	v.jitConfig = config.Normalized()
	v.jitStates = make(map[*bytecode.FuncTemplate]*quickJITState)
	v.jitHotCounts = make(map[*bytecode.FuncTemplate]jitHotCount)
	v.jitTraces = make(map[quickTraceKey]*quickTraceState)
	v.jitStats = jit.Stats{}
	v.jitNativeBytes = 0
	v.jitNativeClock = 0
	v.jitRejections = make(map[jitRejectionKey]uint64)
	v.jitDeopts = make(map[jitDeoptKey]uint64)
	v.jitCompileDone = make(chan nativeCompileResult, 16)
	v.jitPending = 0
	v.jitBudgetSpent = 0
	v.jitAdaptive = jitAdaptiveState{}
	v.jitCompileSlots = make(chan struct{}, v.jitConfig.CompileWorkers)
}

func (v *VM) closeJIT() {
	if v.jitCloseStartHook != nil {
		v.jitCloseStartHook()
	}
	for v.jitPending > 0 {
		result := <-v.jitCompileDone
		v.jitPending--
		if result.program != nil {
			_ = result.program.Close()
		}
	}
	v.jitCompileWG.Wait()
	for _, state := range v.jitStates {
		if state != nil && state.program != nil {
			_ = state.program.Close()
		}
		if state != nil && state.altProgram != nil {
			_ = state.altProgram.Close()
		}
		if state != nil && state.baseProgram != nil {
			_ = state.baseProgram.Close()
		}
	}
	for _, state := range v.jitTraces {
		if state != nil && state.program != nil {
			_ = state.program.Close()
		}
	}
	v.jitNativeBytes = 0
	v.jitNativeClock = 0
}

func (v *VM) jitStateFor(tmpl *bytecode.FuncTemplate) *quickJITState {
	if v.jitConfig.Mode == jit.Off || tmpl == nil {
		return nil
	}
	return v.jitStates[tmpl]
}

func (v *VM) promoteJITState(tmpl *bytecode.FuncTemplate, count jitHotCount) *quickJITState {
	state := v.jitStates[tmpl]
	if state == nil {
		state = &quickJITState{calls: count.calls, backedges: count.backedges}
		v.jitStates[tmpl] = state
	}
	delete(v.jitHotCounts, tmpl)
	return state
}

func (v *VM) noteJITCall(cl *vmClosure) *quickJITState {
	if v.jitConfig.Mode == jit.Off || cl == nil || cl.tmpl == nil {
		return nil
	}
	if v.jitConfig.Stats {
		v.jitStats.Calls++
	}
	state := cl.jitState
	if cl.jitGeneration != v.jitGeneration || state == nil {
		state = v.jitStateFor(cl.tmpl)
		cl.jitState = state
		cl.jitGeneration = v.jitGeneration
	}
	if state == nil {
		count := v.jitHotCounts[cl.tmpl]
		if count.calls < ^uint32(0) {
			count.calls++
		}
		if count.calls < v.callThreshold() {
			v.jitHotCounts[cl.tmpl] = count
			return nil
		}
		state = v.promoteJITState(cl.tmpl, count)
		cl.jitState = state
	}
	if state.calls < ^uint32(0) {
		if state.calls < v.callThreshold() {
			state.calls++
		}
	}
	v.maybeCompileJITState(state, cl.tmpl, state.calls >= v.callThreshold())
	v.maybeSpecializeCallee(state, cl)
	return state
}

func (v *VM) noteJITBackedge(tmpl *bytecode.FuncTemplate) *quickJITState {
	if v.jitConfig.Mode == jit.Off || tmpl == nil {
		return nil
	}
	if v.jitConfig.Stats {
		v.jitStats.Backedges++
	}
	state := v.jitStateFor(tmpl)
	if state == nil {
		count := v.jitHotCounts[tmpl]
		if count.backedges < ^uint32(0) {
			count.backedges++
		}
		if count.backedges < v.backedgeThreshold() {
			v.jitHotCounts[tmpl] = count
			return nil
		}
		state = v.promoteJITState(tmpl, count)
	}
	if state.backedges < ^uint32(0) {
		if state.backedges < v.backedgeThreshold() {
			state.backedges++
		}
	}
	v.maybeCompileJITState(state, tmpl, state.backedges >= v.backedgeThreshold())
	return state
}

func (v *VM) maybeCompileJITState(state *quickJITState, tmpl *bytecode.FuncTemplate, hot bool) {
	if state == nil || !hot || state.program != nil || state.rejected {
		if state != nil && state.rejected && v.jitConfig.Stats {
			// R3-7: the structured rejection cache serves this hot candidate;
			// no compile attempt (and no candidate recount) happens again.
			v.jitStats.RejectionCacheHits++
		}
		return
	}
	// R5-4: the cumulative compile-time budget denies new compiles once spent
	// reaches the limit; the state stays uncompiled and the interpreter
	// continues on Tier 0 / Quick (the denial is observable via BudgetDenied).
	if !v.compileAdmitted() {
		return
	}
	if v.jitConfig.Stats {
		v.jitStats.Candidates++
	}
	compileStart := time.Now()
	// R3-7 compile-time candidate filter: reject statically unsupported
	// shapes (try/catch regions, unsupported opcodes, dynamic arguments,
	// oversized frames) without running the full lowering.
	var program *jit.Program
	var err error
	if scanErr := jit.RejectLeafReason(tmpl); scanErr != nil {
		err = scanErr
	} else {
		program, err = jit.CompileLeaf(tmpl)
	}
	if err == nil && program.IsTrivialThisPropertyGetter() {
		err = fmt.Errorf("jit: cost model rejects trivial this-property getter")
		program = nil
	}
	if v.jitConfig.Stats {
		elapsed := uint64(time.Since(compileStart))
		v.jitStats.CompileNanos += elapsed
		v.spendCompileBudget(elapsed)
	}
	if err != nil {
		v.recordJITRejection("quick", err)
		state.rejected = true
		state.reason = err.Error()
		v.jitStats.LastError = state.reason
		if v.jitConfig.Stats {
			v.jitStats.Rejected++
		}
		v.noteAdaptiveFailure()
	} else {
		state.program = program
		v.dumpJITIR(state)
		if v.jitConfig.Stats {
			v.jitStats.Compiled++
		}
		if v.jitConfig.Mode == jit.Auto {
			if _, guardedCall := program.RequiresSelf(); guardedCall {
				return
			}
			if len(program.Code) >= 128 {
				v.queueNativeCompile(state)
				return
			}
			if !v.compileAdmitted() {
				return
			}
			nativeStart := time.Now()
			if err := v.installNative(state); err != nil {
				v.recordJITRejection("native", err)
				if v.jitConfig.Stats {
					elapsed := uint64(time.Since(nativeStart))
					v.jitStats.NativeCompileNanos += elapsed
					v.spendCompileBudget(elapsed)
				}
				v.jitStats.LastNativeError = err.Error()
				if v.jitConfig.Stats {
					v.jitStats.NativeRejected++
				}
			} else {
				v.dumpJITASM(state)
				if v.jitConfig.Stats {
					elapsed := uint64(time.Since(nativeStart))
					v.jitStats.NativeCompileNanos += elapsed
					v.spendCompileBudget(elapsed)
					v.jitStats.NativeCompiled++
				}
			}
		}
	}
}

func (v *VM) maybeSpecializeCallee(state *quickJITState, cl *vmClosure) {
	if state == nil || state.program == nil || state.callKind != quickCallUnknown || cl == nil {
		return
	}
	upvalueIndex, required := state.program.RequiresSelf()
	if !required {
		return
	}
	value, ok := closureUpvalue(cl, upvalueIndex)
	if !ok {
		state.callKind = quickCallUnsupported
		return
	}
	if value == cl {
		state.callKind = quickCallSelf
		return
	}
	target, ok := value.(*vmClosure)
	if !ok || target.tmpl == nil {
		state.callKind = quickCallUnsupported
		return
	}
	// R5-4: the cumulative compile budget denies the callee leaf compile too.
	if !v.compileAdmitted() {
		return
	}
	targetProgram, err := jit.CompileLeaf(target.tmpl)
	if err != nil {
		v.recordJITRejection("callee", err)
		state.callKind = quickCallUnsupported
		v.noteAdaptiveFailure()
		return
	}
	baseProgram := state.program.CloneForNative()
	inlined, err := state.program.BindCallTarget(targetProgram)
	if err != nil {
		_ = baseProgram.Close()
		v.recordJITRejection("callee", err)
		state.callKind = quickCallUnsupported
		v.noteAdaptiveFailure()
		return
	}
	state.callKind = quickCallBound
	state.callee = target
	state.baseProgram = baseProgram
	if v.jitConfig.Stats {
		v.jitStats.CalleeSpecialized++
		if inlined {
			v.jitStats.CalleeInlined++
		}
	}
	if inlined && v.jitConfig.Mode == jit.Auto {
		if !v.compileAdmitted() {
			return
		}
		nativeStart := time.Now()
		if err := v.installNative(state); err != nil {
			v.recordJITRejection("native", err)
			if v.jitConfig.Stats {
				elapsed := uint64(time.Since(nativeStart))
				v.jitStats.NativeCompileNanos += elapsed
				v.spendCompileBudget(elapsed)
			}
			v.jitStats.LastNativeError = err.Error()
			if v.jitConfig.Stats {
				v.jitStats.NativeRejected++
			}
		} else {
			v.dumpJITASM(state)
			if v.jitConfig.Stats {
				elapsed := uint64(time.Since(nativeStart))
				v.jitStats.NativeCompileNanos += elapsed
				v.spendCompileBudget(elapsed)
				v.jitStats.NativeCompiled++
			}
		}
	}
}

func (v *VM) specializeAlternateCallee(state *quickJITState, target *vmClosure) (*jit.Program, bool) {
	if state == nil || target == nil || target.tmpl == nil || state.baseProgram == nil || state.altProgram != nil {
		return nil, false
	}
	if !v.compileAdmitted() {
		return nil, false
	}
	targetProgram, err := jit.CompileLeaf(target.tmpl)
	if err != nil {
		v.recordJITRejection("callee", err)
		v.noteAdaptiveFailure()
		return nil, false
	}
	program := state.baseProgram.CloneForNative()
	inlined, err := program.BindCallTarget(targetProgram)
	if err != nil {
		v.recordJITRejection("callee", err)
		_ = program.Close()
		v.noteAdaptiveFailure()
		return nil, false
	}
	state.altCallee = target
	state.altProgram = program
	if v.jitConfig.Stats {
		v.jitStats.CalleeSpecialized++
		v.jitStats.CalleePICAdds++
		if inlined {
			v.jitStats.CalleeInlined++
		}
	}
	if inlined && v.jitConfig.Mode == jit.Auto {
		if !v.compileAdmitted() {
			return program, true
		}
		nativeStart := time.Now()
		if v.jitConfig.Dump == jit.DumpASM {
			err = program.CompileNativeForDump()
		} else {
			err = program.CompileNative()
		}
		if v.jitConfig.Stats {
			elapsed := uint64(time.Since(nativeStart))
			v.jitStats.NativeCompileNanos += elapsed
			v.spendCompileBudget(elapsed)
		}
		if err == nil {
			err = v.reserveNative(state, uint64(program.NativeSize()))
		}
		if err != nil {
			_ = program.Close()
			v.recordJITRejection("native-callee-pic", err)
			v.jitStats.LastNativeError = err.Error()
			if v.jitConfig.Stats {
				v.jitStats.NativeRejected++
			}
		} else {
			if v.jitConfig.Dump == jit.DumpASM {
				fmt.Fprintf(v.jitDumpWriter(), "JIT dump tier=native-callee-pic version=2 bytes=%d\n", program.NativeSize())
				io.WriteString(v.jitDumpWriter(), program.NativeDisassembly())
			}
			if v.jitConfig.Stats {
				v.jitStats.NativeCompiled++
			}
		}
	}
	return program, true
}

func closureUpvalue(cl *vmClosure, index int) (engine.Value, bool) {
	if cl == nil || index < 0 || index >= len(cl.upvalues) {
		return nil, false
	}
	uv := cl.upvalues[index]
	if uv == nil {
		return nil, false
	}
	if uv.slot != nil {
		return *uv.slot, true
	}
	return uv.closed, true
}

func storeClosureUpvalue(cl *vmClosure, index int, value engine.Value) bool {
	if cl == nil || index < 0 || index >= len(cl.upvalues) {
		return false
	}
	uv := cl.upvalues[index]
	if uv == nil {
		return false
	}
	if uv.slot != nil {
		*uv.slot = value
	} else {
		uv.closed = value
	}
	return true
}

func (v *VM) queueNativeCompile(state *quickJITState) {
	if state == nil || state.program == nil || state.nativeCompiling || state.nativeDisabled || state.program.HasNative() {
		return
	}
	// R5-4: the queue-length limit caps admitted background jobs (jitPending).
	// A rejected candidate stays on the Quick tier; the denial is observable
	// via QueueDenied and never blocks the interpreter.
	if v.jitConfig.CompileQueueLimit > 0 && v.jitPending >= v.jitConfig.CompileQueueLimit {
		if v.jitConfig.Stats {
			v.jitStats.QueueDenied++
		}
		return
	}
	// R5-4: the cumulative compile-time budget also gates background native
	// compiles ("budget reached -> do not start new compiles").
	if !v.compileAdmitted() {
		return
	}
	program := state.program.CloneForNative()
	if program == nil {
		return
	}
	state.nativeCompiling = true
	v.jitPending++
	if uint64(v.jitPending) > v.jitStats.QueueDepthMax {
		v.jitStats.QueueDepthMax = uint64(v.jitPending)
	}
	if v.jitConfig.Stats {
		v.jitStats.BackgroundQueued++
	}
	generation := v.jitGeneration
	retainDump := v.jitConfig.Dump == jit.DumpASM
	done := v.jitCompileDone
	v.jitCompileWG.Add(1)
	go func() {
		// R5-4: the concurrency semaphore bounds how many of these goroutines
		// actually compile at once. It is acquired inside the goroutine, so a
		// full slot pool never blocks the interpreter hot path; the release is
		// deferred, so a panicking compile still frees its slot.
		v.jitCompileSlots <- struct{}{}
		defer func() { <-v.jitCompileSlots }()
		if v.jitCompileStartHook != nil {
			v.jitCompileStartHook()
		}
		start := time.Now()
		var err error
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("jit: background native compile panic: %v", recovered)
			}
			done <- nativeCompileResult{
				state: state, program: program, generation: generation,
				duration: time.Since(start), err: err,
			}
			v.jitCompileWG.Done()
		}()
		if retainDump {
			err = program.CompileNativeForDump()
		} else {
			err = program.CompileNative()
		}
	}()
}

func (v *VM) pollNativeCompiles() {
	for v.jitPending > 0 {
		select {
		case result := <-v.jitCompileDone:
			v.jitPending--
			if result.state != nil {
				result.state.nativeCompiling = false
			}
			if result.generation != v.jitGeneration || result.state == nil || result.state.rejected || result.state.program == nil {
				if result.program != nil {
					_ = result.program.Close()
				}
				if v.jitConfig.Stats {
					v.jitStats.BackgroundDiscarded++
				}
				continue
			}
			if v.jitConfig.Stats {
				elapsed := uint64(result.duration)
				v.jitStats.NativeCompileNanos += elapsed
				v.spendCompileBudget(elapsed)
				v.jitStats.BackgroundCompleted++
			}
			if result.err != nil {
				v.recordJITRejection("native", result.err)
				v.jitStats.LastNativeError = result.err.Error()
				if v.jitConfig.Stats {
					v.jitStats.NativeRejected++
				}
				_ = result.program.Close()
				continue
			}
			if err := v.adoptNative(result.state, result.program); err != nil {
				v.recordJITRejection("native", err)
				v.jitStats.LastNativeError = err.Error()
				if v.jitConfig.Stats {
					v.jitStats.NativeRejected++
				}
				_ = result.program.Close()
				continue
			}
			v.dumpJITASM(result.state)
			if v.jitConfig.Stats {
				v.jitStats.NativeCompiled++
			}
		default:
			return
		}
	}
}

func (v *VM) installNative(state *quickJITState) error {
	if state == nil || state.program == nil {
		return fmt.Errorf("jit: missing native candidate")
	}
	var err error
	if v.jitConfig.Dump == jit.DumpASM {
		err = state.program.CompileNativeForDump()
	} else {
		err = state.program.CompileNative()
	}
	if err != nil {
		return err
	}
	size := uint64(state.program.NativeSize())
	if err := v.reserveNative(state, size); err != nil {
		_ = state.program.Close()
		return err
	}
	return nil
}

func (v *VM) adoptNative(state *quickJITState, program *jit.Program) error {
	if state == nil || state.program == nil || program == nil {
		return fmt.Errorf("jit: missing prepared native candidate")
	}
	size := uint64(program.NativeSize())
	if err := v.reserveNative(state, size); err != nil {
		return err
	}
	if err := state.program.AdoptNativeFrom(program); err != nil {
		if size <= v.jitNativeBytes {
			v.jitNativeBytes -= size
		}
		state.nativeUsed = 0
		return err
	}
	return nil
}

func (v *VM) reserveNative(state *quickJITState, size uint64) error {
	if err := v.reserveNativeCode(size, state, nil); err != nil {
		return err
	}
	v.touchNative(state)
	return nil
}

func (v *VM) reserveNativeTrace(state *quickTraceState, size uint64) error {
	if err := v.reserveNativeCode(size, nil, state); err != nil {
		return err
	}
	v.touchNativeTrace(state)
	return nil
}

func (v *VM) reserveNativeCode(size uint64, excludedState *quickJITState, excludedTrace *quickTraceState) error {
	limit := v.jitConfig.CodeCacheBytes
	if size == 0 || size > limit {
		return fmt.Errorf("jit: native code size %d exceeds cache budget %d", size, limit)
	}
	for v.jitNativeBytes+size > limit {
		// R5-5 weighted LRU: prefer cold victims (heat below the threshold)
		// over hot ones; only when every installed unit is hot is a hot unit
		// evicted (then by recency). Recency keeps its ordering role inside
		// each heat class, so the historical clock semantics remain intact
		// for cold units and the heat factor is observable via
		// NativeHotEvictions.
		victimState, victimTrace := v.lruVictim(excludedState, excludedTrace, true)
		hotVictim := false
		if victimState == nil && victimTrace == nil {
			victimState, victimTrace = v.lruVictim(excludedState, excludedTrace, false)
			hotVictim = true
		}
		if victimState == nil && victimTrace == nil {
			return fmt.Errorf("jit: native code cache cannot free %d bytes", size)
		}
		if victimState != nil {
			v.dropNative(victimState)
		} else {
			v.dropNativeTrace(victimTrace)
		}
		if v.jitConfig.Stats {
			v.jitStats.NativeEvictions++
			if hotVictim {
				v.jitStats.NativeHotEvictions++
			}
		}
	}
	v.jitNativeBytes += size
	return nil
}

// lruVictim returns the least-recently-used native unit (quickJITState or
// quickTraceState) whose native code is installed, excluding the unit being
// installed. With coldOnly the search is restricted to cold units (nativeHits
// below jitHotHeatThreshold); the caller falls back to coldOnly=false when no
// cold victim can free the requested bytes — which implies every installed
// unit is hot, so the chosen victim is a hot one.
func (v *VM) lruVictim(excludedState *quickJITState, excludedTrace *quickTraceState, coldOnly bool) (*quickJITState, *quickTraceState) {
	var victimState *quickJITState
	var victimTrace *quickTraceState
	victimUsed := ^uint64(0)
	for _, candidate := range v.jitStates {
		if candidate == excludedState || !jitStateHasNative(candidate) {
			continue
		}
		if coldOnly && candidate.nativeHits >= jitHotHeatThreshold {
			continue
		}
		if candidate.nativeUsed < victimUsed {
			victimState, victimTrace, victimUsed = candidate, nil, candidate.nativeUsed
		}
	}
	for _, candidate := range v.jitTraces {
		if candidate == excludedTrace || candidate == nil || candidate.program == nil || !candidate.program.HasNative() {
			continue
		}
		if coldOnly && candidate.nativeHits >= jitHotHeatThreshold {
			continue
		}
		if candidate.nativeUsed < victimUsed {
			victimState, victimTrace, victimUsed = nil, candidate, candidate.nativeUsed
		}
	}
	return victimState, victimTrace
}

func (v *VM) touchNative(state *quickJITState) {
	v.jitNativeClock++
	if v.jitNativeClock == 0 {
		v.jitNativeClock = 1
	}
	state.nativeUsed = v.jitNativeClock
	// R5-5: every native execution (and the install touch) advances the heat
	// factor that weights LRU eviction.
	state.nativeHits++
}

func (v *VM) touchNativeTrace(state *quickTraceState) {
	v.jitNativeClock++
	if v.jitNativeClock == 0 {
		v.jitNativeClock = 1
	}
	state.nativeUsed = v.jitNativeClock
	state.nativeHits++
}

func (v *VM) dropNative(state *quickJITState) {
	if !jitStateHasNative(state) {
		return
	}
	// R4-4: the native input-plan guards are released with the machine code;
	// fold their counters into the VM stats first so hits/rejections remain
	// explainable after the native tier is dropped or evicted.
	if v.jitConfig.Stats && state.program != nil {
		h, a, r, o, c := state.program.NativePlanPropertyPICStats()
		v.jitStats.PropertyPICHits += h
		v.jitStats.PropertyPICAdds += a
		v.jitStats.PropertyPICRejections += r
		v.jitStats.PropertyPICOverflows += o
		v.jitStats.PropertyPICCoolDowns += c
	}
	size := uint64(0)
	if state.program != nil && state.program.HasNative() {
		size += uint64(state.program.NativeSize())
		_ = state.program.Close()
	}
	if state.altProgram != nil && state.altProgram.HasNative() {
		size += uint64(state.altProgram.NativeSize())
		_ = state.altProgram.Close()
	}
	if size <= v.jitNativeBytes {
		v.jitNativeBytes -= size
	} else {
		v.jitNativeBytes = 0
	}
	state.nativeUsed = 0
}

func jitStateHasNative(state *quickJITState) bool {
	return state != nil && (state.program != nil && state.program.HasNative() ||
		state.altProgram != nil && state.altProgram.HasNative())
}

func (v *VM) dropNativeTrace(state *quickTraceState) {
	if state == nil || state.program == nil || !state.program.HasNative() {
		return
	}
	if v.jitConfig.Stats {
		h, a, r, o, c := state.program.NativePlanPropertyPICStats()
		v.jitStats.PropertyPICHits += h
		v.jitStats.PropertyPICAdds += a
		v.jitStats.PropertyPICRejections += r
		v.jitStats.PropertyPICOverflows += o
		v.jitStats.PropertyPICCoolDowns += c
	}
	size := uint64(state.program.NativeSize())
	_ = state.program.Close()
	if size <= v.jitNativeBytes {
		v.jitNativeBytes -= size
	} else {
		v.jitNativeBytes = 0
	}
	state.nativeUsed = 0
}

func (v *VM) tryQuickFrame(frame *vmFrame) (engine.Value, bool, error) {
	if v.insnsEnabled {
		return engine.Undefined(), false, nil
	}
	if v.jitPending > 0 {
		v.pollNativeCompiles()
	}
	state := frame.jitState
	if frame.jitGeneration != v.jitGeneration {
		state = v.jitStateFor(frame.tmpl)
		frame.jitState = state
		frame.jitGeneration = v.jitGeneration
	}
	if state != nil && state.rejected {
		return engine.Undefined(), false, nil
	}
	if state == nil || state.program == nil {
		state = v.noteJITBackedge(frame.tmpl)
		frame.jitState = state
		frame.jitGeneration = v.jitGeneration
	}
	if state == nil || state.program == nil || state.rejected {
		return engine.Undefined(), false, nil
	}
	if _, recursive := state.program.RequiresSelf(); recursive {
		return engine.Undefined(), false, nil
	}
	base := frame.base
	end := base + 1 + frame.tmpl.NumParams
	if base < 0 || end > len(v.stack) {
		return engine.Undefined(), false, nil
	}
	if v.jitConfig.Mode == jit.Auto && state.program.HasNative() {
		v.touchNative(state)
		result, reason, yields, err := state.program.ExecuteNativeBudgetWithSafepoint(
			v.stack[base], v.stack[base+1:end], v.jitConfig.TraceBudget, v.pollJITSafepoint)
		if v.jitConfig.Stats {
			v.jitStats.NativeYields += yields
		}
		if err != nil && reason == jit.Interrupted {
			return engine.Undefined(), false, err
		} else if err != nil {
			v.dropNative(state)
			if v.jitConfig.Stats {
				v.jitStats.Errors++
			}
		} else if reason == jit.Executed {
			if !v.verifyNativeResult(state.program, v.stack[base], v.stack[base+1:end], result) {
				v.dropNative(state)
				state.rejected = true
				v.noteAdaptiveFailure()
				return engine.Undefined(), false, nil
			}
			if v.jitConfig.Stats {
				v.jitStats.NativeExecuted++
				v.noteAdaptiveBenefit()
				if state.callKind == quickCallBound {
					v.jitStats.CalleeExecuted++
				}
			}
			resetJITGuardFailures(state)
			return result, true, nil
		} else if reason == jit.GuardFailed {
			v.noteNativeGuardFailure(state)
			if v.jitConfig.Stats {
				v.jitStats.GuardFailures++
			}
		}
	}
	result, reason, err := state.program.ExecuteWithSafepoint(
		v.stack[base], v.stack[base+1:end], v.jitConfig.TraceBudget, v.pollJITSafepoint)
	if err != nil {
		if reason == jit.Interrupted {
			return engine.Undefined(), false, err
		}
		state.rejected = true
		state.program = nil
		v.noteAdaptiveFailure()
		if v.jitConfig.Stats {
			v.jitStats.Errors++
		}
		return engine.Undefined(), false, nil
	}
	if reason == jit.Executed {
		resetQuickGuardFailures(state)
		if v.jitConfig.Stats {
			v.jitStats.Executed++
			v.noteAdaptiveBenefit()
		}
		return result, true, nil
	}
	if reason == jit.GuardFailed {
		v.noteJITGuardFailure(state)
		if v.jitConfig.Stats {
			v.jitStats.GuardFailures++
		}
	}
	return engine.Undefined(), false, nil
}

func (v *VM) traceNoopCallGuards(frame *vmFrame, startPC, backedgePC int) []jit.TraceCallGuard {
	if frame == nil || frame.tmpl == nil || frame.base < 0 {
		return nil
	}
	code := frame.tmpl.Code
	var guards []jit.TraceCallGuard
	for pc := startPC; pc <= backedgePC; pc += bytecode.InstrSize {
		if bytecode.Opcode(code[pc]) != bytecode.OpCall || pc-bytecode.InstrSize < startPC ||
			pc+bytecode.InstrSize > backedgePC {
			continue
		}
		arg := uint32(code[pc+1])<<16 | uint32(code[pc+2])<<8 | uint32(code[pc+3])
		if arg != 0 || bytecode.Opcode(code[pc-bytecode.InstrSize]) != bytecode.OpLoadLocal ||
			bytecode.Opcode(code[pc+bytecode.InstrSize]) != bytecode.OpPop {
			continue
		}
		loadPC := pc - bytecode.InstrSize
		slot := int(uint32(code[loadPC+1])<<16 | uint32(code[loadPC+2])<<8 | uint32(code[loadPC+3]))
		stackIndex := frame.base + slot
		if slot < 0 || slot >= frame.tmpl.NumLocals || stackIndex < 0 || stackIndex >= len(v.stack) {
			continue
		}
		target, ok := v.stack[stackIndex].(*vmClosure)
		if !ok || target.tmpl == nil {
			continue
		}
		program, err := jit.CompileLeaf(target.tmpl)
		if err != nil || !program.ReturnsUndefined() {
			continue
		}
		guards = append(guards, jit.TraceCallGuard{PC: pc, SourceLocal: slot, Target: target})
	}
	return guards
}

func (v *VM) traceMethodGuards(frame *vmFrame, startPC, backedgePC int) []jit.TraceMethodGuard {
	if frame == nil || frame.tmpl == nil || frame.base < 0 {
		return nil
	}
	code := frame.tmpl.Code
	var guards []jit.TraceMethodGuard
	for pc := startPC; pc <= backedgePC; pc += bytecode.InstrSize {
		if bytecode.Opcode(code[pc]) != bytecode.OpCallMethod || pc-bytecode.InstrSize < startPC {
			continue
		}
		arg := uint32(code[pc+1])<<16 | uint32(code[pc+2])<<8 | uint32(code[pc+3])
		if arg>>16 != 0 {
			continue
		}
		nameIndex := int(arg & 0xFFFF)
		if nameIndex < 0 || nameIndex >= len(frame.tmpl.Constants) || frame.tmpl.Constants[nameIndex].Type() != engine.TypeString ||
			bytecode.Opcode(code[pc-bytecode.InstrSize]) != bytecode.OpLoadLocal {
			continue
		}
		loadPC := pc - bytecode.InstrSize
		slot := int(uint32(code[loadPC+1])<<16 | uint32(code[loadPC+2])<<8 | uint32(code[loadPC+3]))
		stackIndex := frame.base + slot
		if slot < 0 || slot >= frame.tmpl.NumLocals || stackIndex < 0 || stackIndex >= len(v.stack) {
			continue
		}
		receiver := v.stack[stackIndex]
		object, ok := receiver.AsObject()
		if !ok {
			continue
		}
		methodValue, ok := engine.GuardedMethodLookup(object, frame.tmpl.Constants[nameIndex].String())
		if !ok {
			continue
		}
		target, ok := methodValue.(*vmClosure)
		if !ok || target.tmpl == nil {
			continue
		}
		program, err := jit.CompileLeaf(target.tmpl)
		if err != nil || !program.IsTrivialThisPropertyGetter() {
			continue
		}
		property := program.Code[1].Name
		guards = append(guards, jit.TraceMethodGuard{
			PC: pc, SourceLocal: slot, Target: target,
			Method: frame.tmpl.Constants[nameIndex].String(), Property: property,
		})
	}
	return guards
}

func jitTraceOperand(code []byte, pc int) uint32 {
	return uint32(code[pc+1])<<16 | uint32(code[pc+2])<<8 | uint32(code[pc+3])
}

// matchArrayPushTrace recognizes only the compiler's canonical form for:
//
//	for (; i < bound; i++) array.push(i)
//
// Keeping the matcher exact makes the bulk execution below equivalent to the
// bytecode it replaces and leaves all other calls on the normal deopt path.
func (v *VM) matchArrayPushTrace(frame *vmFrame, startPC, backedgePC int) *arrayPushTraceState {
	if frame == nil || frame.tmpl == nil || frame.base < 0 {
		return nil
	}
	code := frame.tmpl.Code
	const instructionCount = 14
	if startPC < 0 || backedgePC != startPC+(instructionCount-1)*bytecode.InstrSize ||
		backedgePC+bytecode.InstrSize > len(code) {
		return nil
	}
	op := func(index int) bytecode.Opcode { return bytecode.Opcode(code[startPC+index*bytecode.InstrSize]) }
	arg := func(index int) uint32 { return jitTraceOperand(code, startPC+index*bytecode.InstrSize) }
	if op(0) != bytecode.OpLoadLocal || op(2) != bytecode.OpLt || op(3) != bytecode.OpJmpFalsePop ||
		op(4) != bytecode.OpLoadLocal || op(5) != bytecode.OpLoadLocal || op(6) != bytecode.OpCallMethod ||
		op(7) != bytecode.OpPop || op(8) != bytecode.OpLoadLocal || op(9) != bytecode.OpDup ||
		op(10) != bytecode.OpInc || op(11) != bytecode.OpStoreLocal ||
		op(12) != bytecode.OpPop || op(13) != bytecode.OpJmp {
		return nil
	}
	indexLocal := int(arg(0))
	receiverLocal := int(arg(4))
	if int(arg(5)) != indexLocal || int(arg(8)) != indexLocal || int(arg(11)) != indexLocal ||
		indexLocal < 0 || indexLocal >= frame.tmpl.NumLocals || receiverLocal < 0 || receiverLocal >= frame.tmpl.NumLocals ||
		indexLocal == receiverLocal {
		return nil
	}
	callArg := arg(6)
	nameIndex := int(callArg & 0xFFFF)
	if callArg>>16 != 1 || nameIndex < 0 || nameIndex >= len(frame.tmpl.Constants) ||
		frame.tmpl.Constants[nameIndex].Type() != engine.TypeString || frame.tmpl.Constants[nameIndex].String() != "push" {
		return nil
	}
	backedgeTarget := backedgePC + bytecode.InstrSize + bytecode.SignedOperand(arg(13))
	exitPC := startPC + 4*bytecode.InstrSize + bytecode.SignedOperand(arg(3))
	if backedgeTarget != startPC || exitPC <= backedgePC || exitPC > len(code) {
		return nil
	}

	trace := &arrayPushTraceState{
		receiverLocal: receiverLocal,
		indexLocal:    indexLocal,
		boundLocal:    -1,
		startPC:       startPC,
		exitPC:        exitPC,
	}
	switch op(1) {
	case bytecode.OpLoadLocal:
		trace.boundLocal = int(arg(1))
		trace.boundIsLocal = true
		if trace.boundLocal < 0 || trace.boundLocal >= frame.tmpl.NumLocals ||
			trace.boundLocal == indexLocal || trace.boundLocal == receiverLocal {
			return nil
		}
	case bytecode.OpPushInt:
		trace.boundConst = float64(arg(1))
	case bytecode.OpPushNegInt:
		trace.boundConst = -float64(arg(1))
	case bytecode.OpPushConst:
		constantIndex := int(arg(1))
		if constantIndex < 0 || constantIndex >= len(frame.tmpl.Constants) ||
			frame.tmpl.Constants[constantIndex].Type() != engine.TypeNumber {
			return nil
		}
		trace.boundConst, _ = frame.tmpl.Constants[constantIndex].Float()
	default:
		return nil
	}

	localsEnd := frame.base + frame.tmpl.NumLocals
	if localsEnd > len(v.stack) {
		return nil
	}
	locals := v.stack[frame.base:localsEnd]
	receiver, ok := locals[receiverLocal].(*engine.ArrayValue)
	if !ok {
		return nil
	}
	pushTarget, err := v.interp.arrayProto.Get("push")
	if err != nil || pushTarget == nil {
		return nil
	}
	currentMethod, err := receiver.Get("push")
	if err != nil || currentMethod != pushTarget {
		return nil
	}
	trace.pushTarget = pushTarget
	if _, _, ok := trace.arrayPushNumbers(locals); !ok {
		return nil
	}
	return trace
}

func (t *arrayPushTraceState) arrayPushNumbers(locals []engine.Value) (float64, float64, bool) {
	if t == nil || t.indexLocal < 0 || t.indexLocal >= len(locals) {
		return 0, 0, false
	}
	index, ok := locals[t.indexLocal].Float()
	if !ok {
		return 0, 0, false
	}
	bound := t.boundConst
	if t.boundIsLocal {
		if t.boundLocal < 0 || t.boundLocal >= len(locals) {
			return 0, 0, false
		}
		bound, ok = locals[t.boundLocal].Float()
		if !ok {
			return 0, 0, false
		}
	}
	const maxSafeInteger = float64(1<<53 - 1)
	if math.IsNaN(index) || math.IsInf(index, 0) || math.Trunc(index) != index || index < 0 || index > maxSafeInteger ||
		math.IsNaN(bound) || math.IsInf(bound, 0) || math.Trunc(bound) != bound || bound < 0 || bound > maxSafeInteger {
		return 0, 0, false
	}
	return index, bound, true
}

func (v *VM) executeArrayPushTrace(trace *arrayPushTraceState, locals []engine.Value) (int, jit.ExitReason, error) {
	if trace == nil || trace.receiverLocal < 0 || trace.receiverLocal >= len(locals) {
		return 0, jit.GuardFailed, nil
	}
	receiver, ok := locals[trace.receiverLocal].(*engine.ArrayValue)
	if !ok {
		return 0, jit.GuardFailed, nil
	}
	method, err := receiver.Get("push")
	if err != nil || method != trace.pushTarget {
		return 0, jit.GuardFailed, nil
	}
	index, bound, ok := trace.arrayPushNumbers(locals)
	if !ok {
		return 0, jit.GuardFailed, nil
	}
	if index >= bound {
		return trace.exitPC, jit.Executed, nil
	}
	remaining := int(bound - index)
	budget := int(v.jitConfig.TraceBudget)
	if budget <= 0 {
		budget = 65536
	}
	count := remaining
	if count > budget {
		count = budget
	}
	receiver.AppendNumberRange(index, count)
	locals[trace.indexLocal] = engine.Number(index + float64(count))
	if count >= budget {
		if err := v.pollJITSafepoint(); err != nil {
			return trace.startPC, jit.Interrupted, err
		}
		return trace.startPC, jit.Yielded, nil
	}
	return trace.exitPC, jit.Executed, nil
}

// --- R4-2: numeric-upvalue closure plans -----------------------------------
//
// closurePlan is the parsed body of a numeric-upvalue closure. The grammar is
// exactly what the bytecode compiler emits for numeric upvalue read/write
// statements; anything else (locals, this, method calls, conditionals,
// assignments of arbitrary expressions) is rejected and the closure stays on
// Tier 0. Evaluating the plan against a float64 cache of the captured
// upvalues is bit-identical to executing the closure body, because every
// statement reads and writes the cache in order and the arithmetic ops mirror
// Tier 0's Number semantics (plain IEEE-754 add/sub/mul/div, math.Pow,
// math.Mod with NaN for `% 0`).

type closureExprKind uint8

const (
	closureExprUpvalue closureExprKind = iota
	closureExprConst
	closureExprBin
)

type closureExpr struct {
	kind  closureExprKind
	slot  int             // closureExprUpvalue: captured upvalue index
	value float64         // closureExprConst: constant
	op    bytecode.Opcode // closureExprBin: ADD SUB MUL DIV MOD POW
	left  *closureExpr
	right *closureExpr
}

type closureWrite struct {
	slot int
	expr closureExpr
}

type closurePlan struct {
	writes   []closureWrite
	result   closureExpr
	readOnly bool // no writes: safe without any write-back
	// resultFirst marks single-expression postfix bodies (`() => n++`):
	// the returned value is captured before the write, while every other
	// shape returns the expression evaluated after all writes.
	resultFirst bool
}

// matchNumericUpvalueClosure parses the callee template's body into a
// closurePlan, or returns false when the body is not one of the supported
// numeric upvalue shapes. It validates the template shape (no params, no
// async/generator/varargs, no locals, one capture per upvalue) and the
// concrete bytecode; runtime value checks (numeric upvalues, identity) stay
// in the executor.
func matchNumericUpvalueClosure(target *vmClosure) (*closurePlan, bool) {
	if target == nil || target.tmpl == nil || target.tmpl.IsAsync || target.tmpl.IsGenerator ||
		target.tmpl.IsVarArgs || target.tmpl.NumParams != 0 || target.tmpl.NumLocals != 1 {
		return nil, false
	}
	numUpvalues := len(target.tmpl.Upvalues)
	if numUpvalues == 0 || numUpvalues != len(target.upvalues) {
		return nil, false
	}
	code := target.tmpl.Code
	end := len(code)
	if end >= bytecode.InstrSize && bytecode.Opcode(code[end-bytecode.InstrSize]) == bytecode.OpReturnUndef {
		end -= bytecode.InstrSize
	}
	if end == 0 {
		return nil, false
	}
	plan := &closurePlan{}
	pc := 0
	for pc < end {
		op := bytecode.Opcode(code[pc])
		arg := uint32(code[pc+1])<<16 | uint32(code[pc+2])<<8 | uint32(code[pc+3])
		if op == bytecode.OpReturn {
			return nil, false // RETURN must close an expression statement
		}
		// `a++` / `a--` statements compile to
		// LOAD_UPVALUE(i) DUP INC|DEC STORE_UPVALUE(i) POP.
		if pc+5*bytecode.InstrSize <= end && op == bytecode.OpLoadUpvalue {
			op1 := bytecode.Opcode(code[pc+1*bytecode.InstrSize])
			op2 := bytecode.Opcode(code[pc+2*bytecode.InstrSize])
			op3 := bytecode.Opcode(code[pc+3*bytecode.InstrSize])
			op4 := bytecode.Opcode(code[pc+4*bytecode.InstrSize])
			arg1 := uint32(code[pc+3*bytecode.InstrSize+1])<<16 | uint32(code[pc+3*bytecode.InstrSize+2])<<8 | uint32(code[pc+3*bytecode.InstrSize+3])
			if op1 == bytecode.OpDup && (op2 == bytecode.OpInc || op2 == bytecode.OpDec) &&
				op3 == bytecode.OpStoreUpvalue && int(arg1) == int(arg) && op4 == bytecode.OpPop {
				delta := float64(1)
				if op2 == bytecode.OpDec {
					delta = -1
				}
				plan.writes = append(plan.writes, closureWrite{
					slot: int(arg),
					expr: closureExpr{kind: closureExprBin, op: bytecode.OpAdd,
						left:  &closureExpr{kind: closureExprUpvalue, slot: int(arg)},
						right: &closureExpr{kind: closureExprConst, value: delta}},
				})
				pc += 5 * bytecode.InstrSize
				continue
			}
			// Single-expression bodies `() => ++n` / `() => n++` compile to
			// LOAD_UPVALUE(i) INC|DEC DUP STORE_UPVALUE(i) RETURN (prefix:
			// returns the new value) and LOAD_UPVALUE(i) DUP INC|DEC
			// STORE_UPVALUE(i) RETURN (postfix: returns the old value).
			prefix := (op1 == bytecode.OpInc || op1 == bytecode.OpDec) &&
				op2 == bytecode.OpDup && op3 == bytecode.OpStoreUpvalue &&
				int(arg1) == int(arg) && op4 == bytecode.OpReturn
			postfix := op1 == bytecode.OpDup && (op2 == bytecode.OpInc || op2 == bytecode.OpDec) &&
				op3 == bytecode.OpStoreUpvalue && int(arg1) == int(arg) && op4 == bytecode.OpReturn
			if (prefix || postfix) && pc+5*bytecode.InstrSize == end {
				delta := float64(1)
				if (prefix && op1 == bytecode.OpDec) || (postfix && op2 == bytecode.OpDec) {
					delta = -1
				}
				plan.writes = append(plan.writes, closureWrite{
					slot: int(arg),
					expr: closureExpr{kind: closureExprBin, op: bytecode.OpAdd,
						left:  &closureExpr{kind: closureExprUpvalue, slot: int(arg)},
						right: &closureExpr{kind: closureExprConst, value: delta}},
				})
				// Prefix returns the new value: the upvalue read evaluates
				// after the write. Postfix returns the old value: capture it
				// before the write (resultFirst).
				plan.result = closureExpr{kind: closureExprUpvalue, slot: int(arg)}
				plan.resultFirst = postfix
				pc = end
				break
			}
		}
		// General statement: <expr> DUP STORE_UPVALUE(i) POP (write) or
		// <expr> RETURN (final return; a body of a single expression is the
		// read-only capture shape `() => a + b`).
		expr, used, ok := parseClosureExpr(code, pc, end, target.tmpl, numUpvalues)
		if !ok {
			return nil, false
		}
		next := pc + used
		if next < end && bytecode.Opcode(code[next]) == bytecode.OpReturn && next+bytecode.InstrSize == end {
			plan.result = *expr
			pc = end
			break
		}
		if next+3*bytecode.InstrSize > end {
			return nil, false
		}
		op1 := bytecode.Opcode(code[next])
		arg1 := uint32(code[next+bytecode.InstrSize+1])<<16 | uint32(code[next+bytecode.InstrSize+2])<<8 | uint32(code[next+bytecode.InstrSize+3])
		op2 := bytecode.Opcode(code[next+bytecode.InstrSize])
		op3 := bytecode.Opcode(code[next+2*bytecode.InstrSize])
		if op1 != bytecode.OpDup || op2 != bytecode.OpStoreUpvalue || int(arg1) >= numUpvalues || op3 != bytecode.OpPop {
			return nil, false
		}
		plan.writes = append(plan.writes, closureWrite{slot: int(arg1), expr: *expr})
		pc = next + 3*bytecode.InstrSize
	}
	if pc != end {
		return nil, false
	}
	plan.readOnly = len(plan.writes) == 0
	return plan, true
}

// parseClosureExpr parses the expression starting at pc (within [0, limit))
// into a closureExpr tree. The bytecode compiler emits postfix binary
// expressions (atoms pushed left-to-right, each BINOP combining the top two),
// so a stack machine over atoms reproduces the exact evaluation order. An
// atom is a captured upvalue or a Number constant (OpPushConst resolves
// through the template constant pool; String constants reject the shape).
// Returns the expression, the instruction count consumed and ok.
func parseClosureExpr(code []byte, pc, limit int, tmpl *bytecode.FuncTemplate, numUpvalues int) (*closureExpr, int, bool) {
	if pc < 0 || limit <= pc || limit > len(code) || limit%bytecode.InstrSize != 0 || pc%bytecode.InstrSize != 0 {
		return nil, 0, false
	}
	stack := make([]closureExpr, 0, 4)
	used := pc
	for used < limit {
		op := bytecode.Opcode(code[used])
		arg := uint32(code[used+1])<<16 | uint32(code[used+2])<<8 | uint32(code[used+3])
		switch op {
		case bytecode.OpLoadUpvalue:
			if int(arg) >= numUpvalues {
				return nil, 0, false
			}
			stack = append(stack, closureExpr{kind: closureExprUpvalue, slot: int(arg)})
			used += bytecode.InstrSize
		case bytecode.OpPushInt:
			stack = append(stack, closureExpr{kind: closureExprConst, value: float64(arg)})
			used += bytecode.InstrSize
		case bytecode.OpPushNegInt:
			stack = append(stack, closureExpr{kind: closureExprConst, value: -float64(arg)})
			used += bytecode.InstrSize
		case bytecode.OpPushConst:
			if int(arg) >= len(tmpl.Constants) || tmpl.Constants[arg].Type() != engine.TypeNumber {
				return nil, 0, false
			}
			n, _ := tmpl.Constants[arg].Float()
			stack = append(stack, closureExpr{kind: closureExprConst, value: n})
			used += bytecode.InstrSize
		case bytecode.OpAdd, bytecode.OpSub, bytecode.OpMul, bytecode.OpDiv, bytecode.OpMod, bytecode.OpPow:
			if len(stack) < 2 {
				return nil, 0, false
			}
			right := stack[len(stack)-1]
			left := stack[len(stack)-2]
			stack = stack[:len(stack)-2]
			stack = append(stack, closureExpr{kind: closureExprBin, op: op, left: &left, right: &right})
			used += bytecode.InstrSize
		default:
			goto done
		}
	}
done:
	if len(stack) != 1 {
		return nil, 0, false
	}
	result := stack[0]
	return &result, used - pc, true
}

// closureTraceUpvalueAliased reports whether any captured upvalue of the
// callee aliases a local that the traced loop itself reads or writes
// (calleeLocal, indexLocal, boundLocal, sumLocal). Such an open upvalue
// would be observed mid-slice by the batch executor, which caches upvalue
// values at entry and writes them back once at the commit point; Tier 0
// instead reads the evolving local every iteration. Aliasing any other frame
// local is safe: the trace slice never touches it, and the single write-back
// reproduces the final state exactly.
func closureTraceUpvalueAliased(trace *closureIncrementTraceState, locals []engine.Value) bool {
	if trace == nil {
		return false
	}
	for _, uv := range trace.upvalues {
		if uv == nil || uv.slot == nil {
			continue // closed upvalue: no alias with the current frame
		}
		for _, slot := range []int{trace.calleeLocal, trace.indexLocal, trace.boundLocal, trace.sumLocal} {
			if slot >= 0 && slot < len(locals) && uv.slot == &locals[slot] {
				return true
			}
		}
	}
	return false
}

// matchClosureIncrementTrace recognizes the benchmark-critical form:
//
//	for (; i < bound; i++) sum += incrementClosure()
//
// where incrementClosure is exactly () => ++numericUpvalue.
func (v *VM) matchClosureIncrementTrace(frame *vmFrame, startPC, backedgePC int) *closureIncrementTraceState {
	if frame == nil || frame.tmpl == nil || frame.base < 0 {
		return nil
	}
	code := frame.tmpl.Code
	const instructionCount = 17
	if startPC < 0 || backedgePC != startPC+(instructionCount-1)*bytecode.InstrSize ||
		backedgePC+bytecode.InstrSize > len(code) {
		return nil
	}
	op := func(index int) bytecode.Opcode { return bytecode.Opcode(code[startPC+index*bytecode.InstrSize]) }
	arg := func(index int) uint32 { return jitTraceOperand(code, startPC+index*bytecode.InstrSize) }
	if op(0) != bytecode.OpLoadLocal || op(1) != bytecode.OpLoadLocal || op(2) != bytecode.OpLt ||
		op(3) != bytecode.OpJmpFalsePop || op(4) != bytecode.OpLoadLocal || op(5) != bytecode.OpLoadLocal ||
		op(6) != bytecode.OpCall || arg(6) != 0 || op(7) != bytecode.OpAdd || op(8) != bytecode.OpDup ||
		op(9) != bytecode.OpStoreLocal || op(10) != bytecode.OpPop || op(11) != bytecode.OpLoadLocal ||
		op(12) != bytecode.OpDup || op(13) != bytecode.OpInc ||
		op(14) != bytecode.OpStoreLocal || op(15) != bytecode.OpPop ||
		op(16) != bytecode.OpJmp {
		return nil
	}
	indexLocal := int(arg(0))
	boundLocal := int(arg(1))
	sumLocal := int(arg(4))
	calleeLocal := int(arg(5))
	if int(arg(9)) != sumLocal || int(arg(11)) != indexLocal || int(arg(14)) != indexLocal {
		return nil
	}
	localCount := frame.tmpl.NumLocals
	for _, slot := range []int{indexLocal, boundLocal, sumLocal, calleeLocal} {
		if slot < 0 || slot >= localCount {
			return nil
		}
	}
	if indexLocal == boundLocal || indexLocal == sumLocal || indexLocal == calleeLocal ||
		boundLocal == sumLocal || boundLocal == calleeLocal || sumLocal == calleeLocal {
		return nil
	}
	backedgeTarget := backedgePC + bytecode.InstrSize + bytecode.SignedOperand(arg(16))
	exitPC := startPC + 4*bytecode.InstrSize + bytecode.SignedOperand(arg(3))
	if backedgeTarget != startPC || exitPC <= backedgePC || exitPC > len(code) {
		return nil
	}
	localsEnd := frame.base + localCount
	if localsEnd > len(v.stack) {
		return nil
	}
	locals := v.stack[frame.base:localsEnd]
	target, ok := locals[calleeLocal].(*vmClosure)
	if !ok || target.vm != v {
		return nil
	}
	plan, ok := matchNumericUpvalueClosure(target)
	if !ok {
		return nil
	}
	trace := &closureIncrementTraceState{
		calleeLocal: calleeLocal, indexLocal: indexLocal, boundLocal: boundLocal,
		sumLocal: sumLocal, target: target, plan: plan,
		upvalues: target.upvalues, startPC: startPC, exitPC: exitPC,
	}
	if closureTraceUpvalueAliased(trace, locals) {
		return nil
	}
	if _, _, _, _, ok := trace.closureLoopNumbers(locals); !ok {
		return nil
	}
	return trace
}

func (t *closureIncrementTraceState) closureLoopNumbers(locals []engine.Value) (float64, float64, float64, []float64, bool) {
	if t == nil || t.target == nil || t.plan == nil || len(t.upvalues) == 0 ||
		t.indexLocal < 0 || t.indexLocal >= len(locals) ||
		t.boundLocal < 0 || t.boundLocal >= len(locals) || t.sumLocal < 0 || t.sumLocal >= len(locals) {
		return 0, 0, 0, nil, false
	}
	index, indexOK := locals[t.indexLocal].Float()
	bound, boundOK := locals[t.boundLocal].Float()
	sum, sumOK := locals[t.sumLocal].Float()
	if !indexOK || !boundOK || !sumOK {
		return 0, 0, 0, nil, false
	}
	values := make([]float64, len(t.upvalues))
	for i := range t.upvalues {
		upvalueValue, upvalueOK := closureUpvalue(t.target, i)
		if !upvalueOK || upvalueValue == nil || upvalueValue.Type() != engine.TypeNumber {
			return 0, 0, 0, nil, false
		}
		current, _ := upvalueValue.Float()
		if math.IsNaN(current) || math.IsInf(current, 0) {
			return 0, 0, 0, nil, false
		}
		values[i] = current
	}
	const maxSafeInteger = float64(1<<53 - 1)
	if math.IsNaN(index) || math.IsInf(index, 0) || math.Trunc(index) != index || index < 0 || index > maxSafeInteger ||
		math.IsNaN(bound) || math.IsInf(bound, 0) || math.Trunc(bound) != bound || bound < 0 || bound > maxSafeInteger ||
		math.IsNaN(sum) || math.IsInf(sum, 0) {
		return 0, 0, 0, nil, false
	}
	return index, bound, sum, values, true
}

// evalClosureExpr evaluates a closureExpr against the upvalue value cache.
// The arithmetic mirrors Tier 0's Number semantics exactly (IEEE-754
// add/sub/mul/div, math.Pow, math.Mod with NaN for a zero divisor), so the
// batch evaluation is bit-identical to executing the closure body.
func evalClosureExpr(e *closureExpr, values []float64) float64 {
	if e == nil {
		return math.NaN()
	}
	switch e.kind {
	case closureExprConst:
		return e.value
	case closureExprUpvalue:
		if e.slot >= 0 && e.slot < len(values) {
			return values[e.slot]
		}
		return math.NaN()
	case closureExprBin:
		left := evalClosureExpr(e.left, values)
		right := evalClosureExpr(e.right, values)
		switch e.op {
		case bytecode.OpAdd:
			return left + right
		case bytecode.OpSub:
			return left - right
		case bytecode.OpMul:
			return left * right
		case bytecode.OpDiv:
			return left / right
		case bytecode.OpMod:
			// Tier 0 OpMod: math.Mod, NaN when the divisor is zero.
			return math.Mod(left, right)
		case bytecode.OpPow:
			return math.Pow(left, right)
		default:
			return math.NaN()
		}
	default:
		return math.NaN()
	}
}

func (v *VM) executeClosureIncrementTrace(trace *closureIncrementTraceState, locals []engine.Value) (int, jit.ExitReason, error) {
	if trace == nil || trace.calleeLocal < 0 || trace.calleeLocal >= len(locals) ||
		locals[trace.calleeLocal] != trace.target || trace.plan == nil {
		return 0, jit.GuardFailed, nil
	}
	// Callee identity + captured upvalue identity: the plan binds to the
	// concrete captured cells, so a different closure instance of the same
	// template (or an upvalue cell that was replaced) must fall back.
	if len(trace.target.upvalues) != len(trace.upvalues) {
		return 0, jit.GuardFailed, nil
	}
	for i := range trace.upvalues {
		if trace.target.upvalues[i] != trace.upvalues[i] {
			return 0, jit.GuardFailed, nil
		}
	}
	if closureTraceUpvalueAliased(trace, locals) {
		return 0, jit.GuardFailed, nil
	}
	index, bound, sum, values, ok := trace.closureLoopNumbers(locals)
	if !ok {
		return 0, jit.GuardFailed, nil
	}
	if index >= bound {
		return trace.exitPC, jit.Executed, nil
	}
	remaining := int(bound - index)
	budget := int(v.jitConfig.TraceBudget)
	if budget <= 0 {
		budget = 65536
	}
	count := remaining
	if count > budget {
		count = budget
	}
	for i := 0; i < count; i++ {
		if trace.plan.resultFirst {
			// `() => n++`: the return value is the pre-write capture.
			sum += evalClosureExpr(&trace.plan.result, values)
		}
		for w := range trace.plan.writes {
			values[trace.plan.writes[w].slot] = evalClosureExpr(&trace.plan.writes[w].expr, values)
		}
		if !trace.plan.resultFirst {
			sum += evalClosureExpr(&trace.plan.result, values)
		}
	}
	// Atomic commit: every written upvalue is stored once, then the loop
	// locals. A read-only plan writes nothing back.
	for w := range trace.plan.writes {
		if !storeClosureUpvalue(trace.target, trace.plan.writes[w].slot, engine.Number(values[trace.plan.writes[w].slot])) {
			return 0, jit.GuardFailed, nil
		}
	}
	locals[trace.sumLocal] = engine.Number(sum)
	locals[trace.indexLocal] = engine.Number(index + float64(count))
	if count >= budget {
		if err := v.pollJITSafepoint(); err != nil {
			return trace.startPC, jit.Interrupted, err
		}
		return trace.startPC, jit.Yielded, nil
	}
	return trace.exitPC, jit.Executed, nil
}

// traceBoundOperand parses the loop bound operand at instruction index
// boundIndex of the canonical loop head (LoadLocal bound / PushInt /
// PushNegInt / PushConst Number). Returns (boundLocal, boundConst, isLocal,
// ok); on ok == false the shape is not a supported range loop.
func traceBoundOperand(tmpl *bytecode.FuncTemplate, op func(int) bytecode.Opcode, arg func(int) uint32, boundIndex, excludedLocal int) (int, float64, bool, bool) {
	switch op(boundIndex) {
	case bytecode.OpLoadLocal:
		boundLocal := int(arg(boundIndex))
		if boundLocal < 0 || boundLocal >= tmpl.NumLocals || boundLocal == excludedLocal {
			return 0, 0, false, false
		}
		return boundLocal, 0, true, true
	case bytecode.OpPushInt:
		return -1, float64(arg(boundIndex)), false, true
	case bytecode.OpPushNegInt:
		return -1, -float64(arg(boundIndex)), false, true
	case bytecode.OpPushConst:
		constantIndex := int(arg(boundIndex))
		if constantIndex < 0 || constantIndex >= len(tmpl.Constants) ||
			tmpl.Constants[constantIndex].Type() != engine.TypeNumber {
			return 0, 0, false, false
		}
		boundConst, _ := tmpl.Constants[constantIndex].Float()
		return -1, boundConst, false, true
	default:
		return 0, 0, false, false
	}
}

// matchArrayIndexTrace recognizes the compiler's canonical R4-5 packed Number
// read form:
//
//	for (; i < bound; i++) sum += array[i]
//
//	0 LoadLocal i     4 LoadLocal sum   8 Add     12 LoadLocal i
//	1 <bound>         5 LoadLocal array 9 Dup     13 Dup
//	2 Lt              6 LoadLocal i    10 Store   14 Inc
//	3 JmpFalsePop     7 GetElem        11 Pop     15 StoreLocal i
//	                                             16 Pop
//	                                             17 Jmp back
//
// The matcher is exact so the bulk execution below is equivalent to the
// bytecode it replaces; every other read shape (prototype index, hole or
// mixed-type elements, Proxy receiver, unsafe numbers) stays on the Tier 0
// path.
func (v *VM) matchArrayIndexTrace(frame *vmFrame, startPC, backedgePC int) *arrayIndexTraceState {
	if frame == nil || frame.tmpl == nil || frame.base < 0 {
		return nil
	}
	code := frame.tmpl.Code
	const instructionCount = 18
	if startPC < 0 || backedgePC != startPC+(instructionCount-1)*bytecode.InstrSize ||
		backedgePC+bytecode.InstrSize > len(code) {
		return nil
	}
	op := func(index int) bytecode.Opcode { return bytecode.Opcode(code[startPC+index*bytecode.InstrSize]) }
	arg := func(index int) uint32 { return jitTraceOperand(code, startPC+index*bytecode.InstrSize) }
	if op(0) != bytecode.OpLoadLocal || op(2) != bytecode.OpLt || op(3) != bytecode.OpJmpFalsePop ||
		op(4) != bytecode.OpLoadLocal || op(5) != bytecode.OpLoadLocal || op(6) != bytecode.OpLoadLocal ||
		op(7) != bytecode.OpGetElem || op(8) != bytecode.OpAdd || op(9) != bytecode.OpDup ||
		op(10) != bytecode.OpStoreLocal || op(11) != bytecode.OpPop || op(12) != bytecode.OpLoadLocal ||
		op(13) != bytecode.OpDup || op(14) != bytecode.OpInc || op(15) != bytecode.OpStoreLocal ||
		op(16) != bytecode.OpPop || op(17) != bytecode.OpJmp {
		return nil
	}
	indexLocal := int(arg(0))
	sumLocal := int(arg(4))
	arrayLocal := int(arg(5))
	if int(arg(6)) != indexLocal || int(arg(12)) != indexLocal || int(arg(15)) != indexLocal ||
		int(arg(10)) != sumLocal {
		return nil
	}
	localCount := frame.tmpl.NumLocals
	for _, slot := range []int{indexLocal, sumLocal, arrayLocal} {
		if slot < 0 || slot >= localCount {
			return nil
		}
	}
	if indexLocal == sumLocal || indexLocal == arrayLocal || sumLocal == arrayLocal {
		return nil
	}
	boundLocal, boundConst, boundIsLocal, ok := traceBoundOperand(frame.tmpl, op, arg, 1, indexLocal)
	if !ok {
		return nil
	}
	// The bound must not alias the sum local: the body stores sum every
	// iteration, so a bound that is also the sum would change mid-loop and
	// make the chunked range diverge from Tier 0.
	if boundIsLocal && boundLocal == sumLocal {
		return nil
	}
	backedgeTarget := backedgePC + bytecode.InstrSize + bytecode.SignedOperand(arg(17))
	exitPC := startPC + 4*bytecode.InstrSize + bytecode.SignedOperand(arg(3))
	if backedgeTarget != startPC || exitPC <= backedgePC || exitPC > len(code) {
		return nil
	}
	localsEnd := frame.base + localCount
	if localsEnd > len(v.stack) {
		return nil
	}
	locals := v.stack[frame.base:localsEnd]
	if _, ok := locals[arrayLocal].(*engine.ArrayValue); !ok {
		return nil
	}
	trace := &arrayIndexTraceState{
		arrayLocal: arrayLocal, sumLocal: sumLocal, indexLocal: indexLocal,
		boundLocal: boundLocal, boundConst: boundConst, boundIsLocal: boundIsLocal,
		startPC: startPC, exitPC: exitPC,
	}
	if _, _, _, ok := trace.arrayIndexNumbers(locals); !ok {
		return nil
	}
	return trace
}

func (t *arrayIndexTraceState) arrayIndexNumbers(locals []engine.Value) (float64, float64, float64, bool) {
	if t == nil || t.indexLocal < 0 || t.indexLocal >= len(locals) ||
		t.sumLocal < 0 || t.sumLocal >= len(locals) {
		return 0, 0, 0, false
	}
	index, ok := locals[t.indexLocal].Float()
	if !ok {
		return 0, 0, 0, false
	}
	bound := t.boundConst
	if t.boundIsLocal {
		if t.boundLocal < 0 || t.boundLocal >= len(locals) {
			return 0, 0, 0, false
		}
		bound, ok = locals[t.boundLocal].Float()
		if !ok {
			return 0, 0, 0, false
		}
	}
	sum, ok := locals[t.sumLocal].Float()
	if !ok {
		return 0, 0, 0, false
	}
	const maxSafeInteger = float64(1<<53 - 1)
	if math.IsNaN(index) || math.IsInf(index, 0) || math.Trunc(index) != index || index < 0 || index > maxSafeInteger ||
		math.IsNaN(bound) || math.IsInf(bound, 0) || math.Trunc(bound) != bound || bound < 0 || bound > maxSafeInteger ||
		math.IsNaN(sum) || math.IsInf(sum, 0) {
		return 0, 0, 0, false
	}
	return index, bound, sum, true
}

// executeArrayIndexTrace bulk-executes the packed Number read chunk. The
// length guard clamps the chunk to the current element storage (an index at
// or above len resolves through the prototype chain in Tier 0 and must fall
// back); a non-Number element in the range fails the whole chunk before any
// local is touched. The sum and index locals are updated atomically per
// committed chunk.
func (v *VM) executeArrayIndexTrace(trace *arrayIndexTraceState, locals []engine.Value) (int, jit.ExitReason, error) {
	if trace == nil || trace.arrayLocal < 0 || trace.arrayLocal >= len(locals) {
		return 0, jit.GuardFailed, nil
	}
	receiver, ok := locals[trace.arrayLocal].(*engine.ArrayValue)
	if !ok {
		return 0, jit.GuardFailed, nil
	}
	index, bound, sum, ok := trace.arrayIndexNumbers(locals)
	if !ok {
		return 0, jit.GuardFailed, nil
	}
	if index >= bound {
		return trace.exitPC, jit.Executed, nil
	}
	remaining := int(bound - index)
	budget := int(v.jitConfig.TraceBudget)
	if budget <= 0 {
		budget = 65536
	}
	count := remaining
	if count > budget {
		count = budget
	}
	elems := receiver.Elems()
	if index >= float64(len(elems)) {
		return 0, jit.GuardFailed, nil
	}
	if maxCount := len(elems) - int(index); count > maxCount {
		count = maxCount
	}
	start := int(index)
	for i := 0; i < count; i++ {
		element := elems[start+i]
		if element == nil || element.Type() != engine.TypeNumber {
			return 0, jit.GuardFailed, nil
		}
		number, _ := element.Float()
		sum += number
	}
	locals[trace.sumLocal] = engine.Number(sum)
	locals[trace.indexLocal] = engine.Number(index + float64(count))
	// A chunk clamped by the element storage (index+count < bound) means the
	// loop continues reading through the prototype chain in Tier 0; the trace
	// must hand the loop back at its head instead of exiting it early.
	if count >= budget {
		if err := v.pollJITSafepoint(); err != nil {
			return trace.startPC, jit.Interrupted, err
		}
		return trace.startPC, jit.Yielded, nil
	}
	if index+float64(count) < bound {
		return trace.startPC, jit.Yielded, nil
	}
	return trace.exitPC, jit.Executed, nil
}

// matchArrayBatchWriteTrace recognizes the compiler's canonical R4-6 batch
// write forms, where the loop variable (value) and the write key both advance
// by exactly one per iteration:
//
//	W1: for (; i < bound; i++) array[i] = i            // key == value == loop var
//	W2: for (; i < bound; i++) { array[j] = i; j++; }  // separate key counter
//	W3: for (; i < bound; i++) array[j++] = i          // post-incremented key
//
//	head: 0 LoadLocal v  1 <bound>  2 Lt  3 JmpFalsePop exit
//	body: 4 LoadLocal v  5 Dup  6 LoadLocal array  7 LoadLocal key
//	      W1/W2: 8 SetElemTop 9 Pop
//	      W3:    8 Dup 9 Inc 10 StoreLocal key 11 SetElemTop 12 Pop
//	tail: increment blocks {LoadLocal c, Dup, Inc, StoreLocal c, Pop}* then Jmp back
//
// W1 requires one block for v; W2 requires blocks for key then v; W3 requires
// one block for v (the key is incremented inside the body). The bulk executor
// then writes array[key+t] = value+t for the committed chunk and syncs the
// length property once, which is the exact final state of the per-iteration
// Tier 0 Set sequence.
func (v *VM) matchArrayBatchWriteTrace(frame *vmFrame, startPC, backedgePC int) *arrayBatchWriteTraceState {
	if frame == nil || frame.tmpl == nil || frame.base < 0 {
		return nil
	}
	code := frame.tmpl.Code
	if startPC < 0 || backedgePC <= startPC+7*bytecode.InstrSize ||
		backedgePC+bytecode.InstrSize > len(code) {
		return nil
	}
	op := func(index int) bytecode.Opcode { return bytecode.Opcode(code[startPC+index*bytecode.InstrSize]) }
	arg := func(index int) uint32 { return jitTraceOperand(code, startPC+index*bytecode.InstrSize) }
	if op(0) != bytecode.OpLoadLocal || op(2) != bytecode.OpLt || op(3) != bytecode.OpJmpFalsePop ||
		op(4) != bytecode.OpLoadLocal || op(5) != bytecode.OpDup || op(6) != bytecode.OpLoadLocal ||
		op(7) != bytecode.OpLoadLocal {
		return nil
	}
	valueLocal := int(arg(0))
	arrayLocal := int(arg(6))
	keyLocal := int(arg(7))
	if int(arg(4)) != valueLocal {
		return nil
	}
	bodyLength := 6
	keyIncInBody := false
	switch op(8) {
	case bytecode.OpSetElemTop:
		if op(9) != bytecode.OpPop {
			return nil
		}
	case bytecode.OpDup:
		bodyLength = 9
		keyIncInBody = true
		if op(9) != bytecode.OpInc || op(10) != bytecode.OpStoreLocal || int(arg(10)) != keyLocal ||
			op(11) != bytecode.OpSetElemTop || op(12) != bytecode.OpPop {
			return nil
		}
	default:
		return nil
	}
	localCount := frame.tmpl.NumLocals
	for _, slot := range []int{valueLocal, arrayLocal, keyLocal} {
		if slot < 0 || slot >= localCount {
			return nil
		}
	}
	if valueLocal == arrayLocal || keyLocal == arrayLocal {
		return nil
	}
	// Parse the tail increment blocks, then the backedge jump. pc is an
	// instruction index within the loop range (the last index is the backedge
	// Jmp), so every bound comparison here is in index units.
	var incBlocks []int
	tailStart := 4 + bodyLength
	lastIndex := (backedgePC - startPC) / bytecode.InstrSize
	pc := tailStart
	for {
		if op(pc) == bytecode.OpJmp {
			break
		}
		if pc+4 > lastIndex ||
			op(pc) != bytecode.OpLoadLocal || op(pc+1) != bytecode.OpDup || op(pc+2) != bytecode.OpInc ||
			op(pc+3) != bytecode.OpStoreLocal || op(pc+4) != bytecode.OpPop {
			return nil
		}
		incBlocks = append(incBlocks, int(arg(pc)))
		pc += 5
	}
	if pc != lastIndex {
		return nil
	}
	// Validate the increment schedule per form.
	if keyLocal == valueLocal {
		if keyIncInBody || len(incBlocks) != 1 || incBlocks[0] != valueLocal {
			return nil
		}
	} else {
		if keyIncInBody {
			if len(incBlocks) != 1 || incBlocks[0] != valueLocal {
				return nil
			}
		} else if len(incBlocks) != 2 || incBlocks[0] != keyLocal || incBlocks[1] != valueLocal {
			return nil
		}
	}
	boundLocal, boundConst, boundIsLocal, ok := traceBoundOperand(frame.tmpl, op, arg, 1, valueLocal)
	if !ok {
		return nil
	}
	// The bound must not alias the key local: the loop tail increments the
	// key every iteration, so a bound that is also the key would move with
	// the writes and make the chunked range diverge from Tier 0 (the
	// value/index aliasing is already excluded above).
	if boundIsLocal && (boundLocal == keyLocal || boundLocal == valueLocal) {
		return nil
	}
	backedgeTarget := backedgePC + bytecode.InstrSize + bytecode.SignedOperand(arg(pc))
	exitPC := startPC + 4*bytecode.InstrSize + bytecode.SignedOperand(arg(3))
	if backedgeTarget != startPC || exitPC <= backedgePC || exitPC > len(code) {
		return nil
	}
	localsEnd := frame.base + localCount
	if localsEnd > len(v.stack) {
		return nil
	}
	locals := v.stack[frame.base:localsEnd]
	if _, ok := locals[arrayLocal].(*engine.ArrayValue); !ok {
		return nil
	}
	trace := &arrayBatchWriteTraceState{
		arrayLocal: arrayLocal, keyLocal: keyLocal, valueLocal: valueLocal,
		indexLocal: valueLocal, boundLocal: boundLocal, boundConst: boundConst,
		boundIsLocal: boundIsLocal, startPC: startPC, exitPC: exitPC,
	}
	if _, _, _, _, ok := trace.arrayBatchNumbers(locals); !ok {
		return nil
	}
	return trace
}

func (t *arrayBatchWriteTraceState) arrayBatchNumbers(locals []engine.Value) (float64, float64, float64, float64, bool) {
	if t == nil || t.indexLocal < 0 || t.indexLocal >= len(locals) ||
		t.keyLocal < 0 || t.keyLocal >= len(locals) {
		return 0, 0, 0, 0, false
	}
	index, ok := locals[t.indexLocal].Float()
	if !ok {
		return 0, 0, 0, 0, false
	}
	bound := t.boundConst
	if t.boundIsLocal {
		if t.boundLocal < 0 || t.boundLocal >= len(locals) {
			return 0, 0, 0, 0, false
		}
		bound, ok = locals[t.boundLocal].Float()
		if !ok {
			return 0, 0, 0, 0, false
		}
	}
	key, ok := locals[t.keyLocal].Float()
	if !ok {
		return 0, 0, 0, 0, false
	}
	value, ok := locals[t.valueLocal].Float()
	if !ok {
		return 0, 0, 0, 0, false
	}
	const maxSafeInteger = float64(1<<53 - 1)
	if math.IsNaN(index) || math.IsInf(index, 0) || math.Trunc(index) != index || index < 0 || index > maxSafeInteger ||
		math.IsNaN(bound) || math.IsInf(bound, 0) || math.Trunc(bound) != bound || bound < 0 || bound > maxSafeInteger ||
		math.IsNaN(key) || math.IsInf(key, 0) || math.Trunc(key) != key || key < 0 || key > maxSafeInteger ||
		math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, 0, 0, 0, false
	}
	return index, bound, key, value, true
}

// executeArrayBatchWriteTrace bulk-executes the batch write chunk: the
// committed range [key, key+count) is filled with value..value+count-1,
// growing the element storage with holes first (exactly the per-write Tier 0
// Set semantics) and syncing the length property once. The length guard keeps
// the final index inside the safe integer domain; the index/key/value locals
// advance atomically per committed chunk so a guard failure or safepoint
// interruption resumes in a state Tier 0 can continue from.
func (v *VM) executeArrayBatchWriteTrace(trace *arrayBatchWriteTraceState, locals []engine.Value) (int, jit.ExitReason, error) {
	if trace == nil || trace.arrayLocal < 0 || trace.arrayLocal >= len(locals) {
		return 0, jit.GuardFailed, nil
	}
	receiver, ok := locals[trace.arrayLocal].(*engine.ArrayValue)
	if !ok {
		return 0, jit.GuardFailed, nil
	}
	index, bound, key, value, ok := trace.arrayBatchNumbers(locals)
	if !ok {
		return 0, jit.GuardFailed, nil
	}
	if index >= bound {
		return trace.exitPC, jit.Executed, nil
	}
	remaining := int(bound - index)
	budget := int(v.jitConfig.TraceBudget)
	if budget <= 0 {
		budget = 65536
	}
	count := remaining
	if count > budget {
		count = budget
	}
	// Length guard: the final write index key+count-1 must stay within the
	// safe integer domain so the length synchronization is exact.
	const maxSafeInteger = float64(1<<53 - 1)
	if key+float64(count) > maxSafeInteger+1 {
		return 0, jit.GuardFailed, nil
	}
	receiver.WriteNumberRange(int(key), value, count)
	locals[trace.indexLocal] = engine.Number(index + float64(count))
	locals[trace.keyLocal] = engine.Number(key + float64(count))
	locals[trace.valueLocal] = engine.Number(value + float64(count))
	if count >= budget {
		if err := v.pollJITSafepoint(); err != nil {
			return trace.startPC, jit.Interrupted, err
		}
		return trace.startPC, jit.Yielded, nil
	}
	return trace.exitPC, jit.Executed, nil
}

func (v *VM) tryQuickTrace(frame *vmFrame, startPC, backedgePC int) (int, bool, error) {
	if v.insnsEnabled || frame == nil || frame.tmpl == nil || frame.jitTraceFailedPC == backedgePC {
		// R4-8: the same frame must not retry a trace version that already
		// failed a guard at this backedge (deopt recovery); interpreting the
		// loop is the oracle-equivalent continuation. Only the failed backedge
		// is blocked: a different loop in the same frame can still try.
		if frame != nil && frame.jitTraceFailedPC == backedgePC && v.jitConfig.Stats {
			v.jitStats.TraceFrameRetriesBlocked++
		}
		return 0, false, nil
	}
	key := quickTraceKey{tmpl: frame.tmpl, backedgePC: backedgePC}
	state := frame.jitTrace
	if frame.jitTraceGeneration != v.jitGeneration || frame.jitTracePC != backedgePC {
		state = v.jitTraces[key]
		if state == nil {
			state = &quickTraceState{}
			v.jitTraces[key] = state
		}
		frame.jitTrace = state
		frame.jitTracePC = backedgePC
		frame.jitTraceGeneration = v.jitGeneration
	}
	if state.rejected {
		if v.jitConfig.Stats {
			// R3-7: the stable rejection cache serves this backedge; no
			// recompile attempt and no matcher/compiler cost.
			v.jitStats.RejectionCacheHits++
		}
		return 0, false, nil
	}
	if state.program == nil && state.arrayPush == nil && state.closureIncrement == nil &&
		state.arrayIndex == nil && state.arrayBatch == nil {
		if state.backedges < ^uint32(0) {
			state.backedges++
		}
		if state.backedges < v.backedgeThreshold() {
			return 0, false, nil
		}
		compileStart := time.Now()
		// The Go-side shape matchers run before the compile-time candidate
		// scan: the R4-5/R4-6 array loops contain OpGetElem / OpSetElemTop,
		// which the general trace compiler does not lower, so they are only
		// reachable through the exact matchers (everything else stays on the
		// scan -> rejection-cache path).
		if arrayPush := v.matchArrayPushTrace(frame, startPC, backedgePC); arrayPush != nil {
			state.arrayPush = arrayPush
			if v.jitConfig.Stats {
				elapsed := uint64(time.Since(compileStart))
				v.jitStats.TraceCompileNanos += elapsed
				v.spendCompileBudget(elapsed)
				v.jitStats.TracesCompiled++
				v.jitStats.ArrayPushSites++
			}
		} else if arrayIndex := v.matchArrayIndexTrace(frame, startPC, backedgePC); arrayIndex != nil {
			state.arrayIndex = arrayIndex
			if v.jitConfig.Stats {
				elapsed := uint64(time.Since(compileStart))
				v.jitStats.TraceCompileNanos += elapsed
				v.spendCompileBudget(elapsed)
				v.jitStats.TracesCompiled++
				v.jitStats.ArrayIndexSites++
			}
		} else if arrayBatch := v.matchArrayBatchWriteTrace(frame, startPC, backedgePC); arrayBatch != nil {
			state.arrayBatch = arrayBatch
			if v.jitConfig.Stats {
				elapsed := uint64(time.Since(compileStart))
				v.jitStats.TraceCompileNanos += elapsed
				v.spendCompileBudget(elapsed)
				v.jitStats.TracesCompiled++
				v.jitStats.ArrayBatchSites++
			}
		} else if closureIncrement := v.matchClosureIncrementTrace(frame, startPC, backedgePC); closureIncrement != nil {
			state.closureIncrement = closureIncrement
			if v.jitConfig.Stats {
				elapsed := uint64(time.Since(compileStart))
				v.jitStats.TraceCompileNanos += elapsed
				v.spendCompileBudget(elapsed)
				v.jitStats.TracesCompiled++
				v.jitStats.ClosureUpvalueSites++
			}
		} else {
			// R5-4: the cumulative compile budget denies the general trace
			// compile too; the loop stays on Tier 0 and keeps advancing.
			if !v.compileAdmitted() {
				return 0, false, nil
			}
			// R3-7 compile-time candidate filter: reject ranges with try/catch
			// regions or unsupported opcodes before the full trace compiler run.
			if scanErr := jit.RejectTraceReason(frame.tmpl, startPC, backedgePC); scanErr != nil {
				v.recordJITRejection("trace", scanErr)
				state.rejected = true
				state.reason = scanErr.Error()
				v.jitStats.LastError = state.reason
				if v.jitConfig.Stats {
					v.jitStats.TracesRejected++
				}
				v.noteAdaptiveFailure()
				return 0, false, nil
			}
			program, err := jit.CompileTraceWithGuards(
				frame.tmpl, startPC, backedgePC,
				v.traceNoopCallGuards(frame, startPC, backedgePC),
				v.traceMethodGuards(frame, startPC, backedgePC))
			if v.jitConfig.Stats {
				elapsed := uint64(time.Since(compileStart))
				v.jitStats.TraceCompileNanos += elapsed
				v.spendCompileBudget(elapsed)
			}
			if err != nil {
				v.recordJITRejection("trace", err)
				state.rejected = true
				state.reason = err.Error()
				v.jitStats.LastError = err.Error()
				if v.jitConfig.Stats {
					v.jitStats.TracesRejected++
				}
				v.noteAdaptiveFailure()
				return 0, false, nil
			}
			state.program = program
			if v.jitConfig.Stats {
				v.jitStats.TracesCompiled++
				v.jitStats.NoopCallSites += uint64(program.GuardedNoopCalls())
				v.jitStats.MethodCallSites += uint64(program.GuardedMethodCalls())
			}
			if v.jitConfig.Dump == jit.DumpIR && !state.dumpedIR {
				fmt.Fprintf(v.jitDumpWriter(), "JIT dump tier=trace\n%s", program.DumpIR())
				state.dumpedIR = true
			}
			if v.jitConfig.Mode == jit.Auto {
				if !v.compileAdmitted() {
					return 0, false, nil
				}
				nativeStart := time.Now()
				if v.jitConfig.Dump == jit.DumpASM {
					err = program.CompileNativeForDump()
				} else {
					err = program.CompileNative()
				}
				if v.jitConfig.Stats {
					elapsed := uint64(time.Since(nativeStart))
					v.jitStats.NativeTraceCompileNanos += elapsed
					v.spendCompileBudget(elapsed)
				}
				if err == nil {
					err = v.reserveNativeTrace(state, uint64(program.NativeSize()))
				}
				if err != nil {
					_ = program.Close()
					v.recordJITRejection("native-trace", err)
					v.jitStats.LastNativeError = err.Error()
					if v.jitConfig.Stats {
						v.jitStats.NativeTracesRejected++
					}
				} else {
					if v.jitConfig.Dump == jit.DumpASM {
						fmt.Fprintf(v.jitDumpWriter(), "JIT dump tier=native-trace bytes=%d\n", program.NativeSize())
						io.WriteString(v.jitDumpWriter(), program.NativeDisassembly())
					}
					if v.jitConfig.Stats {
						v.jitStats.NativeTracesCompiled++
					}
				}
			}
		}
	}

	base := frame.base
	end := base + frame.tmpl.NumLocals
	if base < 0 || end != len(v.stack) {
		return 0, false, nil
	}
	locals := v.stack[base:end]
	if state.arrayPush != nil {
		exitPC, reason, err := v.executeArrayPushTrace(state.arrayPush, locals)
		if err != nil {
			return 0, false, err
		}
		switch reason {
		case jit.Executed:
			resetQuickTraceGuardFailures(state)
			if v.jitConfig.Stats {
				v.jitStats.TracesExecuted++
				v.noteAdaptiveBenefit()
			}
			return exitPC, true, nil
		case jit.Yielded:
			resetQuickTraceGuardFailures(state)
			if v.jitConfig.Stats {
				v.jitStats.TraceYields++
				v.jitStats.ArrayPushYields++
			}
			return exitPC, true, nil
		case jit.GuardFailed:
			frame.jitTraceFailedPC = backedgePC
			v.noteTraceGuardFailure(state)
			if v.jitConfig.Stats {
				v.jitStats.GuardFailures++
			}
		}
		return 0, false, nil
	}
	if state.closureIncrement != nil {
		exitPC, reason, err := v.executeClosureIncrementTrace(state.closureIncrement, locals)
		if err != nil {
			return 0, false, err
		}
		switch reason {
		case jit.Executed:
			resetQuickTraceGuardFailures(state)
			if v.jitConfig.Stats {
				v.jitStats.TracesExecuted++
				v.noteAdaptiveBenefit()
			}
			return exitPC, true, nil
		case jit.Yielded:
			resetQuickTraceGuardFailures(state)
			if v.jitConfig.Stats {
				v.jitStats.TraceYields++
				v.jitStats.ClosureUpvalueYields++
			}
			return exitPC, true, nil
		case jit.GuardFailed:
			frame.jitTraceFailedPC = backedgePC
			v.noteTraceGuardFailure(state)
			if v.jitConfig.Stats {
				v.jitStats.GuardFailures++
			}
		}
		return 0, false, nil
	}
	if state.arrayIndex != nil {
		exitPC, reason, err := v.executeArrayIndexTrace(state.arrayIndex, locals)
		if err != nil {
			return 0, false, err
		}
		switch reason {
		case jit.Executed:
			resetQuickTraceGuardFailures(state)
			if v.jitConfig.Stats {
				v.jitStats.TracesExecuted++
				v.noteAdaptiveBenefit()
			}
			return exitPC, true, nil
		case jit.Yielded:
			resetQuickTraceGuardFailures(state)
			if v.jitConfig.Stats {
				v.jitStats.TraceYields++
				v.jitStats.ArrayIndexYields++
			}
			return exitPC, true, nil
		case jit.GuardFailed:
			frame.jitTraceFailedPC = backedgePC
			v.noteTraceGuardFailure(state)
			if v.jitConfig.Stats {
				v.jitStats.GuardFailures++
			}
		}
		return 0, false, nil
	}
	if state.arrayBatch != nil {
		exitPC, reason, err := v.executeArrayBatchWriteTrace(state.arrayBatch, locals)
		if err != nil {
			return 0, false, err
		}
		switch reason {
		case jit.Executed:
			resetQuickTraceGuardFailures(state)
			if v.jitConfig.Stats {
				v.jitStats.TracesExecuted++
				v.noteAdaptiveBenefit()
			}
			return exitPC, true, nil
		case jit.Yielded:
			resetQuickTraceGuardFailures(state)
			if v.jitConfig.Stats {
				v.jitStats.TraceYields++
				v.jitStats.ArrayBatchYields++
			}
			return exitPC, true, nil
		case jit.GuardFailed:
			frame.jitTraceFailedPC = backedgePC
			v.noteTraceGuardFailure(state)
			if v.jitConfig.Stats {
				v.jitStats.GuardFailures++
			}
		}
		return 0, false, nil
	}
	if v.jitConfig.Mode == jit.Auto && state.program.HasNative() {
		var expectedLocals []engine.Value
		var expectedExit jit.DeoptExit
		var expectedReason jit.ExitReason
		var expectedErr error
		verifyReadOnly := v.jitConfig.Verify && !state.program.HasPropertyWrites()
		if verifyReadOnly {
			expectedLocals = append([]engine.Value(nil), locals...)
			expectedExit, expectedReason, expectedErr = state.program.ExecuteBudgetDetailed(expectedLocals, 0)
		}
		v.touchNativeTrace(state)
		var exit jit.DeoptExit
		var reason jit.ExitReason
		var yields uint64
		var verifyChecked bool
		var verifyMatched = true
		var nativeErr error
		if v.jitConfig.Verify && state.program.HasPropertyWrites() {
			exit, reason, yields, verifyChecked, verifyMatched, nativeErr =
				state.program.ExecuteNativeBudgetVerifiedWithSafepoint(
					locals, v.jitConfig.TraceBudget, v.pollJITSafepoint)
		} else {
			exit, reason, yields, nativeErr = state.program.ExecuteNativeBudgetDetailedWithSafepoint(
				locals, v.jitConfig.TraceBudget, v.pollJITSafepoint)
		}
		if v.jitConfig.Stats {
			v.jitStats.NativeTraceYields += yields
		}
		if verifyChecked {
			v.jitStats.VerifyChecks++
			if !verifyMatched {
				v.jitStats.VerifyFailures++
				v.dropNativeTrace(state)
				if nativeErr == nil && reason == jit.Executed {
					return v.resumeTraceExit(key, exit)
				}
			}
		}
		if nativeErr != nil && reason == jit.Interrupted {
			return 0, false, nativeErr
		} else if nativeErr != nil {
			v.dropNativeTrace(state)
			v.jitStats.LastNativeError = nativeErr.Error()
			if v.jitConfig.Stats {
				v.jitStats.Errors++
			}
		} else if reason == jit.Executed {
			if verifyReadOnly {
				v.jitStats.VerifyChecks++
				if expectedErr != nil || expectedReason != jit.Executed || !jit.SameDeoptExit(expectedExit, exit) ||
					!sameJITLocals(locals, expectedLocals) {
					v.jitStats.VerifyFailures++
					v.dropNativeTrace(state)
					copy(locals, expectedLocals)
					if expectedErr == nil && expectedReason == jit.Executed {
						return v.resumeTraceExit(key, expectedExit)
					}
					return 0, false, nil
				}
			}
			if v.jitConfig.Stats {
				v.jitStats.NativeTracesExecuted++
				v.noteAdaptiveBenefit()
			}
			resetTraceGuardFailures(state)
			return v.resumeTraceExit(key, exit)
		} else if reason == jit.Yielded {
			resetTraceGuardFailures(state)
			return exit.ResumePC, true, nil
		} else if reason == jit.GuardFailed {
			v.noteNativeTraceGuardFailure(state)
			if v.jitConfig.Stats {
				v.jitStats.GuardFailures++
				// R4-8: the native trace entry guard failed; the bridge then
				// re-executes the same trace in Quick (the R4-3 property-PIC
				// learning path: Quick may absorb the new shape). The counter
				// makes this duplicate bridge work observable so the cost can
				// be tracked against the absorption wins.
				v.jitStats.NativeTraceQuickFallbacks++
			}
		}
	}
	exit, reason, err := state.program.ExecuteBudgetDetailedWithSafepoint(
		locals, v.jitConfig.TraceBudget, v.pollJITSafepoint)
	if err != nil {
		if reason == jit.Interrupted {
			return 0, false, err
		}
		state.rejected = true
		state.program = nil
		v.noteAdaptiveFailure()
		v.jitStats.LastError = err.Error()
		if v.jitConfig.Stats {
			v.jitStats.Errors++
		}
		return 0, false, nil
	}
	switch reason {
	case jit.Executed:
		resetQuickTraceGuardFailures(state)
		if v.jitConfig.Stats {
			v.jitStats.TracesExecuted++
			v.noteAdaptiveBenefit()
		}
		return v.resumeTraceExit(key, exit)
	case jit.Yielded:
		resetQuickTraceGuardFailures(state)
		if v.jitConfig.Stats {
			v.jitStats.TraceYields++
		}
		return exit.ResumePC, true, nil
	case jit.GuardFailed:
		frame.jitTraceFailedPC = backedgePC
		v.noteTraceGuardFailure(state)
		if v.jitConfig.Stats {
			v.jitStats.GuardFailures++
		}
	}
	return 0, false, nil
}

func (v *VM) tryQuickCall(cl *vmClosure, thisVal engine.Value, args []engine.Value) (engine.Value, bool, error) {
	if v.insnsEnabled {
		return engine.Undefined(), false, nil
	}
	if v.jitPending > 0 {
		v.pollNativeCompiles()
	}
	var state *quickJITState
	if cl != nil && cl.jitGeneration == v.jitGeneration {
		state = cl.jitState
	}
	profile := state == nil || state.program == nil
	if !profile {
		if _, requiresCallee := state.program.RequiresSelf(); requiresCallee && state.callKind == quickCallUnknown {
			profile = true
		}
	}
	if profile {
		state = v.noteJITCall(cl)
	} else if v.jitConfig.Stats {
		v.jitStats.Calls++
	}
	if state == nil || state.program == nil {
		return engine.Undefined(), false, nil
	}
	program := state.program
	if upvalueIndex, required := program.RequiresSelf(); required {
		if state.calleeDisabled {
			return engine.Undefined(), false, nil
		}
		// Recursive quick execution collapses many JS calls into one Go entry.
		// Keep --monitor call/instruction counters exact by retaining Tier 0.
		if v.callCountEnabled || v.insnsEnabled {
			return engine.Undefined(), false, nil
		}
		if state.callKind == quickCallUnsupported || state.callKind == quickCallUnknown {
			if v.jitConfig.Stats {
				v.jitStats.GuardFailures++
			}
			return engine.Undefined(), false, nil
		}
		value, ok := closureUpvalue(cl, upvalueIndex)
		matched := false
		if ok && state.callKind == quickCallSelf {
			matched = value == cl
		} else if ok && state.callKind == quickCallBound {
			switch value {
			case engine.Value(state.callee):
				matched = true
			case engine.Value(state.altCallee):
				if state.altCallee != nil && state.altProgram != nil {
					program = state.altProgram
					matched = true
					if v.jitConfig.Stats {
						v.jitStats.CalleePICHits++
					}
				}
			default:
				if target, isClosure := value.(*vmClosure); isClosure && state.altProgram == nil {
					program, matched = v.specializeAlternateCallee(state, target)
				}
			}
		}
		if !matched {
			v.noteCalleeGuardFailure(state)
			if v.jitConfig.Stats {
				v.jitStats.GuardFailures++
				if state.callKind == quickCallBound {
					v.jitStats.CalleeGuardFailures++
				}
			}
			return engine.Undefined(), false, nil
		}
		state.calleeGuardFailures = 0
	}
	if program.ReturnsUndefined() {
		if v.jitConfig.Stats {
			v.jitStats.Executed++
			v.noteAdaptiveBenefit()
		}
		return engine.Undefined(), true, nil
	}
	if v.jitConfig.Mode == jit.Auto && program.HasNative() {
		v.touchNative(state)
		result, reason, yields, err := program.ExecuteNativeBudgetWithSafepoint(
			thisVal, args, v.jitConfig.TraceBudget, v.pollJITSafepoint)
		if v.jitConfig.Stats {
			v.jitStats.NativeYields += yields
		}
		if err != nil && reason == jit.Interrupted {
			return engine.Undefined(), false, err
		} else if err != nil {
			v.dropNative(state)
			if v.jitConfig.Stats {
				v.jitStats.Errors++
			}
		} else if reason == jit.Executed {
			if !v.verifyNativeResult(program, thisVal, args, result) {
				v.dropNative(state)
				state.rejected = true
				v.noteAdaptiveFailure()
				return engine.Undefined(), false, nil
			}
			if v.jitConfig.Stats {
				v.jitStats.NativeExecuted++
				v.noteAdaptiveBenefit()
				if state.callKind == quickCallBound {
					v.jitStats.CalleeExecuted++
				}
			}
			resetJITGuardFailures(state)
			return result, true, nil
		} else if reason == jit.GuardFailed {
			v.noteNativeGuardFailure(state)
			if v.jitConfig.Stats {
				v.jitStats.GuardFailures++
			}
		}
	}
	result, reason, err := program.ExecuteWithSafepoint(
		thisVal, args, v.jitConfig.TraceBudget, v.pollJITSafepoint)
	if err != nil {
		if reason == jit.Interrupted {
			return engine.Undefined(), false, err
		}
		if v.jitConfig.Stats {
			v.jitStats.Errors++
		}
		state.rejected = true
		v.noteAdaptiveFailure()
		return engine.Undefined(), false, nil
	}
	switch reason {
	case jit.Executed:
		resetQuickGuardFailures(state)
		if v.jitConfig.Stats {
			v.jitStats.Executed++
			v.noteAdaptiveBenefit()
			if state.callKind == quickCallBound {
				v.jitStats.CalleeExecuted++
			}
		}
		return result, true, nil
	case jit.GuardFailed:
		v.noteJITGuardFailure(state)
		if v.jitConfig.Stats {
			v.jitStats.GuardFailures++
		}
	}
	return engine.Undefined(), false, nil
}
