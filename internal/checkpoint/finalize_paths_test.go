package checkpoint_test

// Finalize's and PreCompact's failure and fallback paths -- the branches that only run when
// something has already gone wrong, which is exactly when their behaviour matters most.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

func TestFinalizeRetriesPastAClaimedSequenceNumber(t *testing.T) {
	f := newFx(t)
	d := seedDraft(t, f)

	// Plant a file at the sequence number the draft intends to claim. In production this is
	// another process that won the race between reading maxSeq and writing; the artifact is
	// created with O_EXCL precisely so the loser finds out rather than overwriting an immutable
	// checkpoint someone else just sealed.
	l := paths.Of(f.p.Root)
	claimed := paths.CheckpointPath(l, 1)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(claimed)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(claimed), []byte(`{"version":1}`), 0o444))

	ref, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err, "a claimed sequence number is retried, not fatal")
	require.Equal(t, core.CheckpointSeq(2), ref.Seq, "the writer takes the next free number")

	// And the file it did not write is untouched: overwriting it is the one thing O_EXCL exists
	// to prevent.
	raw, err := os.ReadFile(paths.Long(claimed))
	require.NoError(t, err)
	require.Equal(t, `{"version":1}`, string(raw))
}

func TestFinalizeFallsBackToTheConfiguredBudget(t *testing.T) {
	f := newFx(t)
	d := seedDraft(t, f)

	// A caller that passes no budget gets the configured one rather than zero. Treating 0 as a
	// literal budget would truncate every checkpoint to nothing, and the caller most likely to
	// omit it is a future subplan calling Finalize directly.
	ref, err := f.w.Finalize(f.ctx(), d, 0)
	require.NoError(t, err)
	raw, err := os.ReadFile(paths.Long(ref.Path))
	require.NoError(t, err)
	cp, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err)
	require.NotEmpty(t, cp.UserIntent.Original, "tier 1 survives; the budget was not read as zero")
}

// gcStore reports every object as absent, standing in for a store that has garbage-collected the
// tool results a draft still points at.
type gcStore struct{ store.Store }

func (gcStore) Has(core.Hash) bool { return false }

func TestFinalizeDropsToolPointersTheStoreNoLongerHolds(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "Check the pool statistics.", true)
	f.tool("toolu_gc_0001", 1, "Bash", "", "pool statistics for the failing window", false)
	f.closedSeg(1, 0, 3)

	collected := f.src
	collected.Store = gcStore{Store: f.src.Store}
	d, err := f.w.Begin(f.ctx(), f.sess, 0, collected)
	require.NoError(t, err)
	_, err = f.w.Advance(f.ctx(), d, []core.SegmentID{1})
	require.NoError(t, err)

	ref, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err)
	raw, err := os.ReadFile(paths.Long(ref.Path))
	require.NoError(t, err)
	cp, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err)

	// A pointer whose object is gone is worse than no pointer: expand(hash) would fail and the
	// reader would spend a turn discovering that the checkpoint lied to it.
	require.Empty(t, cp.Pointers.Tools)
	var found bool
	for _, dr := range cp.Dropped {
		if dr.Kind == "pointer_unresolvable" {
			found = true
			require.Equal(t, "toolu_gc_0001", dr.ID)
		}
	}
	require.True(t, found, "the removal is reported, so a reader can tell it happened; got %+v", cp.Dropped)
}

// unlistableSegments answers Unencoded with an error, which is what a store mid-corruption looks
// like from the cold PreCompact path.
type unlistableSegments struct{ store.SegmentLog }

func (unlistableSegments) Unencoded(context.Context, core.SessionID) ([]store.Segment, error) {
	return nil, errInjected
}

func TestColdPreCompactSealsEvenWhenSegmentsCannotBeListed(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "Investigate the exhaustion.", true)
	f.closedSeg(1, 0, 4)

	broken := f.src
	broken.Segments = unlistableSegments{SegmentLog: f.src.Segments}
	require.NoError(t, f.w.SetSources(broken),
		"Segments is still non-nil here; only Unencoded fails, and only once PreCompact calls it")

	// A catch-up we cannot enumerate is not a reason to lose the checkpoint. Sealing what the
	// draft already has beats returning an error, because the error path ends with the session
	// having no durable artifact at all -- which is the outcome this whole layer exists to prevent.
	res, err := f.w.PreCompact(f.ctx(), f.precompactInput())
	require.NoError(t, err)
	require.True(t, res.NewDraft)
	require.FileExists(t, paths.Long(res.Ref.Path))
}

func TestPreCompactUsesTheLiveDraftWhenOneIsOpen(t *testing.T) {
	f := newFx(t)
	seedForPreCompact(t, f)
	live := f.w.DraftFor(f.sess)
	require.NotNil(t, live)

	res, err := f.w.PreCompact(f.ctx(), f.precompactInput())
	require.NoError(t, err)
	require.False(t, res.NewDraft,
		"the warm path reuses the draft the idle tasks have been advancing; that IS the O5 amortization")
	require.Greater(t, int(res.Ref.Frontier), 0)
}

func TestDraftQuestionsSplitUserFromDerivedAcrossAResume(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "Why does refresh time out?", true)
	f.closedSeg(1, 0, 3)
	d := f.begin()
	d.AddOpenQuestion("A question the caller asked explicitly.")
	f.advance(d, 1)

	_, cp := f.persisted()
	require.Contains(t, cp.OpenQuestions, "A question the caller asked explicitly.")

	// The split survives the round trip through state/draft-<session>.json: on resume, the
	// caller's questions must still be distinguishable from the ones Advance derived, or a restart
	// would silently promote a derived guess to something a human is recorded as having asked.
	resumed, err := f.w.Begin(f.ctx(), f.sess, 0, f.src)
	require.NoError(t, err)
	require.Same(t, d, resumed)

	_, cp2 := f.persisted()
	require.Contains(t, cp2.OpenQuestions, "A question the caller asked explicitly.")
}
