package ipc

import (
	"context"
	"encoding/json"
	"flag"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// This file is white-box (package ipc) so TestSendNeverReturnsError can reach the unexported
// listen() to build raw, deliberately misbehaving listeners (§14's four adversarial behaviors) —
// something ipc.NewServer's Handler-based API cannot express (it always tries to decode and
// respond; some of these behaviors must never respond at all).

// testDeadline is a short, uniform ConnectDeadline/AckDeadline used throughout this file so tests
// run fast without flirting with real scheduling jitter.
const testDeadline = 50 * time.Millisecond

// handlerSeenWait bounds taking a request the server handler has ALREADY received. Send does not
// return until the ACK arrives (§2.4) and the handler pushes onto a buffered channel before
// returning the response that produces that ACK, so by the time Send returns the value is already
// queued and the receive cannot block. The bound exists only so a regression that stopped the
// handler running fails as a named assertion instead of as a package-wide test timeout — the same
// role, and now the same value, as internal/ipc/ipctest's suiteWait and internal/daemon's
// ingestACKWait. It was a bare 2s literal, smaller than either of those for no stated reason
// (V2-MERGE-25 ②), which under load meant a wait whose whole premise is "this cannot block" could
// nevertheless time out.
const handlerSeenWait = 10 * time.Second

// newTestServer starts a real Server running h, cleaned up automatically, and returns the Addr a
// Client can reach it at.
func newTestServer(t *testing.T, h Handler) (Server, Addr) {
	t.Helper()
	addr, err := Resolve(t.TempDir())
	require.NoError(t, err)

	srv, err := NewServer(addr, logging.Nop(), obs.New(core.SystemClock()), MaxLineBytes)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, h) }()
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
		<-done
	})
	return srv, addr
}

// newTestClient builds a real Client against addr, with a short deadline pair and its own spool
// under a fresh subdirectory of t.TempDir(). It is closed automatically.
func newTestClient(t *testing.T, addr Addr, o ClientOptions) (Client, SpoolWriter) {
	t.Helper()
	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool"))
	require.NoError(t, err)

	if o.ConnectDeadline <= 0 {
		o.ConnectDeadline = testDeadline
	}
	if o.AckDeadline <= 0 {
		o.AckDeadline = testDeadline
	}
	c := NewClientWithOptions(addr, spool, logging.Nop(), obs.New(core.SystemClock()), o)
	t.Cleanup(func() { _ = c.Close() })
	return c, spool
}

