package daemon

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
)

// TestIngestWALIsExactBytes pins the WAL durability contract: Accept writes the exact bytes it
// received, single-newline-terminated.
func TestIngestWALIsExactBytes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	clk := testutil.NewFakeClock(testutil.Epoch)
	ing := newIngest(root, config.Defaults(), logging.Nop(), nil, clk)
	t.Cleanup(func() { _ = ing.Close() })

	line := []byte(`{"op":"observe.tool","s":"sess-1","t":1}`)
	req := ipc.Request{Op: ipc.OpObserveTool, Session: "sess-1"}
	require.NoError(t, ing.Accept(req, line))

	walPath := filepath.Join(paths.Of(root).Spool, "wal-sess-1.ndjson")
	got, err := os.ReadFile(walPath)
	require.NoError(t, err)
	require.Equal(t, string(line)+"\n", string(got), "the WAL must contain exactly the received bytes plus one newline")
}

// ingestAcceptBudget is a generous but still meaningful upper bound on the TOTAL wall-clock cost
// of 5 in-process Accept calls: the measured per-call cost is single-digit microseconds
// (BenchmarkIngestAccept), so 500ms for 5 calls is roughly 15,000x headroom over the real cost —
// tight enough to fail on a regression that makes Accept actually block (a full ring's channel
// send has no default case, or a spill re-appears and does synchronous I/O), nowhere near tight
// enough to be flaky under normal OS scheduling jitter. This test does not run in parallel with
// the rest of the suite (no t.Parallel()) specifically so that jitter from unrelated tests'
// goroutines (several of which hold real OS sockets/pipes) cannot pollute this measurement — a
// per-call bound in the low tens of milliseconds was observed to flake under -race with many
// parallel tests contending for scheduler time, for reasons unrelated to Accept's own cost.
const ingestAcceptBudget = 500 * time.Millisecond

// TestIngestRingFullSpillsToSpool pins the non-blocking overflow path: a full ring never blocks
// Accept. Every accepted line reaches the WAL regardless of ring capacity — the WAL append always
// happens first — and a ring-full request is simply dropped from the ring (counted via
// l0_ring_full), never written a second time anywhere: the WAL copy is already durable and is what
// Drain reads.
func TestIngestRingFullSpillsToSpool(t *testing.T) {
	root := t.TempDir()
	clk := testutil.NewFakeClock(testutil.Epoch)
	m := obs.New(clk)
	ing := newIngest(root, config.Defaults(), logging.Nop(), m, clk)
	t.Cleanup(func() { _ = ing.Close() })
	ing.ring = make(chan job, 2) // override the 4096 default so this test can actually fill it

	const n = 5
	start := time.Now()
	for i := 0; i < n; i++ {
		line := []byte(`{"op":"observe.tool","s":"sess-1","t":` + string(rune('0'+i)) + `}`)
		req := ipc.Request{Op: ipc.OpObserveTool, Session: "sess-1"}
		require.NoError(t, ing.Accept(req, line))
	}
	require.Less(t, time.Since(start), ingestAcceptBudget, "Accept must never block on a full ring")

	// All 5 lines reached the WAL regardless of ring capacity.
	walPath := filepath.Join(paths.Of(root).Spool, "wal-sess-1.ndjson")
	walBytes, err := os.ReadFile(walPath)
	require.NoError(t, err)
	require.Equal(t, n, bytes.Count(walBytes, []byte("\n")), "every accepted line must reach the WAL")

	// Exactly ring-capacity (2) landed in the ring; the remaining 3 were dropped from the ring
	// (not written anywhere a second time — the WAL copy above is already durable).
	require.Equal(t, int64(3), m.Counter(counterL0RingFull).Value())

	// Nothing else was written to the spool directory: no client-*.ndjson fallback file exists.
	files, err := ipc.SpoolFiles(paths.Of(root).Spool)
	require.NoError(t, err)
	require.Len(t, files, 1, "the only file in the spool directory must be this session's WAL")
	require.Equal(t, filepath.Base(walPath), filepath.Base(files[0]))
}

// ingestACKWait bounds this test's two channel waits generously (matching this codebase's own
// suiteWait convention in internal/ipc/ipctest) — not run under t.Parallel(), so it is not
// contending with other tests' goroutines for scheduler time the way TestIngestRingFullSpillsToSpool
// was observed to under -race.
const ingestACKWait = 10 * time.Second

// TestIngestACKPrecedesProcessing proves Accept returns — the point at which the server writes the
// ACK — before any worker touches the job, which is what makes the WAL the durability boundary.
func TestIngestACKPrecedesProcessing(t *testing.T) {
	root := t.TempDir()
	clk := testutil.NewFakeClock(testutil.Epoch)
	ing := newIngest(root, config.Defaults(), logging.Nop(), nil, clk)
	t.Cleanup(func() { _ = ing.Close() })

	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ing.Start(ctx, 1, func(context.Context, ipc.Request) {
		entered <- struct{}{}
		<-release
	})

	line := []byte(`{"op":"observe.tool","s":"sess-1","t":1}`)
	req := ipc.Request{Op: ipc.OpObserveTool, Session: "sess-1"}

	done := make(chan struct{})
	go func() {
		require.NoError(t, ing.Accept(req, line))
		close(done)
	}()

	// Accept must complete (the ACK point) before we ever release the worker.
	select {
	case <-done:
	case <-time.After(ingestACKWait):
		t.Fatal("Accept did not return promptly — it must never wait on worker processing")
	}

	// Now confirm the job really was queued (not dropped) by releasing the worker and observing
	// it run.
	select {
	case <-entered:
	case <-time.After(ingestACKWait):
		t.Fatal("worker never received the queued job")
	}
	close(release)
}

