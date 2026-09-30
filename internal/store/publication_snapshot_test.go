package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// A pass against a PublicationSnapshot accounts for the store as it stood at the snapshot, so the
// daemon can finish its startup accounting in the background while it serves (V6 close-out D49).
// Files written after the snapshot are live work, never a gap; residue from before it is still one.

// setModTime moves a file's modification time, so a test can place it unambiguously before or after
// a snapshot whatever the file system's timestamp granularity.
func setModTime(t *testing.T, p string, at time.Time) {
	t.Helper()
	require.NoError(t, os.Chtimes(paths.Long(p), at, at))
}

// TestAuditPublication_SnapshotLeavesLaterWritesUnclassified: a stage-one capture and an unindexed
// object written after the snapshot are what an in-flight turn and an in-flight Put look like. A
// pass against the snapshot leaves both unclassified; the same tree without one reports both.
func TestAuditPublication_SnapshotLeavesLaterWritesUnclassified(t *testing.T) {
	tp := newTestStore(t)
	snap, err := tp.Store.SnapshotPublication(context.Background())
	require.NoError(t, err)

	later := time.Now().Add(time.Hour)
	seedCapture(t, tp.Root, "in-flight", auditOpObserveTool, false, core.OutcomeOK, []byte("captured"))
	sidecar, err := CaptureSidecarPath(tp.Root, auditObsID("in-flight"))
	require.NoError(t, err)
	setModTime(t, sidecar, later)
	h := core.HashBytes(core.DomainChunk, []byte("an in-flight put"))
	writeBareObject(t, tp, h)
	setModTime(t, tp.Store.objectPath(h), later)

	scanCap := DefaultPublicationScanCap()
	scanCap.Snapshot = &snap
	a, err := tp.Store.AuditPublication(context.Background(), scanCap)
	require.NoError(t, err)
	require.False(t, a.HasGaps(), "writes after the snapshot are not gaps of the store it accounts for")
	require.False(t, a.Incomplete, "they are left unclassified, not unknown: %v", a.Notes)
	require.Equal(t, 2, a.PostSnapshotEntries)

	quiet, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)
	require.Equal(t, 1, quiet.UnpublishedCaptures, "without a snapshot the same capture is a gap")
	require.Equal(t, 1, quiet.UnindexedObjectCandidates, "without a snapshot the same object is a candidate")
}

// TestAuditPublication_SnapshotStillReportsResidueFromBeforeIt: the snapshot changes what counts as
// live, never what counts as a gap. Residue older than the snapshot is reported exactly as a pass
// without one reports it.
func TestAuditPublication_SnapshotStillReportsResidueFromBeforeIt(t *testing.T) {
	tp := newTestStore(t)
	earlier := time.Now().Add(-time.Hour)
	seedCapture(t, tp.Root, "residue", auditOpObserveTool, false, core.OutcomeOK, []byte("captured"))
	sidecar, err := CaptureSidecarPath(tp.Root, auditObsID("residue"))
	require.NoError(t, err)
	setModTime(t, sidecar, earlier)
	h := core.HashBytes(core.DomainChunk, []byte("a crash orphan"))
	writeBareObject(t, tp, h)
	setModTime(t, tp.Store.objectPath(h), earlier)

	snap, err := tp.Store.SnapshotPublication(context.Background())
	require.NoError(t, err)
	scanCap := DefaultPublicationScanCap()
	scanCap.Snapshot = &snap
	a, err := tp.Store.AuditPublication(context.Background(), scanCap)
	require.NoError(t, err)
	require.Equal(t, 1, a.UnpublishedCaptures)
	require.Equal(t, 1, a.UnindexedObjectCandidates)
	require.Zero(t, a.PostSnapshotEntries)
}

// TestAuditPublication_SnapshotJudgesObjectsByItsOwnIndex: an object the index referenced at the
// snapshot is not a candidate however the live index moves afterwards, which is what keeps a GC
// that tombstones a root mid-pass from reading as a crash orphan.
func TestAuditPublication_SnapshotJudgesObjectsByItsOwnIndex(t *testing.T) {
	tp := newTestStore(t)
	res, err := tp.Store.PutBytes(context.Background(), []byte("indexed at the snapshot"), PutOptions{Tool: "Bash"})
	require.NoError(t, err)
	snap, err := tp.Store.SnapshotPublication(context.Background())
	require.NoError(t, err)

	// Drop the chunks from the live index, the in-memory state a tombstone leaves before its sweep.
	tp.Store.mu.Lock()
	for _, c := range res.Root.Chunks {
		delete(tp.Store.chunkSet, c.Hash)
	}
	tp.Store.mu.Unlock()

	scanCap := DefaultPublicationScanCap()
	scanCap.Snapshot = &snap
	a, err := tp.Store.AuditPublication(context.Background(), scanCap)
	require.NoError(t, err)
	require.Zero(t, a.UnindexedObjectCandidates, "the snapshot's index referenced every object on disk")

	live, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)
	require.Positive(t, live.UnindexedObjectCandidates, "the live index no longer does")
}

// TestAuditPublication_SnapshotCountsResidueAtItsInstant: a file whose modification time equals the
// snapshot's instant was written before the snapshot, not after it. File times and the wall clock
// tick together on Windows, so residue written in the last clock tick before the snapshot carries
// exactly the snapshot's time, and a pass that read that as live work skipped a real gap until the
// next start (w15-services review). Only a file strictly after the snapshot is live work.
func TestAuditPublication_SnapshotCountsResidueAtItsInstant(t *testing.T) {
	tp := newTestStore(t)
	seedCapture(t, tp.Root, "residue", auditOpObserveTool, false, core.OutcomeOK, []byte("captured"))
	sidecar, err := CaptureSidecarPath(tp.Root, auditObsID("residue"))
	require.NoError(t, err)
	h := core.HashBytes(core.DomainChunk, []byte("a crash orphan"))
	writeBareObject(t, tp, h)

	snap, err := tp.Store.SnapshotPublication(context.Background())
	require.NoError(t, err)
	setModTime(t, sidecar, snap.taken)
	setModTime(t, tp.Store.objectPath(h), snap.taken)

	scanCap := DefaultPublicationScanCap()
	scanCap.Snapshot = &snap
	a, err := tp.Store.AuditPublication(context.Background(), scanCap)
	require.NoError(t, err)
	require.Equal(t, 1, a.UnpublishedCaptures, "a capture written at the snapshot's instant is residue")
	require.Equal(t, 1, a.UnindexedObjectCandidates, "an object written at the snapshot's instant is residue")
	require.Zero(t, a.PostSnapshotEntries)
}

// TestAuditPublication_SnapshotCountsResidueWrittenJustBeforeIt is the same boundary as the store
// meets it, with no time moved: residue written immediately before the snapshot is reported.
func TestAuditPublication_SnapshotCountsResidueWrittenJustBeforeIt(t *testing.T) {
	tp := newTestStore(t)
	seedCapture(t, tp.Root, "residue", auditOpObserveTool, false, core.OutcomeOK, []byte("captured"))
	h := core.HashBytes(core.DomainChunk, []byte("a crash orphan"))
	writeBareObject(t, tp, h)

	snap, err := tp.Store.SnapshotPublication(context.Background())
	require.NoError(t, err)
	scanCap := DefaultPublicationScanCap()
	scanCap.Snapshot = &snap
	a, err := tp.Store.AuditPublication(context.Background(), scanCap)
	require.NoError(t, err)
	require.Equal(t, 1, a.UnpublishedCaptures)
	require.Equal(t, 1, a.UnindexedObjectCandidates)
	require.Zero(t, a.PostSnapshotEntries)
}
