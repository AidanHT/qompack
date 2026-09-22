package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// These tests prove the offline delivery-seal tool now validates a ROTATED (segmented) store across its
// authority, every committed segment and the generation store — read-only — instead of checking only
// the legacy segment 0 and reporting the whole store fine while the active segment is corrupt.

// buildRotatedStore leases n deliveries through a live journal with the rollover seam on (threshold 1,
// so every lease after the first rotates), acknowledging the first if ackFirst, then releases the lock
// so the offline tool can take it. It returns the active segment and the first lease.
func buildRotatedStore(t *testing.T, root string, sess core.SessionID, n int, ackFirst bool) (uint64, deliveryLease) {
	t.Helper()
	setRollover(t, 1)
	ctx := context.Background()
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	j, err := lock.openDeliveryJournal()
	require.NoError(t, err)
	var first deliveryLease
	for i := 0; i < n; i++ {
		l, err := j.lease(ctx, genNonce(i), sess, testDeliveryRequest(genNonce(i)))
		require.NoError(t, err)
		if i == 0 {
			first = l
		}
	}
	if ackFirst {
		require.NoError(t, j.acknowledge(ctx, first.Delivery, first.ObservationID, core.Hash{}))
	}
	active := j.segment
	require.NoError(t, lock.Release())
	return active, first
}

func checkSeal(t *testing.T, root string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := RepairDeliverySeal(DeliverySealOptions{
		ProjectRoot: root, Check: true, Out: &out, Clock: newFakeClock(epoch),
	})
	return out.String(), err
}

// digestState hashes every file under .qompack/state (names + bytes), which excludes the daemon
// lock/heartbeat under .qompack/run — the same exclusion the legacy byte-tree test uses.
func digestState(t *testing.T, root string) string {
	t.Helper()
	state := paths.Of(root).State
	var lines []string
	err := filepath.Walk(paths.Long(state), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		sum := sha256.Sum256(b)
		rel, _ := filepath.Rel(state, p)
		lines = append(lines, filepath.ToSlash(rel)+"="+hex.EncodeToString(sum[:]))
		return nil
	})
	require.NoError(t, err)
	sort.Strings(lines)
	whole := sha256.Sum256([]byte(join(lines)))
	return hex.EncodeToString(whole[:])
}

