// Command jitbench is the unified three-tier JIT benchmark entry (R0-2) and
// the R0-4 result archive producer.
//
// It builds the aluka CLI once (when the binary is missing or -rebuild is
// set), runs the benchmark scripts under --jit=off / --jit=quick / --jit=auto
// in a rotated order across repetitions, and reports the raw samples, the
// per-case median, and the dispersion (relative MAD) for every (case, tier).
// With -out it writes a versioned JSON archive (R0-4 format contract) that is
// validated before being saved; the naming convention is
// `bench/results/jit-<YYYYMMDD>-<goos>-<goarch>.json`.
//
// Usage (from the repository root):
//
//	go run ./bench/cmd/jitbench -reps 5 -out bench/results
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "jitbench:", err)
		os.Exit(1)
	}
}

// sample is one raw measurement: the wall-clock milliseconds of one case in
// one tier during one repetition round.
type sample struct {
	Round int     `json:"round"`
	Ms    float64 `json:"ms"`
}

type tierReport struct {
	Samples  []sample `json:"samples"`
	MedianMs float64  `json:"medianMs"`
	MinMs    float64  `json:"minMs"`
	MaxMs    float64  `json:"maxMs"`
	MeanMs   float64  `json:"meanMs"`
	MadPct   float64  `json:"madPct"`
}

type caseReport struct {
	Script string                 `json:"script,omitempty"`
	Tier   map[string]*tierReport `json:"tier"`
}

type failure struct {
	Script string `json:"script"`
	Tier   string `json:"tier"`
	Round  int    `json:"round"`
	Error  string `json:"error"`
}

type runConfig struct {
	Binary   string   `json:"binary"`
	Scripts  []string `json:"scripts"`
	Tiers    []string `json:"tiers"`
	Reps     int      `json:"reps"`
	Rotate   bool     `json:"rotate"`
	Timeout  string   `json:"timeout"`
	JITStats bool     `json:"jitStats"`
}

// archiveSchemaVersion is the version of the R0-4 result archive format.
// Bumping it requires a corresponding change to validateReport and the R0-4
// format contract in docs/jit-follow-up-development-plan.md.
const archiveSchemaVersion = "1"

// report is the JSON-serializable result archive (R0-4). It carries the run
// parameters (config), the environment/version (platform, cpu, goVersion,
// alukaVersion, commit), the statistics (cases + summary) and the failure
// reasons (failures). SchemaVersion identifies the format contract; validateReport
// enforces it before an archive is written.
type report struct {
	SchemaVersion string                       `json:"schemaVersion"`
	Generated     time.Time                    `json:"generated"`
	Commit        string                       `json:"commit,omitempty"`
	Platform      string                       `json:"platform"`
	CPU           string                       `json:"cpu"`
	GoVersion     string                       `json:"goVersion"`
	AlukaVersion  string                       `json:"alukaVersion,omitempty"`
	Config        runConfig                    `json:"config"`
	Cases         map[string]*caseReport       `json:"cases"`
	Summary       summary                      `json:"summary"`
	JITStats      map[string]map[string]string `json:"jitStats,omitempty"` // script -> tier -> stats line
	Failures      []failure                    `json:"failures,omitempty"`
}

// summary is the archive digest: for every (case, tier) the median and the
// speedup relative to --jit=off (off median / tier median; 1 for off itself).
type summary struct {
	Cases map[string]caseSummary `json:"cases"`
}

type caseSummary struct {
	Tier map[string]tierSummary `json:"tier"`
}

type tierSummary struct {
	MedianMs float64 `json:"medianMs"`
	VsOff    float64 `json:"vsOff,omitempty"` // omitted when the ratio is undefined (zero medians)
}

// buildSummary computes the digest after all medians have been finalized.
func buildSummary(rep *report) summary {
	s := summary{Cases: make(map[string]caseSummary)}
	for name, c := range rep.Cases {
		cs := caseSummary{Tier: make(map[string]tierSummary)}
		offMedian := 0.0
		if off := c.Tier["off"]; off != nil {
			offMedian = off.MedianMs
		}
		for tier, tr := range c.Tier {
			ts := tierSummary{MedianMs: tr.MedianMs}
			if tier == "off" {
				ts.VsOff = 1
			} else if offMedian != 0 && tr.MedianMs != 0 {
				ts.VsOff = offMedian / tr.MedianMs
			}
			cs.Tier[tier] = ts
		}
		s.Cases[name] = cs
	}
	return s
}

