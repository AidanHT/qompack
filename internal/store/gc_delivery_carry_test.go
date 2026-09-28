package store

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// SP20-D4 bounded retention harvest (V6 close-out C1.10, review finding 1). With rollover on by
// default, GC used to read the lease journal of EVERY committed segment and fold the acknowledgements
// of every segment into one set capped at deliveryAckSetMax pairs, oldest first. Segment 0's
// acknowledgements alone nearly fill the cap, so from the first rotation on the leases of every later
// segment read as open: each pass re-read the whole delivery history and held every hash-shaped token
// of those leases in memory, and the set only grew. The fix bounds the harvest to the leases that can
// still be open: the ACTIVE segment's journal, and the archived leases with no acknowledgement, which
// the daemon writes at each rotation into the new segment's carried-lease file. These fixtures render
// that file literally, as every fixture here renders the daemon's wire.

// carryLine is one carried lease as the daemon writes it (a canonical lease line).
func carryLine(nonce, requestHex, obs string) string {
	return leaseLine(nonce, requestHex, obs)
}

// renderCarry renders a segment's carried-lease file: a canonical header naming the segment, the
// body's line count, byte length and digest, then the body.
func renderCarry(segment uint64, lines ...string) []byte {
	var body bytes.Buffer
	for _, ln := range lines {
		body.WriteString(ln)
		body.WriteByte('\n')
	}
	digest := dsegDigest("qompack.delivery.carried-leases.v1", body.Bytes())
	header := fmt.Sprintf(`{"v":1,"format":"qompack.delivery.carried-leases.v1","segment":%d,"count":%d,"bytes":%d,"digest":%q}`,
		segment, len(lines), body.Len(), hex.EncodeToString(digest[:]))
	return append(append([]byte(header), '\n'), body.Bytes()...)
}

func writeCarry(t *testing.T, tp *testProject, segment uint64, raw []byte) {
	t.Helper()
	dir := segDirOf(tp, segment)
	require.NoError(t, os.MkdirAll(paths.Long(dir), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(dir, "delivery-carried-leases.jsonl")), raw, 0o600))
}

// TestGCSegments_AckedArchivedLeasesAreReleasedPastTheAckSetBound is the regression for review
// finding 1. Three segments; every lease of the archived segments 0 and 1 was acknowledged in its own
// segment, one lease of segment 1 never was (so segment 2 carries it), and the active segment holds
// one acknowledged lease. The acknowledgement bound is lowered so it binds after segment 0's
// acknowledgements, as a real segment 0 (up to 65,536 of them) makes it bind at the production bound.
// A settled lease of a later segment must be released, and the unsettled one retained.
func TestGCSegments_AckedArchivedLeasesAreReleasedPastTheAckSetBound(t *testing.T) {
	prev := deliveryAckSetMax
	deliveryAckSetMax = 2
	t.Cleanup(func() { deliveryAckSetMax = prev })
	tp := newTestStore(t)
	ctx := context.Background()

	r0a := gcSeed(t, tp, "src/r0a.txt", "segment 0, acknowledged in segment 0 (a)\n")
	r0b := gcSeed(t, tp, "src/r0b.txt", "segment 0, acknowledged in segment 0 (b)\n")
	r1 := gcSeed(t, tp, "src/r1.txt", "segment 1, acknowledged in segment 1\n")
	open1 := gcSeed(t, tp, "src/open1.txt", "segment 1, never acknowledged\n")
	r2 := gcSeed(t, tp, "src/r2.txt", "segment 2 (active), acknowledged in segment 2\n")

	installSegAuthority(t, tp, []segSpec{{0, ""}, {1, segBaseRoot("seg1")}, {2, segBaseRoot("seg2")}})
	n0a, n0b, n1, nOpen, n2 := deliveryNonce("a"), deliveryNonce("b"), deliveryNonce("c"), deliveryNonce("d"), deliveryNonce("e")
	writeSegJournal(t, tp, 0, deliveryLeaseFile,
		leaseLine(n0a, r0a.Hash.String(), obsIDText("0a")), leaseLine(n0b, r0b.Hash.String(), obsIDText("0b")))
	writeSegJournal(t, tp, 0, deliveryAckFile, ackLine(n0a, obsIDText("0a")), ackLine(n0b, obsIDText("0b")))
	openLease := leaseLine(nOpen, open1.Hash.String(), obsIDText("open1"))
	writeSegJournal(t, tp, 1, deliveryLeaseFile, leaseLine(n1, r1.Hash.String(), obsIDText("1")), openLease)
	writeSegJournal(t, tp, 1, deliveryAckFile, ackLine(n1, obsIDText("1")))
	writeCarry(t, tp, 1, renderCarry(1))
	writeSegJournal(t, tp, 2, deliveryLeaseFile, leaseLine(n2, r2.Hash.String(), obsIDText("2")))
	writeSegJournal(t, tp, 2, deliveryAckFile, ackLine(n2, obsIDText("2")))
	writeCarry(t, tp, 2, renderCarry(2, carryLine(nOpen, open1.Hash.String(), obsIDText("open1"))))

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.False(t, rep.RetentionRootsError)
	for name, r := range map[string]Root{"segment 0 (a)": r0a, "segment 0 (b)": r0b, "segment 1": r1, "segment 2": r2} {
		_, err = tp.Store.GetRoot(ctx, r.Hash)
		require.ErrorIs(t, err, core.ErrNotFound, "the settled lease of %s is released", name)
	}
	_, err = tp.Store.GetRoot(ctx, open1.Hash)
	require.NoError(t, err, "an archived lease with no acknowledgement is retained through the carry")
}

