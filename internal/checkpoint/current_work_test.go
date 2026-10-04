package checkpoint_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// The derived current work (§8: one sentence of the session's most recent prompt) across the edges
// the V6 close-out's wave-20 audit found (D59): a one-prompt session, a prompt list that fails for a
// while (across a daemon restart too), Qompack's own slash commands, a blank, unreadable or
// oversized newest prompt, prompts published out of host order, and a draft persisted by an earlier
// build.

const (
	rateAskGoal          = "We are building a rate limiter for the Kite API gateway."
	rateCorrection60Goal = "Correction: the limit must be 60 requests per minute per client, not 100."
	rateCorrection75Goal = "Correction: the limit must now be 75 requests per minute per client."
)

// promptProbeStore is the shipped store with a SessionPrompts that can be switched to fail the way
// FSStore's does (core.ErrDegraded past its scan limit, or the context's error inside the idle
// Advance budget), and an Open that records how many bytes each read of an object took.
type promptProbeStore struct {
	store.Store
	mu       sync.Mutex
	degraded bool
	reads    map[core.Hash][]int64
	// failing is how many more Opens of each object fail, the way a transient read error does.
	failing map[core.Hash]int
	// cancel, when set, is called once right after the next SessionPrompts answers: the context
	// running out between the list and the reads that follow it.
	cancel context.CancelFunc
}

func newPromptProbeStore(s store.Store) *promptProbeStore {
	return &promptProbeStore{Store: s, reads: map[core.Hash][]int64{}, failing: map[core.Hash]int{}}
}

// failOpens makes the next n Opens of root fail.
func (s *promptProbeStore) failOpens(root core.Hash, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failing[root] = n
}

func (s *promptProbeStore) setDegraded(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.degraded = v
}

func (s *promptProbeStore) SessionPrompts(ctx context.Context, sess core.SessionID) ([]store.ToolUseRecord, error) {
	s.mu.Lock()
	degraded, cancel := s.degraded, s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if degraded {
		return nil, core.ErrDegraded
	}
	sp, ok := s.Store.(store.SessionPrompts)
	if !ok {
		return nil, core.ErrDegraded
	}
	recs, err := sp.SessionPrompts(ctx, sess)
	if cancel != nil {
		cancel()
	}
	return recs, err
}

func (s *promptProbeStore) cancelAfterNextList(cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel = cancel
}

func (s *promptProbeStore) Open(ctx context.Context, root core.Hash) (io.ReadCloser, error) {
	s.mu.Lock()
	fail := s.failing[root] > 0
	if fail {
		s.failing[root]--
	}
	s.mu.Unlock()
	if fail {
		return nil, fmt.Errorf("probe: transient read failure of %s", root)
	}
	rc, err := s.Store.Open(ctx, root)
	if err != nil {
		return nil, err
	}
	return &countingReadCloser{rc: rc, done: func(n int64) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.reads[root] = append(s.reads[root], n)
	}}, nil
}

// readsOf splits root's reads into whole ones (more than limit+1 bytes: past what a bounded read
// takes) and bounded ones.
func (s *promptProbeStore) readsOf(root core.Hash, limit int64) (whole, bounded int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range s.reads[root] {
		if n > limit+1 {
			whole++
			continue
		}
		bounded++
	}
	return whole, bounded
}

type countingReadCloser struct {
	rc   io.ReadCloser
	n    int64
	done func(int64)
}

func (c *countingReadCloser) Read(p []byte) (int, error) {
	n, err := c.rc.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *countingReadCloser) Close() error {
	c.done(c.n)
	return c.rc.Close()
}

// promptStamped records one prompt capture like promptAs, with the record's host stamp given:
// the observer keeps turns in publication order and the host's stamp on the record
// (observer.recordPromptDurable, SP08-D3, D35).
func promptStamped(f *fx, s core.SessionID, turn core.TurnIndex, ts core.UnixMilli, text string) {
	f.t.Helper()
	root := f.put(text, "UserPromptSubmit", "", true)
	id := core.ToolUseID(fmt.Sprintf("prompt_%s_%d", s, int(turn)))
	require.NoError(f.t, f.store.RecordToolUse(f.ctx(), store.ToolUseRecord{
		ID: id, Session: s, Turn: turn, TS: ts, Tool: "UserPromptSubmit", Root: root,
	}))
	require.NoError(f.t, dag.BuildUserPrompt(f.graph, dag.ObservedPrompt{
		Turn: turn, TS: f.now(), Pos: f.nextPos(), Tokens: core.Tokens(len(text) / 4), Ref: string(id),
	}))
}

// lostPromptAs records a prompt of session s at turn, with its graph node, whose bytes were never
// stored: the shape of a prompt whose object GC collected, or whose every read fails.
func lostPromptAs(f *fx, s core.SessionID, turn core.TurnIndex) {
	f.t.Helper()
	lost := core.HashBytes(core.DomainChunk, []byte(fmt.Sprintf("bytes never stored: %s %d", s, int(turn))))
	id := core.ToolUseID(fmt.Sprintf("prompt_%s_%d", s, int(turn)))
	require.NoError(f.t, f.store.RecordToolUse(f.ctx(), store.ToolUseRecord{
		ID: id, Session: s, Turn: turn, TS: f.now(), Tool: "UserPromptSubmit", Root: lost,
	}))
	require.NoError(f.t, dag.BuildUserPrompt(f.graph, dag.ObservedPrompt{
		Turn: turn, TS: f.now(), Pos: f.nextPos(), Tokens: 8, Ref: string(id),
	}))
}