// validateReport checks an archive against the R0-4 format contract before it
// is written. A report is valid only when every (case, tier) either carries
// exactly the configured number of raw samples or has a recorded failure that
// explains the missing measurements.
func validateReport(rep *report) error {
	if rep == nil {
		return errors.New("archive: nil report")
	}
	if rep.SchemaVersion != archiveSchemaVersion {
		return fmt.Errorf("archive: schema version %q, want %q", rep.SchemaVersion, archiveSchemaVersion)
	}
	if len(rep.Config.Tiers) == 0 {
		return errors.New("archive: no tiers configured")
	}
	if rep.Config.Reps < 1 {
		return fmt.Errorf("archive: reps %d < 1", rep.Config.Reps)
	}
	if rep.Generated.IsZero() {
		return errors.New("archive: generated timestamp is required")
	}
	if rep.Commit == "" || rep.Platform == "" || rep.CPU == "" || rep.GoVersion == "" || rep.AlukaVersion == "" {
		return errors.New("archive: commit, platform, cpu, goVersion, and alukaVersion are required")
	}
	if rep.Config.Binary == "" || len(rep.Config.Scripts) == 0 || rep.Config.Timeout == "" {
		return errors.New("archive: binary, scripts, and timeout are required")
	}
	if len(rep.Cases) == 0 {
		return errors.New("archive: no cases measured")
	}
	validTiers := make(map[string]bool, len(rep.Config.Tiers))
	for _, tier := range rep.Config.Tiers {
		switch tier {
		case "off", "quick", "auto":
			validTiers[tier] = true
		default:
			return fmt.Errorf("archive: invalid tier %q in config", tier)
		}
	}
	for name, c := range rep.Cases {
		if name == "" {
			return errors.New("archive: empty case name")
		}
		if len(c.Tier) == 0 {
			return fmt.Errorf("archive: case %q has no tier results", name)
		}
		for tier, tr := range c.Tier {
			if !validTiers[tier] {
				return fmt.Errorf("archive: case %q has unknown tier %q", name, tier)
			}
			if tr == nil || len(tr.Samples) == 0 {
				return fmt.Errorf("archive: case %q tier %q has no samples", name, tier)
			}
			if len(tr.Samples) > rep.Config.Reps {
				return fmt.Errorf("archive: case %q tier %q has %d samples, more than reps %d", name, tier, len(tr.Samples), rep.Config.Reps)
			}
			if missingRound := firstUnexplainedRound(tr.Samples, rep.Failures, c.Script, tier, rep.Config.Reps); missingRound >= 0 {
				return fmt.Errorf("archive: case %q tier %q has %d samples (< reps %d) but no failure recorded", name, tier, len(tr.Samples), rep.Config.Reps)
			}
			for _, s := range tr.Samples {
				if s.Ms < 0 || math.IsNaN(s.Ms) || math.IsInf(s.Ms, 0) {
					return fmt.Errorf("archive: case %q tier %q sample round %d invalid: %v", name, tier, s.Round, s.Ms)
				}
			}
			if tr.MedianMs < 0 || math.IsNaN(tr.MedianMs) || math.IsInf(tr.MedianMs, 0) ||
				tr.MinMs < 0 || tr.MaxMs < 0 || tr.MeanMs < 0 || math.IsNaN(tr.MadPct) {
				return fmt.Errorf("archive: case %q tier %q has invalid summary: %+v", name, tier, tr)
			}
		}
		for _, tier := range rep.Config.Tiers {
			if c.Tier[tier] == nil && firstUnexplainedRound(nil, rep.Failures, c.Script, tier, rep.Config.Reps) >= 0 {
				return fmt.Errorf("archive: case %q missing tier %q without a failure record", name, tier)
			}
		}
	}
	if len(rep.Summary.Cases) == 0 {
		return errors.New("archive: empty summary")
	}
	for name := range rep.Cases {
		if _, ok := rep.Summary.Cases[name]; !ok {
			return fmt.Errorf("archive: summary missing case %q", name)
		}
	}
	for i, f := range rep.Failures {
		if f.Script == "" || f.Tier == "" || f.Error == "" {
			return fmt.Errorf("archive: failure %d is incomplete: %+v", i, f)
		}
		if f.Round < 0 || f.Round >= rep.Config.Reps {
			return fmt.Errorf("archive: failure %d round %d out of range [0,%d)", i, f.Round, rep.Config.Reps)
		}
	}
	return nil
}

