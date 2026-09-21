package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

func TestObservationRestore_PendingIntentCannotCertifyReader(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root := putRoot(t, tp, "readable object with unresolved publication")
	id := obsID(t, 500)
	require.NoError(t, tp.Store.ReserveObservation(ctx, id,
		recWithObs(core.ToolUseID("toolu_restore_pending"), root, id), nil))
	x := newMaint(t, tp, leaseOK)
	var proof RestoreProof
	require.ErrorContains(t, x.proveReader(ctx, tp.Root, &proof), "observation publication")
	_, err := tp.Store.RecoverToolUseByObservation(ctx, id)
	require.NoError(t, err)
	proof = RestoreProof{}
	require.NoError(t, x.proveReader(ctx, tp.Root, &proof))
	require.Positive(t, proof.ContentRootsProven, "the positive control actually reads payloads")
}
