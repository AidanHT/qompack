package contract_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/stretchr/testify/require"
)

// fakeClock is the core.Clock every test in this package uses. §6.1 bans wall-clock sleeps, and a
// fixed clock additionally makes every Result.TS assertion exact rather than approximate.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time                  { return c.now }
func (c *fakeClock) Since(t time.Time) time.Duration { return c.now.Sub(t) }

// newFakeClock returns a clock fixed at the same instant testutil pins its own FakeClock to.
func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

// statePathIn returns <dir>/.qompack/state/contract.json — the path every real caller passes to
// NewMonitor. The directory is deliberately NOT created: NewMonitor must tolerate its absence, and
// persist must create it.
func statePathIn(dir string) string {
	return filepath.Join(paths.Of(dir).State, "contract.json")
}

// newTestMonitor builds a Monitor over a fresh temp project, and returns it with its metrics
// registry and state path so a test can assert on all three.
func newTestMonitor(t *testing.T) (contract.Monitor, obs.Registry, string) {
	t.Helper()
	reg := obs.New(newFakeClock())
	statePath := statePathIn(t.TempDir())
	return contract.NewMonitor(logging.Nop(), reg, statePath), reg, statePath
}

// registerAll registers every assertion in as, failing the test on the first rejection.
func registerAll(t *testing.T, m contract.Monitor, as []contract.Assertion) {
	t.Helper()
	for _, a := range as {
		require.NoError(t, m.Register(a))
	}
}

// fixedResult returns an assertion that always reports r, with r.ID and r.TS filled in from id and
// the Env's clock so a test only has to state the part it cares about.
func fixedResult(id contract.ID, ok bool, sev contract.Severity) contract.Assertion {
	return contract.Assertion{
		ID: id, Severity: sev, Description: string(id),
		Check: func(ctx context.Context, e contract.Env) contract.Result {
			return contract.Result{
				ID: id, OK: ok, Severity: sev,
				Expected: string(id), Observed: "test", TS: core.NowMilli(e.Clock),
			}
		},
	}
}

// flippable returns an assertion whose result is controlled by the returned setter, so a test can
// drive the §12.1 degrade/restore state machine across several RunAll calls without any sleeps or
// re-registration (there is no Deregister, by design).
func flippable(id contract.ID) (contract.Assertion, func(ok bool)) {
	var mu sync.Mutex
	ok := true
	set := func(v bool) {
		mu.Lock()
		ok = v
		mu.Unlock()
	}
	a := contract.Assertion{
		ID: id, Severity: contract.SevCritical, Description: string(id),
		Check: func(ctx context.Context, e contract.Env) contract.Result {
			mu.Lock()
			cur := ok
			mu.Unlock()
			return contract.Result{
				ID: id, OK: cur, Severity: contract.SevCritical,
				Expected: "holds", Observed: "test", TS: core.NowMilli(e.Clock),
			}
		},
	}
	return a, set
}

// captureLoud installs a process-wide Loud observer for the duration of the test and returns an
// accessor for the messages it saw. logging.AttachLoudObserver is process-wide, so tests using it
// must not run in parallel — none in this package do.
func captureLoud(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var msgs []string
	logging.AttachLoudObserver(func(msg string, kv ...any) {
		mu.Lock()
		msgs = append(msgs, msg)
		mu.Unlock()
	})
	t.Cleanup(func() { logging.AttachLoudObserver(nil) })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), msgs...)
	}
}

// readState decodes state/contract.json.
func readState(t *testing.T, statePath string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(statePath)
	require.NoError(t, err)
	var st map[string]any
	require.NoError(t, json.Unmarshal(b, &st))
	return st
}

