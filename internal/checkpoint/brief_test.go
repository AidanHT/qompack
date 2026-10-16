package checkpoint_test

// BriefSnapshot's behaviour suite: the checkpoint half of the brief sequence (V7 plan §5.3 step 3).
// The daemon's brief route settles the session and closes its open segment first; these rows pin
// what the snapshot itself does with the draft it finds, or opens, and reuse writer_test.go's fx
// fixture, so every backend below is real.

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/store"
)

// explains records one assistant turn that explains a change to path, the decision source an
// encoded segment mints a dec_ id from (decisions.go, source a).
func (f *fx) explains(turn core.TurnIndex, path, text string) {
	f.t.Helper()
	root := f.put(text, "", "", false)
	a := dag.AssistantNode(turn)
	file := dag.FileNode(path)
	require.NoError(f.t, f.graph.AddNode(dag.Node{ID: a, Kind: dag.KindAssistant, Turn: turn, Root: root, Pos: f.nextPos()}))
	require.NoError(f.t, f.graph.AddNode(dag.Node{ID: file, Kind: dag.KindFile, Turn: turn, Ref: path, Pos: f.nextPos()}))
	require.NoError(f.t, f.graph.AddEdge(dag.Edge{From: a, To: file, Kind: dag.EdgeExplains, Weight: 1, Turn: turn}))
}

// briefOf runs one BriefSnapshot and fails the row on an error.
func (f *fx) briefOf() checkpoint.Brief {
	f.t.Helper()
	b, err := f.w.BriefSnapshot(f.ctx(), f.sess)
	require.NoError(f.t, err)
	return b
}

// item returns the brief's item with id, and whether there is one.
func item(b checkpoint.Brief, id string) (checkpoint.BriefItem, bool) {
	for _, it := range b.Items {
		if it.ID == id {
			return it, true
		}
	}
	return checkpoint.BriefItem{}, false
}

// ofKind returns the brief's items of kind k, in the brief's order.
func ofKind(b checkpoint.Brief, k checkpoint.BriefKind) []checkpoint.BriefItem {
	var out []checkpoint.BriefItem
	for _, it := range b.Items {
		if it.Kind == k {
			out = append(out, it)
		}
	}
	return out
}

// publishSources hands the writer the fixture's sources the way the daemon's composition root does
// (SetSources), so the cold path has seams before any Begin has run.
func (f *fx) publishSources() {
	f.t.Helper()
	require.NoError(f.t, f.w.SetSources(f.src))
}

// TestBriefSnapshot_AFirstCompactionWithNoDraftBriefsPinsIntentAndTheClosedSegment is third audit
// A3: drafts begin only at a frontier pass or at PreCompact, so a session's first compaction
// usually finds none. The snapshot opens one cold, as PreCompact's cold path does, and the brief
// still carries the pins, the original intent and what the closed segment encodes.
func TestBriefSnapshot_AFirstCompactionWithNoDraftBriefsPinsIntentAndTheClosedSegment(t *testing.T) {
	f := newFx(t)
	f.pins.invs = []pins.Invariant{{ID: "inv_0123456789ab", Text: "Never log access tokens.", Source: "user"}}
	f.prompt(0, "Fix the intermittent 500s on POST /api/session/refresh.", false)
	f.explains(2, "src/auth.ts", "Switched to a transaction-scoped pool. pgbouncer ignores pool_timeout.")
	f.closedSeg(1, 0, 3)
	f.publishSources()
	require.Nil(t, f.w.DraftFor(f.sess), "fixture sanity: no draft exists before the brief")

	b := f.briefOf()

	require.True(t, b.NewDraft, "the snapshot opened the draft cold")
	inv, ok := item(b, "inv_0123456789ab")
	require.True(t, ok, "the pin is briefed")
	require.Equal(t, checkpoint.BriefInvariant, inv.Kind)
	require.Equal(t, "Never log access tokens.", inv.Text, "verbatim")
	orig, ok := item(b, checkpoint.BriefIDIntentOriginal)
	require.True(t, ok, "the original intent is briefed")
	require.Equal(t, "Fix the intermittent 500s on POST /api/session/refresh.", orig.Text)
	decs := ofKind(b, checkpoint.BriefDecision)
	require.Len(t, decs, 1, "the closed segment's explains decision is briefed")
	require.True(t, strings.HasPrefix(decs[0].ID, "dec_"), "a decision keeps its stored id, got %q", decs[0].ID)
	require.Contains(t, decs[0].Text, "src/auth.ts")

	require.NotNil(t, f.w.DraftFor(f.sess), "the cold draft stays live, for PreCompact to find and seal")
	w, _ := f.persisted()
	require.Equal(t, []core.SegmentID{1}, w.Encoded, "the closed segment is encoded into the live draft")
	entries, err := paths.ReadManifest(paths.Of(f.p.Root))
	require.NoError(t, err)
	require.Empty(t, entries, "a brief seals nothing")
}

