// The SP-08 e2e rows: the L0 observer driven through the REAL binary and the REAL daemon — the
// only place the whole path (hook client -> ipc -> WAL -> worker pool -> Services seam ->
// observer -> store/DAG/sketches) is exercised end to end.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// obsProcessBound bounds waiting for hot-path events whose ACKs have already returned to finish
// ASYNCHRONOUS processing (worker pool -> observer -> index append). Basis: each event's
// processing is bounded by the B-C budget class (single-digit milliseconds of store work), so even
// 44 events are sub-second on a quiet host; the bound is two orders of magnitude above that for a
// co-loaded CI runner, and a failure names the mechanism rather than the machine.
const (
	obsProcessBound = 30 * time.Second
	obsProcessTick  = 50 * time.Millisecond
)

// obsRunHook runs one hook subcommand and asserts the two §2.3 invariants every hook owes the
// host: exit 0, and a stdout that parses as a hookio.Output.
func obsRunHook(t *testing.T, bin string, argv []string, payload []byte, env map[string]string) {
	t.Helper()
	stdout, stderr, code := Run(t, bin, argv, payload, env)
	require.Equal(t, 0, code, "argv=%v must exit 0\nstdout:\n%s\nstderr:\n%s", argv, stdout, stderr)
	requireParsesAsOutput(t, stdout)
}

// The payload builders below marshal through a raw map rather than hookio.Event wherever a
// top-level key outside Event's struct tags is needed (Event.Extra is `json:"-"` and would drop
// it on the way out).
func obsToolPayload(t *testing.T, root string, sess core.SessionID, id, path, content string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"hook_event_name": "PostToolUse", "session_id": sess, "cwd": root,
		"tool_name": "Read", "tool_use_id": id,
		"tool_input":    map[string]string{"file_path": path},
		"tool_response": map[string]string{"content": content},
	})
	require.NoError(t, err)
	return b
}

func obsBashPayload(t *testing.T, root string, sess core.SessionID, id, command, out string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"hook_event_name": "PostToolUse", "session_id": sess, "cwd": root,
		"tool_name": "Bash", "tool_use_id": id,
		"tool_input":    map[string]string{"command": command},
		"tool_response": map[string]any{"exit_code": 0, "stdout": out},
	})
	require.NoError(t, err)
	return b
}

func obsPromptPayload(t *testing.T, root string, sess core.SessionID, prompt string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"hook_event_name": "UserPromptSubmit", "session_id": sess, "cwd": root, "prompt": prompt,
	})
	require.NoError(t, err)
	return b
}

func obsStopPayload(t *testing.T, root string, sess core.SessionID) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"hook_event_name": "Stop", "session_id": sess, "cwd": root, "stop_hook_active": true,
	})
	require.NoError(t, err)
	return b
}

func obsSubagentStopPayload(t *testing.T, root string, sess core.SessionID, agentType, summary string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"hook_event_name": "SubagentStop", "session_id": sess, "cwd": root,
		"tool_response": map[string]string{"content": summary},
		"subagent_type": agentType,
	})
	require.NoError(t, err)
	return b
}

func obsFlushPayload(t *testing.T, root string, sess core.SessionID) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"hook_event_name": "SessionEnd", "session_id": sess, "cwd": root,
	})
	require.NoError(t, err)
	return b
}