// TestGCSegments_CarriedLeaseIsRetainedUntilAcknowledged: a lease archived unsettled stays retained
// through each later segment's carry, and an acknowledgement recorded in the active segment releases
// it.
func TestGCSegments_CarriedLeaseIsRetainedUntilAcknowledged(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	held := gcSeed(t, tp, "src/held.txt", "leased in segment 0, acknowledged only in segment 3\n")
	installSegAuthority(t, tp, []segSpec{{0, ""}, {1, segBaseRoot("seg1")}, {2, segBaseRoot("seg2")}, {3, segBaseRoot("seg3")}})
	n, obs := deliveryNonce("f"), obsIDText("held")
	writeSegJournal(t, tp, 0, deliveryLeaseFile, leaseLine(n, held.Hash.String(), obs))
	for seg := uint64(1); seg <= 3; seg++ {
		writeCarry(t, tp, seg, renderCarry(seg, carryLine(n, held.Hash.String(), obs)))
	}

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.False(t, rep.RetentionRootsError)
	_, err = tp.Store.GetRoot(ctx, held.Hash)
	require.NoError(t, err, "carried three segments on, the lease is still open and retained")

	writeSegJournal(t, tp, 3, deliveryAckFile, ackLine(n, obs))
	rep, err = tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.False(t, rep.RetentionRootsError)
	_, err = tp.Store.GetRoot(ctx, held.Hash)
	require.ErrorIs(t, err, core.ErrNotFound, "the active segment's acknowledgement settles the carried lease")
}

