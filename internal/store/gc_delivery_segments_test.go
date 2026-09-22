package store

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Segmented delivery GC integration (SP20-D4; delivery-segment-retention-decision.md). These tests use
// LITERAL protocol fixtures — the exact head + chained transition log + per-segment journal files the
// daemon's (unshipped) producer writes — and prove GC harvests every committed segment's open leases,
// settles a lease from an ACK in a LATER segment, and HALTS (never reverts to legacy) on any torn,
// missing, conflicting or unknown authority. A true cross-package byte-agreement test against the
// daemon producer is owed to main (see gc-segment-integration-work.md).

// segSpec is one transition: the active segment after it and its preceding base root (empty for the
// active-0 legacy transition).
type segSpec struct {
	active   uint64
	baseRoot string
}

// segBaseRoot builds a valid 32-byte-hex base root from a label.
func segBaseRoot(label string) string {
	h := dsegDigest("test.segment.base", []byte(label))
	return hex.EncodeToString(h[:])
}

// buildSegAuthority renders the transition log bytes and the head bytes for a dense chain of specs.
// specs[0] MUST be the active-0 legacy transition {0, ""}.
func buildSegAuthority(t *testing.T, specs []segSpec) (logBytes, headBytes []byte) {
	t.Helper()
	require.NotEmpty(t, specs)
	require.Equal(t, uint64(0), specs[0].active, "specs[0] must be active-0")
	prev := dsegChainSeed
	var buf bytes.Buffer
	var lastLen int64
	var last dsegTransition
	for i, sp := range specs {
		chain := dsegChain(prev, int64(i), sp.active, sp.baseRoot)
		rec := dsegTransition{
			Version: dsegVersion, Seq: int64(i), Active: sp.active,
			BaseRoot: sp.baseRoot, Chain: hex.EncodeToString(chain[:]),
		}
		line, err := json.Marshal(rec)
		require.NoError(t, err)
		line = append(line, '\n')
		buf.Write(line)
		lastLen, last, prev = int64(len(line)), rec, chain
	}
	head := dsegHead{
		Version: dsegVersion, Format: dsegFormat, Seq: last.Seq, Active: last.Active,
		BaseRoot: last.BaseRoot, Chain: last.Chain, LogBytes: int64(buf.Len()), LastLen: lastLen,
	}
	hb, err := json.Marshal(head)
	require.NoError(t, err)
	return buf.Bytes(), hb
}

func segDirOf(tp *testProject, active uint64) string {
	l := paths.Of(tp.Root)
	if active == 0 {
		return l.State
	}
	return filepath.Join(l.State, dsegDir, fmt.Sprintf("%0*d", dsegSeqWidth, active))
}

// installSegAuthority writes the head + log and creates every committed segment's directory with all
// FOUR journal/seal files: empty lease and ack journals, and non-empty lease and ack position seals
// (the seals are structurally required — a committed segment is not proved by two files). Tests then
// overwrite the specific files they exercise.
func installSegAuthority(t *testing.T, tp *testProject, specs []segSpec) {
	t.Helper()
	l := paths.Of(tp.Root)
	logB, headB := buildSegAuthority(t, specs)
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(l.State, dsegLogFile)), logB, 0o600))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(l.State, dsegHeadFile)), headB, 0o600))
	for _, sp := range specs {
		installSegmentFiles(t, tp, sp.active)
	}
}

// installSegmentFiles creates a segment directory with its four journal/seal files: empty journals and
// non-empty (structurally complete) seals.
func installSegmentFiles(t *testing.T, tp *testProject, active uint64) {
	t.Helper()
	dir := segDirOf(tp, active)
	require.NoError(t, os.MkdirAll(paths.Long(dir), 0o700))
	for _, jn := range []string{deliveryLeaseFile, deliveryAckFile} {
		p := filepath.Join(dir, jn)
		if _, err := os.Stat(paths.Long(p)); os.IsNotExist(err) {
			require.NoError(t, os.WriteFile(paths.Long(p), nil, 0o600))
		}
	}
	// Real v1 EMPTY position seals — exactly what a fresh segment's createEmptyDeliveryPositionV1 writes
	// (a canonical {"v":1,"bytes":0,"count":0,"chain":"<journal seed>"} with no trailing newline). The
	// lease and ack seals carry their own journal's seed.
	writeV1Seal(t, filepath.Join(dir, deliveryLeasePositionFile), dsealLeaseSeed)
	writeV1Seal(t, filepath.Join(dir, deliveryAckPositionFile), dsealAckSeed)
}

