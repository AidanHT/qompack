package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
)

// The wall-clock bounds this package waits under, and the raw ipc.Client budgets it constructs
// directly (a test-only admin/status client, distinct from the real hook subcommands under test)
// to poke a live daemon for facts a hook's own stdout cannot report — its live session count,
// whether it is reachable at all.
//
// Every bound below names the mechanism it is waiting on and derives its value from that mechanism
// rather than from a round number (V2-MERGE-25 ②). The rule the block exists to keep is: no bound
// may be smaller than the thing it waits for. A bound that is smaller cannot tell a broken
// mechanism from a slow one — which is exactly how e2eSpoolDrainBound (10s) came to be racing a
// 30s idle-tick fallback it could never win, and failing as an unexplained timeout when it lost.
//
// The *Tick constants are poll cadences, not bounds. Each samples its bound on the order of a
// hundred times: tight enough that a passing run finishes promptly, loose enough not to spend the
// test's own CPU re-dialling.
const (
	// e2eProbeTimeout is the dial budget of a bare liveness probe — ipc.Probe writes nothing and
	// reads nothing, so this bounds the connect alone. Basis: internal/cli's
	// hookConnectDeadlineFloor, the 250ms dial budget a real reply-op hook gives its FIRST
	// post-spawn connect and the smallest budget in this tree documented as sufficient for a cold
	// endpoint. config.Defaults().Runtime.Daemon.ConnectDeadlineMs (5ms) is deliberately not the
	// basis: it is tuned for an already-warm daemon on the hot path, and a probe using it would
	// report "no daemon" for a daemon that is merely busy.
	e2eProbeTimeout = 250 * time.Millisecond

	// e2eRoundTripDeadline is the Send deadline for this file's admin/status round trips against a
	// daemon already known to be reachable. Basis: internal/cli's own reply budgets for real hooks
	// span promptReplyDeadline (250ms) to flushReplyDeadline (15s); this sits between them, three
	// orders of magnitude above the sub-millisecond cost of a local round trip.
	e2eRoundTripDeadline = 5 * time.Second

	// e2eDaemonUpBound waits for a lazily spawned daemon to become reachable. Basis:
	// daemon.SpawnPollBound — the window EnsureRunning itself polls before concluding that a spawn
	// never came up. The multiplier is headroom for OS process creation on a loaded host, the one
	// part of a cold start that no in-process bound models; it is not padding over a hang, because
	// past daemon.SpawnPollBound the spawning side has already given up, so nothing waited for
	// here is still legitimately in progress.
	e2eDaemonUpBound = 8 * daemon.SpawnPollBound
	e2eDaemonUpTick  = 20 * time.Millisecond

	// e2eDaemonDownBound waits for a daemon to take admin.shutdown and go away. Basis:
	// daemon.StopDrainBound, the longest single step of Stop's cleanup sequence; the server close
	// that follows is separately capped inside internal/ipc by its own serverCloseWait. Three
	// times the drain bound covers both with headroom.
	e2eDaemonDownBound = 3 * daemon.StopDrainBound
	e2eDaemonDownTick  = 100 * time.Millisecond

	// e2eHistoryConvergeBound bounds waiting for a history.json mutation from a session-start that
	// is not the FIRST one in a project, or for a daemon's own idle-exit to complete. Basis: those
	// are the only two things it waits for, so it is a full daemon start-up plus a full daemon
	// shutdown — e2eDaemonUpBound + e2eDaemonDownBound — and nothing else.
	//
	// What actually made this flaky earlier in fix round 1 was a real bug, not host load:
	// hookConnectDeadlineFloor's own doc comment (internal/cli/hookclient.go) explains it in full —
	// Important I-1's removal of NewClientWithOptions's double state.bin read (correctly required;
	// see that fix's own comment) also removed an incidental few-ms buffer that had been quietly
	// carrying a Reply op's very first post-daemon-start dial across a too-tight
	// State.ConnectDeadlineMs (5ms, sized for the hot path's already-warm cadence). With that dial
	// budget now widened for session-start/checkpoint/flush specifically, this bound is headroom
	// for real sibling-test load, not compensation for a race.
	e2eHistoryConvergeBound = e2eDaemonUpBound + e2eDaemonDownBound

	// e2eSpoolDrainBound waits for the daemon to drain a client spool file. Its basis is the
	// mechanism the tests actually exercise, not the one they used to fall back on: the daemon
	// re-drains the spool the first time it serves a request (internal/daemon/daemon.go,
	// redrainOnceServing), so by the time the hook call that kicked it has exited, the drain is
	// already running and all that is left to wait for is one pass over a handful of lines — each
	// dispatched under daemon.DrainLineDeadline, which is therefore the unit this is built from.
	//
	// It is deliberately NOT sized from daemon.IdleTickMax (30s). That idle tick is the backstop; a
	// bound large enough to pass via it would leave these tests unable to tell the prompt path from
	// the slow one, which is the defect V2-MERGE-25 recorded in the other direction. Each use below
	// names the fallback in its failure message instead, so a timeout here reads as "the re-drain
	// did not fire", never as "the machine was slow".
	e2eSpoolDrainBound = 2 * daemon.DrainLineDeadline
	e2eSpoolDrainTick  = 100 * time.Millisecond

	// e2eWALVisibleBound waits for WAL lines whose writes have ALREADY happened: ingest.Accept
	// appends to the session WAL before the ACK that lets the hook process exit, so once every hook
	// call has returned the bytes are on disk and this bounds only another process's writes
	// becoming visible to this one. Basis: one daemon.DrainLineDeadline, the smallest bound in the
	// daemon that is unambiguously longer than a local filesystem round trip.
	e2eWALVisibleBound = daemon.DrainLineDeadline

	idleExitSecondsEnvKey = "QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS"
)

