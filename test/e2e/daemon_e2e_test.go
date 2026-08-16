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
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
)

// e2eProbeTimeout and e2eRoundTripDeadline bound the raw ipc.Client this file constructs directly
// (a test-only admin/status client, distinct from the real hook subcommands under test) to poke a
// live daemon for facts a hook's own stdout cannot report — its live session count, whether it is
// reachable at all.
const (
	e2eProbeTimeout      = 200 * time.Millisecond
	e2eRoundTripDeadline = 5 * time.Second
	e2eDaemonUpBound     = 10 * time.Second
	e2eDaemonUpTick      = 20 * time.Millisecond
	e2eDaemonDownBound   = 15 * time.Second
	e2eDaemonDownTick    = 100 * time.Millisecond
	// e2eHistoryConvergeBound is generous relative to e2eDaemonUpBound: it bounds waiting for a
	// history.json mutation from a session-start that is not the FIRST one in a project, or for a
	// daemon's own idle-exit to complete. What actually made this flaky earlier in fix round 1 was
	// a real bug, not host load: hookConnectDeadlineFloor's own doc comment
	// (internal/cli/hookclient.go) explains it in full — Important I-1's removal of
	// NewClientWithOptions's double state.bin read (correctly required; see that fix's own comment)
	// also removed an incidental few-ms buffer that had been quietly carrying a Reply op's very
	// first post-daemon-start dial across a too-tight State.ConnectDeadlineMs (5ms, sized for the
	// hot path's already-warm cadence). With that dial budget now widened for session-start/
	// checkpoint/flush specifically, this bound is generous headroom for real sibling-test load,
	// not compensation for a race.
	e2eHistoryConvergeBound = 20 * time.Second
	e2eSpoolDrainBound      = 10 * time.Second
	e2eSpoolDrainTick       = 100 * time.Millisecond
	idleExitSecondsEnvKey   = "QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS"
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
	c := ipc.NewClientWithOptions(addr, sp, nil, nil, ipc.ClientOptions{ProjectRoot: root})
	defer func() { _ = c.Close() }()

	resp, err := c.Send(context.Background(), ipc.Request{
		Op: ipc.OpStatus, Session: e2eSession, TS: core.NowMilli(core.SystemClock()), Reply: true,
	}, e2eRoundTripDeadline)
	require.NoError(t, err)
	require.True(t, resp.OK, "status round trip must succeed against a reachable daemon")

	var snap daemon.StatusSnapshot
	require.NoError(t, json.Unmarshal(resp.Data, &snap))
	return snap
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
	}, e2eSpoolDrainBound, e2eSpoolDrainTick, "wal-%s.ndjson never reached %d lines (last seen %d) at %s", e2eSession, n, lines, walPath)

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
// observe-tool call ACKs it and the spool drains into the WAL.
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

	// The now-live daemon drains the spool the first call left behind (on start, and on its own
	// idle tick) — eventually every client-*.ndjson spool file is gone.
	require.Eventually(t, func() bool {
		files, err := ipc.SpoolFiles(paths.Of(p.Root).Spool)
		if err != nil {
			return false
		}
		for _, f := range files {
			if filepath.Base(f) != "wal-"+string(e2eSession)+".ndjson" && filepath.Ext(f) == ".ndjson" {
				// any remaining client-*.ndjson spool file means drain hasn't finished
				return false
			}
		}
		return true
	}, e2eSpoolDrainBound, e2eSpoolDrainTick, "the spool the first call left behind was never drained")
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
