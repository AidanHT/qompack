package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

func TestDeliveryTerminal_ArchivedDenialSettlesOrderingWithoutCaptureAck(t *testing.T) {
	roll := parallelRollover(t, 1)
	root := t.TempDir()
	j := roll.open(t, root)
	ctx := context.Background()
	const session core.SessionID = "archived-denial"
	first, err := j.lease(ctx, genNonce(301), session, testDeliveryRequest(genNonce(301)))
	require.NoError(t, err)
	second, err := j.lease(ctx, genNonce(302), session, testDeliveryRequest(genNonce(302)))
	require.NoError(t, err)
	require.Greater(t, j.segment, uint64(0), "the predecessor must be archived")
	require.False(t, j.predecessorsAcknowledged(session, second.ArrivalSeq),
		"an empty active window cannot hide an unsettled predecessor")
	known, held, err := j.leaseHeld(first.Delivery)
	require.NoError(t, err)
	require.True(t, held)
	require.Equal(t, first, known)
	require.NoError(t, j.retireDenied(ctx, first))
	denied, err := j.terminalDenied(first)
	require.NoError(t, err)
	require.True(t, denied)
	require.False(t, j.acknowledged(first.Delivery), "denial is not capture publication")
	require.True(t, j.predecessorsAcknowledged(session, second.ArrivalSeq))
	wrong := first
	wrong.RequestHash = testDeliveryRequest("wrong-request")
	_, err = j.terminalDenied(wrong)
	require.Error(t, err, "a matching nonce cannot substitute for the full original binding")

	require.NoError(t, j.owner.Release())
	j = roll.open(t, root)
	denied, err = j.terminalDenied(first)
	require.NoError(t, err)
	require.True(t, denied)
	require.False(t, j.acknowledged(first.Delivery))
	require.True(t, j.predecessorsAcknowledged(session, second.ArrivalSeq))
}

func TestDeliveryTerminal_RotationDefersDispositionWithoutDeadlock(t *testing.T) {
	roll := parallelRollover(t, 1)
	j := roll.open(t, t.TempDir())
	lease, err := j.lease(context.Background(), genNonce(401), "rotating", testDeliveryRequest(genNonce(401)))
	require.NoError(t, err)
	j.st.Lock()
	j.rotating = true
	j.st.Unlock()
	t.Cleanup(func() {
		j.st.Lock()
		j.rotating = false
		j.rotateDone.Broadcast()
		j.st.Unlock()
	})
	require.Error(t, j.retireDenied(context.Background(), lease))
	_, err = j.terminalDenied(lease)
	require.Error(t, err)
	require.False(t, j.predecessorsAcknowledged(lease.Session, lease.ArrivalSeq))
	j.st.Lock()
	inflight := j.inflight
	j.st.Unlock()
	require.Zero(t, inflight, "a deferred disposition must not hold the rotation barrier")
}