func join(lines []string) string {
	var b bytes.Buffer
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

// TestDeliverySealSegment_RefusesCorruptActiveSegmentThatLegacyOnlyAccepts is the negative control and
// the fix in one test: a rotated store whose LEGACY segment 0 is intact (so the old legacy-only check
// would report the whole store "checked") but whose ACTIVE segment's lease journal is corrupt. The old
// behavior is demonstrated by loading segment 0 alone successfully; the corrected tool refuses.
func TestDeliverySealSegment_RefusesCorruptActiveSegmentThatLegacyOnlyAccepts(t *testing.T) {
	root := t.TempDir()
	active, _ := buildRotatedStore(t, root, "s", 6, false)
	require.GreaterOrEqual(t, active, uint64(2), "the store rotated to a real active segment")
	state := paths.Of(root).State

	// Negative control: segment 0 (the legacy four files) is independently valid, which is exactly what
	// the pre-fix tool checked — so it would have reported this store fine. Segment 0 is archived, so its
	// seal is the frozen old-reader barrier (delivery_frozen_seal.go), which the legacy seal reader refuses
	// by design; the journal is loaded against the position that frozen seal names, with the same scan.
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	seg0 := newDeliveryJournal(lock, filepath.Join(state, deliveryLeaseFile))
	seg0.ackPath = filepath.Join(state, deliveryAckFile)
	frozen, isFrozen, err := readFrozenSealFile(seg0.positionPath(), deliveryChainSeed)
	require.NoError(t, err)
	require.True(t, isFrozen, "an archived segment 0 carries the frozen seal")
	_, err = seg0.loadFrom(frozen, nil)
	require.NoError(t, err, "segment 0 loads on its own; a legacy-only check accepts the whole store")
	require.NoError(t, lock.Release())

	// Corrupt the ACTIVE segment's lease journal.
	activePath := segmentLeasePath(state, active)
	f, err := os.OpenFile(paths.Long(activePath), os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	_, err = f.Write([]byte("this is not a canonical lease line\n"))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// The corrected tool refuses, naming the active segment.
	outStr, err := checkSeal(t, root)
	require.Error(t, err, "a corrupt active segment must be refused, not reported checked")
	require.Contains(t, err.Error(), "segment")
	require.NotContains(t, outStr, "checked", "a refused store is never reported as checked")
}

// TestDeliverySealSegment_ValidMultiSegmentTreePasses: a complete, valid rotated store passes the
// read-only check across all segments and the generation store.
func TestDeliverySealSegment_ValidMultiSegmentTreePasses(t *testing.T) {
	root := t.TempDir()
	active, _ := buildRotatedStore(t, root, "s", 6, false)
	require.GreaterOrEqual(t, active, uint64(2))

	before := digestState(t, root)
	out, err := checkSeal(t, root)
	require.NoError(t, err, "a valid multi-segment store passes")
	require.Contains(t, out, "checked")
	require.Contains(t, out, "segment 0 lease journal")
	require.Contains(t, out, "generation store")

	require.Equal(t, before, digestState(t, root), "--check writes nothing to the state tree")
}

// TestDeliverySealSegment_ArchivedAckExactJoinPasses: an ack for a lease archived into an earlier
// segment is validated against its original generation identity during the read-only check.
func TestDeliverySealSegment_ArchivedAckExactJoinPasses(t *testing.T) {
	root := t.TempDir()
	active, first := buildRotatedStore(t, root, "s", 6, true) // acks the archived arrival-1 lease
	require.GreaterOrEqual(t, active, uint64(2))
	require.Equal(t, uint64(1), first.ArrivalSeq)

	out, err := checkSeal(t, root)
	require.NoError(t, err, "an archived ACK that joins its original lease passes")
	require.Contains(t, out, "archived ACKs joined to their original leases")
}

// TestDeliverySealSegment_MissingActiveSegmentFilesRefused: an authority naming a segment whose files
// are missing is refused (never read as an empty store).
func TestDeliverySealSegment_MissingActiveSegmentFilesRefused(t *testing.T) {
	root := t.TempDir()
	active, _ := buildRotatedStore(t, root, "s", 6, false)
	state := paths.Of(root).State

	require.NoError(t, os.Remove(paths.Long(segmentLeasePath(state, active))))
	_, err := checkSeal(t, root)
	require.Error(t, err, "a segment whose lease journal is missing must be refused")
}

// TestDeliverySealSegment_MissingHeadRefused: an authority log surviving without its head is head loss
// and is refused, never checked as a legacy tree.
func TestDeliverySealSegment_MissingHeadRefused(t *testing.T) {
	root := t.TempDir()
	buildRotatedStore(t, root, "s", 6, false)
	state := paths.Of(root).State

	require.NoError(t, os.Remove(paths.Long(filepath.Join(state, deliverySegmentHeadFile))))
	_, err := checkSeal(t, root)
	require.Error(t, err, "a surviving authority log without its head is head loss, not a legacy tree")
}

// TestDeliverySealSegment_NewerAuthoritySchemaRefused: a head at an unknown version is refused.
func TestDeliverySealSegment_NewerAuthoritySchemaRefused(t *testing.T) {
	root := t.TempDir()
	buildRotatedStore(t, root, "s", 6, false)
	state := paths.Of(root).State
	headPath := filepath.Join(state, deliverySegmentHeadFile)

	raw, err := os.ReadFile(paths.Long(headPath))
	require.NoError(t, err)
	var h segHead
	require.NoError(t, json.Unmarshal(raw, &h))
	h.Version = deliverySegmentVersion + 1
	b, err := json.Marshal(h)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(headPath), b, 0o600))

	_, err = checkSeal(t, root)
	require.Error(t, err, "an authority head at an unknown version is refused")
}

