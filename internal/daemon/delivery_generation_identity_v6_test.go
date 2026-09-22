package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

func TestDeliveryGeneration_V6_ImmutableNonceAndArrival(t *testing.T) {
	g := newTestGenerations(t)
	ctx := context.Background()
	original := mkGenLease(t, genNonce(510), "original-session", 1)
	_, err := g.commit(ctx, []deliveryLease{original})
	require.NoError(t, err)
	root := g.currentRoot()
	for _, conflict := range []deliveryLease{
		mkGenLease(t, original.Delivery, original.Session, 2),
		mkGenLease(t, genNonce(511), original.Session, original.ArrivalSeq),
	} {
		_, err := g.commit(ctx, []deliveryLease{conflict})
		require.ErrorIs(t, err, errGenerationConflict)
		require.Equal(t, root, g.currentRoot(), "failed joins cannot publish a changed identity root")
	}
	got, found, err := g.resolveLease(ctx, original.Delivery)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, original, got)
	badACK := deliveryAck{Version: core.EvidenceVersion + 1, Delivery: original.Delivery, ObservationID: original.ObservationID}
	require.Error(t, g.commitAck(ctx, []deliveryAck{badACK}))
	badTerminal := terminalFor(original)
	badTerminal.Outcome = "captured"
	require.Error(t, g.commitTerminal(ctx, []deliveryTerminal{badTerminal}))
	require.Equal(t, root, g.currentRoot())
	ack := deliveryAck{Version: core.EvidenceVersion, Delivery: original.Delivery, ObservationID: original.ObservationID}
	require.NoError(t, g.commitAck(ctx, []deliveryAck{ack}))
	ack.Root = testDeliveryRequest("different-ack-root")
	require.ErrorIs(t, g.commitAck(ctx, []deliveryAck{ack}), errGenerationConflict)
}

func TestDeliveryGeneration_V6_LogCapacityRefusesBeforeAppend(t *testing.T) {
	g := newTestGenerations(t)
	before, err := os.ReadFile(g.logPath)
	require.NoError(t, err)
	g.logBytes = genMaxLog
	_, err = g.commit(context.Background(), []deliveryLease{mkGenLease(t, genNonce(512), "bounded", 1)})
	require.Error(t, err)
	after, err := os.ReadFile(g.logPath)
	require.NoError(t, err)
	require.Equal(t, before, after, "a format bound must refuse before writing an unreadable log")
}

func TestDeliveryRollover_V6_DisabledMechanismCannotReopenLegacy(t *testing.T) {
	setRollover(t, 1)
	root := t.TempDir()
	j := openRolloverJournal(t, root)
	_, err := j.lease(context.Background(), genNonce(513), "disabled", testDeliveryRequest(genNonce(513)))
	require.NoError(t, err)
	require.NoError(t, j.owner.Release())
	enableDeliveryGenerations = false
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	_, err = lock.openDeliveryJournal()
	require.Error(t, err, "existing migration evidence must not be interpreted by a disabled legacy writer")
}

func TestDeliveryRollover_V6_BaseReadFailureIsNotFirstArrival(t *testing.T) {
	j := newDeliveryJournal(nil, "")
	j.segment = 1
	_, _, err := j.segmentArrivalBase("new-session")
	require.Error(t, err, "an unavailable predecessor root is not a proven new session")
}

func TestDeliveryRollover_V6_MissingActiveAcknowledgementsAreNotRecreated(t *testing.T) {
	setRollover(t, 1)
	root := t.TempDir()
	j := openRolloverJournal(t, root)
	for i := 0; i < 2; i++ {
		_, err := j.lease(context.Background(), genNonce(i), "missing-acks", testDeliveryRequest(genNonce(i)))
		require.NoError(t, err)
	}
	require.Equal(t, uint64(1), j.segment)
	dir := filepath.Dir(j.path)
	require.NoError(t, j.owner.Release())
	for _, name := range []string{deliveryAckFile, deliveryAckPositionFile} {
		require.NoError(t, os.Remove(filepath.Join(dir, name)))
	}
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	_, err = lock.openDeliveryJournal()
	require.Error(t, err)
	for _, name := range []string{deliveryAckFile, deliveryAckPositionFile} {
		_, err := os.Lstat(filepath.Join(dir, name))
		require.True(t, os.IsNotExist(err), "lost evidence must not be recreated as an empty frontier")
	}
}
