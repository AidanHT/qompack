package checkpoint_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/store"
)

// F-UAT05-1 / F-UAT06-2 (Phase 4 live lane): an explicit user correction ("the delimiter must be a
// TAB ... the semicolon requirement is superseded") never reached user_intent.evolution in any
// checkpoint of a real session, so every rehydration re-injected only the superseded requirement.
//
// Evolution was filled only by encoding CLOSED segments, and the observer closes a session's
// segment at SessionEnd: at every host compaction the prompts since the last session end sit in
// the open segment, which neither the cold PreCompact catch-up nor a live draft ever reads. These
// rows pin the fix: every checkpoint carries the session's later prompts, verbatim and in order,
// read from the session's own prompt records.

// promptAs records one prompt capture for session s exactly as the observer's worker path does
// (observer/prompt_delivery.go recordPromptDurable): verbatim bytes, the prompt_<s>_<turn> index
// record, and dag.BuildUserPrompt's node. That node is keyed by TURN alone, so two sessions'
// prompts at one turn share it and the later one's Ref wins — which is why intent is read from the
// session's own index records rather than from the graph.
func promptAs(f *fx, s core.SessionID, turn core.TurnIndex, text string) {
	f.t.Helper()
	root := f.put(text, "UserPromptSubmit", "", true)
	id := core.ToolUseID(fmt.Sprintf("prompt_%s_%d", s, int(turn)))
	require.NoError(f.t, f.store.RecordToolUse(f.ctx(), store.ToolUseRecord{
		ID: id, Session: s, Turn: turn, TS: f.now(), Tool: "UserPromptSubmit", Root: root,
	}))
	require.NoError(f.t, dag.BuildUserPrompt(f.graph, dag.ObservedPrompt{
		Turn: turn, TS: f.now(), Pos: f.nextPos(), Tokens: core.Tokens(len(text) / 4), Ref: string(id),
	}))
}

// openSegment opens the session's one segment and leaves it open, which is where every prompt since
// the session last ended is when the host compacts.
func openSegment(f *fx, s core.SessionID, start core.TurnIndex) {
	f.t.Helper()
	_, err := f.store.Segments().Open(f.ctx(), store.Segment{Session: s, StartTurn: start})
	require.NoError(f.t, err)
}

// sealed reads back the checkpoint a PreCompact result names.
func sealed(t *testing.T, f *fx, res checkpoint.PreCompactResult) checkpoint.Checkpoint {
	t.Helper()
	cp, _, err := f.reader(t).Get(f.ctx(), res.Ref.Seq)
	require.NoError(t, err)
	return cp
}

// precompactAs runs PreCompact for session s the way the daemon's seam does: with the writer's
// sources published first, so a compaction that finds no live draft can open one.
func (f *fx) precompactAs(s core.SessionID) checkpoint.PreCompactResult {
	f.t.Helper()
	require.NoError(f.t, f.w.SetSources(f.src))
	in := f.precompactInput()
	in.Session = s
	res, err := f.w.PreCompact(f.ctx(), in)
	require.NoError(f.t, err)
	return res
}

const (
	semicolonAsk = "Write the export in reports.py. Requirement: the export must use a semicolon (;) as " +
		"the field delimiter."
	tabCorrection = "Correction: the export delimiter must be a TAB character, not a semicolon. The " +
		"semicolon requirement is superseded."
)

// TestPreCompactCarriesACorrectionFromTheOpenSegment is F-UAT05-1 as the live session ran it: the
// original and the correction are both in the session's open segment when the host compacts.
func TestPreCompactCarriesACorrectionFromTheOpenSegment(t *testing.T) {
	f := newFx(t)
	openSegment(f, f.sess, 0)
	promptAs(f, f.sess, 0, semicolonAsk)
	promptAs(f, f.sess, 2, tabCorrection)

	cp := sealed(t, f, f.precompactAs(f.sess))

	require.Equal(t, semicolonAsk, cp.UserIntent.Original)
	require.Equal(t, []string{tabCorrection}, cp.UserIntent.Evolution,
		"the correction is in the checkpoint, verbatim, after the original")
}

// TestPreCompactCarriesEveryCorrectionAcrossCheckpoints is F-UAT06-2's shape: a second correction
// after the first compaction reaches the second checkpoint, after the first, in order — through the
// live draft Finalize opens, which no idle tick has advanced.
func TestPreCompactCarriesEveryCorrectionAcrossCheckpoints(t *testing.T) {
	f := newFx(t)
	openSegment(f, f.sess, 0)
	promptAs(f, f.sess, 0, "We are building a rate limiter. Requirement: allow 100 requests per minute per client.")
	promptAs(f, f.sess, 2, "Correction: the limit must be 60 requests per minute per client, not 100.")
	first := sealed(t, f, f.precompactAs(f.sess))

	promptAs(f, f.sess, 5, "Correction: the limit must now be 75 requests per minute per client.")
	second := sealed(t, f, f.precompactAs(f.sess))

	require.Equal(t, []string{"Correction: the limit must be 60 requests per minute per client, not 100."},
		first.UserIntent.Evolution)
	require.Equal(t, []string{
		"Correction: the limit must be 60 requests per minute per client, not 100.",
		"Correction: the limit must now be 75 requests per minute per client.",
	}, second.UserIntent.Evolution, "oldest first, so the renderer can put the newest on top")
	require.Equal(t, first.UserIntent.Original, second.UserIntent.Original)
}

