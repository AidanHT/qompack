package ipctest

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/stretchr/testify/require"
)

// This file authors the behaviour assertions of the ipctest suites (subplan table, §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md): framing round-trip, the 1 MiB line limit,
// and the ACK/NAK handshake in the form a caller observes it. All are authored now, gated behind
// the same Rule W-1 stub probe as the rest of the suite, so SP-05 inherits them rather than writing
// its own grader.
//
// Every assertion on Response.OK here carries res.Err in its message, and that is not decoration.
// §5.4's never-error rule turns an unreachable or misbehaving daemon into (Response{OK:false},
// nil), so a transport failure arrives at require.True as a bare "Should be true" with the actual
// reason sitting unread in res.Err. That is precisely what made this suite's one recorded flake
// undiagnosable — it failed once under load on a tree byte-identical to develop's and passed 3/3
// in isolation, and the report said nothing about why (§2.0b). Carrying the field costs nothing
// and is the difference between a flake and a diagnosis.

// oversizeRequest returns a Request whose encoded NDJSON line is guaranteed to exceed §2.4's 1 MiB
// frame. It is built from Raw rather than from an Event so it stays a valid Request that a correct
// implementation refuses for its SIZE, not for being malformed.
func oversizeRequest() ipc.Request {
	payload := `"` + strings.Repeat("x", ipc.MaxLineBytes) + `"`
	return ipc.Request{
		Op:      ipc.OpObserveTool,
		Session: probeSession,
		TS:      probeTS,
		Raw:     json.RawMessage(payload),
	}
}

// requestAt returns the probe Request with a distinct TS, so a round-trip case can tell three
// appended lines apart.
func requestAt(ts core.UnixMilli) ipc.Request {
	r := probeRequest()
	r.TS = ts
	return r
}