// restartWriter replaces f's writer with a second one over the same root, the way a daemon restart
// does: nothing in memory, and the session's draft resumed from its file at the next Begin.
func restartWriter(t *testing.T, f *fx) {
	t.Helper()
	w, err := checkpoint.OpenWriter(f.p.Root, f.p.Cfg, f.p.Log, obs.New(f.p.Clock), f.p.Clock)
	require.NoError(t, err)
	f.w = w
}

// TestTransientPromptListFailureNeverMovesCurrentWorkBackwards: a goal taken from the session's
// newest prompt (in its open segment) is not replaced by an older prompt while the prompt list
// cannot be read. The graph fallback only sees the closed segment it encodes, whose newest prompt
// is older; the checkpoint a compaction seals inside that window keeps the newer goal, as its
// intent keeps the evolution it had.
func TestTransientPromptListFailureNeverMovesCurrentWorkBackwards(t *testing.T) {
	f := newFx(t)
	ps := newPromptProbeStore(f.store)
	f.src.Store = ps
	promptAs(f, f.sess, 0, rateAsk)
	promptAs(f, f.sess, 2, rateCorrection60)
	f.closedSeg(1, 0, 3)
	promptAs(f, f.sess, 4, rateCorrection45)

	d := f.begin()
	_, cp := f.persisted()
	require.Equal(t, rateCorrection45Goal, cp.CurrentWork.Goal, "fixture sanity: the newest prompt")

	ps.setDegraded(true)
	f.advance(d) // its refresh fails: the next pass's encoding reads the graph
	f.advance(d, 1)
	_, cp = f.persisted()
	require.Equal(t, rateCorrection45Goal, cp.CurrentWork.Goal,
		"the closed segment's newest prompt is older than the goal held")

	got := sealed(t, f, f.precompactAs(f.sess))
	require.Equal(t, rateCorrection45Goal, got.CurrentWork.Goal)
	require.Equal(t, []string{rateCorrection60, rateCorrection45}, got.UserIntent.Evolution)
}

// TestCurrentWorkMovesForwardAroundAPromptListFailure: the same window, continued. The goal holds
// through a recovery, the graph fallback still moves it FORWARD to a newer prompt of this session
// it encodes while the list cannot be read, and the records take over again once they answer.
func TestCurrentWorkMovesForwardAroundAPromptListFailure(t *testing.T) {
	f := newFx(t)
	ps := newPromptProbeStore(f.store)
	f.src.Store = ps
	promptAs(f, f.sess, 0, rateAsk)
	promptAs(f, f.sess, 2, rateCorrection60)
	f.closedSeg(1, 0, 3)
	promptAs(f, f.sess, 4, rateCorrection45)
	d := f.begin()

	ps.setDegraded(true)
	f.advance(d)
	f.advance(d, 1)
	ps.setDegraded(false)
	f.advance(d)
	_, cp := f.persisted()
	require.Equal(t, rateCorrection45Goal, cp.CurrentWork.Goal, "after the records answer again")

	ps.setDegraded(true)
	f.advance(d)
	promptAs(f, f.sess, 6, rateCorrection75)
	f.closedSeg(2, 4, 7)
	f.advance(d, 2)
	_, cp = f.persisted()
	require.Equal(t, rateCorrection75Goal, cp.CurrentWork.Goal, "a newer prompt the fallback encodes")

	ps.setDegraded(false)
	promptAs(f, f.sess, 8, rateReadLimiter)
	f.advance(d)
	_, cp = f.persisted()
	require.Equal(t, rateReadLimiter, cp.CurrentWork.Goal, "the records' newest once they answer")
}

// TestInterruptedGoalWalkIsWalkedAgain: a refresh whose context runs out after the prompt list
// answered cannot read the new newest prompt; it changes nothing, so the next refresh reads it and
// the goal moves to it.
func TestInterruptedGoalWalkIsWalkedAgain(t *testing.T) {
	f := newFx(t)
	ps := newPromptProbeStore(f.store)
	f.src.Store = ps
	promptAs(f, f.sess, 0, rateAsk)
	promptAs(f, f.sess, 2, rateCorrection60)
	d := f.begin()
	promptAs(f, f.sess, 4, rateCorrection45)

	ctx, cancel := context.WithCancel(f.ctx())
	defer cancel()
	ps.cancelAfterNextList(cancel)
	d.RefreshIntent(ctx)
	f.advance(d)

	_, cp := f.persisted()
	require.Equal(t, rateCorrection45Goal, cp.CurrentWork.Goal)
}

// TestInterruptedGoalWalkKeepsTheGoalItHas: a refresh whose context runs out after the prompt list
// answered cannot read the records it has not cached, and the goal's own record is one of them
// when it is a paste past the evolution's read limit. Such a walk would reach only the cached
// original; it changes nothing, so the goal does not step back to the original in the meantime.
func TestInterruptedGoalWalkKeepsTheGoalItHas(t *testing.T) {
	limit := checkpoint.EvolutionReadLimitForTest
	filler := strings.Repeat(" The limiter keeps one bucket per client.", int(limit)/40+1)
	f := newFx(t)
	ps := newPromptProbeStore(f.store)
	f.src.Store = ps
	promptAs(f, f.sess, 0, rateAsk)
	promptAs(f, f.sess, 2, rateCorrection45+filler)
	d := f.begin()
	promptAs(f, f.sess, 4, "/qompack:status")

	ctx, cancel := context.WithCancel(f.ctx())
	defer cancel()
	ps.cancelAfterNextList(cancel)
	_, err := f.w.Advance(ctx, d, nil)
	require.NoError(t, err)

	_, cp := f.persisted()
	require.Equal(t, rateCorrection45Goal, cp.CurrentWork.Goal)
}

