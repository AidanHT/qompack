package daemon

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
)

// TestIngestWALIsExactBytes pins the WAL durability contract: Accept writes the exact bytes it
// received, single-newline-terminated.
func TestIngestWALIsExactBytes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	clk := newFakeClock(epoch)
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

// TestIngestRingFullWALsEveryLineAndNeverSpills pins the non-blocking overflow path: a full ring
// never blocks Accept. Every accepted line reaches the WAL regardless of ring capacity — the WAL
// append always happens first — and a ring-full request is simply dropped from the ring (counted
// via l0_ring_full), never written a second time anywhere: the WAL copy is already durable and is
// what Drain reads.
//
// It was called TestIngestRingFullSpillsToSpool, which asserted the opposite of what it checks.
// Ruling #23 DELETED the ring-full spill the plan describes — WAL-first ordering already makes
// every line durable, and the spill rested on a byte-identity invariant hookio.Event.Extra
// violates — keeping only the l0_ring_full counter. The final assertion below has required the
// spool directory to hold nothing but the WAL ever since, so the old name told a reader looking
// for the spill's coverage that it was here, and told a reader auditing ruling #23 that the
// deletion had not landed. Both wrong, from a test that passes.
func TestIngestRingFullWALsEveryLineAndNeverSpills(t *testing.T) {
	root := t.TempDir()
	clk := newFakeClock(epoch)
	m := obs.New(clk)
	ing := newIngest(root, config.Defaults(), logging.Nop(), m, clk)
	t.Cleanup(func() { _ = ing.Close() })
	ing.ring = make(chan job, 2) // override the 4096 default so this test can actually fill it

	// Nothing drains this ring, so an Accept that blocked on it would never return: the calls run
	// on their own goroutine and the wait is a hangGuard. A wall-clock bound stood here
	// (ingestAcceptBudget, 500 ms for the five calls) on the premise that Accept costs microseconds;
	// it makes every line durable first, and five fsyncs on hosted windows-latest took 657 ms in ci
	// run 38093108786 with nothing blocked. That bound also caught an Accept that waits a while on
	// a full ring and then drops, which this wait cannot: TestIngestAcceptNeverWaitsOnTheRing reads
	// Accept's code for that instead. A blocking send fails here, and a spill's synchronous write
	// fails the spool assertion below.
	const n = 5
	accepted := make(chan error, 1)
	go func() {
		for i := 0; i < n; i++ {
			line := []byte(`{"op":"observe.tool","s":"sess-1","t":` + string(rune('0'+i)) + `}`)
			req := ipc.Request{Op: ipc.OpObserveTool, Session: "sess-1"}
			if err := ing.Accept(req, line); err != nil {
				accepted <- err
				return
			}
		}
		accepted <- nil
	}()
	select {
	case err := <-accepted:
		require.NoError(t, err)
	case <-hangGuard(t):
		t.Fatal("Accept blocked on a full ring that nothing drains")
	}

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

// TestIngestAcceptNeverWaitsOnTheRing is the ring-full row's other half. That row fails an Accept
// that blocks for good on a full ring, but it cannot time one that waits a while and then drops:
// every Accept first makes its line durable, and a hosted runner's fsync tail outlasts any bound
// short enough to catch such a wait (the 500 ms bound that row carried until 0.3.2 failed on five
// fsyncs taking 657 ms, with nothing blocked). So this row reads Accept's code. Its one send on
// i.ring is the case of a select whose only other clause is default, and that default, with every
// ingest method it calls, waits on no channel and no clock: each select in them keeps a default,
// and they hold no bare send, no bare receive and no timer.
func TestIngestAcceptNeverWaitsOnTheRing(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "ingest.go", nil, 0)
	require.NoError(t, err)
	methods := map[string]*ast.FuncDecl{}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv == nil || len(fd.Recv.List) != 1 {
			continue
		}
		if star, ok := fd.Recv.List[0].Type.(*ast.StarExpr); ok {
			if id, ok := star.X.(*ast.Ident); ok && id.Name == "ingest" {
				methods[fd.Name.Name] = fd
			}
		}
	}
	accept := methods["Accept"]
	require.NotNil(t, accept, "ingest.go declares (*ingest).Accept")

	isRing := func(e ast.Expr) bool {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "ring" {
			return false
		}
		id, ok := sel.X.(*ast.Ident)
		return ok && id.Name == "i"
	}
	var sends []*ast.SendStmt
	var ringSelect *ast.SelectStmt
	ast.Inspect(accept.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SendStmt:
			if isRing(x.Chan) {
				sends = append(sends, x)
			}
		case *ast.SelectStmt:
			for _, cc := range commClauses(x) {
				if send, ok := cc.Comm.(*ast.SendStmt); ok && isRing(send.Chan) {
					ringSelect = x
				}
			}
		}
		return true
	})
	require.Len(t, sends, 1, "Accept sends on i.ring exactly once")
	require.NotNil(t, ringSelect, "Accept's send on i.ring is a select case, never a bare send")

	var dflt *ast.CommClause
	for _, cc := range commClauses(ringSelect) {
		if cc.Comm == nil {
			dflt = cc
		}
	}
	require.Len(t, ringSelect.Body.List, 2, "%s: the ring select has the send and default only, so no "+
		"clause (a timer, a context) can hold Accept on a full ring", fset.Position(ringSelect.Pos()))
	require.NotNil(t, dflt, "%s: the ring select keeps a default, so a full ring drops the job at once",
		fset.Position(ringSelect.Pos()))

	timeWaits := map[string]bool{"Sleep": true, "After": true, "NewTimer": true, "NewTicker": true, "Tick": true}
	seen := map[string]bool{"Accept": true}
	var waits []string
	var visit func(n ast.Node)
	visit = func(n ast.Node) {
		ast.Inspect(n, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectStmt:
				hasDefault := false
				for _, cc := range commClauses(x) {
					hasDefault = hasDefault || cc.Comm == nil
				}
				if !hasDefault {
					waits = append(waits, fset.Position(x.Pos()).String()+": a select with no default")
				}
				// A case of a select that has a default never waits; what runs in each case might.
				for _, cc := range commClauses(x) {
					for _, st := range cc.Body {
						visit(st)
					}
				}
				return false
			case *ast.SendStmt:
				waits = append(waits, fset.Position(x.Pos()).String()+": a bare send")
			case *ast.UnaryExpr:
				if x.Op == token.ARROW {
					waits = append(waits, fset.Position(x.Pos()).String()+": a bare receive")
				}
			case *ast.CallExpr:
				sel, ok := x.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				if id.Name == "time" && timeWaits[sel.Sel.Name] {
					waits = append(waits, fset.Position(x.Pos()).String()+": time."+sel.Sel.Name)
				}
				if fd := methods[sel.Sel.Name]; id.Name == "i" && fd != nil && !seen[sel.Sel.Name] {
					seen[sel.Sel.Name] = true
					visit(fd.Body)
				}
			}
			return true
		})
	}
	for _, st := range dflt.Body {
		visit(st)
	}
	require.Empty(t, waits, "a full ring must cost Accept no wait: its default clause, and the ingest "+
		"methods it calls, wait on nothing")
	require.True(t, seen["requestDrain"], "the default clause still asks for a drain (and was read through it)")
}