// TestIngestWALRotates pins Accept's rotation behaviour past walRotateBytes, using a synthetic
// near-limit walFile rather than actually writing 64 MiB.
func TestIngestWALRotates(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	clk := testutil.NewFakeClock(testutil.Epoch)
	ing := newIngest(root, config.Defaults(), logging.Nop(), nil, clk)
	t.Cleanup(func() { _ = ing.Close() })

	sess := core.SessionID("big")
	wf := &walFile{}
	require.NoError(t, ing.openWALLocked(sess, wf))
	wf.bytes = walRotateBytes - 10 // close to the ceiling
	ing.wals[sess] = wf

	line := bytes.Repeat([]byte("x"), 64) // pushes the segment over the ceiling
	require.NoError(t, ing.appendWAL(sess, line))

	require.Equal(t, 1, ing.wals[sess].seq, "a write that would exceed walRotateBytes must roll to the next segment")

	firstPath := walPath(paths.Of(root).Spool, sess, 0)
	rotatedPath := walPath(paths.Of(root).Spool, sess, 1)
	require.FileExists(t, firstPath)
	require.FileExists(t, rotatedPath)

	rotatedBytes, err := os.ReadFile(rotatedPath)
	require.NoError(t, err)
	require.Equal(t, string(line)+"\n", string(rotatedBytes), "the rotated line must land in the new segment, not the old one")
}

// TestIngestResolvesBlobsEndToEnd pins I-1: ipc.Client externalizes an oversized
// Event.ToolResponse on the LIVE wire path (client.go's Send), not only the spool fallback, so the
// daemon routinely receives {"blob":...} descriptor lines through Accept, not just through Drain.
// This drives a real ipc.Client through a real ipc.Server into ing.Accept exactly as Task 4's
// wiring will, and asserts the request the worker pool ultimately dispatches carries the restored
// payload — and that the blob file is gone afterward.
func TestIngestResolvesBlobsEndToEnd(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)

	clk := testutil.NewFakeClock(testutil.Epoch)
	ing := newIngest(root, config.Defaults(), logging.Nop(), nil, clk)
	t.Cleanup(func() { _ = ing.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	received := make(chan ipc.Request, 1)
	ing.Start(ctx, 1, func(_ context.Context, r ipc.Request) { received <- r })

	srv, err := ipc.NewServer(addr, logging.Nop(), nil, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })
	go func() {
		_ = srv.Serve(ctx, func(_ context.Context, req ipc.Request) ipc.Response {
			line, eerr := ipc.EncodeRequest(req)
			if eerr != nil {
				return ipc.Response{OK: false}
			}
			line = bytes.TrimSuffix(line, []byte("\n")) // Accept's line contract: no trailing '\n'
			if aerr := ing.Accept(req, line); aerr != nil {
				return ipc.Response{OK: false}
			}
			return ipc.Response{OK: true}
		})
	}()

	// The client's own spool must be rooted at the SAME project spool directory the daemon reads
	// from — in production these are the same .qompack/spool, and externalize() writes its blob
	// file relative to the client spool's own path.
	clientSpool, err := ipc.NewSpool(paths.Of(root).Spool)
	require.NoError(t, err)

	cl := ipc.NewClientWithOptions(addr, clientSpool, logging.Nop(), nil, ipc.ClientOptions{
		State: ipc.State{
			Mode: contract.ModeFull, Hot: ipc.HotSync, DaemonEnabled: true,
			MaxPayloadBytes: 64, // small on purpose: forces externalize() to trigger below
		},
		ConnectDeadline: 2 * time.Second,
		AckDeadline:     2 * time.Second,
	})
	t.Cleanup(func() { _ = cl.Close() })

	toolResponse := bytes.Repeat([]byte("x"), 256) // well over the 64-byte externalize threshold
	req := ipc.Request{
		Op: ipc.OpObserveTool, Session: "sess-1",
		Event: &hookio.Event{ToolResponse: toolResponse},
	}
	resp, err := cl.Send(context.Background(), req, 2*time.Second)
	require.NoError(t, err)
	require.True(t, resp.OK, "the daemon must ACK once Accept succeeds, regardless of externalization")

	select {
	case got := <-received:
		require.NotNil(t, got.Event)
		require.Equal(t, string(toolResponse), string(got.Event.ToolResponse),
			"the dispatched event must carry the full payload, not the empty externalized shape")
		require.Empty(t, got.Raw, "the blob descriptor must be cleared once resolved")
	case <-time.After(5 * time.Second):
		t.Fatal("worker never received the dispatched request")
	}

	entries, err := os.ReadDir(paths.Of(root).Spool)
	require.NoError(t, err)
	for _, e := range entries {
		require.False(t, strings.HasPrefix(e.Name(), "blob-"), "the blob file must be deleted once resolved: %s", e.Name())
	}
}
