package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/ipc"
)

// TestDeliveryJournal_AcceptsTheNonceTheHookClientMints is the cross-package pin V5-VERIFY §4.3
// found missing. The hook client mints the delivery label (ipc.NewDeliveryNonce) and this journal
// validates it (validDeliveryToken); each side had a unit test that hand-rolled its own token, and
// the two lengths disagreed for as long as they were tested apart — every real delivery reached
// ingest.leaseDelivery, was refused with ErrContract, and was counted unleased. A lease is the
// first of publication order's stages, so nothing downstream (capture sidecar, verified reference,
// committed frontier) ever ran for a real hook. This test takes the label from the one real
// producer and hands it to the one real consumer.
func TestDeliveryJournal_AcceptsTheNonceTheHookClientMints(t *testing.T) {
	_, _, journal := newTestDeliveryJournal(t)

	nonce, err := ipc.NewDeliveryNonce()
	require.NoError(t, err)
	require.True(t, validDeliveryToken(nonce),
		"the client's minted nonce must be a token the journal accepts, or no delivery is ever leased")

	lease, err := journal.lease(context.Background(), nonce, "session", testDeliveryRequest("x"))
	require.NoError(t, err, "a real delivery label must take a lease")
	require.Equal(t, nonce, lease.Delivery)
	require.NotEmpty(t, lease.ObservationID)

	again, err := journal.lease(context.Background(), nonce, "session", testDeliveryRequest("x"))
	require.NoError(t, err)
	require.Equal(t, lease, again, "a retry of the same delivery takes the same lease back")
}
