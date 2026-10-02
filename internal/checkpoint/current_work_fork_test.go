package checkpoint_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// F-C7-UAT06-1 (candidate 7 live lane): in a forked session (claude --resume <parent>
// --fork-session) the fork's SECOND checkpoint said its current work was the parent's turn-4
// instruction — "Call mcp__plugin_qompack_qompack__record_eliminated ..." cut at the 160-rune goal
// cap — while the fork's newest prompt was its own "must be 45" correction.
//
// Current work was derived from the dependence graph's userprompt nodes in the encoded segment's
// turn range. Those nodes are keyed by turn alone (promptAs explains), so a fork's segment spanning
// turns 2-4 found the PARENT's turn-2 and turn-4 prompts beside its own turn-3 one and took the
// highest turn; and a prompt in the still-open segment (the 45 correction) was never read at all.
// Current work now comes from the same source as the user's intent: the session's own prompt
// records, newest first, refreshed at every Begin, Advance and PreCompact.

// The rest of the UAT-06 conversation, verbatim in shape.
const (
	rateRecordEliminated = "Call mcp__plugin_qompack_qompack__record_eliminated with target \"per-client rate " +
		"limit\", approach \"100 requests per minute\" and reason \"superseded by the user's correction: 60 per " +
		"minute\". Report the raw result in one line."
	rateReadLimiter  = "Use the Read tool to read src/limiter.py and summarize it in one sentence."
	forkReadDesign   = "Use the Read tool to read docs/design.md and summarize it in one sentence."
	forkAlreadyTried = "Call mcp__plugin_qompack_qompack__already_tried with target \"per-client rate limit\" " +
		"and approach \"100 requests per minute\". Report the raw result in one line."
	forkWhy          = "/qompack:why dec_991dbff588ec"
	rateCorrection45 = "Correction: the limit must be 45 requests per minute per client, not 60. The 60 figure " +
		"is superseded. Acknowledge in one sentence."
	rateCorrection45Goal = "Correction: the limit must be 45 requests per minute per client, not 60."
)

// closedSegAs opens and closes one segment of session s over [start, end], the way the observer
// closes a segment at a changepoint or a compaction.
func closedSegAs(f *fx, s core.SessionID, start, end core.TurnIndex) {
	f.t.Helper()
	id, err := f.store.Segments().Open(f.ctx(), store.Segment{Session: s, StartTurn: start})
	require.NoError(f.t, err)
	require.NoError(f.t, f.store.Segments().Close(f.ctx(), id, end, map[string]float64{"tokens": 400}))
}

// liveShapeParent runs the parent session the way UAT-06's session A did: the original, the
// correction, the record_eliminated instruction and a read, all in one closed, encoded segment,
// then one compaction; the session then ends.
func liveShapeParent(t *testing.T, f *fx) {
	t.Helper()
	promptAs(f, forkParent, 0, rateAsk)
	promptAs(f, forkParent, 2, rateCorrection60)
	promptAs(f, forkParent, 4, rateRecordEliminated)
	promptAs(f, forkParent, 6, rateReadLimiter)
	closedSegAs(f, forkParent, 0, 7)
	cp := sealed(t, f, f.precompactAs(forkParent))
	require.Equal(t, "Use the Read tool to read src/limiter.py and summarize it in one sentence.",
		cp.CurrentWork.Goal, "fixture sanity: the parent's own newest prompt")
	require.NoError(t, f.w.Abort(f.w.DraftFor(forkParent)), "the parent session ends")
}

// TestForkCurrentWorkIsTheForksNewestPrompt is F-C7-UAT06-1 in the live lane's exact shape: the
// fork's second checkpoint encodes a segment over turns 2-4 (where the parent has prompts at turns 2
// and 4 and the fork one at turn 3), and the fork's two newest prompts sit in its open segment.
func TestForkCurrentWorkIsTheForksNewestPrompt(t *testing.T) {
	f := newFx(t)
	liveShapeParent(t, f)
	f.noteFork(f.sess)

	promptAs(f, f.sess, 0, forkReadDesign)
	closedSegAs(f, f.sess, 0, 1)
	first := sealed(t, f, f.precompactAs(f.sess))

	promptAs(f, f.sess, 3, forkAlreadyTried)
	closedSegAs(f, f.sess, 2, 4)
	openSegment(f, f.sess, 5)
	promptAs(f, f.sess, 5, forkWhy)
	promptAs(f, f.sess, 7, rateCorrection45)
	second := sealed(t, f, f.precompactAs(f.sess))

	require.Equal(t, forkReadDesign, first.CurrentWork.Goal, "the fork's first block: its own first prompt")
	require.Equal(t, rateCorrection45Goal, second.CurrentWork.Goal,
		"the fork's newest prompt, not an inherited parent prompt at a colliding turn")

	// The inheritance itself is unchanged: the parent's original, its correction and the parent's
	// later prompts before the fork, then the fork's own prompts in order.
	require.Equal(t, rateAsk, second.UserIntent.Original)
	require.Equal(t, []string{
		rateCorrection60, rateRecordEliminated, rateReadLimiter,
		forkReadDesign, forkAlreadyTried, forkWhy, rateCorrection45,
	}, second.UserIntent.Evolution)
}

