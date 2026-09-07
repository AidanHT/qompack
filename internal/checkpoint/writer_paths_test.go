package checkpoint_test

// The writer's parent-chain and failure paths. Advance's error branches all share one invariant
// worth stating once: a failed Advance still PERSISTS what it managed to accumulate before
// returning. Losing a partial draft on a transient store error would silently reset the frontier to
// zero, and the next PreCompact would then re-encode a span that was already encoded -- which is
// the DPI violation this layer exists to make impossible.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// errSegments wraps a real SegmentLog and fails one chosen call, so the error-propagation branches
// can be reached without a stub that would also have to reimplement everything else.
type errSegments struct {
	store.SegmentLog
	// failGetID is the one segment whose read fails. Failing every read would make the draft
	// unable to accumulate anything first, and "a failed Advance keeps what earlier calls
	// encoded" is precisely the claim this fixture exists to test.
	failGetID core.SegmentID
}

var errInjected = errors.New("injected segment-log failure")

func (e errSegments) Get(ctx context.Context, id core.SegmentID) (store.Segment, error) {
	if id == e.failGetID {
		return store.Segment{}, errInjected
	}
	return e.SegmentLog.Get(ctx, id)
}

func TestBeginWithAParentRecordsItsFilename(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "The original words the user typed.", true)
	f.closedSeg(1, 0, 3)
	first := f.begin()
	f.advance(first, 1)
	ref, err := f.w.Finalize(f.ctx(), first, finalizeBudget)
	require.NoError(t, err)

	// parent != 0 is the checkpoint chain: Begin READS the parent -- which is why it has to exist
	// -- and copies the VERBATIM original intent forward out of it, so the user's own words
	// survive arbitrarily many checkpoint generations instead of being re-summarized at each one.
	require.NoError(t, f.w.Abort(f.w.DraftFor(f.sess)))
	d, err := f.w.Begin(f.ctx(), f.sess, ref.Seq, f.src)
	require.NoError(t, err)
	require.NotNil(t, d)

	wire, cp := f.persisted()
	require.Equal(t, ref.Seq, wire.Parent)
	require.Equal(t, "0001.json", cp.Parent,
		"the artifact records the parent by FILENAME, which is what a reader can resolve")
	require.Equal(t, "The original words the user typed.", cp.UserIntent.Original,
		"the verbatim original is carried forward from the parent, not re-derived")
}

func TestBeginOnALiveDraftAdoptsANewParent(t *testing.T) {
	f := newFx(t)
	first, err := f.w.Begin(f.ctx(), f.sess, 0, f.src)
	require.NoError(t, err)
	_, cp := f.persisted()
	require.Empty(t, cp.Parent, "fixture sanity: the first draft has no parent")

	// A second Begin for a live session returns the SAME draft rather than displacing it -- two
	// live drafts for one session would each encode the same segments and the DPI guard would fire
	// on whichever lost the race. But a parent supplied later must still be adopted, because that
	// is how Finalize's successor learns what it descends from.
	again, err := f.w.Begin(f.ctx(), f.sess, core.CheckpointSeq(3), f.src)
	require.NoError(t, err)
	require.Same(t, first, again, "the live draft is reused, not replaced")

	wire, cp2 := f.persisted()
	require.Equal(t, core.CheckpointSeq(3), wire.Parent)
	require.Equal(t, "0003.json", cp2.Parent)
}

func TestAdvanceReportsAnUnknownSegment(t *testing.T) {
	f := newFx(t)
	d := f.begin()

	_, err := f.w.Advance(f.ctx(), d, []core.SegmentID{404})
	require.Error(t, err)
	require.Contains(t, err.Error(), "404", "the error names the segment that could not be read")
}