// writeV1Seal writes a canonical v1 empty position seal for a journal with the given chain seed.
func writeV1Seal(t *testing.T, path string, seed core.Hash) {
	t.Helper()
	b, err := json.Marshal(dsealPosition{Version: dsealV1Version, Chain: seed})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(path), b, 0o600))
}

// buildV2Seal builds a real, fresh v2 A/B image: slot a holds a seq-1 in-bounds record correctly summed
// for the given journal, slot b is empty — exactly what newSealImage produces for a running segment.
func buildV2Seal(t *testing.T, chainDomain string, seed core.Hash) []byte {
	t.Helper()
	img := bytes.Repeat([]byte{dsealPad}, dsealFileSize)
	copy(img, dsealPrefixA)
	copy(img[dsealStride:], dsealPrefixB)
	copy(img[dsealFileSize-len(dsealSuffix):], dsealSuffix)
	copy(img[dsealSlotBOffset:], dsealEmpty) // slot b empty
	chain := core.HashBytes("test.seal.chain", []byte("v2"))
	rec := dsealRecord{Seq: 1, Bytes: 5, Count: 1, Chain: chain}
	rec.Sum = dsealSum(chainDomain, 'a', rec.Seq, rec.Bytes, rec.Count, rec.Chain)
	body, err := json.Marshal(rec)
	require.NoError(t, err)
	require.LessOrEqual(t, len(body), dsealSlotRegion)
	region := bytes.Repeat([]byte{dsealPad}, dsealSlotRegion)
	copy(region, body)
	copy(img[dsealSlotAOffset:], region)
	require.True(t, dsealSelect(img, chainDomain, seed), "guard: the built v2 image must be selectable")
	return img
}

func writeSegJournal(t *testing.T, tp *testProject, active uint64, name string, lines ...string) {
	t.Helper()
	dir := segDirOf(tp, active)
	require.NoError(t, os.MkdirAll(paths.Long(dir), 0o700))
	var buf bytes.Buffer
	for _, ln := range lines {
		buf.WriteString(ln)
		buf.WriteByte('\n')
	}
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(dir, name)), buf.Bytes(), 0o600))
}

func leaseLine(nonce, requestHex, obs string) string {
	return fmt.Sprintf(`{"v":1,"delivery":%q,"request":%q,"observation_id":%q}`, nonce, requestHex, obs)
}

func ackLine(nonce, obs string) string {
	return fmt.Sprintf(`{"v":1,"delivery":%q,"observation_id":%q}`, nonce, obs)
}

// TestGCSegments_HarvestsOldestLeaseAcrossRotation: after a rotation to segment 1, a lease still open
// in the OLDEST segment (0) and a lease in the new segment (1) are both harvested. The pre-integration
// reader saw only segment 0 and swept the segment-1 lease root.
func TestGCSegments_HarvestsOldestLeaseAcrossRotation(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	oldRoot := gcSeed(t, tp, "src/old.txt", "leased in the oldest segment, still open\n")
	newRoot := gcSeed(t, tp, "src/new.txt", "leased in the active segment\n")

	installSegAuthority(t, tp, []segSpec{{0, ""}, {1, segBaseRoot("seg1")}})
	writeSegJournal(t, tp, 0, deliveryLeaseFile,
		leaseLine(deliveryNonce("1"), oldRoot.Hash.String(), obsIDText("old")))
	writeSegJournal(t, tp, 1, deliveryLeaseFile,
		leaseLine(deliveryNonce("2"), newRoot.Hash.String(), obsIDText("new")))

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.False(t, rep.RetentionRootsError)
	_, err = tp.Store.GetRoot(ctx, oldRoot.Hash)
	require.NoError(t, err, "an open lease in the oldest segment must be retained across a rotation")
	_, err = tp.Store.GetRoot(ctx, newRoot.Hash)
	require.NoError(t, err, "an open lease in the active segment must be retained")
}