// readSpoolLines returns the non-empty NDJSON lines currently in s's backing file.
func readSpoolLines(t *testing.T, s ipc.SpoolWriter) []string {
	t.Helper()
	b, err := os.ReadFile(s.Path())
	require.NoError(t, err, "the spool file named by Path must be readable")

	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// runSendNeverErrorsCase is §5.4's central Client promise, and the reason internal/cli can exit 0
// unconditionally: "On ANY failure it spools to disk and returns (Response{OK:false}, nil) — never
// an error that a hook would propagate." A Client that returned an error here would surface a
// Qompack problem as a Claude Code hook failure, which §7.1 forbids outright.
func runSendNeverErrorsCase(t *testing.T, factory func(t *testing.T) ipc.Client) {
	t.Helper()
	c := factory(t)

	res, err := c.Send(context.Background(), probeRequest(), suiteDeadline)
	require.NoError(t, err, "Send must never propagate an error a hook would have to handle (§5.4)")
	requireHonourableResponse(t, res)

	require.NoError(t, c.Close())
}

// runSendReportsHonourableModesCase asserts every Response — success or failure — carries a
// contract mode and a hot-path submode the client can act on. §12.1 has clients stop acting the
// moment the daemon reports a degradation, and §12.2 has them stop connecting the moment it reports
// spool mode; both are unimplementable if either field can come back uninterpretable.
func runSendReportsHonourableModesCase(t *testing.T, factory func(t *testing.T) ipc.Client) {
	t.Helper()
	c := factory(t)

	for _, op := range []ipc.Op{ipc.OpObserveTool, ipc.OpObservePrompt, ipc.OpStatus} {
		req := probeRequest()
		req.Op = op
		res, err := c.Send(context.Background(), req, suiteDeadline)
		require.NoError(t, err)
		requireHonourableResponse(t, res)
	}
}

// runSendCancelledContextCase asserts the never-error rule survives the one case most likely to
// break it: a context that is already cancelled when Send is called. The hook must still exit 0,
// so the cancellation has to become a spooled request and an OK:false response, not an error.
func runSendCancelledContextCase(t *testing.T, factory func(t *testing.T) ipc.Client) {
	t.Helper()
	c := factory(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := c.Send(ctx, probeRequest(), suiteDeadline)
	require.NoError(t, err, "a cancelled context must not turn into an error a hook would propagate")
	requireHonourableResponse(t, res)
}

// runSpoolFramingRoundTripCase is the framing round-trip §15 requires: §2.4's format is one request
// per line, UTF-8 JSON, newline-terminated, and the daemon drains those lines on its next start. A
// line that cannot be parsed back into the Request that produced it is data loss, not a formatting
// nit.
func runSpoolFramingRoundTripCase(t *testing.T, factory func(t *testing.T) ipc.SpoolWriter) {
	t.Helper()
	s := factory(t)

	want := []ipc.Request{requestAt(probeTS), requestAt(probeTS + 1), requestAt(probeTS + 2)}
	for _, r := range want {
		require.NoError(t, s.Append(r))
	}

	lines := readSpoolLines(t, s)
	require.Len(t, lines, len(want), "Append must write exactly one line per request")

	for i, line := range lines {
		var got ipc.Request
		require.NoError(t, json.Unmarshal([]byte(line), &got), "line %d must be valid JSON", i)
		require.Equal(t, want[i].Op, got.Op)
		require.Equal(t, want[i].Session, got.Session)
		require.Equal(t, want[i].TS, got.TS, "lines must be in append order")
	}
}

// runSpoolIsAppendOnlyCase asserts the spool only ever grows and never rewrites what it already
// wrote. The daemon may be draining the file while a client appends to it (§2.4: drain on start and
// on every idle tick), so a writer that rewrote earlier bytes would corrupt a concurrent read.
func runSpoolIsAppendOnlyCase(t *testing.T, factory func(t *testing.T) ipc.SpoolWriter) {
	t.Helper()
	s := factory(t)

	require.NoError(t, s.Append(requestAt(probeTS)))
	before := readSpoolLines(t, s)
	require.Len(t, before, 1)

	require.NoError(t, s.Append(requestAt(probeTS+1)))
	after := readSpoolLines(t, s)

	require.Len(t, after, 2)
	require.Equal(t, before[0], after[0], "an earlier spooled line must never be rewritten")
}

// runSpoolLineLimitCase is §2.4's "1 MiB max line" at the spool. A line above the frame limit could
// never be drained by the daemon that has to read it back, so it must be refused at the point of
// writing — with a known sentinel, core.ErrBudget being the natural one — and nothing may be
// written in its place. Silently truncating would produce a line that parses as a DIFFERENT
// request, which is worse than dropping it.
func runSpoolLineLimitCase(t *testing.T, factory func(t *testing.T) ipc.SpoolWriter) {
	t.Helper()
	s := factory(t)

	require.NoError(t, s.Append(requestAt(probeTS)))
	before := readSpoolLines(t, s)

	err := s.Append(oversizeRequest())
	require.Error(t, err, "a request whose framed line exceeds MaxLineBytes must be refused")
	requireKnownError(t, err)

	after := readSpoolLines(t, s)
	require.Equal(t, before, after, "a refused request must leave the spool byte-identical")
	for _, line := range after {
		require.LessOrEqual(t, len(line)+1, ipc.MaxLineBytes, "no framed line may exceed §2.4's 1 MiB limit")
	}
}

// runSpoolPathIsStableCase asserts Path names one file for the writer's lifetime. /qompack:status
// reports it and the daemon's drain deletes it after draining; a path that changed between calls
// would leave undrained files behind.
func runSpoolPathIsStableCase(t *testing.T, factory func(t *testing.T) ipc.SpoolWriter) {
	t.Helper()
	s := factory(t)

	first := s.Path()
	require.NotEmpty(t, first, "a working SpoolWriter must name the file it appends to")

	require.NoError(t, s.Append(requestAt(probeTS)))
	require.Equal(t, first, s.Path(), "Path must not change once the writer has been used")
}

// runServerAddrStableCase asserts Addr reports the endpoint the Server is actually bound to, and
// reports the same one every time: the client resolves the endpoint independently (§2.4's hash of
// the project root), so a Server whose Addr drifted would be one no client could find.
func runServerAddrStableCase(t *testing.T, factory func(t *testing.T) ipc.Server) {
	t.Helper()
	s := factory(t)
	t.Cleanup(func() { _ = s.Close() })

	addr := s.Addr()
	require.NotEmpty(t, addr.Path, "a Server must report the endpoint it is bound to")
	require.Contains(t, []ipc.AddrKind{ipc.NamedPipe, ipc.UnixSocket}, addr.Kind)
	require.Equal(t, addr, s.Addr(), "Addr must be stable across calls")
}

// runServeReturnsOnCancelCase asserts a cancelled context stops the accept loop. The daemon idles
// out after runtime.daemon.idleExitSeconds with zero live sessions (§2.4), and that shutdown is
// driven by cancelling this context — a Serve that ignored it would leave a daemon resident
// forever.
func runServeReturnsOnCancelCase(t *testing.T, factory func(t *testing.T) ipc.Server) {
	t.Helper()
	s := factory(t)
	t.Cleanup(func() { _ = s.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, nopHandler) }()

	cancel()
	requireServeReturns(t, done)
}

// runCloseStopsServeCase asserts Close stops Serve without needing the context. The lock file at
// .qompack/run/daemon.lock is released on Close, so a Serve that outlived it would hold an endpoint
// no lock claims — the exact state §2.4 calls a stale lock and has the next client reclaim.
func runCloseStopsServeCase(t *testing.T, factory func(t *testing.T) ipc.Server) {
	t.Helper()
	s := factory(t)

	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), nopHandler) }()

	require.NoError(t, s.Close())
	requireServeReturns(t, done)
}