// e2eSession is the fixed session id every daemon_e2e_test.go case uses.
const e2eSession = core.SessionID("sess-e2e-daemon")

// e2eEnv is the base environment every real-binary invocation in this file runs with.
func e2eEnv(p *testutil.Project) map[string]string {
	return map[string]string{
		"QOMPACK_PROJECT_ROOT": p.Root,
		"HOME":                 p.Home(),
		"USERPROFILE":          p.Home(),
	}
}

// e2eStatus dials the daemon at root's resolved address directly (bypassing the hook subcommand
// surface entirely) and returns its decoded daemon.StatusSnapshot. It is this file's only way to
// observe daemon-internal facts — live session count, hot-path submode — that no hook's own stdout
// carries.
func e2eStatus(t *testing.T, root string) daemon.StatusSnapshot {
	t.Helper()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)

	sp, _ := ipc.NewSpool(paths.Of(root).Spool)
	c := ipc.NewClientWithOptions(addr, sp, nil, nil, ipc.ClientOptions{
		ProjectRoot:     root,
		ConnectDeadline: e2eRoundTripDeadline,
		AckDeadline:     e2eRoundTripDeadline,
	})
	defer func() { _ = c.Close() }()

	resp, err := c.Send(context.Background(), ipc.Request{
		Op: ipc.OpStatus, Session: e2eSession, TS: core.NowMilli(core.SystemClock()), Reply: true,
	}, e2eRoundTripDeadline)
	require.NoError(t, err)
	require.True(t, resp.OK, "status round trip must succeed against a reachable daemon; resp.Err=%q", resp.Err)

	var snap daemon.StatusSnapshot
	require.NoError(t, json.Unmarshal(resp.Data, &snap))
	return snap
}

// e2eIngestHistName is obs.Budgets()'s own name for the B-B (L0 ingest) histogram, looked up
// rather than spelled as a literal — exactly as internal/daemon's histName does it, so the budget
// table stays the single source of truth for which series ingest.Accept feeds.
func e2eIngestHistName(t *testing.T) string {
	t.Helper()
	for _, b := range obs.Budgets() {
		if b.ID == obs.BB {
			return b.Hist
		}
	}
	require.FailNow(t, "obs.Budgets() no longer declares a B-B (L0 ingest) budget")
	return ""
}