func TestAdvanceOnAFailedSegmentReadStillPersistsTheDraft(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "Trace the timeout.", true)
	f.closedSeg(1, 0, 3)
	f.closedSeg(2, 4, 7)

	// Advance reads the draft's OWN SourceSet, the one handed to Begin -- so the failure has to be
	// wired in there rather than passed to Advance.
	broken := f.src
	broken.Segments = errSegments{SegmentLog: f.src.Segments, failGetID: 2}
	d, err := f.w.Begin(f.ctx(), f.sess, 0, broken)
	require.NoError(t, err)

	_, err = f.w.Advance(f.ctx(), d, []core.SegmentID{1})
	require.NoError(t, err)
	before, _ := f.persisted()
	require.Equal(t, core.TurnIndex(3), before.Frontier, "fixture sanity: segment 1 landed")

	_, err = f.w.Advance(f.ctx(), d, []core.SegmentID{2})
	require.ErrorIs(t, err, errInjected, "a segment that cannot be read is reported, not skipped")

	after, _ := f.persisted()
	require.Equal(t, before.Frontier, after.Frontier,
		"a failed Advance leaves the frontier where it was; it never rewinds it")
	require.Subset(t, after.Encoded, []core.SegmentID{1},
		"and it never drops what earlier calls already encoded")
}

func TestAdvanceIsANoOpForAnEmptyBatch(t *testing.T) {
	f := newFx(t)
	f.closedSeg(1, 0, 3)
	d := f.begin()

	// The idle task calls Advance on every live session each tick, and most ticks have nothing to
	// do. That path must not persist, not error, and not touch the frontier.
	//
	// "Must not persist" is asserted by deleting the file and checking that Advance does not put it
	// back. A full Marshal plus WriteAtomic — write, Sync, rename, fsync the directory — on every
	// tick of every live session, plus the finished sessions the daemon folds in from the store's
	// recent-session index, is the cost of writing a draft that has not changed.
	require.NoError(t, os.Remove(paths.Long(f.draftPath())))

	front, err := f.w.Advance(f.ctx(), d, nil)
	require.NoError(t, err)
	require.Equal(t, d.Frontier(), front)
	require.NoFileExists(t, paths.Long(f.draftPath()),
		"an Advance that changed nothing must not rewrite the draft file")
}

// TestBeginDoesNotRewriteAnUnchangedLiveDraft is the same economy on the other idle-tick call.
// advanceAllSessions calls Begin for every live session before it calls Advance, and a Begin that
// finds a live draft and adopts no new parent has nothing to write.
func TestBeginDoesNotRewriteAnUnchangedLiveDraft(t *testing.T) {
	f := newFx(t)
	first := f.begin()
	require.NoError(t, os.Remove(paths.Long(f.draftPath())))

	again, err := f.w.Begin(f.ctx(), f.sess, 0, f.src)
	require.NoError(t, err)
	require.Same(t, first, again)
	require.NoFileExists(t, paths.Long(f.draftPath()),
		"re-Begin-ning an unchanged live draft must not rewrite its file")

	// But a genuinely new parent IS a change, and must reach the disk.
	_, err = f.w.Begin(f.ctx(), f.sess, core.CheckpointSeq(3), f.src)
	require.NoError(t, err)
	wire, _ := f.persisted()
	require.Equal(t, core.CheckpointSeq(3), wire.Parent)
}

// flakyMarkSegments fails the FIRST MarkEncoded call and then behaves normally, which is the shape
// of the real hazard: encodeSegmentLocked has already run for the whole batch by the time
// MarkEncoded is reached, so a failure there persists an accumulated draft with nothing recorded
// in d.encoded — and the next tick encodes exactly the same batch again.
type flakyMarkSegments struct {
	store.SegmentLog
	failed *bool
}

func (s flakyMarkSegments) MarkEncoded(ctx context.Context, ids []core.SegmentID, seq core.CheckpointSeq) error {
	if !*s.failed {
		*s.failed = true
		return context.DeadlineExceeded
	}
	return s.SegmentLog.MarkEncoded(ctx, ids, seq)
}