// commClauses is sel's clauses: the parser builds every clause of a select as an *ast.CommClause.
func commClauses(sel *ast.SelectStmt) []*ast.CommClause {
	out := make([]*ast.CommClause, 0, len(sel.Body.List))
	for _, c := range sel.Body.List {
		if cc, ok := c.(*ast.CommClause); ok {
			out = append(out, cc)
		}
	}
	return out
}

// hangGuard is the bound on every wait in this package's tests that only turns a deadlock into a
// failure. It fires when the test is about to run out of time: at the test binary's deadline (go
// test -timeout) less hangGuardReserve, or less a tenth of what is left when that is smaller. It
// says nothing about how long the awaited thing may take: a wait that ends before the guard fires
// passes, however slow the host, and one that never ends fails with its own name instead of as the
// binary's timeout panic, which reports no row. With no deadline (-timeout=0) it never fires.
//
// It replaced ingestACKWait, a fixed 10 s, which a co-loaded host's fsync tail outlasted: the
// acknowledgement T14 row's awaitClosed failed after a 44.9 s run whose waits had all completed
// (V6 close-out wave 22, audit 2 #63, D67(b)).
func hangGuard(t *testing.T) <-chan time.Time {
	t.Helper()
	dl, ok := t.Deadline()
	if !ok {
		return nil // a nil channel never fires
	}
	left := time.Until(dl)
	return time.After(left - min(left/10, hangGuardReserve))
}

