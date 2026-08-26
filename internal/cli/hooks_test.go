package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// SP-05 replaces the six hook bodies with thin ipc clients (hookclient.go); the tests that used to
// live here exercised the old no-op observation logger (hooks-*.jsonl) that body no longer writes.
// These replace them.

// TestHooks_AllSixExitZeroWithValidJSON runs every hook against a real project with no daemon
// reachable — the ordinary in-process-test condition (none of these Env values sets Self, so lazy
// spawn is disabled entirely; see cli.Env.Self's own doc comment) — and checks every one exits 0
// and answers with parseable JSON.
func TestHooks_AllSixExitZeroWithValidJSON(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()

	payload, err := json.Marshal(map[string]any{
		"session_id": "s-hooks",
		"cwd":        dir,
		"tool_name":  "FileRead",
		"source":     "startup",
		"trigger":    "auto",
	})
	require.NoError(t, err)

	for _, hook := range hookNames {
		t.Run(hook, func(t *testing.T) {
			var out, errw bytes.Buffer
			code := Dispatch(context.Background(), All(), argvFor(hook), Env{
				Getenv:  noEnv,
				Stdin:   bytes.NewReader(payload),
				Clock:   testClock(),
				HomeDir: home,
			}, &out, &errw)
			require.Equal(t, ExitOK, code, "hook %q: stderr=%s", hook, errw.String())

			var got hookio.Output
			require.NoError(t, json.Unmarshal(bytes.TrimSpace(out.Bytes()), &got),
				"hook %q must write parseable hookio.Output, got %q", hook, out.String())
		})
	}
}

// TestHooks_ModeOffShortCircuits pins the very first branch of the hook skeleton: a persisted
// ModeOff state answers with the empty response before stdin is even read, and touches nothing
// else.
func TestHooks_ModeOffShortCircuits(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, ipc.WriteState(dir, ipc.State{Mode: contract.ModeOff}))

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), argvFor("observe tool"), Env{
		Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": dir}),
		Stdin:   errReader{}, // ModeOff must short-circuit before this is ever read.
		Clock:   testClock(),
		HomeDir: t.TempDir(),
	}, &out, &errw)

	require.Equal(t, ExitOK, code, "stderr=%s", errw.String())
	require.Equal(t, "{}\n", out.String())
}

// TestHooks_RefuseToCreateStoreUnderMissingRoot pins the guard in hookclient.go's doHook: a payload
// naming a root that does not exist on disk must never MkdirAll a store there.
//
// paths.Resolve is faithful to §3.3, whose last resort is "the payload cwd itself" — so a payload
// carrying a path that is not a real directory resolves cleanly to a path that is not a real
// directory. Without the isDir check, the spool append would conjure a .qompack/ tree in a location
// no project occupies.
func TestHooks_RefuseToCreateStoreUnderMissingRoot(t *testing.T) {
	parent := t.TempDir()
	missing := filepath.Join(parent, "no-such-project")

	payload, err := json.Marshal(map[string]any{"session_id": "s1", "cwd": missing})
	require.NoError(t, err)

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), argvFor("observe tool"), Env{
		Getenv:  noEnv,
		Stdin:   bytes.NewReader(payload),
		Clock:   testClock(),
		HomeDir: t.TempDir(),
	}, &out, &errw)

	require.Equal(t, ExitOK, code)
	require.NotEmpty(t, out.String(), "the host is still owed a response")

	_, statErr := os.Stat(missing)
	require.True(t, os.IsNotExist(statErr),
		"a hook must not conjure a project root that does not exist, got %v", statErr)
}

