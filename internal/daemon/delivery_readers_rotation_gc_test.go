package daemon

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// TestDeliveryReaders_V6_GCAtEveryRotationStepKeepsEveryUnsettledRoot makes GC-during-rotation
// deterministic (V6 close-out C1.10, gate 3; review finding 6). The live-rotation test runs passes
// beside a rotating journal and records whether any overlapped; nothing made one straddle a moving
// authority. Here both interleavings are forced:
//
//   - a full GC pass runs INSIDE each rotation, at every step the rotation stops between (window
//     archived, next segment and its carry staged, segment 0 frozen, transition committed but the live
//     window not yet switched). The authority does not move under such a pass, so it completes —
//     except between segment 0's freeze and the commit, where GC refuses a frozen seal on the segment
//     the authority still names active and halts, deleting nothing — and every root an unsettled lease
//     references, in the active window or archived, is retained;
//   - a rotation runs INSIDE a GC pass, after the pass has harvested its sources and before it
//     rechecks the authority. That pass must halt and delete nothing, and the next pass completes,
//     keeping every unsettled root and collecting the settled ones.
func TestDeliveryReaders_V6_GCAtEveryRotationStepKeepsEveryUnsettledRoot(t *testing.T) {
	setRollover(t, 100) // rotations are driven below, so none lands between a Put and its lease
	ctx := context.Background()
	root := t.TempDir()
	s, err := store.Open(root, config.Defaults(), store.Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	j := openRolloverJournal(t, root)
	policy := store.GCPolicy{RetainDays: -1, RetainSessions: -1}

	var (
		roots   []core.Hash
		leases  []deliveryLease
		settled = map[int]bool{}
	)
	// unsettledMissing lists the unsettled deliveries whose root is gone. It reports instead of failing,
	// because it also runs inside a rotation, where a test failure would leave the barrier held.
	unsettledMissing := func(when string) []string {
		var missing []string
		for k := range leases {
			if settled[k] {
				continue
			}
			if _, err := s.GetRoot(ctx, roots[k]); err != nil {
				missing = append(missing, fmt.Sprintf("%s: delivery %d is unsettled; its root must be retained: %v", when, k, err))
			}
		}
		return missing
	}
	var inRotation []string // failures seen inside a rotation, asserted after it returns
	stages := map[string]int{}
	setRotateHook(t, func(stage string) error {
		rep, err := s.GC(ctx, policy)
		switch {
		case err != nil:
			inRotation = append(inRotation, fmt.Sprintf("stage %s: GC: %v", stage, err))
		case stage == rotateStageFrozen && (!rep.RetentionRootsError || rep.DeletedObjects != 0):
			// Segment 0 is frozen but the authority still names it active: store GC accepts a frozen seal
			// only on an ARCHIVED segment 0, so the pass halts and deletes nothing until the transition
			// commits (or, after a crash here, until the next open finishes the rotation).
			inRotation = append(inRotation, fmt.Sprintf("stage %s: the pass must halt and delete nothing, got %+v", stage, rep))
		case stage != rotateStageFrozen && rep.RetentionRootsError:
			inRotation = append(inRotation, fmt.Sprintf("stage %s: the authority is still, so the pass completes", stage))
		}
		inRotation = append(inRotation, unsettledMissing("GC at rotation stage "+stage)...)
		stages[stage]++
		return nil
	})
	for i := 0; i < 10; i++ {
		put, err := s.PutBytes(ctx, []byte(fmt.Sprintf("payload referenced only by delivery %d\n", i)), store.PutOptions{})
		require.NoError(t, err)
		roots = append(roots, put.Root.Hash)
		l, err := j.lease(ctx, genNonce(i), "gc-steps", put.Root.Hash)
		require.NoError(t, err)
		leases = append(leases, l)
		if i%2 == 0 { // every other delivery settles, some in their own segment, some archived
			require.NoError(t, j.acknowledge(ctx, l.Delivery, l.ObservationID, core.Hash{}))
			settled[i] = true
		}
		if i%3 == 1 && i > 1 { // and an archived unsettled one settles a segment later
			require.NoError(t, j.acknowledge(ctx, leases[i-1].Delivery, leases[i-1].ObservationID, core.Hash{}))
			settled[i-1] = true
		}
		if i%3 == 2 {
			// The rotation a full window triggers, through the same barrier and steps (rotate, doRotate).
			require.NoError(t, j.rotate(ctx, j.segment))
			require.Empty(t, inRotation)
		}
	}
	require.Equal(t, uint64(3), j.segment)
	for _, stage := range []string{rotateStageArchived, rotateStageStaged, rotateStageCommitted} {
		require.Equal(t, int(j.segment), stages[stage], "one GC pass at stage %s of every rotation", stage)
	}
	require.Equal(t, 1, stages[rotateStageFrozen], "segment 0 is frozen once, by the first rotation")
	setRotateHook(t, nil)

	// A rotation lands between the harvest and the recheck: the pass halts and deletes nothing.
	segmentBefore := j.segment
	var rotateErr error
	rotated := false
	restore := store.SetGCAfterHarvestHookForTest(func() {
		if !rotated {
			rotated = true
			rotateErr = j.rotate(ctx, j.segment)
		}
	})
	rep, err := s.GC(ctx, policy)
	restore()
	require.NoError(t, err)
	require.True(t, rotated)
	require.NoError(t, rotateErr)
	require.Equal(t, segmentBefore+1, j.segment)
	require.True(t, rep.RetentionRootsError, "a rotation under the pass halts it")
	require.Zero(t, rep.DeletedObjects, "a halted pass deletes nothing")
	require.Empty(t, unsettledMissing("after the halted pass"))

	rep, err = s.GC(ctx, policy)
	require.NoError(t, err)
	require.False(t, rep.RetentionRootsError, "a quiet pass completes")
	require.Empty(t, unsettledMissing("after the quiet pass"))
	for k := 0; k < 10; k++ {
		if settled[k] {
			_, err := s.GetRoot(ctx, roots[k])
			require.ErrorIs(t, err, core.ErrNotFound, "delivery %d was settled; its root is collectible", k)
		}
	}
}
