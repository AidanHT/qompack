package daemon

// No ledger yet is not an error (coordinator decision D49, finding F-C4-C49-3 of the candidate 4
// live re-run, plans/sdd/V6-closeout/live/rerun-c4/C4.9/notes.txt): in a session with no
// compaction and no elimination the daemon never opens its ledger, and the frontier must still
// advance, quietly.

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// productionShapedSources is internal/cli's wireCheckpointSources supplier with no ledger open: the
// ledger arrives only through an accessor that answers nil, and the partial set travels with
// Resolve's reason wrapped in core.ErrDegraded.
func productionShapedSources(f *cpFixture, cell *negknow.Ledger) func() (checkpoint.SourceSet, error) {
	return func() (checkpoint.SourceSet, error) {
		s := f.src
		s.Ledger = *cell
		s.LedgerFn = func() negknow.Ledger { return *cell }
		if _, err := s.Resolve(); err != nil {
			return s, fmt.Errorf("%w: %w", err, core.ErrDegraded)
		}
		return s, nil
	}
}

// TestAdvanceFrontierTask_AdvancesBeforeAnyLedgerExists is the idle task advance_frontier with no
// ledger open and no elimination on disk: the segment is encoded, the draft exists, and nothing is
// counted or logged as an unavailable source.
func TestAdvanceFrontierTask_AdvancesBeforeAnyLedgerExists(t *testing.T) {
	f := newCPFixture(t)
	f.live(cpSession)
	f.closeSegment(cpSession, 1, 3)

	var cell negknow.Ledger
	log := newRecordingLogger()
	m := obs.New(f.clk)
	advance := advanceFrontierTask(f.reg, f.w, productionShapedSources(f, &cell), log, m)

	for range 3 { // three idle ticks: none may warn
		require.NoError(t, advance(f.ctx()))
	}
	require.FileExists(t, paths.Long(f.draftPath(cpSession)), "the frontier begins its draft without a ledger")
	un, err := f.src.Segments.Unencoded(f.ctx(), cpSession)
	require.NoError(t, err)
	require.Empty(t, un, "and encodes the closed segment: the frontier advances")
	require.Zero(t, m.Snapshot().Counters[counterSourcesUnavailable], "no ledger yet is not an unavailable source")
	require.Empty(t, log.msgs(logWarn), "and nothing is logged as a failure")
	require.Empty(t, log.msgs(logLoud))
}

// TestAdvanceFrontierTask_NoLedgerOverRecordsStaysUnavailable: with an elimination on disk that no
// open ledger serves, the gap is real and the task keeps its unavailable route (reported once).
func TestAdvanceFrontierTask_NoLedgerOverRecordsStaysUnavailable(t *testing.T) {
	f := newCPFixture(t)
	f.live(cpSession)
	f.closeSegment(cpSession, 1, 3)
	ctx := negknow.WithCaller(f.ctx(), negknow.Caller{Session: cpSession, Turn: 2})
	_, err := f.src.Ledger.Record(ctx, negknow.Record{
		Target: "src/cache.go", Approach: "drop the cache", Reason: "load-bearing",
		Evidence: core.HashBytes(core.DomainChunk, []byte("cache evidence")),
	})
	require.NoError(t, err)

	var cell negknow.Ledger
	m := obs.New(f.clk)
	advance := advanceFrontierTask(f.reg, f.w, productionShapedSources(f, &cell), newRecordingLogger(), m)
	require.NoError(t, advance(f.ctx()))
	require.NoFileExists(t, paths.Long(f.draftPath(cpSession)),
		"no draft begins without the records it would carry")
	require.Equal(t, int64(1), m.Snapshot().Counters[counterSourcesUnavailable])
}
