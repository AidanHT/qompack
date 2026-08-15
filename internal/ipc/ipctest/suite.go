// Package ipctest is the conformance suite for the §5.4 transport seams — ipc.Client,
// ipc.SpoolWriter and ipc.Server — plus the paired Transport suite that grades the wire format
// itself (00-ARCHITECTURE.md §5.22). Every implementation SP-05 ships must pass all four. SP-01
// ships the suite, including the behaviour assertions SP-05 inherits (Rule W-1); each guarded
// /behaviour block is skipped until a real implementation lands.
//
// # Why there are four suites for three interfaces
//
// §15's table requires ipctest to assert "framing round-trip; 1 MiB line limit; ACK byte \x06, NAK
// \x15". None of those is observable through a Client alone or a Server alone: a round-trip needs
// both ends, and the ACK/NAK bytes are the transport's internal encoding of Response.OK rather than
// anything an interface returns. RunTransportSuite therefore takes a factory that returns a Client
// already talking to a Server running the suite's own Handler, which is the smallest pairing that
// can assert the wire format at all. Its factory signature carries that Handler, which is the one
// place this package departs from the plain factory func(t) T template — and it departs for a
// reason the template cannot express.
//
// The two raw control bytes are pinned by internal/ipc's own wire_test.go, because they are
// constants rather than behaviour; here they are asserted in the form a caller actually observes,
// as Response.OK.
//
// A <pkg>test package may import only its own base package, testutil and core
// (00-ARCHITECTURE.md §3.2), so this suite constructs Requests by hand and reads spool files with
// os: it cannot import config, logging or obs to build a client's dependencies. Every factory is
// therefore responsible for wiring those itself.
package ipctest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// probeSession and probeTS are the Session and TS every shape-block and probe Request carries.
const (
	probeSession = core.SessionID("ipctest-shape-probe")
	probeTS      = core.UnixMilli(1767225600000)
)

// probeRequest is the Request the shape blocks and the stub probes send. It is a fire-and-forget
// PostToolUse observation: the most common line on the wire, and the cheapest thing to ask any
// implementation to accept.
func probeRequest() ipc.Request {
	return ipc.Request{Op: ipc.OpObserveTool, Session: probeSession, TS: probeTS}
}

// suiteDeadline is the deadline every Send in this suite passes, and suiteWait is the longest the
// suite will wait for a Server goroutine to return.
//
// Neither is read from config, and neither is a production timeout in disguise. A <pkg>test package
// may not import config at all (§3.2's allow-set), and more importantly these are UPPER BOUNDS on a
// test's patience rather than the deadline a hot-path client should use: driving them from
// runtime.daemon.ackDeadlineMs would make the suite fail on a loaded CI box for reasons that have
// nothing to do with the implementation under test. The production deadline is the caller's, and it
// is passed to Send as a parameter precisely so it can differ here.
const (
	suiteDeadline = 5 * time.Second
	suiteWait     = 10 * time.Second
)

// Transport is what a framing round-trip needs and no single §5.4 interface provides: a Client
// already talking to a Server that is running the Handler the suite supplied. The Addr is carried
// for diagnostics only — the suite never dials it itself.
type Transport struct {
	Client ipc.Client
	Addr   ipc.Addr
}

// RunClientSuite is the conformance suite for ipc.Client. name distinguishes multiple factories run
// in the same test binary; factory must return a fresh, ready-to-use Client on every call.
func RunClientSuite(t *testing.T, name string, factory func(t *testing.T) ipc.Client) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		c := factory(t)
		require.NotNil(t, c)

		res, err := c.Send(context.Background(), probeRequest(), suiteDeadline)
		requireKnownError(t, err)
		requireHonourableResponse(t, res)

		requireKnownError(t, c.Close())
	})

	if skipIfStubClient(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("send_never_propagates_an_error", func(t *testing.T) { runSendNeverErrorsCase(t, factory) })
		t.Run("send_always_reports_a_mode_a_client_can_honour", func(t *testing.T) {
			runSendReportsHonourableModesCase(t, factory)
		})
		t.Run("a_cancelled_context_still_does_not_propagate_an_error", func(t *testing.T) {
			runSendCancelledContextCase(t, factory)
		})
	})
}

