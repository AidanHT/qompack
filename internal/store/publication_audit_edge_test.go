package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Edge rows for the publication audit's three phases that the main audit tests leave unexecuted:
// each pending marker, object entry and capture sidecar the pass cannot account for is NOTED (so the
// audit reads incomplete, never clean), entries that are not the phase's own are passed over, and the
// object cap, the byte budget and the snapshot boundary each stop or excuse exactly what they should
// (w16b-cover, C3.6).

// plantPending writes name with content into the pending-write registry.
func plantPending(t *testing.T, tp *testProject, name string, content []byte) string {
	t.Helper()
	dir := filepath.Join(paths.Of(tp.Root).State, pendingWriteDir)
	require.NoError(t, os.MkdirAll(paths.Long(dir), 0o700))
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(paths.Long(p), content, 0o600))
	return p
}

// TestAuditPublication_NotesPendingMarkersItCannotUse: a marker past the read limit, or one that does
// not parse, cannot vouch for any object, so the pass says it is incomplete; a subdirectory or a file
// that is not a marker is not part of the registry and is passed over.
func TestAuditPublication_NotesPendingMarkersItCannotUse(t *testing.T) {
	tp := newTestStore(t)
	plantPending(t, tp, "huge"+pendingWriteSuffix, make([]byte, pendingMarkerReadLimit+1))
	plantPending(t, tp, "torn"+pendingWriteSuffix, []byte(`{"v":`))
	plantPending(t, tp, "readme.txt", []byte("not a marker"))
	require.NoError(t, os.Mkdir(paths.Long(filepath.Join(paths.Of(tp.Root).State, pendingWriteDir, "sub"+pendingWriteSuffix)), 0o700))

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)
	require.True(t, a.Incomplete)
	require.ElementsMatch(t, []string{
		"pending-write marker exceeds the read limit",
		"pending-write marker unparseable",
	}, a.Notes)
}

// TestAuditPublication_StopsAtTheByteBudgetInEitherReadingPhase: a marker or a sidecar larger than
// what is left of the byte budget is not read; the pass stops there and says it was truncated.
func TestAuditPublication_StopsAtTheByteBudgetInEitherReadingPhase(t *testing.T) {
	small := PublicationScanCap{MaxCaptures: 10, MaxObjects: 10, MaxEntries: 100, MaxBytes: 1}

	t.Run("pending marker", func(t *testing.T) {
		tp := newTestStore(t)
		plantPending(t, tp, "m"+pendingWriteSuffix, []byte(`{"v":1,"root":"x","chunks":[]}`))
		a, err := tp.Store.AuditPublication(context.Background(), small)
		require.NoError(t, err)
		require.True(t, a.Truncated)
		require.Contains(t, a.Notes, "scan reached its byte budget")
	})
	t.Run("capture sidecar", func(t *testing.T) {
		tp := newTestStore(t)
		seedCapture(t, tp.Root, "over-budget", auditOpObserveTool, false, core.OutcomeOK, []byte("body"))
		a, err := tp.Store.AuditPublication(context.Background(), small)
		require.NoError(t, err)
		require.True(t, a.Truncated)
		require.Contains(t, a.Notes, "scan reached its byte budget")
	})
}

// TestAuditPublication_ExcusesAMarkerWrittenAfterTheSnapshot: a pass that runs beside a serving store
// accounts for the snapshot it was given; a marker written after it is live work, counted as such and
// not read.
func TestAuditPublication_ExcusesAMarkerWrittenAfterTheSnapshot(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	snap, err := tp.Store.SnapshotPublication(ctx)
	require.NoError(t, err)
	p := plantPending(t, tp, "late"+pendingWriteSuffix, []byte(`{"v":`))
	future := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(paths.Long(p), future, future))

	a, err := tp.Store.AuditPublication(ctx, PublicationScanCap{Snapshot: &snap})
	require.NoError(t, err)
	require.Equal(t, 1, a.PostSnapshotEntries)
	require.NotContains(t, a.Notes, "pending-write marker unparseable", "a post-snapshot marker is not read")
}

// TestAuditPublication_NotesObjectEntriesItCannotAccountFor: the object tree is two fanout levels of
// directories and then content-addressed files. A file where a fanout directory belongs, a directory
// where an object belongs, or a leaf whose name is not a content address is noted, never counted as
// an object.
func TestAuditPublication_NotesObjectEntriesItCannotAccountFor(t *testing.T) {
	tp := newTestStore(t)
	h := core.HashBytes(core.DomainChunk, []byte("bare object"))
	writeBareObject(t, tp, h)
	leafDir := filepath.Dir(tp.Store.objectPath(h))
	require.NoError(t, os.Mkdir(paths.Long(filepath.Join(leafDir, "nested")), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(leafDir, "NOT-A-HASH")), nil, 0o600))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(filepath.Dir(leafDir), "stray")), nil, 0o600))

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)
	require.True(t, a.Incomplete)
	require.Subset(t, a.Notes, []string{
		"unexpected entry in the object tree",
		"unexpected directory in an object fanout",
		"object file name is not a content address",
	})
	require.Equal(t, 1, a.UnindexedObjectCandidates, "only the content-addressed leaf is an object")
}

// TestAuditPublication_StopsAtTheObjectCap: the object phase examines at most MaxObjects leaves and
// says it stopped short.
func TestAuditPublication_StopsAtTheObjectCap(t *testing.T) {
	tp := newTestStore(t)
	for _, s := range []string{"one", "two", "three"} {
		writeBareObject(t, tp, core.HashBytes(core.DomainChunk, []byte(s)))
	}
	a, err := tp.Store.AuditPublication(context.Background(),
		PublicationScanCap{MaxCaptures: 10, MaxObjects: 1, MaxEntries: 100, MaxBytes: 1 << 20})
	require.NoError(t, err)
	require.True(t, a.Truncated)
	require.Equal(t, 1, a.ObjectsScanned)
	require.Contains(t, a.Notes, "object scan reached its cap")
}

// TestAuditPublication_NotesASidecarPastTheReadLimit: a capture sidecar larger than any this build
// writes is reported without being read; a shard entry that is not a sidecar is passed over.
func TestAuditPublication_NotesASidecarPastTheReadLimit(t *testing.T) {
	tp := newTestStore(t)
	writeRawSidecar(t, tp.Root, "oversized", make([]byte, captureSidecarReadLimit+1))
	p, err := CaptureSidecarPath(tp.Root, auditObsID("oversized"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(filepath.Dir(p), "notes.txt")), nil, 0o600))
	require.NoError(t, os.Mkdir(paths.Long(filepath.Join(filepath.Dir(p), "sub.json")), 0o700))

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)
	require.True(t, a.Incomplete)
	require.Equal(t, []string{"capture sidecar exceeds the read limit"}, a.Notes)
	require.Equal(t, 1, a.CapturesScanned)
}