// TestMonitor_StandardAssertionsReportModeFull is the §12.1 property the CI guard
// (test/guards.TestGuard_FreshBuildReportsModeFull) depends on, asserted here at the unit level so
// a regression is caught in this package rather than three trees away: a monitor built from
// StandardAssertions() on a fresh build reports ModeFull, and every result is the
// OK/SevInfo/not-yet-implemented shape.
func TestMonitor_StandardAssertionsReportModeFull(t *testing.T) {
	m, _, _ := newTestMonitor(t)
	registerAll(t, m, contract.StandardAssertions())

	results, mode := m.RunAll(context.Background(), contract.Env{Clock: newFakeClock()})

	require.Equal(t, contract.ModeFull, mode, "a freshly built develop must report ModeFull (§12.1)")
	require.Equal(t, contract.ModeFull, m.Mode())
	require.Len(t, results, 9)
	for _, r := range results {
		require.True(t, r.OK, "%s must not fail on a fresh build", r.ID)
		require.Equal(t, contract.SevInfo, r.Severity, "%s must OBSERVE SevInfo on a fresh build", r.ID)
		require.Equal(t, "not-yet-implemented", r.Observed)
	}
}

// TestMonitor_FlippedCriticalAssertionDegrades is the other half of the guard property: take the
// same StandardAssertions() set, replace exactly one Check with one that reports
// OK: false, Severity: SevCritical, and the monitor must degrade to ModeDegradedPassive. Without
// this, TestMonitor_StandardAssertionsReportModeFull would pass just as happily against a monitor
// that could never degrade at all.
func TestMonitor_FlippedCriticalAssertionDegrades(t *testing.T) {
	m, reg, statePath := newTestMonitor(t)
	loud := captureLoud(t)

	assertions := contract.StandardAssertions()
	flipped := false
	for i, a := range assertions {
		if a.ID != contract.CSessionStartFires {
			continue
		}
		assertions[i] = fixedResult(a.ID, false, contract.SevCritical)
		flipped = true
	}
	require.True(t, flipped, "fixture sanity: the assertion being flipped must exist in the set")
	registerAll(t, m, assertions)

	results, mode := m.RunAll(context.Background(), contract.Env{Clock: newFakeClock()})

	require.Equal(t, contract.ModeDegradedPassive, mode,
		"an observed SevCritical failure must degrade the session (§12.1)")
	require.Equal(t, contract.ModeDegradedPassive, m.Mode())
	require.Len(t, results, 9)
	require.NotEmpty(t, loud(), "degradation must be LOUD — nothing fails silently (§12.1)")

	st := readState(t, statePath)
	require.Equal(t, "degraded-passive", st["mode"])
	require.Contains(t, st["reason"], string(contract.CSessionStartFires))
	require.EqualValues(t, 1, reg.Snapshot().Counters["contract.degrade"])
}

// TestMonitor_WarnFailureDoesNotDegrade asserts the severity gate: §12.1 degrades on a SevCritical
// failure and only on a SevCritical failure. A failing SevWarn assertion is reported and surfaced,
// but the session keeps acting.
func TestMonitor_WarnFailureDoesNotDegrade(t *testing.T) {
	m, _, statePath := newTestMonitor(t)
	registerAll(t, m, []contract.Assertion{
		fixedResult(contract.CPreCompactTiming, false, contract.SevWarn),
		fixedResult(contract.CMCPRegistered, false, contract.SevInfo),
	})

	_, mode := m.RunAll(context.Background(), contract.Env{Clock: newFakeClock()})

	require.Equal(t, contract.ModeFull, mode)
	require.Equal(t, contract.ModeFull, m.Mode())
	_, err := os.Stat(statePath)
	require.True(t, os.IsNotExist(err), "a run that changes nothing must not write state/contract.json")
}