// obsToolUseLines returns index/tool_use.jsonl's non-empty lines, or nil before the file exists.
func obsToolUseLines(root string) []string {
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Index, "tool_use.jsonl")))
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// TestE2E_ObserverThroughDaemon: session-start, 40 observe tool, 3 observe prompt, 1 observe stop
// --subagent, flush — every hook exits 0; the index holds one record per event; the DAG log is
// non-empty; the two observer-owned sketch files exist and tried.bloom does not.
func TestE2E_ObserverThroughDaemon(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)
	sess := core.SessionID("sess-e2e-observer")

	obsRunHook(t, bin, []string{"session-start"}, sessionStartFor(t, p.Root, sess), env)
	e2eWaitDaemonUp(t, p.Root)

	// Distinct ids, paths and content: no supersession, so the line count is exact.
	for i := range 40 {
		obsRunHook(t, bin, []string{"observe", "tool"},
			obsToolPayload(t, p.Root, sess, fmt.Sprintf("toolu_obs_%02d", i),
				fmt.Sprintf("src/obs%02d.go", i), fmt.Sprintf("package obs%02d\n", i)), env)
	}
	for i := range 3 {
		obsRunHook(t, bin, []string{"observe", "prompt"},
			obsPromptPayload(t, p.Root, sess, fmt.Sprintf("prompt number %d, keep going", i)), env)
	}
	obsRunHook(t, bin, []string{"observe", "stop", "--subagent"},
		obsSubagentStopPayload(t, p.Root, sess, "worker", "did the thing"), env)

	// 40 tool records + 3 verbatim prompts + 1 subagent capture. The tool/stop events are
	// processed asynchronously behind the ACK, so wait on the index itself before flushing.
	const wantRecords = 44
	require.Eventually(t, func() bool { return len(obsToolUseLines(p.Root)) >= wantRecords },
		obsProcessBound, obsProcessTick,
		"index/tool_use.jsonl never reached %d lines (have %d)", wantRecords, len(obsToolUseLines(p.Root)))

	obsRunHook(t, bin, []string{"flush"}, obsFlushPayload(t, p.Root, sess), env)

	require.Len(t, obsToolUseLines(p.Root), wantRecords,
		"distinct ids, paths and content must yield exactly one record per event")

	deps, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(p.Root).DAG, "deps.jsonl")))
	require.NoError(t, err, "dag/deps.jsonl must exist after a flush")
	require.NotEmpty(t, bytes.TrimSpace(deps))

	sketches := paths.Of(p.Root).Sketches
	require.FileExists(t, paths.Long(filepath.Join(sketches, "touch.cms")))
	require.FileExists(t, paths.Long(filepath.Join(sketches, "explore.hll")))
	require.NoFileExists(t, paths.Long(filepath.Join(sketches, "tried.bloom")),
		"tried.bloom is negknow.RebuildBloom's alone (§3.3)")
}

// obsDenyWrites makes dir non-writable for the current user: a real deny-ACE via icacls on
// Windows (the FILE_ATTRIBUTE_READONLY bit is a near-no-op for directories there — the same
// mechanism internal/cli's spool-readonly fault site uses), plain permission bits elsewhere.
func obsDenyWrites(dir string) error {
	if runtime.GOOS == "windows" {
		u, err := user.Current()
		if err != nil {
			return err
		}
		return exec.Command("icacls", dir, "/deny", u.Username+":(OI)(CI)W").Run() //nolint:gosec // G204: fixed subcommand over this test's own temp directory
	}
	return os.Chmod(dir, 0o500)
}

// obsErrCounterTotal sums every observer.err.* counter in the daemon's status snapshot, or -1
// when the daemon cannot be asked — Eventually-safe, mirroring e2eLiveIngestSamplesOrUnknown.
func obsErrCounterTotal(root string) int64 {
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
		Op: ipc.OpStatus, Session: "sess-e2e-obsfault", TS: core.NowMilli(core.SystemClock()), Reply: true,
	}, e2eRoundTripDeadline)
	if err != nil || !resp.OK {
		return -1
	}
	var snap daemon.StatusSnapshot
	if json.Unmarshal(resp.Data, &snap) != nil {
		return -1
	}
	var total int64
	for name, v := range snap.Counters {
		if strings.HasPrefix(name, "observer.err.") {
			total += v
		}
	}
	return total
}

// TestE2E_HooksExitZeroUnderFaultInjection: with .qompack/objects made read-only underneath a
// live daemon, the same hook sequence still exits 0 on every call, and the failures are recorded
// observably (the observer.err.* counters the daemon persists and serves).
func TestE2E_HooksExitZeroUnderFaultInjection(t *testing.T) {
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		t.Skip("permission-based fault injection is inert when running as root")
	}
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)
	sess := core.SessionID("sess-e2e-obsfault")

	obsRunHook(t, bin, []string{"session-start"}, sessionStartFor(t, p.Root, sess), env)
	e2eWaitDaemonUp(t, p.Root)

	objects := paths.Of(p.Root).Objects
	t.Cleanup(func() { resetPermissionsForCleanup(p.Root) })
	require.NoError(t, obsDenyWrites(objects))

	for i := range 5 {
		obsRunHook(t, bin, []string{"observe", "tool"},
			obsToolPayload(t, p.Root, sess, fmt.Sprintf("toolu_fault_%02d", i),
				fmt.Sprintf("src/fault%02d.go", i), fmt.Sprintf("package fault%02d\n", i)), env)
	}
	obsRunHook(t, bin, []string{"observe", "prompt"},
		obsPromptPayload(t, p.Root, sess, "a prompt whose bytes cannot land"), env)
	obsRunHook(t, bin, []string{"observe", "stop"}, obsStopPayload(t, p.Root, sess), env)
	obsRunHook(t, bin, []string{"flush"}, obsFlushPayload(t, p.Root, sess), env)

	require.Eventually(t, func() bool { return obsErrCounterTotal(p.Root) > 0 },
		obsProcessBound, obsProcessTick,
		"no observer.err.* counter ever appeared: the injected write failures left no observable record")
}

