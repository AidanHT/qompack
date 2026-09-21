package observer

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/store"
)

// SP08-D3 (Option A) observer-side behaviour: the reply path records nothing and only shows the
// warning; the worker/replay path is the authoritative recorder and, for a LEASED delivery,
// propagates a lost capture as an error so the daemon can leave the frontier unacknowledged. The
// leased redelivery-absorb path (observationRecord) is exercised end to end at the daemon level
// (real store + on-disk sidecar); the local fakeStore has no ToolUse, so it is not re-tested here.

func promptEvent(sess core.SessionID, text string) Event {
	return Event{HookEventName: userPromptSubmit, SessionID: sess, Prompt: text}
}

// submitReply is the LIVE reply path (Option A): OnUserPrompt under the reply-only marker, which
// drains the pending thrash warning and records nothing. It is where the synchronous warning feature
// now lives, so the warning tests drive it here rather than through the record-path submit.
func (h *harness) submitReply(text string) Output {
	h.t.Helper()
	out, err := h.obs.OnUserPrompt(WithPromptReplyOnly(context.Background()), promptOf(text))
	require.NoError(h.t, err)
	return out
}

func TestOnUserPrompt_ReplyOnlyRecordsNothing(t *testing.T) {
	h := newHarness(t)
	const sess = core.SessionID("sess-reply-only")
	st := h.state(sess)
	before := st.Turn

	out, err := h.obs.OnUserPrompt(WithPromptReplyOnly(context.Background()), promptEvent(sess, "hello"))
	require.NoError(t, err)
	require.Equal(t, hookio.Empty(), out, "no warning is pending, so the reply injects nothing")

	require.Empty(t, h.Store.Puts, "the reply path records no verbatim bytes (Option A)")
	require.Empty(t, h.Store.Records, "the reply path writes no index record")
	require.Equal(t, before, h.state(sess).Turn, "the reply path advances no turn")
}

func TestOnUserPrompt_WorkerRecordsAndAdvancesTurn(t *testing.T) {
	h := newHarness(t)
	const sess = core.SessionID("sess-worker")

	// An unleased worker/in-process call (obs=="") records the verbatim capture and closes the turn.
	out, err := h.obs.OnUserPrompt(WithPromptCaptureOnly(context.Background()), promptEvent(sess, "first"))
	require.NoError(t, err)
	require.Equal(t, hookio.Empty(), out, "the worker/replay path emits no host output")

	require.Len(t, h.Store.Puts, 1, "the worker path is the authoritative recorder")
	require.Equal(t, "first", string(h.Store.Puts[0].Body))
	require.Len(t, h.Store.Records, 1)
	require.Equal(t, VerbatimPromptID(sess, 0), h.Store.Records[0].ID, "captured at turn 0")
	require.Equal(t, core.TurnIndex(0), h.Store.Records[0].Turn)
	require.Equal(t, core.TurnIndex(1), h.state(sess).Turn, "the turn advances past the captured prompt")
}

type failingPromptPut struct {
	*store.FSStore
	calls int
}

func (s *failingPromptPut) PutBytes(context.Context, []byte, store.PutOptions) (store.PutResult, error) {
	s.calls++
	return store.PutResult{}, errors.New("injected prompt object write failure")
}

func TestOnUserPrompt_LeasedCaptureFailurePropagates(t *testing.T) {
	r := newRdxRig(t)
	id := r.sidecar(1, "observe.prompt")
	st, ok := r.st.(*store.FSStore)
	require.True(t, ok)
	failure := &failingPromptPut{FSStore: st}
	r.o.opt.Store = failure
	_, err := r.o.OnUserPrompt(WithObservation(context.Background(), id), promptOf("will-not-persist"))
	require.ErrorIs(t, err, ErrUnpublished)
	require.Equal(t, 1, failure.calls, "the failure must come from the object write")
	require.Empty(t, r.index())
	require.Equal(t, core.TurnIndex(0), r.o.session(testSession).Turn)
	require.Equal(t, int64(1), r.counter("observer.err."+stagePromptPut))
}

func TestOnUserPrompt_UnleasedCaptureFailureIsSoftAndAdvances(t *testing.T) {
	h := newHarness(t)
	h.Store.PutErr = errors.New("store closed")
	const sess = core.SessionID("sess-unleased-fail")

	// An unleased in-process caller has no frontier to protect: it keeps the base behaviour — the
	// capture is lost softly and the session still moves on, so later turns are not renumbered.
	_, err := h.obs.OnUserPrompt(context.Background(), promptEvent(sess, "lost"))
	require.NoError(t, err, "an unleased caller has nothing to retry, so the failure stays soft")
	require.Empty(t, h.Store.Records)
	require.Equal(t, core.TurnIndex(1), h.state(sess).Turn, "the turn still advances (base behaviour)")
}

func TestOnUserPrompt_EmptyPromptIsNotATurn(t *testing.T) {
	h := newHarness(t)
	const sess = core.SessionID("sess-empty")
	out, err := h.obs.OnUserPrompt(context.Background(), promptEvent(sess, ""))
	require.NoError(t, err)
	require.Equal(t, hookio.Empty(), out)
	require.Empty(t, h.Store.Puts)
	require.Equal(t, core.TurnIndex(0), h.state(sess).Turn, "an empty prompt advances no turn")
}

func TestOnUserPrompt_WarningDoesNotDependOnWorkerScheduling(t *testing.T) {
	var outputs []Output
	for _, workerFirst := range []bool{false, true} {
		rule := thrashRule(1, 11, "FileRead", "FileEdit", "Bash")
		h := newHarness(t, func(o *Options) { o.Grammar = &fakeGrammar{Thrashing: []grammar.Rule{rule}} })
		h.drive(readOf("toolu_1", "src/a.ts", "alpha\n"))
		worker := func() {
			out, err := h.obs.OnUserPrompt(WithPromptCaptureOnly(context.Background()), promptOf("continue"))
			require.NoError(t, err)
			require.Equal(t, hookio.Empty(), out, "worker must leave warnings for the live reply")
		}
		if workerFirst {
			worker()
		}
		outputs = append(outputs, h.submitReply("continue"))
		if !workerFirst {
			worker()
		}
	}
	require.NotNil(t, outputs[0].HookSpecificOutput)
	require.Equal(t, outputs[0], outputs[1])
}