// TestHooks_RootReResolvesFromPayload pins the skeleton's step 6: the pre-stdin root guess (env or
// process cwd) is discarded once the payload's own cwd disagrees, and everything from that point
// on — including the spool — uses the payload's root.
func TestHooks_RootReResolvesFromPayload(t *testing.T) {
	dir := t.TempDir()

	payload, err := json.Marshal(map[string]any{"session_id": "s1", "cwd": dir, "tool_name": "Read"})
	require.NoError(t, err)

	var out, errw bytes.Buffer
	// No QOMPACK_PROJECT_ROOT: the pre-stdin guess falls back to the test process's own cwd, which
	// is not dir, so the payload's cwd must win.
	code := Dispatch(context.Background(), All(), argvFor("observe tool"), Env{
		Getenv:  noEnv,
		Stdin:   bytes.NewReader(payload),
		Clock:   testClock(),
		HomeDir: t.TempDir(),
	}, &out, &errw)
	require.Equal(t, ExitOK, code, "stderr=%s", errw.String())

	// The client's connect failure (no daemon) spools into dir's own spool directory — proof the
	// re-resolved root, not the pre-stdin guess, is what everything downstream actually used.
	entries, err := os.ReadDir(paths.Of(dir).Spool)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "the client must have spooled under the payload's own root")
}

// TestHooks_ObserveStopSubagentFlagReachesRaw pins rawExtras: `observe stop --subagent` carries
// {"subagent":true} in the spooled request's Raw field.
func TestHooks_ObserveStopSubagentFlagReachesRaw(t *testing.T) {
	dir := t.TempDir()
	payload, err := json.Marshal(map[string]any{"session_id": "s-subagent", "cwd": dir})
	require.NoError(t, err)

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), argvFor("observe stop", "--subagent"), Env{
		Getenv:  noEnv,
		Stdin:   bytes.NewReader(payload),
		Clock:   testClock(),
		HomeDir: t.TempDir(),
	}, &out, &errw)
	require.Equal(t, ExitOK, code, "stderr=%s", errw.String())

	req := onlySpooledRequest(t, dir)
	require.JSONEq(t, `{"subagent":true}`, string(req.Raw))
}

// TestHooks_CheckpointTriggerReachesRaw pins rawExtras' checkpoint branch: the event's own Trigger
// field (not a CLI flag — the manifest invokes `checkpoint` with none) reaches Raw as
// {"trigger":"..."}.
func TestHooks_CheckpointTriggerReachesRaw(t *testing.T) {
	dir := t.TempDir()
	payload, err := json.Marshal(map[string]any{"session_id": "s-trigger", "cwd": dir, "trigger": "manual"})
	require.NoError(t, err)

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), argvFor("checkpoint"), Env{
		Getenv:  noEnv,
		Stdin:   bytes.NewReader(payload),
		Clock:   testClock(),
		HomeDir: t.TempDir(),
	}, &out, &errw)
	require.Equal(t, ExitOK, code, "stderr=%s", errw.String())

	req := onlySpooledRequest(t, dir)
	require.JSONEq(t, `{"trigger":"manual"}`, string(req.Raw))
}

// TestHooks_LogQuietWritesWhenLogsDirExists pins logQuiet's positive case (fix round 1, Important
// I-8): a hook whose stdin cannot be read at all (errReader{}, so hookio.ReadEvent fails) still
// records that failure to .qompack/logs/hook-quiet-YYYYMMDD.jsonl, PROVIDED that directory already
// exists.
func TestHooks_LogQuietWritesWhenLogsDirExists(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(dir)))

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), argvFor("observe tool"), Env{
		Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": dir}),
		Stdin:   errReader{},
		Clock:   testClock(),
		HomeDir: t.TempDir(),
	}, &out, &errw)
	require.Equal(t, ExitOK, code, "stderr=%s", errw.String())

	matches, err := filepath.Glob(filepath.Join(paths.Of(dir).Logs, "hook-quiet-*.jsonl"))
	require.NoError(t, err)
	require.Len(t, matches, 1, "logQuiet must write exactly one hook-quiet-*.jsonl line when the logs directory already exists")

	b, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	var rec quietLogLine
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(b), &rec))
	require.NotEmpty(t, rec.TS)
	require.Contains(t, rec.Err, "stdin is unreadable")
}

