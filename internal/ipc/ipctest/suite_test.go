package ipctest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/ipc/ipctest"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/stretchr/testify/require"
)

// fakeStubClient mirrors the shape of an SP-01-style stub Client: every operation reports
// core.ErrNotImplemented, exactly like ipc.NewClient's own stub does today. It exists only to
// exercise RunClientSuite before SP-05 ships a real transport.
type fakeStubClient struct{}

func (fakeStubClient) Send(ctx context.Context, req ipc.Request, deadline time.Duration) (ipc.Response, error) {
	return ipc.Response{}, core.ErrNotImplemented
}
func (fakeStubClient) Close() error { return core.ErrNotImplemented }

// fakeStubSpool is fakeStubClient's SpoolWriter counterpart.
type fakeStubSpool struct{}

func (fakeStubSpool) Append(req ipc.Request) error { return core.ErrNotImplemented }
func (fakeStubSpool) Path() string                 { return "" }

// fakeStubServer is the stub Server ipc does not ship: §5.4 gives Server no constructor, so there
// is nothing in the package for RunServerSuite to point at until SP-05 builds one.
type fakeStubServer struct{}

func (fakeStubServer) Serve(ctx context.Context, h ipc.Handler) error { return core.ErrNotImplemented }
func (fakeStubServer) Addr() ipc.Addr                                 { return ipc.Addr{} }
func (fakeStubServer) Close() error                                   { return core.ErrNotImplemented }

// workingSpool is a minimal, correct SpoolWriter: one compact JSON line per request, newline
// terminated, appended, with §2.4's 1 MiB frame limit enforced before anything is written.
//
// It exists so RunSpoolWriterSuite's behaviour block is demonstrably satisfiable rather than an
// executable specification nobody has ever run — an unrunnable spec is worth very little to SP-05,
// who inherits it. It is NOT the real spool: SP-05's writes to
// .qompack/spool/client-<pid>.ndjson, coordinates with the daemon's drain, and is the durability
// boundary §2.4 describes. This one only has to be right about the framing.
type workingSpool struct{ path string }