// TestBriefSnapshot_APinMadeAfterBeginAndAnOpenSegmentPromptAppear is third audit A2: catch-up
// alone runs no Advance when nothing closed, so without the seal's refreshes a brief would miss a
// pin made mid-session and a prompt in the still-open segment that used no tool.
func TestBriefSnapshot_APinMadeAfterBeginAndAnOpenSegmentPromptAppear(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "Fix the intermittent 500s on POST /api/session/refresh.", false)
	f.begin()

	f.pins.invs = []pins.Invariant{{ID: "inv_ba5eba11c0de", Text: "Keep the public API stable.", Source: "user"}}
	f.prompt(7, "Also cover the retry path. It fails after the second attempt.", false)

	b := f.briefOf()

	require.False(t, b.NewDraft, "the live draft was used")
	require.True(t, b.PinsReread, "the pins were read again for this brief")
	inv, ok := item(b, "inv_ba5eba11c0de")
	require.True(t, ok, "a pin made after Begin is briefed")
	require.Equal(t, "Keep the public API stable.", inv.Text)
	evo, ok := item(b, checkpoint.BriefIDIntentEvolutionPrefix+"1")
	require.True(t, ok, "the open segment's prompt is briefed as the intent's first evolution")
	require.Equal(t, checkpoint.BriefIntentEvolution, evo.Kind)
	require.Equal(t, "Also cover the retry path. It fails after the second attempt.", evo.Text, "verbatim")
	work, ok := item(b, checkpoint.BriefIDCurrentWork)
	require.True(t, ok, "current work is briefed")
	require.Contains(t, work.Text, "Also cover the retry path.", "current work follows the newest prompt")
}

// TestBriefSnapshot_ListsActiveEliminationsOnly: R22's elimination kind is the active records; a
// stale one (its evidence changed) is not something the summary should be told to keep.
func TestBriefSnapshot_ListsActiveEliminationsOnly(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "Fix the intermittent 500s.", false)
	active := f.elim(f.sess, negknow.ScopeSession, "src/auth.ts:refreshToken", "widen pool timeout", "did not help")
	stale := f.elim(f.sess, negknow.ScopeSession, "src/db.ts:connect", "raise max connections", "pool exhausted")
	f.begin()
	require.NoError(t, f.ledger.MarkStale(f.ctx(), []string{stale}, []string{"src/db.ts"}))

	b := f.briefOf()

	got, ok := item(b, active)
	require.True(t, ok, "the active elimination is briefed")
	require.Equal(t, checkpoint.BriefElimination, got.Kind)
	require.Contains(t, got.Text, "widen pool timeout")
	require.Contains(t, got.Text, "src/auth.ts:refreshToken")
	require.Contains(t, got.Text, "did not help")
	_, ok = item(b, stale)
	require.False(t, ok, "a stale elimination is not briefed")
	for _, d := range ofKind(b, checkpoint.BriefDecision) {
		require.NotContains(t, d.Text, "widen pool timeout",
			"the decision an elimination mints restates it, and is not briefed twice")
	}
}

