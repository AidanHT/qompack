package daemon

import (
	"context"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/stretchr/testify/require"
)

// These are gate M5-G15-C's daemon-side assertions: the real composition root running the real
// selector and producing the outcome the real rehydrator consumes.
//
// The most important case in the file is the FIRST one. Selection ships off, so the disabled path
// is the path essentially every session takes, and if it were not exactly the pre-SP-15 path then
// the feature would have shipped a behaviour change to everyone who never enabled it.

// selTestBudget is a budget generous enough that nothing is refused for size alone; the cases that
// care about pressure name their own.
const selTestBudget = core.Tokens(4000)

// withPSelection opens the process-wide closing-note-3 gate for one test and restores it after.
// The gate is a process-wide atomic, so none of these cases may run in parallel.
func withPSelection(t *testing.T) {
	t.Helper()
	scheduler.EnablePSelection()
	t.Cleanup(scheduler.DisablePSelection)
}

// selService builds a rehydrateService with selection enabled or disabled and no estimator wired,
// so pricing takes the documented bare fallback and the numbers in these cases are stable.
func selService(t *testing.T, enabled bool) *rehydrateService {
	t.Helper()
	cfg := config.Defaults()
	cfg.Runtime.Selection.SubmodularEnabled = enabled
	return &rehydrateService{o: RehydrateOptions{Cfg: cfg, Log: logging.Nop()}}
}

// selRec builds one elimination record with a chosen id and status.
func selRec(id string, status negknow.Status) negknow.Record {
	return negknow.Record{
		ID: id, Target: "src/pool.go", Approach: "widen the timeout",
		Reason: "pgbouncer 1.18 caps it upstream",
		Scope:  negknow.ScopeSession, Status: status,
	}
}

func selCheckpoint(recs ...negknow.Record) checkpoint.Checkpoint {
	return checkpoint.Checkpoint{Session: "s1", Seq: 1, Eliminated: recs}
}

func TestSelectionFor_DisabledIsTheShippedPath(t *testing.T) {
	withPSelection(t) // even with the gate WIDE OPEN, the operator switch decides
	s := selService(t, false)

	got := s.selectionFor(context.Background(),
		selCheckpoint(selRec("a", negknow.StatusActive)), selTestBudget, nil)

	require.Nil(t, got,
		"selection ships off; a nil outcome is what makes the disabled path the pre-SP-15 path")
}

func TestSelectionFor_EnabledWithoutTheShipOrderGateDegrades(t *testing.T) {
	// The operator switch is on in a process where p-selection never came up. NewSelector refuses
	// with ErrNotImplemented, and that is a CONFIGURATION state, not a fault: the rehydration must
	// still happen, on the heuristic that shipped.
	scheduler.DisablePSelection()
	s := selService(t, true)

	got := s.selectionFor(context.Background(),
		selCheckpoint(selRec("a", negknow.StatusActive)), selTestBudget, nil)

	require.Nil(t, got, "a refused constructor degrades to the shipped path, it does not fail the session")
}

func TestSelectionFor_NoCandidatesIsNilNotAnEmptySelection(t *testing.T) {
	withPSelection(t)
	s := selService(t, true)

	got := s.selectionFor(context.Background(), selCheckpoint(), selTestBudget, nil)

	require.Nil(t, got,
		"no candidates means no selection ran; an empty outcome would instead assert that a "+
			"selection ran and chose nothing, which would empty item 3")
}

func TestSelectionFor_CarriesActiveEliminations(t *testing.T) {
	withPSelection(t)
	s := selService(t, true)

	got := s.selectionFor(context.Background(),
		selCheckpoint(selRec("a", negknow.StatusActive), selRec("b", negknow.StatusActive)),
		selTestBudget, nil)

	require.NotNil(t, got)
	require.ElementsMatch(t, []dag.NodeID{"a", "b"}, got.Keep)
	require.False(t, got.Overflow)
	require.Empty(t, got.Archive)
	require.Positive(t, int(got.Tokens), "a carried selection has a priced cost")
}

