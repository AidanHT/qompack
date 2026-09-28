package daemon

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// emptyCarry is the carry of a segment opened with nothing archived unacknowledged.
func emptyCarry(t *testing.T, segment uint64) []byte {
	t.Helper()
	raw, err := encodeDeliveryCarry(segment, nil)
	require.NoError(t, err)
	return raw
}

// readCarry reads and decodes a committed segment's carry from disk.
func readCarry(t *testing.T, stateDir string, segment uint64) []deliveryLease {
	t.Helper()
	raw, err := os.ReadFile(paths.Long(filepath.Join(segmentDir(stateDir, segment), deliveryCarryFile)))
	require.NoError(t, err, "segment %d must carry a file", segment)
	leases, err := decodeDeliveryCarry(raw, segment)
	require.NoError(t, err)
	return leases
}

// TestDeliveryCarry_RoundTripAndRefusals: the carry reads back exactly as written, and anything the
// writer would not have written — another segment's header, an altered or torn body, a line out of
// order — is refused rather than read as fewer carried leases.
func TestDeliveryCarry_RoundTripAndRefusals(t *testing.T) {
	a := mkGenLease(t, genNonce(1), "carry-a", 3)
	b := mkGenLease(t, genNonce(2), "carry-a", 7)
	c := mkGenLease(t, genNonce(3), "carry-b", 1)
	raw, err := encodeDeliveryCarry(4, []deliveryLease{a, b, c})
	require.NoError(t, err)
	got, err := decodeDeliveryCarry(raw, 4)
	require.NoError(t, err)
	require.Equal(t, []deliveryLease{a, b, c}, got)

	empty, err := decodeDeliveryCarry(emptyCarry(t, 4), 4)
	require.NoError(t, err)
	require.Empty(t, empty)

	outOfOrder, err := encodeDeliveryCarry(4, []deliveryLease{b, a})
	require.NoError(t, err, "the encoder writes what it is given")
	for name, bad := range map[string][]byte{
		"another segment's carry": raw,
		"an altered body":         bytes.Replace(raw, []byte(`"arrival":7`), []byte(`"arrival":8`), 1),
		"a torn body":             raw[:len(raw)-9],
		"no header line":          []byte(`{"v":1}`),
		"leases out of order":     outOfOrder,
	} {
		segment := uint64(4)
		if name == "another segment's carry" {
			segment = 5
		}
		_, err := decodeDeliveryCarry(bad, segment)
		require.ErrorIs(t, err, errSegmentUnavailable, name)
	}
}

// TestDeliveryRollover_CarryNamesEveryArchivedUnacknowledgedLease: each rotation carries into the new
// segment exactly the archived leases with no acknowledgement — the outgoing window's and the ones the
// outgoing segment itself carried — and drops a carried lease once an acknowledgement settles it, in
// whichever later segment that acknowledgement lands. A lease denied by a terminal disposition but never
// acknowledged stays carried, because store GC never released on a terminal disposition.
func TestDeliveryRollover_CarryNamesEveryArchivedUnacknowledgedLease(t *testing.T) {
	roll := parallelRollover(t, 3)
	ctx := context.Background()
	j := roll.open(t, t.TempDir())
	var leases []deliveryLease
	lease := func(i int) deliveryLease {
		t.Helper()
		l, err := j.lease(ctx, genNonce(i), "carry", testDeliveryRequest(genNonce(i)))
		require.NoError(t, err)
		leases = append(leases, l)
		return l
	}
	ack := func(l deliveryLease) {
		t.Helper()
		require.NoError(t, j.acknowledge(ctx, l.Delivery, l.ObservationID, core.Hash{}))
	}
	want := func(idx ...int) []deliveryLease {
		out := make([]deliveryLease, 0, len(idx))
		for _, i := range idx {
			out = append(out, leases[i])
		}
		sort.Slice(out, func(a, b int) bool { return leaseBefore(out[a], out[b]) })
		return out
	}

	// Segment 0: leases 0, 1, 2; 0 acknowledged, 1 denied, 2 left open. Lease 3 rotates.
	ack(lease(0))
	require.NoError(t, j.retireDenied(ctx, lease(1)))
	lease(2)
	lease(3)
	require.Equal(t, uint64(1), j.segment)
	require.Equal(t, want(1, 2), readCarry(t, j.stateDir, 1), "segment 1 carries the denied and the open lease")

	// Segment 1: 3 (leased above), 4, 5; the ARCHIVED lease 2 is acknowledged here, 3 and 5 too.
	ack(leases[2])
	ack(leases[3])
	lease(4)
	ack(lease(5))
	lease(6) // rotates
	require.Equal(t, uint64(2), j.segment)
	require.Equal(t, want(1, 4), readCarry(t, j.stateDir, 2),
		"an acknowledgement recorded in a later segment drops the carried lease; the rest carry on")

	// Segment 2: lease 4, archived in segment 1, is acknowledged two segments on.
	ack(leases[4])
	ack(leases[6])
	lease(7)
	lease(8)
	lease(9) // rotates
	require.Equal(t, uint64(3), j.segment)
	require.Equal(t, want(1, 7, 8), readCarry(t, j.stateDir, 3))

	// A reopen resumes from the committed carry, and the next rotation builds on it.
	stateDir := j.stateDir
	require.NoError(t, j.owner.Release())
	j = roll.open(t, filepath.Dir(filepath.Dir(stateDir)))
	require.Equal(t, uint64(3), j.segment)
	ack(leases[8])
	lease(10)
	lease(11)
	lease(12) // rotates
	require.Equal(t, uint64(4), j.segment)
	require.Equal(t, want(1, 7, 9, 10, 11), readCarry(t, j.stateDir, 4))
}