// TestDeliverySealSegment_ConversionRefusedPreservesBytes: --to v1 on a segmented store is refused and
// writes nothing.
func TestDeliverySealSegment_ConversionRefusedPreservesBytes(t *testing.T) {
	root := t.TempDir()
	buildRotatedStore(t, root, "s", 6, false)

	before := digestState(t, root)
	var out bytes.Buffer
	err := RepairDeliverySeal(DeliverySealOptions{
		ProjectRoot: root, ToV1: true, Out: &out, Clock: newFakeClock(epoch),
	})
	require.Error(t, err, "--to v1 on a segmented store is refused")
	require.Contains(t, err.Error(), "refused")
	require.Equal(t, before, digestState(t, root), "a refused conversion writes nothing to the state tree")
}

// TestDeliverySealSegment_UnmigratedLegacyTreeStillChecks: a genuinely unmigrated tree (no authority,
// no migration evidence) keeps the existing legacy-only behavior.
func TestDeliverySealSegment_UnmigratedLegacyTreeStillChecks(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	// A plain legacy store: seam OFF, so no authority or generation store is created.
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	j, err := lock.openDeliveryJournal()
	require.NoError(t, err)
	_, err = j.lease(ctx, genNonce(0), "s", testDeliveryRequest("a"))
	require.NoError(t, err)
	require.NoError(t, lock.Release())

	out, err := checkSeal(t, root)
	require.NoError(t, err, "an unmigrated legacy tree still checks as before")
	require.Contains(t, out, "checked")
}

// genRootPagePath is the on-disk root pointer for a committed root (its content hash names it); the
// pointer names the pack record that holds the root page's bytes.
func genRootPagePath(state, rootHex string) string {
	b, _ := hex.DecodeString(rootHex)
	shard := hex.EncodeToString(b[:1])
	return filepath.Join(state, "delivery-generations", "pages", shard, rootHex+radixRootSuffix)
}

// corruptGenRootPage flips one byte of a committed root's PAGE bytes inside the pack its pointer names,
// so the page no longer hashes to its name (the pointer itself stays intact).
func corruptGenRootPage(t *testing.T, state, rootHex string) {
	t.Helper()
	pointer, err := os.ReadFile(paths.Long(genRootPagePath(state, rootHex)))
	require.NoError(t, err)
	require.Len(t, pointer, radixRootFileLen)
	pack := binary.BigEndian.Uint64(pointer[len(radixRootMagic):])
	off := binary.BigEndian.Uint64(pointer[len(radixRootMagic)+8:])
	packPath := filepath.Join(state, "delivery-generations", "pages", radixPacksDir, radixPackName(pack))
	flipPackByte(t, packPath, int64(off)+radixRecordHeaderLen+7)
}

// TestDeliverySealSegment_CorruptPredecessorPageWithFirstArrivalRefused is the fix for main's #1: the
// active segment's first lease is a BRAND-NEW session at arrival 1, whose predecessor lookup at the
// segment's base root hits a corrupt page. Swallowing that error into "new session" would let the
// density check (1 == 0+1) pass; propagating it refuses. This is the case the old closure got wrong.
func TestDeliverySealSegment_CorruptPredecessorPageWithFirstArrivalRefused(t *testing.T) {
	root := t.TempDir()
	state := paths.Of(root).State
	setRollover(t, 1)
	ctx := context.Background()

	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	j, err := lock.openDeliveryJournal()
	require.NoError(t, err)
	for i := 0; i < 3; i++ { // session A: arrivals 1,2,3 across segments 0,1,2
		_, err := j.lease(ctx, genNonce(i), "A", testDeliveryRequest(genNonce(i)))
		require.NoError(t, err)
	}
	bl, err := j.lease(ctx, genNonce(3), "B", testDeliveryRequest(genNonce(3))) // NEW session B, arrival 1
	require.NoError(t, err)
	require.Equal(t, uint64(1), bl.ArrivalSeq)
	active := j.segment
	require.GreaterOrEqual(t, active, uint64(1))
	require.NoError(t, lock.Release())

	raw, err := os.ReadFile(paths.Long(filepath.Join(state, deliverySegmentHeadFile)))
	require.NoError(t, err)
	var h segHead
	require.NoError(t, json.Unmarshal(raw, &h))
	require.Equal(t, active, h.Active)
	require.NotEmpty(t, h.BaseRoot, "the active segment has a real predecessor root")

	require.FileExists(t, genRootPagePath(state, h.BaseRoot))
	corruptGenRootPage(t, state, h.BaseRoot)

	_, err = checkSeal(t, root)
	require.Error(t, err, "a corrupt predecessor page under a first-arrival lease must refuse, not be swallowed")
}