// TestTransientReadFailureOfTheNewestPromptIsReadAgain: a refresh that cannot read the newest
// prompt's bytes (a transient Open failure, the context still live) takes the newest readable
// prompt's goal for now; the next refresh reads the newest prompt again, and the sealed goal agrees
// with the evolution's last entry.
func TestTransientReadFailureOfTheNewestPromptIsReadAgain(t *testing.T) {
	f := newFx(t)
	ps := newPromptProbeStore(f.store)
	f.src.Store = ps
	promptAs(f, f.sess, 0, rateAsk)
	promptAs(f, f.sess, 2, rateCorrection60)
	d := f.begin()
	promptAs(f, f.sess, 4, rateCorrection45)
	root := f.put(rateCorrection45, "UserPromptSubmit", "", true)

	// Both of one refresh's reads of it fail: the evolution's bounded read and the goal walk's.
	ps.failOpens(root, 2)
	f.advance(d)
	_, cp := f.persisted()
	require.Equal(t, rateCorrection60Goal, cp.CurrentWork.Goal, "fixture sanity: the failing refresh")
	f.advance(d)

	got := sealed(t, f, f.precompactAs(f.sess))
	require.Equal(t, rateCorrection45Goal, got.CurrentWork.Goal)
	require.Equal(t, []string{rateCorrection60, rateCorrection45}, got.UserIntent.Evolution)
}

// TestResumedDraftNeverMovesCurrentWorkBackwardsWhileThePromptListFails: the daemon restarts while
// the prompt list cannot be read. The resumed draft keeps the goal its newest prompt gave, and the
// graph fallback does not replace it with the older prompt of the closed segment it encodes: the
// turn the goal came from survives the restart.
func TestResumedDraftNeverMovesCurrentWorkBackwardsWhileThePromptListFails(t *testing.T) {
	f := newFx(t)
	ps := newPromptProbeStore(f.store)
	f.src.Store = ps
	promptAs(f, f.sess, 0, rateAsk)
	promptAs(f, f.sess, 2, rateCorrection60)
	f.closedSeg(1, 0, 3)
	promptAs(f, f.sess, 4, rateCorrection45)
	f.begin()
	_, cp := f.persisted()
	require.Equal(t, rateCorrection45Goal, cp.CurrentWork.Goal, "fixture sanity: the newest prompt")

	ps.setDegraded(true)
	restartWriter(t, f)
	d := f.begin()
	_, cp = f.persisted()
	require.Equal(t, rateCorrection45Goal, cp.CurrentWork.Goal, "fixture sanity: the resumed goal")
	f.advance(d)
	f.advance(d, 1)
	_, cp = f.persisted()
	require.Equal(t, rateCorrection45Goal, cp.CurrentWork.Goal,
		"the closed segment's newest prompt is older than the goal held")

	got := sealed(t, f, f.precompactAs(f.sess))
	require.Equal(t, rateCorrection45Goal, got.CurrentWork.Goal)
}

// TestResumedDraftKeepsTheTurnOfAnUnchangedGoal: a refresh can move the goal to a newer prompt
// without changing its text or anything else the draft holds: the user repeats the correction,
// with the original restated just before it, so the evolution lists the same entries. The turn the
// goal now comes from is still persisted, so after a restart into a failing prompt list the graph
// fallback does not take the restated original (newer than the first correction, older than the
// repeat) as current work.
func TestResumedDraftKeepsTheTurnOfAnUnchangedGoal(t *testing.T) {
	f := newFx(t)
	ps := newPromptProbeStore(f.store)
	f.src.Store = ps
	promptAs(f, f.sess, 0, rateAsk)
	promptAs(f, f.sess, 2, rateCorrection60)
	f.closedSeg(1, 0, 3)
	d := f.begin()
	promptAs(f, f.sess, 3, rateAsk)
	promptAs(f, f.sess, 4, rateCorrection60)
	f.advance(d)
	_, cp := f.persisted()
	require.Equal(t, rateCorrection60Goal, cp.CurrentWork.Goal, "fixture sanity: the repeated correction")
	require.Equal(t, []string{rateCorrection60}, cp.UserIntent.Evolution, "fixture sanity: unchanged")

	ps.setDegraded(true)
	restartWriter(t, f)
	d = f.begin()
	f.advance(d, 1)

	_, cp = f.persisted()
	require.Equal(t, rateCorrection60Goal, cp.CurrentWork.Goal)
}

// TestResumedCandidateSevenForkDraftTakesItsOwnGoalFromTheGraph: a fork's draft persisted by
// candidate 7, with its parent's prompt as its goal and no goal_turn, resumed while the prompt
// list cannot be read. Candidate 7 derived that goal from a segment the draft had encoded, so the
// fork's closed segment encoded now lies past it: the fallback replaces it with the fork's own
// prompt, which is never a move backwards.
func TestResumedCandidateSevenForkDraftTakesItsOwnGoalFromTheGraph(t *testing.T) {
	f := newFx(t)
	liveShapeParent(t, f)
	f.noteFork(f.sess)
	promptAs(f, f.sess, 1, rateCorrection45)
	closedSegAs(f, f.sess, 0, 2)
	plantDerivedGoal(t, f, rateReadLimiter)

	f.src.Store = degradedPromptsStore{f.store}
	cp := sealed(t, f, f.precompactAs(f.sess))

	require.Equal(t, rateCorrection45Goal, cp.CurrentWork.Goal)
}

