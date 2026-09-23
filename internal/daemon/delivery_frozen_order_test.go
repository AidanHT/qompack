package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// SP20-D4 old-reader barrier ordering (V6 close-out review, finding 3). Segment 0's seals used to be
// frozen only after the transition to segment 1 had committed, in the switch that follows it. A crash in
// between left segment 0 archived under ordinary seals, and a pre-segment build that opened the store
// before a current one could append to it, re-minting arrivals segment 1 owns. Segment 0 is now frozen
// before the transition commits, and a crash between the freeze and the commit is finished at the next
// open.

// requirePreSegmentReadersRefuse asserts both of segment 0's seals refuse the readers every build that
// predates segments opens them with.
func requirePreSegmentReadersRefuse(t *testing.T, state, when string) {
	t.Helper()
	for _, seal := range []struct {
		name   string
		seed   core.Hash
		domain string
	}{{deliveryPositionFile, deliveryChainSeed, deliveryChainDomain}, {deliveryAckPositionFile, deliveryAckChainSeed, deliveryAckChainDomain}} {
		p := filepath.Join(state, seal.name)
		_, _, _, err := loadDeliverySeal(p, seal.seed, seal.domain)
		require.Error(t, err, "%s: %s: the pre-segment dual reader must refuse segment 0", when, seal.name)
		_, err = loadDeliveryPosition(p, seal.seed)
		require.Error(t, err, "%s: %s: the strict v1 reader must refuse it too", when, seal.name)
	}
}

// TestDeliveryRollover_SegmentZeroIsFrozenBeforeItsTransitionCommits: a rotation out of segment 0 that
// fails in the switch after its transition committed (the crash window the review names) has already
// frozen segment 0, so a pre-segment build that reaches the store first is refused.
func TestDeliveryRollover_SegmentZeroIsFrozenBeforeItsTransitionCommits(t *testing.T) {
	setRollover(t, 1)
	ctx := context.Background()
	root := t.TempDir()
	j := openRolloverJournal(t, root)
	first, err := j.lease(ctx, genNonce(0), "freeze-order", testDeliveryRequest(genNonce(0)))
	require.NoError(t, err)
	oldFile := j.file
	j.writer = leaseFaultWriter{file: oldFile, close: func() error {
		_ = oldFile.Close()
		return errors.New("injected close failure after the transition committed")
	}}
	_, err = j.lease(ctx, genNonce(1), "freeze-order", testDeliveryRequest(genNonce(1)))
	require.Error(t, err, "the switch fails after the transition committed")
	state := paths.Of(root).State
	auth, _, err := readonlySegmentAuthority(ctx, state)
	require.NoError(t, err)
	require.Equal(t, uint64(1), auth.transitions[len(auth.transitions)-1].Active, "the transition is committed")
	requirePreSegmentReadersRefuse(t, state, "after a crash in the switch")

	require.NoError(t, j.owner.Release())
	reopened := openRolloverJournal(t, root)
	require.Equal(t, uint64(1), reopened.segment)
	got, err := reopened.lease(ctx, first.Delivery, first.Session, first.RequestHash)
	require.NoError(t, err)
	require.Equal(t, first, got, "the archived identity resolves")
	next, err := reopened.lease(ctx, genNonce(1), "freeze-order", testDeliveryRequest(genNonce(1)))
	require.NoError(t, err)
	require.Equal(t, uint64(2), next.ArrivalSeq)
}

// setRotateHook installs deliveryRotateHook for one test.
func setRotateHook(t *testing.T, hook func(stage string) error) {
	t.Helper()
	prev := deliveryRotateHook
	deliveryRotateHook = hook
	t.Cleanup(func() { deliveryRotateHook = prev })
}

// TestDeliveryRollover_CrashBetweenFreezeAndTransitionFinishesTheRotation: a rotation stopped after
// freezing segment 0 and before committing its transition leaves segment 0 active under frozen seals.
// A pre-segment build is refused there, and the next current open finishes the rotation: segment 1
// committed, arrivals dense, every archived identity resolving, nothing re-minted.
func TestDeliveryRollover_CrashBetweenFreezeAndTransitionFinishesTheRotation(t *testing.T) {
	setRollover(t, 2)
	ctx := context.Background()
	root := t.TempDir()
	j := openRolloverJournal(t, root)
	var window []deliveryLease
	for i := 0; i < 2; i++ {
		l, err := j.lease(ctx, genNonce(i), "crash-frozen", testDeliveryRequest(genNonce(i)))
		require.NoError(t, err)
		window = append(window, l)
	}
	require.NoError(t, j.acknowledge(ctx, window[0].Delivery, window[0].ObservationID, core.Hash{}))
	crash := errors.New("crash after the freeze, before the transition")
	setRotateHook(t, func(stage string) error {
		if stage == rotateStageFrozen {
			return crash
		}
		return nil
	})
	_, err := j.lease(ctx, genNonce(2), "crash-frozen", testDeliveryRequest(genNonce(2)))
	require.ErrorIs(t, err, crash)
	state := paths.Of(root).State
	auth, _, err := readonlySegmentAuthority(ctx, state)
	require.NoError(t, err)
	require.Equal(t, uint64(0), auth.transitions[len(auth.transitions)-1].Active, "the transition never committed")
	requirePreSegmentReadersRefuse(t, state, "segment 0 frozen before its transition")
	require.NoError(t, j.owner.Release())

	setRotateHook(t, nil)
	reopened := openRolloverJournal(t, root)
	require.Equal(t, uint64(1), reopened.segment, "the open finished the rotation")
	for _, l := range window {
		got, err := reopened.lease(ctx, l.Delivery, l.Session, l.RequestHash)
		require.NoError(t, err)
		require.Equal(t, l, got)
	}
	require.True(t, reopened.acknowledged(window[0].Delivery))
	next, err := reopened.lease(ctx, genNonce(2), "crash-frozen", testDeliveryRequest(genNonce(2)))
	require.NoError(t, err)
	require.Equal(t, uint64(3), next.ArrivalSeq, "arrivals continue densely")
	requirePreSegmentReadersRefuse(t, state, "after the finished rotation")
	require.Equal(t, readCarry(t, state, 1), []deliveryLease{window[1]}, "the finished rotation carried the open lease")
}
