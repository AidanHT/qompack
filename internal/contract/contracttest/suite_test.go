package contracttest_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/contract/contracttest"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// fakeStubMonitor is what a Monitor would look like if SP-01 had stubbed it like every other §5
// interface: Register reports core.ErrNotImplemented and nothing is ever executed. contract does
// NOT ship this — §12.1 requires the real mechanics in wave 0 — but the suite must still grade a
// stub correctly, and this is what proves it does.
type fakeStubMonitor struct{}

func (fakeStubMonitor) Register(a contract.Assertion) error { return core.ErrNotImplemented }

func (fakeStubMonitor) RunAll(ctx context.Context, e contract.Env) ([]contract.Result, contract.Mode) {
	return nil, contract.ModeFull
}
func (fakeStubMonitor) Mode() contract.Mode                              { return contract.ModeFull }
func (fakeStubMonitor) Degrade(reason string, results []contract.Result) {}
func (fakeStubMonitor) Restore(reason string)                            {}
func (fakeStubMonitor) Report() []contract.Result                        { return nil }

// fakeStubHistory is the stub History §5.19 leaves SP-05 to implement: every method returns its
// documented zero value, and nothing is remembered.
type fakeStubHistory struct{}

func (fakeStubHistory) Saw(id contract.ID) bool                        { return false }
func (fakeStubHistory) LastSeen(id contract.ID) (core.UnixMilli, bool) { return 0, false }
func (fakeStubHistory) Record(id contract.ID, at core.UnixMilli)       {}
func (fakeStubHistory) Sessions() int                                  { return 0 }

// memHistory is a minimal, correct, in-memory History. It exists so RunHistorySuite's behaviour
// block is demonstrably satisfiable rather than an executable specification nobody has ever run:
// an unrunnable spec is worth very little to SP-05, who inherits it. It is deliberately NOT
// exported and NOT shipped in package contract — the real History is cross-session and persistent,
// and that is SP-05's to build.
type memHistory struct {
	mu       sync.Mutex
	seen     map[contract.ID]core.UnixMilli
	sessions int
}

func newMemHistory() *memHistory {
	return &memHistory{seen: make(map[contract.ID]core.UnixMilli), sessions: 1}
}

func (h *memHistory) Saw(id contract.ID) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.seen[id]
	return ok
}

func (h *memHistory) LastSeen(id contract.ID) (core.UnixMilli, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ts, ok := h.seen[id]
	return ts, ok
}

func (h *memHistory) Record(id contract.ID, at core.UnixMilli) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if prev, ok := h.seen[id]; !ok || at > prev {
		h.seen[id] = at
	}
}

func (h *memHistory) Sessions() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessions
}

// fakeClock is the core.Clock the real-monitor factory hands obs.New. §6.1 bans wall-clock sleeps
// and a fixed clock keeps every metrics timestamp deterministic.
type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time                  { return c.now }
func (c fakeClock) Since(t time.Time) time.Duration { return c.now.Sub(t) }

func newFakeClock() fakeClock {
	return fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

// newRealMonitor builds the monitor SP-01 actually ships, over a fresh temp project. Every call
// gets its own state path, which the suite requires: several behaviour cases drive the §12.1
// degrade/restore state machine and must not inherit a previous case's persisted mode.
func newRealMonitor(t *testing.T) contract.Monitor {
	t.Helper()
	statePath := filepath.Join(paths.Of(t.TempDir()).State, "contract.json")
	return contract.NewMonitor(logging.Nop(), obs.New(newFakeClock()), statePath)
}

// TestContractSuite_ShapePassesAgainstStub is the mandatory per-suite-package assertion: both
// suites' shape blocks pass against a deliberately stubbed implementation, and both behaviour
// blocks are skipped with the exact Rule W-1 message.
//
// Each suite runs inside its own subtest because Rule W-1's skip is a t.Skip on the calling test:
// invoking both suites directly would let the first one's skip abort the second before its shape
// block ever ran.
func TestContractSuite_ShapePassesAgainstStub(t *testing.T) {
	t.Run("monitor", func(t *testing.T) {
		contracttest.RunMonitorSuite(t, "fake-stub-monitor", func(t *testing.T) contract.Monitor {
			return fakeStubMonitor{}
		})
	})
	t.Run("history", func(t *testing.T) {
		contracttest.RunHistorySuite(t, "fake-stub-history", func(t *testing.T) contract.History {
			return fakeStubHistory{}
		})
	})
}

// TestRunMonitorSuite_StubIsSkipped proves the Rule W-1 probe still discriminates even though
// contract.NewMonitor is real: a Monitor whose Register reports core.ErrNotImplemented skips its
// behaviour block with the exact Rule W-1 message.
func TestRunMonitorSuite_StubIsSkipped(t *testing.T) {
	contracttest.RunMonitorSuite(t, "stub", func(t *testing.T) contract.Monitor {
		return fakeStubMonitor{}
	})
}

// TestRunMonitorSuite_AgainstQompackMonitor is the one that matters: RunMonitorSuite run against
// the Monitor SP-01 actually ships. Its behaviour block is NOT skipped, because §12.1 requires the
// monitor mechanics to be real in wave 0 — so the §12.1 state machine is graded here, today, by
// the same suite SP-05 will be graded by.
func TestRunMonitorSuite_AgainstQompackMonitor(t *testing.T) {
	contracttest.RunMonitorSuite(t, "contract.NewMonitor", newRealMonitor)
}

// TestRunHistorySuite_StubIsSkipped is the ordinary Rule W-1 case: contract ships no History, so a
// stub one skips its behaviour block.
func TestRunHistorySuite_StubIsSkipped(t *testing.T) {
	contracttest.RunHistorySuite(t, "stub", func(t *testing.T) contract.History {
		return fakeStubHistory{}
	})
}

// TestRunHistorySuite_AgainstAnInMemoryHistory runs the History behaviour block against a minimal
// correct implementation, proving the specification SP-05 inherits is satisfiable rather than
// merely unrun.
func TestRunHistorySuite_AgainstAnInMemoryHistory(t *testing.T) {
	contracttest.RunHistorySuite(t, "in-memory", func(t *testing.T) contract.History {
		return newMemHistory()
	})
}

// TestRunHistorySuite_AgainstSessionHistory runs the History conformance suite against
// contract.SessionHistory, SP-05's real cross-session implementation: this is what flips Rule W-1's
// skip off for good — the behaviour block now runs against the History every real caller uses,
// not only against the suite's own throwaway memHistory.
func TestRunHistorySuite_AgainstSessionHistory(t *testing.T) {
	contracttest.RunHistorySuite(t, "contract.SessionHistory", func(t *testing.T) contract.History {
		return &contract.SessionHistory{}
	})
}