// readLines returns the non-empty lines of the file at p, or nil if it does not exist.
func readLines(t *testing.T, p string) []string {
	t.Helper()
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func TestNewClient_ToleratesNilDependencies(t *testing.T) {
	require.NotPanics(t, func() {
		c := NewClient(Addr{}, nil, nil, nil)
		require.NotNil(t, c)

		res, err := c.Send(context.Background(), Request{}, 0)
		require.NoError(t, err, "a client with no spool and no daemon must still never error")
		require.False(t, res.OK)

		require.NoError(t, c.Close())
	})
}

// TestSendACKPath is the table's row: an in-process server that ACKs every request leaves the
// spool untouched and reports OK/HotSync.
func TestSendACKPath(t *testing.T) {
	_, addr := newTestServer(t, func(context.Context, Request) Response {
		return Response{OK: true}
	})
	c, spool := newTestClient(t, addr, ClientOptions{})

	res, err := c.Send(context.Background(), Request{Op: OpObserveTool, Session: "s", TS: 1}, time.Second)
	require.NoError(t, err)
	require.True(t, res.OK)
	require.Equal(t, HotSync, res.Hot)

	_, statErr := os.Stat(spool.Path())
	require.True(t, os.IsNotExist(statErr), "spool file must not exist after a successful ACK")
}

// TestSendNAKSwitchesToSpool is the table's row: a refusing server flips the client into spool
// submode; the request lands in the spool; and a second Send for a hot-path op never dials again.
func TestSendNAKSwitchesToSpool(t *testing.T) {
	var calls atomic.Int64
	_, addr := newTestServer(t, func(context.Context, Request) Response {
		calls.Add(1)
		return Response{OK: false}
	})
	c, spool := newTestClient(t, addr, ClientOptions{})

	res, err := c.Send(context.Background(), Request{Op: OpObserveTool, Session: "s", TS: 1}, time.Second)
	require.NoError(t, err)
	require.False(t, res.OK)
	require.Equal(t, HotSpool, res.Hot)
	require.Len(t, readLines(t, spool.Path()), 1)
	require.EqualValues(t, 1, calls.Load())

	res2, err := c.Send(context.Background(), Request{Op: OpObserveTool, Session: "s", TS: 2}, time.Second)
	require.NoError(t, err)
	require.False(t, res2.OK)
	require.EqualValues(t, 1, calls.Load(), "a client already in spool submode must never dial again for a hot-path op")
	require.Len(t, readLines(t, spool.Path()), 2)
}

// TestSendDaemonDownSpoolsAndReturnsNilError is the table's row: nothing listening at addr spools
// the request, returns a nil error, and triggers exactly one lazy-spawn attempt.
func TestSendDaemonDownSpoolsAndReturnsNilError(t *testing.T) {
	root := t.TempDir()
	addr, err := Resolve(root)
	require.NoError(t, err)

	var spawnCalls atomic.Int64
	c, spool := newTestClient(t, addr, ClientOptions{
		ProjectRoot: root,
		Self:        "qompack-fake",
		Spawn: func(string, string) error {
			spawnCalls.Add(1)
			return nil
		},
	})

	res, err := c.Send(context.Background(), Request{Op: OpObserveTool, Session: "s", TS: 1}, time.Second)
	require.NoError(t, err)
	require.False(t, res.OK)
	require.Len(t, readLines(t, spool.Path()), 1)
	require.EqualValues(t, 1, spawnCalls.Load())
}

// TestSendFailedSpoolAppendCountsAsDropped is I-2: a request whose Raw payload alone (no Event,
// so externalize is a no-op) exceeds MaxLineBytes reaches spool.Append intact, which refuses it —
// that refusal must be counted as l0_dropped, never as l0_spooled, or the event is lost while the
// metrics claim it was durably enqueued.
func TestSendFailedSpoolAppendCountsAsDropped(t *testing.T) {
	root := t.TempDir()
	addr, err := Resolve(root) // nothing listening: Send reaches spoolAndReturn via the connect failure path
	require.NoError(t, err)

	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool"))
	require.NoError(t, err)
	reg := obs.New(core.SystemClock())
	c := NewClientWithOptions(addr, spool, logging.Nop(), reg, ClientOptions{
		ConnectDeadline: testDeadline, AckDeadline: testDeadline,
	})
	t.Cleanup(func() { _ = c.Close() })

	oversizeRaw := json.RawMessage(`"` + strings.Repeat("x", MaxLineBytes) + `"`)
	res, err := c.Send(context.Background(), Request{Op: OpObserveTool, Session: "s", TS: 1, Raw: oversizeRaw}, time.Second)
	require.NoError(t, err, "a refused spool append must never surface as an error a hook would propagate")
	require.False(t, res.OK)

	require.EqualValues(t, 1, reg.Counter(counterL0Dropped).Value(), "a refused Append must count as dropped")
	require.EqualValues(t, 0, reg.Counter(counterL0Spooled).Value(), "a refused Append must never be counted as spooled")

	_, statErr := os.Stat(spool.Path())
	require.True(t, os.IsNotExist(statErr), "a refused Append must leave no spool file behind")
}

// TestSendHotSpoolSkipsConnect is the table's row: State.Hot = HotSpool skips connecting entirely
// for a hot-path op, but session.start (not hot-path) still connects.
func TestSendHotSpoolSkipsConnect(t *testing.T) {
	var calls atomic.Int64
	_, addr := newTestServer(t, func(context.Context, Request) Response {
		calls.Add(1)
		return Response{OK: true}
	})

	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool"))
	require.NoError(t, err)
	c := NewClientWithOptions(addr, spool, logging.Nop(), obs.New(core.SystemClock()), ClientOptions{
		ConnectDeadline: testDeadline, AckDeadline: testDeadline,
		State: State{Mode: contract.ModeFull, Hot: HotSpool, DaemonEnabled: true, MaxPayloadBytes: MaxLineBytes},
	})
	t.Cleanup(func() { _ = c.Close() })

	res, err := c.Send(context.Background(), Request{Op: OpObserveTool, Session: "s", TS: 1}, time.Second)
	require.NoError(t, err)
	require.False(t, res.OK)
	require.EqualValues(t, 0, calls.Load(), "observe.tool is hot-path: HotSpool must skip the connect entirely")

	res2, err := c.Send(context.Background(), Request{Op: OpSessionStart, Session: "s", TS: 2, Reply: true}, time.Second)
	require.NoError(t, err)
	require.True(t, res2.OK)
	require.EqualValues(t, 1, calls.Load(), "session.start is not hot-path: it must still connect even in HotSpool")
}

// TestSendModeOffDoesNothing is the table's row: ModeOff dials nothing and writes nothing.
func TestSendModeOffDoesNothing(t *testing.T) {
	addr := Addr{Kind: UnixSocket, Path: filepath.Join(t.TempDir(), "unreachable.sock")}
	spoolPath := filepath.Join(t.TempDir(), "spool")
	spool, err := NewSpool(spoolPath)
	require.NoError(t, err)
	c := NewClientWithOptions(addr, spool, logging.Nop(), obs.New(core.SystemClock()), ClientOptions{
		ConnectDeadline: testDeadline, AckDeadline: testDeadline,
		State: State{Mode: contract.ModeOff},
	})
	t.Cleanup(func() { _ = c.Close() })

	res, err := c.Send(context.Background(), Request{Op: OpObserveTool, Session: "s", TS: 1}, time.Second)
	require.NoError(t, err)
	require.True(t, res.OK)
	require.Equal(t, contract.ModeOff, res.Mode)

	_, statErr := os.Stat(spool.Path())
	require.True(t, os.IsNotExist(statErr), "ModeOff must never write to the spool")
}

// TestSendReplyPath is the table's row: a Reply request returns the handler's response verbatim.
func TestSendReplyPath(t *testing.T) {
	const ctx = "hi"
	_, addr := newTestServer(t, func(context.Context, Request) Response {
		return Response{OK: true, Output: &hookio.Output{
			HookSpecificOutput: &hookio.HSO{AdditionalContext: ctx},
		}}
	})
	c, _ := newTestClient(t, addr, ClientOptions{})

	res, err := c.Send(context.Background(), Request{Op: OpSessionStart, Session: "s", TS: 1, Reply: true}, time.Second)
	require.NoError(t, err)
	require.True(t, res.OK)
	require.NotNil(t, res.Output)
	require.Equal(t, ctx, res.Output.HookSpecificOutput.AdditionalContext)
}

// TestSendOversizeExternalizes is the table's row: a 2 MiB Event.ToolResponse against a 1 MiB
// MaxPayloadBytes never reaches the wire whole — the line the server actually receives is small
// and carries a {"blob":...} reference, and the blob file on disk holds the full payload.
func TestSendOversizeExternalizes(t *testing.T) {
	const payloadSize = 2 << 20
	payload, err := json.Marshal(strings.Repeat("x", payloadSize))
	require.NoError(t, err)

	seen := make(chan Request, 1)
	_, addr := newTestServer(t, func(_ context.Context, req Request) Response {
		seen <- req
		return Response{OK: true}
	})

	spoolDir := filepath.Join(t.TempDir(), "spool")
	spool, err := NewSpool(spoolDir)
	require.NoError(t, err)
	c := NewClientWithOptions(addr, spool, logging.Nop(), obs.New(core.SystemClock()), ClientOptions{
		ConnectDeadline: testDeadline, AckDeadline: testDeadline,
		State: State{Mode: contract.ModeFull, DaemonEnabled: true, MaxPayloadBytes: 1 << 20},
	})
	t.Cleanup(func() { _ = c.Close() })

	req := Request{
		Op: OpObserveTool, Session: "s", TS: 1,
		Event: &hookio.Event{HookEventName: "PostToolUse", ToolResponse: json.RawMessage(payload)},
	}
	res, err := c.Send(context.Background(), req, time.Second)
	require.NoError(t, err)
	require.True(t, res.OK)

	var got Request
	select {
	case got = <-seen:
	case <-time.After(handlerSeenWait):
		t.Fatal("server never received the request")
	}

	line, err := EncodeRequest(got)
	require.NoError(t, err)
	require.Less(t, len(line), 4096, "the line on the wire must be small — the payload rides a side blob, not the socket")

	var ref blobRef
	require.NoError(t, json.Unmarshal(got.Raw, &ref))
	require.Equal(t, blobField, ref.Field)
	require.EqualValues(t, len(payload), ref.Bytes)

	blobBytes, err := os.ReadFile(filepath.Join(spoolDir, ref.Blob))
	require.NoError(t, err)
	require.Equal(t, payload, blobBytes)

	// A nulled json.RawMessage marshals as the literal `null` and decodes back as those four
	// bytes, not as an empty/nil RawMessage — the same generic-decoder quirk frame_test.go
	// documents via rawMessageNullIsNil. DecodeRequest has no hookio-specific knowledge to
	// normalize it, so either shape here is the correct "nulled" outcome.
	require.True(t, len(got.Event.ToolResponse) == 0 || string(got.Event.ToolResponse) == "null",
		"the wire copy's ToolResponse must be nulled, got %q", got.Event.ToolResponse)
}

// TestSendOversizeExternalizePreservesExistingRaw is I-3: a request that legitimately carries
// both a non-empty Raw and an oversized Event.ToolResponse must not have Raw silently destroyed
// by externalization — wire.go allows both fields simultaneously, and externalize only ever
// intends to move ToolResponse.
func TestSendOversizeExternalizePreservesExistingRaw(t *testing.T) {
	const payloadSize = 2 << 20
	payload, err := json.Marshal(strings.Repeat("x", payloadSize))
	require.NoError(t, err)
	const originalRaw = `{"pre-existing":true}`

	seen := make(chan Request, 1)
	_, addr := newTestServer(t, func(_ context.Context, req Request) Response {
		seen <- req
		return Response{OK: true}
	})

	spoolDir := filepath.Join(t.TempDir(), "spool")
	spool, err := NewSpool(spoolDir)
	require.NoError(t, err)
	c := NewClientWithOptions(addr, spool, logging.Nop(), obs.New(core.SystemClock()), ClientOptions{
		ConnectDeadline: testDeadline, AckDeadline: testDeadline,
		State: State{Mode: contract.ModeFull, DaemonEnabled: true, MaxPayloadBytes: 1 << 20},
	})
	t.Cleanup(func() { _ = c.Close() })

	req := Request{
		Op: OpObserveTool, Session: "s", TS: 1,
		Event: &hookio.Event{HookEventName: "PostToolUse", ToolResponse: json.RawMessage(payload)},
		Raw:   json.RawMessage(originalRaw),
	}
	res, err := c.Send(context.Background(), req, time.Second)
	require.NoError(t, err)
	require.True(t, res.OK)

	var got Request
	select {
	case got = <-seen:
	case <-time.After(handlerSeenWait):
		t.Fatal("server never received the request")
	}

	var ref blobRef
	require.NoError(t, json.Unmarshal(got.Raw, &ref))
	require.Equal(t, blobField, ref.Field)
	require.JSONEq(t, originalRaw, string(ref.X), "the pre-existing Raw payload must survive under the blob descriptor's own key")
}

// TestClientClose_ClosesSpoolAndTolerantOfNilSpool asserts Close reaches the concrete spool's own
// Close through the type assertion, and is a safe no-op against a nil SpoolWriter.
func TestClientClose_ClosesSpoolAndTolerantOfNilSpool(t *testing.T) {
	c := NewClient(Addr{}, nil, logging.Nop(), obs.New(core.SystemClock()))
	require.NoError(t, c.Close())

	spool, err := NewSpool(t.TempDir())
	require.NoError(t, err)
	c2 := NewClient(Addr{}, spool, logging.Nop(), obs.New(core.SystemClock()))
	require.NoError(t, c2.Close())
	require.NoError(t, c2.Close(), "Close must tolerate being called twice")
}

// TestLazySpawn_CalledOnceAcrossManyFailedSends asserts lazySpawn's per-client at-most-once
// guarantee: many failed Sends against an unreachable daemon must still spawn only once.
func TestLazySpawn_CalledOnceAcrossManyFailedSends(t *testing.T) {
	root := t.TempDir()
	addr, err := Resolve(root)
	require.NoError(t, err)

	var spawnCalls atomic.Int64
	c, _ := newTestClient(t, addr, ClientOptions{
		ProjectRoot: root,
		Self:        "qompack-fake",
		Spawn: func(string, string) error {
			spawnCalls.Add(1)
			return nil
		},
	})

	for i := 0; i < 5; i++ {
		_, err := c.Send(context.Background(), Request{Op: OpObserveTool, Session: "s", TS: core.UnixMilli(i)}, time.Second)
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, spawnCalls.Load())
}

// TestLazySpawn_SpoolIsDurableBeforeSpawn asserts the ordering the daemon's startup drain depends
// on: by the time Spawn is invoked, the spooled entry is already readable on disk.
//
// This is the invariant, not an implementation detail. daemon.Run drains the spool once at startup
// and then only on an idle tick, which is up to idleTickMax (30s) away. A client that spawns first
// and appends second lets the daemon scan an empty directory and miss the very entry that caused
// the cold start — recorded durably, but invisible for half a minute. It fails only under load,
// which is why it reached a full-suite run rather than this package.
//
// Asserting from inside Spawn is what makes it deterministic: it samples the exact instant the
// daemon process would begin, with no timing bound to tune.
func TestLazySpawn_SpoolIsDurableBeforeSpawn(t *testing.T) {
	root := t.TempDir()
	addr, err := Resolve(root)
	require.NoError(t, err)

	// spoolPath is filled in below, before the Send that triggers the closure.
	var spoolPath, spoolAtSpawn string
	var sawSpawn bool
	c, spool := newTestClient(t, addr, ClientOptions{
		ProjectRoot: root,
		Self:        "qompack-fake",
		Spawn: func(string, string) error {
			sawSpawn = true
			if b, readErr := os.ReadFile(spoolPath); readErr == nil {
				spoolAtSpawn = string(b)
			}
			return nil
		},
	})
	spoolPath = spool.Path()

	_, err = c.Send(context.Background(), Request{Op: OpObserveTool, Session: "s", TS: 1}, time.Second)
	require.NoError(t, err)
	require.True(t, sawSpawn, "the connect failure must have reached lazySpawn")

	// Read through the SpoolWriter's own path so this cannot pass by reading some other file.
	onDisk, readErr := os.ReadFile(spool.Path())
	require.NoError(t, readErr, "the failed Send must have spooled")
	require.NotEmpty(t, onDisk)
	require.Equal(t, string(onDisk), spoolAtSpawn,
		"the spooled entry must already be on disk when Spawn is called — the daemon it launches "+
			"drains once at startup and then not again until an idle tick")
}

// TestLazySpawn_SkippedWithoutSelfOrSpawn asserts lazySpawn does nothing — not even take the
// lock file — when Self or Spawn is unset, which is how a caller opts out entirely.
func TestLazySpawn_SkippedWithoutSelfOrSpawn(t *testing.T) {
	root := t.TempDir()
	addr, err := Resolve(root)
	require.NoError(t, err)

	c, _ := newTestClient(t, addr, ClientOptions{ProjectRoot: root}) // no Self, no Spawn
	_, err = c.Send(context.Background(), Request{Op: OpObserveTool, Session: "s", TS: 1}, time.Second)
	require.NoError(t, err)

	_, statErr := os.Stat(filepath.Join(root, ".qompack", "run", spawnLockName))
	require.True(t, os.IsNotExist(statErr), "lazySpawn must not even take the lock when Spawn is nil")
}

// TestLazySpawn_StaleLockIsReclaimed asserts a spawn.lock older than spawnLockStaleAfter is
// removed and retried rather than permanently blocking every future lazy spawn in this project.
func TestLazySpawn_StaleLockIsReclaimed(t *testing.T) {
	root := t.TempDir()
	addr, err := Resolve(root)
	require.NoError(t, err)

	runDir := filepath.Join(root, ".qompack", "run")
	require.NoError(t, os.MkdirAll(runDir, 0o700))
	lockPath := filepath.Join(runDir, spawnLockName)

	clk := &fakeSpawnClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	staleMS := clk.Now().Add(-spawnLockStaleAfter - time.Second).UnixMilli()
	require.NoError(t, os.WriteFile(lockPath, []byte(strconv.FormatInt(staleMS, 10)), 0o600))

	var spawnCalls atomic.Int64
	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool"))
	require.NoError(t, err)
	c := NewClientWithOptions(addr, spool, logging.Nop(), obs.New(core.SystemClock()), ClientOptions{
		ConnectDeadline: testDeadline, AckDeadline: testDeadline,
		ProjectRoot: root, Self: "qompack-fake", Clock: clk,
		Spawn: func(string, string) error { spawnCalls.Add(1); return nil },
	})
	t.Cleanup(func() { _ = c.Close() })

	_, err = c.Send(context.Background(), Request{Op: OpObserveTool, Session: "s", TS: 1}, time.Second)
	require.NoError(t, err)
	require.EqualValues(t, 1, spawnCalls.Load(), "a stale lock must be reclaimed, not treated as an in-flight spawn")
}

// TestLazySpawn_FreshLockFromAnotherClientIsHonoured asserts a spawn.lock younger than
// spawnLockStaleAfter is left alone: another client is assumed to already be spawning.
func TestLazySpawn_FreshLockFromAnotherClientIsHonoured(t *testing.T) {
	root := t.TempDir()
	addr, err := Resolve(root)
	require.NoError(t, err)

	runDir := filepath.Join(root, ".qompack", "run")
	require.NoError(t, os.MkdirAll(runDir, 0o700))
	lockPath := filepath.Join(runDir, spawnLockName)
	require.NoError(t, os.WriteFile(lockPath, []byte(strconv.FormatInt(time.Now().UnixMilli(), 10)), 0o600))

	var spawnCalls atomic.Int64
	c, _ := newTestClient(t, addr, ClientOptions{
		ProjectRoot: root, Self: "qompack-fake",
		Spawn: func(string, string) error { spawnCalls.Add(1); return nil },
	})

	_, err = c.Send(context.Background(), Request{Op: OpObserveTool, Session: "s", TS: 1}, time.Second)
	require.NoError(t, err)
	require.EqualValues(t, 0, spawnCalls.Load(), "a fresh lock means another client is already spawning")
}

// fakeSpawnClock is a minimal, independently-advanceable core.Clock for the lazySpawn staleness
// tests: it needs to report a fixed "now" and support Add on that instant, which is a shape
// testutil.FakeClock does not currently need to expose in a form Add-able like this without
// importing testutil (an internal/testutil package that is itself a composition root ipc must not
// depend on, per §3.2).
type fakeSpawnClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeSpawnClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeSpawnClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }

