package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseBenchmarkOutput(t *testing.T) {
	stdout := `fib25: 35.01
JIT stats: mode=auto threshold=1000 backedgeThreshold=10000 traceBudget=65536 codeCacheLimit=4194304 calls=100 compiled=1
JIT rejection: tier=native count=1 reason="boom"
JIT deopt: function="propAccess" backedgePC=128 exitID=0 resumePC=132 count=1
propAccess-3M: 732.85
IC stats: get 100/0, set 100/0, call 100/0
some warning to stderr or stdout
12345
`
	parsed := parseBenchmarkOutput(stdout)
	if !parsed.hasBench {
		t.Fatal("hasBench = false, want true")
	}
	if got, want := parsed.cases["fib25"], 35.01; got != want {
		t.Errorf("fib25 = %v, want %v", got, want)
	}
	if got, want := parsed.cases["propAccess-3M"], 732.85; got != want {
		t.Errorf("propAccess-3M = %v, want %v", got, want)
	}
	if len(parsed.cases) != 2 {
		t.Errorf("parsed %d cases, want 2: %v", len(parsed.cases), parsed.cases)
	}
	if !strings.HasPrefix(parsed.jitStats, "JIT stats: mode=auto") {
		t.Errorf("jitStats = %q, want mode=auto line", parsed.jitStats)
	}
}

func TestParseBenchmarkOutputNoBench(t *testing.T) {
	parsed := parseBenchmarkOutput("JIT stats: mode=off threshold=1000\n")
	if parsed.hasBench {
		t.Fatal("hasBench = true, want false for stats-only output")
	}
	if len(parsed.cases) != 0 {
		t.Fatalf("cases = %v, want none", parsed.cases)
	}
	if parsed.jitStats == "" {
		t.Fatal("jitStats not captured")
	}
}

func TestParseBenchmarkOutputRejectsBadValues(t *testing.T) {
	// Names with spaces, or values that are not plain decimals, must not match.
	parsed := parseBenchmarkOutput("fib 25: 35.01\nstr: NaN\nx: 1e3\nfib25: 35.01\n")
	if len(parsed.cases) != 1 || parsed.cases["fib25"] != 35.01 {
		t.Fatalf("cases = %v, want only fib25=35.01", parsed.cases)
	}
}

func TestSummarize(t *testing.T) {
	sum := summarize([]float64{1, 2, 3, 4, 5})
	if sum.Median != 3 || sum.Min != 1 || sum.Max != 5 || sum.Mean != 3 {
		t.Fatalf("summary = %+v", sum)
	}
	// deviations from 3: [2 1 0 1 2], median 1 -> 1/3 = 33.33%
	if math.Abs(sum.MadPct-100.0/3) > 1e-9 {
		t.Errorf("MadPct = %v, want %v", sum.MadPct, 100.0/3)
	}
}

func TestSummarizeEvenCount(t *testing.T) {
	sum := summarize([]float64{1, 2, 3, 4})
	if sum.Median != 2.5 || sum.Min != 1 || sum.Max != 4 {
		t.Fatalf("summary = %+v", sum)
	}
}

func TestSummarizeZeroMedian(t *testing.T) {
	sum := summarize([]float64{0, 0, 0})
	if sum.Median != 0 || sum.MadPct != 0 || sum.Min != 0 || sum.Max != 0 {
		t.Fatalf("summary = %+v, want all zero", sum)
	}
}

func TestSummarizeDoesNotMutateInput(t *testing.T) {
	samples := []float64{3, 1, 2}
	_ = summarize(samples)
	if !reflect.DeepEqual(samples, []float64{3, 1, 2}) {
		t.Fatalf("summarize mutated its input: %v", samples)
	}
}

func TestRotatedTiers(t *testing.T) {
	tiers := []string{"off", "quick", "auto"}
	want := [][]string{
		{"off", "quick", "auto"},
		{"quick", "auto", "off"},
		{"auto", "off", "quick"},
		{"off", "quick", "auto"}, // wraps after len(tiers) rounds
	}
	for round, expected := range want {
		got := rotatedTiers(tiers, round)
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("round %d: order = %v, want %v", round, got, expected)
		}
	}
}