// TestDeliverySealSegment_TornGenerationManifestRefused is the fix for main's #2: a torn trailing record
// in the generation manifest refuses (the bounded stream reader detects the partial line).
func TestDeliverySealSegment_TornGenerationManifestRefused(t *testing.T) {
	root := t.TempDir()
	buildRotatedStore(t, root, "s", 6, false)
	state := paths.Of(root).State

	manifest := filepath.Join(state, "delivery-generations", "manifest.jsonl")
	f, err := os.OpenFile(paths.Long(manifest), os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	_, err = f.Write([]byte(`{"v":1,"seq":999`)) // torn: no closing brace/newline
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, err = checkSeal(t, root)
	require.Error(t, err, "a torn generation manifest tail refuses")
}

// TestDeliverySealSegment_NonDenseActiveAuthorityRefused is the fix for main's #4: a crafted authority
// whose active segment jumps (1 → 3) with an otherwise-valid chain is refused — active must advance by
// exactly one, matching the producer's dense protocol.
func TestDeliverySealSegment_NonDenseActiveAuthorityRefused(t *testing.T) {
	root := t.TempDir()
	state := paths.Of(root).State
	require.NoError(t, os.MkdirAll(paths.Long(state), 0o700))

	mk := func(seq int64, active uint64, base string, prev radixHash) (segTransition, radixHash) {
		ch := segChain(prev, seq, active, base)
		return segTransition{Version: deliverySegmentVersion, Seq: seq, Active: active, BaseRoot: base, Chain: hex.EncodeToString(ch[:])}, ch
	}
	line := func(r segTransition) []byte {
		b, err := json.Marshal(r)
		require.NoError(t, err)
		return append(b, '\n')
	}
	r0, c0 := mk(0, 0, "", segChainSeed)
	r1, c1 := mk(1, 1, segRoot(1), c0)
	r2, c2 := mk(2, 3, segRoot(3), c1) // active jumps 1 → 3 with a valid chain

	var log []byte
	log = append(log, line(r0)...)
	log = append(log, line(r1)...)
	beforeR2 := len(log)
	log = append(log, line(r2)...)
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(state, deliverySegmentLogFile)), log, 0o600))

	head := segHead{
		Version: deliverySegmentVersion, Format: deliverySegmentFormat, Seq: 2, Active: 3,
		BaseRoot: segRoot(3), Chain: hex.EncodeToString(c2[:]), LogBytes: int64(len(log)), LastLen: int64(len(log) - beforeR2),
	}
	hb, err := json.Marshal(head)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(state, deliverySegmentHeadFile)), hb, 0o600))

	_, err = checkSeal(t, root)
	require.Error(t, err, "a non-dense active progression in the authority is refused")
}

// TestDeliverySealSegment_SegmentDirNotADirectoryRefused is part of the fix for main's #3: a segment
// path that is not a directory (a regular file swapped in) is rejected by the pinned-child identity
// check before any reader runs.
func TestDeliverySealSegment_SegmentDirNotADirectoryRefused(t *testing.T) {
	root := t.TempDir()
	active, _ := buildRotatedStore(t, root, "s", 6, false)
	state := paths.Of(root).State

	dir := filepath.Join(state, deliverySegmentsDir, segmentSeqName(active))
	require.NoError(t, os.RemoveAll(paths.Long(dir)))
	require.NoError(t, os.WriteFile(paths.Long(dir), []byte("not a directory"), 0o600))

	_, err := checkSeal(t, root)
	require.Error(t, err, "a segment path that is not a directory is refused")
}
