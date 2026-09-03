package main

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// benchLineRE matches the per-case lines emitted by benchmark scripts such as
// tests/benchmark/perf-compare.js:
//
//	fib25: 35.01
//
// The name is a single token (letters/digits/_/-/., no spaces); the value is a
// decimal number of milliseconds. Lines that do not match this shape (JIT
// stats/rejection/deopt, IC stats, warnings, bare result values) are ignored.
var benchLineRE = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_.-]*): (\d+(?:\.\d+)?)$`)

// parsedOutput is the per-invocation parse result of the CLI stdout.
type parsedOutput struct {
	cases    map[string]float64 // case name -> ms
	jitStats string             // first "JIT stats: ..." line, if any
	hasBench bool               // whether any benchmark line was found
}

// parseBenchmarkOutput extracts `name: ms` benchmark lines from the CLI stdout
// and captures the first "JIT stats:" line for the environment record.
func parseBenchmarkOutput(stdout string) parsedOutput {
	out := parsedOutput{cases: make(map[string]float64)}
	for _, raw := range strings.Split(stdout, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "JIT stats:") && out.jitStats == "" {
			out.jitStats = line
			continue
		}
		m := benchLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ms, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			continue
		}
		out.cases[m[1]] = ms
		out.hasBench = true
	}
	return out
}

// statSummary is the statistical digest of a set of raw samples.
type statSummary struct {
	Samples []float64
	Median  float64
	Min     float64
	Max     float64
	Mean    float64
	// MadPct is the relative median absolute deviation (dispersion) as a
	// percentage of the central value (median, or mean when the median is
	// zero). It reports how stable the raw samples are around their center.
	MadPct float64
}

// summarize computes median/min/max/mean and the relative MAD from raw
// samples. Samples are copied; the caller's slice is not modified.
func summarize(samples []float64) statSummary {
	out := statSummary{Samples: samples}
	n := len(samples)
	if n == 0 {
		return out
	}
	sorted := make([]float64, n)
	copy(sorted, samples)
	sort.Float64s(sorted)
	out.Min = sorted[0]
	out.Max = sorted[n-1]
	out.Median = medianSorted(sorted)
	sum := 0.0
	for _, s := range samples {
		sum += s
	}
	out.Mean = sum / float64(n)
	denom := out.Median
	if denom == 0 {
		denom = out.Mean
	}
	if denom == 0 {
		return out // all samples zero: no dispersion
	}
	devs := make([]float64, n)
	for i, s := range samples {
		devs[i] = math.Abs(s - out.Median)
	}
	sort.Float64s(devs)
	out.MadPct = medianSorted(devs) / denom * 100
	return out
}

// medianSorted returns the median of an already-sorted slice.
func medianSorted(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// rotatedTiers returns tiers shifted left by round, so the execution order
// differs between rounds and no tier always runs first or last. This spreads
// temperature and frequency bias evenly across the tiers.
func rotatedTiers(tiers []string, round int) []string {
	if len(tiers) == 0 {
		return nil
	}
	shift := round % len(tiers)
	order := make([]string, 0, len(tiers))
	order = append(order, tiers[shift:]...)
	order = append(order, tiers[:shift]...)
	return order
}