func firstUnexplainedRound(samples []sample, failures []failure, script, tier string, reps int) int {
	covered := make([]bool, reps)
	for _, s := range samples {
		if s.Round >= 0 && s.Round < reps {
			covered[s.Round] = true
		}
	}
	for _, f := range failures {
		if script != "" && f.Script == script && f.Tier == tier && f.Round >= 0 && f.Round < reps {
			covered[f.Round] = true
		}
	}
	for round, ok := range covered {
		if !ok {
			return round
		}
	}
	return -1
}

// execFunc runs one CLI invocation and returns stdout, stderr, the wall-clock
// duration and any error. It is abstracted so tests can inject a fake.
type execFunc func(ctx context.Context, name string, args ...string) (stdout, stderr string, wall time.Duration, err error)

type runner struct {
	binary   string
	scripts  []string
	tiers    []string
	reps     int
	rotate   bool
	timeout  time.Duration
	jitStats bool

	exec execFunc
}

// run executes the whole benchmark suite and aggregates per-case samples.
func (r *runner) run(ctx context.Context) (*report, error) {
	rep := &report{
		SchemaVersion: archiveSchemaVersion,
		Config: runConfig{
			Binary: r.binary, Scripts: append([]string(nil), r.scripts...),
			Tiers: append([]string(nil), r.tiers...), Reps: r.reps,
			Rotate: r.rotate, Timeout: r.timeout.String(), JITStats: r.jitStats,
		},
		Cases:    make(map[string]*caseReport),
		JITStats: make(map[string]map[string]string),
	}
	for round := 0; round < r.reps; round++ {
		order := r.tiers
		if r.rotate {
			order = rotatedTiers(r.tiers, round)
		}
		for _, tier := range order {
			for _, script := range r.scripts {
				invCtx, cancel := context.WithTimeout(ctx, r.timeout)
				args := []string{"--jit=" + tier}
				if r.jitStats {
					args = append(args, "--jit-stats")
				}
				args = append(args, script)
				stdout, stderr, wall, err := r.exec(invCtx, r.binary, args...)
				cancel()
				if err != nil {
					detail := strings.TrimSpace(stderr)
					if detail == "" {
						detail = err.Error()
					} else {
						detail = err.Error() + ": " + detail
					}
					rep.Failures = append(rep.Failures, failure{
						Script: script, Tier: tier, Round: round, Error: detail,
					})
					continue
				}
				parsed := parseBenchmarkOutput(stdout)
				if parsed.hasBench {
					for name, ms := range parsed.cases {
						r.addSample(rep, script, name, tier, round, ms)
					}
				} else {
					// The script emits no per-case lines (e.g. mixed.js):
					// the whole-process wall-clock is the sample.
					r.addSample(rep, script, filepath.Base(script), tier, round,
						float64(wall)/float64(time.Millisecond))
				}
				if r.jitStats && parsed.jitStats != "" {
					if rep.JITStats[script] == nil {
						rep.JITStats[script] = make(map[string]string)
					}
					rep.JITStats[script][tier] = parsed.jitStats
				}
			}
		}
	}
	for _, c := range rep.Cases {
		for _, tr := range c.Tier {
			raw := make([]float64, len(tr.Samples))
			for i, s := range tr.Samples {
				raw[i] = s.Ms
			}
			sum := summarize(raw)
			tr.MedianMs, tr.MinMs, tr.MaxMs, tr.MeanMs, tr.MadPct =
				sum.Median, sum.Min, sum.Max, sum.Mean, sum.MadPct
		}
	}
	rep.Summary = buildSummary(rep)
	return rep, nil
}

