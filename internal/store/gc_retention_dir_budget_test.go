package store

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The retention-directory enumeration answers to the GC deadline BETWEEN batches, and only there:
// listRetentionDir's own contract is that the check is never made before the first read, "so a
// trivially small (or empty) directory always completes, exactly as the old os.ReadDir did, while a
// genuinely large enumeration still answers to ctx and the deadline".
//
// os.File.ReadDir(n) does not report io.EOF on the call that returns the last entries; it reports it
// on the NEXT call, which returns none. A loop that checks the deadline after every batch that came
// back without an error therefore checks it after the first — and only — batch of a one-file
// directory too, and an expired deadline truncated the WHOLE mark there: nothing collected, no
// cursor persisted, for a listing that was already complete. test/integration's
// TestIntegration_GCNeverCollectsALiveRootUnderIngest found it (its pinning checkpoint directory
// holds one file) on Linux and Windows alike; these pin it where it lives.

// expiredGCBudget is a budget whose deadline has already passed, so any deadline check it answers
// reports truncation.
func expiredGCBudget() *gcBudget {
	return newGCBudget(context.Background(), time.Now().Add(-time.Hour))
}

// listAllJSON accepts every regular .json entry, the way gcRootFiles' checkpoint lister does.
func listAllJSON(dir string) func(fs.DirEntry) (gcRootFile, bool) {
	return func(e fs.DirEntry) (gcRootFile, bool) {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			return gcRootFile{}, false
		}
		return gcRootFile{path: filepath.Join(dir, e.Name()), class: RetentionCheckpoint}, true
	}
}

// plantJSONFiles writes n small .json files into dir.
func plantJSONFiles(t *testing.T, dir string, n int) {
	t.Helper()
	require.NoError(t, os.MkdirAll(paths.Long(dir), 0o700))
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, fmt.Sprintf("%05d.json", i))
		require.NoError(t, os.WriteFile(paths.Long(p), []byte("{}"), 0o600))
	}
}

// TestListRetentionDir_AShortListingCompletesUnderAnExpiredDeadline: a directory whose whole listing
// fits in one batch has nothing left to budget for, so an expired deadline must not truncate it.
func TestListRetentionDir_AShortListingCompletesUnderAnExpiredDeadline(t *testing.T) {
	tp := newTestStore(t)
	dir := paths.Of(tp.Root).Checkpoints
	plantJSONFiles(t, dir, 3)

	out, truncated, err := tp.Store.listRetentionDir(dir, expiredGCBudget(), maxRetentionSources, listAllJSON(dir))
	require.NoError(t, err)
	require.False(t, truncated,
		"a listing that one batch completed was truncated by the deadline check that is only meant to run "+
			"BETWEEN batches — the whole mark is thrown away for a directory that was already fully read")
	require.Len(t, out, 3)
}

// TestListRetentionDir_AFullBatchStillAnswersAnExpiredDeadline is the other half: a directory larger
// than one batch still meets the deadline between batches, so the fix above cannot have removed the
// budget from genuinely large enumerations.
func TestListRetentionDir_AFullBatchStillAnswersAnExpiredDeadline(t *testing.T) {
	tp := newTestStore(t)
	dir := paths.Of(tp.Root).Checkpoints
	plantJSONFiles(t, dir, gcDirBatch+1)

	out, truncated, err := tp.Store.listRetentionDir(dir, expiredGCBudget(), maxRetentionSources, listAllJSON(dir))
	require.NoError(t, err)
	require.True(t, truncated, "an enumeration past one full batch must still answer an expired deadline")
	require.Nil(t, out, "a truncated enumeration hands back nothing to harvest")
}

// TestGC_AnExpiredDeadlineDoesNotStopASmallPassAtItsCheckpointListing is the same defect seen from
// the collector: every loop of this pass is shorter than one budget check (gcCheckEvery), so the
// deadline has nowhere to land and the pass must complete — collect the unreferenced root, keep the
// checkpointed one — rather than stop at the mark with nothing collected.
func TestGC_AnExpiredDeadlineDoesNotStopASmallPassAtItsCheckpointListing(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	kept := gcSeed(t, tp, "src/kept.ts", "held by the checkpoint\n")
	doomed := gcSeed(t, tp, "src/doomed.ts", "held by nothing\n")
	writeCheckpointJSON(t, tp, "0001.json", kept.Hash.String())

	rep, err := tp.Store.GC(ctx, GCPolicy{RetainDays: -1, RetainSessions: -1, Deadline: time.Nanosecond})
	require.NoError(t, err)
	require.False(t, rep.Truncated,
		"no loop of this pass reaches a budget check, so an expired deadline must not truncate it")
	require.Positive(t, rep.DeletedObjects, "the pass must have reached its sweep")

	_, err = tp.Store.GetRoot(ctx, kept.Hash)
	require.NoError(t, err, "the checkpointed root survives")
	_, err = tp.Store.GetRoot(ctx, doomed.Hash)
	require.ErrorIs(t, err, core.ErrNotFound, "the unreferenced root is collected")
}
