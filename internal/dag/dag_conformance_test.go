package dag_test

import (
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/dag/dagtest"
	"github.com/qompack/qompack/internal/logging"
	"github.com/stretchr/testify/require"
)

// TestGraphConformance runs the dagtest suite against the real dag.Open, from this package rather
// than from inside dagtest.
//
// dagtest has a suite_test.go of its own that does something similar, but it lives next to the
// grader, so a change there can be made consistent with the suite without anyone noticing that the
// IMPLEMENTATION no longer conforms. This entry point is on the implementation's side of the fence,
// which is where 00-ARCHITECTURE.md §5.22 means the obligation to sit: every graph SP-07 ships must
// pass RunGraphSuite, and this is the assertion that says so about the one it actually ships.
//
// It is package dag_test rather than package dag because dagtest imports dag; an in-package test
// file importing it would be an import cycle.
func TestGraphConformance(t *testing.T) {
	dagtest.RunGraphSuite(t, "dag.Open", func(t *testing.T) dag.Graph {
		g, err := dag.Open(t.TempDir(), config.Defaults(), logging.Nop())
		require.NoError(t, err)
		return g
	})
}

// TestOpenResultIsMaintainer pins the type assertion SP-05 and SP-12 depend on to register idle
// compaction. Graph deliberately does not carry the maintenance methods — widening it would break
// every existing consumer — so the only thing standing between "idle compaction works" and "idle
// compaction silently never runs" is that this assertion succeeds.
func TestOpenResultIsMaintainer(t *testing.T) {
	g, err := dag.Open(t.TempDir(), config.Defaults(), logging.Nop())
	require.NoError(t, err)

	m, ok := g.(dag.Maintainer)
	require.True(t, ok, "dag.Open's result must satisfy dag.Maintainer")
	require.Equal(t, 0, m.Generation(), "a fresh graph has never been compacted")
	require.False(t, m.NeedsCompaction(), "an empty log is never worth rewriting")
	require.NoError(t, m.Tombstone(nil), "tombstoning nothing is a no-op, not an error")
}
