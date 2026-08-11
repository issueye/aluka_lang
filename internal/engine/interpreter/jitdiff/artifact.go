package jitdiff

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultArtifactRoot returns <repo root>/bench/results/jitdiff. The repo
// root is located by walking up from the current working directory until
// go.mod is found, so the path is correct whether the framework runs under
// `go test` (package dir) or from the repository root.
func DefaultArtifactRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return filepath.Join("bench", "results", "jitdiff")
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return filepath.Join(dir, "bench", "results", "jitdiff")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Join("bench", "results", "jitdiff")
}

// Artifact is the self-contained differential failure record (R1-8). It
// carries the generator version, seed, generation parameters, the failing
// source, per-tier results with IR dumps and JIT stats, and a single command
// that replays the failure.
type Artifact struct {
	GeneratorVersion string       `json:"generatorVersion"`
	Seed             int64        `json:"seed"`
	Params           Params       `json:"params"`
	CaseID           int          `json:"caseId"`
	Kind             string       `json:"kind"`
	Body             string       `json:"body"`
	Source           string       `json:"source"`
	Results          []TierResult `json:"results"` // off first
	ReproCommand     string       `json:"reproCommand"`
	Dir              string       `json:"-"`
}

// ReproCommand builds the single command that replays an artifact directory
// with the framework's replay test.
func ReproCommand(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	return fmt.Sprintf("go test ./internal/engine/interpreter/jitdiff -run 'TestReplayFailure' -artifact %q -count=1", abs)
}

// SaveArtifact captures the failing case (rerunning each tier with IR
// capture) and writes the artifact directory. It returns the Artifact with
// its ReproCommand filled in.
func SaveArtifact(baseDir string, mismatch *Mismatch, params Params) (*Artifact, error) {
	if mismatch == nil || mismatch.Case == nil || len(mismatch.Results) != len(Tiers) {
		return nil, fmt.Errorf("jitdiff: invalid mismatch artifact input")
	}
	if !tierResultsDiffer(mismatch.Results) {
		return nil, fmt.Errorf("jitdiff: artifact input contains no differential mismatch")
	}
	params = params.Normalized()
	results := append([]TierResult(nil), mismatch.Results...)
	for i, tier := range Tiers {
		captured, err := RunTier(mismatch.Case, tier, params, true)
		if err != nil {
			return nil, err
		}
		// Preserve the first failing observation. IR capture requires a rerun,
		// which may no longer reproduce a timing-sensitive mismatch.
		results[i].IR = captured.IR
	}
	art := &Artifact{
		GeneratorVersion: Version,
		Seed:             mismatch.Case.Seed,
		Params:           params,
		CaseID:           mismatch.Case.ID,
		Kind:             mismatch.Case.Kind.String(),
		Body:             mismatch.Case.Body,
		Source:           mismatch.Case.Source,
		Results:          results,
	}
	dir := filepath.Join(baseDir, fmt.Sprintf("jitdiff-%d-%s", mismatch.Case.Seed, mismatch.Case.Kind))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	art.Dir = dir
	art.ReproCommand = ReproCommand(dir)

	if err := os.WriteFile(filepath.Join(dir, "case.js"), []byte(mismatch.Case.Source), 0o644); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(art, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), append(data, '\n'), 0o644); err != nil {
		return nil, err
	}
	// A human-readable summary for quick triage.
	var b strings.Builder
	b.WriteString("jitdiff differential mismatch artifact\n")
	b.WriteString(fmt.Sprintf("generatorVersion=%s seed=%d caseID=%d kind=%s\n", Version, art.Seed, art.CaseID, art.Kind))
	b.WriteString(mismatch.Error())
	b.WriteString("\n\nreproduce with:\n  " + art.ReproCommand + "\n")
	if err := os.WriteFile(filepath.Join(dir, "SUMMARY.txt"), []byte(b.String()), 0o644); err != nil {
		return nil, err
	}
	return art, nil
}

// LoadArtifact reads a saved artifact directory.
func LoadArtifact(dir string) (*Artifact, error) {
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil, err
	}
	var art Artifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, err
	}
	art.Dir = dir
	return &art, nil
}

// Replay re-runs the artifact's case across all tiers and returns the results
// plus whether the recorded failure still reproduces.
func (a *Artifact) Replay() ([]TierResult, bool, error) {
	c := &Case{ID: a.CaseID, Kind: parseKind(a.Kind), Seed: a.Seed, Params: a.Params, Body: a.Body, Source: a.Source}
	results := make([]TierResult, 0, len(Tiers))
	for _, tier := range Tiers {
		res, err := RunTier(c, tier, a.Params, false)
		if err != nil {
			return results, false, err
		}
		results = append(results, res)
	}
	off := results[0]
	for i := 1; i < len(results); i++ {
		if results[i].EvalErr != off.EvalErr || results[i].Result != off.Result {
			return results, true, nil
		}
	}
	return results, false, nil
}

func tierResultsDiffer(results []TierResult) bool {
	if len(results) < 2 {
		return false
	}
	off := results[0]
	for i := 1; i < len(results); i++ {
		if results[i].EvalErr != off.EvalErr || results[i].Result != off.Result {
			return true
		}
	}
	return false
}

func parseKind(s string) Kind {
	for i := 0; i < KindCount; i++ {
		if Kind(i).String() == s {
			return Kind(i)
		}
	}
	return KindExpr
}