// TestDeliveryRollover_DamagedCarryRefusesTheNextRotation: a rotation builds the next carry on this
// segment's, so a carry that no longer matches its header stops the rotation (the journal fails closed)
// rather than dropping the leases it named.
func TestDeliveryRollover_DamagedCarryRefusesTheNextRotation(t *testing.T) {
	roll := parallelRollover(t, 2)
	ctx := context.Background()
	j := roll.open(t, t.TempDir())
	for i := 0; i < 3; i++ {
		_, err := j.lease(ctx, genNonce(i), "damaged", testDeliveryRequest(genNonce(i)))
		require.NoError(t, err)
	}
	require.Equal(t, uint64(1), j.segment)
	path := filepath.Join(segmentDir(j.stateDir, 1), deliveryCarryFile)
	raw, err := os.ReadFile(paths.Long(path))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(path), raw[:len(raw)-5], 0o600))

	_, err = j.lease(ctx, genNonce(3), "damaged", testDeliveryRequest(genNonce(3)))
	require.NoError(t, err, "the window has room")
	_, err = j.lease(ctx, genNonce(4), "damaged", testDeliveryRequest(genNonce(4)))
	require.Error(t, err, "the rotation that would build on a damaged carry refuses")
	require.Equal(t, uint64(1), j.segment)
	_, err = os.Stat(paths.Long(segmentDir(j.stateDir, 2)))
	require.True(t, os.IsNotExist(err), "no segment is staged over the damaged carry")
}

// TestDeliverySealSegment_CarryIsCheckedAgainstTheBaseRoot: the offline check (fsck --seal-check)
// validates every later segment's carried-lease file read-only — against its header, and each carried
// lease against the generation store at the segment's base root — and refuses a carry that was altered,
// that names a lease the store does not hold, or that carries a lease already acknowledged there.
func TestDeliverySealSegment_CarryIsCheckedAgainstTheBaseRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	active, _ := buildRotatedStore(t, root, "carry-check", 4, false)
	require.Equal(t, uint64(3), active)
	out, err := checkSeal(t, root)
	require.NoError(t, err, out)
	require.Contains(t, out, "segment 3 carried leases: 3 archived with no acknowledgement")

	state := paths.Of(root).State
	carryPath := filepath.Join(segmentDir(state, 3), deliveryCarryFile)
	good, err := os.ReadFile(paths.Long(carryPath))
	require.NoError(t, err)
	carried, err := decodeDeliveryCarry(good, 3)
	require.NoError(t, err)
	stranger := mkGenLease(t, genNonce(900), "carry-check-stranger", 1)
	acked := carried[0]

	for name, raw := range map[string][]byte{
		"an altered carry": bytes.Replace(good, []byte(`"arrival":2`), []byte(`"arrival":9`), 1),
		"a lease the store does not hold": func() []byte {
			b, err := encodeDeliveryCarry(3, append(append([]deliveryLease(nil), carried...), stranger))
			require.NoError(t, err)
			return b
		}(),
	} {
		require.NoError(t, os.WriteFile(paths.Long(carryPath), raw, 0o600))
		_, err := checkSeal(t, root)
		require.ErrorIs(t, err, errSegmentReaderRefused, name)
	}

	// A carried lease the store records as acknowledged at the base root: acknowledge it through a live
	// journal (segment 3's ack journal, mirrored into the store), then roll the carry of the NEXT segment
	// back to one that still names it.
	require.NoError(t, os.WriteFile(paths.Long(carryPath), good, 0o600))
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	lock.rolloverEntries = 1 // as buildRotatedStore's: the next lease rotates to segment 4
	j, err := lock.openDeliveryJournal()
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, j.acknowledge(ctx, acked.Delivery, acked.ObservationID, core.Hash{}))
	_, err = j.lease(ctx, genNonce(10), "carry-check", testDeliveryRequest(genNonce(10)))
	require.NoError(t, err)
	require.Equal(t, uint64(4), j.segment)
	require.NoError(t, lock.Release())
	next := filepath.Join(segmentDir(state, 4), deliveryCarryFile)
	nextCarry, err := decodeDeliveryCarry(func() []byte {
		b, err := os.ReadFile(paths.Long(next))
		require.NoError(t, err)
		return b
	}(), 4)
	require.NoError(t, err)
	require.NotContains(t, nextCarry, acked, "the acknowledged lease is no longer carried")
	stale, err := encodeDeliveryCarry(4, carriedPlus(nextCarry, acked))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(next), stale, 0o600))
	_, err = checkSeal(t, root)
	require.ErrorIs(t, err, errSegmentReaderRefused, "a carry naming an acknowledged lease is refused")
}

func carriedPlus(leases []deliveryLease, l deliveryLease) []deliveryLease {
	out := append(append([]deliveryLease(nil), leases...), l)
	sort.Slice(out, func(a, b int) bool { return leaseBefore(out[a], out[b]) })
	return out
}