// TestEvolutionKeepsTheNewestRestatementsPastTheCap: a long session says more than the evolution cap
// holds. The cap used to keep the OLDEST 64, so every correction after the 65th prompt was lost to
// the one field that carries the current authority; it keeps the newest now, and says how many
// earlier ones it left out.
func TestEvolutionKeepsTheNewestRestatementsPastTheCap(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "Start the migration.", true)
	for i := 1; i <= 70; i++ {
		f.prompt(core.TurnIndex(i), fmt.Sprintf("Step %02d: continue.", i), true)
	}
	f.prompt(71, "Correction: the migration must stay read-only.", true)
	f.closedSeg(1, 0, 71)
	d := f.begin()
	f.advance(d, 1)

	_, cp := f.persisted()
	require.Len(t, cp.UserIntent.Evolution, 64, "the cap still holds")
	require.Equal(t, "Correction: the migration must stay read-only.",
		cp.UserIntent.Evolution[len(cp.UserIntent.Evolution)-1], "the newest restatement is kept")
	require.Equal(t, "Step 08: continue.", cp.UserIntent.Evolution[0], "the oldest are the ones left out")

	var elided []checkpoint.DropEntry
	for _, e := range cp.Dropped {
		if e.Kind == "user_intent_evolution" {
			elided = append(elided, e)
		}
	}
	require.Len(t, elided, 1, "one entry, however many were left out: %v", cp.Dropped)
	require.Contains(t, elided[0].Detail, "7 earlier restatements")
}

// TestBeginDoesNotAdoptAnotherSessionsIntent: a new session in a project whose newest checkpoint
// belongs to another session starts from its OWN first prompt. Adopting the other session's
// original made every such session's first rehydration report an intent_mismatch Loud.
func TestBeginDoesNotAdoptAnotherSessionsIntent(t *testing.T) {
	f := newFx(t)
	const other = core.SessionID("sess_sp10_other")
	openSegment(f, other, 0)
	promptAs(f, other, 0, "Build the CSV importer.")
	promptAs(f, other, 2, "Correction: the importer must skip the header row.")
	f.precompactAs(other)

	openSegment(f, f.sess, 0)
	promptAs(f, f.sess, 0, "Fix the flaky auth refresh.")
	cp := sealed(t, f, f.precompactAs(f.sess))

	require.Equal(t, "Fix the flaky auth refresh.", cp.UserIntent.Original)
	require.Empty(t, cp.UserIntent.Evolution, "nothing of the other session's intent is carried")
}

// TestBeginResumesFromTheSessionsOwnCheckpoint: a session that resumes after another session has
// compacted in the same project keeps its own original and history, not the newer checkpoint's.
func TestBeginResumesFromTheSessionsOwnCheckpoint(t *testing.T) {
	f := newFx(t)
	openSegment(f, f.sess, 0)
	promptAs(f, f.sess, 0, "Fix the flaky auth refresh.")
	promptAs(f, f.sess, 2, "Correction: keep the refresh token in memory only.")
	f.precompactAs(f.sess)
	require.NoError(t, f.w.Abort(f.w.DraftFor(f.sess)), "the session ends; its successor draft goes")

	const other = core.SessionID("sess_sp10_other")
	openSegment(f, other, 0)
	promptAs(f, other, 0, "Build the CSV importer.")
	f.precompactAs(other)

	promptAs(f, f.sess, 4, "Continue with the refresh fix.")
	cp := sealed(t, f, f.precompactAs(f.sess))

	require.Equal(t, "Fix the flaky auth refresh.", cp.UserIntent.Original)
	require.Equal(t, []string{
		"Correction: keep the refresh token in memory only.",
		"Continue with the refresh fix.",
	}, cp.UserIntent.Evolution)
}

