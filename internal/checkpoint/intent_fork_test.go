package checkpoint_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// F-UAT06-1 (Phase 4 live lane): `claude --resume <id> --fork-session` starts a NEW session id whose
// SessionStart source is "fork", and whose conversation is the parent's. The fork continues the
// parent's task, so its checkpoints carry the parent's original intent and history, and its own
// prompts — its first included — are evolution entries after them. The host names no parent in any
// hook, so Qompack records, when the fork starts, the session the user was last talking to as the
// parent it continues (NoteFork), and every checkpoint of the fork reads its inherited intent from
// that session's prompt records up to the moment the fork started.

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

	f.noteFork(f.sess)

	l, err := checkpoint.ReadLineage(paths.Of(f.p.Root), f.sess)
	require.NoError(t, err)
	require.NotNil(t, l, "the fork's lineage is recorded when it starts")
	require.Equal(t, f.sess, l.Session)
	require.Equal(t, "fork", l.Source)
	require.Equal(t, core.CheckpointSeq(1), l.ParentSeq, "the parent's newest checkpoint when the fork started")
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
	f.noteFork(f.sess)

	const other = core.SessionID("sess_sp10_other")
	openSegment(f, other, 0)
	promptAs(f, other, 0, "Build the CSV importer.")
	f.precompactAs(other)
	f.noteFork(f.sess)

	l, err := checkpoint.ReadLineage(paths.Of(f.p.Root), f.sess)
	require.NoError(t, err)
	require.Equal(t, core.CheckpointSeq(1), l.ParentSeq)
	require.Equal(t, forkParent, l.ParentSession)
}