// TestMonitor_TwoConsecutiveCleanRunsRestore is §12.1's recovery rule, including the part that is
// easy to get wrong: the FIRST clean run after a degradation does not restore. If it did, a
// flapping host contract would toggle the session between acting and not acting on every start.
func TestMonitor_TwoConsecutiveCleanRunsRestore(t *testing.T) {
	m, reg, statePath := newTestMonitor(t)
	loud := captureLoud(t)

	a, set := flippable(contract.CSessionStartFires)
	require.NoError(t, m.Register(a))
	env := contract.Env{Clock: newFakeClock()}

	set(false)
	_, mode := m.RunAll(context.Background(), env)
	require.Equal(t, contract.ModeDegradedPassive, mode)

	set(true)
	_, mode = m.RunAll(context.Background(), env)
	require.Equal(t, contract.ModeDegradedPassive, mode,
		"one clean run is not enough: §12.1 requires TWO consecutive clean runs before Restore")
	require.Equal(t, contract.ModeDegradedPassive, m.Mode())

	_, mode = m.RunAll(context.Background(), env)
	require.Equal(t, contract.ModeFull, mode, "the second consecutive clean run must restore (§12.1)")
	require.Equal(t, contract.ModeFull, m.Mode())

	require.Equal(t, "full", readState(t, statePath)["mode"])
	require.EqualValues(t, 1, reg.Snapshot().Counters["contract.restore"])
	require.Len(t, loud(), 2,
		"exactly two LOUD lines: one for the degradation and one for the restore, which §12.1 requires to be logged just as loudly")
}

// TestMonitor_CleanRunStreakResetsOnAFurtherFailure asserts the streak is CONSECUTIVE: a clean run
// followed by another failure followed by a clean run must not restore, because that is one clean
// run in a row, not two.
func TestMonitor_CleanRunStreakResetsOnAFurtherFailure(t *testing.T) {
	m, _, _ := newTestMonitor(t)
	a, set := flippable(contract.CHookPayloadShape)
	require.NoError(t, m.Register(a))
	env := contract.Env{Clock: newFakeClock()}

	set(false)
	_, _ = m.RunAll(context.Background(), env)
	set(true)
	_, _ = m.RunAll(context.Background(), env)
	set(false)
	_, _ = m.RunAll(context.Background(), env)
	set(true)
	_, mode := m.RunAll(context.Background(), env)

	require.Equal(t, contract.ModeDegradedPassive, mode,
		"the clean-run streak must reset on every further failure")
}

// TestMonitor_DegradationSurvivesIntoTheNextSession asserts §12.1's "persists the reason so
// /qompack:status and the next SessionStart both surface it": a second Monitor over the same state
// path starts degraded and reports the results the degrading run recorded.
func TestMonitor_DegradationSurvivesIntoTheNextSession(t *testing.T) {
	root := t.TempDir()
	statePath := statePathIn(root)

	first := contract.NewMonitor(logging.Nop(), obs.New(newFakeClock()), statePath)
	require.NoError(t, first.Register(fixedResult(contract.CAdditionalContext, false, contract.SevCritical)))
	_, mode := first.RunAll(context.Background(), contract.Env{Clock: newFakeClock()})
	require.Equal(t, contract.ModeDegradedPassive, mode)

	next := contract.NewMonitor(logging.Nop(), obs.New(newFakeClock()), statePath)
	require.Equal(t, contract.ModeDegradedPassive, next.Mode(),
		"the next SessionStart must surface the degradation, not start clean")

	report := next.Report()
	require.Len(t, report, 1)
	require.Equal(t, contract.CAdditionalContext, report[0].ID)
	require.False(t, report[0].OK)
}

// TestMonitor_CorruptStateFileFallsBackToModeFull asserts §12.3's "everything else fails toward do
// nothing" applied to the monitor's own state file: an unparseable or unrecognized state must not
// leave the session in a mode nobody chose.
func TestMonitor_CorruptStateFileFallsBackToModeFull(t *testing.T) {
	for _, body := range []string{"{not json", `{"mode":"sideways"}`, ""} {
		root := t.TempDir()
		statePath := statePathIn(root)
		require.NoError(t, os.MkdirAll(filepath.Dir(statePath), 0o700))
		require.NoError(t, os.WriteFile(statePath, []byte(body), 0o600))

		m := contract.NewMonitor(logging.Nop(), obs.New(newFakeClock()), statePath)
		require.Equal(t, contract.ModeFull, m.Mode(), "corrupt state %q must fall back to ModeFull", body)
	}
}