// TestBriefSnapshot_RetriesOnceOnTheSuccessorWhenTheDraftIsSealedBeforeItsAdvance is third audit A8:
// a cadence seal, or a PreCompact after the mod gave up, can seal the draft between the snapshot
// taking it and its catch-up Advance. The snapshot takes the successor the seal opened and retries
// once, as the frontier advancer does.
func TestBriefSnapshot_RetriesOnceOnTheSuccessorWhenTheDraftIsSealedBeforeItsAdvance(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "Fix the intermittent 500s.", false)
	f.explains(2, "src/auth.ts", "Switched to a transaction-scoped pool. pgbouncer ignores pool_timeout.")
	f.closedSeg(1, 0, 3)
	sealing := &sealingSegments{SegmentLog: f.src.Segments}
	f.src.Segments = sealing
	first := f.begin()
	sealing.seal = func() {
		_, err := f.w.Finalize(context.WithoutCancel(f.ctx()), first, 0)
		require.NoError(t, err, "the seal that races the brief")
	}

	b, err := f.w.BriefSnapshot(f.ctx(), f.sess)
	require.NoError(t, err)

	require.True(t, b.Retried, "the snapshot retried once")
	succ := f.w.DraftFor(f.sess)
	require.NotNil(t, succ)
	require.NotSame(t, first, succ, "the retry used the successor the seal opened")
	require.Equal(t, 2, sealing.lists, "one catch-up per attempt")
	require.Len(t, ofKind(b, checkpoint.BriefDecision), 1, "the retry encoded the closed segment into the successor")
	wire, _ := f.persisted()
	require.Equal(t, succ.Seq(), wire.Seq, "the successor's file is the live one")
	require.Equal(t, []core.SegmentID{1}, wire.Encoded)
}

// sealingSegments is the segment log with one hook: the first listing of unencoded segments runs
// seal first, so a seal lands between the snapshot taking its draft and its catch-up Advance.
type sealingSegments struct {
	store.SegmentLog
	seal  func()
	once  sync.Once
	lists int
}

func (s *sealingSegments) Unencoded(ctx context.Context, sess core.SessionID) ([]store.Segment, error) {
	s.lists++
	if s.seal != nil {
		s.once.Do(s.seal)
	}
	return s.SegmentLog.Unencoded(ctx, sess)
}

// TestBriefSnapshot_ConcurrentWithAnIdleAdvanceEncodesEachSegmentOnce: the snapshot's catch-up takes
// no lock of its own (Q22): Advance is idempotent per sequence number, so a brief racing the idle
// frontier's Advance over the same closed segments encodes each of them exactly once.
//
// The overlap is forced: the brief lists the two segments as unencoded, the advancer is held until
// that listing is made, and the brief hands its listing to its own Advance only once the advancer is
// inside Advance. So both offer the same two segments to the one draft.
func TestBriefSnapshot_ConcurrentWithAnIdleAdvanceEncodesEachSegmentOnce(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "Fix the intermittent 500s.", false)
	f.tool("toolu_br_0001", 1, "Read", "src/auth.ts", "export function refreshToken() {}", false)
	f.tool("toolu_br_0002", 5, "Read", "src/db.ts", "export function connect() {}", false)
	f.closedSeg(1, 0, 3)
	f.closedSeg(2, 4, 6)
	race := &racingSegments{SegmentLog: f.src.Segments, listed: make(chan struct{}), inAdvance: make(chan struct{})}
	f.src.Segments = race
	f.publishSources()
	adv := checkpoint.NewFrontierAdvancer(f.w, func() (checkpoint.SourceSet, error) {
		if !waitFor(race.listed) {
			return checkpoint.SourceSet{}, errors.New("the brief never listed its catch-up")
		}
		return f.src, nil
	})

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := f.w.BriefSnapshot(f.ctx(), f.sess)
		errs <- err
	}()
	go func() {
		defer wg.Done()
		_, err := adv.Advance(f.ctx(), f.sess, []core.SegmentID{1, 2})
		errs <- err
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, []core.SegmentID{1, 2}, race.offered, "the brief offered both segments the advancer was encoding")
	require.True(t, race.overlapped, "the brief's listing was handed over while the advancer was inside Advance")

	d := f.w.DraftFor(f.sess)
	require.NotNil(t, d)
	require.Equal(t, 2, d.EncodedCount(), "both segments are in the one live draft")
	_, cp := f.persisted()
	require.Equal(t, []core.SegmentID{1, 2}, cp.EncodedSegments)
	for _, id := range []string{"seg 1 ", "seg 2 "} {
		require.Equal(t, 1, strings.Count(cp.Narrative, id), "segment %q is encoded once, not twice", id)
	}
	for _, id := range []core.SegmentID{1, 2} {
		seg, err := f.store.Segments().Get(f.ctx(), id)
		require.NoError(t, err)
		require.Equal(t, d.Seq(), seg.CheckpointSeq, "segment %d is held by the live draft's sequence", id)
	}
}

