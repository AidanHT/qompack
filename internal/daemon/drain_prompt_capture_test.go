package daemon

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// SP08-D3 regression suite. These INVERT the original evidence test, which pinned the defect: a
// prompt reaching the daemon only by WAL/spool replay was never verbatim-captured, the observer's
// turn counter never advanced, and the next live prompt took prompt_<s>_0 — the id the rehydrator
// serves as the verbatim original.
//
// Original test id (CARRIED-DEFECTS.tsv row SP08-D3):
//   TestCarriedDefect_SP08D3_DrainedPromptIsNeverCaptured  ->  now
//   TestCarriedDefect_SP08D3_ReplayedPromptIsCapturedAtTurnZero (below), plus the focused cases.
// (CARRIED-DEFECTS.tsv is main-owned; the row's status/name are main's to reconcile.)

func spD3Prompt(dd *daemon, root string, sess core.SessionID, nonce, text string) ipc.Request {
	return ipc.Request{
		Op: ipc.OpObservePrompt, Session: sess, TS: core.NowMilli(dd.clk), Reply: true, Nonce: nonce,
		Event: &hookio.Event{HookEventName: "UserPromptSubmit", SessionID: sess, CWD: root, Prompt: text},
	}
}

func spD3Drainer(dd *daemon, root string) {
	dd.drain.Store(newDrainer(DrainConfig{
		Root: root, Log: dd.log, Metrics: dd.m, Clock: dd.clk,
		Dispatch: dd.drainDispatch, Seen: dd.ing.seen, Admit: dd.admitDelivery,
		Journal: dd.deliveryJournal, IsLive: dd.sessionIsLive,
	}))
}

func spD3PromptText(t *testing.T, o *Options, id core.ToolUseID) string {
	t.Helper()
	ctx := context.Background()
	rec, err := o.Store.ToolUse(ctx, id)
	require.NoError(t, err, "no %s index record", id)
	rc, err := o.Store.Open(ctx, rec.Root)
	require.NoError(t, err)
	b, err := io.ReadAll(rc)
	require.NoError(t, rc.Close())
	require.NoError(t, err)
	return string(b)
}

// TestCarriedDefect_SP08D3_ReplayedPromptIsCapturedAtTurnZero is the inverted SP08-D3 evidence test:
// a spool/WAL-replayed prompt IS verbatim-captured at turn 0, and a later LIVE prompt stays at turn
// 1 rather than taking the original's slot.
func TestCarriedDefect_SP08D3_ReplayedPromptIsCapturedAtTurnZero(t *testing.T) {
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	spD3Drainer(dd, root)
	t.Cleanup(func() {
		grace, cancel := context.WithTimeout(context.Background(), promptRecordWait)
		defer cancel()
		dd.stopPromptRecordings(grace)
	})

	const sess core.SessionID = "sess-sp08d3-replay"
	const spoolFile = "client-00001.ndjson"
	ctx := context.Background()

	// The hook client spooled the session's first prompt because no daemon answered it.
	first := spD3Prompt(dd, root, sess, testDeliveryToken('d'), "first")
	writeSpoolLine(t, root, spoolFile, first)

	n, err := dd.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "the spooled prompt is dispatched once")

	journal, err := dd.deliveryJournal()
	require.NoError(t, err)
	require.True(t, journal.acknowledged(first.Nonce),
		"the frontier is acknowledged BECAUSE the verbatim capture became durable")
	_, statErr := os.Stat(paths.Long(filepath.Join(paths.Of(root).Spool, spoolFile)))
	require.ErrorIs(t, statErr, fs.ErrNotExist, "its spool copy is released only after a durable capture")

	id0 := observer.VerbatimPromptID(sess, 0)
	rec0, err := o.Store.ToolUse(ctx, id0)
	require.NoError(t, err, "SP08-D3 fixed: the replayed prompt IS captured at turn 0 (%s)", id0)
	require.Equal(t, core.TurnIndex(0), rec0.Turn)
	require.Equal(t, "first", spD3PromptText(t, o, id0), "%s holds the replayed prompt's own text", id0)

	require.Equal(t, int64(0), dd.m.Counter(observerPromptPutErr).Value(),
		"a successful capture is no soft-drop into %s", observerPromptPutErr)
	require.Equal(t, int64(0), dd.m.Counter(counterPromptReplayedUncaptured).Value(),
		"a captured replay is not a loss; %s is retired to zero", counterPromptReplayedUncaptured)

	// A live prompt in the same session is captured at turn 1 — NOT turn 0. The reply path shows only
	// the (absent) warning; the worker records it when drainRing runs the queued job.
	resp := dd.dispatchOp(ctx, spD3Prompt(dd, root, sess, testDeliveryToken('e'), "second"))
	require.True(t, resp.OK)
	if resp.Output != nil {
		require.Nil(t, resp.Output.HookSpecificOutput, "no warning is pending, so the reply injects nothing")
	}
	drainRing(t, dd)

	id1 := observer.VerbatimPromptID(sess, 1)
	rec1, err := o.Store.ToolUse(ctx, id1)
	require.NoError(t, err, "the live prompt is captured at turn 1 (%s)", id1)
	require.Equal(t, core.TurnIndex(1), rec1.Turn, "with turn 0 already captured, the live prompt takes turn 1")
	require.Equal(t, "second", spD3PromptText(t, o, id1))

	require.Equal(t, "first", spD3PromptText(t, o, id0), "%s still holds the ORIGINAL, not the later prompt", id0)
	_, err = o.Store.ToolUse(ctx, observer.VerbatimPromptID(sess, 2))
	require.ErrorIs(t, err, core.ErrNotFound, "the live worker job does not duplicate into a third turn")
	require.Equal(t, int64(0), dd.m.Counter(counterPromptReplayedUncaptured).Value(),
		"a live job through runIngested is not a counted replay loss")
}