// --- TestSendNeverReturnsError: the rapid property test -------------------------------------

// randomRequest draws a request across the whole KnownOps vocabulary, reusing
// TestDecodeRequestRoundTrip's generator shape (frame_test.go) so this file does not invent a
// second one.
func randomRequest(rt *rapid.T) Request {
	req := Request{
		Op:      rapid.SampledFrom(KnownOps()).Draw(rt, "op"),
		Session: core.SessionID(rapid.StringMatching(`[a-zA-Z0-9_-]{1,24}`).Draw(rt, "session")),
		TS:      core.UnixMilli(rapid.Int64Range(0, 4102444800000).Draw(rt, "ts")),
		Reply:   rapid.Bool().Draw(rt, "reply"),
	}
	if rapid.Bool().Draw(rt, "hasEvent") {
		req.Event = &hookio.Event{
			HookEventName: rapid.StringMatching(`[A-Za-z]{1,20}`).Draw(rt, "hookEventName"),
			ToolName:      rapid.StringMatching(`[A-Za-z]{0,20}`).Draw(rt, "toolName"),
		}
	}
	return req
}

// acceptedConns tracks connections a raw test listener has accepted, so misbehaving-listener
// helpers can close them all at cleanup instead of leaking file/pipe handles across 200 iterations.
type acceptedConns struct {
	mu    sync.Mutex
	conns []net.Conn
}