// racingSegments forces a brief's catch-up and an idle Advance to overlap: the brief's listing
// (Unencoded) closes listed and then waits for inAdvance, which the first Get closes. Only the
// advancer calls Get while the brief waits, because the brief's own Advance starts after its listing
// returns.
type racingSegments struct {
	store.SegmentLog
	listed, inAdvance chan struct{}
	listOnce, getOnce sync.Once
	offered           []core.SegmentID
	overlapped        bool
}

func (s *racingSegments) Unencoded(ctx context.Context, sess core.SessionID) ([]store.Segment, error) {
	segs, err := s.SegmentLog.Unencoded(ctx, sess)
	first := false
	s.listOnce.Do(func() {
		first = true
		for _, seg := range segs {
			s.offered = append(s.offered, seg.ID)
		}
		close(s.listed)
	})
	if first {
		s.overlapped = waitFor(s.inAdvance)
	}
	return segs, err
}

func (s *racingSegments) Get(ctx context.Context, id core.SegmentID) (store.Segment, error) {
	s.getOnce.Do(func() { close(s.inAdvance) })
	return s.SegmentLog.Get(ctx, id)
}

// waitFor reports whether ch closes within a bound no co-loaded host exhausts; false fails the row
// that asked rather than hanging it.
func waitFor(ch <-chan struct{}) bool {
	t := time.NewTimer(30 * time.Second)
	defer t.Stop()
	select {
	case <-ch:
		return true
	case <-t.C:
		return false
	}
}

// TestBriefSnapshot_BriefsTheSealedDraftWhileItsSealIsStillWriting is review finding I2: a seal
// closes the draft first and opens its successor only after its writes, so a brief whose catch-up
// meets ErrDraftSealed inside that window finds the same sealed draft on its retry. It briefs from
// that draft's items, whose refreshes the seal already ran, instead of failing.
func TestBriefSnapshot_BriefsTheSealedDraftWhileItsSealIsStillWriting(t *testing.T) {
	f := newFx(t)
	f.pins.invs = []pins.Invariant{{ID: "inv_0123456789ab", Text: "Never log access tokens.", Source: "user"}}
	f.prompt(0, "Fix the intermittent 500s.", false)
	f.publishSources()
	first := f.begin()
	f.closedSeg(1, 0, 3) // closed after Begin, so the brief's catch-up offers it to the sealed draft

	writing, release := make(chan struct{}), make(chan struct{})
	var entered, released sync.Once
	letGo := func() { released.Do(func() { close(release) }) }
	t.Cleanup(letGo)
	checkpoint.SetBarriersForTest(f.w, paths.Barriers{SyncFile: func(file *os.File) error {
		entered.Do(func() { close(writing) })
		<-release
		return file.Sync()
	}})
	sealed := make(chan error, 1)
	go func() {
		_, err := f.w.Finalize(context.WithoutCancel(f.ctx()), first, 0)
		sealed <- err
	}()
	require.True(t, waitFor(writing), "fixture sanity: the seal is held inside its artifact write")
	require.Same(t, first, f.w.DraftFor(f.sess), "fixture sanity: the sealed draft is still the live one")

	b, err := f.w.BriefSnapshot(f.ctx(), f.sess)
	letGo()
	require.NoError(t, <-sealed)

	require.NoError(t, err, "the brief is served from the sealed draft, not failed")
	require.True(t, b.Retried)
	require.True(t, b.Sealed, "it says it briefed a draft whose seal was in flight")
	_, ok := item(b, "inv_0123456789ab")
	require.True(t, ok, "the pins are briefed")
	orig, ok := item(b, checkpoint.BriefIDIntentOriginal)
	require.True(t, ok, "the sealed draft's intent is briefed")
	require.Equal(t, "Fix the intermittent 500s.", orig.Text)
}