// TestEliminationCandidates_OnlyActiveRecordsBind is the G6.3 assertion, and it is the reason the
// qualification is carried rather than collapsed.
//
// An elimination is a conditional fact, so a stale one must not be forced into the payload as
// though it still held. Mandatory is what forces; Qualification.Active() is what gates Mandatory.
func TestEliminationCandidates_OnlyActiveRecordsBind(t *testing.T) {
	s := selService(t, true)
	cands := s.eliminationCandidates(context.Background(), selCheckpoint(
		selRec("active", negknow.StatusActive),
		selRec("stale", negknow.StatusStale),
		selRec("odd", negknow.Status("something-this-build-does-not-know")),
	), nil)

	require.Len(t, cands, 3)
	byID := map[dag.NodeID]bool{}
	for _, c := range cands {
		byID[c.Item] = c.Mandatory
		for _, r := range c.Reps {
			require.Equal(t, c.Item, r.Item, "a representation belongs to its own item")
		}
	}
	require.True(t, byID["active"], "a current elimination is authoritative and must be carried")
	require.False(t, byID["stale"], "a stale elimination is carried on merit, never forced")
	require.False(t, byID["odd"],
		"an unrecognised status is UNKNOWN applicability; the safe reading is that it does not bind")
}

func TestEliminationCandidates_QualificationsSurviveTranslation(t *testing.T) {
	s := selService(t, true)
	cands := s.eliminationCandidates(context.Background(), selCheckpoint(
		selRec("active", negknow.StatusActive),
		selRec("stale", negknow.StatusStale),
		selRec("odd", negknow.Status("unheard-of")),
	), nil)

	want := map[dag.NodeID]string{"active": "current", "stale": "stale", "odd": "uncertain"}
	for _, c := range cands {
		for _, r := range c.Reps {
			require.Equal(t, want[c.Item], r.Prov.Qualification.String(),
				"%s must keep its qualification through the negknow-to-analyzer translation", c.Item)
		}
	}
}

func TestEliminationCandidates_EveryItemOffersAPointerFallback(t *testing.T) {
	// Under pressure the selector must be able to name a record rather than drop it, which it can
	// only do if a cheaper representation exists to fall back to.
	s := selService(t, true)
	cands := s.eliminationCandidates(context.Background(),
		selCheckpoint(selRec("a", negknow.StatusActive)), nil)

	require.Len(t, cands, 1)
	require.Len(t, cands[0].Reps, 2)
	require.Less(t, int(cands[0].Reps[1].AssembledCost), int(cands[0].Reps[0].AssembledCost),
		"the pointer must be cheaper than the line, or it is not a fallback")
	require.Less(t, cands[0].Reps[1].Coverage, cands[0].Reps[0].Coverage,
		"and it must cover less, or the line would never be worth buying")
}

// TestSelectionFor_PressureDegradesToAPointerBeforeOverflowing pins the ORDER of the two
// concessions, which is the whole reason an elimination offers a pointer at all.
//
// A budget too small for the rendered line is not too small to name the record. The session that
// is told "this was already tried, call already_tried for the details" still has the constraint;
// the session that is told nothing has lost it. So the pointer must be spent before overflow is
// even considered, and an overflow that fires while a pointer would have fitted is a bug.
func TestSelectionFor_PressureDegradesToAPointerBeforeOverflowing(t *testing.T) {
	withPSelection(t)
	s := selService(t, true)
	rec := selRec("elim-pool-timeout", negknow.StatusActive)

	// Enough for the pointer (the id), nowhere near enough for the rendered line.
	budget := s.priceString(rec.ID)
	got := s.selectionFor(context.Background(), selCheckpoint(rec), budget, nil)

	require.NotNil(t, got)
	require.False(t, got.Overflow, "a pointer fits, so there is nothing to overflow")
	require.Equal(t, []dag.NodeID{dag.NodeID(rec.ID)}, got.Keep,
		"the constraint is kept, at the fidelity the budget allows")
	require.LessOrEqual(t, int(got.Tokens), int(budget), "and still inside the budget")
}