// TestForkWithNoPromptOfItsOwnHasNoDerivedCurrentWork: a fork compacted before its user has said
// anything in it. It has no prompt of its own, so nothing is derived: the goal stays empty, as a
// fork's first draft always began, and no parent prompt is promoted into it through a turn the
// fork's segment shares with the parent's.
func TestForkWithNoPromptOfItsOwnHasNoDerivedCurrentWork(t *testing.T) {
	f := newFx(t)
	liveShapeParent(t, f)
	f.noteFork(f.sess)
	closedSegAs(f, f.sess, 0, 4)

	cp := sealed(t, f, f.precompactAs(f.sess))

	require.Empty(t, cp.CurrentWork.Goal)
	require.Equal(t, rateAsk, cp.UserIntent.Original, "the inherited intent is carried as before")
	require.Equal(t, []string{rateCorrection60, rateRecordEliminated, rateReadLimiter}, cp.UserIntent.Evolution)
}

// TestForkWithNoPromptOfItsOwnKeepsAnEmptyGoal is the same fork compacted while its only segment
// is still open — the shape a fork that compacts before anything closes a segment has. Before and
// after the fix its goal is empty.
func TestForkWithNoPromptOfItsOwnKeepsAnEmptyGoal(t *testing.T) {
	f := newFx(t)
	liveShapeParent(t, f)
	f.noteFork(f.sess)
	openSegment(f, f.sess, 0)

	cp := sealed(t, f, f.precompactAs(f.sess))

	require.Empty(t, cp.CurrentWork.Goal)
	require.Equal(t, rateAsk, cp.UserIntent.Original)
}

// TestResumedSessionCurrentWorkIsItsNewestPrompt: claude --resume keeps the session id, so the
// resumed conversation's prompts are the same session's records at later turns. Its second
// checkpoint's current work is its newest prompt — the one in the open segment included — and
// another session's prompt at a turn inside its encoded range is not.
func TestResumedSessionCurrentWorkIsItsNewestPrompt(t *testing.T) {
	f := newFx(t)
	const other = core.SessionID("sess_sp10_other")
	promptAs(f, f.sess, 0, rateAsk)
	promptAs(f, f.sess, 2, rateCorrection60)
	closedSegAs(f, f.sess, 0, 3)
	first := sealed(t, f, f.precompactAs(f.sess))
	require.NoError(t, f.w.Abort(f.w.DraftFor(f.sess)), "the session ends; the user resumes it later")

	promptAs(f, other, 5, "Build the CSV importer.")
	promptAs(f, f.sess, 4, forkWhy)
	closedSegAs(f, f.sess, 4, 5)
	openSegment(f, f.sess, 6)
	promptAs(f, f.sess, 6, rateCorrection45)
	second := sealed(t, f, f.precompactAs(f.sess))

	require.Equal(t, "Correction: the limit must be 60 requests per minute per client, not 100.", first.CurrentWork.Goal)
	require.Equal(t, rateCorrection45Goal, second.CurrentWork.Goal)
	require.Equal(t, rateAsk, second.UserIntent.Original)
	require.Equal(t, []string{rateCorrection60, forkWhy, rateCorrection45}, second.UserIntent.Evolution)
}

// TestExplicitCurrentWorkSurvivesAPromptRefresh: SetCurrentWork stops every derivation for good
// (§7), the prompt-record refresh at PreCompact included.
func TestExplicitCurrentWorkSurvivesAPromptRefresh(t *testing.T) {
	f := newFx(t)
	openSegment(f, f.sess, 0)
	promptAs(f, f.sess, 0, rateAsk)
	f.begin().SetCurrentWork(checkpoint.CurrentWork{Goal: "Ship the limiter."})
	promptAs(f, f.sess, 2, rateCorrection60)

	cp := sealed(t, f, f.precompactAs(f.sess))
	require.Equal(t, "Ship the limiter.", cp.CurrentWork.Goal)
	require.Equal(t, []string{rateCorrection60}, cp.UserIntent.Evolution, "the intent itself is still refreshed")
}

// hiddenPromptsStore is the shipped store without the SessionPrompts capability: embedding the
// interface exposes only store.Store's own methods.
type hiddenPromptsStore struct{ store.Store }

// degradedPromptsStore has the capability but cannot answer, the way FSStore.SessionPrompts is
// core.ErrDegraded past its scan limit.
type degradedPromptsStore struct{ store.Store }

func (degradedPromptsStore) SessionPrompts(context.Context, core.SessionID) ([]store.ToolUseRecord, error) {
	return nil, core.ErrDegraded
}

// TestCurrentWorkWithoutAnswerFromSessionPromptsComesFromTheGraph: when the session's own prompt
// records cannot be listed — a store without the capability, or one whose SessionPrompts fails —
// the goal still comes from the graph's userprompt nodes in the encoded segment, as before the
// own-records derivation. Those nodes are shared across sessions by turn, so only one whose record
// belongs to this session counts: another session's prompt at a turn inside the range is not this
// session's current work (F-C7-UAT06-1's collision).
func TestCurrentWorkWithoutAnswerFromSessionPromptsComesFromTheGraph(t *testing.T) {
	for name, wrap := range map[string]func(store.Store) store.Store{
		"without the capability": func(s store.Store) store.Store { return hiddenPromptsStore{s} },
		"capability degraded":    func(s store.Store) store.Store { return degradedPromptsStore{s} },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFx(t)
			const other = core.SessionID("sess_sp10_other")
			f.src.Store = wrap(f.store)
			promptAs(f, f.sess, 0, rateAsk)
			promptAs(f, f.sess, 2, rateCorrection60)
			promptAs(f, other, 3, "Build the CSV importer.")
			closedSegAs(f, f.sess, 0, 3)

			cp := sealed(t, f, f.precompactAs(f.sess))

			require.Equal(t, "Correction: the limit must be 60 requests per minute per client, not 100.",
				cp.CurrentWork.Goal)
		})
	}
}