// TestE2E_SupersessionVisibleAfterRestart: the second identical read of a path supersedes the
// first, and the flip survives a full daemon restart and a fresh store.Open over the same tree.
func TestE2E_SupersessionVisibleAfterRestart(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)
	sess := core.SessionID("sess-e2e-supersede")

	obsRunHook(t, bin, []string{"session-start"}, sessionStartFor(t, p.Root, sess), env)
	e2eWaitDaemonUp(t, p.Root)

	content := "package dup\nfunc Dup() int { return 1 }\n"
	obsRunHook(t, bin, []string{"observe", "tool"},
		obsToolPayload(t, p.Root, sess, "toolu_sup_a", "src/dup.go", content), env)
	require.Eventually(t, func() bool { return len(obsToolUseLines(p.Root)) >= 1 },
		obsProcessBound, obsProcessTick, "the first read was never indexed")

	obsRunHook(t, bin, []string{"observe", "tool"},
		obsToolPayload(t, p.Root, sess, "toolu_sup_b", "src/dup.go", content), env)
	require.Eventually(t, func() bool {
		return strings.Contains(strings.Join(obsToolUseLines(p.Root), "\n"), `"op":"supersede"`)
	}, obsProcessBound, obsProcessTick, "the second identical read never superseded the first")

	obsRunHook(t, bin, []string{"flush"}, obsFlushPayload(t, p.Root, sess), env)
	e2eShutdownIfReachable(t, p.Root)

	// Restart the daemon over the same tree, then shut it down and read the store directly.
	obsRunHook(t, bin, []string{"session-start"}, sessionStartFor(t, p.Root, sess), env)
	e2eWaitDaemonUp(t, p.Root)
	e2eShutdownIfReachable(t, p.Root)

	st, err := store.Open(p.Root, config.Defaults(), store.Deps{Log: logging.Nop()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	rec, err := st.ToolUse(context.Background(), "toolu_sup_a")
	require.NoError(t, err)
	require.Equal(t, store.StatusSuperseded, rec.Status,
		"the first record must carry the superseded status after a restart")
	require.Equal(t, core.ToolUseID("toolu_sup_b"), rec.SupersededBy)
}

// TestE2E_VerbatimPromptSurvivesRestart: G2.3's end-to-end proof — the prompt's exact bytes,
// volatile substrings included, come back from a fresh store.Open after the daemon is gone.
func TestE2E_VerbatimPromptSurvivesRestart(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)
	sess := core.SessionID("sess-e2e-verbatim")

	// A curly apostrophe, an ISO-8601 timestamp and a PID: exactly the substrings the configured
	// canonicalizer classes would strip, all of which verbatim capture must keep.
	prompt := "It’s 2026-08-24T12:34:56Z and PID 4242 — don’t normalise me"

	obsRunHook(t, bin, []string{"session-start"}, sessionStartFor(t, p.Root, sess), env)
	e2eWaitDaemonUp(t, p.Root)
	obsRunHook(t, bin, []string{"observe", "prompt"}, obsPromptPayload(t, p.Root, sess, prompt), env)
	obsRunHook(t, bin, []string{"flush"}, obsFlushPayload(t, p.Root, sess), env)
	e2eShutdownIfReachable(t, p.Root)

	st, err := store.Open(p.Root, config.Defaults(), store.Deps{Log: logging.Nop()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	// The prompt was the session's first event, so it was recorded at turn 0 under the derived id.
	rec, err := st.ToolUse(context.Background(), observer.VerbatimPromptID(sess, 0))
	require.NoError(t, err, "the verbatim prompt record must survive the restart")
	rc, err := st.Open(context.Background(), rec.Root)
	require.NoError(t, err)
	got, err := io.ReadAll(rc)
	require.NoError(t, rc.Close())
	require.NoError(t, err)

	require.Equal(t, prompt, string(got), "the bytes must equal the original prompt exactly")
	for _, volatile := range []string{"’", "2026-08-24T12:34:56Z", "4242"} {
		require.Contains(t, string(got), volatile, "the volatile substring must be intact")
	}
}

// TestE2E_SubagentNameReachesTheDaemon: the hook client resolves subagent_type in ITS process
// (Event.Extra is dropped at the IPC boundary) and the capture record carries the real name — the
// assertion the in-process unit test cannot make.
func TestE2E_SubagentNameReachesTheDaemon(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)
	sess := core.SessionID("sess-e2e-subagent")

	obsRunHook(t, bin, []string{"session-start"}, sessionStartFor(t, p.Root, sess), env)
	e2eWaitDaemonUp(t, p.Root)
	obsRunHook(t, bin, []string{"observe", "stop", "--subagent"},
		obsSubagentStopPayload(t, p.Root, sess, "code-reviewer", "reviewed the diff"), env)

	// The capture is written asynchronously behind the ACK; the raw line carries the resolved
	// name whichever wire keys the index uses, so this waits schema-free before the flush.
	require.Eventually(t, func() bool {
		return strings.Contains(strings.Join(obsToolUseLines(p.Root), "\n"), "code-reviewer")
	}, obsProcessBound, obsProcessTick, "no capture line naming the subagent ever appeared")

	obsRunHook(t, bin, []string{"flush"}, obsFlushPayload(t, p.Root, sess), env)
	e2eShutdownIfReachable(t, p.Root)

	// Read the record back through the store's own index over the same tool_use.jsonl.
	st, err := store.Open(p.Root, config.Defaults(), store.Deps{Log: logging.Nop()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	rec, err := st.ToolUse(context.Background(), observer.SubagentCaptureID(sess, 0))
	require.NoError(t, err, "the capture record must be indexed under the derived id")
	require.Equal(t, "SubagentStop", rec.Tool)
	require.Equal(t, "code-reviewer", rec.Subagent,
		"the subagent field must carry the real name, never the %q fallback", "subagent")
}

// TestE2E_ThinSliceDropsControlOnlyEdges: real traffic produces EdgeControlOnly links, and a thin
// backward slice over the persisted graph is strictly smaller than a full one — the property ADR
// 0007's figures claim of production traffic.
func TestE2E_ThinSliceDropsControlOnlyEdges(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)
	sess := core.SessionID("sess-e2e-thinslice")

	obsRunHook(t, bin, []string{"session-start"}, sessionStartFor(t, p.Root, sess), env)
	e2eWaitDaemonUp(t, p.Root)

	// 40 mixed calls: repeated reads of one hot file (shared-state coupling -> EdgeSequence
	// links) followed by unrelated Bash calls (nothing shared -> EdgeControlOnly links).
	lastID := ""
	for i := range 40 {
		id := fmt.Sprintf("toolu_thin_%02d", i)
		lastID = id
		if i < 10 {
			obsRunHook(t, bin, []string{"observe", "tool"},
				obsToolPayload(t, p.Root, sess, id, "src/hot.go", "package hot\n"), env)
		} else {
			obsRunHook(t, bin, []string{"observe", "tool"},
				obsBashPayload(t, p.Root, sess, id, fmt.Sprintf("echo %d", i), fmt.Sprintf("out %d\n", i)), env)
		}
	}
	require.Eventually(t, func() bool {
		return strings.Contains(strings.Join(obsToolUseLines(p.Root), "\n"), lastID)
	}, obsProcessBound, obsProcessTick, "the last mixed call was never indexed")

	obsRunHook(t, bin, []string{"flush"}, obsFlushPayload(t, p.Root, sess), env)
	e2eShutdownIfReachable(t, p.Root)

	g, err := dag.Open(p.Root, config.Defaults(), logging.Nop())
	require.NoError(t, err)

	foundControlOnly := false
	for _, n := range g.NodesAfter(0) {
		for _, e := range g.Out(n.ID) {
			if e.Kind == dag.EdgeControlOnly {
				foundControlOnly = true
			}
		}
	}
	require.True(t, foundControlOnly, "real mixed traffic must produce at least one EdgeControlOnly")

	criteria := []dag.NodeID{dag.ToolResultNode(core.ToolUseID(lastID))}
	full, err := g.BackwardSlice(criteria, dag.SliceOptions{Thin: false})
	require.NoError(t, err)
	thin, err := g.BackwardSlice(criteria, dag.SliceOptions{Thin: true})
	require.NoError(t, err)
	require.NotEmpty(t, thin.Order)
	require.Less(t, len(thin.Order), len(full.Order),
		"Thin: true must return strictly fewer nodes than Thin: false")
}