// TestAdvanceDoesNotDuplicateTheNarrativeWhenAMarkFails covers the one accumulator that had no
// dedup.
//
// Advance's doc comment promises the retry after a failed MarkEncoded is safe because "the draft is
// a superset" — true of every field except the narrative, which was appended with += and no key.
// Under the 2 s idle budget a MarkEncoded that returns the context's error is reachable, and every
// such tick used to add one more copy of the same block, for as long as it kept timing out.
func TestAdvanceDoesNotDuplicateTheNarrativeWhenAMarkFails(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "Trace the retry loop.", true)
	f.tool("toolu_flaky", 1, "Read", "src/a.ts", "body", false)
	f.closedSeg(1, 0, 3)

	failed := false
	broken := f.src
	broken.Segments = flakyMarkSegments{SegmentLog: f.src.Segments, failed: &failed}
	d, err := f.w.Begin(f.ctx(), f.sess, 0, broken)
	require.NoError(t, err)

	_, err = f.w.Advance(f.ctx(), d, []core.SegmentID{1})
	require.ErrorIs(t, err, context.DeadlineExceeded, "fixture sanity: the mark failed")
	_, cp := f.persisted()
	require.Empty(t, cp.EncodedSegments, "nothing was marked, so the batch is still owed")
	require.Equal(t, 1, strings.Count(cp.Narrative, "seg 1 turns "))

	// The retry the doc comment promises is safe.
	_, err = f.w.Advance(f.ctx(), d, []core.SegmentID{1})
	require.NoError(t, err)

	_, cp = f.persisted()
	require.Equal(t, []core.SegmentID{1}, cp.EncodedSegments)
	require.Equal(t, 1, strings.Count(cp.Narrative, "seg 1 turns "),
		"re-encoding a batch must converge, not append a second copy of its narrative")
}

func TestAdvanceReencodingTheSameSegmentIsIdempotent(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "Investigate.", true)
	f.closedSeg(1, 0, 5)
	d := f.begin()

	first := f.advance(d, 1)
	encodedAfterFirst, _ := f.persisted()

	// The DPI guard is "never compress a compression": MarkEncoded is idempotent for the SAME
	// checkpoint seq, so replaying a batch after a daemon restart must converge rather than either
	// erroring or double-counting.
	second, err := f.w.Advance(f.ctx(), d, []core.SegmentID{1})
	require.NoError(t, err)
	require.Equal(t, first, second, "the frontier does not move on a replay")

	encodedAfterSecond, _ := f.persisted()
	require.Equal(t, encodedAfterFirst.Encoded, encodedAfterSecond.Encoded,
		"and the segment is not listed twice")
}

func TestDraftOpenQuestionsDedupeAndSurviveAPersistRoundTrip(t *testing.T) {
	f := newFx(t)
	d := f.begin()

	d.AddOpenQuestion("Is staging on the same pgbouncer build?")
	d.AddOpenQuestion("Is staging on the same pgbouncer build?")
	d.AddOpenQuestion("Does the IdP guarantee idempotency on a retried rotation?")

	_, cp := f.persisted()
	require.Len(t, cp.OpenQuestions, 2, "the exact-duplicate question is added once")

	// Explicit questions must come back after a restart, and must sort ahead of anything Advance
	// derives: they are what a human actually asked, and derived ones are guesses about staleness.
	//
	// The round trip is a real one — a second writer over the same root, resuming the draft file
	// this one persisted. Aborting first would delete that file, so what came back would be a fresh
	// draft, and asserting only that it is non-nil would say nothing about the questions at all.
	restarted, err := checkpoint.OpenWriter(f.p.Root, f.p.Cfg, f.p.Log, obs.New(f.p.Clock), f.p.Clock)
	require.NoError(t, err)
	resumed, err := restarted.Begin(f.ctx(), f.sess, 0, f.src)
	require.NoError(t, err)
	require.NotNil(t, resumed)
	require.Equal(t, d.Seq(), resumed.Seq(), "the persisted draft was resumed, not replaced")

	_, back := f.persisted()
	require.Equal(t, []string{
		"Is staging on the same pgbouncer build?",
		"Does the IdP guarantee idempotency on a retried rotation?",
	}, back.OpenQuestions, "the explicit questions survive the restart, in the order they were asked")
}

func TestSetCurrentWorkStopsAdvanceFromDerivingIt(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "Fix the refresh endpoint.", true)
	f.closedSeg(1, 0, 3)
	d := f.begin()

	d.SetCurrentWork(checkpoint.CurrentWork{
		Goal: "A goal the caller set explicitly.", NextStep: "And a next step.",
	})
	f.advance(d, 1)

	_, cp := f.persisted()
	// Once a caller has stated the goal, Advance must stop inventing one. A daemon that silently
	// overwrote a stated goal with a derived guess would be worse than one that derived nothing.
	require.Equal(t, "A goal the caller set explicitly.", cp.CurrentWork.Goal)
	require.Equal(t, "And a next step.", cp.CurrentWork.NextStep)
}
