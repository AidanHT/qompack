package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Rule R on a segmented store (SP20-D4; V6 close-out C1.10). The segmented offline check refused every
// torn seal slot, so a crash in the middle of the ACTIVE segment's seal write — the one state Rule R
// exists for — left a rotated store with no operator repair at all: the daemon's strict reader refuses
// to open it, and --to v1 was refused outright. Rule R now reaches the active segment's seals (and only
// those: an archived segment's last seal completed before it rotated), with the same consent, the same
// report of exactly which lines it admitted, and the same "whole store checks first" ordering.

// segmentedToolProject builds a rotated store whose ACTIVE segment (1) holds two leases under a v2
// seal left by an unclean stop, so its seal has two valid slots, the newest of which a test can tear.
func segmentedToolProject(t *testing.T) (root string, leases []deliveryLease) {
	t.Helper()
	root = t.TempDir()
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	lock.sealFormat = 2
	lock.rolloverEntries = 2 // on the lock, not the package, so the caller may run in parallel
	j, err := lock.openDeliveryJournal()
	require.NoError(t, err)
	for i := 0; i < 4; i++ { // two fill segment 0, the third rotates, the fourth seals seq 2 in segment 1
		l, err := j.lease(context.Background(), genNonce(i), "ruler", testDeliveryRequest(genNonce(i)))
		require.NoError(t, err)
		leases = append(leases, l)
	}
	require.Equal(t, uint64(1), j.segment)
	require.NoError(t, j.acknowledge(context.Background(), leases[0].Delivery, leases[0].ObservationID, core.Hash{}))
	_ = j.poison(deliveryJournalError()) // an unclean stop: the held v2 seals are not downgraded
	require.NoError(t, lock.Release())
	return root, leases
}

// tearActiveSegmentSlot tears the newest slot of the given segment's lease seal, leaving the older one
// valid: the image the strict reader refuses and Rule R alone accepts.
func tearActiveSegmentSlot(t *testing.T, root string, seg uint64) string {
	t.Helper()
	p := filepath.Join(segmentDir(paths.Of(root).State, seg), deliveryPositionFile)
	image, err := os.ReadFile(paths.Long(p))
	require.NoError(t, err)
	require.True(t, isDeliverySealImage(image), "the unclean stop should have left a v2 image")
	effective, _, err := selectSeal(image, deliveryChainDomain, deliveryChainSeed)
	require.NoError(t, err)
	image[slotFor(effective.Seq).offset()+3] = 'X'
	require.NoError(t, os.WriteFile(paths.Long(p), image, 0o600))
	_, _, err = selectSeal(image, deliveryChainDomain, deliveryChainSeed)
	require.Error(t, err, "fixture: the strict reader refuses the torn slot")
	return p
}

func TestDeliverySealSegment_RuleRRepairsATornActiveSegmentSeal(t *testing.T) {
	t.Parallel()
	root, leases := segmentedToolProject(t)
	seal := tearActiveSegmentSlot(t, root, 1)
	torn, err := os.ReadFile(paths.Long(seal))
	require.NoError(t, err)

	// The daemon's own reader refuses the torn active segment.
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	_, err = lock.openDeliveryJournal()
	require.Error(t, err, "the strict reader refuses a torn active seal")
	_ = lock.Release()

	// Without consent the segmented check refuses and writes nothing.
	_, err = checkSeal(t, root)
	require.Error(t, err)
	unchanged, err := os.ReadFile(paths.Long(seal))
	require.NoError(t, err)
	require.Equal(t, torn, unchanged)

	// With consent, a check accepts the valid record, names the admitted line, and writes nothing.
	report, err := toolTestRun(t, root, DeliverySealOptions{Check: true, AcceptTornSlot: true, Confirm: true})
	require.NoError(t, err)
	require.Contains(t, report, "one torn slot")
	require.Contains(t, report, "admitted 1 line")
	require.Contains(t, report, leases[3].Delivery, "the admitted line is printed, not counted")
	unchanged, err = os.ReadFile(paths.Long(seal))
	require.NoError(t, err)
	require.Equal(t, torn, unchanged, "a check writes nothing")

	// --to v1 --accept-torn-slot repairs the active segment's seal, and the daemon opens with every
	// identity intact and continues densely.
	report, err = toolTestRun(t, root, DeliverySealOptions{ToV1: true, AcceptTornSlot: true, Confirm: true})
	require.NoError(t, err)
	require.Contains(t, report, "segment 1 lease seal")
	j := rolloverAt(2).open(t, root)
	for _, l := range leases {
		got, err := j.lease(context.Background(), l.Delivery, l.Session, l.RequestHash)
		require.NoError(t, err)
		require.Equal(t, l, got)
	}
	require.True(t, j.acknowledged(leases[0].Delivery))
	next, err := j.lease(context.Background(), genNonce(4), "ruler", testDeliveryRequest(genNonce(4)))
	require.NoError(t, err)
	require.Equal(t, uint64(5), next.ArrivalSeq)
}

func TestDeliverySealSegment_RuleRNeverReachesAnArchivedSegment(t *testing.T) {
	t.Parallel()
	root, _ := segmentedToolProject(t)
	// Rotate once more so segment 1 is archived, with its v2 seal left as its last write sealed it.
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	lock.sealFormat = 2
	lock.rolloverEntries = 2
	j, err := lock.openDeliveryJournal()
	require.NoError(t, err)
	_, err = j.lease(context.Background(), genNonce(10), "ruler", testDeliveryRequest(genNonce(10)))
	require.NoError(t, err)
	require.Equal(t, uint64(2), j.segment)
	require.NoError(t, lock.Release())

	seal := tearActiveSegmentSlot(t, root, 1)
	before, err := os.ReadFile(paths.Long(seal))
	require.NoError(t, err)
	for _, o := range []DeliverySealOptions{
		{Check: true, AcceptTornSlot: true, Confirm: true},
		{ToV1: true, AcceptTornSlot: true, Confirm: true},
	} {
		_, err := toolTestRun(t, root, o)
		require.Error(t, err, "a torn archived seal is evidence, never Rule R's to accept")
		after, err := os.ReadFile(paths.Long(seal))
		require.NoError(t, err)
		require.Equal(t, before, after)
	}
}

// TestDeliverySealSegment_ConversionWithoutATornSlotIsStillRefused: --to v1 on a segmented store is a
// rollback no old writer could use; it stays refused unless it is Rule R's repair of the active segment.
func TestDeliverySealSegment_ConversionWithoutATornSlotIsStillRefused(t *testing.T) {
	t.Parallel()
	root, _ := segmentedToolProject(t)
	before := digestState(t, root)
	for _, o := range []DeliverySealOptions{
		{ToV1: true},
		{ToV1: true, AcceptTornSlot: true, Confirm: true}, // consent, but nothing torn to repair
	} {
		_, err := toolTestRun(t, root, o)
		require.Error(t, err)
		require.ErrorContains(t, err, "--to v1 is refused")
	}
	require.Equal(t, before, digestState(t, root))
}