func (a *acceptedConns) add(c net.Conn) {
	a.mu.Lock()
	a.conns = append(a.conns, c)
	a.mu.Unlock()
}

func (a *acceptedConns) closeAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, c := range a.conns {
		_ = c.Close()
	}
}

// startMisbehavingListener binds addr and runs behavior against every accepted connection until
// the listener is closed at test cleanup:
//
//   - "closes-immediately": the connection is closed the instant it is accepted, before reading
//     or writing anything — the client may or may not get its write in before the reset.
//   - "hangs": the connection is accepted and then left completely alone (no read, no write,
//     no close) until cleanup — the client's own deadline is what has to end the call, not
//     anything the server does.
//   - "bogus-byte": whatever the client wrote is drained best-effort, then a single byte that is
//     neither ACK nor NAK is written back.
func startMisbehavingListener(t *testing.T, addr Addr, behavior string) {
	t.Helper()
	ln, err := listen(addr, logging.Nop(), MaxLineBytes)
	require.NoError(t, err)

	accepted := &acceptedConns{}
	t.Cleanup(func() {
		_ = ln.Close()
		accepted.closeAll()
	})

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.add(conn)
			switch behavior {
			case "closes-immediately":
				_ = conn.Close()
			case "hangs":
				// deliberately do nothing: held open until accepted.closeAll() at cleanup.
			case "bogus-byte":
				go func(c net.Conn) {
					buf := make([]byte, 4096)
					_, _ = c.Read(buf)
					_, _ = c.Write([]byte{0xFF})
				}(conn)
			}
		}
	}()
}

