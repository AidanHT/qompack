package analyzer_test

import (
	"context"
	"testing"

	"github.com/qompack/qompack/internal/analyzer"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/stretchr/testify/require"
)

// The compaction point and the lambda/lazy settings every case below shares. None duplicates a
// config default, so the fixtures stay readable without tripping D11 even if this file ever
// stopped being a _test.go file.
const (
	fixtureP      = 100
	fixtureLambda = 0.5
)

// Every case below is written to hold in BOTH builds — before and after SP-12 flips
// scheduler.PSelectionAvailable() — so that none of them needs a t.Skip. The only skip reason
// permitted anywhere in this subplan's tree is Rule W-1's, and a constructor guard SP-01
// implements for real is never skipped (§14.1 rule 3 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md).

// TestNewSelector_RefusesABlockBeforeP is §13 invariant 4: nothing scattered before p. The
// candidate set is filtered in the constructor, so a pre-p block cannot even be handed to a
// Selector, let alone selected by one. This holds regardless of the ship-order gate.
func TestNewSelector_RefusesABlockBeforeP(t *testing.T) {
	for _, pos := range []int{0, 1, fixtureP - 1} {
		blocks := []analyzer.Block{{ID: dag.NodeID("tooluse:early"), Pos: pos}}

		sel, err := analyzer.NewSelector(fixtureP, blocks, dag.Slice{}, nil, fixtureLambda, true)
		require.Nil(t, sel, "a refused construction must not hand back a usable Selector")
		require.ErrorIs(t, err, core.ErrBudget, "a block before p is core.ErrBudget (pos=%d)", pos)
		require.Contains(t, err.Error(), "tooluse:early", "the error must name the offending block")
		require.Contains(t, err.Error(), "invariant 4", "the error must name the invariant it enforces")
	}
}

// TestNewSelector_PosCheckRunsBeforeTheShipOrderCheck pins the normative ORDER of the two guards.
// A candidate set containing one legal and one pre-p block must report the invariant-4 error, so
// that the reason a caller sees is the structural one rather than the temporary ship-order one —
// and must keep reporting it after SP-12 opens the gate.
func TestNewSelector_PosCheckRunsBeforeTheShipOrderCheck(t *testing.T) {
	blocks := []analyzer.Block{
		{ID: dag.NodeID("tooluse:late"), Pos: fixtureP + 1},
		{ID: dag.NodeID("tooluse:early"), Pos: fixtureP - 1},
	}

	_, err := analyzer.NewSelector(fixtureP, blocks, dag.Slice{}, nil, fixtureLambda, true)
	require.ErrorIs(t, err, core.ErrBudget)
	require.False(t, core.IsNotImplemented(err),
		"the ship-order error must never mask the §13 invariant 4 error")
}

// TestNewSelector_ShipOrderGateDecidesTheLegalCandidateSet is the closing note's priority 3: do
// not ship submodular selection before p-selection. With every block legal, exactly two outcomes
// are permitted, and which one applies is decided solely by scheduler.PSelectionAvailable().
func TestNewSelector_ShipOrderGateDecidesTheLegalCandidateSet(t *testing.T) {
	blocks := []analyzer.Block{
		{ID: dag.NodeID("tooluse:at-p"), Pos: fixtureP},
		{ID: dag.NodeID("tooluse:after-p"), Pos: fixtureP + 1},
	}

	sel, err := analyzer.NewSelector(fixtureP, blocks, dag.Slice{}, nil, fixtureLambda, true)

	if scheduler.PSelectionAvailable() {
		require.NoError(t, err, "with p-selection available, a legal candidate set constructs")
		require.NotNil(t, sel)
		require.Equal(t, fixtureP, sel.P(), "a Selector reports the p it was constructed with")
		return
	}

	require.Nil(t, sel)
	require.True(t, core.IsNotImplemented(err), "the ship-order gate reports core.ErrNotImplemented")
	require.Contains(t, err.Error(), "p-selection", "the error must name what is missing")
}

// TestNewSelector_PosEqualToPIsLegal pins the boundary: the rule is "before p", so a block AT p
// is a candidate. Asserting only that the error is NOT ErrBudget keeps the case meaningful in
// both builds.
func TestNewSelector_PosEqualToPIsLegal(t *testing.T) {
	blocks := []analyzer.Block{{ID: dag.NodeID("tooluse:at-p"), Pos: fixtureP}}

	_, err := analyzer.NewSelector(fixtureP, blocks, dag.Slice{}, nil, fixtureLambda, true)
	require.NotErrorIs(t, err, core.ErrBudget, "a block exactly at p is not before p")
}

// TestNewSelector_EmptyCandidateSetStillConsultsTheShipOrderGate asserts the guards are not
// short-circuited by an empty candidate set: with nothing to filter, the ship-order gate is still
// consulted rather than skipped.
func TestNewSelector_EmptyCandidateSetStillConsultsTheShipOrderGate(t *testing.T) {
	sel, err := analyzer.NewSelector(fixtureP, nil, dag.Slice{}, nil, fixtureLambda, false)

	if scheduler.PSelectionAvailable() {
		require.NoError(t, err)
		require.NotNil(t, sel)
		return
	}
	require.True(t, core.IsNotImplemented(err))
}

// TestStubSelector_SelectIsNotImplemented documents the split §14.1 rule 3 draws through this
// package: the CONSTRUCTOR is real, the selection behind it is not. It is written so that it
// asserts nothing at all in a build where construction is refused, and the real thing in a build
// where it succeeds.
func TestStubSelector_SelectIsNotImplemented(t *testing.T) {
	sel, err := analyzer.NewSelector(fixtureP, nil, dag.Slice{}, nil, fixtureLambda, false)
	if err != nil {
		require.True(t, core.IsNotImplemented(err))
		return
	}
	_, err = sel.Select(context.Background(), core.Tokens(1000))
	if err != nil {
		require.True(t, core.IsNotImplemented(err),
			"Select is either the SP-01 stub or SP-15's real implementation, never anything else")
	}
}
