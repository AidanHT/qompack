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

// SP20-D4 old-reader barrier (V6 close-out C1.10; rollover-compatibility-critical-review.md §2). After a
// rotation the four legacy files stay behind as segment 0, and a reader that predates segments opens
// them as THE journal and appends to it, re-minting arrivals later segments already advanced past. The
// one file every such reader must parse before it can append is the position seal, so when segment 0 is
// archived its two seals are rewritten as FROZEN documents: the same sealed position, in a shape the
// pre-segment dual reader (readDeliverySealImage, then the strict v1 loadDeliveryPosition) refuses.

// rotatedLegacyStore leases two deliveries through a journal that rotates after every lease (threshold 1
// on its lock, so the caller may run in parallel), acknowledging the first, so segment 0 is archived.
func rotatedLegacyStore(t *testing.T) (root string, first deliveryLease) {
	t.Helper()
	ctx := context.Background()
	root = t.TempDir()
	j := rolloverAt(1).open(t, root)
	var err error
	first, err = j.lease(ctx, genNonce(0), "frozen", testDeliveryRequest(genNonce(0)))
	require.NoError(t, err)
	require.NoError(t, j.acknowledge(ctx, first.Delivery, first.ObservationID, core.Hash{}))
	_, err = j.lease(ctx, genNonce(1), "frozen", testDeliveryRequest(genNonce(1)))
	require.NoError(t, err)
	require.Equal(t, uint64(1), j.segment, "segment 0 is archived")
	require.NoError(t, j.owner.Release())
	return root, first
}

// TestDeliveryRollover_ArchivedLegacySegmentRefusesThePreSegmentReader: both legacy seals refuse the
// reader every pre-segment build opens them with, while the sealed position they carry still checks
// the untouched legacy journals exactly.
func TestDeliveryRollover_ArchivedLegacySegmentRefusesThePreSegmentReader(t *testing.T) {
	t.Parallel()
	root, first := rotatedLegacyStore(t)
	state := paths.Of(root).State
	for _, seal := range []struct {
		name, journal string
		seed          core.Hash
		domain        string
	}{
		{deliveryPositionFile, deliveryLeaseFile, deliveryChainSeed, deliveryChainDomain},
		{deliveryAckPositionFile, deliveryAckFile, deliveryAckChainSeed, deliveryAckChainDomain},
	} {
		p := filepath.Join(state, seal.name)
		_, _, _, err := loadDeliverySeal(p, seal.seed, seal.domain)
		require.Error(t, err, "%s: the pre-segment dual reader must refuse an archived segment 0", seal.name)
		_, err = loadDeliveryPosition(p, seal.seed)
		require.Error(t, err, "%s: the strict v1 reader must refuse it too", seal.name)

		raw, err := os.ReadFile(paths.Long(p))
		require.NoError(t, err)
		pos, ok := parseFrozenSeal(raw, seal.seed)
		require.True(t, ok, "%s is a frozen seal", seal.name)
		info, err := os.Stat(paths.Long(filepath.Join(state, seal.journal)))
		require.NoError(t, err)
		require.Equal(t, info.Size(), pos.Bytes, "%s seals the whole untouched journal", seal.name)
		require.Equal(t, 1, pos.Count)
	}
	// The current reader still resolves the archived identity and continues densely.
	j := rolloverAt(1).open(t, root)
	got, err := j.lease(context.Background(), first.Delivery, first.Session, first.RequestHash)
	require.NoError(t, err)
	require.Equal(t, first, got)
	require.True(t, j.acknowledged(first.Delivery))
}

