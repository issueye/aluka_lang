package jitdiff

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
)

// replayArtifact is the -artifact flag used by TestReplayFailure to replay a
// saved differential failure with a single command (R1-8).
var replayArtifact = flag.String("artifact", "", "replay a saved jitdiff failure artifact directory")

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}

// prSeed is the fixed seed of the PR quick set (>= 1,000 samples).
const prSeed = 0x2026_0811

func prParams() Params {
	return Params{Seed: prSeed, MaxExprDepth: 3, MaxLoopBound: 24, TraceBudget: 3}
}

// TestGeneratorDeterminism verifies that identical seeds and params produce
// byte-identical cases and that different seeds diverge.
func TestGeneratorDeterminism(t *testing.T) {
	params := prParams()
	a := NewGenerator(42, params).Generate(50)
	b := NewGenerator(42, params).Generate(50)
	if len(a) != len(b) {
		t.Fatalf("case counts differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Source != b[i].Source || a[i].Kind != b[i].Kind || a[i].Seed != b[i].Seed {
			t.Fatalf("case %d differs between identical seeds", i)
		}
	}
	c := NewGenerator(43, params).Generate(50)
	diverged := false
	for i := range a {
		if a[i].Source != c[i].Source {
			diverged = true
			break
		}
	}
	if !diverged {
		t.Fatal("different seeds produced identical output")
	}
}

// TestValueDomainCoverage verifies the R1-2 value domain is actually present
// in the generated corpus: Number edges (NaN/Infinity/-0/subnormal/2^53+),
// Boolean, null, undefined, String, BigInt, Symbol identity and object
// identity.
func TestGeneratedCorpusIncludesValueLeaves(t *testing.T) {
	cases := NewGenerator(prSeed, prParams()).Generate(600)
	var all strings.Builder
	for _, c := range cases {
		all.WriteString(c.Source)
		all.WriteByte('\n')
	}
	text := all.String()
	required := []string{
		"NaN", "Infinity", "-Infinity", "(-0)", "1e-320", "9007199254740993",
		"true", "false", "null", "undefined",
		`"a"`, `"ab"`, "7n", "0n",
		"SYM1", "SYM2", "OBJ_A", "OBJ_B",
	}
	for _, token := range required {
		if !strings.Contains(text, token) {
			t.Errorf("generated corpus (%d cases) missing value %q", 600, token)
		}
	}
}

func TestValueDomainOperationCoverage(t *testing.T) {
	cases := valueDomainCases()
	if len(cases) != len(valueDomainSpecs()) {
		t.Fatalf("value-domain cases=%d specs=%d", len(cases), len(valueDomainSpecs()))
	}
	wantOps := strings.Join(valueDomainOperations, ",")
	seen := make(map[string]bool, len(cases))
	var guardFailures uint64
	for _, c := range cases {
		if c.ValueDomain == "" || seen[c.ValueDomain] {
			t.Fatalf("missing or duplicate value domain %q", c.ValueDomain)
		}
		seen[c.ValueDomain] = true
		if got := strings.Join(c.Coverage, ","); got != wantOps {
			t.Fatalf("domain %s coverage=%q, want %q", c.ValueDomain, got, wantOps)
		}
		results, err := RunCase(c, c.Params)
		if err != nil {
			t.Fatalf("domain %s: %v", c.ValueDomain, err)
		}
		guardFailures += uint64(results[1].Stats.GuardFailures + results[2].Stats.GuardFailures)
	}
	if guardFailures == 0 {
		t.Fatal("structured value-domain corpus did not exercise any runtime guard failure")
	}
}