// TestResumedCandidateSevenDraftKeepsTheUngatedFallback: the same decision where it differs from a
// gate at turn 0. A draft file without goal_turn but holding a derived goal (candidate 7) resumed
// while the prompt list cannot be read takes the goal of the session's prompt at turn 0 from the
// closed segment the fallback encodes, rather than keeping the goal it was persisted with.
func TestResumedCandidateSevenDraftKeepsTheUngatedFallback(t *testing.T) {
	f := newFx(t)
	promptAs(f, f.sess, 0, rateAsk)
	f.closedSeg(1, 0, 1)
	plantDerivedGoal(t, f, rateReadLimiter)

	f.src.Store = degradedPromptsStore{f.store}
	cp := sealed(t, f, f.precompactAs(f.sess))

	require.Equal(t, rateAskGoal, cp.CurrentWork.Goal)
}

// TestResumedDraftWithNoGoalTakesATurnZeroPromptFromTheGraph: a draft that holds no derived goal
// persists no goal_turn, so after a restart into a failing prompt list the graph fallback takes
// the goal of a prompt at turn 0, the session's first.
func TestResumedDraftWithNoGoalTakesATurnZeroPromptFromTheGraph(t *testing.T) {
	f := newFx(t)
	ps := newPromptProbeStore(f.store)
	f.src.Store = ps
	f.begin()
	wire, cp := f.persisted()
	require.Empty(t, cp.CurrentWork.Goal, "fixture sanity: no prompt yet")
	require.Nil(t, wire.GoalTurn, "no derived goal, so no turn it came from")

	promptAs(f, f.sess, 0, rateAsk)
	f.closedSeg(1, 0, 1)
	ps.setDegraded(true)
	restartWriter(t, f)
	d := f.begin()
	f.advance(d, 1)

	_, cp = f.persisted()
	require.Equal(t, rateAskGoal, cp.CurrentWork.Goal)
}

// TestResumedDraftKeepsItsGoalWhileItsPromptIsUnreadable: a resumed draft reads its prompts again,
// and the only one that gives its goal cannot be read for a while. A walk that could not read
// every record it passed does not clear the goal it holds; a compaction in that window seals it.
func TestResumedDraftKeepsItsGoalWhileItsPromptIsUnreadable(t *testing.T) {
	f := newFx(t)
	ps := newPromptProbeStore(f.store)
	f.src.Store = ps
	promptAs(f, f.sess, 0, "/qompack:status")
	promptAs(f, f.sess, 2, rateCorrection45)
	root := f.put(rateCorrection45, "UserPromptSubmit", "", true)
	f.begin()
	_, cp := f.persisted()
	require.Equal(t, rateCorrection45Goal, cp.CurrentWork.Goal, "fixture sanity: the goal held")

	restartWriter(t, f)
	ps.failOpens(root, 1<<20)
	f.begin()
	_, cp = f.persisted()
	require.Equal(t, rateCorrection45Goal, cp.CurrentWork.Goal, "the refresh that cannot read it")
	got := sealed(t, f, f.precompactAs(f.sess))
	require.Equal(t, rateCorrection45Goal, got.CurrentWork.Goal, "sealed in that window")
}

// TestResumedDraftNeverMovesCurrentWorkBackwardsWhileItsPromptIsUnreadable: the same restart, with
// an older prompt that gives a goal too. A walk that cannot read the record its goal came from
// finds the older prompt first; it does not step back to it, because the goal held came from a
// later turn. A compaction in that window seals the newer goal.
func TestResumedDraftNeverMovesCurrentWorkBackwardsWhileItsPromptIsUnreadable(t *testing.T) {
	f := newFx(t)
	ps := newPromptProbeStore(f.store)
	f.src.Store = ps
	promptAs(f, f.sess, 0, rateAsk)
	promptAs(f, f.sess, 2, rateCorrection60)
	promptAs(f, f.sess, 4, rateCorrection45)
	root := f.put(rateCorrection45, "UserPromptSubmit", "", true)
	f.begin()
	_, cp := f.persisted()
	require.Equal(t, rateCorrection45Goal, cp.CurrentWork.Goal, "fixture sanity: the goal held")

	restartWriter(t, f)
	ps.failOpens(root, 1<<20)
	f.begin()
	_, cp = f.persisted()
	require.Equal(t, rateCorrection45Goal, cp.CurrentWork.Goal, "the refresh that cannot read it")
	got := sealed(t, f, f.precompactAs(f.sess))
	require.Equal(t, rateCorrection45Goal, got.CurrentWork.Goal, "sealed in that window")
}

// TestResumedDraftWithAGoalTurnNoRecordReachesTakesTheRecordsGoal: a draft file whose goal_turn
// lies past every prompt record the store lists — the store restored from an older backup than
// the state directory — resumes into a walk that reads every record. The records have the last
// word: the goal is their newest, not the one the draft was persisted with.
func TestResumedDraftWithAGoalTurnNoRecordReachesTakesTheRecordsGoal(t *testing.T) {
	f := newFx(t)
	promptAs(f, f.sess, 0, rateAsk)
	promptAs(f, f.sess, 2, rateCorrection60)
	past := core.TurnIndex(9)
	plantDerivedGoalAt(t, f, rateReadLimiter, &past)

	cp := sealed(t, f, f.precompactAs(f.sess))

	require.Equal(t, rateCorrection60Goal, cp.CurrentWork.Goal)
}