// e2eLiveIngestSamples reports how many hot-path requests the daemon at root has ACCEPTED over
// the transport: the sample count of the B-B histogram in its own status snapshot.
//
// It is this file's proof that a hook call REACHED a running daemon rather than spooling. Only
// ingest.Accept records into that histogram, and every one of Accept's callers is a live observe.*
// wire route: acceptHotPathEvent (internal/daemon/handlers.go:336, observe.tool and observe.stop)
// and handleObservePrompt (handlers.go:369). No replay path reaches it — drainDispatch hands a
// hot-path line straight to runIngested precisely so that a drained line is not re-WAL'd
// (internal/daemon/daemon.go). So a sample here can only have come from a request that arrived
// over the wire — and unlike the spool tier itself, nothing takes it back: no drain, no replay and
// no idle tick edits a histogram.
func e2eLiveIngestSamples(t *testing.T, root string) int64 {
	t.Helper()
	return e2eStatus(t, root).Latency[e2eIngestHistName(t)].N
}

// e2eLiveIngestSamplesOrUnknown is e2eLiveIngestSamples for use inside a require.Eventually
// condition, returning -1 instead of failing when the daemon cannot be reached or its reply cannot
// be decoded.
//
// It exists because testify runs an Eventually condition on its own goroutine, where require.*'s
// FailNow is invalid — t.FailNow must be called from the goroutine running the test. A condition
// that used the fatal reader would turn a transient status miss into a runtime complaint instead
// of another poll. -1 is never a real sample count, so a caller comparing against a positive
// threshold treats "could not ask" and "asked, and the answer was too low" identically: keep
// polling, and fail with the surrounding message if the bound expires.
func e2eLiveIngestSamplesOrUnknown(root, histName string) int64 {
	addr, err := ipc.Resolve(root)
	if err != nil {
		return -1
	}
	sp, _ := ipc.NewSpool(paths.Of(root).Spool)
	c := ipc.NewClientWithOptions(addr, sp, nil, nil, ipc.ClientOptions{
		ProjectRoot:     root,
		ConnectDeadline: e2eRoundTripDeadline,
		AckDeadline:     e2eRoundTripDeadline,
	})
	defer func() { _ = c.Close() }()

	resp, err := c.Send(context.Background(), ipc.Request{
		Op: ipc.OpStatus, Session: e2eSession, TS: core.NowMilli(core.SystemClock()), Reply: true,
	}, e2eRoundTripDeadline)
	if err != nil || !resp.OK {
		return -1
	}
	var snap daemon.StatusSnapshot
	if json.Unmarshal(resp.Data, &snap) != nil {
		return -1
	}
	return snap.Latency[histName].N
}

// e2eWaitDaemonUp polls until a daemon answers at root's resolved address.
func e2eWaitDaemonUp(t *testing.T, root string) {
	t.Helper()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return ipc.Probe(addr, e2eProbeTimeout) },
		e2eDaemonUpBound, e2eDaemonUpTick, "no daemon ever became reachable at %s", addr.Path)
}

// TestE2EHookRoundTrip is task-6-spec.md's e2e table row: session-start brings the daemon up, then
// 50 observe-tool calls all exit 0, land in the session's WAL, and the daemon's own status reports
// exactly one live session.
func TestE2EHookRoundTrip(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)

	stdout, stderr, code := Run(t, bin, []string{"session-start"}, sessionStartPayload(t, p.Root), env)
	require.Equal(t, 0, code, "stderr:\n%s", stderr)
	requireParsesAsOutput(t, stdout)

	e2eWaitDaemonUp(t, p.Root)

	const n = 50
	for i := 0; i < n; i++ {
		stdout, stderr, code := Run(t, bin, []string{"observe", "tool"}, observeToolPayload(t, p.Root), env)
		require.Equal(t, 0, code, "observe tool #%d: stderr:\n%s", i, stderr)
		requireParsesAsOutput(t, stdout)
	}

	walPath := filepath.Join(paths.Of(p.Root).Spool, "wal-"+string(e2eSession)+".ndjson")
	var lines int
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(walPath)
		if err != nil {
			return false
		}
		lines = countNonEmptyLines(string(b))
		return lines >= n
	}, e2eWALVisibleBound, e2eSpoolDrainTick, "wal-%s.ndjson never reached %d lines (last seen %d) at %s", e2eSession, n, lines, walPath)

	snap := e2eStatus(t, p.Root)
	live := 0
	for _, s := range snap.Sessions {
		if s.ID == e2eSession && s.Live {
			live++
		}
	}
	require.Equal(t, 1, live, "the daemon must report exactly one live session; sessions=%+v", snap.Sessions)
}