// TestCarriedDefect_SP08D3_SpooledHostFirstPromptLosesTurnZero pins the half of SP08-D3 the
// V6 fix does not close (acceptance item 2): turn order is publication order, and a prompt that
// reached only a hook's client spool is published in an order that is not the host's. The ordering
// gate orders LEASED arrivals only, a client-spool line is leased when a drain reaches it, and
// nothing orders a replay by req.TS. So the host's second prompt becomes prompt_<s>_0 — the id
// internal/rehydrate serves as the verbatim original, checking only its session and turn — and the
// host-first prompt is captured at turn 1, in two ways:
//
//   - live_second: the first prompt's hook could not reach the daemon and spooled it; the second
//     arrives live and its worker publishes it before any drain replays the spool.
//   - spool_file_order: both prompts were spooled, each by its own hook process into its own
//     client-<pid>.ndjson; ipc.SpoolFiles drains client spools in file-name order, and a pid says
//     nothing about time (nor does an unpadded decimal sort as a number).
//
// Neither of item 2's resolutions (a req.TS order, or a rehydrator that stops presenting a later
// turn as the original) exists yet. This asserts TODAY's wrong outcome on purpose: fixing the
// residual makes it fail, and the CARRIED-DEFECTS.tsv row must then move to fixed with this test
// inverted.
func TestCarriedDefect_SP08D3_SpooledHostFirstPromptLosesTurnZero(t *testing.T) {
	for _, tc := range []struct {
		name string
		// deliver hands the daemon the host's two prompts, first then second, and returns once
		// both have been published.
		deliver func(t *testing.T, dd *daemon, root string, first, second ipc.Request)
	}{
		{"live_second", func(t *testing.T, dd *daemon, root string, first, second ipc.Request) {
			writeSpoolLine(t, root, "client-7.ndjson", first)
			_, err := dd.deliveryJournal()
			require.NoError(t, err)
			resp := dd.dispatchOp(context.Background(), second)
			require.True(t, resp.OK, "the live prompt is accepted: %+v", resp)
			drainRing(t, dd)
			n, err := dd.Drain(context.Background())
			require.NoError(t, err)
			require.Equal(t, 1, n, "the spooled prompt is replayed once, after the live one published")
		}},
		{"spool_file_order", func(t *testing.T, dd *daemon, root string, first, second ipc.Request) {
			// Host order: pid 9's hook spooled first, pid 10's second. "client-10" sorts first.
			writeSpoolLine(t, root, "client-9.ndjson", first)
			writeSpoolLine(t, root, "client-10.ndjson", second)
			n, err := dd.Drain(context.Background())
			require.NoError(t, err)
			require.Equal(t, 2, n, "both spooled prompts are replayed in one pass")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			_, dd, o := wireTestDaemon(t, root, nil)
			lock := lockFor(t, dd, root)
			t.Cleanup(func() { _ = lock.Release() })
			spD3Drainer(dd, root)
			t.Cleanup(func() {
				grace, cancel := context.WithTimeout(context.Background(), promptRecordWait)
				defer cancel()
				dd.stopPromptRecordings(grace)
			})

			const sess core.SessionID = "sess-sp08d3-spooled-first"
			first := spD3Prompt(dd, root, sess, testDeliveryToken('a'), "first")
			second := spD3Prompt(dd, root, sess, testDeliveryToken('b'), "second")
			require.LessOrEqual(t, first.TS, second.TS, "the host sent \"first\" first")
			tc.deliver(t, dd, root, first, second)

			// Both are captured (the V6 fix), in publication order rather than host order.
			require.Equal(t, "second", spD3PromptText(t, o, observer.VerbatimPromptID(sess, 0)),
				"SP08-D3 residual: the host's second prompt takes turn 0, the rehydrator's verbatim original")
			require.Equal(t, "first", spD3PromptText(t, o, observer.VerbatimPromptID(sess, 1)),
				"SP08-D3 residual: the host-first prompt is captured at turn 1")
		})
	}
}