// fakeExec returns an execFunc that records the `--jit=` tier of every call
// and produces stdout/wall/duration through outputForTier.
func fakeExec(log *[]string, outputForTier func(tier string) (stdout string, wall time.Duration, err error)) execFunc {
	return func(ctx context.Context, name string, args ...string) (string, string, time.Duration, error) {
		tier := ""
		for _, a := range args {
			if strings.HasPrefix(a, "--jit=") {
				tier = strings.TrimPrefix(a, "--jit=")
			}
		}
		*log = append(*log, tier)
		stdout, wall, err := outputForTier(tier)
		return stdout, "", wall, err
	}
}

func TestRunnerCollectsSamplesWithRotation(t *testing.T) {
	var log []string
	values := map[string]string{
		"off":   "fib25: 100.00\n",
		"quick": "fib25: 50.00\n",
		"auto":  "fib25: 20.00\n",
	}
	r := &runner{
		binary: "bin/aluka", scripts: []string{"tests/benchmark/perf-compare.js"},
		tiers: []string{"off", "quick", "auto"}, reps: 3, rotate: true,
		timeout: time.Minute, exec: fakeExec(&log, func(tier string) (string, time.Duration, error) {
			return values[tier], time.Second, nil
		}),
	}
	rep, err := r.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Rotation: round0 off,quick,auto; round1 quick,auto,off; round2 auto,off,quick.
	wantOrder := []string{"off", "quick", "auto", "quick", "auto", "off", "auto", "off", "quick"}
	if !reflect.DeepEqual(log, wantOrder) {
		t.Fatalf("invocation order = %v, want %v", log, wantOrder)
	}
	c := rep.Cases["fib25"]
	if c == nil {
		t.Fatal("fib25 case missing")
	}
	for tier, median := range map[string]float64{"off": 100, "quick": 50, "auto": 20} {
		tr := c.Tier[tier]
		if tr == nil {
			t.Fatalf("tier %s missing", tier)
		}
		if len(tr.Samples) != 3 {
			t.Fatalf("tier %s has %d samples, want 3", tier, len(tr.Samples))
		}
		if tr.MedianMs != median || tr.MinMs != median || tr.MaxMs != median || tr.MeanMs != median {
			t.Errorf("tier %s summary = median %v min %v max %v mean %v, want %v",
				tier, tr.MedianMs, tr.MinMs, tr.MaxMs, tr.MeanMs, median)
		}
		if tr.MadPct != 0 {
			t.Errorf("tier %s MadPct = %v, want 0", tier, tr.MadPct)
		}
	}
	if len(rep.Failures) != 0 {
		t.Fatalf("unexpected failures: %v", rep.Failures)
	}
}

func TestRunnerWallClockMode(t *testing.T) {
	var log []string
	r := &runner{
		binary: "bin/aluka", scripts: []string{"tests/benchmark/mixed.js"},
		tiers: []string{"off", "auto"}, reps: 2, rotate: true,
		timeout: time.Minute, exec: fakeExec(&log, func(tier string) (string, time.Duration, error) {
			return "", 25 * time.Millisecond, nil // no per-case lines
		}),
	}
	rep, err := r.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tr := rep.Cases["mixed.js"].Tier["off"]
	if tr == nil || len(tr.Samples) != 2 {
		t.Fatalf("mixed.js/off samples = %+v, want 2", tr)
	}
	for _, s := range tr.Samples {
		if s.Ms != 25 {
			t.Errorf("sample = %v ms, want 25", s.Ms)
		}
	}
	if tr.MedianMs != 25 {
		t.Errorf("median = %v, want 25", tr.MedianMs)
	}
	if len(rep.Failures) != 0 {
		t.Fatalf("unexpected failures: %v", rep.Failures)
	}
}