// TestClearedGoalDropsItsTurnForTheGraphFallback: the same restored backup, where the records the
// store lists give no goal at all (a slash command only). The walk reads every one, so the goal
// is cleared, and the turn it came from goes with it: the draft persists no goal_turn beside an
// empty goal. Kept, that stale turn would gate the graph fallback, which then refused every
// prompt at or before it, so a prompt the user gives next, encoded while the prompt list cannot be
// read, would never become current work.
func TestClearedGoalDropsItsTurnForTheGraphFallback(t *testing.T) {
	f := newFx(t)
	ps := newPromptProbeStore(f.store)
	f.src.Store = ps
	promptAs(f, f.sess, 0, "/qompack:status")
	past := core.TurnIndex(9)
	plantDerivedGoalAt(t, f, rateReadLimiter, &past)

	d := f.begin()
	wire, cp := f.persisted()
	require.Empty(t, cp.CurrentWork.Goal, "no record the store lists gives a goal")
	require.Nil(t, wire.GoalTurn, "a cleared goal came from no turn")

	promptAs(f, f.sess, 2, rateCorrection60)
	f.closedSeg(1, 0, 3)
	ps.setDegraded(true)
	f.advance(d) // its refresh fails: the next pass's encoding reads the graph
	f.advance(d, 1)

	_, cp = f.persisted()
	require.Equal(t, rateCorrection60Goal, cp.CurrentWork.Goal)
}

// TestFallbackGoalTurnGatesALaterIncompleteWalk (audit 2's #23): the graph fallback records the
// turn of the goal it takes, because a later records walk is gated by the same turn. Here the
// fallback moves the goal forward to the closed segment's newest prompt (turn 6) while the prompt
// list cannot be read; once the list answers again, that prompt's bytes cannot be read, so the
// walk is incomplete and finds the turn-4 prompt behind it. Without the fallback's turn the gate
// would still hold the records walk's turn 2, and current work would step back to turn 4.
func TestFallbackGoalTurnGatesALaterIncompleteWalk(t *testing.T) {
	f := newFx(t)
	ps := newPromptProbeStore(f.store)
	f.src.Store = ps
	promptAs(f, f.sess, 0, rateAsk)
	promptAs(f, f.sess, 2, rateCorrection60)
	d := f.begin()
	wire, cp := f.persisted()
	require.Equal(t, rateCorrection60Goal, cp.CurrentWork.Goal, "fixture sanity: the records' goal")
	require.NotNil(t, wire.GoalTurn)
	require.Equal(t, core.TurnIndex(2), *wire.GoalTurn, "fixture sanity: from turn 2")

	ps.setDegraded(true)
	promptAs(f, f.sess, 4, rateCorrection45)
	promptAs(f, f.sess, 6, rateCorrection75)
	f.closedSeg(1, 0, 7)
	f.advance(d) // its refresh fails: the next pass's encoding reads the graph
	f.advance(d, 1)
	wire, cp = f.persisted()
	require.Equal(t, rateCorrection75Goal, cp.CurrentWork.Goal, "the fallback takes the segment's newest prompt")
	require.NotNil(t, wire.GoalTurn, "the fallback records the turn of the goal it took")
	require.Equal(t, core.TurnIndex(6), *wire.GoalTurn, "the fallback records the turn of the goal it took")

	ps.setDegraded(false)
	ps.failOpens(f.put(rateCorrection75, "UserPromptSubmit", "", true), 1<<20) // its bytes cannot be read
	f.advance(d)
	_, cp = f.persisted()
	require.Equal(t, rateCorrection75Goal, cp.CurrentWork.Goal,
		"a walk that could not read the turn-6 prompt must not step back to turn 4")

	got := sealed(t, f, f.precompactAs(f.sess))
	require.Equal(t, rateCorrection75Goal, got.CurrentWork.Goal)
}

// TestExplicitCurrentWorkSurvivesTheGraphFallback (audit 2's #24): SetCurrentWork stops every
// derivation for good (§7), the graph fallback included. While SessionPrompts cannot answer, Advance
// encodes a closed segment holding a newer prompt of this session, which the fallback would take
// as the goal of a derived current work; the explicit one stays, and a compaction inside that
// window seals it.
func TestExplicitCurrentWorkSurvivesTheGraphFallback(t *testing.T) {
	f := newFx(t)
	ps := newPromptProbeStore(f.store)
	f.src.Store = ps
	promptAs(f, f.sess, 0, rateAsk)
	d := f.begin()
	d.SetCurrentWork(checkpoint.CurrentWork{Goal: "Ship the limiter."})

	promptAs(f, f.sess, 2, rateCorrection60)
	f.closedSeg(1, 0, 3)
	ps.setDegraded(true)
	f.advance(d) // its refresh fails: the next pass's encoding reads the graph
	f.advance(d, 1)
	_, cp := f.persisted()
	require.Equal(t, "Ship the limiter.", cp.CurrentWork.Goal, "the fallback must not replace explicit work")

	got := sealed(t, f, f.precompactAs(f.sess))
	require.Equal(t, "Ship the limiter.", got.CurrentWork.Goal)
}

