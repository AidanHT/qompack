package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/store"
)

// A PreCompact hook spools its request when the reply misses the client's deadline
// (hookclient.go checkpointReplyDeadline, ipc client.go spooling on a read error), although the daemon
// handled it: checkpoint_replay_test.go is built on that premise. The live route never leases the
// request, so a drain leases the spooled copy fresh and replays it through handleCheckpoint. Only the
// contract arming was replay-aware: the seal ran a second time, the scheduler's tap closed the
// post-compaction segment as compacted when work had happened since, and PrecompactWallMs took a
// second sample for precompact.has_time_to_write's p99 (V6 close-out audit 2, finding 4).

// sealRig is a daemon whose PreCompact seam counts its seals (failing them while failSeal is set),
// decorated by the scheduler's tap, with a drainer and an open segment for sess.
type sealRig struct {
	dd       *daemon
	r        *schedRuntime
	seals    atomic.Int64
	failSeal atomic.Bool
	// sealedOK counts the seals that succeeded.
	sealedOK atomic.Int64
	// held, when a row sets it before the first seal, holds that seal until it is closed; the seal
	// closes heldAt first. failFirst fails the first seal only.
	held, heldAt chan struct{}
	failFirst    atomic.Bool
}

func newSealRig(t *testing.T, sess core.SessionID) *sealRig {
	t.Helper()
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	spD3Drainer(dd, root)
	rig := &sealRig{dd: dd}
	dd.svc.PreCompact = func(context.Context, hookio.Event) (hookio.Output, error) {
		n := rig.seals.Add(1)
		if n == 1 && rig.held != nil {
			close(rig.heldAt)
			<-rig.held
		}
		if rig.failSeal.Load() || (n == 1 && rig.failFirst.Load()) {
			return hookio.Empty(), errors.New("seal failed")
		}
		rig.sealedOK.Add(1)
		return hookio.Empty(), nil
	}
	so := SchedulerRuntimeOptions{
		ProjectRoot: root, Cfg: o.Cfg, Clock: dd.clk, Log: dd.log, Metrics: dd.m,
		Store: o.Store, Graph: o.Graph,
	}
	rt, err := NewSchedulerRuntime(so)
	require.NoError(t, err)
	t.Cleanup(scheduler.DisablePSelection)
	WrapServicesForScheduler(dd.svc, rt, so)
	r, ok := rt.(*schedRuntime)
	require.True(t, ok)
	rig.r = r
	_, err = r.segs.Open(context.Background(), store.Segment{Session: sess, StartTurn: 0})
	require.NoError(t, err)
	return rig
}

// read accepts and runs one leased Read of sess.
func (rig *sealRig) read(t *testing.T, sess core.SessionID, id core.ToolUseID, nonce rune, file string) {
	t.Helper()
	require.Equal(t, dispatchSettled,
		rig.dd.ing.dispatch(context.Background(), rig.dd.runIngested, tappedReadOf(t, rig.dd, sess, id, nonce, file)))
}

// drainSpooled writes req to a hook client's spool, as the hook does on a missed reply, and drains it.
func (rig *sealRig) drainSpooled(t *testing.T, req ipc.Request) {
	t.Helper()
	writeSpoolLines(t, rig.dd.root, "client-4242.ndjson", req)
	_, err := rig.dd.Drain(context.Background())
	require.NoError(t, err)
	require.NoFileExists(t, filepath.Join(paths.Of(rig.dd.root).Spool, "client-4242.ndjson"),
		"fixture sanity: the drain consumed the spooled copy")
}

func (rig *sealRig) wallSamples() int {
	return len(contract.LoadHistory(contract.HistoryPath(rig.dd.root)).PrecompactWallMs)
}

func (rig *sealRig) compactCloses() int64 {
	return rig.dd.m.Counter(counterSegmentClosedPrefix + causeCompact).Value()
}