func TestRunnerRecordsFailures(t *testing.T) {
	var log []string
	r := &runner{
		binary: "bin/aluka", scripts: []string{"tests/benchmark/perf-compare.js"},
		tiers: []string{"off", "quick", "auto"}, reps: 2, rotate: true,
		timeout: time.Minute, exec: fakeExec(&log, func(tier string) (string, time.Duration, error) {
			if tier == "quick" {
				return "", time.Millisecond, fmt.Errorf("quick tier failed")
			}
			return "fib25: 10.00\n", time.Millisecond, nil
		}),
	}
	rep, err := r.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Failures) != 2 { // quick failed in both rounds
		t.Fatalf("failures = %v, want 2", rep.Failures)
	}
	for _, f := range rep.Failures {
		if f.Tier != "quick" || !strings.Contains(f.Error, "quick tier failed") {
			t.Errorf("failure = %+v", f)
		}
	}
	c := rep.Cases["fib25"]
	if c.Tier["quick"] != nil {
		t.Fatalf("quick should have no samples, got %+v", c.Tier["quick"])
	}
	if c.Tier["off"] == nil || c.Tier["auto"] == nil {
		t.Fatalf("off/auto must keep samples: %+v", c.Tier)
	}
}

func TestRunnerCapturesJITStats(t *testing.T) {
	var log []string
	r := &runner{
		binary: "bin/aluka", scripts: []string{"tests/benchmark/perf-compare.js"},
		tiers: []string{"off", "auto"}, reps: 1, rotate: true,
		timeout: time.Minute, jitStats: true,
		exec: fakeExec(&log, func(tier string) (string, time.Duration, error) {
			line := fmt.Sprintf("JIT stats: mode=%s threshold=1000\n", tier)
			return line + "fib25: 10.00\n", time.Millisecond, nil
		}),
	}
	rep, err := r.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byTier := rep.JITStats["tests/benchmark/perf-compare.js"]
	if byTier == nil {
		t.Fatal("script JIT stats missing")
	}
	if !strings.HasPrefix(byTier["off"], "JIT stats: mode=off") {
		t.Errorf("off stats = %q", byTier["off"])
	}
	if !strings.HasPrefix(byTier["auto"], "JIT stats: mode=auto") {
		t.Errorf("auto stats = %q", byTier["auto"])
	}
}

func TestParseTiers(t *testing.T) {
	got, err := parseTiers("off, quick ,auto")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"off", "quick", "auto"}) {
		t.Fatalf("got %v", got)
	}
	if _, err := parseTiers("off,opt"); err == nil {
		t.Fatal("expected error for invalid tier")
	}
	if _, err := parseTiers(""); err == nil {
		t.Fatal("expected error for empty tier list")
	}
}

func TestRunnerNoRotationWhenDisabled(t *testing.T) {
	var log []string
	r := &runner{
		binary: "bin/aluka", scripts: []string{"tests/benchmark/perf-compare.js"},
		tiers: []string{"off", "quick", "auto"}, reps: 2, rotate: false,
		timeout: time.Minute, exec: fakeExec(&log, func(tier string) (string, time.Duration, error) {
			return "fib25: 10.00\n", time.Millisecond, nil
		}),
	}
	if _, err := r.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"off", "quick", "auto", "off", "quick", "auto"}
	if !reflect.DeepEqual(log, want) {
		t.Fatalf("invocation order = %v, want %v", log, want)
	}
}

// validReport returns a report that satisfies the R0-4 format contract.
func validReport() *report {
	rep := &report{
		SchemaVersion: archiveSchemaVersion,
		Generated:     time.Now().UTC(),
		Commit:        "fbe9b5e",
		Platform:      "windows/amd64",
		CPU:           "test cpu",
		GoVersion:     "go1.25",
		AlukaVersion:  "0.1.0-dev",
		Config: runConfig{
			Binary: "bin/aluka", Scripts: []string{"tests/benchmark/perf-compare.js"},
			Tiers: []string{"off", "quick", "auto"}, Reps: 2, Rotate: true, Timeout: "1m0s",
		},
		Cases: map[string]*caseReport{
			"fib25": {Script: "tests/benchmark/perf-compare.js", Tier: map[string]*tierReport{
				"off":   {Samples: []sample{{Round: 0, Ms: 10}, {Round: 1, Ms: 12}}, MedianMs: 11, MinMs: 10, MaxMs: 12, MeanMs: 11, MadPct: 1},
				"quick": {Samples: []sample{{Round: 0, Ms: 6}, {Round: 1, Ms: 5}}, MedianMs: 5.5, MinMs: 5, MaxMs: 6, MeanMs: 5.5, MadPct: 1},
				"auto":  {Samples: []sample{{Round: 0, Ms: 2}, {Round: 1, Ms: 3}}, MedianMs: 2.5, MinMs: 2, MaxMs: 3, MeanMs: 2.5, MadPct: 1},
			}},
		},
	}
	rep.Summary = buildSummary(rep)
	return rep
}

