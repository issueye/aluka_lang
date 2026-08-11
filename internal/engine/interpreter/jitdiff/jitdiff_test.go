package jitdiff

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/jit"
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

// TestArtifactRoundTripExceptionExit saves and replays the R1-4 exception-exit
// fixed case: the artifact must carry the throw-bearing source (the pending
// exception configuration) and replay to identical results in all tiers.
func TestArtifactRoundTripExceptionExit(t *testing.T) {
	var c *Case
	for _, candidate := range FixedCases() {
		if candidate.ID == -18 {
			c = candidate
			break
		}
	}
	if c == nil {
		t.Fatal("exception-exit fixed case -18 not found")
	}
	c.applySource()
	results, err := RunCase(c, c.Params)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Result != c.Expected {
		t.Fatalf("off result:\n%s\nwant:\n%s", results[0].Result, c.Expected)
	}
	dir := t.TempDir()
	// Inject a synthetic first-observation mismatch; SaveArtifact reruns only
	// to capture IR and must not overwrite this original result.
	results[1].Result = results[1].Result + "\nsynthetic-mismatch"
	art, err := SaveArtifact(dir, &Mismatch{Case: c, Results: results}, c.Params)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadArtifact(art.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Source, "throw") {
		t.Fatalf("artifact source lost the throw (pending exception config): %q", loaded.Source)
	}
	results2, reproduced, err := loaded.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if reproduced {
		t.Fatalf("exception-exit case reported as reproduced mismatch: %+v", results2)
	}
	for _, res := range results2 {
		if res.Result != c.Expected {
			t.Fatalf("replay tier %s result:\n%s\nwant:\n%s", res.Tier, res.Result, c.Expected)
		}
	}
}

// TestArtifactRoundTripSideEffect proves the R1-5 side-effect fixed cases flow
// through the existing failure-artifact and single-command replay machinery:
// a synthetic mismatch on the call-guard + property-write case (-19) saves a
// faithful artifact (source, hook config) whose replay reproduces the exact
// expected event log in every tier.
func TestArtifactRoundTripSideEffect(t *testing.T) {
	var c *Case
	for _, candidate := range FixedCases() {
		if candidate.ID == -19 {
			c = candidate
			break
		}
	}
	if c == nil {
		t.Fatal("side-effect fixed case -19 not found")
	}
	c.applySource()
	results, err := RunCase(c, c.Params)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Result != c.Expected {
		t.Fatalf("off result:\n%s\nwant:\n%s", results[0].Result, c.Expected)
	}
	dir := t.TempDir()
	results[1].Result = results[1].Result + "\nsynthetic-mismatch"
	art, err := SaveArtifact(dir, &Mismatch{Case: c, Results: results}, c.Params)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadArtifact(art.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Source, "THROWER") || !strings.Contains(loaded.Source, "o.a = i") {
		t.Fatalf("artifact source lost the side-effect scenario: %q", loaded.Source)
	}
	results2, reproduced, err := loaded.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if reproduced {
		t.Fatalf("side-effect case reported as reproduced mismatch: %+v", results2)
	}
	for _, res := range results2 {
		if res.Result != c.Expected {
			t.Fatalf("replay tier %s result:\n%s\nwant:\n%s", res.Tier, res.Result, c.Expected)
		}
	}
}

// TestGuardMutationFixedCases is the R1-6 fixed regression gate: every guard
// mutation family (-24..-31) must produce the exact expected off event log,
// be identical across off/quick/auto, and — critically — must actually hit
// its target guard inside the JIT: the warmup compiled and the mutation
// produced a real GuardFailures count in both quick and auto, proving the
// case is not merely Tier 0 behavior.
func TestGuardMutationFixedCases(t *testing.T) {
	for _, c := range FixedCases() {
		if c.Kind != KindGuardMutation {
			continue
		}
		c.applySource()
		t.Run(fmt.Sprintf("%02d", -c.ID), func(t *testing.T) {
			results, err := RunCase(c, c.Params)
			if err != nil {
				if mismatch, ok := err.(*Mismatch); ok {
					t.Fatalf("cross-tier mismatch: %v", mismatch)
				}
				t.Fatalf("infrastructure error: %v", err)
			}
			if results[0].Result != c.Expected {
				t.Fatalf("off event log:\n%s\nwant:\n%s", results[0].Result, c.Expected)
			}
			compiled := func(s jit.Stats) bool { return s.TracesCompiled > 0 || s.Compiled > 0 }
			quick, auto := results[1].Stats, results[2].Stats
			if !compiled(quick) || quick.GuardFailures == 0 {
				t.Fatalf("quick did not hit the mutated guard (compiled+warmup required): %+v", quick)
			}
			if !compiled(auto) || auto.GuardFailures == 0 {
				t.Fatalf("auto did not hit the mutated guard (compiled+warmup required): %+v", auto)
			}
		})
	}
}

