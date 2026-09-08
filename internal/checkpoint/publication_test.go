package checkpoint_test

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// The publication and recovery rows of plans/V4-SP-10-checkpointer-l4.md: what a caller may still
// rely on after a checkpoint FAILS to publish, and what a reader does with a store whose newest
// artifact no longer verifies.

// reader opens a Reader over f's project.
func (f *fx) reader(t *testing.T) checkpoint.Reader {
	t.Helper()
	r, err := checkpoint.OpenReader(f.p.Root, f.p.Log, obs.New(f.p.Clock))
	require.NoError(t, err)
	return r
}

// blockSeqRange makes every sequence number in [from, from+n) unwritable by occupying its path
// with a DIRECTORY. paths.CreateNew's O_EXCL open then fails as ErrExist at each one, so a
// Finalize that would have landed at `from` exhausts its collision retries and reports
// core.ErrAppendOnly — a publication failure produced without touching production code.
func blockSeqRange(t *testing.T, l paths.Layout, from core.CheckpointSeq, n int) {
	t.Helper()
	for i := range n {
		p := paths.Long(paths.CheckpointPath(l, from+core.CheckpointSeq(i)))
		require.NoError(t, os.MkdirAll(p, 0o755))
	}
}

// TestPublicationFailureRetainsThePreviousCheckpoint is the guarantee a failed publish owes its
// caller: the checkpoint that WAS usable stays usable and stays selectable. A store must never be
// left with no usable checkpoint because the next one could not be written.
func TestPublicationFailureRetainsThePreviousCheckpoint(t *testing.T) {
	f := newFx(t)
	first := seedDraft(t, f)
	ref1, err := f.w.Finalize(f.ctx(), first, finalizeBudget)
	require.NoError(t, err)
	require.Equal(t, core.CheckpointSeq(1), ref1.Seq)

	// The successor draft Finalize opened. Give it something of its own, then make every sequence
	// number it could claim unwritable.
	second := f.w.DraftFor(f.sess)
	require.NotNil(t, second, "Finalize opens a successor so advancement resumes")
	f.closedSeg(2, 4, 6)
	f.advance(second, 2)
	blockSeqRange(t, paths.Of(f.p.Root), 2, 16)

	_, err = f.w.Finalize(f.ctx(), second, finalizeBudget)
	require.Error(t, err, "the publication must fail, not silently succeed elsewhere")

	// 1. The previous checkpoint is still selectable.
	r := f.reader(t)
	cp, ref, err := r.Latest(f.ctx(), f.sess)
	require.NoError(t, err, "a failed publish must not leave the store with no usable checkpoint")
	require.Equal(t, core.CheckpointSeq(1), ref.Seq)
	require.Equal(t, f.sess, cp.Session)

	// 2. Its bytes still verify: nothing was rolled back over it.
	bad, err := r.Verify(f.ctx())
	require.NoError(t, err)
	require.Empty(t, bad)

	// 3. The failed draft is not stranded. Finalize lifts the seal it took when the artifact never
	//    landed, so the same draft can be retried once the obstruction clears.
	require.Same(t, second, f.w.DraftFor(f.sess), "the draft is retained, not discarded")
	blocked := paths.Long(paths.CheckpointPath(paths.Of(f.p.Root), 2))
	require.NoError(t, os.RemoveAll(blocked))
	for i := 3; i < 18; i++ {
		require.NoError(t, os.RemoveAll(paths.Long(paths.CheckpointPath(paths.Of(f.p.Root), core.CheckpointSeq(i)))))
	}
	ref2, err := f.w.Finalize(f.ctx(), second, finalizeBudget)
	require.NoError(t, err, "the retained draft finalizes once the failure clears")
	require.Greater(t, ref2.Seq, ref1.Seq)
}