func TestBuildSummary(t *testing.T) {
	s := buildSummary(validReport())
	fib, ok := s.Cases["fib25"]
	if !ok {
		t.Fatal("summary missing case fib25")
	}
	off, ok := fib.Tier["off"]
	if !ok || off.MedianMs != 11 || off.VsOff != 1 {
		t.Fatalf("off summary = %+v, want median 11 vsOff 1", off)
	}
	if got := fib.Tier["quick"].VsOff; math.Abs(got-2.0) > 1e-9 {
		t.Errorf("quick vsOff = %v, want 2.0", got)
	}
	if got := fib.Tier["auto"].VsOff; math.Abs(got-4.4) > 1e-9 {
		t.Errorf("auto vsOff = %v, want 4.4", got)
	}
}

func TestValidateReport(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*report)
		want   string // substring of the expected error; "" means valid
	}{
		{"valid", func(r *report) {}, ""},
		{"schema version", func(r *report) { r.SchemaVersion = "2" }, "schema version"},
		{"no tiers", func(r *report) { r.Config.Tiers = nil }, "no tiers"},
		{"reps zero", func(r *report) { r.Config.Reps = 0 }, "reps 0"},
		{"no generated timestamp", func(r *report) { r.Generated = time.Time{} }, "generated timestamp"},
		{"no commit", func(r *report) { r.Commit = "" }, "commit"},
		{"no platform", func(r *report) { r.Platform = "" }, "platform"},
		{"no cpu", func(r *report) { r.CPU = "" }, "cpu"},
		{"no aluka version", func(r *report) { r.AlukaVersion = "" }, "alukaVersion"},
		{"no binary", func(r *report) { r.Config.Binary = "" }, "binary"},
		{"no scripts", func(r *report) { r.Config.Scripts = nil }, "scripts"},
		{"no timeout", func(r *report) { r.Config.Timeout = "" }, "timeout"},
		{"no cases", func(r *report) { r.Cases = nil }, "no cases"},
		{"unknown tier", func(r *report) {
			r.Cases["fib25"].Tier["turbo"] = &tierReport{Samples: []sample{{0, 1}}, MedianMs: 1, MinMs: 1, MaxMs: 1, MeanMs: 1}
		}, "unknown tier"},
		{"samples more than reps", func(r *report) {
			r.Cases["fib25"].Tier["off"].Samples = append(r.Cases["fib25"].Tier["off"].Samples, sample{Round: 2, Ms: 13})
		}, "more than reps"},
		{"samples less than reps without failure", func(r *report) {
			r.Cases["fib25"].Tier["off"].Samples = r.Cases["fib25"].Tier["off"].Samples[:1]
		}, "no failure"},
		{"missing tier without failure", func(r *report) {
			delete(r.Cases["fib25"].Tier, "quick")
		}, "missing tier"},
		{"missing tier explained by failure", func(r *report) {
			delete(r.Cases["fib25"].Tier, "quick")
			r.Failures = append(r.Failures, failure{Script: "tests/benchmark/perf-compare.js", Tier: "quick", Round: 0, Error: "boom"})
			r.Failures = append(r.Failures, failure{Script: "tests/benchmark/perf-compare.js", Tier: "quick", Round: 1, Error: "boom"})
		}, ""},
		{"missing tier only partly explained", func(r *report) {
			delete(r.Cases["fib25"].Tier, "quick")
			r.Failures = append(r.Failures, failure{Script: "s.js", Tier: "quick", Round: 0, Error: "boom"})
		}, "missing tier"},
		{"missing tier explained by other script", func(r *report) {
			delete(r.Cases["fib25"].Tier, "quick")
			r.Failures = append(r.Failures,
				failure{Script: "other.js", Tier: "quick", Round: 0, Error: "boom"},
				failure{Script: "other.js", Tier: "quick", Round: 1, Error: "boom"})
		}, "missing tier"},
		{"negative sample", func(r *report) {
			r.Cases["fib25"].Tier["off"].Samples[0].Ms = -1
		}, "invalid"},
		{"summary missing case", func(r *report) {
			r.Summary.Cases["other"] = caseSummary{}
			delete(r.Summary.Cases, "fib25")
		}, "summary missing case"},
		{"incomplete failure", func(r *report) {
			r.Failures = append(r.Failures, failure{Script: "s.js", Tier: "auto"})
		}, "incomplete"},
		{"failure round out of range", func(r *report) {
			r.Failures = append(r.Failures, failure{Script: "s.js", Tier: "auto", Round: 5, Error: "boom"})
		}, "out of range"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep := validReport()
			tc.mutate(rep)
			err := validateReport(rep)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("validateReport = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateReport = %v, want error containing %q", err, tc.want)
			}
		})
	}
	if err := validateReport(nil); err == nil {
		t.Fatal("validateReport(nil) = nil, want error")
	}
}

