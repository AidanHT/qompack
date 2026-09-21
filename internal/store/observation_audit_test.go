package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

func TestObservationAudit_UnpublishedIntentCannotLookClean(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root := putRoot(t, tp, "pending publication")
	observation := obsID(t, 120)
	rec := recWithObs(core.ToolUseID("toolu_audit_pending"), root, observation)
	require.NoError(t, tp.Store.ReserveObservation(ctx, observation, rec, nil))
	audit, err := tp.Store.AuditPublication(ctx, DefaultPublicationScanCap())
	require.NoError(t, err)
	require.True(t, audit.Incomplete)
	require.Contains(t, audit.Notes, "observation publication remains incomplete")
	_, err = tp.Store.RecoverToolUseByObservation(ctx, observation)
	require.NoError(t, err)
	audit, err = tp.Store.AuditPublication(ctx, DefaultPublicationScanCap())
	require.NoError(t, err)
	require.False(t, audit.Incomplete, "completed intent and indexed object are a positive control")
}

func TestObservationAudit_UnknownStateAndBoundsStayExplicit(t *testing.T) {
	tp := newTestStore(t)
	tp.Store.mu.Lock()
	tp.Store.obsSidecarUncertain = true
	tp.Store.obsBindings = map[core.ObservationID]*obsBinding{
		obsID(t, 1): {committed: true},
		obsID(t, 2): {committed: true},
	}
	tp.Store.mu.Unlock()
	audit, err := tp.Store.AuditPublication(context.Background(), PublicationScanCap{MaxEntries: 1})
	require.NoError(t, err)
	require.True(t, audit.Incomplete)
	require.True(t, audit.Truncated)
	require.Contains(t, audit.Notes, "observation intent integrity is unavailable")
	require.Contains(t, audit.Notes, "observation intent audit reached its entry budget")
}