// TestMonitor_DegradeWritesLoudLog asserts the file half of §12.1's "logging.Loud (log file +
// LOUD.log)": the degradation reaches .qompack/logs/LOUD.log, which is append-only and never
// rotated, so it survives to be read by a human after the fact.
func TestMonitor_DegradeWritesLoudLog(t *testing.T) {
	root := t.TempDir()
	logDir := paths.Of(root).Logs
	log, closer, err := logging.New(logDir, logging.Info)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, closer.Close()) })

	m := contract.NewMonitor(log, obs.New(newFakeClock()), statePathIn(root))
	require.NoError(t, m.Register(fixedResult(contract.CSessionStartSourceCompact, false, contract.SevCritical)))
	_, mode := m.RunAll(context.Background(), contract.Env{Clock: newFakeClock()})
	require.Equal(t, contract.ModeDegradedPassive, mode)

	b, err := os.ReadFile(filepath.Join(logDir, "LOUD.log"))
	require.NoError(t, err)
	require.Contains(t, string(b), "degrading to passive recording")
	require.Contains(t, string(b), string(contract.CSessionStartSourceCompact))
}

// TestMonitor_DegradeIsLoudWithoutAStatePath asserts the persistence path is not load-bearing for
// loudness: a monitor with no state path (a caller with no project layout) still degrades, and
// still says so.
func TestMonitor_DegradeIsLoudWithoutAStatePath(t *testing.T) {
	loud := captureLoud(t)
	m := contract.NewMonitor(logging.Nop(), nil, "")
	require.NoError(t, m.Register(fixedResult(contract.CTranscriptReadable, false, contract.SevCritical)))

	_, mode := m.RunAll(context.Background(), contract.Env{Clock: newFakeClock()})

	require.Equal(t, contract.ModeDegradedPassive, mode)
	require.NotEmpty(t, loud())
}

// TestMonitor_NilLoggerAndRegistryAreTolerated asserts NewMonitor's defensive contract: a nil
// Logger and a nil obs.Registry must not turn a degradation into a panic on the hot path.
func TestMonitor_NilLoggerAndRegistryAreTolerated(t *testing.T) {
	m := contract.NewMonitor(nil, nil, "")
	require.NoError(t, m.Register(fixedResult(contract.CPluginRootResolves, false, contract.SevCritical)))
	require.NotPanics(t, func() {
		_, _ = m.RunAll(context.Background(), contract.Env{Clock: newFakeClock()})
	})
}

// TestMonitor_RunAllToleratesANilClock asserts RunAll substitutes a clock for a half-filled Env
// before invoking any Check. StandardAssertions' own Check stamps its Result from Env.Clock, so
// without this a caller that omitted the clock would panic instead of getting results.
func TestMonitor_RunAllToleratesANilClock(t *testing.T) {
	m, _, _ := newTestMonitor(t)
	registerAll(t, m, contract.StandardAssertions())

	var results []contract.Result
	var mode contract.Mode
	require.NotPanics(t, func() {
		results, mode = m.RunAll(context.Background(), contract.Env{})
	})
	require.Equal(t, contract.ModeFull, mode)
	require.Len(t, results, 9)
	for _, r := range results {
		require.NotZero(t, r.TS, "%s must still be timestamped when Env.Clock was nil", r.ID)
	}
}

// TestMonitor_RegisterRejectsUnusableAssertions asserts Register's error return does real work: an
// assertion with no ID or no Check could never produce a usable Result, so it is refused at
// registration rather than panicking inside RunAll on the hot path.
func TestMonitor_RegisterRejectsUnusableAssertions(t *testing.T) {
	m, _, _ := newTestMonitor(t)

	require.Error(t, m.Register(contract.Assertion{}), "an assertion with no ID must be refused")
	require.Error(t, m.Register(contract.Assertion{ID: contract.CSessionStartFires}),
		"an assertion with no Check must be refused")

	results, mode := m.RunAll(context.Background(), contract.Env{Clock: newFakeClock()})
	require.Empty(t, results, "a refused assertion must not have been registered")
	require.Equal(t, contract.ModeFull, mode)
}