// TestDifferentialPRSet is the PR gate: a fixed-seed run of 1,000 generated
// samples across off/quick/auto with Tier 0 as the oracle. Any mismatch fails
// and is saved as a reproducible artifact under bench/results/jitdiff.
func TestDifferentialPRSet(t *testing.T) {
	const count = 1000
	sum, err := RunSuite(prSeed, prParams(), count, DefaultArtifactRoot())
	if err != nil {
		t.Fatalf("%v", err)
	}
	if sum.QuickExecuted == 0 {
		t.Fatalf("no Quick execution across %d cases; generator may not exercise the JIT: %+v", count, sum)
	}
	if sum.NativeExecuted == 0 {
		t.Fatalf("no Native execution across %d cases; generator may not exercise Native: %+v", count, sum)
	}
	for _, kind := range AllKinds {
		if kind.ExpectsQuickHit() && sum.QuickHitsByKind[kind.String()] == 0 {
			t.Errorf("hit kind %s had no Quick executions: %+v", kind, sum.QuickHitsByKind)
		}
		if kind.ExpectsNativeHit() && sum.NativeHitsByKind[kind.String()] == 0 {
			t.Errorf("hit kind %s had no Native executions: %+v", kind, sum.NativeHitsByKind)
		}
	}
	nativeKinds := 0
	for _, hits := range sum.NativeHitsByKind {
		if hits > 0 {
			nativeKinds++
		}
	}
	if nativeKinds < 3 {
		t.Fatalf("Native executions covered only %d kinds: %+v", nativeKinds, sum.NativeHitsByKind)
	}
	t.Logf("PR set summary: total=%d quickHit=%d autoNativeHit=%d quickExecuted=%d nativeExecuted=%d guardFailures=%d tracesCompiled=%d",
		sum.Total, sum.QuickHit, sum.AutoNativeHit, sum.QuickExecuted, sum.NativeExecuted, sum.GuardFailures, sum.TracesCompiled)
	t.Logf("PR set tier hits: quick=%v native=%v", sum.QuickHitsByKind, sum.NativeHitsByKind)
}

// TestDifferentialNightly is the nightly gate: 100,000 generated samples from
// multiple fixed seeds. It is gated behind JITDIFF_NIGHTLY=1 so the PR suite
// stays fast; the one-time completion run records the results in the plan docs.
func TestDifferentialNightly(t *testing.T) {
	if os.Getenv("JITDIFF_NIGHTLY") == "" {
		t.Skip("set JITDIFF_NIGHTLY=1 to run the 100,000-case nightly differential")
	}
	seeds := []int64{0x2026_0811, 0x2026_0812, 0x2026_0813, 0x2026_0814, 0x2026_0815}
	const perSeed = 20000
	for _, seed := range seeds {
		params := prParams()
		params.Seed = seed
		sum, err := RunSuite(seed, params, perSeed, DefaultArtifactRoot())
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		t.Logf("seed %d: total=%d quickHit=%d autoNativeHit=%d quickExecuted=%d nativeExecuted=%d guardFailures=%d",
			seed, sum.Total, sum.QuickHit, sum.AutoNativeHit, sum.QuickExecuted, sum.NativeExecuted, sum.GuardFailures)
	}
}

// TestEventLogFixedCases verifies the deterministic event-log coverage: for
// each event category (property write, array append, upvalue write, function
// call, getter/setter, callback throw, try/catch, safepoint-yield deopt
// prefix, Symbol identity, BigInt TypeError, loose equality fallback) the
// fixed case must produce the exact expected off-mode event log AND identical
// logs across all three tiers.
func TestEventLogFixedCases(t *testing.T) {
	for _, c := range FixedCases() {
		c.applySource()
		t.Run(fmt.Sprintf("%02d-%s", -c.ID, c.Kind), func(t *testing.T) {
			results, err := RunCase(c, c.Params)
			if err != nil {
				if mismatch, ok := err.(*Mismatch); ok {
					t.Fatalf("cross-tier mismatch: %v", mismatch)
				}
				t.Fatalf("infrastructure error: %v", err)
			}
			if c.ExpectedErr != "" {
				// Top-level exception propagation: all tiers must raise the
				// same normalized error (the fixed case records the off one).
				if got := NormalizedErr(results[0].EvalErr); got != c.ExpectedErr {
					t.Fatalf("off eval error = %q, want %q", got, c.ExpectedErr)
				}
				for i := 1; i < len(results); i++ {
					if NormalizedErr(results[i].EvalErr) != c.ExpectedErr {
						t.Fatalf("tier %s eval error = %q, want %q", results[i].Tier, NormalizedErr(results[i].EvalErr), c.ExpectedErr)
					}
				}
				return
			}
			if got := results[0].Result; got != c.Expected {
				t.Fatalf("off event log:\n%s\nwant:\n%s", got, c.Expected)
			}
			for i := 1; i < len(results); i++ {
				if results[i].Result != results[0].Result {
					t.Fatalf("tier %s event log differs from off:\n%s", results[i].Tier, results[i].Result)
				}
			}
		})
	}
}