// TestSendNeverReturnsError is task-2-spec.md's rapid property test: 200 random requests spread
// across 4 adversarial listener behaviors (50 checks each, set via the rapid.checks flag), none of
// which may ever cause Send to return a non-nil error.
func TestSendNeverReturnsError(t *testing.T) {
	// rapid.checks is a process-wide flag: left mutated, it would silently halve the rigor of
	// every other rapid.Check in this package (e.g. frame_test.go's request/response round trip),
	// since Go runs a package's tests in one process. Save and restore it around this test only.
	checksFlag := flag.Lookup("rapid.checks")
	require.NotNil(t, checksFlag, "pgregory.net/rapid must have registered its -rapid.checks flag")
	original := checksFlag.Value.String()
	require.NoError(t, flag.Set("rapid.checks", "50"))
	t.Cleanup(func() { _ = flag.Set("rapid.checks", original) })

	for _, behavior := range []string{"no-listener", "closes-immediately", "hangs", "bogus-byte"} {
		t.Run(behavior, func(t *testing.T) {
			rapid.Check(t, func(rt *rapid.T) {
				root := t.TempDir()
				addr, err := Resolve(root)
				require.NoError(rt, err)

				if behavior != "no-listener" {
					startMisbehavingListener(t, addr, behavior)
				}

				spool, err := NewSpool(filepath.Join(root, "spool"))
				require.NoError(rt, err)
				c := NewClientWithOptions(addr, spool, logging.Nop(), obs.New(core.SystemClock()), ClientOptions{
					ConnectDeadline: testDeadline, AckDeadline: testDeadline,
				})
				defer func() { _ = c.Close() }()

				req := randomRequest(rt)

				start := time.Now()
				res, err := c.Send(context.Background(), req, testDeadline)
				elapsed := time.Since(start)

				require.NoError(rt, err, "Send must never return an error, whatever the listener does")
				require.NotEqual(rt, "unknown", res.Mode.String())
				require.LessOrEqual(rt, elapsed, 3*testDeadline+500*time.Millisecond,
					"a single Send must not block far past its own deadlines")
			})
		})
	}
}
