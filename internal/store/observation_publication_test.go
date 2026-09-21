package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

func obsPath(root string) string { return filepath.Join(paths.Of(root).Index, observationsFile) }

func appendObsLine(t *testing.T, root, line string) {
	t.Helper()
	f, err := os.OpenFile(paths.Long(obsPath(root)), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	require.NoError(t, err)
	_, werr := f.WriteString(line + "\n")
	require.NoError(t, werr)
	require.NoError(t, f.Close())
}

// TestObservationPublish_WiredReopenRecommits: the ordinary observation-bearing write commits, and a
// reopen re-derives the committed binding from the sidecar plus the tool_use index.
func TestObservationPublish_WiredReopenRecommits(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root := putRoot(t, tp, "reopen content")
	obs := obsID(t, 1)
	const id = core.ToolUseID("toolu_01OBSREOPENAAAAAAAAAAA")
	require.NoError(t, tp.Store.RecordToolUse(ctx, recWithObs(id, root, obs)))
	require.FileExists(t, paths.Long(obsPath(tp.Root)))

	got, err := tp.Store.ToolUseByObservation(ctx, obs)
	require.NoError(t, err)
	require.Equal(t, id, got.ID)

	require.NoError(t, tp.Store.Close())
	reopened := openOver(t, tp.project)
	got, err = reopened.Store.ToolUseByObservation(ctx, obs)
	require.NoError(t, err, "reopen must recover the committed binding")
	require.Equal(t, id, got.ID)
}

// TestObservationPublish_ActualPartialBatchCut is the REAL record-without-all-marks cut: the intent and
// record are persisted and one selected mark is present but another is not. Recovery completes only the
// missing mark, preserves a later conflicting supersession, and never returns success while incomplete.
func TestObservationPublish_ActualPartialBatchCut(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	rRoot := putRoot(t, tp, "record R content")
	t1Root := putRoot(t, tp, "t1 content")
	t2Root := putRoot(t, tp, "t2 content")
	laterRoot := putRoot(t, tp, "later content")

	const rID = core.ToolUseID("toolu_01OBSPARTIALRAAAAAAAAA")
	const t1 = core.ToolUseID("toolu_01OBSPARTIALT1AAAAAAAA")
	const t2 = core.ToolUseID("toolu_01OBSPARTIALT2AAAAAAAA")
	const later = core.ToolUseID("toolu_01OBSPARTIALLTAAAAAAA")
	obs := obsID(t, 7)

	// Pre-existing supersede targets and a later record.
	require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{ID: t1, Session: "sess-obs", Turn: 1, TS: 1, Tool: "FileRead", Root: t1Root, Path: "src/t1.ts"}))
	require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{ID: t2, Session: "sess-obs", Turn: 2, TS: 2, Tool: "FileRead", Root: t2Root, Path: "src/t2.ts"}))
	require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{ID: later, Session: "sess-obs", Turn: 3, TS: 3, Tool: "FileRead", Root: laterRoot, Path: "src/later.ts"}))
	// t1 is already superseded by a LATER, unrelated record — recovery must preserve that.
	require.NoError(t, tp.Store.MarkSuperseded(ctx, t1, later))

	// The cut: reserve the intent (durable), then persist the record and ONE of its two marks.
	rRec := recWithObs(rID, rRoot, obs)
	require.NoError(t, tp.Store.ReserveObservation(ctx, obs, rRec, []core.ToolUseID{t1, t2}))
	plainR := rRec
	plainR.Observation = ""
	require.NoError(t, tp.Store.RecordToolUse(ctx, plainR)) // record persisted, no marks yet

	// Before recovery: pending → unavailable, never the record.
	_, err := tp.Store.ToolUseByObservation(ctx, obs)
	require.ErrorIs(t, err, core.ErrDegraded)

	// Recover completes the missing t2 mark, leaves t1's later supersession intact, and commits.
	got, err := tp.Store.RecoverToolUseByObservation(ctx, obs)
	require.NoError(t, err)
	require.Equal(t, rID, got.ID)

	t2rec, err := tp.Store.ToolUse(ctx, t2)
	require.NoError(t, err)
	require.Equal(t, StatusSuperseded, t2rec.Status)
	require.Equal(t, rID, t2rec.SupersededBy, "the missing mark is completed by recovery")

	t1rec, err := tp.Store.ToolUse(ctx, t1)
	require.NoError(t, err)
	require.Equal(t, later, t1rec.SupersededBy, "a later supersession must not be overwritten")

	got, err = tp.Store.ToolUseByObservation(ctx, obs)
	require.NoError(t, err)
	require.Equal(t, rID, got.ID)
}

// TestObservationPublish_MissingObjectsKeepIntentIncomplete: recovery of an intent whose record's
// original root is not durable must NOT fabricate a publication — it stays incomplete and unavailable.
func TestObservationPublish_MissingObjectsKeepIntentIncomplete(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	obs := obsID(t, 3)
	const id = core.ToolUseID("toolu_01OBSMISSINGAAAAAAAAAA")
	missingRoot := core.HashBytes(core.DomainChunk, []byte("never-put-root"))

	// Reserve is allowed (it only writes the intent); recovery must then refuse.
	require.NoError(t, tp.Store.ReserveObservation(ctx, obs, recWithObs(id, missingRoot, obs), nil))

	_, err := tp.Store.RecoverToolUseByObservation(ctx, obs)
	require.ErrorIs(t, err, core.ErrDegraded, "a missing original root cannot be published")
	_, err = tp.Store.ToolUse(ctx, id)
	require.ErrorIs(t, err, core.ErrNotFound, "no legacy record may be fabricated")
	_, err = tp.Store.ToolUseByObservation(ctx, obs)
	require.ErrorIs(t, err, core.ErrDegraded, "the intent stays pending, unavailable")
}

