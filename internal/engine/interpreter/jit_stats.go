// JIT 可观测面：--jit-stats 聚合统计、拒绝/deopt 归因记录与 --jit-dump 输出。

package interpreter

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/aluka-lang/aluka/internal/engine/bytecode"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

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
