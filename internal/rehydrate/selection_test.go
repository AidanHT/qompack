package rehydrate

import (
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/stretchr/testify/require"
)

// These are gate M5-G15-C's consumer assertions: a selection reaching the REAL item-3 builder,
// not a unit selector return (plans/V5-SP-15-analyzer-selection-and-grammar.md, M5-G15-C).
//
// The rollback case is first and is the most important one in the file. "Disable the selector" is
// only a real switch if the disabled path is the path that shipped, so TestApplySelection_NilIsThe
// RollbackPath asserts identity — the same backing slice, not merely an equal one — rather than
// re-deriving what the old ranking would have produced.

// selElim is a three-record elimination fixture whose ids sort differently from the order a
// selection will impose, so an assertion on order cannot pass by accident.
func selElims() []negknow.Record {
	return []negknow.Record{
		elim("a", "src/pool.go", "widen the timeout", "pgbouncer 1.18 caps it upstream"),
		elim("b", "src/retry.ts", "client-side backoff", "duplicates the charge"),
		elim("c", "src/cache.go", "invalidate on write", "the write path has no key"),
	}
}

func recIDs(recs []negknow.Record) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.ID
	}
	return out
}

func TestApplySelection_NilIsTheRollbackPath(t *testing.T) {
	in := selElims()
	got, drops := applySelection(in, nil)

	require.Nil(t, drops, "a build with no selection reports no selection drops")
	require.Equal(t, len(in), len(got))
	require.Equal(t, recIDs(in), recIDs(got), "order is untouched: the slice-score ranking still decides")
}

func TestApplySelection_KeepOrderReplacesSliceScoreOrder(t *testing.T) {
	got, drops := applySelection(selElims(), &SelectionOutcome{
		Keep: []dag.NodeID{"c", "a", "b"},
	})

	require.Equal(t, []string{"c", "a", "b"}, recIDs(got),
		"Keep is the selector's own ranking and must survive into item 3")
	require.Empty(t, drops, "everything was admitted, so nothing is reported as lost")
}

func TestApplySelection_ArchivedRecordsAreReportedWithARecoveryPath(t *testing.T) {
	got, drops := applySelection(selElims(), &SelectionOutcome{
		Keep:    []dag.NodeID{"a"},
		Archive: []dag.NodeID{"b", "c"},
	})

	require.Equal(t, []string{"a"}, recIDs(got))
	require.Equal(t, []checkpoint.DropEntry{
		{Kind: dropKindArchive, ID: "b", Detail: archiveRecoveryDetail},
		{Kind: dropKindArchive, ID: "c", Detail: archiveRecoveryDetail},
	}, drops)

	for _, d := range drops {
		require.Contains(t, d.Detail, "recoverable",
			"an archive-only outcome without a way back is indistinguishable from a silent drop")
	}
}

func TestApplySelection_AnUnmentionedRecordIsReportedNotVanished(t *testing.T) {
	// "b" appears in neither Keep nor Archive. It must still be accounted for: an item the
	// selector never mentioned is exactly the shape a silent loss would take.
	_, drops := applySelection(selElims(), &SelectionOutcome{
		Keep:    []dag.NodeID{"a"},
		Archive: []dag.NodeID{"c"},
	})

	require.Equal(t, []checkpoint.DropEntry{
		{Kind: dropKindArchive, ID: "c", Detail: archiveRecoveryDetail},
		{Kind: dropKindArchive, ID: "b", Detail: archiveRecoveryDetail},
	}, drops, "the archived record is reported first, then the unmentioned remainder")
}

func TestApplySelection_ChosenWinsOverArchived(t *testing.T) {
	// A malformed selection naming one item in both lists must deliver it once, not twice, and
	// must not report an injected record as archived.
	got, drops := applySelection(selElims(), &SelectionOutcome{
		Keep:    []dag.NodeID{"a", "b", "c"},
		Archive: []dag.NodeID{"a"},
	})

	require.Equal(t, []string{"a", "b", "c"}, recIDs(got))
	require.Empty(t, drops)
}