// TestBriefSnapshot_ADeadlineThatPassesAfterTheCatchUpSkipsTheRefreshes: once the context ends the
// snapshot starts no further step. The catch-up has run; the refreshes and the pin re-read do not,
// and the items are the draft's own.
func TestBriefSnapshot_ADeadlineThatPassesAfterTheCatchUpSkipsTheRefreshes(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "Fix the intermittent 500s.", false)
	ctx, cancel := context.WithCancel(f.ctx())
	defer cancel()
	cut := &cutAfterCatchUp{SegmentLog: f.src.Segments}
	f.src.Segments = cut
	f.begin()
	f.closedSeg(1, 0, 3)
	f.pins.invs = []pins.Invariant{{ID: "inv_ba5eba11c0de", Text: "Keep the public API stable.", Source: "user"}}
	cut.cancel = cancel // armed now: the frontier read Begin made is behind us

	b, err := f.w.BriefSnapshot(ctx, f.sess)

	require.NoError(t, err, "the draft was in hand; its items are answered")
	require.False(t, b.PinsReread, "the pin re-read did not start")
	_, ok := item(b, "inv_ba5eba11c0de")
	require.False(t, ok, "a pin made after Begin is not seen without the re-read")
	w, _ := f.persisted()
	require.Equal(t, []core.SegmentID{1}, w.Encoded, "the catch-up ran before the deadline")
}

// cutAfterCatchUp ends the brief's context at the frontier read that closes the catch-up's Advance,
// once cancel is set.
type cutAfterCatchUp struct {
	store.SegmentLog
	cancel context.CancelFunc
}

func (s *cutAfterCatchUp) Frontier(ctx context.Context, sess core.SessionID) (core.TurnIndex, error) {
	fr, err := s.SegmentLog.Frontier(ctx, sess)
	if s.cancel != nil {
		s.cancel()
	}
	return fr, err
}

// TestBriefSnapshot_StartsNoWorkOnceItsDeadlineHasPassed: an abandoned brief must not compete with
// the seal for the draft: with its context already over it opens nothing and encodes nothing.
func TestBriefSnapshot_StartsNoWorkOnceItsDeadlineHasPassed(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "Fix the intermittent 500s.", false)
	f.closedSeg(1, 0, 3)
	f.publishSources()
	ctx, cancel := context.WithCancel(f.ctx())
	cancel()

	_, err := f.w.BriefSnapshot(ctx, f.sess)

	require.True(t, errors.Is(err, context.Canceled), "the snapshot reports its context, got %v", err)
	require.Nil(t, f.w.DraftFor(f.sess), "no draft was opened")
}

// TestBriefSnapshot_ReportsABeginFailure: with no sources to begin from (the daemon's route answers
// served-empty for this), the snapshot fails rather than inventing a draft.
func TestBriefSnapshot_ReportsABeginFailure(t *testing.T) {
	f := newFx(t)
	_, err := f.w.BriefSnapshot(f.ctx(), f.sess)
	require.Error(t, err, "no SourceSet was ever published, so Begin cannot open a draft")
	require.Nil(t, f.w.DraftFor(f.sess))
}