// TestMonitor_RunAllPreservesRegistrationOrder asserts results come back in the order assertions
// were registered, which is what makes /qompack:status's banner and the persisted result set
// diffable between runs.
func TestMonitor_RunAllPreservesRegistrationOrder(t *testing.T) {
	m, _, _ := newTestMonitor(t)
	want := []contract.ID{contract.CPluginRootResolves, contract.CSessionStartFires, contract.CMCPRegistered}
	for _, id := range want {
		require.NoError(t, m.Register(fixedResult(id, true, contract.SevInfo)))
	}

	results, _ := m.RunAll(context.Background(), contract.Env{Clock: newFakeClock()})

	got := make([]contract.ID, len(results))
	for i, r := range results {
		got[i] = r.ID
	}
	require.Equal(t, want, got)
}

// TestMonitor_ReportIsACopy asserts Report hands out a copy: /qompack:status must not be able to
// mutate the monitor's record of why the session degraded.
func TestMonitor_ReportIsACopy(t *testing.T) {
	m, _, _ := newTestMonitor(t)
	require.NoError(t, m.Register(fixedResult(contract.CMCPRegistered, true, contract.SevInfo)))
	_, _ = m.RunAll(context.Background(), contract.Env{Clock: newFakeClock()})

	first := m.Report()
	require.Len(t, first, 1)
	first[0].ID = "mutated"
	first[0].OK = false

	require.Equal(t, contract.CMCPRegistered, m.Report()[0].ID)
	require.True(t, m.Report()[0].OK)
}

// TestMonitor_ModeOffIsNeverEnteredOrLeftAutomatically asserts ModeOff is the operator's decision:
// a monitor whose persisted state says "off" keeps reporting results, but neither degrades nor
// restores itself out of the mode an operator chose.
func TestMonitor_ModeOffIsNeverEnteredOrLeftAutomatically(t *testing.T) {
	root := t.TempDir()
	statePath := statePathIn(root)
	require.NoError(t, os.MkdirAll(filepath.Dir(statePath), 0o700))
	require.NoError(t, os.WriteFile(statePath, []byte(`{"mode":"off","reason":"operator"}`), 0o600))

	m := contract.NewMonitor(logging.Nop(), obs.New(newFakeClock()), statePath)
	require.Equal(t, contract.ModeOff, m.Mode())
	require.NoError(t, m.Register(fixedResult(contract.CSessionStartFires, false, contract.SevCritical)))

	results, mode := m.RunAll(context.Background(), contract.Env{Clock: newFakeClock()})

	require.Equal(t, contract.ModeOff, mode)
	require.Len(t, results, 1, "the assertions still run and are still reported while off")

	require.Equal(t, "off", readState(t, statePath)["mode"], "RunAll must not have rewritten the operator's mode")
}

// TestMonitor_DegradeReasonNamesExpectedAndObserved asserts the persisted reason carries both
// halves of the banner §12.1 requires /qompack:status to lead with: what was expected and what was
// observed.
func TestMonitor_DegradeReasonNamesExpectedAndObserved(t *testing.T) {
	m, _, statePath := newTestMonitor(t)
	m.Degrade("critical host-contract failure", []contract.Result{{
		ID: contract.CAdditionalContext, OK: false, Severity: contract.SevCritical,
		Expected: "sentinel reaches the transcript", Observed: "sentinel absent",
	}})

	require.Equal(t, contract.ModeDegradedPassive, m.Mode())
	st := readState(t, statePath)
	require.Equal(t, "critical host-contract failure", st["reason"])

	raw, err := os.ReadFile(statePath)
	require.NoError(t, err)
	require.True(t, strings.Contains(string(raw), "sentinel absent"),
		"the persisted results must carry what was actually observed")
}