// TestE2ELazySpawn is task-6-spec.md's e2e table row: with no daemon running, one observe-tool call
// spools and exits 0; within the bound below a daemon comes up on its own (lazySpawn); a second
// observe-tool call ACKs it and the spool the first call left behind drains.
//
// "Drains" and not "drains into the WAL": a replayed hot-path line goes straight to runIngested
// (internal/daemon/daemon.go, drainDispatch) and is deliberately never re-WAL'd, so the WAL is
// where a LIVE call's bytes land, never a drained one's.
func TestE2ELazySpawn(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)

	addr, err := ipc.Resolve(p.Root)
	require.NoError(t, err)
	require.False(t, ipc.Probe(addr, e2eProbeTimeout), "no daemon must be running yet")

	stdout, stderr, code := Run(t, bin, []string{"observe", "tool"}, observeToolPayload(t, p.Root), env)
	require.Equal(t, 0, code, "stderr:\n%s", stderr)
	requireParsesAsOutput(t, stdout)

	spoolFiles, err := ipc.SpoolFiles(paths.Of(p.Root).Spool)
	require.NoError(t, err)
	require.NotEmpty(t, spoolFiles, "the first call, with no daemon reachable, must have spooled")

	e2eWaitDaemonUp(t, p.Root)

	stdout, stderr, code = Run(t, bin, []string{"observe", "tool"}, observeToolPayload(t, p.Root), env)
	require.Equal(t, 0, code, "stderr:\n%s", stderr)
	requireParsesAsOutput(t, stdout)

	// The second call must genuinely have REACHED the daemon, not spooled like the first. Its own
	// exit code cannot say so — every hook exits 0 either way (§5.4) — so ask the daemon: one live
	// B-B ingest sample can only have come from a request that arrived over the transport
	// (e2eLiveIngestSamples), and only from call 2, since call 1 provably spooled (asserted above)
	// and this test sends no other hot-path op.
	//
	// It is bounded, and the bound covers exactly one window: the hook can exit BEFORE the daemon
	// finishes counting it. A hot-path send waits AckDeadline (8 ms, config/defaults.go) and on
	// expiry the client spools and the hook exits 0 anyway, while a healthy daemon that already
	// took the request goes on to finish Accept — the same "ACK wait expires against a daemon that
	// already ingested it" case the drain assertion below is written around. So this waits on
	// e2eWALVisibleBound, the bound this file already uses for writes that have ALREADY happened
	// becoming visible, rather than assuming the ACK proves the count has landed.
	//
	// It deliberately does NOT read spool/wal-<session>.ndjson, which is what it used to do. That
	// WAL line is real — Accept appends it before the ACK — but the FILE is transient by design,
	// and what removes it is the very re-drain asserted below: a fully drained WAL is deleted
	// unless its session is still live (internal/daemon/drain.go, shouldDelete), and here it never
	// is, because no session.start runs and observe.tool only Touches the registry — a documented
	// no-op for an id Ensure has never seen (registry.go). The old assertion therefore raced its
	// own evidence, and lost wherever unlink is not blocked by the ingest's still-open handle: CI
	// run 32296920486 failed it on ubuntu, macos and cover while windows passed. No bound is the
	// answer to that; nothing brings a deleted file back.
	//
	// Sensitivity is kept where it counts. This still fails on every state the WAL check failed
	// on: a second call that spooled instead of connecting, or was lost outright, records no
	// sample at all, and a daemon that died between the two calls fails e2eStatus's own round
	// trip. The one state it no longer fails on is the state the WAL check could not tell apart
	// from those — an event accepted, WAL'd, ACK'd and then drained. That Accept really writes the
	// WAL stays asserted where the file is durable: TestE2EHookRoundTrip's session IS live
	// (session-start ran first), so its WAL is never deletable and its 50-line wait still fails
	// the moment a hot-path accept stops appending.
	//
	// It remains the precondition for the drain bound below: the daemon re-drains the spool on the
	// first request it serves, so a second call that never connected would leave that drain
	// waiting on the idle tick instead, and naming it here makes the difference a named failure
	// rather than an unexplained timeout twenty lines further down.
	histName := e2eIngestHistName(t)
	require.Eventually(t, func() bool { return e2eLiveIngestSamplesOrUnknown(p.Root, histName) >= 1 },
		e2eWALVisibleBound, e2eSpoolDrainTick,
		"the second observe-tool call never reached the daemon: it left no live %s sample, so it spooled (or was lost) instead of being accepted over the transport",
		histName)

	// With a request served, the daemon has already kicked its spool re-drain: every spool file
	// the first call left behind goes away without waiting for an idle tick.
	//
	// The wait names call 1's exact files (spoolFiles, captured above) rather than asserting the
	// directory holds no client-*.ndjson at all, because the SECOND call can legitimately add one
	// after the re-drain's single directory snapshot: under heavy co-load its ACK wait can expire
	// against a healthy daemon that already ingested the request — the ingest-sample assertion
	// above still passes — and that late file is redrainOnceServing's documented idle-tick
	// territory (§2.5a E, deliberately deferred), not a drain failure. The blanket form failed V2-VERIFY's
	// whole-tree `-count=2` gate on exactly that state (V2-MERGE-25 ②'s bound class, resurfaced);
	// the targeted form pins the same contract with no sensitivity lost — a re-drain that never
	// fires, or that misses any of call 1's files, still fails here.
	require.Eventually(t, func() bool {
		for _, f := range spoolFiles {
			if _, statErr := os.Stat(f); !os.IsNotExist(statErr) {
				// a surviving call-1 spool file means the served-request re-drain missed it
				return false
			}
		}
		return true
	}, e2eSpoolDrainBound, e2eSpoolDrainTick,
		"the spool the first call left behind was not drained within %s of a served request; only the %s idle-tick fallback would still take it",
		e2eSpoolDrainBound, daemon.IdleTickMax)
}

