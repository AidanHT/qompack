package negknowtest_test

import (
	"context"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/negknow/negknowtest"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
)

// fakeStubLedger mirrors the shape of an SP-01-style stub Ledger: every operation reports the
// documented zero value or core.ErrNotImplemented, exactly like negknow.Open's own stub does
// today. It exists only to exercise RunLedgerSuite before SP-09 ships a real Ledger.
type fakeStubLedger struct{}

func (fakeStubLedger) Record(ctx context.Context, r negknow.Record) (string, error) {
	return "", core.ErrNotImplemented
}

func (fakeStubLedger) Query(ctx context.Context, target, approach string, scope negknow.Scope) (negknow.Answer, error) {
	return negknow.Answer{}, core.ErrNotImplemented
}

func (fakeStubLedger) Get(ctx context.Context, id string) (negknow.Record, error) {
	return negknow.Record{}, core.ErrNotImplemented
}

func (fakeStubLedger) Active(ctx context.Context, scope negknow.Scope) ([]negknow.Record, error) {
	return nil, core.ErrNotImplemented
}

func (fakeStubLedger) All(ctx context.Context) ([]negknow.Record, error) {
	return nil, core.ErrNotImplemented
}

func (fakeStubLedger) MarkStale(ctx context.Context, ids []string, because []string) error {
	return core.ErrNotImplemented
}

func (fakeStubLedger) RefreshStaleness(ctx context.Context, s store.Store) ([]string, error) {
	return nil, core.ErrNotImplemented
}

func (fakeStubLedger) RebuildBloom(ctx context.Context) (*sketch.Bloom, negknow.Health, error) {
	return nil, negknow.Health{}, core.ErrNotImplemented
}
func (fakeStubLedger) Health() negknow.Health { return negknow.Health{} }
func (fakeStubLedger) Close() error           { return core.ErrNotImplemented }

// TestRunLedgerSuite_StubIsSkipped proves the suite's shape block passes against a stub Ledger
// and that its behaviour block is skipped with the exact Rule W-1 message. SP-09 reuses
// RunLedgerSuite unchanged, pointed at its real implementation, to flip that skip off.
func TestRunLedgerSuite_StubIsSkipped(t *testing.T) {
	negknowtest.RunLedgerSuite(t, "fake-stub", func(t *testing.T) negknow.Ledger {
		return fakeStubLedger{}
	})
}

// TestRunLedgerSuite_AgainstQompackOpen exercises RunLedgerSuite against the ledger negknow.Open
// actually returns, end to end. As of SP-09 that is the real ledger, so the suite's behaviour
// block runs rather than skipping — which is what Rule W-1's runtime probe exists to switch on.
//
// The config is deliberately the ZERO config.Config rather than a loaded one. Every Appendix C
// value the ledger reads is defaulted inside Open (normalizeEliminations, newConfiguredBloom), so
// this factory exercises the degradation path a composition root that has not loaded
// configuration yet would take — and the whole behaviour block has to pass over it. The
// real-defaults path is the other half, and negknow's own TestLedgerConformance runs the same
// suite over testutil.NewProject's fully loaded configuration.
func TestRunLedgerSuite_AgainstQompackOpen(t *testing.T) {
	negknowtest.RunLedgerSuite(t, "negknow.Open", func(t *testing.T) negknow.Ledger {
		l, err := negknow.Open(t.TempDir(), config.Config{}, nil, negknow.Deps{})
		if err != nil {
			t.Fatal(err)
		}
		// A real ledger holds an open append-only handle on records/eliminations.jsonl, so it
		// MUST be closed before the test's TempDir is removed: on Windows an open handle makes
		// RemoveAll fail and the test error out during cleanup. The SP-01 stub held no handles,
		// which is why this was not needed until SP-09 landed — the same one-line addition
		// internal/store/storetest/suite_test.go needed when SP-06 replaced its own stub.
		t.Cleanup(func() { _ = l.Close() })
		return l
	})
}
