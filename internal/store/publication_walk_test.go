package store

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The publication walk opens the project root once per pass and reaches everything through the
// handle of the directory that listed it (V6 close-out D51), and it waits on the caller's Yield
// before each unit of its I/O. These tests pin what that must not change: the pass finds the same
// gaps with and without a Yield, a Yield that ends stops it as interrupted, and a link anywhere in
// the walked tree — a phase directory, one of its ancestors, a capture shard, an object fanout — is
// refused, never followed out of the project. The link tests hold on the walk before D51 too; they
// are the confinement guarantees the one-root walk keeps.

// seedWalkedStore gives tp one capture gap, one legitimately unpublished capture, one published
// capture, two indexed objects, one pending object and one unindexed object candidate: every class
// the walk tells apart.
func seedWalkedStore(t *testing.T, tp *testProject) {
	t.Helper()
	seedCapture(t, tp.Root, "walk-gap", auditOpObserveTool, false, core.OutcomeOK, []byte("tool result"))
	seedCapture(t, tp.Root, "walk-denied", auditOpObserveTool, false, core.OutcomeDenied, nil)
	seedCapture(t, tp.Root, "walk-published", auditOpObserveTool, true, core.OutcomeOK, []byte("published"))
	for _, body := range []string{"first indexed body of the walk", "second indexed body of the walk"} {
		_, err := tp.Store.PutBytes(context.Background(), []byte(body), PutOptions{Tool: "Bash"})
		require.NoError(t, err)
	}
	pendingHash := core.HashBytes(core.DomainChunk, []byte("walk: in-flight write"))
	writeBareObject(t, tp, pendingHash)
	require.NotNil(t, tp.Store.pending(core.HashBytes(core.DomainChunk, []byte("walk-root")),
		[]ChunkRef{{Hash: pendingHash, Len: 4}}))
	writeBareObject(t, tp, core.HashBytes(core.DomainChunk, []byte("walk: orphan with no index line")))
}

// TestAuditPublication_YieldBeforeEveryEntryLeavesTheAnswerUnchanged: a Yield is waited on before
// every entry the pass handles, and a pass that yields reports exactly what one that does not
// reports.
func TestAuditPublication_YieldBeforeEveryEntryLeavesTheAnswerUnchanged(t *testing.T) {
	tp := newTestStore(t)
	seedWalkedStore(t, tp)

	plain, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)
	require.False(t, plain.Incomplete, "fixture sanity: %v", plain.Notes)
	require.Equal(t, 1, plain.UnpublishedCaptures)
	require.Equal(t, 1, plain.LegitimatelyUnpublished)
	require.Equal(t, 1, plain.PendingObjects)
	require.Equal(t, 1, plain.UnindexedObjectCandidates)

	yields := 0
	scanCap := DefaultPublicationScanCap()
	scanCap.Yield = func(ctx context.Context) error {
		yields++
		return ctx.Err()
	}
	yielding, err := tp.Store.AuditPublication(context.Background(), scanCap)
	require.NoError(t, err)
	require.Equal(t, plain, yielding, "yielding changes when the pass runs, never what it finds")
	require.GreaterOrEqual(t, yields, yielding.CapturesScanned+yielding.ObjectsScanned,
		"the pass yields before each sidecar and object it handles")
}

// TestAuditPublication_YieldThatEndsStopsThePassAsInterrupted: a Yield whose context ended while it
// waited stops the pass there, as a cancellation does, and the pass says it did not finish.
func TestAuditPublication_YieldThatEndsStopsThePassAsInterrupted(t *testing.T) {
	tp := newTestStore(t)
	seedWalkedStore(t, tp)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scanCap := DefaultPublicationScanCap()
	scanCap.Yield = func(ctx context.Context) error {
		cancel() // the daemon stopped while the pass waited
		return ctx.Err()
	}
	a, err := tp.Store.AuditPublication(ctx, scanCap)
	require.ErrorIs(t, err, context.Canceled)
	require.True(t, a.Incomplete)
	require.Contains(t, a.Notes, "scan interrupted before it finished")
	require.Zero(t, a.CapturesScanned+a.ObjectsScanned, "nothing is handled after the wait ended")
}

// outsideObject is the unindexed object outsideProjectWithGaps leaves in the outside project.
var outsideObject = core.HashBytes(core.DomainChunk, []byte("outside object"))