// RunSpoolWriterSuite is the conformance suite for ipc.SpoolWriter, the durability fallback every
// failure path in Client.Send lands on. factory must return a fresh SpoolWriter — with a fresh,
// empty backing file — on every call.
func RunSpoolWriterSuite(t *testing.T, name string, factory func(t *testing.T) ipc.SpoolWriter) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		s := factory(t)
		require.NotNil(t, s)

		requireKnownError(t, s.Append(probeRequest()))
		_ = s.Path() // no error return; any path, including the stub's empty one, is shape-valid
	})

	if skipIfStubSpool(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("append_writes_one_ndjson_line_per_request", func(t *testing.T) {
			runSpoolFramingRoundTripCase(t, factory)
		})
		t.Run("append_only_never_rewrites_an_earlier_line", func(t *testing.T) {
			runSpoolIsAppendOnlyCase(t, factory)
		})
		t.Run("no_line_exceeds_the_frame_limit", func(t *testing.T) {
			runSpoolLineLimitCase(t, factory)
		})
		t.Run("path_is_stable_and_non_empty", func(t *testing.T) { runSpoolPathIsStableCase(t, factory) })
	})
}

// RunServerSuite is the conformance suite for ipc.Server. factory must return a fresh Server that
// has not been served yet; the suite starts and stops it.
func RunServerSuite(t *testing.T, name string, factory func(t *testing.T) ipc.Server) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		s := factory(t)
		require.NotNil(t, s)

		_ = s.Addr() // no error return; a stub's zero Addr is shape-valid

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		requireKnownServeError(t, s.Serve(ctx, nopHandler))

		requireKnownError(t, s.Close())
	})

	if skipIfStubServer(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("addr_is_resolved_and_stable", func(t *testing.T) { runServerAddrStableCase(t, factory) })
		t.Run("serve_returns_when_the_context_is_cancelled", func(t *testing.T) {
			runServeReturnsOnCancelCase(t, factory)
		})
		t.Run("close_stops_serve", func(t *testing.T) { runCloseStopsServeCase(t, factory) })
	})
}

// RunTransportSuite is the conformance suite for the wire format itself: §2.4's NDJSON framing, its
// 1 MiB line limit, and the ACK/NAK handshake in the form a caller observes it (Response.OK).
//
// factory is handed the Handler the Server must run and must return a Client already able to reach
// it, registering whatever cleanup the pairing needs with t.Cleanup. Putting the start-up
// synchronisation in the factory rather than in the suite is deliberate: only the implementation
// knows when its listener is actually bound, and the alternative — the suite starting Serve in a
// goroutine and hoping — is exactly the kind of wall-clock race §6.1 bans sleeps to prevent.
func RunTransportSuite(t *testing.T, name string, factory func(t *testing.T, h ipc.Handler) Transport) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		tr := factory(t, nopHandler)
		require.NotNil(t, tr.Client)

		res, err := tr.Client.Send(context.Background(), probeRequest(), suiteDeadline)
		requireKnownError(t, err)
		requireHonourableResponse(t, res)
	})

	if skipIfStubTransport(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("fire_and_forget_request_round_trips_the_frame", func(t *testing.T) {
			runFireAndForgetFramingCase(t, factory)
		})
		t.Run("reply_request_returns_the_handlers_response", func(t *testing.T) {
			runReplyRoundTripCase(t, factory)
		})
		t.Run("a_refused_request_is_not_ok_but_is_still_not_an_error", func(t *testing.T) {
			runNakIsNotAnErrorCase(t, factory)
		})
		t.Run("an_oversize_request_never_reaches_the_handler", func(t *testing.T) {
			runOversizeFrameCase(t, factory)
		})
	})
}

// nopHandler is the Handler the shape blocks pass: it accepts everything and reports the healthy
// defaults, so a shape assertion never fails because of what the handler decided.
func nopHandler(ctx context.Context, req ipc.Request) ipc.Response {
	return ipc.Response{OK: true}
}

// requireKnownError fails the test unless err is nil or wraps one of the four sentinels every stub
// and every real implementation is allowed to return from an operation (00-ARCHITECTURE.md §5.22;
// §15 of plans/V1-SP-01-foundation-toolchain-and-contracts.md).
func requireKnownError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	known := errors.Is(err, core.ErrNotImplemented) ||
		errors.Is(err, core.ErrNotFound) ||
		errors.Is(err, core.ErrBudget) ||
		errors.Is(err, core.ErrDegraded)
	require.True(t, known, "unexpected error: %v", err)
}

