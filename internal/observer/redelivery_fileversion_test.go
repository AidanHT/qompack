package observer

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// rdxBodyV2 is a second content of supersedePath, so that a read of it is a NEW file version and
// not a re-read the store's own (root, turn) dedup would fold away.
const rdxBodyV2 = "export async function refreshToken() { return renew() }\nexport const ttl = 60\n"

// TestRedelivery_ReadCutBeforeItsFileVersionIsRepairedByTheReplay is the deterministic form of the
// V5 close-out's x05 failure under co-load:
//
//	"the record whose dependency changed must carry §8.3's stale note ... [active]"
//
// A hook read the rewritten file; the observer landed the index record (step 6a) and was cancelled
// — a Stop's runCancel — inside the sidecar link's fsync, before step 7 appended the §8.2 file
// version. The delivery stayed unacknowledged and was redelivered on the next start, where the
// recognized-replay branch returned before step 7, so the version was never appended by anyone:
// store.ChangedSince saw the old root, RefreshStaleness flipped nothing, and already_tried kept
// answering active for an elimination whose dependency had changed — §12's High-severity
// direction, and one no later refresh can recover from, because the history is what is missing.
//
// The property: after a redelivery on a live context, the file history's newest version carries
// the recognized record's root, whatever point the first run was cut at. The version a replay
// appends is built from the INDEX record (its ts, turn, root and bytes), not from the redelivering
// process's clock or turn, so it is the same line the first run would have written, and the
// store's (root, turn) dedup makes it a no-op when the first run did write it.
//
// Same construction as the SP08-D2 sweep: a fake clock, a real store, a counted context.
func TestRedelivery_ReadCutBeforeItsFileVersionIsRepairedByTheReplay(t *testing.T) {
	for _, proc := range rdxProcessStates {
		for cut := range rdxReadCutPoints {
			t.Run(fmt.Sprintf("%s/cut after %d checks", proc.name, cut), func(t *testing.T) {
				r := newRdxRig(t)
				ctx := context.Background()

				// A: the first version of the path, fully recorded — the version an elimination's
				// depends_on hash would have been recorded against.
				_, err := r.o.OnToolUse(ctx, readOf("toolu_A", supersedePath, rdxBody))
				require.NoError(t, err)
				recA, err := r.st.ToolUse(ctx, "toolu_A")
				require.NoError(t, err)
				histA, err := r.st.FileHistory(ctx, recA.Path)
				require.NoError(t, err)
				require.Len(t, histA, 1, "fixture: A appended the path's first version")
				require.Equal(t, recA.Root, histA[0].Root)
				r.clock.Advance(time.Second)

				// B: the path's NEW content. Its first run is cut after `cut` context checks.
				obsB := r.sidecar(2, rdxOpTool)
				b := readOf("toolu_B", supersedePath, rdxBodyV2)
				_, _ = r.o.OnToolUse(WithObservation(rdxCutAfter(cut), obsB), b) //nolint:errcheck // the cut's error is the point

				if proc.restart {
					r.restart(proc.persist)
				}

				// The redelivery, on a live context, under the lease the daemon already holds.
				r.clock.Advance(time.Second)
				r.sidecar(2, rdxOpTool)
				_, err = r.o.OnToolUse(WithObservation(ctx, obsB), b)
				require.NoError(t, err)

				recB, err := r.st.ToolUse(ctx, "toolu_B")
				require.NoError(t, err, "the redelivery leaves B in the index whatever the cut")
				require.NotEqual(t, recA.Root, recB.Root, "fixture: B is a different content of the path")

				hist, err := r.st.FileHistory(ctx, recB.Path)
				require.NoError(t, err)
				newest := hist[len(hist)-1]
				require.Equal(t, recB.Root, newest.Root,
					"index/tool_use.jsonl holds B (root %s) but the newest file version of %s is root %s: "+
						"the first run was cut between the record and its file version and the replay did "+
						"not repair it, so ChangedSince will never see this edit (history has %d versions)",
					recB.Root.Short(), recB.Path, newest.Root.Short(), len(hist))
				require.Len(t, hist, 2, "exactly one version per distinct content: no duplicate from the replay")
				require.Equal(t, recB.TS, newest.TS, "the repaired version carries the first run's instant, not the replay's")
				require.Equal(t, recB.Turn, newest.Turn, "the repaired version carries the first run's turn, not the replay's")
			})
		}
	}
}