// outsideProjectWithGaps builds a second project, outside tp's, holding a capture gap and an
// unindexed object: what a link out of tp's tree would expose if the walk followed it. It returns
// that project's root.
func outsideProjectWithGaps(t *testing.T) string {
	t.Helper()
	outside := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, paths.EnsureLayout(paths.Of(outside)))
	seedCapture(t, outside, "outside-gap", auditOpObserveTool, false, core.OutcomeOK, []byte("outside"))
	hx := hex.EncodeToString(outsideObject[:])
	p := filepath.Join(paths.Of(outside).Objects, hx[:fanoutWidth], hx[fanoutWidth:2*fanoutWidth], hx)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(p), []byte("outside bytes"), 0o600))
	return outside
}

// requireNothingFollowed asserts the pass counted nothing a link led to and said it was incomplete.
func requireNothingFollowed(t *testing.T, a PublicationAudit, note string) {
	t.Helper()
	require.Zero(t, a.UnpublishedCaptures, "a capture behind a link was classified")
	require.Zero(t, a.UnindexedObjectCandidates, "an object behind a link was classified")
	require.True(t, a.Incomplete, "a link the walk refused makes the pass incomplete")
	require.Contains(t, a.Notes, note)
}

// TestAuditPublication_LinkedCaptureTreeIsNotFollowed: records/captures itself replaced by a link out
// of the project stops the capture phase, which is noted, not walked.
func TestAuditPublication_LinkedCaptureTreeIsNotFollowed(t *testing.T) {
	tp := newTestStore(t)
	outside := outsideProjectWithGaps(t)
	captures := filepath.Join(paths.Of(tp.Root).Records, captureSidecarDir)
	require.NoError(t, os.RemoveAll(paths.Long(captures)))
	require.NoError(t, makeDirLink(captures, filepath.Join(paths.Of(outside).Records, captureSidecarDir)))

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)
	requireNothingFollowed(t, a, "a directory under .qompack could not be opened")
	require.Zero(t, a.CapturesScanned)
}

// TestAuditPublication_LinkedRecordsAncestorIsNotFollowed: a link one level above the phase directory
// (.qompack/records) is refused on the way down, exactly as the phase directory's own would be.
func TestAuditPublication_LinkedRecordsAncestorIsNotFollowed(t *testing.T) {
	tp := newTestStore(t)
	outside := outsideProjectWithGaps(t)
	records := paths.Of(tp.Root).Records
	require.NoError(t, os.RemoveAll(paths.Long(records)))
	require.NoError(t, makeDirLink(records, paths.Of(outside).Records))

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)
	requireNothingFollowed(t, a, "a directory under .qompack could not be opened")
	require.Zero(t, a.CapturesScanned)
}

// TestAuditPublication_LinkedCaptureShardIsNotFollowed: one shard of the capture tree replaced by a
// link is skipped with its note, and the rest of the tree is still walked.
func TestAuditPublication_LinkedCaptureShardIsNotFollowed(t *testing.T) {
	tp := newTestStore(t)
	outside := outsideProjectWithGaps(t)
	seedCapture(t, tp.Root, "inside-published", auditOpObserveTool, true, core.OutcomeOK, []byte("inside"))
	outsideSidecar, err := CaptureSidecarPath(outside, auditObsID("outside-gap"))
	require.NoError(t, err)
	shardName := filepath.Base(filepath.Dir(outsideSidecar))
	shard := filepath.Join(paths.Of(tp.Root).Records, captureSidecarDir, shardName+"-linked")
	require.NoError(t, makeDirLink(shard, filepath.Dir(outsideSidecar)))

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)
	requireNothingFollowed(t, a, "symlink or reparse point in the capture tree was not traversed")
	require.Equal(t, 1, a.CapturesScanned, "the capture beside the link is still classified")
}

// TestAuditPublication_LinkedObjectFanoutIsNotFollowed: an object fanout directory replaced by a link
// is skipped with its note; no object behind it is counted.
func TestAuditPublication_LinkedObjectFanoutIsNotFollowed(t *testing.T) {
	tp := newTestStore(t)
	outside := outsideProjectWithGaps(t)
	l1 := hex.EncodeToString(outsideObject[:])[:fanoutWidth]
	fanout := filepath.Join(paths.Of(tp.Root).Objects, l1)
	require.NoError(t, os.RemoveAll(paths.Long(fanout)))
	require.NoError(t, makeDirLink(fanout, filepath.Join(paths.Of(outside).Objects, l1)))

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)
	requireNothingFollowed(t, a, "symlink or reparse point in the object tree was not traversed")
	require.Zero(t, a.ObjectsScanned)
}