// TestE2EIdleExit is task-6-spec.md's e2e table row: with a fast idle-exit configured, the daemon
// self-terminates once its last session ends, and cleans up its own run/ artifacts.
func TestE2EIdleExit(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)
	env[idleExitSecondsEnvKey] = "1"

	stdout, stderr, code := Run(t, bin, []string{"session-start"}, sessionStartPayload(t, p.Root), env)
	require.Equal(t, 0, code, "stderr:\n%s", stderr)
	requireParsesAsOutput(t, stdout)
	e2eWaitDaemonUp(t, p.Root)

	stdout, stderr, code = Run(t, bin, []string{"flush"}, sessionEndPayload(t, p.Root), env)
	require.Equal(t, 0, code, "stderr:\n%s", stderr)
	requireParsesAsOutput(t, stdout)

	lockPath := filepath.Join(paths.Of(p.Root).Run, "daemon.lock")
	statePath := filepath.Join(paths.Of(p.Root).Run, "state.bin")
	require.Eventually(t, func() bool {
		_, lockErr := os.Stat(lockPath)
		_, stateErr := os.Stat(statePath)
		return os.IsNotExist(lockErr) && os.IsNotExist(stateErr)
	}, e2eHistoryConvergeBound, e2eDaemonDownTick,
		"the daemon must idle-exit and remove both run/daemon.lock and run/state.bin")
}