// hangGuardReserve is the most hangGuard keeps of the binary's deadline for the failing test to
// report and run its cleanups before the timeout panic would.
const hangGuardReserve = 30 * time.Second

// hung reports, without waiting, whether guard (a hangGuard) has fired: for the waits that poll.
func hung(guard <-chan time.Time) bool {
	select {
	case <-guard:
		return true
	default:
		return false
	}
}

// TestIngestACKPrecedesProcessing proves Accept returns — the point at which the server writes the
// ACK — before any worker touches the job, which is what makes the WAL the durability boundary.
func TestIngestACKPrecedesProcessing(t *testing.T) {
	root := t.TempDir()
	clk := newFakeClock(epoch)
	ing := newIngest(root, config.Defaults(), logging.Nop(), nil, clk)
	t.Cleanup(func() { _ = ing.Close() })

	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ing.Start(ctx, 1, func(context.Context, ipc.Request) ipc.Response {
		entered <- struct{}{}
		<-release
		return ipc.Response{OK: true}
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
	case <-hangGuard(t):
		t.Fatal("Accept did not return promptly — it must never wait on worker processing")
	}

	// Now confirm the job really was queued (not dropped) by releasing the worker and observing
	// it run.
	select {
	case <-entered:
	case <-hangGuard(t):
		t.Fatal("worker never received the queued job")
	}
	close(release)
}

// TestIngestWALRotates pins Accept's rotation behaviour past walRotateBytes, using a synthetic
// near-limit walFile rather than actually writing 64 MiB.
func TestIngestWALRotates(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	clk := newFakeClock(epoch)
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

	clk := newFakeClock(epoch)
	ing := newIngest(root, config.Defaults(), logging.Nop(), nil, clk)
	t.Cleanup(func() { _ = ing.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	received := make(chan ipc.Request, 1)
	ing.Start(ctx, 1, func(_ context.Context, r ipc.Request) ipc.Response { received <- r; return ipc.Response{OK: true} })

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
	case <-hangGuard(t):
		t.Fatal("worker never received the dispatched request")
	}

	entries, err := os.ReadDir(paths.Of(root).Spool)
	require.NoError(t, err)
	blobs := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "blob-") {
			blobs++
		}
	}
	require.Equal(t, 1, blobs, "live handling must retain the WAL's source until its drain offset is durable")
}