// TestGCSegments_AckInLaterSegmentSettlesOlderLease: an acknowledgement recorded in the ACTIVE segment
// settles a lease opened in the OLDEST segment, releasing its root; a lease with no matching ack is
// retained. The join is exact nonce+observation+version across segments.
func TestGCSegments_AckInLaterSegmentSettlesOlderLease(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	settled := gcSeed(t, tp, "src/settled.txt", "leased in seg0, acknowledged in seg1\n")
	openR := gcSeed(t, tp, "src/open.txt", "leased in seg0, never acknowledged\n")

	installSegAuthority(t, tp, []segSpec{{0, ""}, {1, segBaseRoot("seg1")}})
	nSettled, nOpen := deliveryNonce("1"), deliveryNonce("2")
	oSettled := obsIDText("settled")
	writeSegJournal(t, tp, 0, deliveryLeaseFile,
		leaseLine(nSettled, settled.Hash.String(), oSettled),
		leaseLine(nOpen, openR.Hash.String(), obsIDText("open")))
	// The ACK lands in the LATER segment's ack journal, under the exact identity.
	writeSegJournal(t, tp, 1, deliveryAckFile, ackLine(nSettled, oSettled))

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.False(t, rep.RetentionRootsError)
	_, err = tp.Store.GetRoot(ctx, settled.Hash)
	require.ErrorIs(t, err, core.ErrNotFound, "an ack in a later segment must settle the older lease")
	_, err = tp.Store.GetRoot(ctx, openR.Hash)
	require.NoError(t, err, "a lease with no matching ack stays open across segments")
}

// TestGCSegments_MissingCommittedSegmentHalts: the authority commits segment 1 but its directory/lease
// journal is absent. A committed segment's files are REQUIRED; a missing one halts the pass rather than
// harvesting a short set.
func TestGCSegments_MissingCommittedSegmentHalts(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	doomed := gcSeed(t, tp, "src/doomed.txt", "collectable only if the pass runs\n")

	installSegAuthority(t, tp, []segSpec{{0, ""}, {1, segBaseRoot("seg1")}})
	// Remove segment 1's directory that installSegAuthority created: the authority still names it.
	require.NoError(t, os.RemoveAll(paths.Long(segDirOf(tp, 1))))

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.True(t, rep.RetentionRootsError, "a committed segment whose files are missing must halt the pass")
	require.Zero(t, rep.DeletedObjects)
	_, err = tp.Store.GetRoot(ctx, doomed.Hash)
	require.NoError(t, err)
}

// TestGCSegments_MissingSealHalts: a committed segment is proved by FOUR files, not two. If a required
// position seal is absent, the segment is structurally incomplete and the pass halts.
func TestGCSegments_MissingSealHalts(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	doomed := gcSeed(t, tp, "src/doomed.txt", "collectable only if the pass runs\n")

	installSegAuthority(t, tp, []segSpec{{0, ""}, {1, segBaseRoot("seg1")}})
	// Remove segment 1's lease position seal; its lease and ack journals remain.
	require.NoError(t, os.Remove(paths.Long(filepath.Join(segDirOf(tp, 1), deliveryLeasePositionFile))))

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.True(t, rep.RetentionRootsError, "a committed segment missing a required seal must halt")
	require.Zero(t, rep.DeletedObjects)
	_, err = tp.Store.GetRoot(ctx, doomed.Hash)
	require.NoError(t, err)
}

// sealCase drives the seal-structure negative controls: each overwrites a committed segment's lease
// seal with the given bytes and asserts the pass halts, then the doomed root survives.
func sealCase(t *testing.T, mutate func(t *testing.T, sealPath string)) {
	t.Helper()
	tp := newTestStore(t)
	ctx := context.Background()
	doomed := gcSeed(t, tp, "src/doomed.txt", "collectable only if the pass runs\n")
	installSegAuthority(t, tp, []segSpec{{0, ""}, {1, segBaseRoot("seg1")}})
	mutate(t, paths.Long(filepath.Join(segDirOf(tp, 1), deliveryLeasePositionFile)))

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.True(t, rep.RetentionRootsError, "an unsupported/malformed seal must halt")
	require.Zero(t, rep.DeletedObjects)
	_, err = tp.Store.GetRoot(ctx, doomed.Hash)
	require.NoError(t, err)
}

// TestGCSegments_SealGarbageHalts: arbitrary non-empty bytes are not a seal.
func TestGCSegments_SealGarbageHalts(t *testing.T) {
	sealCase(t, func(t *testing.T, p string) {
		require.NoError(t, os.WriteFile(p, []byte("not a seal at all, just bytes\n"), 0o600))
	})
}

