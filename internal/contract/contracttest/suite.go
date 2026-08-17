// Package contracttest is the conformance suite for contract.Monitor and contract.History
// (00-ARCHITECTURE.md §5.22): every implementation SP-05 ships must pass RunMonitorSuite and
// RunHistorySuite. SP-01 ships the suite itself, including the behaviour assertions SP-05
// inherits (Rule W-1).
//
// # Why the Monitor behaviour block runs instead of skipping
//
// contract is the one package in wave 0 whose stub behaviour is load-bearing: §12.1 requires a
// freshly built develop to report ModeFull, so SP-01 ships the monitor MECHANICS for real (see
// contract's own package documentation). RunMonitorSuite's Rule W-1 probe therefore reports "not a
// stub" for contract.NewMonitor, and its behaviour block runs — and passes — today. The probe is
// still there, and is still honest: a genuinely stubbed Monitor (one whose Register reports
// core.ErrNotImplemented, or whose RunAll never executes what was registered) skips with the exact
// Rule W-1 message, which suite_test.go demonstrates against a deliberately stubbed fake.
//
// contract shipped no History implementation at wave 0 — §5.19 gives it no constructor — so before
// SP-05, RunHistorySuite's behaviour block was the ordinary Rule W-1 case: it skipped until a real
// History was handed to it. SP-05 shipped that History, contract.SessionHistory (history.go), and
// suite_test.go now runs the behaviour block against it directly, alongside the suite's own
// throwaway memHistory. The Rule W-1 probe itself is unchanged and still correctly skips a
// deliberately stubbed fakeStubHistory.
//
// A <pkg>test package may import only its own base package, testutil and core
// (00-ARCHITECTURE.md §3.2), so this suite constructs contract.Env with a clock of its own and
// leaves Cfg, Store, Event and Log at their zero values: it cannot import config, store, hookio or
// logging to fill them. Every implementation must therefore tolerate a half-filled Env, which is
// exactly what a caller outside a hook has.
package contracttest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// probeID is the assertion ID the shape block and the stub probe both register. It is one of the
// nine real §5.19 IDs rather than an invented one, because a Monitor is entitled to reject an
// assertion it does not recognize.
const probeID = contract.CSessionStartFires

// notYetImplementedObserved is §12.1's exact Observed string for an assertion whose producer is
// absent from the build. The suite writes it into a synthetic assertion rather than reading it back
// from StandardAssertions, so these assertions keep testing the MONITOR's rule ("a result reporting
// OK/SevInfo never degrades") as SP-05 replaces the standard Checks one by one with real
// observations.
const notYetImplementedObserved = "not-yet-implemented"

// suiteClock is the core.Clock every assertion in this suite stamps its Result from. §6.1 bans
// wall-clock sleeps, and a fixed clock makes every timestamp assertion exact.
type suiteClock struct{}

var suiteInstant = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func (suiteClock) Now() time.Time                  { return suiteInstant }
func (suiteClock) Since(t time.Time) time.Duration { return suiteInstant.Sub(t) }

// suiteEnv is the half-filled Env this suite can legally construct: a clock, and nothing else.
func suiteEnv() contract.Env { return contract.Env{Clock: suiteClock{}} }