func (s *workingSpool) Append(req ipc.Request) error {
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if len(b)+1 > ipc.MaxLineBytes {
		return fmt.Errorf("%w: spool line of %d bytes exceeds the %d byte frame limit",
			core.ErrBudget, len(b)+1, ipc.MaxLineBytes)
	}

	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

func (s *workingSpool) Path() string { return s.path }

// newWorkingSpool returns a workingSpool over a fresh temp file. Every test in this package writes
// only under t.TempDir().
func newWorkingSpool(t *testing.T) ipc.SpoolWriter {
	t.Helper()
	return &workingSpool{path: filepath.Join(t.TempDir(), "client-0.ndjson")}
}

// clock is the core.Clock obs.New needs; a fixed instant keeps metric timestamps deterministic and
// keeps §6.1's ban on wall-clock dependence honest.
type clock struct{}

func (clock) Now() time.Time                  { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
func (clock) Since(t time.Time) time.Duration { return clock{}.Now().Sub(t) }

// newQompackStubClient builds the real Client SP-05 ships, wired the way a hook would wire it —
// against a temp project root with no daemon listening, so its behaviour block exercises the
// spool-and-return failure path end to end. The name is unchanged from SP-01's placeholder so
// this file's history stays legible in blame; what it builds is no longer a stub.
func newQompackStubClient(t *testing.T) ipc.Client {
	t.Helper()
	addr, err := ipc.Resolve(t.TempDir())
	require.NoError(t, err)
	spool, err := ipc.NewSpool(t.TempDir())
	require.NoError(t, err)
	c := ipc.NewClient(addr, spool, logging.Nop(), obs.New(clock{}))
	// Close releases the spool's backing file handle: without this, TempDir's own cleanup cannot
	// remove a file this process still has open (fails outright on Windows).
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// newQompackStubSpool builds the real SpoolWriter SP-05 ships.
func newQompackStubSpool(t *testing.T) ipc.SpoolWriter {
	t.Helper()
	s, err := ipc.NewSpool(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() {
		if cl, ok := s.(interface{ Close() error }); ok {
			_ = cl.Close()
		}
	})
	return s
}

// newQompackServer builds the real Server SP-05 ships, bound but not yet serving.
func newQompackServer(t *testing.T) ipc.Server {
	t.Helper()
	addr, err := ipc.Resolve(t.TempDir())
	require.NoError(t, err)
	srv, err := ipc.NewServer(addr, logging.Nop(), obs.New(clock{}), ipc.MaxLineBytes)
	require.NoError(t, err)
	return srv
}

// transportTestPatience is the connect and ACK budget the Client this factory hands back is built
// with. It is the suite's own patience, deliberately NOT the production hot-path budget, and it
// mirrors what RunTransportSuite's own doc comment says about suiteDeadline: "these are UPPER
// BOUNDS on a test's patience rather than the deadline a hot-path client should use ... The
// production deadline is the caller's, and it is passed to Send as a parameter precisely so it can
// differ here."
//
// It has to be set explicitly, because the parameter the suite passes to Send only bounds a Reply
// request's response line (client.go's awaitReply). The connect and the one-byte ACK are bounded by
// the Client's OWN ConnectDeadline/AckDeadline, and ipc.NewClient inherits those from
// config.Defaults() — runtime.daemon.ackDeadlineMs = 8, and a connectDeadlineMs sized for the hot
// path (5 ms, or 25 ms on Windows). Building this factory's client with ipc.NewClient therefore
// graded the wire format against a production connect budget, which is exactly the coupling that
// comment exists to forbid, and it is why this suite failed on windows-latest with res.OK false and
// res.Err empty: that pair is client.spoolAndReturn's signature, i.e. "never reached the server",
// not "the server refused".
//
// The 5 ms figure this suite originally inherited was not merely tight on Windows, it was unusable
// there: go-winio's dial retries the ERROR_PIPE_BUSY that a listener with no free pipe instance
// returns on a hard-coded time.Sleep(10 * time.Millisecond) (pipe.go's tryDialPipe), so any budget
// under 10 ms buys exactly one CreateFile attempt and no retry at all. config now ships 25 ms there
// — 2.5 of those quanta, so the retry actually happens (internal/config/deadlines.go) — but that
// makes the coupling merely survivable, not correct: a wire-format suite still must not be graded
// against whatever the production hot path happens to budget.
//
// The production budget itself is unchanged by this suite and still graded where it belongs:
// config's own defaults test pins both platforms' connect deadline and the 8 ms ACK,
// internal/ipc's TestConnectDeadlineDefaultClearsTheBusyRetryQuantum keeps the connect deadline
// above the dial's own retry quantum, and the B-A/B-B latency budgets grade the hot path.
const transportTestPatience = 5 * time.Second

// transportWarmUpSession and transportWarmUpTS identify the readiness handshake's own probe below.
// The Session exists so the wrapped handler can recognise the factory's own request and answer it
// itself; no suite case ever sends this Session, so h never sees a request the suite did not ask
// for. The TS matches the suite's own probeTS, which this external test package cannot name.
const (
	transportWarmUpSession = core.SessionID("ipctest-transport-warmup")
	transportWarmUpTS      = core.UnixMilli(1767225600000)
)

// newQompackTransport pairs the real Server (running h) with the real Client SP-05 ships. Serve
// is started here, and cleanup order matters: the client's own Close (spool handle) first, then
// cancelling ctx to stop Serve, then Close as a backstop in case cancellation alone left it
// running (Close is idempotent, per server.go's own sync.Once).
//
// # Why this factory performs a readiness handshake before returning
//
// RunTransportSuite's contract puts start-up synchronisation here on purpose — "the alternative —
// the suite starting Serve in a goroutine and hoping — is exactly the kind of wall-clock race §6.1
// bans sleeps to prevent". This factory used to be that alternative: it started Serve in a
// goroutine and handed the Client straight back.
//
// A bound Server is not a reachable one. On Windows winio.ListenPipe creates only its firstHandle,
// which is deliberately un-connectable — go-winio's own words: "By not asking for read or write
// access, the named pipe file system will put this pipe into an initially disconnected state,
// blocking client connections until the next call with first == false" (pipe.go's
// makeServerPipeHandle). The connectable instance is created by makeServerPipe inside
// listenerRoutine, and listenerRoutine only creates one when Accept asks for it. So between
// ipc.NewServer returning and the accept loop reaching its first Accept, the endpoint refuses every
// dial — measured here as a 300 ms dial failing outright against a bound, never-Accepting listener.
// The suite then graded a Client that had never connected, and the two cases that assert
// require.False(res.OK) passed while it did.
//
// The handshake below is a bounded wait on an observable condition, not a sleep: one probe request
// the wrapped handler answers itself, which cannot return OK until the accept loop has accepted it.
func newQompackTransport(t *testing.T, h ipc.Handler) ipctest.Transport {
	t.Helper()
	addr, err := ipc.Resolve(t.TempDir())
	require.NoError(t, err)
	srv, err := ipc.NewServer(addr, logging.Nop(), obs.New(clock{}), ipc.MaxLineBytes)
	require.NoError(t, err)

	// gate answers the readiness probe itself and forwards everything else verbatim, so the
	// handler the suite supplied observes exactly the requests the suite sent and no others.
	gate := func(ctx context.Context, req ipc.Request) ipc.Response {
		if req.Session == transportWarmUpSession {
			return ipc.Response{OK: true}
		}
		return h(ctx, req)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, gate) }()

	spool, err := ipc.NewSpool(t.TempDir())
	require.NoError(t, err)
	c := ipc.NewClientWithOptions(addr, spool, logging.Nop(), obs.New(clock{}), ipc.ClientOptions{
		ConnectDeadline: transportTestPatience,
		AckDeadline:     transportTestPatience,
	})

	t.Cleanup(func() {
		_ = c.Close()
		cancel()
		_ = srv.Close()
		<-done
	})

	warm, err := c.Send(context.Background(), ipc.Request{
		Op:      ipc.OpObserveTool,
		Session: transportWarmUpSession,
		TS:      transportWarmUpTS,
	}, transportTestPatience)
	require.NoError(t, err)
	require.True(t, warm.OK,
		"the Server was still not accepting %s after Serve started, so the suite would have graded a Client that never connected; warm.Err=%q",
		transportTestPatience, warm.Err)

	return ipctest.Transport{Client: c, Addr: addr}
}

// TestIPCSuite_ShapePassesAgainstStub is the mandatory per-suite-package assertion: every suite's
// shape block passes against a deliberately stubbed implementation, and every behaviour block is
// skipped with the exact Rule W-1 message.
//
// Each suite runs inside its own subtest because Rule W-1's skip is a t.Skip on the calling test:
// invoking all four directly would let the first one's skip abort the rest before their shape
// blocks ever ran.
func TestIPCSuite_ShapePassesAgainstStub(t *testing.T) {
	t.Run("client", func(t *testing.T) {
		ipctest.RunClientSuite(t, "fake-stub-client", func(t *testing.T) ipc.Client {
			return fakeStubClient{}
		})
	})
	t.Run("spool", func(t *testing.T) {
		ipctest.RunSpoolWriterSuite(t, "fake-stub-spool", func(t *testing.T) ipc.SpoolWriter {
			return fakeStubSpool{}
		})
	})
	t.Run("server", func(t *testing.T) {
		ipctest.RunServerSuite(t, "fake-stub-server", func(t *testing.T) ipc.Server {
			return fakeStubServer{}
		})
	})
	t.Run("transport", func(t *testing.T) {
		ipctest.RunTransportSuite(t, "fake-stub-transport", func(t *testing.T, h ipc.Handler) ipctest.Transport {
			return ipctest.Transport{Client: fakeStubClient{}}
		})
	})
}

// TestRunClientSuite_AgainstQompackStub exercises RunClientSuite against the real ipc.NewClient
// (SP-05 replaced the SP-01 stub this test originally targeted; the name is kept so history stays
// legible), so a change to its behaviour that breaks the conformance suite is caught here. Because
// ipc.NewClient is no longer a stub, this run exercises the full behaviour block too — Rule W-1's
// skip no longer applies.
func TestRunClientSuite_AgainstQompackStub(t *testing.T) {
	ipctest.RunClientSuite(t, "ipc.NewClient", newQompackStubClient)
}

// TestRunSpoolWriterSuite_AgainstQompackStub is TestRunClientSuite_AgainstQompackStub's SpoolWriter
// sibling, against the real ipc.NewSpool.
func TestRunSpoolWriterSuite_AgainstQompackStub(t *testing.T) {
	ipctest.RunSpoolWriterSuite(t, "ipc.NewSpool", newQompackStubSpool)
}

// TestRunServerSuite_AgainstQompackServer exercises RunServerSuite against the real ipc.NewServer
// — the fourth and last of the four factories this package's conformance suites require, wired to
// a real implementation (see also the Client/SpoolWriter pair above and the Transport pairing
// below).
func TestRunServerSuite_AgainstQompackServer(t *testing.T) {
	ipctest.RunServerSuite(t, "ipc.NewServer", newQompackServer)
}

// TestRunTransportSuite_AgainstQompackServer exercises RunTransportSuite against a real
// ipc.NewServer paired with a real ipc.NewClient — the framing round-trip, the 1 MiB line limit,
// and the ACK/NAK handshake, end to end over the real wire.
func TestRunTransportSuite_AgainstQompackServer(t *testing.T) {
	ipctest.RunTransportSuite(t, "ipc.NewServer+NewClient", newQompackTransport)
}

// TestRunSpoolWriterSuite_AgainstAWorkingSpool runs the SpoolWriter behaviour block against a
// minimal correct implementation, proving the framing and 1 MiB-limit specification SP-05 inherits
// is satisfiable rather than merely unrun.
func TestRunSpoolWriterSuite_AgainstAWorkingSpool(t *testing.T) {
	ipctest.RunSpoolWriterSuite(t, "working-spool", newWorkingSpool)
}

// TestRunServerSuite_StubIsSkipped proves the Server suite's shape block passes against a stub and
// that its behaviour block is skipped with the exact Rule W-1 message. ipc ships no Server at all
// — §5.4 gives it no constructor — so this fake is the only Server in the tree until SP-05 lands.
func TestRunServerSuite_StubIsSkipped(t *testing.T) {
	ipctest.RunServerSuite(t, "stub-server", func(t *testing.T) ipc.Server {
		return fakeStubServer{}
	})
}

// TestRunTransportSuite_StubIsSkipped is the Server suite's paired sibling: the framing round-trip
// needs both ends, and neither is real yet.
func TestRunTransportSuite_StubIsSkipped(t *testing.T) {
	ipctest.RunTransportSuite(t, "stub-transport", func(t *testing.T, h ipc.Handler) ipctest.Transport {
		addr, err := ipc.Resolve(t.TempDir())
		require.NoError(t, err)
		return ipctest.Transport{Client: fakeStubClient{}, Addr: addr}
	})
}