// TestUnreadableNewestPromptLeavesAnOversizedGoalReadOnce:the newest prompt's bytes are gone for
// good, so no walk reads every record it passes, and every refresh walks again. The goal prompt
// behind it, a paste past the evolution's read limit that the evolution does not cache, is still
// read whole once per draft, not at every refresh: the unreadable record costs a failed Open.
func TestUnreadableNewestPromptLeavesAnOversizedGoalReadOnce(t *testing.T) {
	limit := checkpoint.EvolutionReadLimitForTest
	big := rateCorrection45 + strings.Repeat(" The limiter keeps one bucket per client.", int(limit)/40+1)
	f := newFx(t)
	ps := newPromptProbeStore(f.store)
	f.src.Store = ps
	promptAs(f, f.sess, 0, rateAsk)
	promptAs(f, f.sess, 2, big)
	lostPromptAs(f, f.sess, 4)
	root := f.put(big, "UserPromptSubmit", "", true)

	d := f.begin()
	for range 5 {
		f.advance(d)
	}
	d.RefreshIntent(f.ctx()) // PreCompact's refresh, without the successor its seal opens
	_, got := f.persisted()

	require.Equal(t, rateCorrection45Goal, got.CurrentWork.Goal)
	whole, bounded := ps.readsOf(root, limit)
	require.Equal(t, 1, whole, "read whole once, for the goal")
	require.Equal(t, 1, bounded, "read bounded once, to learn it is past the limit")
}

// The prompts a session's current work skips: a slash-command invocation is not a statement of
// the task. Claude Code hands UserPromptSubmit the prompt as typed, so a plugin command arrives as
// "/qompack:why dec_..." (the C7 UAT-06 store holds exactly that), while a built-in such as
// /compact never reaches the hook at all. Qompack's own commands (/qompack:status, why, dropped,
// recall, eval, pin) inspect or annotate the session and are never the task in flight, whatever
// their arguments; a bare "/name" says nothing about the task either. Another command's arguments
// are the user's own words and are kept.
func TestSlashCommandPromptIsNotCurrentWork(t *testing.T) {
	for _, tc := range []struct{ name, prompt, goal string }{
		{"qompack status", "/qompack:status", rateCorrection60Goal},
		{"qompack why with an id", "/qompack:why dec_991dbff588ec", rateCorrection60Goal},
		{"qompack dropped with a flag", "/qompack:dropped --json", rateCorrection60Goal},
		{"qompack why after whitespace", " /qompack:why dec_991dbff588ec\n", rateCorrection60Goal},
		{
			"qompack pin with its invariant", "/qompack:pin Never write to prod.db from the export code.",
			rateCorrection60Goal,
		},
		{"a bare command", "/compact", rateCorrection60Goal},
		{"a bare namespaced command with whitespace", "  /frontend:review\n", rateCorrection60Goal},
		{"a dash after the slash is not a command name", "/-flag", "/-flag"},
		{
			"another command with arguments", "/fix-issue 42 the login form drops the session.",
			"/fix-issue 42 the login form drops the session.",
		},
		{
			"a path is not a command", "/usr/local/bin/kite crashes on start. Fix it.",
			"/usr/local/bin/kite crashes on start.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFx(t)
			openSegment(f, f.sess, 0)
			promptAs(f, f.sess, 0, rateAsk)
			promptAs(f, f.sess, 2, rateCorrection60)
			promptAs(f, f.sess, 4, tc.prompt)

			cp := sealed(t, f, f.precompactAs(f.sess))

			require.Equal(t, tc.goal, cp.CurrentWork.Goal)
			require.Equal(t, []string{rateCorrection60, tc.prompt}, cp.UserIntent.Evolution,
				"the intent still carries every prompt verbatim")
		})
	}
}

// TestBlankPromptIsNotCurrentWork: a prompt of only whitespace states no task either; the goal
// comes from the newest prompt before it. The intent skips a blank prompt too, and its refresh
// caches one as no text; the second case is one the evolution never read, because it stopped at a
// newer prompt past its read limit (a pasted injection block, which gives no goal), so the goal
// walk reads the blank prompt's whitespace itself.
func TestBlankPromptIsNotCurrentWork(t *testing.T) {
	limit := checkpoint.EvolutionReadLimitForTest
	injected := checkpoint.OpenTag(4) + "\n" + strings.Repeat("stale summary line\n", int(limit)/19+1) +
		checkpoint.InjectionCloseTag
	const blank = " \u00a0\n\t\u2003 "
	for _, tc := range []struct {
		name  string
		newer []string
	}{
		{"the newest prompt", nil},
		{"one the evolution never read", []string{injected}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFx(t)
			openSegment(f, f.sess, 0)
			promptAs(f, f.sess, 0, rateAsk)
			promptAs(f, f.sess, 2, rateCorrection60)
			promptAs(f, f.sess, 4, blank)
			for i, p := range tc.newer {
				promptAs(f, f.sess, core.TurnIndex(6+2*i), p)
			}

			cp := sealed(t, f, f.precompactAs(f.sess))

			require.Equal(t, rateCorrection60Goal, cp.CurrentWork.Goal)
		})
	}
}

// TestSlashCommandPromptIsNotCurrentWorkFromTheGraph: the graph fallback skips one too.
func TestSlashCommandPromptIsNotCurrentWorkFromTheGraph(t *testing.T) {
	f := newFx(t)
	f.src.Store = degradedPromptsStore{f.store}
	promptAs(f, f.sess, 0, rateAsk)
	promptAs(f, f.sess, 2, rateCorrection60)
	promptAs(f, f.sess, 4, "/qompack:status")
	closedSegAs(f, f.sess, 0, 5)

	cp := sealed(t, f, f.precompactAs(f.sess))

	require.Equal(t, rateCorrection60Goal, cp.CurrentWork.Goal)
}

