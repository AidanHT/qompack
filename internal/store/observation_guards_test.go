package store

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

func TestObservationGuards_ExactSupersedingReplayRemainsIdempotent(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	oldRoot := putRoot(t, tp, "old")
	root := putRoot(t, tp, "replacement")
	old := core.ToolUseID("toolu_old_for_repeat")
	require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{ID: old, Root: oldRoot, Session: "sess-obs", Tool: "Read"}))
	rec := recWithObs("toolu_same_intent", root, obsID(t, 601))
	_, recorded, err := tp.Store.RecordToolUseSuperseding(ctx, rec, []core.ToolUseID{old})
	require.NoError(t, err)
	require.True(t, recorded)
	before, err := os.ReadFile(obsPath(tp.Root))
	require.NoError(t, err)
	marked, recorded, err := tp.Store.RecordToolUseSuperseding(ctx, rec, []core.ToolUseID{old})
	require.NoError(t, err, "original target is now superseded, but the request is unchanged")
	require.False(t, recorded)
	require.Empty(t, marked)
	after, err := os.ReadFile(obsPath(tp.Root))
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestObservationGuards_UnterminatedIntentIsNotAppendable(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	rec := recWithObs("toolu_missing_newline", putRoot(t, tp, "complete payload"), obsID(t, 602))
	require.NoError(t, tp.Store.RecordToolUse(ctx, rec))
	require.NoError(t, tp.Store.Close())
	raw, err := os.ReadFile(obsPath(tp.Root))
	require.NoError(t, err)
	require.Equal(t, byte('\n'), raw[len(raw)-1])
	torn := raw[:len(raw)-1]
	require.NoError(t, os.WriteFile(obsPath(tp.Root), torn, 0o600))
	reopened := openOver(t, tp.project)
	_, err = reopened.Store.RecoverToolUseByObservation(ctx, rec.Observation)
	require.ErrorIs(t, err, core.ErrDegraded)
	newer := rec
	newer.ID = "toolu_no_append"
	newer.Observation = obsID(t, 603)
	require.ErrorIs(t, reopened.Store.RecordToolUse(ctx, newer), core.ErrDegraded)
	after, err := os.ReadFile(obsPath(tp.Root))
	require.NoError(t, err)
	require.Equal(t, torn, after, "do not merge a new JSON record into an unterminated one")
}

func TestObservationGuards_CommittedRecoveryVerifiesOriginalObjects(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	rec := recWithObs("toolu_missing_object", putRoot(t, tp, "original payload"), obsID(t, 604))
	require.NoError(t, tp.Store.RecordToolUse(ctx, rec))
	root, err := tp.Store.GetRoot(ctx, rec.Root)
	require.NoError(t, err)
	require.NotEmpty(t, root.Chunks)
	require.NoError(t, tp.Store.Close())
	require.NoError(t, os.Remove(tp.Store.objectPath(root.Chunks[0].Hash)))
	reopened := openOver(t, tp.project)
	_, err = reopened.Store.RecoverToolUseByObservation(ctx, rec.Observation)
	require.ErrorIs(t, err, core.ErrDegraded, "a complete index does not prove the payload survived")
}

func TestObservationGuards_CloseWaitsForIntentPublication(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	rec := recWithObs("toolu_close_barrier", putRoot(t, tp, "close barrier"), obsID(t, 605))
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	tp.Store.obsSyncData = func(f *os.File) error { close(started); <-release; return f.Sync() }
	writeDone := make(chan error, 1)
	go func() { writeDone <- tp.Store.ReserveObservation(ctx, rec.Observation, rec, nil) }()
	<-started
	closeDone := make(chan error, 1)
	go func() { closeDone <- tp.Store.Close() }()
	closedEarly := false
	select {
	case <-closeDone:
		closedEarly = true
	case <-time.After(50 * time.Millisecond):
	}
	unblock()
	writeErr := <-writeDone
	if !closedEarly {
		require.NoError(t, <-closeDone)
	}
	require.False(t, closedEarly, "Close must not overtake the sidecar's fsync")
	require.NoError(t, writeErr)
	before, err := os.ReadFile(obsPath(tp.Root))
	require.NoError(t, err)
	require.ErrorIs(t, tp.Store.ReserveObservation(ctx, obsID(t, 606), rec, nil), core.ErrDegraded)
	after, err := os.ReadFile(obsPath(tp.Root))
	require.NoError(t, err)
	require.Equal(t, before, after)
}