// requireServeReturns waits for a Serve goroutine to finish, bounded by suiteWait so a Serve that
// never returns fails as a named assertion rather than as a whole-package test timeout. §6.1 bans
// wall-clock sleeps; this is a bounded wait on a channel, which is the sanctioned alternative.
func requireServeReturns(t *testing.T, done <-chan error) {
	t.Helper()
	timer := time.NewTimer(suiteWait)
	defer timer.Stop()

	select {
	case err := <-done:
		requireKnownServeError(t, err)
	case <-timer.C:
		t.Fatalf("Serve did not return within %s", suiteWait)
	}
}

// runFireAndForgetFramingCase is the end-to-end framing round-trip: a request written by the client
// must reach the handler as the same request, field for field. §13 invariant 2's verbatim capture
// is only as good as the transport underneath it, so the Event is compared too — not just the
// envelope.
//
// The handler reports on a buffered channel rather than into a variable guarded by a wait: the
// client's Send does not return until the ACK arrives (§2.4), so by the time Send returns the
// handler has already run, and the receive below cannot block.
func runFireAndForgetFramingCase(t *testing.T, factory func(t *testing.T, h ipc.Handler) Transport) {
	t.Helper()

	seen := make(chan ipc.Request, 1)
	tr := factory(t, func(ctx context.Context, req ipc.Request) ipc.Response {
		seen <- req
		return ipc.Response{OK: true}
	})

	want := probeRequest()
	want.Raw = json.RawMessage(`{"tool":"Read","path":"src/auth.ts"}`)

	res, err := tr.Client.Send(context.Background(), want, suiteDeadline)
	require.NoError(t, err)
	require.True(t, res.OK, "an acknowledged request must report OK — this is the \\x06 ACK a caller observes; res.Err=%q", res.Err)

	got := receiveRequest(t, seen)
	require.Equal(t, want.Op, got.Op)
	require.Equal(t, want.Session, got.Session)
	require.Equal(t, want.TS, got.TS)
	require.False(t, got.Reply, "a fire-and-forget request must not arrive asking for a reply")
	require.JSONEq(t, string(want.Raw), string(got.Raw), "the framed payload must arrive byte-equivalent")
}

// runReplyRoundTripCase asserts §2.4's second framing mode: a request with Reply set receives a
// full NDJSON response line instead of the one-byte ACK, and everything the handler put in that
// response reaches the caller. This is the path UserPromptSubmit's additionalContext,
// MCP-over-daemon and status all take.
func runReplyRoundTripCase(t *testing.T, factory func(t *testing.T, h ipc.Handler) Transport) {
	t.Helper()

	const detail = `{"sessions":3}`
	tr := factory(t, func(ctx context.Context, req ipc.Request) ipc.Response {
		return ipc.Response{OK: true, Data: json.RawMessage(detail)}
	})

	req := probeRequest()
	req.Op = ipc.OpStatus
	req.Reply = true

	res, err := tr.Client.Send(context.Background(), req, suiteDeadline)
	require.NoError(t, err)
	require.True(t, res.OK, "the handler above answers OK unconditionally, so a false here is the transport, not the handler; res.Err=%q", res.Err)
	require.JSONEq(t, detail, string(res.Data), "a reply request must return the handler's data")
	requireHonourableResponse(t, res)
}