func TestSelectionFor_OverflowsRatherThanDroppingAConstraint(t *testing.T) {
	withPSelection(t)
	s := selService(t, true)
	rec := selRec("elim-pool-timeout", negknow.StatusActive)

	// One token below the cheapest representation: now nothing can carry it.
	budget := s.priceString(rec.ID) - 1
	require.Positive(t, int(budget), "fixture sanity: the budget must still be a real number")

	got := s.selectionFor(context.Background(), selCheckpoint(rec), budget, nil)

	require.NotNil(t, got)
	require.True(t, got.Overflow, "a mandatory record that cannot be carried is an explicit overflow")
	require.Equal(t, rec.ID, got.Item, "the overflow names the record in the id column")
	require.Contains(t, got.Reason, rec.ID, "and explains it in prose for the drop report detail")
	require.Contains(t, got.Archive, dag.NodeID(rec.ID), "and it stays recoverable from the archive")
	require.Empty(t, got.Keep, "never a partial serialization, never a dropped constraint")
}

func TestSelectionFor_AStaleRecordNeverOverflows(t *testing.T) {
	// Overflow is what a MANDATORY record does when it cannot be carried. A stale record is
	// carried on merit, so a budget too small for it is an ordinary non-selection — reporting an
	// overflow here would announce a lost constraint that was never binding.
	withPSelection(t)
	s := selService(t, true)
	rec := selRec("elim-pool-timeout", negknow.StatusStale)

	got := s.selectionFor(context.Background(), selCheckpoint(rec), 1, nil)

	if got != nil {
		require.False(t, got.Overflow, "a non-binding record has no constraint to overflow")
	}
}

func TestSelectionFor_IsDeterministic(t *testing.T) {
	withPSelection(t)
	s := selService(t, true)
	cp := selCheckpoint(
		selRec("c", negknow.StatusActive), selRec("a", negknow.StatusActive),
		selRec("b", negknow.StatusStale),
	)

	first := s.selectionFor(context.Background(), cp, selTestBudget, nil)
	require.NotNil(t, first)
	for i := 0; i < 10; i++ {
		require.Equal(t, first, s.selectionFor(context.Background(), cp, selTestBudget, nil))
	}
}

func TestSelectableEliminations_TheLedgerCopyWins(t *testing.T) {
	// The checkpoint's copy is frozen at write time; the ledger's carries the current status. A
	// record that went stale after the checkpoint was written must not be re-promoted by it.
	s := selService(t, true)
	led := &selFakeLedger{active: map[negknow.Scope][]negknow.Record{
		negknow.ScopeSession: {selRec("a", negknow.StatusStale)},
	}}

	got := s.selectableEliminations(context.Background(),
		selCheckpoint(selRec("a", negknow.StatusActive)), led)

	require.Len(t, got, 1, "the union is deduplicated by id")
	require.Equal(t, negknow.StatusStale, got[0].Status, "the ledger copy wins over the frozen one")
}

func TestSelectableEliminations_ALedgerErrorLeavesTheFrozenCopyStanding(t *testing.T) {
	s := selService(t, true)
	led := &selFakeLedger{err: core.ErrNotFound}

	got := s.selectableEliminations(context.Background(),
		selCheckpoint(selRec("a", negknow.StatusActive)), led)

	require.Len(t, got, 1, "a ledger error degrades to the checkpoint's own copy, never to nothing")
	require.Equal(t, "a", got[0].ID)
}

// selFakeLedger answers Active and nothing else; every other method is inherited from a nil
// embedded interface, so an unexpected call panics loudly rather than returning a plausible zero.
type selFakeLedger struct {
	negknow.Ledger
	active map[negknow.Scope][]negknow.Record
	err    error
}

func (l *selFakeLedger) Active(_ context.Context, scope negknow.Scope) ([]negknow.Record, error) {
	if l.err != nil {
		return nil, l.err
	}
	return l.active[scope], nil
}
