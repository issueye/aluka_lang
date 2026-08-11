package jitdiff

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/aluka-lang/aluka/internal/engine/interpreter"
	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// Tier is one execution mode of the differential.
type Tier struct {
	Name string
	Mode jit.Mode
}

// Tiers is the fixed execution order: off first (the oracle), then quick,
// then auto.
var Tiers = []Tier{
	{Name: "off", Mode: jit.Off},
	{Name: "quick", Mode: jit.Quick},
	{Name: "auto", Mode: jit.Auto},
}

// TierResult is the observable outcome of one case in one tier.
type TierResult struct {
	Tier    string    `json:"tier"`
	Result  string    `json:"result"`              // serialized event log
	EvalErr string    `json:"evalError,omitempty"` // non-empty when Eval failed
	Stats   jit.Stats `json:"stats"`
	IR      string    `json:"ir,omitempty"` // per-tier IR dump (captured on demand)
}

// RunTier executes one case in one tier inside a fresh VM.
func RunTier(c *Case, tier Tier, params Params, captureIR bool) (TierResult, error) {
	params = params.Normalized()
	vm, err := interpreter.NewVM()
	if err != nil {
		return TierResult{}, err
	}
	defer vm.Close()
	var irBuf bytes.Buffer
	config := jit.Config{
		Mode: tier.Mode, Threshold: 1, BackedgeThreshold: 1,
		TraceBudget: params.TraceBudget, Verify: params.Verify && tier.Mode == jit.Auto,
		Stats: true,
	}
	if captureIR {
		config.Dump = jit.DumpIR
		config.DumpWriter = &irBuf
	}
	vm.ConfigureJIT(config)
	_, err = vm.Eval(c.Source, "jitdiff-case.js")
	result := TierResult{Tier: tier.Name}
	if err != nil {
		result.EvalErr = err.Error()
	} else {
		value, gerr := vm.Global().Get("JITDIFF_RESULT")
		if gerr != nil {
			return TierResult{}, gerr
		}
		result.Result = value.String()
	}
	result.Stats = vm.JITStats()
	if captureIR {
		result.IR = irBuf.String()
	}
	return result, nil
}

// Mismatch reports a differential failure between Tier 0 and another tier.
type Mismatch struct {
	Case    *Case
	Results []TierResult // off first, then quick, then auto
}

func (m *Mismatch) Error() string {
	off := m.Results[0]
	var lines []string
	lines = append(lines, fmt.Sprintf("jitdiff: differential mismatch in case %d kind=%s seed=%d", m.Case.ID, m.Case.Kind, m.Case.Seed))
	for i := 1; i < len(m.Results); i++ {
		res := m.Results[i]
		if res.EvalErr != off.EvalErr {
			lines = append(lines, fmt.Sprintf("  tier %s: evalErr=%q (off=%q)", res.Tier, res.EvalErr, off.EvalErr))
		}
		if res.Result != off.Result {
			lines = append(lines, fmt.Sprintf("  tier %s: event log differs from off", res.Tier))
			lines = append(lines, diffLines("off", off.Result, res.Tier, res.Result)...)
		}
	}
	return strings.Join(lines, "\n")
}

// diffLines returns a compact side-by-side of the first differing event-log
// lines between the oracle and a tier.
func diffLines(oracleName, oracle, tierName, tier string) []string {
	o := strings.Split(oracle, "\n")
	t := strings.Split(tier, "\n")
	limit := len(o)
	if len(t) > limit {
		limit = len(t)
	}
	lines := make([]string, 0, 2*limit+1)
	for i := 0; i < limit; i++ {
		var oLine, tLine string
		if i < len(o) {
			oLine = o[i]
		} else {
			oLine = "<missing>"
		}
		if i < len(t) {
			tLine = t[i]
		} else {
			tLine = "<missing>"
		}
		if oLine != tLine {
			lines = append(lines, fmt.Sprintf("    event %d  %s=%q", i, oracleName, oLine))
			lines = append(lines, fmt.Sprintf("    event %d  %s=%q", i, tierName, tLine))
			if len(lines) >= 8 {
				break
			}
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "  (event log length or trailing content differs)")
	}
	return lines
}