// TestEvolutionHoldsTheRehydrationCeilingSoPointersSurvive: a session of long pastes. Truncate never
// cuts user_intent (tier 1), so an evolution bounded only by count (64 verbatim prompts of any size)
// pushed the checkpoint past its budget and cost it every pointer, open question and decision while
// still recording budget_exceeded — for restatements no rehydration could ever inject, since item 2
// admits evolution newest first and stops at the first that does not fit the payload ceiling. The
// checkpoint keeps the newest whole restatements up to that ceiling and names the rest by prompt id.
func TestEvolutionHoldsTheRehydrationCeilingSoPointersSurvive(t *testing.T) {
	f := newFx(t)
	const pastes = 64
	f.prompt(0, "Reconcile the ledger exports with the bank statements.", true)
	for i := 1; i <= pastes; i++ {
		f.prompt(core.TurnIndex(i), fmt.Sprintf("Paste %02d: ", i)+strings.Repeat("lorem ipsum dolor ", 222), true)
	}
	const correction = "Correction: reconcile against the March statements only."
	f.prompt(pastes+1, correction, true)
	f.tool("toolu_bound_0001", pastes+2, "Bash", "", "reconciled 412 of 430 rows", false)
	f.tool("toolu_bound_0002", pastes+3, "Bash", "", "18 rows differ by rounding", false)
	f.closedSeg(1, 0, pastes+3)
	d := f.begin()
	f.advance(d, 1)

	res, err := f.w.PreCompact(f.ctx(), f.precompactInput())
	require.NoError(t, err)
	cp := sealed(t, f, res)

	for _, e := range cp.Dropped {
		require.NotEqual(t, "budget_exceeded", e.Kind, "tier 1 fits the checkpoint budget: %v", e)
		require.NotEqual(t, "tool_pointer", e.Kind, "no pointer is cut to make room for evolution: %v", e)
	}
	require.Len(t, cp.Pointers.Tools, 2, "the tool pointers survive truncation")

	require.NotEmpty(t, cp.UserIntent.Evolution)
	require.Equal(t, correction, cp.UserIntent.Evolution[len(cp.UserIntent.Evolution)-1],
		"the newest restatement, the current authority, is kept")
	total := 0
	for _, e := range cp.UserIntent.Evolution {
		total += utf8.RuneCountInString(strings.TrimSpace(e))
	}
	require.LessOrEqual(t, total, checkpoint.EvolutionCeilingChars,
		"no more restatement text than one rehydration can carry")

	var elided []checkpoint.DropEntry
	for _, e := range cp.Dropped {
		if e.Kind == "user_intent_evolution" {
			elided = append(elided, e)
		}
	}
	require.Len(t, elided, 1, "%v", cp.Dropped)
	kept := len(cp.UserIntent.Evolution) - 1 // the pastes kept beside the correction
	newestLeftOut := fmt.Sprintf("prompt_%s_%d", f.sess, pastes-kept)
	require.Contains(t, elided[0].Detail, fmt.Sprintf("%d earlier restatements", pastes-kept))
	require.Contains(t, elided[0].Detail, "expand(tool_use_id="+newestLeftOut+")",
		"the newest restatement left out is named with the call that returns it")
	require.Contains(t, elided[0].Detail, fmt.Sprintf("expand(tool_use_id=prompt_%s_1)", f.sess),
		"and so is the oldest")
}

// TestRefreshHoldsOnlyTheTextsItKeeps: the draft's prompt-text cache used to hold the verbatim
// text of every prompt the session ever made, read even when the cap discarded it, and handed on to
// every successor draft for the daemon's life. It holds the original and the kept restatements.
func TestRefreshHoldsOnlyTheTextsItKeeps(t *testing.T) {
	f := newFx(t)
	const prompts = 100
	for i := 0; i <= prompts; i++ {
		f.prompt(core.TurnIndex(i), fmt.Sprintf("Prompt %03d: continue.", i), true)
	}
	f.closedSeg(1, 0, prompts)
	d := f.begin()
	f.advance(d, 1)

	_, cp := f.persisted()
	require.Len(t, cp.UserIntent.Evolution, 64, "fixture sanity: the count cap binds")
	require.Equal(t, len(cp.UserIntent.Evolution)+1, checkpoint.PromptCacheLenForTest(d),
		"the original and the kept restatements, nothing the cap left out")
}

// TestBeginConsumesAStashedCacheOnEveryPath: afterSeal stashes the sealed draft's cache for the
// successor Begin opens. Only a FRESH draft used to take it, so a Begin that found a live draft or
// resumed a persisted one left it stashed for good.
func TestBeginConsumesAStashedCacheOnEveryPath(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "Start the migration.", true)
	d := f.begin()

	checkpoint.HandOffForTest(f.w, d)
	require.True(t, checkpoint.HandoffPendingForTest(f.w, f.sess), "fixture sanity")
	require.Same(t, d, f.begin(), "the live draft is returned")
	require.False(t, checkpoint.HandoffPendingForTest(f.w, f.sess), "the live path consumes it")

	w2, err := checkpoint.OpenWriter(f.p.Root, f.p.Cfg, f.p.Log, obs.New(f.p.Clock), f.p.Clock)
	require.NoError(t, err)
	checkpoint.HandOffForTest(w2, d)
	_, err = w2.Begin(f.ctx(), f.sess, 0, f.src)
	require.NoError(t, err, "a second writer resumes the persisted draft")
	require.False(t, checkpoint.HandoffPendingForTest(w2, f.sess), "the resume path consumes it")
}