// RunMonitorSuite is the conformance suite for contract.Monitor. name distinguishes multiple
// factories run in the same test binary; factory must return a fresh, ready-to-use Monitor on
// every call — including a fresh state path, since several behaviour cases drive the §12.1
// degrade/restore state machine and must not inherit a previous case's persisted mode.
func RunMonitorSuite(t *testing.T, name string, factory func(t *testing.T) contract.Monitor) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		m := factory(t)
		require.NotNil(t, m)
		ctx := context.Background()

		// Register is handed a well-formed assertion: an implementation is entitled to reject a
		// malformed one, so registering contract.Assertion{} here would assert the opposite of
		// what the interface promises. The malformed case is a behaviour assertion below.
		requireKnownError(t, m.Register(alwaysAssertion(probeID, true, contract.SevInfo)))

		results, mode := m.RunAll(ctx, suiteEnv())
		requireKnownMode(t, mode)
		for _, r := range results {
			requireKnownSeverity(t, r.Severity)
		}

		requireKnownMode(t, m.Mode())

		// Degrade and Restore have no error return: the shape assertion is that both are callable
		// on any implementation and leave the Monitor reporting a mode the rest of the system
		// knows how to render.
		m.Degrade("contracttest: shape probe", results)
		requireKnownMode(t, m.Mode())
		m.Restore("contracttest: shape probe")
		requireKnownMode(t, m.Mode())

		for _, r := range m.Report() {
			requireKnownSeverity(t, r.Severity)
		}
	})

	if skipIfStubMonitor(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("not_yet_implemented_result_never_degrades", func(t *testing.T) {
			runNotYetImplementedNeverDegradesCase(t, factory)
		})
		t.Run("critical_failure_degrades_to_passive", func(t *testing.T) {
			runCriticalFailureDegradesCase(t, factory)
		})
		t.Run("warn_failure_does_not_degrade", func(t *testing.T) {
			runWarnFailureDoesNotDegradeCase(t, factory)
		})
		t.Run("two_consecutive_clean_runs_restore", func(t *testing.T) {
			runTwoCleanRunsRestoreCase(t, factory)
		})
		t.Run("results_follow_registration_order", func(t *testing.T) {
			runRegistrationOrderCase(t, factory)
		})
		t.Run("report_reflects_the_most_recent_run", func(t *testing.T) {
			runReportReflectsLastRunCase(t, factory)
		})
		t.Run("register_refuses_an_unusable_assertion", func(t *testing.T) {
			runRegisterRefusesUnusableCase(t, factory)
		})
	})
}

// RunHistorySuite is the conformance suite for contract.History, the cross-session record of which
// hook contracts have ever been observed to hold. factory must return a fresh, empty History on
// every call.
func RunHistorySuite(t *testing.T, name string, factory func(t *testing.T) contract.History) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		h := factory(t)
		require.NotNil(t, h)

		// None of History's four methods has an error return, so the shape assertion is that all
		// four are callable and return usable zero values on an empty History.
		_ = h.Saw(probeID)
		_, _ = h.LastSeen(probeID)
		h.Record(probeID, core.NowMilli(suiteClock{}))
		require.GreaterOrEqual(t, h.Sessions(), 0, "Sessions must never report a negative count")
	})

	if skipIfStubHistory(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("record_then_saw", func(t *testing.T) { runHistoryRecordThenSawCase(t, factory) })
		t.Run("last_seen_returns_the_recorded_instant", func(t *testing.T) {
			runHistoryLastSeenCase(t, factory)
		})
		t.Run("unknown_id_is_never_saw", func(t *testing.T) { runHistoryUnknownIDCase(t, factory) })
		t.Run("sessions_never_decreases", func(t *testing.T) { runHistorySessionsMonotoneCase(t, factory) })
	})
}

// alwaysAssertion returns an assertion whose Check always reports the same (ok, severity) pair,
// stamped from the Env's clock.
func alwaysAssertion(id contract.ID, ok bool, sev contract.Severity) contract.Assertion {
	return contract.Assertion{
		ID: id, Severity: sev, Description: string(id),
		Check: func(ctx context.Context, e contract.Env) contract.Result {
			return contract.Result{
				ID: id, OK: ok, Severity: sev,
				Expected: string(id), Observed: "contracttest", TS: core.NowMilli(e.Clock),
			}
		},
	}
}

// notYetImplementedAssertion returns the §12.1 shape that makes a fresh build ModeFull: an
// assertion DECLARING SevCritical whose Check reports OK with SevInfo and
// Observed: "not-yet-implemented", because the subsystem that would produce the signal is absent
// from the build.
func notYetImplementedAssertion(id contract.ID) contract.Assertion {
	return contract.Assertion{
		ID: id, Severity: contract.SevCritical, Description: string(id),
		Check: func(ctx context.Context, e contract.Env) contract.Result {
			return contract.Result{
				ID: id, OK: true, Severity: contract.SevInfo,
				Expected: string(id), Observed: notYetImplementedObserved, TS: core.NowMilli(e.Clock),
			}
		},
	}
}

