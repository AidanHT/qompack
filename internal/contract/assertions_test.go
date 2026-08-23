package contract_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// wave1Producers are the five §12.1 assertions SP-05 always declares from wave 1 (task-4-spec.md):
// their producer exists in this build regardless of which later wave's subsystem has landed.
var wave1Producers = []contract.ID{
	contract.CSessionStartFires,
	contract.CSessionStartSourceCompact,
	contract.CHookPayloadShape,
	contract.CTranscriptReadable,
	contract.CPluginRootResolves,
}

// laterWaveIDs are the four §12.1 names as a later wave's subsystem: SP-10 (precompact.*), SP-11
// (hook.additional_context_delivered) and SP-13 (mcp.server_registered). None of them has a
// producer declared anywhere in this package's tests unless a specific test declares it itself
// (and cleans up with t.Cleanup(contract.ResetProducers)).
var laterWaveIDs = map[contract.ID]bool{
	contract.CPreCompactTiming:      true,
	contract.CPreCompactCustomInstr: true,
	contract.CAdditionalContext:     true,
	contract.CMCPRegistered:         true,
}

// declareWave1 declares exactly the five wave-1 producers, cleaning up with t.Cleanup so no test
// leaks a declared producer into another — including golden_test.go's frozen fixture, which depends
// on the process-wide producer set being empty.
func declareWave1(t *testing.T) {
	t.Helper()
	for _, id := range wave1Producers {
		contract.DeclareProducer(id)
	}
	t.Cleanup(contract.ResetProducers)
}