// TestGCSegments_SealUnknownSchemaHalts: a v1-shaped sidecar with an unknown version is refused.
func TestGCSegments_SealUnknownSchemaHalts(t *testing.T) {
	sealCase(t, func(t *testing.T, p string) {
		require.NoError(t, os.WriteFile(p, []byte(`{"v":9,"bytes":0,"count":0,"chain":"`+
			dsealLeaseSeed.String()+`"}`), 0o600))
	})
}

// TestGCSegments_SealTornV2Halts: a full-size v2 image whose static suffix byte is corrupt (a torn or
// foreign write) is refused — the layout no longer matches and it is not a valid v1 sidecar either.
func TestGCSegments_SealTornV2Halts(t *testing.T) {
	sealCase(t, func(t *testing.T, p string) {
		img := buildV2Seal(t, dsealLeaseChainDomain, dsealLeaseSeed)
		img[len(img)-1] = ' ' // clobber the closing '}' — no longer a v2 image
		require.NoError(t, os.WriteFile(p, img, 0o600))
	})
}

// TestGCSegments_SealWrongJournalSumHalts: a v2 record correctly summed for the ACK journal, planted in
// the LEASE seal, fails the lease seal's sum check (the sum binds a record to its journal).
func TestGCSegments_SealWrongJournalSumHalts(t *testing.T) {
	sealCase(t, func(t *testing.T, p string) {
		img := buildV2Seal(t, dsealAckChainDomain, dsealAckSeed) // summed for the ACK journal
		require.NoError(t, os.WriteFile(p, img, 0o600))
	})
}

// TestGCSegments_ValidV2SealAccepted: a real, running-producer v2 A/B seal is accepted (no halt). This
// pins that the store reader takes the fixed-size v2 format the running writer uses, not only v1.
func TestGCSegments_ValidV2SealAccepted(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	leased := gcSeed(t, tp, "src/leased.txt", "held by an open lease; seg has a v2 seal\n")
	installSegAuthority(t, tp, []segSpec{{0, ""}, {1, segBaseRoot("seg1")}})
	// Replace segment 1's v1 seals with real v2 A/B images (each for its own journal).
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(segDirOf(tp, 1), deliveryLeasePositionFile)),
		buildV2Seal(t, dsealLeaseChainDomain, dsealLeaseSeed), 0o600))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(segDirOf(tp, 1), deliveryAckPositionFile)),
		buildV2Seal(t, dsealAckChainDomain, dsealAckSeed), 0o600))
	writeSegJournal(t, tp, 1, deliveryLeaseFile,
		leaseLine(deliveryNonce("2"), leased.Hash.String(), obsIDText("v2")))

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.False(t, rep.RetentionRootsError, "a valid v2 seal must be accepted")
	_, err = tp.Store.GetRoot(ctx, leased.Hash)
	require.NoError(t, err, "a segment with a valid v2 seal is harvested normally")
}

// TestGCSegments_NonDenseActiveHalts: the active segment index must be DENSE with the transition
// sequence (active == seq). A log whose active jumps is refused — GC never infers over a gap.
func TestGCSegments_NonDenseActiveHalts(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	doomed := gcSeed(t, tp, "src/doomed.txt", "collectable only if the pass runs\n")

	// seq 1 names active 2 — a non-dense jump the reader must refuse.
	installSegAuthority(t, tp, []segSpec{{0, ""}, {2, segBaseRoot("jump")}})

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.True(t, rep.RetentionRootsError, "a non-dense active must halt")
	_, err = tp.Store.GetRoot(ctx, doomed.Hash)
	require.NoError(t, err)
}

// TestGCSegments_TornLogHalts: a torn trailing transition record (a crash mid-append shape) is not a
// clean authority; the reader refuses rather than adopting a partial chain.
func TestGCSegments_TornLogHalts(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	doomed := gcSeed(t, tp, "src/doomed.txt", "collectable only if the pass runs\n")
	l := paths.Of(tp.Root)

	installSegAuthority(t, tp, []segSpec{{0, ""}, {1, segBaseRoot("seg1")}})
	// Append a torn (unterminated, partial) record to the log.
	logPath := paths.Long(filepath.Join(l.State, dsegLogFile))
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	_, werr := f.WriteString(`{"v":1,"seq":2,"active":2,"base_root":"`)
	require.NoError(t, werr)
	require.NoError(t, f.Close())

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.True(t, rep.RetentionRootsError, "a torn transition log must halt, never revert to legacy")
	_, err = tp.Store.GetRoot(ctx, doomed.Hash)
	require.NoError(t, err)
}