// TestRollbackToAPriorCheckpointWhenTheNewestIsCorrupt is §12's "refuse to use the affected
// checkpoint, fall back to its parent", asserted end to end over a real writer: two published
// checkpoints, the newer one corrupted on disk, and the reader still resolves the session to the
// older one rather than failing hard or fabricating a chain.
func TestRollbackToAPriorCheckpointWhenTheNewestIsCorrupt(t *testing.T) {
	f := newFx(t)
	first := seedDraft(t, f)
	_, err := f.w.Finalize(f.ctx(), first, finalizeBudget)
	require.NoError(t, err)

	second := f.w.DraftFor(f.sess)
	require.NotNil(t, second)
	f.closedSeg(2, 4, 6)
	f.advance(second, 2)
	ref2, err := f.w.Finalize(f.ctx(), second, finalizeBudget)
	require.NoError(t, err)

	// Corrupt the NEWEST artifact. It is 0444, so the write bit has to be restored first — which
	// is itself the point: nothing in normal operation can do this.
	p := paths.Long(ref2.Path)
	require.NoError(t, os.Chmod(p, 0o644))
	require.NoError(t, os.WriteFile(p, []byte(`{"version":1,"seq":2}`), 0o644))

	r := f.reader(t)
	_, ref, err := r.Latest(f.ctx(), f.sess)
	require.NoError(t, err, "the rollback is a fallback, not a hard failure")
	require.Equal(t, core.CheckpointSeq(1), ref.Seq, "the session rolls back to the prior checkpoint")

	// And the corruption is REPORTED, not papered over: fsck names the bad sequence number.
	bad, err := r.Verify(f.ctx())
	require.NoError(t, err)
	require.Equal(t, []core.CheckpointSeq{ref2.Seq}, bad)
}

// TestLatestOverAWhollyUnverifiableStoreIsNotFound pins the other end of the fallback: when no
// artifact verifies there is nothing to fall back TO, and the reader says so with
// core.ErrNotFound rather than inventing an empty checkpoint a rehydration would replay as truth.
func TestLatestOverAWhollyUnverifiableStoreIsNotFound(t *testing.T) {
	f := newFx(t)
	d := seedDraft(t, f)
	ref, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err)

	require.NoError(t, os.Chmod(paths.Long(ref.Path), 0o644))
	require.NoError(t, os.Remove(paths.Long(ref.Path)))

	_, _, err = f.reader(t).Latest(f.ctx(), f.sess)
	require.ErrorIs(t, err, core.ErrNotFound)
}

// TestResolveLatestReportsTheFallbackItTook is the explicit-reason requirement. Latest's own
// signature cannot carry one — it is the shipped Reader contract that SP-11 and SP-13 consume —
// so ResolveLatest is the form that answers WITH the reason: which sequence numbers it stepped
// over and why it did not return the newest.
func TestResolveLatestReportsTheFallbackItTook(t *testing.T) {
	f := newFx(t)
	first := seedDraft(t, f)
	_, err := f.w.Finalize(f.ctx(), first, finalizeBudget)
	require.NoError(t, err)
	second := f.w.DraftFor(f.sess)
	f.closedSeg(2, 4, 6)
	f.advance(second, 2)
	ref2, err := f.w.Finalize(f.ctx(), second, finalizeBudget)
	require.NoError(t, err)

	r := f.reader(t)

	// Clean store: no fallback, and the resolution says so rather than leaving it ambiguous.
	res, err := checkpoint.ResolveLatest(f.ctx(), r, f.sess)
	require.NoError(t, err)
	require.False(t, res.FellBack)
	require.Empty(t, res.Reason)
	require.Equal(t, ref2.Seq, res.Ref.Seq)

	require.NoError(t, os.Chmod(paths.Long(ref2.Path), 0o644))
	require.NoError(t, os.WriteFile(paths.Long(ref2.Path), []byte("{"), 0o644))

	res, err = checkpoint.ResolveLatest(f.ctx(), r, f.sess)
	require.NoError(t, err)
	require.True(t, res.FellBack, "a corrupt newest is a fallback, and it is reported as one")
	require.Equal(t, core.CheckpointSeq(1), res.Ref.Seq)
	require.Equal(t, []core.CheckpointSeq{ref2.Seq}, res.Skipped)
	require.Contains(t, res.Reason, "does not verify")
}