// TestSP08D3_DistinctSameTextRepliesStayDistinct: two replayed prompts with IDENTICAL text are two
// turns, not one. There is no content-based dedup — turn identity keeps them apart.
func TestSP08D3_DistinctSameTextRepliesStayDistinct(t *testing.T) {
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	spD3Drainer(dd, root)
	t.Cleanup(func() {
		grace, cancel := context.WithTimeout(context.Background(), promptRecordWait)
		defer cancel()
		dd.stopPromptRecordings(grace)
	})

	const sess core.SessionID = "sess-sp08d3-sametext"
	ctx := context.Background()
	writeSpoolLine(t, root, "client-1.ndjson", spD3Prompt(dd, root, sess, testDeliveryToken('a'), "hello"))
	_, err := dd.Drain(ctx)
	require.NoError(t, err)
	writeSpoolLine(t, root, "client-2.ndjson", spD3Prompt(dd, root, sess, testDeliveryToken('b'), "hello"))
	_, err = dd.Drain(ctx)
	require.NoError(t, err)

	require.Equal(t, "hello", spD3PromptText(t, o, observer.VerbatimPromptID(sess, 0)))
	require.Equal(t, "hello", spD3PromptText(t, o, observer.VerbatimPromptID(sess, 1)),
		"a distinct delivery with identical text is a distinct turn, not a dedup")
	r0, _ := o.Store.ToolUse(ctx, observer.VerbatimPromptID(sess, 0))
	r1, _ := o.Store.ToolUse(ctx, observer.VerbatimPromptID(sess, 1))
	require.NotEqual(t, r0.ID, r1.ID)
}

// TestSP08D3_CaptureFailureIsNotAcknowledged: a leased prompt whose durable capture fails must NOT
// reach the committed frontier — it stays pending (spool retained) for a later retry, rather than
// being acknowledged as restored (the SP08-D3 contract: unavailable/failure remains explicit
// pending).
func TestSP08D3_CaptureFailureIsNotAcknowledged(t *testing.T) {
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	spD3Drainer(dd, root)

	const sess core.SessionID = "sess-sp08d3-fail"
	const spoolFile = "client-fail.ndjson"
	ctx := context.Background()
	req := spD3Prompt(dd, root, sess, testDeliveryToken('c'), "will-not-persist")
	writeSpoolLine(t, root, spoolFile, req)

	// Force the verbatim Put to fail: a closed store answers PutBytes with an error, which
	// onUserPrompt propagates (leased) so runIngested returns non-OK. The drain then does NOT
	// acknowledge — surfaced as a non-ack (an error or a gap either way; the point is what is not
	// persisted, not how the drain reports it).
	require.NoError(t, o.Store.Close())

	_, _ = dd.Drain(ctx)

	journal, jerr := dd.deliveryJournal()
	require.NoError(t, jerr)
	require.False(t, journal.acknowledged(req.Nonce),
		"a capture that did not become durable must not reach the committed frontier")
	_, statErr := os.Stat(paths.Long(filepath.Join(paths.Of(root).Spool, spoolFile)))
	require.NoError(t, statErr, "the spool copy is retained for retry, not released")
}

// TestSP08D3_ReplayThroughRunIngestedEmitsNoOutput: a replay is recording, never acting. runIngested
// returns no host Output for a prompt, so nothing can be injected late off the replay path.
func TestSP08D3_ReplayThroughRunIngestedEmitsNoOutput(t *testing.T) {
	root := t.TempDir()
	_, dd, _ := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	t.Cleanup(func() {
		grace, cancel := context.WithTimeout(context.Background(), promptRecordWait)
		defer cancel()
		dd.stopPromptRecordings(grace)
	})

	const sess core.SessionID = "sess-sp08d3-noinject"
	// A direct runIngested call, unleased (no observation identity), so linkObservation is a no-op
	// and no pre-written sidecar is required: the point here is that runIngested's prompt arm returns
	// NO host Output whatever the capture did — a replay is recording, never acting.
	ctx := context.Background()
	req := spD3Prompt(dd, root, sess, testDeliveryToken('f'), "please")
	resp := dd.runIngested(ctx, req)
	require.True(t, resp.OK)
	require.Nil(t, resp.Output, "a replayed prompt never returns an injectable Output (no late injection)")

	rec, err := dd.svc.Store.ToolUse(ctx, observer.VerbatimPromptID(sess, 0))
	require.NoError(t, err, "and it still records the verbatim capture")
	require.Equal(t, "please", spD3PromptTextFromStore(t, dd.svc.Store, rec))
}

func spD3PromptTextFromStore(t *testing.T, s store.Store, rec store.ToolUseRecord) string {
	t.Helper()
	rc, err := s.Open(context.Background(), rec.Root)
	require.NoError(t, err)
	b, err := io.ReadAll(rc)
	require.NoError(t, rc.Close())
	require.NoError(t, err)
	return string(b)
}
