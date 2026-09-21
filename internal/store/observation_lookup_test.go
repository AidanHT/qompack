package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// Shared helpers for the observation tests: canonical observation ids (core.NewObservationID) and REAL
// roots (PutBytes), because SyncPublication verifies the record's objects before publication.

func obsID(t *testing.T, arrival uint64) core.ObservationID {
	t.Helper()
	id, err := core.NewObservationID("sess-obs", arrival)
	require.NoError(t, err)
	return id
}

func putRoot(t *testing.T, tp *testProject, body string) core.Hash {
	t.Helper()
	res, err := tp.Store.PutBytes(context.Background(), []byte(body), PutOptions{Tool: "FileRead", Path: "src/x.ts"})
	require.NoError(t, err)
	require.NoError(t, tp.Store.Flush(context.Background()))
	return res.Root.Hash
}

func recWithObs(id core.ToolUseID, root core.Hash, obs core.ObservationID) ToolUseRecord {
	return ToolUseRecord{
		ID: id, Session: "sess-obs", Turn: 1, TS: 1, Tool: "FileRead",
		Root: root, Path: "src/x.ts", Bytes: 5, Tokens: 2, Observation: obs,
	}
}

// TestObservationLookup_WiredPublishCommits proves the wired path: an ordinary RecordToolUse with a
// nonempty Observation creates the committed binding (no separate Reserve call).
func TestObservationLookup_WiredPublishCommits(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root := putRoot(t, tp, "wired publish content")
	obs := obsID(t, 1)
	const id = core.ToolUseID("toolu_01OBSWIREDAAAAAAAAAAAA")

	require.NoError(t, tp.Store.RecordToolUse(ctx, recWithObs(id, root, obs)))

	got, err := tp.Store.ToolUseByObservation(ctx, obs)
	require.NoError(t, err, "the ordinary write must have created the committed binding")
	require.Equal(t, id, got.ID)
	// The legacy record is durable and resolvable by id too.
	got, err = tp.Store.ToolUse(ctx, id)
	require.NoError(t, err)
	require.Equal(t, root, got.Root)
}

func TestObservationLookup_NoBindingIsNotFound(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root := putRoot(t, tp, "legacy content")
	require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
		ID: "toolu_01OBSLEGACYAAAAAAAAAAAA", Session: "sess-obs", Turn: 1, TS: 1, Tool: "FileRead", Root: root, Path: "src/a.ts",
	}))
	_, err := tp.Store.ToolUseByObservation(ctx, obsID(t, 99))
	require.ErrorIs(t, err, core.ErrNotFound, "a legacy record with no intent does not resolve by observation")
}

func TestObservationLookup_EmptyAndInvalidIDAreNotFound(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	_, err := tp.Store.ToolUseByObservation(ctx, "")
	require.ErrorIs(t, err, core.ErrNotFound)
	// A noncanonical id (not a sha256 hash) is refused as a miss, not accepted as a weaker key.
	_, err = tp.Store.ToolUseByObservation(ctx, "sha256:o1")
	require.ErrorIs(t, err, core.ErrNotFound, "a noncanonical observation id is refused")
}

func TestObservationLookup_ClosedStoreDegrades(t *testing.T) {
	tp := newTestStore(t)
	require.NoError(t, tp.Store.Close())
	_, err := tp.Store.ToolUseByObservation(context.Background(), obsID(t, 1))
	require.ErrorIs(t, err, core.ErrDegraded)
}

func TestObservationLookup_CancelledContext(t *testing.T) {
	tp := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tp.Store.ToolUseByObservation(ctx, obsID(t, 1))
	require.ErrorIs(t, err, context.Canceled)
}

// TestObservationLookup_PendingIntentIsUnavailable: a reserved-but-unpublished intent is unavailable
// (degraded), never a false absence and never the unpublished record.
func TestObservationLookup_PendingIntentIsUnavailable(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root := putRoot(t, tp, "pending content")
	obs := obsID(t, 5)
	require.NoError(t, tp.Store.ReserveObservation(ctx, obs, recWithObs("toolu_01OBSPENDINGAAAAAAAAAA", root, obs), nil))
	_, err := tp.Store.ToolUseByObservation(ctx, obs)
	require.ErrorIs(t, err, core.ErrDegraded, "a reserved-but-unpublished intent is unavailable")
	_, err = tp.Store.ToolUse(ctx, "toolu_01OBSPENDINGAAAAAAAAAA")
	require.ErrorIs(t, err, core.ErrNotFound, "reserve must not have appended the legacy record")
}
