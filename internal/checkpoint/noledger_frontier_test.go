package checkpoint_test

// No ledger yet is not an error (coordinator decision D49 of the V6 close-out, finding F-C4-C49-3
// of the candidate 4 live re-run, plans/sdd/V6-closeout/live/rerun-c4/C4.9/notes.txt).
//
// The daemon opens its elimination ledger lazily: on a compaction, or on the first already_tried or
// record_eliminated call. In a session with neither, the checkpoint SourceSet's ledger accessor
// answers nil for the daemon's whole life, and act.advance_frontier failed every 30 s with
// "SourceSet.Ledger is nil: its accessor resolved to no ledger" — the frontier never advanced.
// While the project holds no elimination record at all there is no negative knowledge to read, so
// the frontier advances without it; once a ledger exists it is read as before.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
)

// lazyLedgerSupplier is the production supplier's shape (internal/cli wireCheckpointSources): the
// ledger arrives only through the accessor, which answers *cell (nil until something opens one),
// and a set that does not resolve travels WITH its reason, wrapped in core.ErrDegraded.
func lazyLedgerSupplier(f *fx, cell *negknow.Ledger) func() (checkpoint.SourceSet, error) {
	return func() (checkpoint.SourceSet, error) {
		src := f.src
		src.Ledger = *cell
		src.LedgerFn = func() negknow.Ledger { return *cell }
		if _, err := src.Resolve(); err != nil {
			return src, fmt.Errorf("%w: %w", err, core.ErrDegraded)
		}
		return src, nil
	}
}

// withoutLedgerLog removes the (empty) elimination log newFx's ledger created, which is the state
// of a project in which nothing has ever opened a ledger.
func withoutLedgerLog(t *testing.T, f *fx) {
	t.Helper()
	require.NoError(t, f.ledger.Close())
	require.NoError(t, os.Remove(paths.Long(filepath.Join(paths.Of(f.p.Root).Records, "eliminations.jsonl"))))
}

// TestFrontierAdvancer_AdvancesBeforeAnyLedgerExists is F-C4-C49-3: no ledger has been opened and
// the project has no elimination record, so the frontier advances, without negative knowledge.
func TestFrontierAdvancer_AdvancesBeforeAnyLedgerExists(t *testing.T) {
	f := newFx(t)
	withoutLedgerLog(t, f)
	var cell negknow.Ledger
	advancer := checkpoint.NewFrontierAdvancer(f.w, lazyLedgerSupplier(f, &cell))

	f.tool("tu_nl_1", 2, "Read", "src/a.go", "package a", false)
	f.closedSeg(1, 0, 3)
	fr, err := advancer.Advance(f.ctx(), f.sess, []core.SegmentID{1})
	require.NoError(t, err, "no ledger yet is not an error")
	require.Equal(t, core.TurnIndex(3), fr, "and the frontier advances")

	_, cp := f.persisted()
	require.Empty(t, cp.Eliminated, "there was no negative knowledge to carry")
	require.NoFileExists(t, paths.Long(filepath.Join(paths.Of(f.p.Root).Records, "eliminations.jsonl")),
		"advancing without a ledger opens none")
}

// TestFrontierAdvancer_ReadsTheLedgerOnceOneExists: the draft a no-ledger advance began reads the
// ledger as soon as one exists — at the next advance, and at the seal.
func TestFrontierAdvancer_ReadsTheLedgerOnceOneExists(t *testing.T) {
	f := newFx(t)
	var cell negknow.Ledger
	advancer := checkpoint.NewFrontierAdvancer(f.w, lazyLedgerSupplier(f, &cell))

	f.closedSeg(1, 0, 3)
	_, err := advancer.Advance(f.ctx(), f.sess, []core.SegmentID{1})
	require.NoError(t, err)

	// A record_eliminated call opens the ledger and records into it.
	cell = f.ledger
	ctx := negknow.WithCaller(f.ctx(), negknow.Caller{Session: f.sess, Turn: 5})
	id, err := f.ledger.Record(ctx, negknow.Record{
		Target: "src/pool.go:DialPool", Approach: "widen pool timeout", Reason: "max_idle caps it",
		Evidence: core.HashBytes(core.DomainChunk, []byte("pool evidence")),
	})
	require.NoError(t, err)

	f.closedSeg(2, 4, 7)
	fr, err := advancer.Advance(f.ctx(), f.sess, []core.SegmentID{2})
	require.NoError(t, err)
	require.Equal(t, core.TurnIndex(7), fr)
	_, cp := f.persisted()
	require.Len(t, cp.Eliminated, 1, "the next advance reads the ledger that now exists")
	require.Equal(t, id, cp.Eliminated[0].ID)
}

// TestPreCompact_SealsTheLedgerThatOpenedAfterANoLedgerAdvance: a live draft begun before any
// ledger existed is sealed against the ledger the compaction opened, not against the nil it began
// with.
func TestPreCompact_SealsTheLedgerThatOpenedAfterANoLedgerAdvance(t *testing.T) {
	f := newFx(t)
	var cell negknow.Ledger
	advancer := checkpoint.NewFrontierAdvancer(f.w, lazyLedgerSupplier(f, &cell))

	f.prompt(0, "Fix the pool starvation.", true)
	f.closedSeg(1, 0, 3)
	_, err := advancer.Advance(f.ctx(), f.sess, []core.SegmentID{1})
	require.NoError(t, err)

	cell = f.ledger // the compaction's lazy open
	ctx := negknow.WithCaller(f.ctx(), negknow.Caller{Session: f.sess, Turn: 6})
	id, err := f.ledger.Record(ctx, negknow.Record{
		Target: "src/pool.go:DialPool", Approach: "widen pool timeout", Reason: "max_idle caps it",
		Evidence: core.HashBytes(core.DomainChunk, []byte("pool evidence")),
	})
	require.NoError(t, err)

	res, err := f.w.PreCompact(f.ctx(), f.precompactInput())
	require.NoError(t, err)
	cp := f.sealed(t, res.Ref.Seq)
	require.Len(t, cp.Eliminated, 1, "the seal reads the ledger that exists at the seal")
	require.Equal(t, id, cp.Eliminated[0].ID)
	require.Len(t, rejected(cp, "widen pool timeout"), 1, "and mints its decision")
}

// TestFrontierAdvancer_NoLedgerOverRecordsStaysUnavailable: with elimination records on disk, a
// ledger that is still missing is a real gap, not "no negative knowledge": the advance refuses
// rather than begin a draft that would carry none of them.
func TestFrontierAdvancer_NoLedgerOverRecordsStaysUnavailable(t *testing.T) {
	f := newFx(t)
	f.elim(f.sess, negknow.ScopeProject, "src/cache.go", "drop the cache", "load-bearing")
	var cell negknow.Ledger
	advancer := checkpoint.NewFrontierAdvancer(f.w, lazyLedgerSupplier(f, &cell))

	f.closedSeg(1, 0, 3)
	_, err := advancer.Advance(f.ctx(), f.sess, []core.SegmentID{1})
	require.Error(t, err)
	require.True(t, errors.Is(err, checkpoint.ErrNoLedger), "reported as the missing ledger it is: %v", err)
	require.Nil(t, f.w.DraftFor(f.sess), "no draft begins without the records it would carry")
}