// TestGeneratorProducesGuardMutationCases proves the random generator emits
// guard-mutation cases whose warmup / mutation / post-mutation schedule is
// embedded in the case source, so seed, source and mutation schedule travel
// together in the artifact and replay exactly.
func TestGeneratorProducesGuardMutationCases(t *testing.T) {
	g := NewGenerator(prSeed, prParams())
	count := 0
	for _, c := range g.Generate(200) {
		if c.Kind != KindGuardMutation {
			continue
		}
		count++
		// The schedule is fully embedded: warmup call, a mutation statement,
		// and post-mutation calls are all in the source.
		if !strings.Contains(c.Source, "LOG(\"call\"") || !strings.Contains(c.Source, "LOG(\"return\"") {
			t.Fatalf("guard-mutation case %d lost its call schedule:\n%s", c.ID, c.Source)
		}
	}
	if count == 0 {
		t.Fatal("generator produced no guard-mutation cases in 200 samples")
	}
}

// TestArtifactRoundTripGuardMutation proves the R1-6 mutation schedule
// survives the failure-artifact round trip: a synthetic mismatch on the
// third-shape case (-24) saves the source with its mutation statements, and
// the single-command replay reproduces the exact expected event log.
func TestArtifactRoundTripGuardMutation(t *testing.T) {
	var c *Case
	for _, candidate := range FixedCases() {
		if candidate.ID == -24 {
			c = candidate
			break
		}
	}
	if c == nil {
		t.Fatal("guard-mutation fixed case -24 not found")
	}
	c.applySource()
	results, err := RunCase(c, c.Params)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Result != c.Expected {
		t.Fatalf("off result:\n%s\nwant:\n%s", results[0].Result, c.Expected)
	}
	dir := t.TempDir()
	results[1].Result = results[1].Result + "\nsynthetic-mismatch"
	art, err := SaveArtifact(dir, &Mismatch{Case: c, Results: results}, c.Params)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadArtifact(art.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Source, "S3") || !strings.Contains(loaded.Source, "kS(S1") {
		t.Fatalf("artifact source lost the mutation schedule (third shape / warmup): %q", loaded.Source)
	}
	results2, reproduced, err := loaded.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if reproduced {
		t.Fatalf("guard-mutation case reported as reproduced mismatch: %+v", results2)
	}
	for _, res := range results2 {
		if res.Result != c.Expected {
			t.Fatalf("replay tier %s result:\n%s\nwant:\n%s", res.Tier, res.Result, c.Expected)
		}
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

// TestGeneratedCorpusIncludesPrimitiveOpKinds proves the R3-4/R3-5 generator
// emits the new String/BigInt operation kinds in the random corpus.
func TestGeneratedCorpusIncludesPrimitiveOpKinds(t *testing.T) {
	g := NewGenerator(prSeed, prParams())
	kinds := make(map[Kind]int)
	for _, c := range g.Generate(400) {
		kinds[c.Kind]++
	}
	for _, k := range []Kind{KindStringOps, KindBigIntArith, KindBigIntBitwise, KindBigIntCompare} {
		if kinds[k] == 0 {
			t.Fatalf("generator produced no %s cases in 400 samples", k)
		}
	}
}

// TestPrimitiveOpsFixedCasesHitQuick proves the R3-4/R3-5 fixed cases are not
// Tier 0 sleight of hand: Quick must compile and execute each one, and the
// mixed-type/exception shapes must produce real guard failures that fall back
// to Tier 0.
func TestPrimitiveOpsFixedCasesHitQuick(t *testing.T) {
	needsFallback := map[int]bool{-32: true, -34: true, -35: true, -36: true, -37: true, -38: true}
	for _, c := range FixedCases() {
		if c.Kind != KindStringOps && c.Kind != KindBigIntArith &&
			c.Kind != KindBigIntBitwise && c.Kind != KindBigIntCompare {
			continue
		}
		c.applySource()
		t.Run(fmt.Sprintf("%02d", -c.ID), func(t *testing.T) {
			results, err := RunCase(c, c.Params)
			if err != nil {
				if mismatch, ok := err.(*Mismatch); ok {
					t.Fatalf("cross-tier mismatch: %v", mismatch)
				}
				t.Fatalf("infrastructure error: %v", err)
			}
			quick := results[1].Stats
			if quick.Compiled+quick.TracesCompiled == 0 || quick.Executed+quick.TracesExecuted == 0 {
				t.Fatalf("case %d did not compile+execute in Quick: %+v", c.ID, quick)
			}
			if needsFallback[c.ID] && quick.GuardFailures == 0 {
				t.Fatalf("case %d produced no guard failure (mixed/exception fallback missing): %+v", c.ID, quick)
			}
		})
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
