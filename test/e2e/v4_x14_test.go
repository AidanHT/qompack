// V4 §4.14 — every registered contract assertion either has a declared producer in the bound
// Services, or reports an explicit unknown/unsupported state with a reason.
//
// RETIRED CLAUSE (reconciliation map §4.14, row V4-SP05-03, migration disposition 4): the
// historical "nine assertions, zero without a producer" FIXED COUNT is retired. This row enumerates
// the LIVE registry — contract.StandardAssertions() — and reconciles whatever it finds. Adding a
// tenth assertion must not fail it, and deleting one must not make it pass vacuously; the guard
// below asserts the registry is non-empty and that every member is accounted for by name.
//
// Wired: a real daemon composed exactly as internal/cli composes it (v4_harness_test.go). Its
// daemon.New calls DeclareProducers over the Services its own binds populated, and
// contract.HasProducer is process-global, so what this row reads IS what the composition root
// declared — not a list this test wrote.
package e2e

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// x14v4Session is this row's session identity.
const x14v4Session = core.SessionID("sess-e2e-v4-x14")

// x14v4NotYetImplemented is §12.1's frozen Observed string for an assertion whose producer is
// absent from the build. It is matched literally by test/guards and by the contract golden fixture,
// so it is a contract, not a message.
const x14v4NotYetImplemented = "not-yet-implemented"

// x14v4Violations is the reconciliation itself, factored out so it can be run BOTH ways: over the
// live registry, where it must find nothing, and over a registry deliberately holding an assertion
// with no producer and no unsupported state, where it must find exactly that one.
//
// The rule: for every assertion, either its producer is declared in this build, or its Result says
// explicitly that it could not be observed. An assertion that is neither produced nor explained is
// a silent false OK — the failure mode this whole row exists to catch.
func x14v4Violations(assertions []contract.Assertion, results map[contract.ID]contract.Result) []string {
	var bad []string
	for _, a := range assertions {
		res, ran := results[a.ID]
		switch {
		case !ran:
			bad = append(bad, string(a.ID)+": registered but produced no Result")
		case contract.HasProducer(a.ID):
			// A declared producer: the real Check ran, and whatever it observed is a real
			// observation. Nothing further is owed.
		case res.Observed == x14v4NotYetImplemented:
			// No producer, and the assertion says so in the frozen §12.1 spelling.
		case res.Observed == "":
			bad = append(bad, string(a.ID)+": no declared producer and no explanation at all")
		default:
			bad = append(bad, string(a.ID)+
				": no declared producer, and its observation ("+res.Observed+
				") does not report an unknown/unsupported state")
		}
	}
	return bad
}

// TestV4_EveryContractAssertionHasARealProducer is V4-VERIFY §4.14.
func TestV4_EveryContractAssertionHasARealProducer(t *testing.T) {
	ctx := context.Background()
	p := v4Project(t)

	// The composition root runs FIRST: daemon.New is what calls DeclareProducers over the Services
	// its binds populated, and that is the fact this row reads back.
	r := v4StartRig(t, p)
	env := e2eEnv(p)
	obsRunHook(t, r.Bin, []string{"session-start"}, sessionStartFor(t, p.Root, x14v4Session), env)
	obsRunHook(t, r.Bin, []string{"observe", "prompt"},
		obsPromptPayload(t, p.Root, x14v4Session, "reconcile the contract registry against its producers"), env)
	r.SeedTurns(t, x14v4Session, "v4x14", 2)

	// ── The LIVE registry, enumerated rather than listed ─────────────────────────────────────────
	assertions := contract.StandardAssertions()
	require.NotEmpty(t, assertions,
		"the live assertion registry must not be empty, or the reconciliation below is vacuous")
	seen := map[contract.ID]bool{}
	for _, a := range assertions {
		require.NotEmpty(t, a.ID, "every registered assertion must carry an ID")
		require.NotNil(t, a.Check, "every registered assertion must carry a Check")
		require.NotEmpty(t, a.Description, "every registered assertion must describe itself")
		require.False(t, seen[a.ID], "assertion %s is registered twice", a.ID)
		seen[a.ID] = true
	}

	// ── Run them through the REAL monitor over the REAL project, with the REAL bound Services ────
	mon := contract.NewMonitor(p.Log, r.Opts.Metrics,
		filepath.Join(paths.Of(p.Root).State, "v4x14-contract.json"))
	for _, a := range assertions {
		require.NoError(t, mon.Register(a), "registering %s", a.ID)
	}
	results, _ := mon.RunAll(ctx, contract.Env{
		ProjectRoot: p.Root,
		Cfg:         p.Cfg,
		Store:       r.Opts.Store,
		Log:         p.Log,
		Clock:       p.Clock,
	})
	require.Len(t, results, len(assertions),
		"RunAll must return one Result per registered assertion")

	byID := make(map[contract.ID]contract.Result, len(results))
	for _, res := range results {
		byID[res.ID] = res
		require.NotEmpty(t, res.Expected, "%s must state what the host contract promised", res.ID)
		require.NotEmpty(t, res.Observed, "%s must state what was actually seen", res.ID)
	}

	// At least one producer must have been declared by the composition root, or "every assertion
	// is explained by not-yet-implemented" would pass on a build that wires nothing at all.
	declared := 0
	for _, a := range assertions {
		if contract.HasProducer(a.ID) {
			declared++
		}
	}
	require.Positive(t, declared,
		"the shipped composition root must declare at least one producer; contract.DeclareProducers "+
			"runs inside daemon.New and this row composed a real daemon before reading it")

	require.Empty(t, x14v4Violations(assertions, byID),
		"every registered assertion must either have a declared producer in the bound Services, or "+
			"report an explicit unknown/unsupported state with a reason")

	// The reconciliation is REPORTED, not counted: the fixed nine-assertion claim is retired, so
	// the row records what it found rather than asserting a number.
	t.Logf("v4 §4.14: %d assertions registered, %d with a declared producer, %d reporting %s",
		len(assertions), declared, len(assertions)-declared, x14v4NotYetImplemented)

	// ── NEGATIVE CONTROL: an assertion with no producer and no unsupported state ─────────────────
	//
	// The same reconciliation, run over a registry holding one such assertion, must name it. This
	// is the mechanism-removal arm: if x14v4Violations were vacuous — always returning nil — the
	// assertion above would pass for the wrong reason, and this one would fail.
	const orphan = contract.ID("v4.x14.orphan")
	require.False(t, contract.HasProducer(orphan), "the control assertion must have no producer")

	orphaned := append(append([]contract.Assertion(nil), assertions...), contract.Assertion{
		ID: orphan, Severity: contract.SevWarn, Description: "an assertion nobody produces",
		Check: func(context.Context, contract.Env) contract.Result { return contract.Result{ID: orphan} },
	})
	orphanResults := make(map[contract.ID]contract.Result, len(byID)+1)
	for id, res := range byID {
		orphanResults[id] = res
	}
	orphanResults[orphan] = contract.Result{
		ID: orphan, OK: true, Severity: contract.SevInfo,
		Expected: "an assertion nobody produces", Observed: "ok",
	}

	violations := x14v4Violations(orphaned, orphanResults)
	require.Len(t, violations, 1,
		"NEGATIVE CONTROL: exactly the unproduced, unexplained assertion must be reported: %v",
		violations)
	require.Contains(t, violations[0], string(orphan))

	// And the other shape of the same failure: registered, but producing no Result at all.
	silent := x14v4Violations(orphaned, byID)
	require.Len(t, silent, 1, "an assertion that produces no Result must also be reported: %v", silent)
	require.Contains(t, silent[0], "produced no Result")
}