func (r *runner) addSample(rep *report, script, caseName, tier string, round int, ms float64) {
	c := rep.Cases[caseName]
	if c == nil {
		c = &caseReport{Script: script, Tier: make(map[string]*tierReport)}
		rep.Cases[caseName] = c
	}
	tr := c.Tier[tier]
	if tr == nil {
		tr = &tierReport{}
		c.Tier[tier] = tr
	}
	tr.Samples = append(tr.Samples, sample{Round: round, Ms: ms})
}

func commandExec(ctx context.Context, name string, args ...string) (string, string, time.Duration, error) {
	start := time.Now()
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	wall := time.Since(start)
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("timeout after %v", wall)
	}
	return stdout.String(), stderr.String(), wall, err
}

func printReport(w io.Writer, rep *report) {
	fmt.Fprintf(w, "jitbench: aluka JIT three-tier benchmark\n")
	fmt.Fprintf(w, "  generated=%s  commit=%s\n",
		rep.Generated.Format(time.RFC3339), rep.Commit)
	fmt.Fprintf(w, "  platform=%s  cpu=%s  go=%s  aluka=%s\n",
		rep.Platform, rep.CPU, rep.GoVersion, rep.AlukaVersion)
	fmt.Fprintf(w, "  binary=%s\n", rep.Config.Binary)
	fmt.Fprintf(w, "  scripts=%v\n  tiers=%v  reps=%d  rotate=%v  timeout=%s\n",
		rep.Config.Scripts, rep.Config.Tiers, rep.Config.Reps, rep.Config.Rotate, rep.Config.Timeout)
	fmt.Fprintln(w)
	names := make([]string, 0, len(rep.Cases))
	for name := range rep.Cases {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(w, "%s\n", name)
		c := rep.Cases[name]
		offMedian := 0.0
		if off := c.Tier["off"]; off != nil {
			offMedian = off.MedianMs
		}
		for _, tier := range rep.Config.Tiers {
			tr := c.Tier[tier]
			if tr == nil {
				fmt.Fprintf(w, "  %-6s no samples\n", tier)
				continue
			}
			if tier == "off" || offMedian == 0 || tr.MedianMs == 0 {
				fmt.Fprintf(w, "  %-6s median=%8.2fms  min=%8.2f  max=%8.2f  mean=%8.2f  mad=%.1f%%  samples=%v\n",
					tier, tr.MedianMs, tr.MinMs, tr.MaxMs, tr.MeanMs, tr.MadPct, formatSamples(tr.Samples))
				continue
			}
			fmt.Fprintf(w, "  %-6s median=%8.2fms  vsOff=%5.2fx  min=%8.2f  max=%8.2f  mean=%8.2f  mad=%.1f%%  samples=%v\n",
				tier, tr.MedianMs, offMedian/tr.MedianMs, tr.MinMs, tr.MaxMs, tr.MeanMs, tr.MadPct, formatSamples(tr.Samples))
		}
	}
	if len(rep.JITStats) > 0 {
		fmt.Fprintln(w, "\nJIT stats (per script/tier):")
		for script, byTier := range rep.JITStats {
			for tier, line := range byTier {
				fmt.Fprintf(w, "  %s [%s]: %s\n", script, tier, line)
			}
		}
	}
	if len(rep.Failures) > 0 {
		fmt.Fprintln(w, "\nfailures:")
		for _, f := range rep.Failures {
			fmt.Fprintf(w, "  script=%s tier=%s round=%d: %s\n", f.Script, f.Tier, f.Round, f.Error)
		}
	}
}