// runNakIsNotAnErrorCase asserts the NAK path: a request the daemon refuses comes back as
// OK: false — the \x15 a caller observes — and still not as a Go error, because §5.4's never-error
// rule covers refusal exactly as it covers unreachability. The refusal reason travels in Err, where
// a hook can log it and still exit 0.
func runNakIsNotAnErrorCase(t *testing.T, factory func(t *testing.T, h ipc.Handler) Transport) {
	t.Helper()

	const reason = "ipctest: refused by the handler"
	seen := make(chan ipc.Request, 1)
	tr := factory(t, func(ctx context.Context, req ipc.Request) ipc.Response {
		seen <- req
		return ipc.Response{OK: false, Err: reason}
	})

	res, err := tr.Client.Send(context.Background(), probeRequest(), suiteDeadline)
	require.NoError(t, err, "a refused request must not become an error a hook would propagate")
	require.False(t, res.OK, "a refused request must report OK: false — this is the \\x15 NAK a caller observes")

	// A refusal and a daemon that was never reached produce the SAME Response — OK: false with an
	// empty Err — because §5.4's never-error rule routes both through the client's spool-and-return.
	// Asserting only OK: false therefore passes just as happily when nothing ever crossed the wire,
	// which is how a real transport failure sat behind a green run of this very case (§2.0b: the
	// two cases asserting require.False(res.OK) passed while the two asserting require.True failed).
	// The handler having actually run is the independent observation that tells the two apart, and
	// it is the same evidence runFireAndForgetFramingCase already relies on.
	got := receiveRequest(t, seen)
	require.Equal(t, probeRequest().Op, got.Op, "the refused request must have reached the handler as itself")
	requireHonourableResponse(t, res)
}

// runOversizeFrameCase is the 1 MiB line limit end to end: a request too large to frame must never
// reach the handler, and must still not produce an error the hook would propagate. Both halves
// matter — accepting it would let one runaway tool result stall the daemon's read loop, and
// erroring would fail a hook over a payload Qompack chose not to record.
func runOversizeFrameCase(t *testing.T, factory func(t *testing.T, h ipc.Handler) Transport) {
	t.Helper()

	seen := make(chan ipc.Request, 1)
	tr := factory(t, func(ctx context.Context, req ipc.Request) ipc.Response {
		seen <- req
		return ipc.Response{OK: true}
	})

	// Both halves below — "not reported as delivered" and "never reached the handler" — are also
	// what a client that never connected produces, because §5.4's never-error rule gives an
	// unreachable daemon the same OK: false with an empty Err that a size refusal gets. So the
	// case first establishes that this transport is live, with an ordinary request that must be
	// delivered AND must arrive at the handler. Without it the two assertions below are satisfied
	// by a transport that was down the whole time, which is exactly how a real Windows transport
	// failure sat behind a green run of this case (§2.0b).
	//
	// The liveness probe goes FIRST, not after: a NAK is the daemon's in-band hint that it has
	// degraded to spool submode, and §12.2 has the client honour that for the rest of its life —
	// so a hot-path request sent AFTER the oversize refusal is spooled without ever dialling, and
	// would report a transport failure that is really the client obeying the NAK it was just sent.
	live := requestAt(probeTS + 1)
	res, err := tr.Client.Send(context.Background(), live, suiteDeadline)
	require.NoError(t, err, "an ordinary request must not become an error a hook would propagate")
	require.True(t, res.OK,
		"the transport must deliver an ordinary request — a false here means the oversize assertions below would be vacuous; res.Err=%q", res.Err)
	require.Equal(t, live.TS, receiveRequest(t, seen).TS, "the ordinary request must have reached the handler")

	res, err = tr.Client.Send(context.Background(), oversizeRequest(), suiteDeadline)
	require.NoError(t, err, "an oversize request must not become an error a hook would propagate")
	require.False(t, res.OK, "an oversize request must not be reported as delivered")

	select {
	case got := <-seen:
		t.Fatalf("a request whose framed line exceeds MaxLineBytes reached the handler: op=%s", got.Op)
	default:
	}
}

// receiveRequest takes the request the handler reported, bounded by suiteWait so a handler that was
// never called fails as a named assertion rather than as a package-wide timeout.
func receiveRequest(t *testing.T, seen <-chan ipc.Request) ipc.Request {
	t.Helper()
	timer := time.NewTimer(suiteWait)
	defer timer.Stop()

	select {
	case req := <-seen:
		return req
	case <-timer.C:
		t.Fatalf("the handler was not called within %s", suiteWait)
		return ipc.Request{}
	}
}