// requireKnownServeError is requireKnownError for Serve, which additionally may report the
// context's own cancellation: a Server handed an already-cancelled context has done nothing wrong
// by saying so.
func requireKnownServeError(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	requireKnownError(t, err)
}

// requireHonourableResponse fails the test unless res carries a contract mode and a hot-path mode a
// client can actually act on. Both are on every response, including failures, because §12.1 and
// §12.2 require a client to honour them immediately — a mode the client cannot interpret is a
// degradation it will ignore.
//
// The contract mode is checked through Mode.String rather than by comparing against the three
// constants because a <pkg>test package may not import contract (§3.2's allow-set); "unknown" is
// precisely the spelling contract.Mode.String reserves for a value nothing downstream can render.
func requireHonourableResponse(t *testing.T, res ipc.Response) {
	t.Helper()
	require.NotEqual(t, "unknown", res.Mode.String(),
		"Response.Mode(%d) is not a mode a client can honour", uint8(res.Mode))
	requireKnownHotPathMode(t, res.Hot)
}

// requireKnownHotPathMode fails the test unless h is one of the two submodes §12.2 declares.
func requireKnownHotPathMode(t *testing.T, h ipc.HotPathMode) {
	t.Helper()
	switch h {
	case ipc.HotSync, ipc.HotSpool:
	default:
		require.Failf(t, "unknown hot-path mode", "HotPathMode(%d)", uint8(h))
	}
}

// isStubClient reports whether factory currently produces a stub Client, using Send as the probe
// (plans/OWNERS.tsv: ipc's probe method is Send).
//
// A stub Send reports core.ErrNotImplemented, which deliberately contradicts §5.4's "never an error
// a hook would propagate" — that contradiction is what makes the probe possible at all, and the
// never-error contract is graded in the behaviour block below.
func isStubClient(t *testing.T, factory func(t *testing.T) ipc.Client) bool {
	t.Helper()
	_, err := factory(t).Send(context.Background(), probeRequest(), suiteDeadline)
	return core.IsNotImplemented(err)
}

// skipIfStubClient calls t.Skip with the exact Rule W-1 message when factory still produces a stub
// Client, and reports whether it did.
func skipIfStubClient(t *testing.T, factory func(t *testing.T) ipc.Client) bool {
	t.Helper()
	if isStubClient(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}

// isStubSpool reports whether factory currently produces a stub SpoolWriter, using Append as the
// probe: it is the SpoolWriter's only write operation, and the one every failure path in Send
// depends on.
func isStubSpool(t *testing.T, factory func(t *testing.T) ipc.SpoolWriter) bool {
	t.Helper()
	return core.IsNotImplemented(factory(t).Append(probeRequest()))
}

// skipIfStubSpool calls t.Skip with the exact Rule W-1 message when factory still produces a stub
// SpoolWriter, and reports whether it did.
func skipIfStubSpool(t *testing.T, factory func(t *testing.T) ipc.SpoolWriter) bool {
	t.Helper()
	if isStubSpool(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}

// isStubServer reports whether factory currently produces a stub Server, using Serve against an
// already-cancelled context as the probe: a real Server returns promptly and reports either nil or
// the context's cancellation, while a stub reports core.ErrNotImplemented.
func isStubServer(t *testing.T, factory func(t *testing.T) ipc.Server) bool {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return core.IsNotImplemented(factory(t).Serve(ctx, nopHandler))
}

// skipIfStubServer calls t.Skip with the exact Rule W-1 message when factory still produces a stub
// Server, and reports whether it did.
func skipIfStubServer(t *testing.T, factory func(t *testing.T) ipc.Server) bool {
	t.Helper()
	if isStubServer(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}

// isStubTransport reports whether factory currently produces a stubbed pairing, probed through its
// Client exactly as isStubClient does.
func isStubTransport(t *testing.T, factory func(t *testing.T, h ipc.Handler) Transport) bool {
	t.Helper()
	tr := factory(t, nopHandler)
	_, err := tr.Client.Send(context.Background(), probeRequest(), suiteDeadline)
	return core.IsNotImplemented(err)
}

// skipIfStubTransport calls t.Skip with the exact Rule W-1 message when factory still produces a
// stubbed pairing, and reports whether it did.
func skipIfStubTransport(t *testing.T, factory func(t *testing.T, h ipc.Handler) Transport) bool {
	t.Helper()
	if isStubTransport(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}