// TestResolveChainStopsAtAMissingAncestorWithAReason covers the missing-chain half. Reader.Chain
// refuses a broken ancestry outright, which is the right answer for fsck; a rehydrator needs the
// usable SUFFIX plus a statement of what is missing, and must never be handed a silently
// shortened chain it would mistake for the whole history.
func TestResolveChainStopsAtAMissingAncestorWithAReason(t *testing.T) {
	f := newFx(t)
	first := seedDraft(t, f)
	ref1, err := f.w.Finalize(f.ctx(), first, finalizeBudget)
	require.NoError(t, err)
	second := f.w.DraftFor(f.sess)
	f.closedSeg(2, 4, 6)
	f.advance(second, 2)
	ref2, err := f.w.Finalize(f.ctx(), second, finalizeBudget)
	require.NoError(t, err)

	r := f.reader(t)
	res, err := checkpoint.ResolveChain(f.ctx(), r, ref2.Seq)
	require.NoError(t, err)
	require.False(t, res.Truncated)
	require.Len(t, res.Checkpoints, 2)
	require.Equal(t, core.CheckpointSeq(1), res.Checkpoints[0].Seq, "oldest first")

	// Destroy the ANCESTOR. The chain is now broken behind the newest link.
	require.NoError(t, os.Chmod(paths.Long(ref1.Path), 0o644))
	require.NoError(t, os.WriteFile(paths.Long(ref1.Path), []byte("{"), 0o644))

	res, err = checkpoint.ResolveChain(f.ctx(), r, ref2.Seq)
	require.NoError(t, err, "a broken ancestry falls back explicitly rather than failing hard")
	require.True(t, res.Truncated)
	require.Len(t, res.Checkpoints, 1, "only the usable suffix")
	require.Equal(t, ref2.Seq, res.Checkpoints[0].Seq)
	require.NotEmpty(t, res.Reason)
	require.Equal(t, core.CheckpointSeq(1), res.MissingFrom)
}

// TestResolveLatestOnAnEmptyStoreIsNotFound keeps the resolver from fabricating a chain where
// there is none.
func TestResolveLatestOnAnEmptyStoreIsNotFound(t *testing.T) {
	f := newFx(t)
	_, err := checkpoint.ResolveLatest(f.ctx(), f.reader(t), f.sess)
	require.ErrorIs(t, err, core.ErrNotFound)
}

// TestResolveHonoursCancellation is the bounded-resource row: a cancelled context stops the
// resolution instead of walking the whole manifest.
func TestResolveHonoursCancellation(t *testing.T) {
	f := newFx(t)
	d := seedDraft(t, f)
	ref, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(f.ctx())
	cancel()
	_, err = checkpoint.ResolveLatest(ctx, f.reader(t), f.sess)
	require.ErrorIs(t, err, context.Canceled)
	_, err = checkpoint.ResolveChain(ctx, f.reader(t), ref.Seq)
	require.ErrorIs(t, err, context.Canceled)
}

// TestLegacyFormatCheckpointsStayReadable asserts the reader accepts an artifact written in the
// oldest on-disk shape it has ever produced: only the keys v1 required, none of the fields later
// tiers added. A reader that needed a newer key would make every checkpoint a project already
// holds unreadable at upgrade — the one failure mode this layer cannot recover from.
func TestLegacyFormatCheckpointsStayReadable(t *testing.T) {
	f := newFx(t)
	d := seedDraft(t, f)
	_, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err)

	l := paths.Of(f.p.Root)
	legacy := []byte(`{"version":1,"session":"` + string(f.sess) + `","seq":2,` +
		`"created":"2026-01-01T00:00:00.000Z","parent":"0001.json"}`)
	p := paths.Long(paths.CheckpointPath(l, 2))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, paths.CreateNew(paths.CheckpointPath(l, 2), legacy))
	require.NoError(t, paths.AppendManifest(l, paths.ManifestEntry{
		Seq: 2, SHA256: core.Hash(sha256.Sum256(legacy)).String(), Bytes: int64(len(legacy)),
		Created: core.NowMilli(f.p.Clock),
	}))

	cp, ref, err := f.reader(t).Get(f.ctx(), 2)
	require.NoError(t, err, "a v1 artifact with only its required keys still decodes")
	require.Equal(t, core.CheckpointSeq(2), ref.Seq)
	require.Equal(t, f.sess, cp.Session)
	require.Empty(t, cp.EncodedSegments, "an absent key reads as absent, not as an error")

	chain, err := f.reader(t).Chain(f.ctx(), 2)
	require.NoError(t, err)
	require.Len(t, chain, 2, "and it still links to its parent")
}