// TestGCSegments_UnknownSchemaHeadHalts: a head carrying an unknown format string is authority this
// build cannot vouch for; it halts rather than guessing.
func TestGCSegments_UnknownSchemaHeadHalts(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	doomed := gcSeed(t, tp, "src/doomed.txt", "collectable only if the pass runs\n")
	l := paths.Of(tp.Root)

	installSegAuthority(t, tp, []segSpec{{0, ""}, {1, segBaseRoot("seg1")}})
	// Overwrite the head with a future/unknown format.
	bad := `{"v":2,"format":"qompack.delivery.segments.v99","seq":0,"active":0,"base_root":"","chain":"` +
		hex.EncodeToString(dsegChainSeed[:]) + `","log_bytes":1,"last_len":1}`
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(l.State, dsegHeadFile)), []byte(bad), 0o600))

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.True(t, rep.RetentionRootsError, "an unknown authority schema must halt")
	_, err = tp.Store.GetRoot(ctx, doomed.Hash)
	require.NoError(t, err)
}

// TestGCSegments_MigrationEvidenceWithoutAuthorityHalts: a delivery-segments directory survives but the
// head and log are gone (head loss with history). The reader must NOT reopen the legacy segment and
// sweep — it halts.
func TestGCSegments_MigrationEvidenceWithoutAuthorityHalts(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	doomed := gcSeed(t, tp, "src/doomed.txt", "collectable only if the pass runs\n")
	l := paths.Of(tp.Root)

	// A segment directory exists (migration happened) but there is no head and no log.
	require.NoError(t, os.MkdirAll(paths.Long(segDirOf(tp, 1)), 0o700))

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.True(t, rep.RetentionRootsError, "migration evidence without an authority is head loss, not legacy")
	_, err = tp.Store.GetRoot(ctx, doomed.Hash)
	require.NoError(t, err)
	_ = l
}

// TestGCSegments_StagedSegmentLeaseRetained: a staged (uncommitted) segment directory — present on disk
// but not named by the committed authority — has its lease roots conservatively retained, never deleted.
func TestGCSegments_StagedSegmentLeaseRetained(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	staged := gcSeed(t, tp, "src/staged.txt", "leased in a staged, uncommitted segment\n")

	// Authority commits only segments 0 and 1; segment 2 is staged on disk but uncommitted.
	installSegAuthority(t, tp, []segSpec{{0, ""}, {1, segBaseRoot("seg1")}})
	writeSegJournal(t, tp, 2, deliveryLeaseFile,
		leaseLine(deliveryNonce("7"), staged.Hash.String(), obsIDText("staged")))

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.False(t, rep.RetentionRootsError)
	_, err = tp.Store.GetRoot(ctx, staged.Hash)
	require.NoError(t, err, "a staged segment's lease roots are conservatively retained")
}

// TestGCSegments_FrontierRecheckDetectsLogTailRotation directly exercises the post-harvest stable-
// frontier recheck for the case main flagged: a rotation fsyncs a new committed transition to the LOG
// before it checkpoints the head, so witnessing the head alone would miss it. The recheck compares the
// whole log against the resolve-time witness and refuses when it has grown, even though the head is
// unchanged.
func TestGCSegments_FrontierRecheckDetectsLogTailRotation(t *testing.T) {
	tp := newTestStore(t)
	l := paths.Of(tp.Root)
	installSegAuthority(t, tp, []segSpec{{0, ""}, {1, segBaseRoot("seg1")}})

	budget := newGCBudget(context.Background(), time.Time{})
	view, err := tp.Store.resolveDeliverySegments(budget)
	require.NoError(t, err)
	require.False(t, view.legacy)

	// A committed log tail lands (fsynced) before the head checkpoint would; the head is untouched.
	rotatedLog, _ := buildSegAuthority(t, []segSpec{{0, ""}, {1, segBaseRoot("seg1")}, {2, segBaseRoot("seg2")}})
	installSegmentFiles(t, tp, 2)
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(l.State, dsegLogFile)), rotatedLog, 0o600))

	err = tp.Store.dsegRecheckFrontier(view)
	require.ErrorIs(t, err, errRetentionRootsUnavailable,
		"a committed log tail that lands during the harvest must be detected before collection")
}