// TestHooks_LogQuietNeverCreatesLogsDir pins logQuiet's negative case (fix round 1, Important
// I-8): on a project whose .qompack/logs has never been created by anything else, the exact same
// failure must leave the project untouched — a hook must never create a directory on its own
// error path.
func TestHooks_LogQuietNeverCreatesLogsDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(dir, 0o700)) // the project root itself exists; nothing under it does.

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), argvFor("observe tool"), Env{
		Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": dir}),
		Stdin:   errReader{},
		Clock:   testClock(),
		HomeDir: t.TempDir(),
	}, &out, &errw)
	require.Equal(t, ExitOK, code, "stderr=%s", errw.String())

	_, statErr := os.Stat(paths.Of(dir).Logs)
	require.True(t, os.IsNotExist(statErr),
		"logQuiet must never create .qompack/logs itself, got stat error %v", statErr)
	_, statErr = os.Stat(paths.Of(dir).Dot)
	require.True(t, os.IsNotExist(statErr),
		"a hook whose only failure is an unreadable stdin must not conjure .qompack/ at all, got stat error %v", statErr)
}

// onlySpooledRequest reads the one spool file a single hook invocation with no reachable daemon
// must have produced under root, and decodes its one NDJSON line as an ipc.Request.
func onlySpooledRequest(t *testing.T, root string) ipc.Request {
	t.Helper()
	dir := paths.Of(root).Spool
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "exactly one client spool file")

	b, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)

	req, err := ipc.DecodeRequest(bytes.TrimSpace(b))
	require.NoError(t, err)
	return req
}

// TestHookConnectDeadline pins fix round 2's Important N-2: only a non-hot-path op with its own
// fixed spec.deadline (session-start, checkpoint, flush) gets widened to hookConnectDeadlineFloor.
// observe.prompt is the case the original predicate (spec.deadline > 0 alone) got wrong — it
// carries a fixed spec.deadline (promptReplyDeadline) despite being squarely on the hot path
// (ipc.Op.HotPath()), and must keep State.ConnectDeadlineMs's own tight budget exactly like
// observe.tool and observe.stop.
func TestHookConnectDeadline(t *testing.T) {
	// A hot-path-sized connect budget, well under hookConnectDeadlineFloor: the point of every row
	// below is which ops get widened past it, so the number is written here rather than read from
	// config.Defaults() (whose own value is platform-specific — internal/config/deadlines.go — and
	// would make "was it widened?" depend on the host running the test).
	tightState := ipc.State{ConnectDeadlineMs: 5}

	tests := []struct {
		name string
		spec hookSpec
		want time.Duration
	}{
		{"observe.tool (hot path, no fixed deadline)", hookSpec{op: ipc.OpObserveTool, reply: false}, 5 * time.Millisecond},
		{
			"observe.prompt (hot path, DOES carry a fixed deadline)",
			hookSpec{op: ipc.OpObservePrompt, reply: true, deadline: promptReplyDeadline},
			5 * time.Millisecond, // must NOT be widened — this is the exact N-2 regression case.
		},
		{"observe.stop (hot path, no fixed deadline)", hookSpec{op: ipc.OpObserveStop, reply: false}, 5 * time.Millisecond},
		{
			"session-start (not hot path, fixed deadline)",
			hookSpec{op: ipc.OpSessionStart, reply: true, deadline: sessionStartReplyDeadline},
			hookConnectDeadlineFloor,
		},
		{
			"checkpoint (not hot path, fixed deadline)",
			hookSpec{op: ipc.OpCheckpoint, reply: true, deadline: checkpointReplyDeadline},
			hookConnectDeadlineFloor,
		},
		{
			"flush (not hot path, fixed deadline)",
			hookSpec{op: ipc.OpFlush, reply: true, deadline: flushReplyDeadline},
			hookConnectDeadlineFloor,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, hookConnectDeadline(tt.spec, tightState))
		})
	}

	t.Run("a non-hot-path op's own State.ConnectDeadlineMs is kept when it already exceeds the floor", func(t *testing.T) {
		roomy := ipc.State{ConnectDeadlineMs: 9000}
		spec := hookSpec{op: ipc.OpFlush, reply: true, deadline: flushReplyDeadline}
		require.Equal(t, 9000*time.Millisecond, hookConnectDeadline(spec, roomy))
	})
}