// TestSlashCommandWalkReadsExactlyItsBound: a fresh draft's walk back past skipped prompts reads
// the goalWalkLimit newest records, the last of them included, and not one more.
func TestSlashCommandWalkReadsExactlyItsBound(t *testing.T) {
	const limit = checkpoint.GoalWalkLimitForTest
	for _, tc := range []struct {
		name     string
		commands int
		goal     string
	}{
		{"the bound's last record", limit - 1, rateCorrection60Goal},
		{"one past the bound", limit, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFx(t)
			openSegment(f, f.sess, 0)
			promptAs(f, f.sess, 0, rateAsk)
			promptAs(f, f.sess, 2, rateCorrection60)
			for i := range tc.commands {
				promptAs(f, f.sess, core.TurnIndex(4+i), "/qompack:status")
			}

			cp := sealed(t, f, f.precompactAs(f.sess))

			require.Equal(t, tc.goal, cp.CurrentWork.Goal)
		})
	}
}

// TestUnreadableNewestPromptFallsBackToTheNewestReadableOneFromTheGraph: the graph fallback walks
// back past a prompt node whose bytes cannot be read, as the records walk does.
func TestUnreadableNewestPromptFallsBackToTheNewestReadableOneFromTheGraph(t *testing.T) {
	f := newFx(t)
	f.src.Store = degradedPromptsStore{f.store}
	promptAs(f, f.sess, 0, rateAsk)
	promptAs(f, f.sess, 2, rateCorrection60)
	lostPromptAs(f, f.sess, 4)
	closedSegAs(f, f.sess, 0, 5)

	cp := sealed(t, f, f.precompactAs(f.sess))

	require.Equal(t, rateCorrection60Goal, cp.CurrentWork.Goal)
}

// TestSlashCommandWalkFromTheGraphReadsExactlyItsBound: the graph fallback's walk back past
// skipped prompt nodes is bounded the same way: the goalWalkLimit newest nodes, and not one more.
func TestSlashCommandWalkFromTheGraphReadsExactlyItsBound(t *testing.T) {
	const limit = checkpoint.GoalWalkLimitForTest
	for _, tc := range []struct {
		name     string
		commands int
		goal     string
	}{
		{"the bound's last node", limit - 1, rateAskGoal},
		{"one past the bound", limit, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFx(t)
			f.src.Store = degradedPromptsStore{f.store}
			promptAs(f, f.sess, 0, rateAsk)
			for i := range tc.commands {
				promptAs(f, f.sess, core.TurnIndex(1+i), "/qompack:status")
			}
			closedSegAs(f, f.sess, 0, core.TurnIndex(tc.commands+1))

			cp := sealed(t, f, f.precompactAs(f.sess))

			require.Equal(t, tc.goal, cp.CurrentWork.Goal)
		})
	}
}

// TestSlashCommandWalkIsBounded: the walk back past skipped prompts reads at most as many records as
// the evolution can list. A session whose newest maxIntentEvolution prompts are all skipped keeps
// the goal it has rather than reading its whole history at every new prompt.
func TestSlashCommandWalkIsBounded(t *testing.T) {
	f := newFx(t)
	openSegment(f, f.sess, 0)
	promptAs(f, f.sess, 0, rateAsk)
	promptAs(f, f.sess, 2, rateCorrection60)
	d := f.begin()
	_, cp := f.persisted()
	require.Equal(t, rateCorrection60Goal, cp.CurrentWork.Goal)

	const limit = checkpoint.GoalWalkLimitForTest
	for i := range limit {
		promptAs(f, f.sess, core.TurnIndex(4+i), "/qompack:status")
	}
	f.advance(d)
	_, cp = f.persisted()
	require.Equal(t, rateCorrection60Goal, cp.CurrentWork.Goal, "kept: the walk stopped at its bound")

	// One more fresh draft over the same records finds nothing within the bound and so has none,
	// where an unbounded walk would have reached the correction.
	require.NoError(t, f.w.Abort(d))
	cp2 := sealed(t, f, f.precompactAs(f.sess))
	require.Empty(t, cp2.CurrentWork.Goal)
}

// TestUnreadableNewestPromptFallsBackToTheNewestReadableOne: a fresh draft whose newest prompt's
// bytes cannot be read takes its goal from the newest prompt it can read — the one the same
// checkpoint's evolution ends with — rather than having none.
func TestUnreadableNewestPromptFallsBackToTheNewestReadableOne(t *testing.T) {
	f := newFx(t)
	openSegment(f, f.sess, 0)
	promptAs(f, f.sess, 0, rateAsk)
	promptAs(f, f.sess, 2, rateCorrection60)
	lost := core.HashBytes(core.DomainChunk, []byte("a prompt whose bytes were never stored"))
	id := core.ToolUseID(fmt.Sprintf("prompt_%s_%d", f.sess, 4))
	require.NoError(t, f.store.RecordToolUse(f.ctx(), store.ToolUseRecord{
		ID: id, Session: f.sess, Turn: 4, TS: f.now(), Tool: "UserPromptSubmit", Root: lost,
	}))

	cp := sealed(t, f, f.precompactAs(f.sess))

	require.Equal(t, rateCorrection60Goal, cp.CurrentWork.Goal)
	require.Equal(t, []string{rateCorrection60}, cp.UserIntent.Evolution)
}