// RunCase runs one case across all tiers and compares against the off oracle.
// A mismatch is returned as *Mismatch; any other error is an infrastructure
// failure (VM creation, Eval of an ill-formed generated source, ...).
func RunCase(c *Case, p Params) ([]TierResult, error) {
	results := make([]TierResult, 0, len(Tiers))
	for _, tier := range Tiers {
		res, err := RunTier(c, tier, p, false)
		if err != nil {
			return results, err
		}
		results = append(results, res)
	}
	off := results[0]
	if off.EvalErr != "" {
		// The generated source itself is broken; this is a generator bug,
		// not a JIT mismatch, and must be reported as such.
		return results, fmt.Errorf("jitdiff: case %d kind=%s source failed in off mode: %s", c.ID, c.Kind, off.EvalErr)
	}
	for i := 1; i < len(results); i++ {
		res := results[i]
		if res.EvalErr != "" || res.Result != off.Result {
			return results, &Mismatch{Case: c, Results: results}
		}
	}
	return results, nil
}

// SuiteSummary aggregates a full differential run for the test report.
type SuiteSummary struct {
	Version string `json:"version"`
	Seed    int64  `json:"seed"`
	Params  Params `json:"params"`
	Total   int    `json:"total"`

	QuickHit         int            `json:"quickHit"`       // hit-kind cases executed by Quick
	AutoNativeHit    int            `json:"autoNativeHit"`  // hit-kind cases executed by Native in Auto
	QuickExecuted    uint64         `json:"quickExecuted"`  // total Quick function+trace executions
	NativeExecuted   uint64         `json:"nativeExecuted"` // total Native function+trace executions
	GuardFailures    uint64         `json:"guardFailures"`  // quick+auto guard failures
	TracesCompiled   uint64         `json:"tracesCompiled"`
	ByKind           map[string]int `json:"byKind"`
	QuickHitsByKind  map[string]int `json:"quickHitsByKind"`
	NativeHitsByKind map[string]int `json:"nativeHitsByKind"`
}

// RunSuite generates count cases from seed and runs each across all tiers.
// On the first mismatch the failing case is saved as a reproducible artifact
// under artifactDir and a *Mismatch error is returned.
func RunSuite(seed int64, params Params, count int, artifactDir string) (*SuiteSummary, error) {
	params = params.Normalized()
	g := NewGenerator(seed, params)
	cases := g.Generate(count)
	sum := &SuiteSummary{
		Version: Version, Seed: seed, Params: params, Total: count,
		ByKind:           make(map[string]int),
		QuickHitsByKind:  make(map[string]int),
		NativeHitsByKind: make(map[string]int),
	}
	for _, c := range cases {
		sum.ByKind[c.Kind.String()]++
		results, err := RunCase(c, params)
		if err != nil {
			if mismatch, ok := err.(*Mismatch); ok {
				art, aerr := SaveArtifact(artifactDir, mismatch, params)
				if aerr != nil {
					return sum, fmt.Errorf("jitdiff: mismatch in case %d: %v (artifact save failed: %v)", c.ID, err, aerr)
				}
				return sum, fmt.Errorf("jitdiff: mismatch in case %d (kind=%s seed=%d): %v\nreproduce with:\n  %s", c.ID, c.Kind, c.Seed, mismatch, art.ReproCommand)
			}
			return sum, err
		}
		quick := results[1]
		auto := results[2]
		sum.GuardFailures += uint64(quick.Stats.GuardFailures + auto.Stats.GuardFailures)
		sum.QuickExecuted += quick.Stats.Executed + quick.Stats.TracesExecuted
		sum.NativeExecuted += auto.Stats.NativeExecuted + auto.Stats.NativeTracesExecuted
		sum.TracesCompiled += quick.Stats.TracesCompiled + auto.Stats.TracesCompiled
		if c.Kind.ExpectsQuickHit() && quick.Stats.Executed+quick.Stats.TracesExecuted > 0 {
			sum.QuickHit++
			sum.QuickHitsByKind[c.Kind.String()]++
		}
		if c.Kind.ExpectsNativeHit() && auto.Stats.NativeExecuted+auto.Stats.NativeTracesExecuted > 0 {
			sum.AutoNativeHit++
			sum.NativeHitsByKind[c.Kind.String()]++
		}
	}
	return sum, nil
}