// TestEnsureDaemonRunning_GatedOnDaemonEnabled pins fix round 2's FR-6: session-start's preSend
// seam must not attempt daemon.EnsureRunning at all when runtime.daemon.enabled is false — before
// this fix, an operator who disabled the daemon still got a resident process spawned at every
// session start. self names a path that does not exist, so if EnsureRunning IS attempted it
// reaches SpawnDetached, which fails fast (no process is ever actually started) and logs
// "daemon: spawn failed" via Warn — an observable, deterministic proxy for "EnsureRunning ran"
// that needs no real daemon process on either side of the assertion.
func TestEnsureDaemonRunning_GatedOnDaemonEnabled(t *testing.T) {
	// hookLogger.materialize() never closes the *os.File logging.New hands back (a hook process
	// is short-lived and the OS reclaims it at exit) — harmless in production, but it means a
	// t.TempDir() root exercising the real Warn-logging path here would fail its own automatic
	// cleanup on Windows ("used by another process"). A manually-managed, best-effort-removed
	// directory sidesteps that without weakening the assertions.
	newRoot := func(t *testing.T) string {
		t.Helper()
		root, err := os.MkdirTemp("", "qompack-ensurerunning-*")
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.RemoveAll(root) })
		require.NoError(t, paths.EnsureLayout(paths.Of(root)))
		return root
	}
	selfPath := filepath.Join(t.TempDir(), "does-not-exist")

	t.Run("disabled: EnsureRunning is never attempted", func(t *testing.T) {
		root := newRoot(t)

		ensureDaemonRunning(root, selfPath, ipc.State{DaemonEnabled: false}, testClock())

		matches, err := filepath.Glob(filepath.Join(paths.Of(root).Logs, "qompack-*.log"))
		require.NoError(t, err)
		for _, m := range matches {
			b, rerr := os.ReadFile(m)
			require.NoError(t, rerr)
			require.NotContains(t, string(b), "spawn failed",
				"runtime.daemon.enabled=false must never reach daemon.EnsureRunning/SpawnDetached")
		}
	})

	t.Run("enabled: EnsureRunning is attempted", func(t *testing.T) {
		root := newRoot(t)

		ensureDaemonRunning(root, selfPath, ipc.State{DaemonEnabled: true}, testClock())

		matches, err := filepath.Glob(filepath.Join(paths.Of(root).Logs, "qompack-*.log"))
		require.NoError(t, err)
		require.NotEmpty(t, matches, "runtime.daemon.enabled=true must still let EnsureRunning run")

		b, err := os.ReadFile(matches[0])
		require.NoError(t, err)
		require.Contains(t, string(b), "spawn failed",
			"EnsureRunning must have reached SpawnDetached, which fails fast against a nonexistent self")
	})
}

