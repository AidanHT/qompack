package daemon

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// archiveFixture is one rotation's worth of work over a store that already holds an archived window:
// the prior window (some of it still unsettled), and the outgoing window whose acknowledgements and
// terminal dispositions settle leases of both windows.
type archiveFixture struct {
	prior, prior2         []deliveryLease
	priorAcks             []deliveryAck
	window                []deliveryLease
	acks                  []deliveryAck
	terminals             []deliveryTerminal
	windowSize, perWindow int
}

// newArchiveFixture builds a fixture of windowSize leases per window across sessions sessions. Arrivals
// are dense per session and continue from the prior window into the outgoing one. In the prior window
// every fourth lease stays unsettled at its rotation; the outgoing window acknowledges half of those
// (acknowledgements of ARCHIVED leases, joined against the store) and denies one, and settles its own
// leases by acknowledgement or denial except the last two of each session, which stay in flight.
func newArchiveFixture(t *testing.T, windowSize, sessions int) archiveFixture {
	t.Helper()
	fx := archiveFixture{windowSize: windowSize, perWindow: windowSize / sessions}
	nonce := 0
	for s := 0; s < sessions; s++ {
		sess := core.SessionID(fmt.Sprintf("archive-order-%02d", s))
		for a := 1; a <= 2*fx.perWindow; a++ {
			l := mkGenLease(t, genNonce(nonce), sess, uint64(a))
			nonce++
			if a <= fx.perWindow {
				fx.prior = append(fx.prior, l)
				if a%4 != 0 {
					fx.priorAcks = append(fx.priorAcks, mkAck(l))
				} else {
					fx.prior2 = append(fx.prior2, l) // unsettled when the prior window rotated
				}
				continue
			}
			fx.window = append(fx.window, l)
			switch {
			case a > 2*fx.perWindow-2: // in flight at the rotation
			case a%7 == 0:
				fx.terminals = append(fx.terminals, terminalFor(l))
			default:
				fx.acks = append(fx.acks, mkAck(l))
			}
		}
	}
	for i, l := range fx.prior2 {
		switch {
		case i == 0:
			fx.terminals = append(fx.terminals, terminalFor(l))
		case i%2 == 0:
			fx.acks = append(fx.acks, mkAck(l))
		}
	}
	return fx
}

// TestDeliveryRollover_ArchiveRecordsExactlyWhatTheBatchCommitsRecord pins the contract archiveWindow's
// comment states and the rotation relies on: archiving a window records exactly what commit, commitAck
// and commitTerminal record for the same three batches, in that order — the same leases, arrival
// watermarks, order entries, acknowledgements, terminal memberships and per-session frontiers. The
// radix is canonical for its key set, so equal contents are equal roots. The windows are large enough
// that both paths publish intermediate generations (genTxnMaxHeld), and the outgoing window settles
// leases the prior window archived unsettled, so the joins against the store are exercised too.
func TestDeliveryRollover_ArchiveRecordsExactlyWhatTheBatchCommitsRecord(t *testing.T) {
	ctx := context.Background()
	fx := newArchiveFixture(t, 12_000, 6)

	archived := newTestGenerations(t)
	require.NoError(t, archived.archiveWindow(ctx, fx.prior, fx.priorAcks, nil))
	batched := newTestGenerations(t)
	_, err := batched.commit(ctx, fx.prior)
	require.NoError(t, err)
	require.NoError(t, batched.commitAck(ctx, fx.priorAcks))
	require.Equal(t, batched.currentRoot(), archived.currentRoot(), "the prior window archives to the batch commits' root")

	require.NoError(t, archived.archiveWindow(ctx, fx.window, fx.acks, fx.terminals))
	_, err = batched.commit(ctx, fx.window)
	require.NoError(t, err)
	require.NoError(t, batched.commitAck(ctx, fx.acks))
	require.NoError(t, batched.commitTerminal(ctx, fx.terminals))
	require.Greater(t, archived.generationCount(), int64(2), "the window is large enough to publish intermediate generations")
	require.Equal(t, batched.currentRoot(), archived.currentRoot(),
		"archiving the window records exactly what the three batch commits record")

	// And the contents the rotation depends on, read back: every lease resolves, every session's
	// frontier stops at its oldest in-flight arrival.
	for _, l := range append(append([]deliveryLease(nil), fx.prior...), fx.window...) {
		got, found, err := archived.resolveLease(ctx, l.Delivery)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, l, got)
	}
	for s := 0; s < 6; s++ {
		sess := core.SessionID(fmt.Sprintf("archive-order-%02d", s))
		f, pending, err := archived.sessionFrontier(ctx, sess)
		require.NoError(t, err)
		require.True(t, pending)
		wantF, wantPending, err := batched.sessionFrontier(ctx, sess)
		require.NoError(t, err)
		require.Equal(t, wantPending, pending)
		require.Equal(t, wantF, f, "session %s frontier", sess)
	}
}

// TestDeliveryRollover_ArchiveIsIdempotentAfterAnInterruptedAttempt: re-archiving a window the store
// already holds (an open finishing an interrupted rotation) moves nothing.
func TestDeliveryRollover_ArchiveIsIdempotentAfterAnInterruptedAttempt(t *testing.T) {
	ctx := context.Background()
	fx := newArchiveFixture(t, 6_000, 3)
	g := newTestGenerations(t)
	require.NoError(t, g.archiveWindow(ctx, fx.prior, fx.priorAcks, nil))
	require.NoError(t, g.archiveWindow(ctx, fx.window, fx.acks, fx.terminals))
	root, gens := g.currentRoot(), g.generationCount()
	require.NoError(t, g.archiveWindow(ctx, fx.window, fx.acks, fx.terminals))
	require.Equal(t, root, g.currentRoot(), "a re-archived window changes nothing")
	require.Equal(t, gens, g.generationCount(), "and commits no generation")
}

// TestDeliveryRollover_ArchiveRefusesAConflictingLease: a window lease whose nonce the store already
// holds under a different identity is a conflict, whatever position it takes in the archive.
func TestDeliveryRollover_ArchiveRefusesAConflictingLease(t *testing.T) {
	ctx := context.Background()
	fx := newArchiveFixture(t, 3_000, 3)
	g := newTestGenerations(t)
	require.NoError(t, g.archiveWindow(ctx, fx.prior, fx.priorAcks, nil))
	clash := fx.window[len(fx.window)/2]
	clash.Delivery = fx.prior[len(fx.prior)/3].Delivery // an archived nonce, re-used with another identity
	window := append([]deliveryLease(nil), fx.window...)
	window[len(window)/2] = clash
	require.ErrorIs(t, g.archiveWindow(ctx, window, nil, nil), errGenerationConflict)
}
