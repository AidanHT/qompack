package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
)

// Audit 2's status finding #9 (wave 22, D67): a compaction the user cancelled, or one that failed,
// after its PreCompact hook ran. Nothing cleared the obligation it armed, so the session's next
// start, a --resume, failed session_start.source_compact at critical severity and degraded the
// project on a store where every hook fired. Pinned with the genuine failure it must still report.

// TestSessionStartSourceCompact_CancelledCompactionThenResumeStaysFull is #9: PreCompact ran, the
// compaction was cancelled or failed, so the host started no compact session, and the session
// later went on — a prompt, or its SessionEnd — before a --resume of it. That resume is no host
// failure: the project stays in full mode and no banner blames the host. The SessionEnd case holds
// across a daemon restart between the end and the resume.
func TestSessionStartSourceCompact_CancelledCompactionThenResumeStaysFull(t *testing.T) {
	const sess = core.SessionID("sess-cancelled")
	for _, between := range []string{"prompt", "session end", "session end, then a daemon restart"} {
		t.Run(between, func(t *testing.T) {
			root := t.TempDir()
			dd := replayProbeDaemonAt(t, root)
			require.True(t, dd.dispatchOp(context.Background(), startRequest(dd, sess, "startup", "", "n-start")).OK)
			pre := checkpointRequest(dd, sess, "n-pre")
			require.True(t, dd.dispatchOp(context.Background(), pre).OK)
			after := pre.TS + 1000
			switch between {
			case "prompt":
				promptScan(dd, sess, "", after)
			default:
				resp := dd.dispatchOp(context.Background(), sessionEndRequest(dd, sess, after))
				require.True(t, resp.OK, resp.Err)
			}
			if between == "session end, then a daemon restart" {
				dd = replayProbeDaemonAt(t, root)
			}
			resume := startRequest(dd, sess, "resume", "", "n-resume")
			resume.TS = after + 1000
			resp := dd.dispatchOp(context.Background(), resume)
			require.True(t, resp.OK)
			r := reportOf(t, dd, contract.CSessionStartSourceCompact)
			require.True(t, r.OK, "a cancelled compaction is no host failure (observed %q)", r.Observed)
			require.Equal(t, contract.ModeFull, dd.monitor.Mode())
			require.NotNil(t, resp.Output)
			require.Empty(t, resp.Output.SystemMessage, "no banner blames the host")
			require.False(t, history(t, dd).AwaitingCompactStart, "the resume resolves the obligation")
		})
	}
}

// TestSessionStartSourceCompact_NonCompactStartRightAfterPreCompactStillFails pins the other
// direction of #9: a PreCompact followed by a start of the same session that is not a compaction,
// with nothing of that session in between, is the host breaking the contract. Neither a prompt nor
// a SessionEnd the host fired BEFORE the PreCompact (replayed late from a spool) counts as between.
func TestSessionStartSourceCompact_NonCompactStartRightAfterPreCompactStillFails(t *testing.T) {
	const sess = core.SessionID("sess-wrong-source")
	for _, before := range []string{"nothing", "a prompt fired before the PreCompact", "a SessionEnd fired before the PreCompact"} {
		t.Run(before, func(t *testing.T) {
			dd := replayProbeDaemon(t)
			require.True(t, dd.dispatchOp(context.Background(), startRequest(dd, sess, "startup", "", "n-start")).OK)
			pre := checkpointRequest(dd, sess, "n-pre")
			require.True(t, dd.dispatchOp(context.Background(), pre).OK)
			earlier := pre.TS - 1000
			switch before {
			case "a prompt fired before the PreCompact":
				promptScan(dd, sess, "", earlier)
			case "a SessionEnd fired before the PreCompact":
				end := sessionEndRequest(dd, sess, earlier)
				require.True(t, dd.drainDispatch(context.Background(), end).OK)
			}
			start := startRequest(dd, sess, "resume", "", "n-resume")
			start.TS = pre.TS + 1000
			resp := dd.dispatchOp(context.Background(), start)
			require.True(t, resp.OK)
			r := reportOf(t, dd, contract.CSessionStartSourceCompact)
			require.False(t, r.OK, "a resume right after PreCompact is no compaction")
			require.Equal(t, contract.SevCritical, r.Severity)
			require.Equal(t, "compact", r.Expected)
			require.Equal(t, "resume", r.Observed)
			require.Equal(t, contract.ModeDegradedPassive, dd.monitor.Mode())
		})
	}
}