// TestArtifactRoundTrip verifies that a saved artifact loads back and replays.
func TestArtifactRoundTrip(t *testing.T) {
	c := FixedCases()[0]
	c.applySource()
	results, err := RunCase(c, c.Params)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// Inject a synthetic first-observation mismatch. SaveArtifact reruns only
	// to capture IR and must not overwrite this original result.
	results[1].Result = results[1].Result + "\nsynthetic-mismatch"
	art, err := SaveArtifact(dir, &Mismatch{Case: c, Results: results}, c.Params)
	if err != nil {
		t.Fatal(err)
	}
	if art.ReproCommand == "" || !strings.Contains(art.ReproCommand, "TestReplayFailure") {
		t.Fatalf("repro command missing or wrong: %q", art.ReproCommand)
	}
	loaded, err := LoadArtifact(art.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Seed != c.Seed || loaded.Kind != c.Kind.String() || loaded.Source != c.Source ||
		loaded.GeneratorVersion != Version {
		t.Fatalf("artifact round-trip mismatch: %+v", loaded)
	}
	if len(loaded.Results) != len(Tiers) {
		t.Fatalf("artifact has %d results, want %d", len(loaded.Results), len(Tiers))
	}
	if !strings.Contains(loaded.Results[1].Result, "synthetic-mismatch") {
		t.Fatalf("artifact lost original mismatch: %+v", loaded.Results[1])
	}
	// Off mode never compiles (it is the oracle), so it has no IR dump; the
	// quick and auto tiers must have captured IR for this compilable case.
	if loaded.Results[1].IR == "" || loaded.Results[2].IR == "" {
		t.Fatalf("artifact quick/auto tiers lack IR dumps: quick=%d bytes auto=%d bytes",
			len(loaded.Results[1].IR), len(loaded.Results[2].IR))
	}
	results2, reproduced, err := loaded.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if reproduced {
		t.Fatalf("passing case reported as reproduced mismatch: %+v", results2)
	}
}

func TestSaveArtifactRejectsPassingResults(t *testing.T) {
	c := FixedCases()[0]
	c.applySource()
	results, err := RunCase(c, c.Params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SaveArtifact(t.TempDir(), &Mismatch{Case: c, Results: results}, c.Params); err == nil {
		t.Fatal("SaveArtifact accepted results without a mismatch")
	}
}

func TestRunTierHonorsVerify(t *testing.T) {
	c := FixedCases()[0]
	c.applySource()
	params := c.Params
	params.Verify = true
	result, err := RunTier(c, Tiers[2], params, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.VerifyChecks == 0 || result.Stats.VerifyFailures != 0 {
		t.Fatalf("Verify=true stats = %+v, want successful verify checks", result.Stats)
	}
}

// TestReplayFailure replays a saved artifact directory with a single command:
//
//	go test ./internal/engine/interpreter/jitdiff -run 'TestReplayFailure' -artifact <dir> -count=1
//
// It fails (loudly) when the recorded mismatch no longer reproduces.
func TestReplayFailure(t *testing.T) {
	if *replayArtifact == "" {
		t.Skip("no -artifact flag; replay is driven from a saved failure directory")
	}
	art, err := LoadArtifact(*replayArtifact)
	if err != nil {
		t.Fatal(err)
	}
	results, reproduced, err := art.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if !reproduced {
		t.Fatalf("recorded mismatch (seed=%d kind=%s) no longer reproduces:\noff=%q\nquick=%q\nauto=%q",
			art.Seed, art.Kind, results[0].Result, results[1].Result, results[2].Result)
	}
	t.Logf("reproduced mismatch seed=%d kind=%s: off=%q quick=%q auto=%q",
		art.Seed, art.Kind, results[0].Result, results[1].Result, results[2].Result)
}