// TestRawExtras_ResolvesTheSubagentNameClientSide pins the client-side half of the subagent-name
// seam. hookio.Event.Extra is tagged `json:"-"`, so ReadEvent fills it in THIS process and
// ipc.EncodeRequest then drops it — a daemon-side reader of e.Extra sees an empty map in
// production. rawExtras is therefore the only place the agent's name can be resolved from, and it
// must resolve it here, where Extra is real, rather than forward a key nothing downstream can see.
//
// The table pins the resolution rules the wire format depends on: three keys in priority order, a
// value that is not a non-empty JSON string falls through to the next key, and all three missing
// emits the pre-existing {"subagent":true} byte-for-byte so an older daemon's decodeSubagent is
// untouched.
func TestRawExtras_ResolvesTheSubagentNameClientSide(t *testing.T) {
	raw := func(pairs map[string]string) map[string]json.RawMessage {
		if pairs == nil {
			return nil
		}
		m := make(map[string]json.RawMessage, len(pairs))
		for k, v := range pairs {
			m[k] = json.RawMessage(v)
		}
		return m
	}

	cases := []struct {
		name  string
		extra map[string]json.RawMessage
		want  string
	}{
		{name: "no extras at all", extra: nil, want: `{"subagent":true}`},
		{name: "no name key among the extras", extra: raw(map[string]string{"unrelated": `"x"`}), want: `{"subagent":true}`},
		{name: "subagent_type wins", extra: raw(map[string]string{"subagent_type": `"code-reviewer"`}), want: `{"subagent":true,"agent":"code-reviewer"}`},
		{name: "agent_name is second", extra: raw(map[string]string{"agent_name": `"explorer"`}), want: `{"subagent":true,"agent":"explorer"}`},
		{name: "agent is third", extra: raw(map[string]string{"agent": `"planner"`}), want: `{"subagent":true,"agent":"planner"}`},
		{
			name:  "priority order is subagent_type, agent_name, agent",
			extra: raw(map[string]string{"agent": `"third"`, "agent_name": `"second"`, "subagent_type": `"first"`}),
			want:  `{"subagent":true,"agent":"first"}`,
		},
		{
			name:  "a numeric value falls through to the next key",
			extra: raw(map[string]string{"subagent_type": `7`, "agent_name": `"explorer"`}),
			want:  `{"subagent":true,"agent":"explorer"}`,
		},
		{
			name:  "an object value falls through to the next key",
			extra: raw(map[string]string{"subagent_type": `{"name":"nested"}`, "agent": `"planner"`}),
			want:  `{"subagent":true,"agent":"planner"}`,
		},
		{
			name:  "an empty string is not a name",
			extra: raw(map[string]string{"subagent_type": `""`, "agent_name": `""`, "agent": `""`}),
			want:  `{"subagent":true}`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rawExtras(ipc.OpObserveStop, []string{"--subagent"}, hookio.Event{Extra: c.extra})
			require.JSONEq(t, c.want, string(got))
		})
	}

	// The no-name shape must be byte-identical to what shipped, not merely JSON-equal: the wire
	// format is what an already-installed daemon parses.
	require.Equal(t, `{"subagent":true}`,
		string(rawExtras(ipc.OpObserveStop, []string{"--subagent"}, hookio.Event{})))

	// Nothing changes for a Stop without the flag, or for another op.
	require.Nil(t, rawExtras(ipc.OpObserveStop, nil, hookio.Event{Extra: raw(map[string]string{"subagent_type": `"code-reviewer"`})}))
	require.Nil(t, rawExtras(ipc.OpObserveTool, []string{"--subagent"}, hookio.Event{Extra: raw(map[string]string{"subagent_type": `"code-reviewer"`})}))
}

// TestHooks_SubagentNameReachesTheSpooledRequest is the end-to-end half of the same seam: a real
// SubagentStop payload dispatched through the real hook body, whose unclaimed subagent_type key
// hookio.ReadEvent routes into Event.Extra, must reach Request.Raw — the one field the transport
// does carry.
func TestHooks_SubagentNameReachesTheSpooledRequest(t *testing.T) {
	dir := t.TempDir()
	payload, err := json.Marshal(map[string]any{
		"session_id":    "s-subagent-named",
		"cwd":           dir,
		"subagent_type": "code-reviewer",
	})
	require.NoError(t, err)

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), argvFor("observe stop", "--subagent"), Env{
		Getenv:  noEnv,
		Stdin:   bytes.NewReader(payload),
		Clock:   testClock(),
		HomeDir: t.TempDir(),
	}, &out, &errw)
	require.Equal(t, ExitOK, code, "stderr=%s", errw.String())

	req := onlySpooledRequest(t, dir)
	require.JSONEq(t, `{"subagent":true,"agent":"code-reviewer"}`, string(req.Raw))
}