// TestNoteForkWithNoCheckpointRecordsNoParent: a fork in a project where no other session has said
// anything and nothing has compacted. Nothing names its parent, and the record says so rather than
// guessing.
func TestNoteForkWithNoCheckpointRecordsNoParent(t *testing.T) {
	f := newFx(t)
	f.noteFork(f.sess)

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
	f.noteFork(f.sess)

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
	f.noteFork(f.sess)
	openSegment(f, f.sess, 0)
	promptAs(f, f.sess, 0, forkFirstPrompt)
	f.precompactAs(f.sess)
	require.NoError(t, f.w.Abort(f.w.DraftFor(f.sess)))

	const grandchild = core.SessionID("sess_sp10_grandchild")
	f.noteFork(grandchild)
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

// ── review round (wave 13 fix seat) ─────────────────────────────────────────────────────────────
//
// A fork's conversation is its parent's as it stood when the fork started, not as it stood at the
// parent's last compaction. These rows pin the inheritance to what the parent actually said before
// the fork, read from its own prompt records, and the parent to the session the user was last
// talking to — whether or not it ever compacted.

// noteFork records s as a fork starting now, the way the daemon's session.start route does: with
// the host's stamp on the fork's SessionStart, after every prompt the fixture has made so far, and
// with the writer's sources published, as the daemon's wiring publishes them before any hook
// (daemon.BindCheckpoint).
func (f *fx) noteFork(s core.SessionID) {
	f.t.Helper()
	require.NoError(f.t, f.w.SetSources(f.src))
	require.NoError(f.t, f.w.NoteFork(f.ctx(), s, f.now()))
}

// lineageOf reads s's lineage record back.
func (f *fx) lineageOf(s core.SessionID) *checkpoint.Lineage {
	f.t.Helper()
	l, err := checkpoint.ReadLineage(paths.Of(f.p.Root), s)
	require.NoError(f.t, err)
	require.NotNil(f.t, l)
	return l
}

// TestForkCarriesTheParentsCorrectionsAfterItsLastCheckpoint: the parent corrects itself after its
// last compaction, and the user then forks it. The fork's conversation holds that correction, so
// the fork's checkpoint must too; inheriting the parent's last SEALED checkpoint instead brought
// back the superseded requirement on the fork path (the F-UAT06-2 class).
func TestForkCarriesTheParentsCorrectionsAfterItsLastCheckpoint(t *testing.T) {
	f := newFx(t)
	sealParent(t, f)
	promptAs(f, forkParent, 4, rateCorrection75)

	f.noteFork(f.sess)
	openSegment(f, f.sess, 0)
	promptAs(f, f.sess, 0, forkFirstPrompt)
	cp := sealed(t, f, f.precompactAs(f.sess))

	require.Equal(t, rateAsk, cp.UserIntent.Original)
	require.Equal(t, []string{rateCorrection60, rateCorrection75, forkFirstPrompt}, cp.UserIntent.Evolution,
		"the parent's correction after its last checkpoint is part of the conversation the fork continues")
}

// TestForkOfANeverCompactedParentInheritsItsIntent: the parent never compacted, and an older,
// unrelated session did. The fork continues the session the user was last talking to, not the one
// that happened to seal the project's newest checkpoint.
func TestForkOfANeverCompactedParentInheritsItsIntent(t *testing.T) {
	f := newFx(t)
	const other = core.SessionID("sess_sp10_other")
	openSegment(f, other, 0)
	promptAs(f, other, 0, "Build the CSV importer.")
	f.precompactAs(other)
	require.NoError(t, f.w.Abort(f.w.DraftFor(other)))

	openSegment(f, forkParent, 0)
	promptAs(f, forkParent, 0, rateAsk)
	promptAs(f, forkParent, 2, rateCorrection60)

	f.noteFork(f.sess)
	l := f.lineageOf(f.sess)
	require.Equal(t, forkParent, l.ParentSession, "the session the user was last talking to")
	require.Equal(t, forkParent, l.OriginSession)
	require.Zero(t, l.ParentSeq, "the parent never compacted, so it has no checkpoint to name")

	openSegment(f, f.sess, 0)
	promptAs(f, f.sess, 0, forkFirstPrompt)
	cp := sealed(t, f, f.precompactAs(f.sess))
	require.Equal(t, rateAsk, cp.UserIntent.Original)
	require.Equal(t, []string{rateCorrection60, forkFirstPrompt}, cp.UserIntent.Evolution)
}

// TestForkInheritsOnlyWhatItsParentSaidBeforeTheFork: the parent carries on in its own terminal
// after the fork started. What it says then is not part of the fork's conversation.
func TestForkInheritsOnlyWhatItsParentSaidBeforeTheFork(t *testing.T) {
	f := newFx(t)
	openSegment(f, forkParent, 0)
	promptAs(f, forkParent, 0, rateAsk)
	f.noteFork(f.sess)
	promptAs(f, forkParent, 2, "Unrelated: also add request logging to the gateway.")

	openSegment(f, f.sess, 0)
	promptAs(f, f.sess, 0, forkFirstPrompt)
	cp := sealed(t, f, f.precompactAs(f.sess))
	require.Equal(t, rateAsk, cp.UserIntent.Original)
	require.Equal(t, []string{forkFirstPrompt}, cp.UserIntent.Evolution,
		"the parent's prompt after the fork started belongs to the parent alone")
}

// TestForkWithAnUnreadableLineageKeepsTheIntentItInherited: the fork's lineage record is damaged
// after the fork sealed a checkpoint. The fork's own chain still carries what it inherited, and a
// refresh must not replace that original with the fork's own first prompt.
func TestForkWithAnUnreadableLineageKeepsTheIntentItInherited(t *testing.T) {
	f := newFx(t)
	sealParent(t, f)
	f.noteFork(f.sess)
	openSegment(f, f.sess, 0)
	promptAs(f, f.sess, 0, forkFirstPrompt)
	first := sealed(t, f, f.precompactAs(f.sess))
	require.Equal(t, rateAsk, first.UserIntent.Original, "fixture sanity")
	require.NoError(t, f.w.Abort(f.w.DraftFor(f.sess)), "the fork's session ends")

	p := filepath.Join(paths.Of(f.p.Root).State, "lineage-"+string(f.sess)+".json")
	require.NoError(t, os.WriteFile(paths.Long(p), []byte("{not json"), 0o600))

	promptAs(f, f.sess, 3, rateCorrection75)
	second := sealed(t, f, f.precompactAs(f.sess))
	require.Equal(t, rateAsk, second.UserIntent.Original,
		"the fork's own chain is the only copy of what it inherited")
	require.Equal(t, []string{rateCorrection60, forkFirstPrompt, rateCorrection75}, second.UserIntent.Evolution)
}

// capabilityless hides every optional capability of the store it wraps, the way a store other than
// the file store would present itself.
type capabilityless struct{ store.Store }

// TestForkWithoutPromptEnumerationUsesTheParentsCheckpoint: a store that can neither say who spoke
// last nor enumerate a session's prompts. The parent falls back to the session that sealed the
// project's newest checkpoint, and the fork inherits that checkpoint's copy of the intent.
func TestForkWithoutPromptEnumerationUsesTheParentsCheckpoint(t *testing.T) {
	f := newFx(t)
	sealParent(t, f)
	src := f.src
	src.Store = capabilityless{f.store}
	require.NoError(t, f.w.SetSources(src))
	require.NoError(t, f.w.NoteFork(f.ctx(), f.sess, f.now()))

	l := f.lineageOf(f.sess)
	require.Equal(t, forkParent, l.ParentSession, "the session that sealed the newest checkpoint")
	require.Equal(t, core.CheckpointSeq(1), l.ParentSeq)

	_, err := f.w.Begin(f.ctx(), f.sess, 0, src)
	require.NoError(t, err)
	_, cp := f.persisted()
	require.Equal(t, rateAsk, cp.UserIntent.Original, "the parent checkpoint's copy")
	require.Equal(t, []string{rateCorrection60}, cp.UserIntent.Evolution)
}
