package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
)

// A PreCompact the checkpoint route observes arms session_start.source_compact: the session's NEXT
// SessionStart must carry source=compact, or the host broke the contract and the project degrades at
// critical severity (contract.checkSessionStartSourceCompact). The checkpoint hook spools its request
// when its reply misses the client's deadline, although the daemon may already have handled it, and
// a drain replays it later — possibly after the compact SessionStart it announced has come and gone.
// Re-arming the obligation then pins it on the session's next start, a resume, which is no compact,
// and the project degrades, blaming the host for Qompack's own replay order.

func checkpointRequest(dd *daemon, sess core.SessionID, nonce string) ipc.Request {
	ev := &hookio.Event{HookEventName: "PreCompact", SessionID: sess, CWD: dd.root, Trigger: "auto"}
	return ipc.Request{Op: ipc.OpCheckpoint, Session: sess, Reply: true, Event: ev, Nonce: nonce, TS: core.NowMilli(dd.clk)}
}

// TestCheckpointReplay_DoesNotReopenACompactStartAlreadyObserved: the replay of a PreCompact whose
// compact SessionStart the daemon has already handled leaves the obligation resolved.
func TestCheckpointReplay_DoesNotReopenACompactStartAlreadyObserved(t *testing.T) {
	dd := replayProbeDaemon(t)
	const sess = core.SessionID("sess-late-precompact")
	pre := checkpointRequest(dd, sess, "nonce-precompact")

	require.True(t, dd.dispatchOp(context.Background(), pre).OK)
	require.True(t, history(t, dd).AwaitingCompactStart, "fixture: the live PreCompact arms the obligation")
	require.True(t, dd.dispatchOp(context.Background(), startRequest(dd, sess, "compact", "", "nonce-compact")).OK)
	require.False(t, history(t, dd).AwaitingCompactStart, "fixture: the compact start resolves it")

	require.True(t, dd.drainDispatch(context.Background(), pre).OK)
	require.False(t, history(t, dd).AwaitingCompactStart,
		"a replayed PreCompact must not re-arm an obligation its compact SessionStart already met")

	resp := dd.dispatchOp(context.Background(), startRequest(dd, sess, "resume", "", "nonce-resume"))
	require.True(t, resp.OK)
	require.Equal(t, contract.ModeFull, dd.monitor.Mode(), "a resume after a met compaction breaks no contract")
	require.NotNil(t, resp.Output)
	require.Empty(t, resp.Output.SystemMessage)
}

// TestCheckpointReplay_StillArmsWhenNoStartHasFollowed: a PreCompact the daemon never saw live — it
// was down when the hook ran — still arms the obligation from its replay, which the startup drain
// runs before the compact SessionStart is served.
func TestCheckpointReplay_StillArmsWhenNoStartHasFollowed(t *testing.T) {
	dd := replayProbeDaemon(t)
	const sess = core.SessionID("sess-spooled-precompact")

	require.True(t, dd.drainDispatch(context.Background(), checkpointRequest(dd, sess, "nonce-spooled")).OK)
	h := history(t, dd)
	require.True(t, h.AwaitingCompactStart, "a PreCompact no start has answered yet still arms the obligation")
	require.Equal(t, sess, h.LastPrecompactSession)
}

// TestSessionStartReplay_AStartFromBeforeThePreCompactLeavesItsObligationPending: a startup
// SessionStart that could not reach a cold daemon is spooled; the session's PreCompact then reaches
// the daemon live and arms session_start.source_compact; and only after that does a drain replay the
// spooled start. That start was fired BEFORE the PreCompact, so it is not the start the PreCompact
// announced: resolving the obligation against it failed the assertion at critical severity (a
// startup is no compact) and degraded the project, blaming the host for Qompack's replay order. The
// obligation must stay pending for the compact start that does follow.
func TestSessionStartReplay_AStartFromBeforeThePreCompactLeavesItsObligationPending(t *testing.T) {
	dd := replayProbeDaemon(t)
	const sess = core.SessionID("sess-stale-start")
	stale := startRequest(dd, sess, "startup", "", "nonce-stale-start")
	stale.TS -= 1000 // fired a second before the PreCompact below

	require.True(t, dd.dispatchOp(context.Background(), checkpointRequest(dd, sess, "nonce-precompact")).OK)
	require.True(t, dd.drainDispatch(context.Background(), stale).OK)
	require.Equal(t, contract.ModeFull, dd.monitor.Mode(),
		"a start fired before the PreCompact must not be taken for the start it announced")
	require.True(t, history(t, dd).AwaitingCompactStart, "the obligation stays pending for the start that follows")

	resp := dd.dispatchOp(context.Background(), startRequest(dd, sess, "compact", "", "nonce-compact"))
	require.True(t, resp.OK)
	require.False(t, history(t, dd).AwaitingCompactStart, "the compact start resolves it")
	require.Equal(t, contract.ModeFull, dd.monitor.Mode())
}