// TestE2ESelfTestExitsZeroOnHealthy is task-6-spec.md's e2e table row: a healthy, freshly-created
// project's `qompack self-test --json` exits 0 with mode "full".
func TestE2ESelfTestExitsZeroOnHealthy(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })

	stdout, stderr, code := Run(t, bin, []string{"self-test", "--json"}, nil, e2eEnv(p))
	require.Equal(t, 0, code, "stderr:\n%s", stderr)

	var report struct {
		Mode string `json:"mode"`
		Exit int    `json:"exit"`
	}
	require.NoError(t, json.Unmarshal(stdout, &report), "stdout:\n%s", stdout)
	require.Equal(t, "full", report.Mode)
	require.Equal(t, 0, report.Exit)
}

// TestE2ESelfTestExitsNonZeroOnCritical is task-6-spec.md's e2e table row: a project with a
// pre-degraded state/contract.json (as a real daemon-driven degradation would leave one) makes
// `qompack self-test` exit 1 with a banner naming the failed assertion.
//
// The failure is provoked at its real source — three consecutive SessionStarts with no
// terminal-hook marker ever written between them (§12.1's own "absence across two sessions" —
// checkSessionStartFires, internal/contract/assertions.go) — rather than by hand-writing
// state/contract.json, because self-test's own contract checks (internal/cli/selftest.go) run a
// THROWAWAY Monitor against the persisted SessionHistory; they never read the daemon's mode file.
// Three calls, not two: checkSessionStartFires special-cases a brand-new project's very FIRST
// SessionStart to "first-session" (History.Sessions() == 0) without touching StartsWithoutMarker
// at all, so the counter only starts accumulating from the SECOND call onward — it takes a third
// to reach the >= 2 fail threshold.
func TestE2ESelfTestExitsNonZeroOnCritical(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)

	sessions := []core.SessionID{"sess-e2e-degrade-1", "sess-e2e-degrade-2", "sess-e2e-degrade-3"}

	for i, sess := range sessions {
		stdout, stderr, code := Run(t, bin, []string{"session-start"}, sessionStartFor(t, p.Root, sess), env)
		require.Equal(t, 0, code, "session-start #%d: stderr:\n%s", i, stderr)
		requireParsesAsOutput(t, stdout)
		if i == 0 {
			e2eWaitDaemonUp(t, p.Root)
		}

		want := i // StartsWithoutMarker after call i (0-indexed): 0, 1, 2.
		require.Eventually(t, func() bool {
			h := contract.LoadHistory(contract.HistoryPath(p.Root))
			return h.StartsWithoutMarker >= want
		}, e2eHistoryConvergeBound, e2eDaemonUpTick, "session-start #%d never advanced StartsWithoutMarker to >= %d", i, want)
	}

	stdout, stderr, code := Run(t, bin, []string{"self-test", "--json"}, nil, env)
	require.Equal(t, 1, code, "self-test must exit 1 once session_start.fires has genuinely failed; stderr:\n%s", stderr)

	var report struct {
		Checks []struct {
			ID       string `json:"id"`
			OK       bool   `json:"ok"`
			Severity int    `json:"severity"`
		} `json:"checks"`
		Mode string `json:"mode"`
		Exit int    `json:"exit"`
	}
	require.NoError(t, json.Unmarshal(stdout, &report), "stdout:\n%s", stdout)
	require.Equal(t, 1, report.Exit)

	found := false
	for _, c := range report.Checks {
		if c.ID == string(contract.CSessionStartFires) {
			require.False(t, c.OK)
			require.Equal(t, int(contract.SevCritical), c.Severity)
			found = true
		}
	}
	require.True(t, found, "self-test must name session_start.fires among its checks")
}