// TestGCSegments_UnreadableCarryHalts: the active segment's carried-lease file is the only record of
// the archived leases that are still open, so one that is missing, torn, altered, named for another
// segment, or longer than the harvest's bound halts the pass — collecting against it could delete what
// an open lease still needs.
func TestGCSegments_UnreadableCarryHalts(t *testing.T) {
	ctx := context.Background()
	lease := func(label string) string { return carryLine(deliveryNonce("9"), obsIDText(label), obsIDText(label)) }
	valid := renderCarry(1, lease("x"), lease("y"))
	for _, tc := range []struct {
		name string
		raw  []byte // nil: no file at all
		max  int
	}{
		{"missing", nil, 0},
		{"empty", []byte{}, 0},
		{"torn body", valid[:len(valid)-7], 0},
		{"a byte changed", bytes.Replace(valid, []byte(`"v":1,"delivery"`), []byte(`"v":1,"deliverx"`), 1), 0},
		{"a line dropped", append(bytes.SplitAfterN(valid, []byte("\n"), 3)[0], bytes.SplitAfterN(valid, []byte("\n"), 3)[1]...), 0},
		{"another segment's carry", renderCarry(2, lease("x"), lease("y")), 0},
		{"a noncanonical header", bytes.Replace(valid, []byte(`{"v":1,"format"`), []byte(`{"v":1, "format"`), 1), 0},
		{"an unknown format", bytes.Replace(valid, []byte("carried-leases.v1"), []byte("carried-leases.v9"), 1), 0},
		{"over the bound", valid, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.max > 0 {
				prev := dcarryMaxLeases
				dcarryMaxLeases = tc.max
				t.Cleanup(func() { dcarryMaxLeases = prev })
			}
			tp := newTestStore(t)
			doomed := gcSeed(t, tp, "src/doomed.txt", "collectable only if the pass runs\n")
			installSegAuthority(t, tp, []segSpec{{0, ""}, {1, segBaseRoot("seg1")}})
			carry := filepath.Join(segDirOf(tp, 1), "delivery-carried-leases.jsonl")
			require.NoError(t, os.Remove(paths.Long(carry)))
			if tc.raw != nil {
				writeCarry(t, tp, 1, tc.raw)
			}
			rep, err := tp.Store.GC(ctx, forceCollect)
			require.NoError(t, err)
			require.True(t, rep.RetentionRootsError, "an unreadable carry halts the pass")
			require.Equal(t, tc.max > 0, rep.DeliveryCarryOverBound,
				"only a carry past the harvest bound is reported as that bound; damage is not")
			require.Zero(t, rep.DeletedObjects)
			_, err = tp.Store.GetRoot(ctx, doomed.Hash)
			require.NoError(t, err)
		})
	}
}

// TestGCSegments_CarryOverTheBoundIsLoudOnceAndCountedEveryPass: owner decision D6 accepted the
// harvest bound as a documented residual on the condition that reaching it shows. Every pass that
// halts on it is counted; the halt is Loud on the first pass of a run of them, not on every idle tick;
// and a pass whose harvest completes ends the run, so a later halt is Loud again.
func TestGCSegments_CarryOverTheBoundIsLoudOnceAndCountedEveryPass(t *testing.T) {
	prev := dcarryMaxLeases
	dcarryMaxLeases = 1
	t.Cleanup(func() { dcarryMaxLeases = prev })
	ctx := context.Background()
	log := &loudCountingLogger{}
	tp := newTestStore(t, withLog(log))
	installSegAuthority(t, tp, []segSpec{{0, ""}, {1, segBaseRoot("seg1")}})
	lease := func(label string) string { return carryLine(deliveryNonce("9"), obsIDText(label), obsIDText(label)) }
	over, within := renderCarry(1, lease("x"), lease("y")), renderCarry(1, lease("x"))

	writeCarry(t, tp, 1, over)
	for pass := 1; pass <= 3; pass++ {
		rep, err := tp.Store.GC(ctx, forceCollect)
		require.NoError(t, err)
		require.True(t, rep.DeliveryCarryOverBound)
		require.Equal(t, int64(pass), tp.counter(CounterGCDeliveryCarryOverBound), "every halted pass is counted")
		require.Equal(t, 1, log.count(), "the halt is Loud once for the run of halted passes")
	}

	writeCarry(t, tp, 1, within)
	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.False(t, rep.RetentionRootsError, "a carry within the bound is harvested")
	require.False(t, rep.DeliveryCarryOverBound)

	writeCarry(t, tp, 1, over)
	rep, err = tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.True(t, rep.DeliveryCarryOverBound)
	require.Equal(t, 2, log.count(), "a new run of halted passes is Loud again")
	require.Equal(t, int64(4), tp.counter(CounterGCDeliveryCarryOverBound))
}