// TestDeliveryRollover_ArchivedLegacySegmentIsRefrozenAtOpen: a crash between the transition commit and
// the freeze leaves segment 0 with an ordinary seal; the next open verifies it against the journal and
// freezes it before assigning anything.
func TestDeliveryRollover_ArchivedLegacySegmentIsRefrozenAtOpen(t *testing.T) {
	t.Parallel()
	root, _ := rotatedLegacyStore(t)
	state := paths.Of(root).State
	// Put ordinary v1 seals back, sealing exactly the frozen positions (the pre-freeze state).
	for _, seal := range []struct {
		name string
		seed core.Hash
	}{{deliveryPositionFile, deliveryChainSeed}, {deliveryAckPositionFile, deliveryAckChainSeed}} {
		p := filepath.Join(state, seal.name)
		raw, err := os.ReadFile(paths.Long(p))
		require.NoError(t, err)
		pos, ok := parseFrozenSeal(raw, seal.seed)
		require.True(t, ok)
		require.NoError(t, writeDeliveryPositionV1(p, pos.Bytes, pos.Count, pos.Chain))
		_, err = loadDeliveryPosition(p, seal.seed)
		require.NoError(t, err, "fixture: an ordinary v1 seal again")
	}
	_ = rolloverAt(1).open(t, root)
	for _, seal := range []struct {
		name string
		seed core.Hash
	}{{deliveryPositionFile, deliveryChainSeed}, {deliveryAckPositionFile, deliveryAckChainSeed}} {
		p := filepath.Join(state, seal.name)
		_, err := loadDeliveryPosition(p, seal.seed)
		require.Error(t, err, "%s is frozen again by the open", seal.name)
		raw, err := os.ReadFile(paths.Long(p))
		require.NoError(t, err)
		_, ok := parseFrozenSeal(raw, seal.seed)
		require.True(t, ok, "%s is a frozen seal again", seal.name)
	}
}

// TestDeliveryRollover_FrozenSealThatDisagreesWithItsJournalRefusesOpen: the open checks that each
// archived legacy journal still has exactly the length its frozen seal names (the full chain scan of an
// archived segment is the offline check's, as for every other archived segment); a mismatch is evidence
// to preserve, not to rewrite.
func TestDeliveryRollover_FrozenSealThatDisagreesWithItsJournalRefusesOpen(t *testing.T) {
	t.Parallel()
	root, _ := rotatedLegacyStore(t)
	state := paths.Of(root).State
	p := filepath.Join(state, deliveryPositionFile)
	raw, err := os.ReadFile(paths.Long(p))
	require.NoError(t, err)
	pos, ok := parseFrozenSeal(raw, deliveryChainSeed)
	require.True(t, ok)
	wrong, err := encodeFrozenSeal(deliveryPosition{Version: core.EvidenceVersion, Bytes: pos.Bytes - 1, Count: pos.Count, Chain: pos.Chain}, deliveryChainSeed)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(p), wrong, 0o600))

	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	_, err = lock.openDeliveryJournal()
	require.Error(t, err)
	after, err := os.ReadFile(paths.Long(p))
	require.NoError(t, err)
	require.Equal(t, wrong, after, "a disagreeing frozen seal is preserved, never rewritten")
}

// TestDeliveryRollover_FrozenSealOnTheActiveSegmentRefusesOpen: a frozen seal names an ARCHIVED
// segment 0. Found on a store whose authority still names segment 0 active, it is refused, never
// silently thawed into a writable journal.
func TestDeliveryRollover_FrozenSealOnTheActiveSegmentRefusesOpen(t *testing.T) {
	roll := parallelRollover(t, 100)
	ctx := context.Background()
	root := t.TempDir()
	j := roll.open(t, root)
	l, err := j.lease(ctx, genNonce(0), "active", testDeliveryRequest(genNonce(0)))
	require.NoError(t, err)
	require.NoError(t, j.owner.Release())
	state := paths.Of(root).State
	p := filepath.Join(state, deliveryPositionFile)
	pos, err := loadDeliveryPosition(p, deliveryChainSeed)
	require.NoError(t, err)
	frozen, err := encodeFrozenSeal(pos, deliveryChainSeed)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(p), frozen, 0o600))

	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	_, err = lock.openDeliveryJournal()
	require.Error(t, err, "segment 0 is still active: a frozen seal there is refused")
	after, err := os.ReadFile(paths.Long(p))
	require.NoError(t, err)
	require.Equal(t, frozen, after)
	require.Equal(t, uint64(1), l.ArrivalSeq)
}