// assertionByID returns the StandardAssertions() entry with the given ID, failing the test if none
// matches. Tests that exercise one assertion's Check directly use this rather than an index, so a
// reordering of StandardAssertions() (which TestStandardAssertions_MatchTheNormativeTable would
// also catch) cannot silently point this test at the wrong assertion.
func assertionByID(t *testing.T, id contract.ID) contract.Assertion {
	t.Helper()
	for _, a := range contract.StandardAssertions() {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("no standard assertion %s", id)
	return contract.Assertion{}
}

// TestFreshBuildReportsModeFull is the CI test §12.1 explicitly demands (task-4-spec.md): with the
// five wave-1 producers declared (simulating what the daemon's startup will do, the next task) and
// a well-formed Event against an empty History, a fresh build reports ModeFull, and the four
// later-wave assertions still report OK/SevInfo/not-yet-implemented because their producer stays
// undeclared.
func TestFreshBuildReportsModeFull(t *testing.T) {
	declareWave1(t)
	t.Setenv("CLAUDE_PLUGIN_ROOT", "")

	m, _, _ := newTestMonitor(t)
	registerAll(t, m, contract.StandardAssertions())

	env := contract.Env{
		Clock:   newFakeClock(),
		History: &contract.SessionHistory{},
		Event: hookio.Event{
			HookEventName: "SessionStart", SessionID: "sess-1", CWD: "/tmp/project", Source: "startup",
		},
	}

	results, mode := m.RunAll(context.Background(), env)

	require.Equal(t, contract.ModeFull, mode, "a freshly built develop must report ModeFull (§12.1)")
	require.Len(t, results, 9)
	for _, r := range results {
		if !laterWaveIDs[r.ID] {
			continue
		}
		require.True(t, r.OK, "%s: a later-wave assertion with no declared producer must report OK", r.ID)
		require.Equal(t, contract.SevInfo, r.Severity, "%s: must OBSERVE SevInfo", r.ID)
		require.Equal(t, "not-yet-implemented", r.Observed, "%s", r.ID)
	}
}

// TestCriticalFailureDegrades asserts a real, gated, producer-declared assertion drives the same
// §12.1 degradation the mechanics test with synthetic fixedResult assertions: PreCompactWallMs'
// p99 landing at 100% of the timeout is a SevCritical fail even though CPreCompactTiming DECLARES
// SevWarn (task-4-spec.md's table).
func TestCriticalFailureDegrades(t *testing.T) {
	contract.DeclareProducer(contract.CPreCompactTiming)
	t.Cleanup(contract.ResetProducers)

	m, _, statePath := newTestMonitor(t)
	require.NoError(t, m.Register(assertionByID(t, contract.CPreCompactTiming)))
	loud := captureLoud(t)

	h := &contract.SessionHistory{PrecompactTimeoutMs: 2000, PrecompactWallMs: []int64{2000, 2000, 2000, 2000}}
	env := contract.Env{Clock: newFakeClock(), History: h}

	results, mode := m.RunAll(context.Background(), env)

	require.Equal(t, contract.ModeDegradedPassive, mode)
	require.Len(t, results, 1)
	require.Equal(t, contract.SevCritical, results[0].Severity,
		"p99 at 100%% of the timeout observes SevCritical even though the assertion DECLARES SevWarn")
	require.Len(t, loud(), 1)

	st := readState(t, statePath)
	require.Contains(t, st["reason"], string(contract.CPreCompactTiming))
	require.NotZero(t, st["since"])
}

// TestTwoCleanRunsRestore and TestOneCleanRunDoesNotRestore restate §12.1's recovery rule under the
// exact names task-4-spec.md's test table gives them; monitor_test.go's
// TestMonitor_TwoConsecutiveCleanRunsRestore already covers the mechanics in more depth, so these
// stay minimal.
func TestTwoCleanRunsRestore(t *testing.T) {
	m, _, _ := newTestMonitor(t)
	a, set := flippable(contract.CHookPayloadShape)
	require.NoError(t, m.Register(a))
	env := contract.Env{Clock: newFakeClock()}

	set(false)
	_, mode := m.RunAll(context.Background(), env)
	require.Equal(t, contract.ModeDegradedPassive, mode)

	set(true)
	loud := captureLoud(t)
	_, mode = m.RunAll(context.Background(), env)
	require.Equal(t, contract.ModeDegradedPassive, mode, "run 1: one clean run must not restore")

	_, mode = m.RunAll(context.Background(), env)
	require.Equal(t, contract.ModeFull, mode, "run 2: two consecutive clean runs must restore")
	require.Len(t, loud(), 1, "the restore itself must be Loud")
}

func TestOneCleanRunDoesNotRestore(t *testing.T) {
	m, _, _ := newTestMonitor(t)
	a, set := flippable(contract.CTranscriptReadable)
	require.NoError(t, m.Register(a))
	env := contract.Env{Clock: newFakeClock()}

	set(false)
	_, _ = m.RunAll(context.Background(), env)
	set(true)
	_, mode := m.RunAll(context.Background(), env)
	require.Equal(t, contract.ModeDegradedPassive, mode)
}

// TestRuntimeModeOffShortCircuits asserts runtime.mode == "off" returns (nil, ModeOff) without
// running a single registered assertion (task-4-spec.md step 1) — proved with a counting Check.
func TestRuntimeModeOffShortCircuits(t *testing.T) {
	m, _, _ := newTestMonitor(t)
	var calls int
	require.NoError(t, m.Register(contract.Assertion{
		ID: contract.CSessionStartFires, Severity: contract.SevCritical, Description: "x",
		Check: func(ctx context.Context, e contract.Env) contract.Result {
			calls++
			return contract.Result{ID: contract.CSessionStartFires, OK: true}
		},
	}))

	env := contract.Env{Clock: newFakeClock(), Cfg: config.Config{Runtime: config.RuntimeCfg{Mode: "off"}}}
	results, mode := m.RunAll(context.Background(), env)

	require.Nil(t, results)
	require.Equal(t, contract.ModeOff, mode)
	require.Zero(t, calls, "zero assertions must execute when runtime.mode = off")
}

// TestRuntimeModeOffForcesModeOff (Important I4 / ruling) asserts runtime.mode == "off" forces
// Mode() to ModeOff through the same in-memory mechanism "passive"/"full" use, not only RunAll's own
// return value: a daemon route that consults Mode() (and Mode().MayAct()) rather than RunAll's
// second return value must never act or record while the operator has switched Qompack off.
// Switching back to "auto" on the next RunAll must restore the persisted state machine's mode.
func TestRuntimeModeOffForcesModeOff(t *testing.T) {
	m, _, _ := newTestMonitor(t)
	require.NoError(t, m.Register(fixedResult(contract.CSessionStartFires, true, contract.SevInfo)))

	offEnv := contract.Env{Clock: newFakeClock(), Cfg: config.Config{Runtime: config.RuntimeCfg{Mode: "off"}}}
	results, mode := m.RunAll(context.Background(), offEnv)
	require.Nil(t, results)
	require.Equal(t, contract.ModeOff, mode)
	require.Equal(t, contract.ModeOff, m.Mode(), "Mode() must agree with what RunAll just returned")
	require.False(t, m.Mode().MayAct(), "an operator-off session must never be able to act")

	autoEnv := contract.Env{Clock: newFakeClock()}
	_, mode = m.RunAll(context.Background(), autoEnv)
	require.Equal(t, contract.ModeFull, mode, "switching back to auto must restore the natural state machine's mode")
	require.Equal(t, contract.ModeFull, m.Mode())
}

// TestRuntimeModePassiveForces asserts runtime.mode == "passive" forces ModeDegradedPassive even
// when every registered assertion is clean, and that the results are still populated (they are
// "for reporting", per task-4-spec.md step 2).
func TestRuntimeModePassiveForces(t *testing.T) {
	m, _, _ := newTestMonitor(t)
	require.NoError(t, m.Register(fixedResult(contract.CSessionStartFires, true, contract.SevInfo)))

	env := contract.Env{Clock: newFakeClock(), Cfg: config.Config{Runtime: config.RuntimeCfg{Mode: "passive"}}}
	results, mode := m.RunAll(context.Background(), env)

	require.Equal(t, contract.ModeDegradedPassive, mode)
	require.Len(t, results, 1)
	require.True(t, results[0].OK, "a forced mode does not fabricate a failing result")
	require.Equal(t, contract.ModeDegradedPassive, m.Mode(), "Mode() must reflect the forced override too")
}

// TestRuntimeModeFullForces is the symmetric escape hatch: runtime.mode == "full" forces ModeFull
// even over a naturally degraded run (task-4-spec.md step 3: "an escape hatch for a user whose host
// build breaks our detection").
func TestRuntimeModeFullForces(t *testing.T) {
	m, _, _ := newTestMonitor(t)
	require.NoError(t, m.Register(fixedResult(contract.CSessionStartFires, false, contract.SevCritical)))

	env := contract.Env{Clock: newFakeClock(), Cfg: config.Config{Runtime: config.RuntimeCfg{Mode: "full"}}}
	results, mode := m.RunAll(context.Background(), env)

	require.Equal(t, contract.ModeFull, mode)
	require.Len(t, results, 1)
	require.False(t, results[0].OK, "the underlying failure is still reported")
	require.Equal(t, contract.ModeFull, m.Mode())
}

// TestPanickingAssertionDoesNotDegrade asserts a panicking Check is recovered into
// OK:false/SevWarn/"assertion panicked" and never degrades the session (task-4-spec.md step 4).
func TestPanickingAssertionDoesNotDegrade(t *testing.T) {
	m, _, _ := newTestMonitor(t)
	require.NoError(t, m.Register(contract.Assertion{
		ID: contract.CSessionStartFires, Severity: contract.SevCritical, Description: "x",
		Check: func(ctx context.Context, e contract.Env) contract.Result {
			panic("boom")
		},
	}))

	results, mode := m.RunAll(context.Background(), contract.Env{Clock: newFakeClock()})

	require.Equal(t, contract.ModeFull, mode, "a panicking assertion must never degrade the session")
	require.Len(t, results, 1)
	require.False(t, results[0].OK)
	require.Equal(t, contract.SevWarn, results[0].Severity)
	require.Equal(t, "assertion panicked", results[0].Observed)
}

// TestSessionStartFiresNeedsTwoMisses drives the real checkSessionStartFires observation through
// two consecutive RunAll calls, each a DIFFERENT session (§12.1 is "absence across two SESSIONS"),
// with no marker ever written: the first miss reports OK (StartsWithoutMarker == 1), and only the
// second consecutive miss — in a different session — fails.
func TestSessionStartFiresNeedsTwoMisses(t *testing.T) {
	contract.DeclareProducer(contract.CSessionStartFires)
	t.Cleanup(contract.ResetProducers)

	m, _, _ := newTestMonitor(t)
	require.NoError(t, m.Register(assertionByID(t, contract.CSessionStartFires)))

	h := &contract.SessionHistory{SessionCount: 1}
	root := t.TempDir()

	env := contract.Env{
		Clock: newFakeClock(), ProjectRoot: root, History: h,
		Event: hookio.Event{SessionID: "sess-a"},
	}
	results, mode := m.RunAll(context.Background(), env)
	require.Equal(t, contract.ModeFull, mode)
	require.True(t, results[0].OK)
	require.Equal(t, 1, h.StartsWithoutMarker)

	h.SessionCount = 2
	env2 := contract.Env{
		Clock: newFakeClock(), ProjectRoot: root, History: h,
		Event: hookio.Event{SessionID: "sess-b"},
	}
	results, mode = m.RunAll(context.Background(), env2)
	require.Equal(t, contract.ModeDegradedPassive, mode)
	require.False(t, results[0].OK)
	require.Equal(t, contract.SevCritical, results[0].Severity)
	require.Equal(t, 2, h.StartsWithoutMarker)
}

// TestSessionStartFires_SameSessionRunTwiceDoesNotDoubleCount (Important I1) asserts a second
// RunAll inside the SAME session — a self-test synthesizing an Env, a daemon retry — does not
// double-count the miss toward the >= 2 degrade threshold: StartsWithoutMarker must stay at 1, not
// jump to 2, when Event.SessionID does not change between calls.
func TestSessionStartFires_SameSessionRunTwiceDoesNotDoubleCount(t *testing.T) {
	contract.DeclareProducer(contract.CSessionStartFires)
	t.Cleanup(contract.ResetProducers)

	h := &contract.SessionHistory{SessionCount: 3}
	env := contract.Env{
		Clock: newFakeClock(), ProjectRoot: t.TempDir(), History: h,
		Event: hookio.Event{SessionID: "sess-x"},
	}
	a := assertionByID(t, contract.CSessionStartFires)

	r := a.Check(context.Background(), env)
	require.True(t, r.OK)
	require.Equal(t, 1, h.StartsWithoutMarker)

	r = a.Check(context.Background(), env)
	require.True(t, r.OK, "a second RunAll within the SAME session must not double-count toward degrade")
	require.Equal(t, 1, h.StartsWithoutMarker)
}

// TestSessionStartFires_ConsecutiveAbsenceIsPerSession (Important I1) pins the reviewer-accepted
// consecutive-absence semantics end to end: miss (session A) / marker present (session B, resets
// the counter) / miss (session C) must leave StartsWithoutMarker == 1, not 2 — and does so WITHOUT
// hand-bumping History.SessionCount between calls, driving session progression only through
// Event.SessionID and a real marker.json the way the daemon actually will.
func TestSessionStartFires_ConsecutiveAbsenceIsPerSession(t *testing.T) {
	contract.DeclareProducer(contract.CSessionStartFires)
	t.Cleanup(contract.ResetProducers)

	m, _, _ := newTestMonitor(t)
	require.NoError(t, m.Register(assertionByID(t, contract.CSessionStartFires)))

	root := t.TempDir()
	h := &contract.SessionHistory{SessionCount: 5}

	// Miss: session "a", no marker at all yet.
	_, mode := m.RunAll(context.Background(), contract.Env{
		Clock: newFakeClock(), ProjectRoot: root, History: h, Event: hookio.Event{SessionID: "sess-a"},
	})
	require.Equal(t, contract.ModeFull, mode)
	require.Equal(t, 1, h.StartsWithoutMarker)

	// Marker present (left by session "a"'s terminal hook), session "b" observes it: resets to 0.
	require.NoError(t, contract.WriteMarker(root, "sess-a", 1))
	_, mode = m.RunAll(context.Background(), contract.Env{
		Clock: newFakeClock(), ProjectRoot: root, History: h, Event: hookio.Event{SessionID: "sess-b"},
	})
	require.Equal(t, contract.ModeFull, mode)
	require.Equal(t, 0, h.StartsWithoutMarker)

	// Miss again: session "c", genuinely distinct from "a" and "b" — no coincidental id reuse, so
	// this run's "absent" reading cannot be attributed to the marker HAPPENING to name the current
	// session rather than to no marker existing at all. The marker file itself is removed first:
	// under checkSessionStartFires's own rule ("marker exists AND names a session DIFFERENT from
	// the current one -> OK"), a marker naming any id other than "c" would itself read as
	// marker-found here, which is not the scenario this test is pinning.
	require.NoError(t, os.Remove(contract.MarkerPath(root)))
	_, mode = m.RunAll(context.Background(), contract.Env{
		Clock: newFakeClock(), ProjectRoot: root, History: h, Event: hookio.Event{SessionID: "sess-c"},
	})
	require.Equal(t, contract.ModeFull, mode, "one fresh miss must not degrade")
	require.Equal(t, 1, h.StartsWithoutMarker, "the streak must be 1, not 2 — the marker-present run reset it")
}

// TestSessionStartFires_EmptyProjectRootIsNoObservation (Important I2) asserts a half-filled Env —
// exactly what a self-test synthesizing an Env from persisted History might hand this Check — is
// read as "no observation yet", never as a failure, and mutates nothing: task-4-spec.md's nil
// tolerance paragraph is explicit that an omitted field must never fail, and must certainly never
// leave a persistent side effect behind.
func TestSessionStartFires_EmptyProjectRootIsNoObservation(t *testing.T) {
	contract.DeclareProducer(contract.CSessionStartFires)
	t.Cleanup(contract.ResetProducers)

	h := &contract.SessionHistory{SessionCount: 3}
	r := assertionByID(t, contract.CSessionStartFires).Check(context.Background(), contract.Env{
		Clock: newFakeClock(), History: h, Event: hookio.Event{SessionID: "sess-a"},
	})

	require.True(t, r.OK)
	require.Equal(t, "no observation yet", r.Observed)
	require.Zero(t, h.StartsWithoutMarker, "an empty ProjectRoot must mutate nothing")
	require.Empty(t, h.LastSessionID)
}

// TestSessionStartFires_DaemonPreWriteOfLastSessionIDDoesNotWedgeFutureCounting is fix round 2's
// NEW-1 regression test. LastSessionID (history.go) is now documented as OWNED exclusively by
// checkSessionStartFires — the daemon must never write it, and in particular must never pre-set it
// to the INCOMING session's own id before RunAll. This test simulates that exact ownership
// violation and proves two things: (1) the violation does suppress THIS session's miss — the check
// cannot distinguish "already counted this session" from "a caller wrote this field behind my
// back", which is precisely why the field is documented as off-limits — and (2) the violation does
// NOT permanently wedge the mechanism: consecutive-absence counting resumes correctly for every
// genuinely new session id afterward. A broken fix for I1 (e.g. one that stopped updating
// LastSessionID at all once it had ever been set, or that could never again tell two different
// sessions apart) would fail the second half of this test.
func TestSessionStartFires_DaemonPreWriteOfLastSessionIDDoesNotWedgeFutureCounting(t *testing.T) {
	contract.DeclareProducer(contract.CSessionStartFires)
	t.Cleanup(contract.ResetProducers)

	root := t.TempDir()
	h := &contract.SessionHistory{SessionCount: 3}
	a := assertionByID(t, contract.CSessionStartFires)

	// The forbidden daemon-style pre-write: LastSessionID set to the INCOMING session's own id
	// before the Check ever runs.
	h.LastSessionID = "sess-a"

	r := a.Check(context.Background(), contract.Env{
		Clock: newFakeClock(), ProjectRoot: root, History: h, Event: hookio.Event{SessionID: "sess-a"},
	})
	require.True(t, r.OK)
	require.Equal(t, 0, h.StartsWithoutMarker,
		"the pre-write makes this session's miss look already-counted — this is exactly the hazard LastSessionID's doc comment forbids the daemon from causing")

	// A genuinely NEW session id, still no marker: counting must still work correctly and must not
	// be permanently wedged by the earlier ownership violation.
	r = a.Check(context.Background(), contract.Env{
		Clock: newFakeClock(), ProjectRoot: root, History: h, Event: hookio.Event{SessionID: "sess-b"},
	})
	require.True(t, r.OK)
	require.Equal(t, 1, h.StartsWithoutMarker, "a genuinely new session id must still count correctly")

	// And a second consecutive real miss, a third distinct session, must still degrade normally —
	// the mechanism must have fully recovered, not merely limped along at a reduced count.
	r = a.Check(context.Background(), contract.Env{
		Clock: newFakeClock(), ProjectRoot: root, History: h, Event: hookio.Event{SessionID: "sess-c"},
	})
	require.False(t, r.OK, "the mechanism must have fully recovered: two real consecutive misses must still degrade")
	require.Equal(t, contract.SevCritical, r.Severity)
	require.Equal(t, 2, h.StartsWithoutMarker)
}

// TestSourceCompactEvaluatedOnFollowingStart calls checkSessionStartSourceCompact directly (via
// StandardAssertions()) with History.AwaitingCompactStart set and Event.Source == "startup": the
// assertion must fail naming what it expected and what it saw, and clear the flag regardless.
func TestSourceCompactEvaluatedOnFollowingStart(t *testing.T) {
	contract.DeclareProducer(contract.CSessionStartSourceCompact)
	t.Cleanup(contract.ResetProducers)

	h := &contract.SessionHistory{AwaitingCompactStart: true}
	env := contract.Env{Clock: newFakeClock(), History: h, Event: hookio.Event{Source: "startup"}}

	r := assertionByID(t, contract.CSessionStartSourceCompact).Check(context.Background(), env)

	require.False(t, r.OK)
	require.Equal(t, "compact", r.Expected)
	require.Equal(t, "startup", r.Observed)
	require.False(t, h.AwaitingCompactStart, "the flag must be cleared whichever way the check resolves")
}

// TestPreCompactTimeoutUnknownIsOK asserts a never-observed timeout (PrecompactTimeoutMs == 0)
// reports OK/"timeout-unknown" rather than a division against zero or a fabricated failure.
func TestPreCompactTimeoutUnknownIsOK(t *testing.T) {
	contract.DeclareProducer(contract.CPreCompactTiming)
	t.Cleanup(contract.ResetProducers)

	h := &contract.SessionHistory{}
	r := assertionByID(t, contract.CPreCompactTiming).Check(context.Background(),
		contract.Env{Clock: newFakeClock(), History: h})

	require.True(t, r.OK)
	require.Equal(t, "timeout-unknown", r.Observed)
}

// TestPreCompactTiming_NoSamplesIsOK (Minor M9) pins the "no-samples" arm that sits between the
// TimeoutMs==0 case and the p99 comparison: a known timeout with zero recorded wall-time samples
// reports OK, not a fabricated pass or a divide-by-zero. It is behaviourally equivalent to what
// percentileMs(nil, 99) == 0 would already produce (ratio 0, so OK either way), but the distinct
// Observed string is worth pinning so a future refactor cannot silently collapse it.
func TestPreCompactTiming_NoSamplesIsOK(t *testing.T) {
	contract.DeclareProducer(contract.CPreCompactTiming)
	t.Cleanup(contract.ResetProducers)

	h := &contract.SessionHistory{PrecompactTimeoutMs: 2000}
	r := assertionByID(t, contract.CPreCompactTiming).Check(context.Background(),
		contract.Env{Clock: newFakeClock(), History: h})

	require.True(t, r.OK)
	require.Equal(t, "no-samples", r.Observed)
}

// TestCustomInstructionsProbePhrase asserts the custom_instructions probe: a transcript whose tail
// contains the emitted instruction's first line reports OK; one that does not reports a SevWarn
// failure (advisory by design, §8.5).
func TestCustomInstructionsProbePhrase(t *testing.T) {
	contract.DeclareProducer(contract.CPreCompactCustomInstr)
	t.Cleanup(contract.ResetProducers)

	const phrase = "this is a forty-char probe phrase for the scan"
	require.GreaterOrEqual(t, len(phrase), 24)

	dir := t.TempDir()
	found := filepath.Join(dir, "found.jsonl")
	require.NoError(t, os.WriteFile(found, []byte("irrelevant line\n"+phrase+"\nmore text\n"), 0o600))
	absent := filepath.Join(dir, "absent.jsonl")
	require.NoError(t, os.WriteFile(absent, []byte("nothing here matches at all\n"), 0o600))

	h := &contract.SessionHistory{}
	h.SetPrecompactInstr(phrase)
	a := assertionByID(t, contract.CPreCompactCustomInstr)

	r := a.Check(context.Background(), contract.Env{
		Clock: newFakeClock(), History: h, Event: hookio.Event{TranscriptPath: found},
	})
	require.True(t, r.OK, "the phrase is present in the transcript tail")

	r = a.Check(context.Background(), contract.Env{
		Clock: newFakeClock(), History: h, Event: hookio.Event{TranscriptPath: absent},
	})
	require.False(t, r.OK)
	require.Equal(t, contract.SevWarn, r.Severity, "custom_instructions is advisory by design (§8.5)")
}

// TestCustomInstructionsProbePhrase_ShortFirstLineIsNoObservation (Important I3) asserts a first
// line shorter than 24 characters is not specific enough to be a probe phrase: the assertion must
// report "no probe possible", never scan for (and false-positive against) a short, generic phrase.
func TestCustomInstructionsProbePhrase_ShortFirstLineIsNoObservation(t *testing.T) {
	contract.DeclareProducer(contract.CPreCompactCustomInstr)
	t.Cleanup(contract.ResetProducers)

	h := &contract.SessionHistory{}
	h.SetPrecompactInstr("short heading") // < 24 chars
	require.Less(t, len(h.PrecompactInstr), 24)

	path := filepath.Join(t.TempDir(), "t.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("short heading appears verbatim here too\n"), 0o600))

	r := assertionByID(t, contract.CPreCompactCustomInstr).Check(context.Background(), contract.Env{
		Clock: newFakeClock(), History: h, Event: hookio.Event{TranscriptPath: path},
	})

	require.True(t, r.OK, "a too-short phrase must be a no-observation OK, never an automatic pass")
	require.Equal(t, "no probe phrase long enough", r.Observed)
}

// TestCustomInstructionsProbePhrase_LeadingNewlineIsNoObservation (Important I3) asserts an
// instruction beginning with a newline — an empty first line — cannot make bytes.Contains
// vacuously true against every transcript: the assertion must refuse to scan for an empty phrase.
func TestCustomInstructionsProbePhrase_LeadingNewlineIsNoObservation(t *testing.T) {
	contract.DeclareProducer(contract.CPreCompactCustomInstr)
	t.Cleanup(contract.ResetProducers)

	h := &contract.SessionHistory{}
	h.SetPrecompactInstr("\nthis second line alone is long enough to qualify")

	path := filepath.Join(t.TempDir(), "t.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("absolutely anything at all\n"), 0o600))

	r := assertionByID(t, contract.CPreCompactCustomInstr).Check(context.Background(), contract.Env{
		Clock: newFakeClock(), History: h, Event: hookio.Event{TranscriptPath: path},
	})

	require.True(t, r.OK, "an empty first line must never make bytes.Contains vacuously true")
	require.Equal(t, "no probe phrase long enough", r.Observed)
}

// TestCustomInstructionsProbePhrase_GenuineMatchAndMiss (Important I3) is the positive control: a
// first line genuinely >= 24 characters is used as the probe phrase, matching or missing the
// transcript tail exactly as TestCustomInstructionsProbePhrase already exercises — restated here
// under a name that makes the >=24 rule's coverage explicit rather than incidental.
func TestCustomInstructionsProbePhrase_GenuineMatchAndMiss(t *testing.T) {
	contract.DeclareProducer(contract.CPreCompactCustomInstr)
	t.Cleanup(contract.ResetProducers)

	const phrase = "this first line alone is exactly long enough"
	require.GreaterOrEqual(t, len(phrase), 24)

	h := &contract.SessionHistory{}
	h.SetPrecompactInstr(phrase + "\nsome trailing detail on a second line")

	dir := t.TempDir()
	found := filepath.Join(dir, "found.jsonl")
	require.NoError(t, os.WriteFile(found, []byte(phrase+"\n"), 0o600))
	absent := filepath.Join(dir, "absent.jsonl")
	require.NoError(t, os.WriteFile(absent, []byte("nothing here matches at all\n"), 0o600))

	a := assertionByID(t, contract.CPreCompactCustomInstr)

	r := a.Check(context.Background(), contract.Env{
		Clock: newFakeClock(), History: h, Event: hookio.Event{TranscriptPath: found},
	})
	require.True(t, r.OK)

	r = a.Check(context.Background(), contract.Env{
		Clock: newFakeClock(), History: h, Event: hookio.Event{TranscriptPath: absent},
	})
	require.False(t, r.OK)
	require.Equal(t, contract.SevWarn, r.Severity)
}

// TestHookPayloadShapeRejectsEmptySession asserts a payload missing session_id fails and names the
// missing field in Observed.
func TestHookPayloadShapeRejectsEmptySession(t *testing.T) {
	contract.DeclareProducer(contract.CHookPayloadShape)
	t.Cleanup(contract.ResetProducers)

	r := assertionByID(t, contract.CHookPayloadShape).Check(context.Background(), contract.Env{
		Clock: newFakeClock(), Event: hookio.Event{HookEventName: "PostToolUse"},
	})

	require.False(t, r.OK)
	require.Contains(t, r.Observed, "session_id")
}

// TestPluginRootUnsetIsOK asserts an unset CLAUDE_PLUGIN_ROOT reports OK/"unset" rather than a
// failure: most hosts never set it, and that is not evidence of anything broken.
func TestPluginRootUnsetIsOK(t *testing.T) {
	contract.DeclareProducer(contract.CPluginRootResolves)
	t.Cleanup(contract.ResetProducers)
	t.Setenv("CLAUDE_PLUGIN_ROOT", "")

	r := assertionByID(t, contract.CPluginRootResolves).Check(context.Background(),
		contract.Env{Clock: newFakeClock()})

	require.True(t, r.OK)
	require.Equal(t, "unset", r.Observed)
}

// TestHistoryPersistsAcrossMonitors asserts both persisted records survive into the next
// SessionStart: the Monitor's own degrade/restore state (state/contract.json, unchanged mechanic)
// and the cross-session SessionHistory (state/history.json, this task's new one).
func TestHistoryPersistsAcrossMonitors(t *testing.T) {
	root := t.TempDir()
	statePath := statePathIn(root)
	historyPath := contract.HistoryPath(root)

	h := contract.LoadHistory(historyPath)
	h.SessionCount = 1

	first := contract.NewMonitor(logging.Nop(), obs.New(newFakeClock()), statePath)
	require.NoError(t, first.Register(fixedResult(contract.CHookPayloadShape, false, contract.SevCritical)))
	_, mode := first.RunAll(context.Background(), contract.Env{Clock: newFakeClock(), History: h})
	require.Equal(t, contract.ModeDegradedPassive, mode)
	require.NoError(t, contract.SaveHistory(historyPath, h))

	reloaded := contract.LoadHistory(historyPath)
	require.Equal(t, 1, reloaded.SessionCount, "the cross-session record must round-trip")

	second := contract.NewMonitor(logging.Nop(), obs.New(newFakeClock()), statePath)
	require.Equal(t, contract.ModeDegradedPassive, second.Mode(),
		"the next SessionStart must surface the degradation, not start clean")
}

// The three tests below pin the session-id half of §12.1's source_compact observable, which the
// assertion ignored until the 2026-08-22 audit: it read only AwaitingCompactStart, so a PreCompact
// in one session resolved against whatever session started next. SessionHistory.LastPrecompactSession
// had been written by the daemon since SP-05 and read by nothing.

// TestSourceCompact_PendingForAnotherSessionDoesNotFail is the defect itself: session X compacts,
// an unrelated session Y starts with source=startup, and the assertion must not blame Y for X.
func TestSourceCompact_PendingForAnotherSessionDoesNotFail(t *testing.T) {
	contract.DeclareProducer(contract.CSessionStartSourceCompact)
	t.Cleanup(contract.ResetProducers)

	h := &contract.SessionHistory{AwaitingCompactStart: true, LastPrecompactSession: "sess-x"}
	env := contract.Env{
		Clock: newFakeClock(), History: h,
		Event: hookio.Event{SessionID: "sess-y", Source: "startup"},
	}

	r := assertionByID(t, contract.CSessionStartSourceCompact).Check(context.Background(), env)

	require.True(t, r.OK, "a start of a different session must not resolve session X's pending observation")
	require.Equal(t, "precompact-pending-for-another-session", r.Observed)
	require.False(t, h.AwaitingCompactStart,
		"the pending flag must be dropped, or the next start of any session inherits a stale obligation")
}

// TestSourceCompact_SameSessionStillFails is the mutation guard for the test above: the fix must not
// turn the assertion off. Same session id, wrong source, still SevCritical-false.
func TestSourceCompact_SameSessionStillFails(t *testing.T) {
	contract.DeclareProducer(contract.CSessionStartSourceCompact)
	t.Cleanup(contract.ResetProducers)

	h := &contract.SessionHistory{AwaitingCompactStart: true, LastPrecompactSession: "sess-x"}
	env := contract.Env{
		Clock: newFakeClock(), History: h,
		Event: hookio.Event{SessionID: "sess-x", Source: "startup"},
	}

	r := assertionByID(t, contract.CSessionStartSourceCompact).Check(context.Background(), env)

	require.False(t, r.OK, "the compacting session's own start with the wrong source must still fail")
	require.Equal(t, "compact", r.Expected)
	require.Equal(t, "startup", r.Observed)
	require.False(t, h.AwaitingCompactStart)
}

// TestSourceCompact_SameSessionCompactPasses closes the table: the honest success path.
func TestSourceCompact_SameSessionCompactPasses(t *testing.T) {
	contract.DeclareProducer(contract.CSessionStartSourceCompact)
	t.Cleanup(contract.ResetProducers)

	h := &contract.SessionHistory{AwaitingCompactStart: true, LastPrecompactSession: "sess-x"}
	env := contract.Env{
		Clock: newFakeClock(), History: h,
		Event: hookio.Event{SessionID: "sess-x", Source: "compact"},
	}

	r := assertionByID(t, contract.CSessionStartSourceCompact).Check(context.Background(), env)

	require.True(t, r.OK)
	require.Equal(t, "compact", r.Observed)
	require.False(t, h.AwaitingCompactStart)
}