// flippableAssertion returns a SevCritical assertion whose result the returned setter controls, so
// a case can drive the degrade/restore state machine across several RunAll calls without sleeping
// and without needing a Deregister the interface deliberately does not have.
func flippableAssertion(id contract.ID) (contract.Assertion, func(ok bool)) {
	var mu sync.Mutex
	ok := true
	a := contract.Assertion{
		ID: id, Severity: contract.SevCritical, Description: string(id),
		Check: func(ctx context.Context, e contract.Env) contract.Result {
			mu.Lock()
			cur := ok
			mu.Unlock()
			return contract.Result{
				ID: id, OK: cur, Severity: contract.SevCritical,
				Expected: string(id), Observed: "contracttest", TS: core.NowMilli(e.Clock),
			}
		},
	}
	return a, func(v bool) {
		mu.Lock()
		ok = v
		mu.Unlock()
	}
}

// requireKnownError fails the test unless err is nil or wraps one of the four sentinels every stub
// and every real implementation is allowed to return from an operation (00-ARCHITECTURE.md §5.22;
// §15 of plans/V1-SP-01-foundation-toolchain-and-contracts.md).
func requireKnownError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	known := errors.Is(err, core.ErrNotImplemented) ||
		errors.Is(err, core.ErrNotFound) ||
		errors.Is(err, core.ErrBudget) ||
		errors.Is(err, core.ErrDegraded)
	require.True(t, known, "unexpected error: %v", err)
}

// requireKnownMode fails the test unless m is one of the three modes §5.19 declares. It compares
// through Mode.String because that spelling is the on-disk and on-screen contract: a Mode that
// renders "unknown" is one nothing downstream can display or persist.
func requireKnownMode(t *testing.T, m contract.Mode) {
	t.Helper()
	switch m {
	case contract.ModeFull, contract.ModeDegradedPassive, contract.ModeOff:
	default:
		require.Failf(t, "unknown mode", "Mode(%d) renders %q", uint8(m), m.String())
	}
}

// requireKnownSeverity fails the test unless s is one of the three severities §5.19 declares.
func requireKnownSeverity(t *testing.T, s contract.Severity) {
	t.Helper()
	switch s {
	case contract.SevInfo, contract.SevWarn, contract.SevCritical:
	default:
		require.Failf(t, "unknown severity", "Severity(%d)", uint8(s))
	}
}

// isStubMonitor reports whether factory currently produces a stub Monitor. plans/OWNERS.tsv names
// RunAll as contract's probe method, but RunAll has no error return, so "still a stub" cannot be
// core.IsNotImplemented of anything it returns. It is detected instead by the two ways a Monitor
// can fail to be one: refusing to register a well-formed assertion with core.ErrNotImplemented, or
// registering it and then not executing it.
func isStubMonitor(t *testing.T, factory func(t *testing.T) contract.Monitor) bool {
	t.Helper()
	m := factory(t)
	if core.IsNotImplemented(m.Register(alwaysAssertion(probeID, true, contract.SevInfo))) {
		return true
	}
	results, _ := m.RunAll(context.Background(), suiteEnv())
	for _, r := range results {
		if r.ID == probeID {
			return false
		}
	}
	return true
}

// skipIfStubMonitor calls t.Skip with the exact Rule W-1 message when factory still produces a stub
// Monitor, and reports whether it did. It does NOT skip for contract.NewMonitor: SP-01 ships the
// monitor mechanics for real, so the behaviour block below runs and passes today.
func skipIfStubMonitor(t *testing.T, factory func(t *testing.T) contract.Monitor) bool {
	t.Helper()
	if isStubMonitor(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}

// isStubHistory reports whether factory currently produces a stub History: one that does not
// remember what it was just told. Record and Saw have no error returns, so this round-trip is the
// only mechanical probe available — and it is the right one, since remembering an observation is
// the entire purpose of the interface.
func isStubHistory(t *testing.T, factory func(t *testing.T) contract.History) bool {
	t.Helper()
	h := factory(t)
	h.Record(probeID, core.NowMilli(suiteClock{}))
	return !h.Saw(probeID)
}

// skipIfStubHistory calls t.Skip with the exact Rule W-1 message when factory still produces a stub
// History, and reports whether it did.
func skipIfStubHistory(t *testing.T, factory func(t *testing.T) contract.History) bool {
	t.Helper()
	if isStubHistory(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}
