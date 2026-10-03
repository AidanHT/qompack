package checkpoint_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// The derived current work (§8: one sentence of the session's most recent prompt) across the edges
// the V6 close-out's wave-20 audit found (D59): a one-prompt session, a prompt list that fails for a
// while, Qompack's own slash commands, an unreadable or oversized newest prompt, prompts published
// out of host order, and a draft persisted by an earlier build.

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
	// cancel, when set, is called once right after the next SessionPrompts answers: the context
	// running out between the list and the reads that follow it.
	cancel context.CancelFunc
}

func newPromptProbeStore(s store.Store) *promptProbeStore {
	return &promptProbeStore{Store: s, reads: map[core.Hash][]int64{}}
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
// answered cannot read the new newest prompt; it is not remembered as walked, so the next refresh
// reads it and the goal moves to it.
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
		{
			"qompack pin with its invariant", "/qompack:pin Never write to prod.db from the export code.",
			rateCorrection60Goal,
		},
		{"a bare command", "/compact", rateCorrection60Goal},
		{"a bare namespaced command with whitespace", "  /frontend:review\n", rateCorrection60Goal},
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
// a candidate-7 daemon left a fork's draft, and no live draft, the way a restart leaves it.
func plantDerivedGoal(t *testing.T, f *fx, goal string) {
	t.Helper()
	require.NoError(t, f.w.SetSources(f.src))
	f.begin()
	wire, cp := f.persisted()
	require.NoError(t, f.w.Abort(f.w.DraftFor(f.sess)))
	cp.CurrentWork = checkpoint.CurrentWork{Goal: goal}
	raw, err := checkpoint.Marshal(cp)
	require.NoError(t, err)
	wire.Checkpoint = raw
	b, err := json.Marshal(wire)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(f.draftPath()), b, 0o600))
}