func formatSamples(samples []sample) string {
	parts := make([]string, len(samples))
	for i, s := range samples {
		parts[i] = fmt.Sprintf("%.2f", s.Ms)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func defaultBinaryPath() string {
	name := "bin/aluka"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func buildBinary(path string) error {
	fmt.Fprintf(os.Stderr, "jitbench: building %s (go build ./cmd/aluka)\n", path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create binary directory: %w", err)
	}
	cmd := exec.Command("go", "build", "-o", path, "./cmd/aluka")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build failed: %v\n%s", err, out)
	}
	return nil
}

func parseTiers(s string) ([]string, error) {
	var out []string
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		switch t {
		case "off", "quick", "auto":
			out = append(out, t)
		default:
			return nil, fmt.Errorf("invalid tier %q (want off, quick, or auto)", t)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no tiers selected")
	}
	return out, nil
}

func gitShortCommit() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func binaryVersion(binary string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--version")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func cpuInfo() string {
	if s := os.Getenv("PROCESSOR_IDENTIFIER"); s != "" {
		return s
	}
	return fmt.Sprintf("%d CPUs", runtime.NumCPU())
}

func run(args []string) error {
	fs := flag.NewFlagSet("jitbench", flag.ContinueOnError)
	var scripts stringList
	binary := fs.String("binary", "", "path to the aluka CLI binary (default: bin/aluka[.exe] in the working directory)")
	rebuild := fs.Bool("rebuild", false, "rebuild the CLI binary even if it already exists")
	skipBuild := fs.Bool("skip-build", false, "never build; fail if the binary is missing")
	fs.Var(&scripts, "script", "benchmark script to run (repeatable; default: tests/benchmark/perf-compare.js and mixed.js)")
	reps := fs.Int("reps", 5, "raw samples per case per tier")
	tiers := fs.String("tiers", "off,quick,auto", "comma-separated tiers to run")
	rotate := fs.Bool("rotate", true, "rotate the tier order across rounds")
	timeout := fs.Duration("timeout", 2*time.Minute, "per-invocation timeout")
	jitStatsOut := fs.Bool("jit-stats", false, "pass --jit-stats to the CLI and record the stats line")
	outPath := fs.String("out", "", "write the JSON result archive to this path or directory (directory uses the R0-4 name jit-<date>-<platform>.json)")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: jitbench [flags]\n\n")
		fmt.Fprintf(fs.Output(), "Runs the JIT three-tier benchmark suite (off/quick/auto) against one\n")
		fmt.Fprintf(fs.Output(), "build of the aluka CLI and reports raw samples, medians and dispersion.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *reps < 1 {
		return fmt.Errorf("-reps must be >= 1, got %d", *reps)
	}
	tierList, err := parseTiers(*tiers)
	if err != nil {
		return err
	}
	if len(scripts) == 0 {
		scripts = stringList{"tests/benchmark/perf-compare.js", "tests/benchmark/mixed.js"}
	}
	if *binary == "" {
		*binary = defaultBinaryPath()
	}
	abs, err := filepath.Abs(*binary)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(abs); statErr != nil || *rebuild {
		if *skipBuild {
			return fmt.Errorf("binary %s is missing and -skip-build is set", abs)
		}
		if err := buildBinary(abs); err != nil {
			return err
		}
	}
	*binary = abs

	r := &runner{
		binary: *binary, scripts: append([]string(nil), scripts...),
		tiers: tierList, reps: *reps, rotate: *rotate,
		timeout: *timeout, jitStats: *jitStatsOut, exec: commandExec,
	}
	rep, err := r.run(context.Background())
	if err != nil {
		return err
	}
	rep.Generated = time.Now().UTC()
	rep.Commit = gitShortCommit()
	rep.Platform = runtime.GOOS + "/" + runtime.GOARCH
	rep.CPU = cpuInfo()
	rep.GoVersion = runtime.Version()
	rep.AlukaVersion = binaryVersion(*binary)
	if err := validateReport(rep); err != nil {
		return err
	}

	printReport(os.Stdout, rep)
	if *outPath != "" {
		if info, statErr := os.Stat(*outPath); statErr == nil && info.IsDir() {
			// The R0-4 convention names archives jit-<date>-<platform>.json;
			// allow -out to point at the results directory.
			*outPath = filepath.Join(*outPath, defaultArchiveName(rep.Generated))
		}
		if err := writeReport(*outPath, rep); err != nil {
			return err
		}
	}
	return nil
}

// defaultArchiveName is the R0-4 file naming convention
// `jit-<YYYYMMDD>-<goos>-<goarch>.json`.
func defaultArchiveName(t time.Time) string {
	return fmt.Sprintf("jit-%s-%s-%s.json", t.Format("20060102"), runtime.GOOS, runtime.GOARCH)
}

func writeReport(path string, rep *report) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("jitbench: create output directory: %w", err)
		}
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return fmt.Errorf("jitbench: encode report: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("jitbench: write report: %w", err)
	}
	fmt.Fprintf(os.Stderr, "jitbench: wrote %s\n", path)
	return nil
}
