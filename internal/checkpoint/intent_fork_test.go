package checkpoint_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// F-UAT06-1 (Phase 4 live lane): `claude --resume <id> --fork-session` starts a NEW session id whose
// SessionStart source is "fork", and whose conversation is the parent's. The fork continues the
// parent's task, so its checkpoints carry the parent's original intent and history, and its own
// prompts — its first included — are evolution entries after them. The host names no parent in any
// hook, so Qompack records, when the fork starts, the project's newest checkpoint as the parent it
// continues (NoteFork), and every checkpoint of the fork reads its inherited intent from there.

// The UAT-06 conversation, verbatim in shape.
const (
	rateAsk = "We are building a rate limiter for the Kite API gateway. Requirement: allow 100 " +
		"requests per minute per client."
	rateCorrection60 = "Correction: the limit must be 60 requests per minute per client, not 100. The 100 " +
		"figure is superseded."
	forkFirstPrompt  = "We are continuing in a forked session. In one sentence: what is the current per-client rate limit requirement?"
	rateCorrection75 = "Correction: the limit must now be 75 requests per minute per client. The 60 figure is superseded as well."
)

const forkParent = core.SessionID("sess_sp10_parent")

// sealParent runs the parent session to one compaction and ends it: checkpoint 1 carries its
// original and its first correction.
func sealParent(t *testing.T, f *fx) {
	t.Helper()
	openSegment(f, forkParent, 0)
	promptAs(f, forkParent, 0, rateAsk)
	promptAs(f, forkParent, 2, rateCorrection60)
	cp := sealed(t, f, f.precompactAs(forkParent))
	require.Equal(t, rateAsk, cp.UserIntent.Original, "fixture sanity")
	require.Equal(t, []string{rateCorrection60}, cp.UserIntent.Evolution, "fixture sanity")
	require.NoError(t, f.w.Abort(f.w.DraftFor(forkParent)), "the parent session ends")
}

func TestNoteForkRecordsTheCheckpointTheForkContinues(t *testing.T) {
	f := newFx(t)
	sealParent(t, f)

	require.NoError(t, f.w.NoteFork(f.ctx(), f.sess))

	l, err := checkpoint.ReadLineage(paths.Of(f.p.Root), f.sess)
	require.NoError(t, err)
	require.NotNil(t, l, "the fork's lineage is recorded when it starts")
	require.Equal(t, f.sess, l.Session)
	require.Equal(t, "fork", l.Source)
	require.Equal(t, core.CheckpointSeq(1), l.ParentSeq, "the project's newest checkpoint when the fork started")
	require.Equal(t, forkParent, l.ParentSession)
	require.Equal(t, forkParent, l.OriginSession, "whose first prompt the inherited original is")

	none, err := checkpoint.ReadLineage(paths.Of(f.p.Root), forkParent)
	require.NoError(t, err)
	require.Nil(t, none, "a session that was not forked has no lineage record")
}

// TestNoteForkIsWrittenOnce: the record names the parent as it stood when the fork started. A
// second SessionStart for the same fork — a replay from a hook spool — must not re-point it at a
// checkpoint sealed since.
func TestNoteForkIsWrittenOnce(t *testing.T) {
	f := newFx(t)
	sealParent(t, f)
	require.NoError(t, f.w.NoteFork(f.ctx(), f.sess))

	const other = core.SessionID("sess_sp10_other")
	openSegment(f, other, 0)
	promptAs(f, other, 0, "Build the CSV importer.")
	f.precompactAs(other)
	require.NoError(t, f.w.NoteFork(f.ctx(), f.sess))

	l, err := checkpoint.ReadLineage(paths.Of(f.p.Root), f.sess)
	require.NoError(t, err)
	require.Equal(t, core.CheckpointSeq(1), l.ParentSeq)
	require.Equal(t, forkParent, l.ParentSession)
}

// TestNoteForkWithNoCheckpointRecordsNoParent: a fork of a session that never compacted, in a
// project with no checkpoint at all. Nothing names its parent, and the record says so rather than
// guessing.
func TestNoteForkWithNoCheckpointRecordsNoParent(t *testing.T) {
	f := newFx(t)
	require.NoError(t, f.w.NoteFork(f.ctx(), f.sess))

	l, err := checkpoint.ReadLineage(paths.Of(f.p.Root), f.sess)
	require.NoError(t, err)
	require.NotNil(t, l)
	require.Zero(t, l.ParentSeq)
	require.Empty(t, l.ParentSession)
	require.Empty(t, l.OriginSession)
}

// TestForkCarriesTheParentsIntentThenItsOwnPrompts is F-UAT06-1's checkpoint half: the fork's
// checkpoints keep the parent's original and correction, and the fork's own first prompt is the
// next evolution entry rather than a new original — across two compactions of the fork.
func TestForkCarriesTheParentsIntentThenItsOwnPrompts(t *testing.T) {
	f := newFx(t)
	sealParent(t, f)
	require.NoError(t, f.w.NoteFork(f.ctx(), f.sess))

	openSegment(f, f.sess, 0)
	promptAs(f, f.sess, 0, forkFirstPrompt)
	first := sealed(t, f, f.precompactAs(f.sess))

	promptAs(f, f.sess, 3, rateCorrection75)
	second := sealed(t, f, f.precompactAs(f.sess))

	require.Equal(t, rateAsk, first.UserIntent.Original, "a fork continues its parent's task")
	require.Equal(t, []string{rateCorrection60, forkFirstPrompt}, first.UserIntent.Evolution)
	require.Equal(t, rateAsk, second.UserIntent.Original)
	require.Equal(t, []string{rateCorrection60, forkFirstPrompt, rateCorrection75}, second.UserIntent.Evolution,
		"oldest first: the parent's history, then the fork's own prompts in order")
}

// TestForkOfAForkKeepsTheFirstOrigin: forking the fork continues the same task, whose original is
// still the first session's first prompt.
func TestForkOfAForkKeepsTheFirstOrigin(t *testing.T) {
	f := newFx(t)
	sealParent(t, f)
	require.NoError(t, f.w.NoteFork(f.ctx(), f.sess))
	openSegment(f, f.sess, 0)
	promptAs(f, f.sess, 0, forkFirstPrompt)
	f.precompactAs(f.sess)
	require.NoError(t, f.w.Abort(f.w.DraftFor(f.sess)))

	const grandchild = core.SessionID("sess_sp10_grandchild")
	require.NoError(t, f.w.NoteFork(f.ctx(), grandchild))
	l, err := checkpoint.ReadLineage(paths.Of(f.p.Root), grandchild)
	require.NoError(t, err)
	require.Equal(t, f.sess, l.ParentSession)
	require.Equal(t, forkParent, l.OriginSession, "the original is still the first session's first prompt")

	openSegment(f, grandchild, 0)
	promptAs(f, grandchild, 0, rateCorrection75)
	cp := sealed(t, f, f.precompactAs(grandchild))
	require.Equal(t, rateAsk, cp.UserIntent.Original)
	require.Equal(t, []string{rateCorrection60, forkFirstPrompt, rateCorrection75}, cp.UserIntent.Evolution)
}