func TestValidateArchiveRoundTrip(t *testing.T) {
	r := &runner{
		binary: "bin/aluka", scripts: []string{"tests/benchmark/perf-compare.js", "tests/benchmark/mixed.js"},
		tiers: []string{"off", "auto"}, reps: 2, rotate: true,
		timeout: time.Minute,
		exec: func(ctx context.Context, name string, args ...string) (string, string, time.Duration, error) {
			script := ""
			for _, a := range args {
				if !strings.HasPrefix(a, "--") {
					script = a
				}
			}
			if strings.HasSuffix(script, "perf-compare.js") {
				return "fib25: 10.00\n", "", time.Millisecond, nil
			}
			return "", "", 20 * time.Millisecond, nil // mixed.js: wall-clock mode
		},
	}
	rep, err := r.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rep.Generated = time.Now().UTC()
	rep.Commit = "fbe9b5e"
	rep.Platform = "windows/amd64"
	rep.CPU = "test cpu"
	rep.GoVersion = "go1.25"
	rep.AlukaVersion = "0.1.0-dev"
	if err := validateReport(rep); err != nil {
		t.Fatalf("fresh archive invalid: %v", err)
	}
	path := filepath.Join(t.TempDir(), "jit-archive.json")
	if err := writeReport(path, rep); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("reload archive: %v", err)
	}
	if err := validateReport(&decoded); err != nil {
		t.Fatalf("reloaded archive invalid: %v", err)
	}
	if decoded.SchemaVersion != archiveSchemaVersion {
		t.Errorf("schemaVersion = %q, want %q", decoded.SchemaVersion, archiveSchemaVersion)
	}
	if decoded.Cases["fib25"].Tier["off"].MedianMs != 10 {
		t.Errorf("fib25/off median = %v, want 10", decoded.Cases["fib25"].Tier["off"].MedianMs)
	}
	if decoded.Cases["mixed.js"] == nil {
		t.Fatal("wall-clock case mixed.js missing after round trip")
	}
	if decoded.Summary.Cases["fib25"].Tier["auto"].VsOff != 1 {
		t.Errorf("fib25/auto vsOff = %v, want 1 (same median as off)", decoded.Summary.Cases["fib25"].Tier["auto"].VsOff)
	}
}

func TestDefaultArchiveName(t *testing.T) {
	got := defaultArchiveName(time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC))
	want := "jit-20260811-" + runtime.GOOS + "-" + runtime.GOARCH + ".json"
	if got != want {
		t.Fatalf("archive name = %q, want %q", got, want)
	}
}

func TestBuildBinaryCreatesParentDirectory(t *testing.T) {
	oldWorkingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Clean(filepath.Join(oldWorkingDir, "..", "..", ".."))
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWorkingDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	target := filepath.Join(t.TempDir(), "nested", "aluka"+map[bool]string{true: ".exe"}[runtime.GOOS == "windows"])
	if err := buildBinary(target); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(target); err != nil || info.IsDir() {
		t.Fatalf("built binary stat = (%v, %v), want regular file", info, err)
	}
}