// TestObservationPublish_UncertainSidecarBlocksUnknownButKeepsCommitted: a malformed sidecar line makes
// completeness unprovable, so an UNKNOWN observation and a new reservation are unavailable, while an
// already-committed binding is preserved.
func TestObservationPublish_UncertainSidecarBlocksUnknownButKeepsCommitted(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root := putRoot(t, tp, "good content")
	good := obsID(t, 1)
	const goodID = core.ToolUseID("toolu_01OBSGOODAAAAAAAAAAAA")
	require.NoError(t, tp.Store.RecordToolUse(ctx, recWithObs(goodID, root, good)))
	require.NoError(t, tp.Store.Close())

	// A line this build cannot read canonically (unknown version), then reopen.
	appendObsLine(t, tp.Root, `{"v":999,"obs":"`+string(obsID(t, 2))+`","rec":{"v":1,"id":"toolu_01OBSFUTUREAAAAAAAAAA"}}`)
	reopened := openOver(t, tp.project)

	// The committed binding survives.
	got, err := reopened.Store.ToolUseByObservation(ctx, good)
	require.NoError(t, err)
	require.Equal(t, goodID, got.ID)
	// The attributable bad line's observation is unavailable, not absent.
	_, err = reopened.Store.ToolUseByObservation(ctx, obsID(t, 2))
	require.ErrorIs(t, err, core.ErrDegraded)
	// An UNKNOWN observation is unavailable while completeness is unproved (never false absence).
	_, err = reopened.Store.ToolUseByObservation(ctx, obsID(t, 42))
	require.ErrorIs(t, err, core.ErrDegraded)
	// A new reservation is refused while the sidecar is uncertain.
	err = reopened.Store.ReserveObservation(ctx, obsID(t, 43), recWithObs("toolu_01OBSNEWAAAAAAAAAAAA", root, obsID(t, 43)), nil)
	require.ErrorIs(t, err, core.ErrDegraded)
}

// TestObservationPublish_IdentityConflictAndIdempotence: a reserved id may not be landed under
// different immutable metadata by any writer; re-reserving the exact intent is idempotent; a distinct
// observation over the SAME compatible record is allowed, but incompatible metadata is refused.
func TestObservationPublish_IdentityConflictAndIdempotence(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root1 := putRoot(t, tp, "identity root 1")
	root2 := putRoot(t, tp, "identity root 2")
	const id = core.ToolUseID("toolu_01OBSIDENTITYAAAAAAAA")
	obs1 := obsID(t, 1)

	require.NoError(t, tp.Store.ReserveObservation(ctx, obs1, recWithObs(id, root1, obs1), nil))

	// A no-observation writer cannot land the reserved id under a different root.
	err := tp.Store.RecordToolUse(ctx, ToolUseRecord{ID: id, Session: "sess-obs", Turn: 1, TS: 1, Tool: "FileRead", Root: root2, Path: "src/x.ts"})
	require.ErrorIs(t, err, core.ErrAppendOnly, "a foreign writer cannot steal a reserved id under different metadata")

	// Re-reserving the exact same intent is idempotent (writes nothing, no error).
	require.NoError(t, tp.Store.ReserveObservation(ctx, obs1, recWithObs(id, root1, obs1), nil))

	// Rebinding the observation to a different record identity is refused.
	err = tp.Store.ReserveObservation(ctx, obs1, recWithObs(id, root2, obs1), nil)
	require.ErrorIs(t, err, core.ErrAppendOnly)

	// A DISTINCT observation over the same compatible record (same id + identity) is allowed.
	require.NoError(t, tp.Store.ReserveObservation(ctx, obsID(t, 2), recWithObs(id, root1, obsID(t, 2)), nil))

	// A distinct observation naming the same id under DIFFERENT session metadata is refused.
	err = tp.Store.ReserveObservation(ctx, obsID(t, 3), ToolUseRecord{
		ID: id, Session: "other-session", Turn: 1, TS: 1, Tool: "FileRead", Root: root1, Path: "src/x.ts", Observation: obsID(t, 3),
	}, nil)
	require.ErrorIs(t, err, core.ErrAppendOnly, "incompatible immutable metadata for a reserved id is refused")
}

// TestObservationPublish_NonRegularSidecarRefused: a directory (or symlink) where the sidecar should be
// is refused before any write-open, so a planted alias cannot redirect the write.
func TestObservationPublish_NonRegularSidecarRefused(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	require.NoError(t, os.MkdirAll(paths.Long(obsPath(tp.Root)), 0o700)) // a directory at the sidecar path
	root := putRoot(t, tp, "content")
	err := tp.Store.ReserveObservation(ctx, obsID(t, 1), recWithObs("toolu_01OBSNONREGAAAAAAAAAA", root, obsID(t, 1)), nil)
	require.ErrorIs(t, err, core.ErrDegraded, "a non-regular sidecar path must be refused before opening for write")
}