// TestE2ESpoolSubmodeEndToEnd is task-6-spec.md's e2e table row: with the hot-path state record
// forced into spool submode, an observe-tool call must append straight to the spool WITHOUT ever
// connecting to the (reachable) daemon, and exit 0. The next idle tick drains it.
func TestE2ESpoolSubmodeEndToEnd(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)

	stdout, stderr, code := Run(t, bin, []string{"session-start"}, sessionStartPayload(t, p.Root), env)
	require.Equal(t, 0, code, "stderr:\n%s", stderr)
	requireParsesAsOutput(t, stdout)
	e2eWaitDaemonUp(t, p.Root)

	// The session's own WAL segment is what actually distinguishes "never connected" from
	// "connected" (fix round 1, Important I-6: the previous assertion pinned
	// Counters["ipc_decode_error"], which increments only on a malformed frame — unchanged on
	// both a successful connect AND no connect at all, so it could never fail either way).
	// observe.tool is a hot-path op: a NORMALLY-accepted request is WAL-appended by
	// ingest.Accept before the daemon ever ACKs it (§2.4's own durability-boundary ordering), so
	// if the spool-submode client below had connected despite Hot==HotSpool, this session's WAL
	// would gain a line. Session-start itself is not a hot-path op and never touches the WAL, so
	// the line count is 0 both before and after a genuinely spooled call.
	walPath := filepath.Join(paths.Of(p.Root).Spool, "wal-"+string(e2eSession)+".ndjson")
	walLinesBefore := e2eCountFileLines(t, walPath)

	st := ipc.ReadState(p.Root, config.Defaults())
	st.Hot = ipc.HotSpool
	require.NoError(t, ipc.WriteState(p.Root, st))

	stdout, stderr, code = Run(t, bin, []string{"observe", "tool"}, observeToolPayload(t, p.Root), env)
	require.Equal(t, 0, code, "stderr:\n%s", stderr)
	requireParsesAsOutput(t, stdout)

	spoolFiles, err := ipc.SpoolFiles(paths.Of(p.Root).Spool)
	require.NoError(t, err)
	foundClientSpool := false
	for _, f := range spoolFiles {
		if filepath.Base(f) != "wal-"+string(e2eSession)+".ndjson" {
			foundClientSpool = true
		}
	}
	require.True(t, foundClientSpool, "a spool-submode client must append to its own spool file, never the daemon's WAL")

	walLinesAfter := e2eCountFileLines(t, walPath)
	require.Equal(t, walLinesBefore, walLinesAfter,
		"a spool-submode client must never have connected — the session's WAL must gain no lines from this call")
}

// e2eCountFileLines counts non-empty lines in the file at p, reporting 0 for a file that does not
// exist yet (the ordinary case before any WAL segment has been created for a session).
func e2eCountFileLines(t *testing.T, p string) int {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		require.NoError(t, err)
	}
	return countNonEmptyLines(string(b))
}

// sessionStartPayload, sessionEndPayload and observeToolPayload build minimal, valid payloads
// rooted at root for this file's own session id.
func sessionStartPayload(t *testing.T, root string) []byte {
	return sessionStartFor(t, root, e2eSession)
}

func sessionStartFor(t *testing.T, root string, sess core.SessionID) []byte {
	t.Helper()
	b, err := json.Marshal(hookio.Event{
		HookEventName: "SessionStart", SessionID: sess, CWD: root, Source: "startup",
	})
	require.NoError(t, err)
	return b
}

func sessionEndPayload(t *testing.T, root string) []byte {
	t.Helper()
	b, err := json.Marshal(hookio.Event{HookEventName: "SessionEnd", SessionID: e2eSession, CWD: root})
	require.NoError(t, err)
	return b
}

func observeToolPayload(t *testing.T, root string) []byte {
	t.Helper()
	b, err := json.Marshal(hookio.Event{
		HookEventName: "PostToolUse", SessionID: e2eSession, CWD: root,
		ToolName: "Read", ToolUseID: core.ToolUseID("toolu_e2e"),
		ToolInput: json.RawMessage(`{"file_path":"a.go"}`), ToolResponse: json.RawMessage(`{"content":"package a"}`),
	})
	require.NoError(t, err)
	return b
}

// requireParsesAsOutput asserts stdout is a single valid hookio.Output — the ubiquitous "every hook
// exits 0 with valid JSON" property this whole file leans on.
func requireParsesAsOutput(t *testing.T, stdout []byte) {
	t.Helper()
	var out hookio.Output
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(stdout), &out), "stdout:\n%s", stdout)
}

// countNonEmptyLines counts non-blank lines in s.
func countNonEmptyLines(s string) int {
	n := 0
	for _, line := range splitLines(s) {
		if len(line) > 0 {
			n++
		}
	}
	return n
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