func TestApplySelection_ARepeatedKeepIDDeliversOneCopy(t *testing.T) {
	got, _ := applySelection(selElims(), &SelectionOutcome{
		Keep: []dag.NodeID{"a", "a", "b", "c"},
	})
	require.Equal(t, []string{"a", "b", "c"}, recIDs(got),
		"at most one representation per item: a repeated id is not a second copy")
}

func TestApplySelection_AnUnknownKeepIDIsIgnored(t *testing.T) {
	// The selector ran before Build, and Build's own stale filter runs after it, so a Keep entry
	// naming a record this build no longer has is an expected disagreement rather than corruption.
	got, drops := applySelection(selElims(), &SelectionOutcome{
		Keep: []dag.NodeID{"a", "gone", "b", "c"},
	})
	require.Equal(t, []string{"a", "b", "c"}, recIDs(got))
	require.Empty(t, drops)
}

func TestApplySelection_OverflowIsNamedAndRecoverable(t *testing.T) {
	_, drops := applySelection(selElims(), &SelectionOutcome{
		Keep:     []dag.NodeID{"a", "b", "c"},
		Overflow: true,
		Item:     "a",
		Reason:   "mandatory item a cannot be carried at any qualified representation within 4 tokens",
	})

	require.Len(t, drops, 1)
	require.Equal(t, dropKindOverflow, drops[0].Kind)
	require.Equal(t, "a", drops[0].ID,
		"the id column carries an IDENTIFIER; the prose belongs in the detail")
	require.Contains(t, drops[0].Detail, "OVERFLOW")
	require.Contains(t, drops[0].Detail, "recoverable")
	require.Contains(t, drops[0].Detail, "within 4 tokens",
		"the selector's own explanation reaches the report rather than being discarded")
	require.True(t, Overflowed(drops),
		"a selection overflow must be recognized by the same predicate every other overflow is")
}

func TestApplySelection_IsDeterministic(t *testing.T) {
	sel := &SelectionOutcome{Keep: []dag.NodeID{"c"}, Archive: []dag.NodeID{"a"}}
	first, firstDrops := applySelection(selElims(), sel)
	for i := 0; i < 20; i++ {
		got, drops := applySelection(selElims(), sel)
		require.Equal(t, recIDs(first), recIDs(got))
		require.Equal(t, firstDrops, drops, "the unmentioned remainder is sorted, not map-ordered")
	}
}

func TestBuildEliminations_HonoursTheSelectionOrder(t *testing.T) {
	// The real builder, not the helper: gate M5-G15-C wants the selection reaching item 3.
	cp := checkpoint.Checkpoint{Session: "s1", Seq: 1}
	cp.Eliminated = selElims()
	r := requestFor(t, cp, generousTestBudget)
	r.Selection = &SelectionOutcome{Keep: []dag.NodeID{"c", "a"}, Archive: []dag.NodeID{"b"}}

	got := buildEliminations(bg(), r, depsWith(&spyLogger{}), nil)

	require.Equal(t, 2, got.seen, "only the admitted records are item 3's population")
	require.Contains(t, got.units[0].text, "src/cache.go", "Keep[0] renders first")
	require.Contains(t, got.units[1].text, "src/pool.go", "Keep[1] renders second")
	require.Contains(t, got.drops, checkpoint.DropEntry{
		Kind: dropKindArchive, ID: "b", Detail: archiveRecoveryDetail,
	}, "the archived record reaches item 7 with its recovery path")
}

func TestBuildEliminations_NilSelectionKeepsTheSliceScoreRanking(t *testing.T) {
	cp := checkpoint.Checkpoint{Session: "s1", Seq: 1}
	cp.Eliminated = selElims()
	r := requestFor(t, cp, generousTestBudget)
	require.Nil(t, r.Selection)

	got := buildEliminations(bg(), r, depsWith(&spyLogger{}), nil)

	require.Equal(t, 3, got.seen)
	for _, d := range got.drops {
		require.NotEqual(t, dropKindArchive, d.Kind,
			"with the selector disabled nothing is archived, because nothing was selected")
	}
}