// TestAcceptWireLineWALsExactlyOneTerminator pins Accept against the bytes the wire path actually
// feeds it. handlers.go hands Accept ipc.EncodeRequest's output, and json.Encoder.Encode
// terminates that with '\n' already — so an Accept that blindly appends its own terminator writes
// a blank separator line after every record. Drain tolerates the blanks (and its comment claims
// the writer never emits them), which is how the doubled terminator stayed invisible while it
// silently broke live-vs-drain dedup: the live key was hashed over the terminated line, the drain
// key over the trimmed one. TestIngestWALIsExactBytes keeps the exact-bytes contract for a caller
// that supplies an unterminated line; this is the same contract for the terminated caller.
func TestAcceptWireLineWALsExactlyOneTerminator(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	clk := newFakeClock(epoch)
	ing := newIngest(root, config.Defaults(), logging.Nop(), nil, clk)
	t.Cleanup(func() { _ = ing.Close() })

	ev := &hookio.Event{HookEventName: "PostToolUse", SessionID: "sess-1", CWD: root}
	req := ipc.Request{Op: ipc.OpObserveTool, Session: "sess-1", Event: ev, TS: core.NowMilli(clk)}
	line, err := ipc.EncodeRequest(req)
	require.NoError(t, err)
	require.True(t, bytes.HasSuffix(line, []byte{'\n'}),
		"precondition: the encoder terminates the line; if this ever changes, "+
			"TestIngestWALIsExactBytes covers the unterminated shape")

	require.NoError(t, ing.Accept(req, line))

	got, err := os.ReadFile(filepath.Join(paths.Of(root).Spool, "wal-sess-1.ndjson"))
	require.NoError(t, err)
	require.Equal(t, string(bytes.TrimSuffix(line, []byte{'\n'}))+"\n", string(got),
		"one record, one terminator: the WAL must hold no blank separator lines")
}

// TestLiveDispatchedLineIsNotRedispatchedByDrain pins same-daemon live-vs-drain dedup. A line accepted
// and dispatched on the live path (WAL append -> ring -> worker, which records the dedup key in
// the seen-set) must be skipped when a later Drain — the SessionEnd flush route or an
// idle tick — re-reads the same WAL file. The two sides can only agree if they hash the same
// bytes: Accept receives the encoder-terminated line while Drain reads the line back trimmed, so
// the key must be computed over the trimmed bytes on both sides. IsLive answers false here
// because that is the flush-route shape — a live session's WAL is skipped wholesale, which is
// exactly what kept this class invisible to the idle-tick tests.
func TestLiveDispatchedLineIsNotRedispatchedByDrain(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	clk := newFakeClock(epoch)
	ing := newIngest(root, config.Defaults(), logging.Nop(), nil, clk)
	t.Cleanup(func() { _ = ing.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var mu sync.Mutex
	liveRuns := 0
	ing.Start(ctx, 1, func(context.Context, ipc.Request) ipc.Response {
		mu.Lock()
		liveRuns++
		mu.Unlock()
		return ipc.Response{OK: true}
	})

	ev := &hookio.Event{HookEventName: "PostToolUse", SessionID: "sess-1", CWD: root}
	req := ipc.Request{Op: ipc.OpObserveTool, Session: "sess-1", Event: ev, TS: core.NowMilli(clk)}
	line, err := ipc.EncodeRequest(req)
	require.NoError(t, err)
	require.NoError(t, ing.Accept(req, line))

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return liveRuns == 1
	}, 2*time.Second, 10*time.Millisecond, "the live path must dispatch the accepted line once")
	// This fixture declares the session ended. Join the worker so its acknowledgement is
	// visible, and close the WAL writer before asking Drain to retire it on Windows.
	cancel()
	ing.Wait()
	require.NoError(t, ing.Close())

	drainRuns := 0
	dr := newDrainer(DrainConfig{
		Root: root, Log: logging.Nop(), Clock: clk, Seen: ing.seen,
		IsLive: func(core.SessionID) bool { return false },
		Dispatch: func(context.Context, ipc.Request) ipc.Response {
			drainRuns++
			return ipc.Response{OK: true}
		},
	})
	n, err := dr.Drain(context.Background())
	require.NoError(t, err)
	require.Zero(t, n, "Drain must dedup the line the live path already dispatched")
	require.Zero(t, drainRuns, "the drain dispatch function must never see an already-dispatched line")

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, liveRuns, "the live count must not move during Drain")
}
