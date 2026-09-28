package observer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// SP08-D1 under owner decision D20: a fresh leased capture keeps both of its full publication passes,
// and within a pass each directory is fsynced once. These counter names are restated for the reason
// publication_passes_test.go gives for passCounter.
const (
	fileBarrierCounter = "store.publication.sync.file"
	dirBarrierCounter  = "store.publication.sync.dir"
)

// barrierCounts is one reading of the three publication counters.
type barrierCounts struct{ passes, files, dirs int64 }

func (r *rdxRig) barriers() barrierCounts {
	return barrierCounts{r.passes(), r.counter(fileBarrierCounter), r.counter(dirBarrierCounter)}
}

func (b barrierCounts) minus(o barrierCounts) barrierCounts {
	return barrierCounts{b.passes - o.passes, b.files - o.files, b.dirs - o.dirs}
}

// TestPublicationPasses_Leased256KBCaptureFsyncsEachDirectoryOncePerPass pins the publication fsyncs
// of the capture BenchmarkOnToolUse_TestOutput256KB_Leased times, for each of its fixtures: a first
// capture of novel content, then the fixture's second call (the same bytes, one changed line, or all
// new bytes). Each capture pays exactly two passes over its root, and a standalone pass over that
// root afterwards prices one pass: the capture's file and directory fsyncs are twice the pass's, so
// the post-write pass is still the whole re-proof. Within a pass there is one file fsync per closure
// object plus the two index files, and at most a fanout leaf and a first-level fanout directory per
// object plus the objects/ root and index/ once each. The objects/ root fsynced once per object,
// which is what the pass did before, cannot fit that bound for two or more objects.
func TestPublicationPasses_Leased256KBCaptureFsyncsEachDirectoryOncePerPass(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "corpora", "toolout", "testrunner", "go-test-rerun.txt"))
	require.NoError(t, err)
	out := repeatTo(string(raw), 256<<10)

	for _, f := range benchFixtures() {
		t.Run(f.name, func(t *testing.T) {
			r := newPassRig(t)
			ctx := context.Background()
			sync, ok := r.st.(store.PublicationSync)
			require.True(t, ok, "fixture: the rig's store syncs publications")
			for i := range 2 {
				r.clock.Advance(time.Second)
				id := core.ToolUseID(fmt.Sprintf("toolu_%d", i))
				obsID := r.sidecar(uint64(i+1), rdxOpTool)
				before := r.barriers()
				_, err := r.o.OnToolUse(WithObservation(ctx, obsID), bashOf(string(id), "go test ./...", f.vary(out, i)))
				require.NoError(t, err)
				capture := r.barriers().minus(before)
				r.requirePublished(obsID, id)

				rec, err := r.st.ToolUse(ctx, id)
				require.NoError(t, err)
				before = r.barriers()
				require.NoError(t, sync.SyncPublication(ctx, rec.Root))
				pass := r.barriers().minus(before)
				objects := pass.files - 2
				t.Logf("capture %d: %d closure objects; per pass %d file + %d directory fsyncs; per capture %d + %d",
					i, objects, pass.files, pass.dirs, capture.files, capture.dirs)

				require.Equal(t, int64(1), pass.passes)
				require.GreaterOrEqual(t, objects, int64(2), "fixture: the 256 KB result spans several objects")
				require.Equal(t, int64(2), capture.passes, "capture %d: the two §0.2.2 barriers", i)
				require.Equal(t, 2*pass.files, capture.files, "capture %d: both passes fsync every closure object", i)
				require.Equal(t, 2*pass.dirs, capture.dirs, "capture %d: both passes fsync every closure directory", i)
				require.LessOrEqual(t, pass.dirs, 2*objects+2,
					"capture %d: one fsync per directory, not a leaf, a fanout and the objects/ root per object", i)
			}
		})
	}
}
