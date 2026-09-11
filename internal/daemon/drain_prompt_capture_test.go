package daemon

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// TestCarriedDefect_SP08D3_DrainedPromptIsNeverCaptured is SP08-D3's evidence test. It pins WRONG
// behaviour, deliberately: the V6 fix must invert it, and this test failing is how that fix shows.
//
// An observe.prompt that reaches the daemon only by WAL/spool replay is never verbatim-captured.
// drainDispatch routes it to runIngested, whose prompt arm runs only the sentinel scan, and the
// drain then acknowledges the delivery and deletes its spool copy. That is the loss for every prompt
// no daemon captured live: a cold start, an idle-exit respawn, a connect failure, HotSpool, a crash
// before the capture landed.
//
// The second half pins what that loss turns into. With turn 0 never captured, the observer's turn
// counter never moved, so the next LIVE prompt is recorded as prompt_<s>_0 — the id the rehydrator
// reads as the session's verbatim original. That live capture is also the in-test positive control:
// the same rig, store and observer do capture, so the missing "first" is the replay arm's doing.
//
// It runs the real store and the real observer through WireObserver, and the drainer Run builds.
func TestCarriedDefect_SP08D3_DrainedPromptIsNeverCaptured(t *testing.T) {
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	dd.drain.Store(newDrainer(DrainConfig{
		Root: root, Log: dd.log, Metrics: dd.m, Clock: dd.clk,
		Dispatch: dd.drainDispatch, Seen: dd.ing.seen, Admit: dd.admitDelivery,
		Journal: dd.deliveryJournal, IsLive: dd.sessionIsLive,
	}))
	// Registered last, so it runs first: the live capture below runs on a goroutine of its own, and
	// is joined here before wireTestDaemon's cleanups close the store it writes to.
	t.Cleanup(func() {
		grace, cancel := context.WithTimeout(context.Background(), promptRecordWait)
		defer cancel()
		dd.stopPromptRecordings(grace)
	})

	const sess core.SessionID = "sess-sp08d3-replay"
	const spoolFile = "client-00001.ndjson"
	ctx := context.Background()
	prompt := func(nonce, text string) ipc.Request {
		return ipc.Request{
			Op: ipc.OpObservePrompt, Session: sess, TS: core.NowMilli(dd.clk), Reply: true, Nonce: nonce,
			Event: &hookio.Event{HookEventName: "UserPromptSubmit", SessionID: sess, CWD: root, Prompt: text},
		}
	}

	// The hook client spooled the session's first prompt because no daemon answered it.
	first := prompt(testDeliveryToken('d'), "first")
	writeSpoolLine(t, root, spoolFile, first)

	n, err := dd.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "the spooled prompt is dispatched once")

	journal, err := dd.deliveryJournal()
	require.NoError(t, err)
	require.True(t, journal.acknowledged(first.Nonce),
		"the replayed prompt reaches the committed frontier, so nothing will ever redeliver it")
	_, statErr := os.Stat(paths.Long(filepath.Join(paths.Of(root).Spool, spoolFile)))
	require.ErrorIs(t, statErr, fs.ErrNotExist, "and its spool copy, the last record of it, is deleted")

	id := observer.VerbatimPromptID(sess, 0)
	_, err = o.Store.ToolUse(ctx, id)
	require.ErrorIs(t, err, core.ErrNotFound,
		"SP08-D3: the replayed prompt was acknowledged without a verbatim capture; no %s exists", id)
	require.Equal(t, int64(0), dd.m.Counter(observerPromptPutErr).Value(),
		"the capture was never attempted, so this is not a soft-drop into %s", observerPromptPutErr)
	require.Equal(t, int64(1), dd.m.Counter(counterPromptReplayedUncaptured).Value(),
		"the loss must be countable: %s", counterPromptReplayedUncaptured)

	// The substitution. A live prompt in the same session is captured, at turn 0.
	resp := dd.dispatchOp(ctx, prompt(testDeliveryToken('e'), "second"))
	require.True(t, resp.OK)
	var rec store.ToolUseRecord
	require.Eventually(t, func() bool {
		r, lookupErr := o.Store.ToolUse(ctx, id)
		if lookupErr != nil {
			return false
		}
		rec = r
		return true
	}, promptRecordWait, 10*time.Millisecond, "the live capture never landed: no %s index record", id)
	require.Equal(t, core.TurnIndex(0), rec.Turn,
		"with turn 0 never captured, the next live prompt takes the original's turn")
	rc, err := o.Store.Open(ctx, rec.Root)
	require.NoError(t, err)
	stored, err := io.ReadAll(rc)
	require.NoError(t, rc.Close())
	require.NoError(t, err)
	require.Equal(t, "second", string(stored),
		"%s, the id the rehydrator reads as the verbatim original, holds the later prompt", id)

	// The live worker shares runIngested. Running the live delivery's queued job through it must not
	// count: the reply path captured that prompt, and the counter is for replays only.
	drainRing(t, dd)
	require.Equal(t, int64(1), dd.m.Counter(counterPromptReplayedUncaptured).Value(),
		"only drainDispatch counts; a live job through runIngested is not a replay")
}