// TestCheckpoint_ASpooledDuplicateOfASealedPreCompactIsNotSealedAgain: the live PreCompact seals,
// the session works on into the successor segment, and the hook's spooled copy of the same request
// reaches a drain. The copy is acknowledged and changes nothing: no second seal, no compaction close
// of the post-compaction segment, no second wall sample. A spooled PreCompact the daemon never saw
// is still sealed when replayed, and so is the copy of one whose live seal failed.
func TestCheckpoint_ASpooledDuplicateOfASealedPreCompactIsNotSealedAgain(t *testing.T) {
	const sess core.SessionID = "sess-precompact-duplicate"
	t.Run("a copy of a sealed PreCompact", func(t *testing.T) {
		rig := newSealRig(t, sess)
		ctx := context.Background()
		rig.read(t, sess, "toolu_before_compaction", 'a', "src/a.py")
		ck := checkpointRequest(rig.dd, sess, testDeliveryToken('e'))
		ck.Capture = admittedCapture(`{"hook_event_name":"PreCompact"}`)
		require.True(t, rig.dd.dispatchOp(ctx, ck).OK)
		require.Equal(t, int64(1), rig.seals.Load(), "fixture sanity: the live PreCompact sealed")
		closes := rig.compactCloses()
		require.Equal(t, int64(1), closes, "fixture sanity: the tap closed the compacted span")
		require.Equal(t, 1, rig.wallSamples())

		prompt := ipc.Request{
			Op: ipc.OpObservePrompt, Session: sess, Reply: true, TS: core.NowMilli(rig.dd.clk), Nonce: testDeliveryToken('c'),
			Event: &hookio.Event{HookEventName: "UserPromptSubmit", SessionID: sess, CWD: rig.dd.root, Prompt: "carry on"},
		}
		line, err := ipc.EncodeRequest(prompt)
		require.NoError(t, err)
		require.NoError(t, rig.dd.ing.Accept(prompt, line))
		require.Equal(t, dispatchSettled, rig.dd.ing.dispatch(ctx, rig.dd.runIngested, <-rig.dd.ing.ring))
		rig.read(t, sess, "toolu_after_compaction", 'b', "src/b.py")
		post := openTokens(rig.r)
		require.Positive(t, post, "fixture sanity: the post-compaction read is in the successor")

		rig.drainSpooled(t, ck)

		require.Equal(t, int64(1), rig.seals.Load(), "a duplicate of a PreCompact already sealed must not seal again")
		require.Equal(t, closes, rig.compactCloses(), "a duplicate must not close the post-compaction segment as compacted")
		require.Equal(t, post, openTokens(rig.r), "the post-compaction work stays in the open segment")
		require.Equal(t, 1, rig.wallSamples(), "one PreCompact, one wall sample")
	})
	t.Run("a PreCompact the daemon never saw", func(t *testing.T) {
		rig := newSealRig(t, sess)
		rig.read(t, sess, "toolu_before_compaction", 'a', "src/a.py")
		ck := checkpointRequest(rig.dd, sess, testDeliveryToken('e'))
		ck.Capture = admittedCapture(`{"hook_event_name":"PreCompact"}`)

		rig.drainSpooled(t, ck)

		require.Equal(t, int64(1), rig.seals.Load(), "a replayed PreCompact nobody sealed is sealed")
		require.Equal(t, int64(1), rig.compactCloses())
		require.Equal(t, 1, rig.wallSamples())
	})
	t.Run("a copy of a PreCompact whose live seal failed", func(t *testing.T) {
		rig := newSealRig(t, sess)
		ck := checkpointRequest(rig.dd, sess, testDeliveryToken('e'))
		ck.Capture = admittedCapture(`{"hook_event_name":"PreCompact"}`)
		rig.failSeal.Store(true)
		require.True(t, rig.dd.dispatchOp(context.Background(), ck).OK)
		rig.failSeal.Store(false)

		rig.drainSpooled(t, ck)

		require.Equal(t, int64(2), rig.seals.Load(), "the copy retries a seal that failed")
	})
}

// TestCheckpoint_ACopyIsConsumedOnlyAfterASealOfItsPreCompactSucceeded is the wave 22 verifier's
// finding against the row above. The hook spools a PreCompact only when the live reply misses its
// deadline, so the live seal can still be running when a drain replays the copy. The route claimed
// the nonce before it sealed and took any claim for a seal, so the copy was acknowledged, and
// consumed, on the strength of a seal that had not finished. When that seal then failed, the claim
// was released with no copy left to retry it, and the compaction had no checkpoint; base 2bf29705
// sealed it through the copy. A copy is now skipped only once a seal of its PreCompact has
// succeeded. The live seal is held by a channel, not a clock.
func TestCheckpoint_ACopyIsConsumedOnlyAfterASealOfItsPreCompactSucceeded(t *testing.T) {
	const sess core.SessionID = "sess-precompact-in-flight"
	for _, liveFails := range []bool{true, false} {
		name := "the live seal then succeeds"
		if liveFails {
			name = "the live seal then fails"
		}
		t.Run(name, func(t *testing.T) {
			rig := newSealRig(t, sess)
			rig.held, rig.heldAt = make(chan struct{}), make(chan struct{})
			rig.failFirst.Store(liveFails)
			ck := checkpointRequest(rig.dd, sess, testDeliveryToken('e'))
			ck.Capture = admittedCapture(`{"hook_event_name":"PreCompact"}`)
			done := make(chan ipc.Response, 1)
			go func() { done <- rig.dd.dispatchOp(context.Background(), ck) }()
			// The live route's answer, once its seal is let go; a row that fails early lets it go too.
			finish := sync.OnceValue(func() ipc.Response {
				close(rig.held)
				return <-done
			})
			t.Cleanup(func() { finish() })
			<-rig.heldAt

			rig.drainSpooled(t, ck)
			require.Equal(t, int64(1), rig.sealedOK.Load(),
				"the copy is consumed while the live seal is still running, so it must have sealed itself")

			require.True(t, finish().OK)
			if liveFails {
				require.Equal(t, int64(1), rig.sealedOK.Load(), "one PreCompact, one successful seal: the copy's")
			}
		})
	}
}