// TestGCSegments_UnmigratedTreeStaysLegacy: with no authority and no migration evidence, GC harvests
// exactly the legacy segment 0 lease journal — the pre-segment behavior, unchanged.
func TestGCSegments_UnmigratedTreeStaysLegacy(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	l := paths.Of(tp.Root)
	leased := gcSeed(t, tp, "src/legacy.txt", "held by a legacy open lease\n")
	writeJSONLLines(t, l.State, deliveryLeaseFile,
		map[string]any{"v": 1, "delivery": deliveryNonce("1"), "request": leased.Hash.String()})

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.False(t, rep.RetentionRootsError)
	_, err = tp.Store.GetRoot(ctx, leased.Hash)
	require.NoError(t, err, "the legacy segment 0 lease is harvested exactly as before segments existed")
}

// frozenSealBytes renders a canonical frozen segment-0 seal (the daemon's old-reader barrier, mirrored
// by dsealFrozen) sealing an empty journal with the given seed.
func frozenSealBytes(t *testing.T, seed core.Hash) []byte {
	t.Helper()
	b, err := json.Marshal(dsealFrozen{Format: dsealFrozenFormat, Segment: 0, Chain: seed})
	require.NoError(t, err)
	require.True(t, dsealParseFrozen(b, seed), "fixture: a canonical frozen seal")
	return b
}

// TestGCSegments_FrozenSealAcceptedOnlyOnTheArchivedLegacySegment: once the store has rotated, segment
// 0's seals are frozen and GC harvests it normally. The same document is refused anywhere it cannot
// legitimately be: on a later segment, or on segment 0 while it is still the active segment.
func TestGCSegments_FrozenSealAcceptedOnlyOnTheArchivedLegacySegment(t *testing.T) {
	ctx := context.Background()
	freeze := func(t *testing.T, tp *testProject, active uint64) {
		t.Helper()
		dir := segDirOf(tp, active)
		require.NoError(t, os.WriteFile(paths.Long(filepath.Join(dir, deliveryLeasePositionFile)),
			frozenSealBytes(t, dsealLeaseSeed), 0o600))
		require.NoError(t, os.WriteFile(paths.Long(filepath.Join(dir, deliveryAckPositionFile)),
			frozenSealBytes(t, dsealAckSeed), 0o600))
	}

	t.Run("archived segment 0", func(t *testing.T) {
		tp := newTestStore(t)
		leased := gcSeed(t, tp, "src/leased.txt", "held by an open lease in segment 1\n")
		installSegAuthority(t, tp, []segSpec{{0, ""}, {1, segBaseRoot("seg1")}})
		freeze(t, tp, 0)
		writeSegJournal(t, tp, 1, deliveryLeaseFile,
			leaseLine(deliveryNonce("1"), leased.Hash.String(), obsIDText("frozen")))
		rep, err := tp.Store.GC(ctx, forceCollect)
		require.NoError(t, err)
		require.False(t, rep.RetentionRootsError, "a frozen seal on the archived legacy segment is accepted")
		_, err = tp.Store.GetRoot(ctx, leased.Hash)
		require.NoError(t, err)
	})
	for _, tc := range []struct {
		name   string
		specs  []segSpec
		frozen uint64
	}{
		{"a later segment", []segSpec{{0, ""}, {1, segBaseRoot("seg1")}}, 1},
		{"segment 0 while active", []segSpec{{0, ""}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tp := newTestStore(t)
			doomed := gcSeed(t, tp, "src/doomed.txt", "collectable only if the pass runs\n")
			installSegAuthority(t, tp, tc.specs)
			freeze(t, tp, tc.frozen)
			rep, err := tp.Store.GC(ctx, forceCollect)
			require.NoError(t, err)
			require.True(t, rep.RetentionRootsError, "a frozen seal where none can be halts the pass")
			require.Zero(t, rep.DeletedObjects)
			_, err = tp.Store.GetRoot(ctx, doomed.Hash)
			require.NoError(t, err)
		})
	}
}