// TestCurrentWorkFollowsCapturedTurnOrder pins the order "newest" means: the captured turn, not the
// record's host stamp. A prompt that reached only a hook's client spool can be published behind
// one its host sent later (observer.prompt_out_of_host_order); captured turns are never renumbered
// (D35(b)), the evolution lists prompts in turn order, and current work agrees with the
// evolution's last entry rather than contradicting it exactly where the order is in doubt.
func TestCurrentWorkFollowsCapturedTurnOrder(t *testing.T) {
	f := newFx(t)
	openSegment(f, f.sess, 0)
	t0 := core.NowMilli(f.p.Clock)
	promptStamped(f, f.sess, 0, t0, rateAsk)
	promptStamped(f, f.sess, 2, t0+30_000, rateCorrection45)
	promptStamped(f, f.sess, 4, t0+10_000, rateCorrection60)

	cp := sealed(t, f, f.precompactAs(f.sess))

	require.Equal(t, rateCorrection60Goal, cp.CurrentWork.Goal)
	require.Equal(t, []string{rateCorrection45, rateCorrection60}, cp.UserIntent.Evolution)
}

// TestOversizedNewestPromptIsReadOncePerDraft: a newest prompt past the evolution's read limit (a
// long paste) is read whole once per draft — for its goal — and bounded once — for the evolution to
// learn it is too long — not again at every Advance and PreCompact refresh. The same holds for one
// that renders to nothing (a pasted injection block), which gives no goal, whether or not an older
// prompt gives one.
func TestOversizedNewestPromptIsReadOncePerDraft(t *testing.T) {
	limit := checkpoint.EvolutionReadLimitForTest
	filler := strings.Repeat(" The limiter keeps one bucket per client.", int(limit)/40+1)
	injected := checkpoint.OpenTag(4) + "\n" + strings.Repeat("stale summary line\n", int(limit)/19+1) +
		checkpoint.InjectionCloseTag
	for _, tc := range []struct{ name, first, newest, goal string }{
		{"a long paste", rateAsk, rateCorrection45 + filler, rateCorrection45Goal},
		{"a pasted injection block", rateAsk, injected, rateAskGoal},
		{"a pasted injection block after only a command", "/qompack:status", injected, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, int64(len(tc.newest)), limit+1, "fixture sanity: past the read limit")
			f := newFx(t)
			ps := newPromptProbeStore(f.store)
			f.src.Store = ps
			promptAs(f, f.sess, 0, tc.first)
			promptAs(f, f.sess, 2, tc.newest)
			root := f.put(tc.newest, "UserPromptSubmit", "", true)

			d := f.begin()
			for range 5 {
				f.advance(d)
			}
			d.RefreshIntent(f.ctx()) // PreCompact's refresh, without the successor its seal opens
			_, got := f.persisted()

			require.Equal(t, tc.goal, got.CurrentWork.Goal)
			whole, bounded := ps.readsOf(root, limit)
			require.Equal(t, 1, whole, "read whole once, for the goal")
			require.Equal(t, 1, bounded, "read bounded once, to learn it is past the limit")
		})
	}
}

// TestResumedDraftReDerivesCurrentWork: a draft persisted by an earlier build is resumed with
// whatever goal that build derived. Candidate 7 gave a fork its PARENT's prompt (F-C7-UAT06-1), so
// a resumed draft's derived goal is derived again from the session's own prompts: replaced by its
// own newest, and cleared when no prompt of its own can give one.
func TestResumedDraftReDerivesCurrentWork(t *testing.T) {
	for _, tc := range []struct {
		name string
		own  []string
		goal string
	}{
		{"no prompt of its own", nil, ""},
		{"only a slash command of its own", []string{forkWhy}, ""},
		{
			"exactly the walk's bound of slash commands of its own",
			slices.Repeat([]string{forkWhy}, checkpoint.GoalWalkLimitForTest), "",
		},
		{"a prompt of its own", []string{forkWhy, rateCorrection45}, rateCorrection45Goal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFx(t)
			liveShapeParent(t, f)
			f.noteFork(f.sess)
			openSegment(f, f.sess, 0)
			for i, p := range tc.own {
				promptAs(f, f.sess, core.TurnIndex(1+2*i), p)
			}
			plantDerivedGoal(t, f, rateReadLimiter)

			cp := sealed(t, f, f.precompactAs(f.sess))

			require.Equal(t, tc.goal, cp.CurrentWork.Goal)
		})
	}
}

// plantDerivedGoal leaves session f.sess with a persisted draft whose derived goal is goal, the way
// a candidate-7 daemon left a fork's draft (no goal_turn key), and no live draft, the way a restart
// leaves it.
func plantDerivedGoal(t *testing.T, f *fx, goal string) {
	t.Helper()
	plantDerivedGoalAt(t, f, goal, nil)
}

// plantDerivedGoalAt is plantDerivedGoal with the draft file's goal_turn set to turn (nil: none).
func plantDerivedGoalAt(t *testing.T, f *fx, goal string, turn *core.TurnIndex) {
	t.Helper()
	require.NoError(t, f.w.SetSources(f.src))
	f.begin()
	wire, cp := f.persisted()
	require.NoError(t, f.w.Abort(f.w.DraftFor(f.sess)))
	cp.CurrentWork = checkpoint.CurrentWork{Goal: goal}
	raw, err := checkpoint.Marshal(cp)
	require.NoError(t, err)
	wire.Checkpoint = raw
	wire.GoalTurn = turn
	b, err := json.Marshal(wire)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(f.draftPath()), b, 0o600))
}
