package jitdiff

import (
	"hash/fnv"
	"math/rand"
	"strings"
	"testing"

	"github.com/aluka-lang/aluka/internal/engine/jit"
)

// fuzzArtifactRand derives a deterministic PRNG from fuzz bytes.
func fuzzArtifactRand(data []byte) *rand.Rand {
	h := fnv.New64a()
	_, _ = h.Write(data)
	return rand.New(rand.NewSource(int64(h.Sum64())))
}

// fuzzArtifactCase derives a Case whose Body always comes from the R1-6
// mutation fixed cases (bounded, side-effect-safe programs) while every other
// metadata field — ID, Kind, Seed, Params, Expected, Hook — is randomized,
// together with the per-tier results and a random IR dump string. This keeps
// replay execution bounded while exercising SaveArtifact/LoadArtifact/Replay
// against arbitrary meta/IR/artifact mutations.
func fuzzArtifactCase(rng *rand.Rand) (*Case, []TierResult, Params) {
	// Body pool: bounded programs only (the R1-6 mutation fixed cases and
	// other short loops). The million-iteration hook cases are excluded so
	// every replay finishes in bounded time without relying on a hook.
	var pool []*Case
	for _, c := range FixedCases() {
		if !strings.Contains(c.Body, "1000000") {
			pool = append(pool, c)
		}
	}
	bodyCase := pool[rng.Intn(len(pool))]
	bodyCase.applySource()
	params := Params{
		Seed: 0, MaxExprDepth: 3, MaxLoopBound: 24,
		TraceBudget: uint32(rng.Intn(5)),
		Verify:      rng.Intn(2) == 0,
	}
	caseID := rng.Intn(4000) - 2000
	seed := rng.Int63()
	if rng.Intn(4) == 0 {
		caseID = 0
	}
	if rng.Intn(4) == 0 {
		seed = 0
	}
	c := &Case{
		ID:          caseID,
		Kind:        Kind(rng.Intn(KindCount)),
		Seed:        seed,
		Params:      params,
		Body:        bodyCase.Body,
		Source:      bodyCase.Source,
		Expected:    bodyCase.Expected,
		ExpectedErr: bodyCase.ExpectedErr,
		Coverage:    []string{"fuzz"},
	}
	if rng.Intn(2) == 0 {
		c.Hook = &RunHook{OOMBytes: 1 << 40, TriggerOOM: 1}
	}
	results := make([]TierResult, 3)
	results[0] = TierResult{Tier: "off", Result: "call:k\nreturn:n:1"}
	for i := 1; i < 3; i++ {
		results[i] = TierResult{Tier: Tiers[i].Name, Result: "call:k\nreturn:n:1\nsynthetic"}
		if rng.Intn(2) == 0 {
			results[i].EvalErr = "Error:boom"
		}
		results[i].Stats = jitStatsFromRand(rng)
		if rng.Intn(2) == 0 {
			results[i].IR = "params=0 locals=1\n0000  return_undef\n" + strings.Repeat("x", rng.Intn(64))
		}
	}
	return c, results, params
}

func jitStatsFromRand(rng *rand.Rand) jit.Stats {
	return jit.Stats{
		TracesCompiled: uint64(rng.Intn(8)),
		TracesExecuted: uint64(rng.Intn(8)),
		GuardFailures:  uint64(rng.Intn(64)),
		NativeCompiled: uint64(rng.Intn(8)),
		NativeExecuted: uint64(rng.Intn(8)),
		VerifyChecks:   uint64(rng.Intn(8)),
		VerifyFailures: uint64(rng.Intn(8)),
	}
}

// FuzzArtifactReplay fuzzes the failure-artifact save/load/replay machinery
// with random metadata, results and IR dumps around legal case bodies.
// SaveArtifact must reject malformed state with a controlled error,
// LoadArtifact must accept anything that was saved, and Replay must either
// reproduce the mismatch or finish cleanly — never panic, never hang (bodies
// are bounded) and never lose the seed/source metadata.
func FuzzArtifactReplay(f *testing.F) {
	f.Add([]byte("artifact-seed"))
	f.Add([]byte{0x00, 0x01, 0x02})
	f.Add([]byte("mutation-corpus"))
	f.Fuzz(func(t *testing.T, data []byte) {
		rng := fuzzArtifactRand(data)
		c, results, params := fuzzArtifactCase(rng)
		dir := t.TempDir()
		art, err := SaveArtifact(dir, &Mismatch{Case: c, Results: results}, params)
		if err != nil {
			return // controlled rejection of malformed artifact state
		}
		loaded, err := LoadArtifact(art.Dir)
		if err != nil {
			t.Fatalf("saved artifact must load: %v", err)
		}
		if loaded.Source != c.Source || loaded.Seed != c.Seed || loaded.CaseID != c.ID {
			t.Fatalf("artifact metadata changed: source=%q seed=%d case=%d, want source=%q seed=%d case=%d",
				loaded.Source, loaded.Seed, loaded.CaseID, c.Source, c.Seed, c.ID)
		}
		if !strings.Contains(loaded.Source, "function") {
			t.Fatalf("artifact source lost the case body: %q", loaded.Source)
		}
		_, _, err = loaded.Replay()
		_ = err // controlled replay error; no panic, no hang
	})
}
