// V1 cross-component integration tests, e2e half (plans/V1-VERIFY-foundation-and-contracts.md §4).
//
// These exist because SP-01's packages now coexist on one branch. Every test here crosses at least
// three package boundaries and none of them could have been written inside a single package's
// tests. They are authored at the V1 checkpoint and are a permanent part of the suite from now on.
//
// Wave 0 delivers no store, no observer, no checkpointer and no retrieval layer, so the
// "hook event -> store -> tombstone -> retrieval round-trip" seam does not exist yet and is
// deliberately NOT simulated here. What is tested is the set of seams that do exist at wave 0:
// the hook wire format -> CLI dispatch -> config load -> layout -> hook log (IT-1), the five-layer
// config precedence reaching observable hook behaviour (IT-2), and the plugin manifest driving the
// real binary (IT-5). The round-trip test belongs to V2, when the store lands.
package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/stretchr/testify/require"
)

// contractsHookioInput is the frozen hookio payload corpus, relative to this package's directory.
// These are the SAME bytes internal/hookio parses in its own unit tests; feeding them to a real
// process is what makes IT-1 an integration test rather than a second copy of I1.
const contractsHookioInput = "../../testdata/golden/contracts/hookio/input"

// v1Call is one invocation in IT-1's lifecycle sequence.
type v1Call struct {
	// argv is the subcommand as the plugin manifest spells it.
	argv []string
	// fixture is the basename under contractsHookioInput, or "" when the payload is derived.
	fixture string
	// derive, when non-nil, mutates the decoded fixture before it is fed in. It exists for the
	// second PostToolUse call, which §4 IT-1 requires to be a Bash tool use and for which no
	// frozen fixture exists.
	derive func(map[string]any)
	// logHook is the HOST EVENT NAME this call answers (SessionStart, PostToolUse, ...) — not the
	// subcommand. `observe stop --subagent` and `observe stop` both carry "Stop" here (the two
	// host events share one subcommand; SP-08 owns distinguishing them at the observer layer).
	logHook string
	// wantStdout is the exact response the host must receive, trailing newline included. Compared
	// exactly for every call except SessionStart, whose additionalContext carries a live §12.1
	// sentinel token that varies run to run (checked separately, via
	// hookSpecificOutput.HookEventName — fix round 1, Minor M-2).
	wantStdout string
}

// v1Lifecycle is IT-1's eight invocations in the order a real session produces them.
//
// The order is not cosmetic. SessionStart precedes everything, PreCompact arrives after work has
// happened, and SessionEnd closes. A hook that only works when run first — because it depends on a
// layout another hook created — would pass a six-independent-runs test and fail this one.
var v1Lifecycle = []v1Call{
	{
		argv: []string{"session-start"}, fixture: "session_start_startup", logHook: "SessionStart",
		wantStdout: `{"hookSpecificOutput":{"hookEventName":"SessionStart"}}` + "\n",
	},
	{argv: []string{"observe", "prompt"}, fixture: "user_prompt_submit", logHook: "UserPromptSubmit", wantStdout: "{}\n"},
	{argv: []string{"observe", "tool"}, fixture: "post_tool_use", logHook: "PostToolUse", wantStdout: "{}\n"},
	{argv: []string{"observe", "tool"}, fixture: "post_tool_use", derive: asBashToolUse, logHook: "PostToolUse", wantStdout: "{}\n"},
	{argv: []string{"observe", "stop"}, fixture: "stop", logHook: "Stop", wantStdout: "{}\n"},
	{
		// PreCompact's hookSpecificOutput is populated only by an actual svc.PreCompact seam
		// (SP-10), absent from a wave-1-only build, so checkpoint answers the minimal response —
		// same as every other fire-and-forget/no-seam-yet call in this lifecycle.
		argv: []string{"checkpoint"}, fixture: "pre_compact", logHook: "PreCompact", wantStdout: "{}\n",
	},
	{argv: []string{"flush"}, fixture: "session_end", logHook: "SessionEnd", wantStdout: "{}\n"},
	{argv: []string{"observe", "stop", "--subagent"}, fixture: "subagent_stop", logHook: "Stop", wantStdout: "{}\n"},
}

// bashCommand is the command string the derived Bash tool use carries. It is asserted absent from
// the project's logs (v1Secrets, below), so it has to be a string that could not appear there by
// coincidence.
const bashCommand = "go test ./internal/auth/... -run TestRefreshRejectsExpired"

// asBashToolUse turns the frozen FileRead payload into a Bash one. §4 IT-1 asks for two PostToolUse
// calls with different tools; only the FileRead shape is frozen, so the Bash variant is derived
// from it rather than invented, which keeps every field except the four that must differ identical
// to the frozen fixture.
func asBashToolUse(m map[string]any) {
	m["tool_name"] = "Bash"
	m["tool_use_id"] = "toolu_09Z8Y7X6W5V4U3T2S1R0Q9P8"
	m["tool_input"] = map[string]any{"command": bashCommand}
	m["tool_response"] = map[string]any{"stdout": "ok  \tgithub.com/qompack/qompack/internal/auth\t0.21s\n"}
}

// v1Secrets is every substring that MUST NOT reach .qompack/logs/** — the day log, LOUD.log, and
// hook-quiet-*.jsonl (internal/cli/hookclient.go's logQuiet). This is IT-1's own self-described
// "load-bearing half" (restored, fix round 1, Important I-4, after being deleted with no
// replacement when the observation log it originally targeted, hooks-*.jsonl, was removed): none
// of the surviving log files is a content store, so a regression that started writing payload text
// into any of them would otherwise be invisible to this suite. The WAL under spool/ is deliberately
// EXCLUDED — it is the durable content store the whole system exists to build, and Requests
// legitimately carry Event.Prompt/ToolInput/ToolResponse verbatim.
var v1Secrets = []string{
	"Fix the intermittent 500s on POST /api/session/refresh.", // the prompt, verbatim
	"src/auth.ts", // a file path from tool_input
	"numLines",    // a key from the FileRead tool_response body
	bashCommand,   // the derived Bash command
	"github.com/qompack/qompack/internal/auth", // the derived Bash tool_response body
	"/home/u/.claude/projects/proj/sess.jsonl", // transcript_path
	"/home/u/proj", // the payload cwd
}

// v1LogsCorpus returns the concatenated contents of every file under root's .qompack/logs/ — the
// day log(s), LOUD.log, and any hook-quiet-*.jsonl — for the payload-leak assertion below.
func v1LogsCorpus(t *testing.T, root string) string {
	t.Helper()
	logsDir := paths.Of(root).Logs
	var sb strings.Builder
	_ = filepath.WalkDir(logsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, readErr := os.ReadFile(p)
		require.NoError(t, readErr, "reading %s", p)
		sb.Write(b)
		sb.WriteByte('\n')
		return nil
	})
	return sb.String()
}

// TestV1_HookLifecycleThroughRealBinary is §4 IT-1.
//
// Crosses pluginmanifest -> cmd/qompack -> cli -> hookio -> config -> paths -> logging: the frozen
// wire payloads go in on stdin, a real process handles them, and the assertions are on what the
// host saw (exit code, stdout) and on what the process left behind (.qompack/).
//
// The payloads are fed VERBATIM — their cwd is the frozen "/home/u/proj", which does not exist on
// this machine. That is deliberate: QOMPACK_PROJECT_ROOT is §3.3's first resolution step and must
// beat the payload cwd, so if root resolution ever regressed to preferring the payload the hook
// log would not appear in the temp project at all and every assertion below would fail loudly.
func TestV1_HookLifecycleThroughRealBinary(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t, testutil.WithGit(), testutil.WithClock(testutil.Epoch))
	// session-start, below, brings up a real detached daemon; shut it down before this test's own
	// t.TempDir() cleanup runs, or a still-open log file handle can make that cleanup fail on
	// Windows (open-file delete semantics) — see faultinject_test.go's e2eShutdownIfReachable.
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })

	env := map[string]string{
		"QOMPACK_PROJECT_ROOT": p.Root,
		"HOME":                 p.Home(),
		"USERPROFILE":          p.Home(),
	}

	// SP-05 replaces the six hook bodies with thin ipc clients (internal/cli/hookclient.go):
	// session-start is now the designated daemon starter (§2.4/§5.21), so the FIRST call below
	// brings up a real, resident daemon that every later call in this sequence talks to. That
	// means the exact response bytes are no longer a CLI-side constant: SessionStart's
	// additionalContext now carries a live §12.1 sentinel token the daemon mints unconditionally
	// (handleSessionStart mints and emits it whenever the mode may act, with no wave-3 seam
	// required). PreCompact's hookSpecificOutput, by contrast, is populated only by an actual
	// svc.PreCompact seam (SP-10) — absent from a wave-1-only build — so a bare `checkpoint` call
	// here answers with the minimal "{}" response, exactly like every fire-and-forget hook. What
	// stays true, and what this loop still proves end to end across pluginmanifest -> cmd/qompack
	// -> cli -> ipc -> daemon -> hookio, is that every call in the lifecycle exits 0 with exactly
	// one well-formed hookio.Output, and that SessionStart specifically still answers through
	// hookSpecificOutput naming itself.
	for i, call := range v1Lifecycle {
		payload := v1Payload(t, call)

		stdout, stderr, code := Run(t, bin, call.argv, payload, env)

		require.Equal(t, 0, code,
			"call %d (%v): §2.3 permits a hook no outcome but exit 0\nstdout:\n%s\nstderr:\n%s",
			i+1, call.argv, stdout, stderr)
		requireLoneOutput(t, stdout)

		var out hookio.Output
		require.NoError(t, json.Unmarshal(stdout, &out), "call %d (%v): stdout:\n%s", i+1, call.argv, stdout)
		if call.logHook == "SessionStart" {
			require.NotNil(t, out.HookSpecificOutput, "call %d (%v) answers through hookSpecificOutput", i+1, call.argv)
			require.Equal(t, call.logHook, out.HookSpecificOutput.HookEventName)
		} else {
			require.Equal(t, call.wantStdout, string(stdout), "call %d (%v): exact response shape", i+1, call.argv)
		}
	}

	// No payload content anywhere in the project's logs (fix round 1, Important I-4).
	corpus := v1LogsCorpus(t, p.Root)
	for _, secret := range v1Secrets {
		require.NotContains(t, corpus, secret,
			"the project's logs are an observability record, not a content store (§7.4): %q leaked", secret)
	}

	// The layout self-ignores, so a project that commits its working tree cannot commit the store.
	gitignore, err := os.ReadFile(filepath.Join(paths.Of(p.Root).Dot, ".gitignore"))
	require.NoError(t, err)
	require.Equal(t, "*\n", string(gitignore), ".qompack/.gitignore must self-ignore (§3.3)")
}

// TestV1_ConfigPrecedenceReachesHookBehaviour is §4 IT-2, updated for SP-05's architecture.
//
// Crosses config's five layers -> `config print --provenance`. IT-2 originally also crossed into a
// bare hook subcommand's own read-limit decision, because wave-0's hook body called config.Load
// directly. SP-05 deliberately removes that seam from the hot path (task-6-spec.md's hookclient.go
// skeleton: "ipc.ReadState(root, config.Defaults()) — never config.Load on the hot path"): a bare
// hook invocation against a project no daemon has ever touched reads under config.Defaults()
// alone, precedence or no precedence, until a daemon has actually run and written run/state.bin.
// The first sub-test below still proves the five-layer precedence chain resolves correctly and
// reaches the tool an operator actually runs (`config print --provenance`); the second proves the
// new seam directly: config precedence now reaches the hot path THROUGH a live daemon's state.bin,
// not through a hook's own config.Load.
func TestV1_ConfigPrecedenceReachesHookBehaviour(t *testing.T) {
	bin := Build(t)

	const (
		userLimit    = 4096  // ~/.qompack/config.json
		projectLimit = 8192  // <root>/.qompack/config.json — must win over the user layer
		envLimit     = 16384 // QOMPACK_RUNTIME__HOTPATH__MAXPAYLOADBYTES — must win over the project file
	)

	cases := []struct {
		name       string
		extraEnv   map[string]string
		extraArgs  []string
		wantOrigin string // provenance origin for runtime.hotPath.maxPayloadBytes
	}{
		{name: "project_file_wins_over_user_file", wantOrigin: "project"},
		{
			name:       "env_beats_both_files",
			extraEnv:   map[string]string{"QOMPACK_RUNTIME__HOTPATH__MAXPAYLOADBYTES": "16384"},
			wantOrigin: "env",
		},
		{
			name:       "flag_beats_everything",
			extraArgs:  []string{"--set", "runtime.hotPath.maxPayloadBytes=32768"},
			wantOrigin: "flag",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := v1LimitProject(t, projectLimit, userLimit)
			env := v1BaseEnv(p)
			for k, v := range tc.extraEnv {
				env[k] = v
			}

			out, perr, pcode := Run(t, bin, append([]string{"config", "print", "--provenance"}, tc.extraArgs...), nil, env)
			require.Equal(t, 0, pcode, "stderr:\n%s", perr)
			require.Contains(t, v1ProvenanceLine(t, string(out), "maxPayloadBytes"), tc.wantOrigin)
		})
	}

	// A bare hook invocation against a project no daemon has ever touched must read under
	// config.Defaults() alone, never the project's own maxPayloadBytes: this is the new hot-path
	// invariant task-6-spec.md's skeleton mandates (hookclient.go: "ipc.ReadState(root,
	// config.Defaults()) — one 32-byte read, never config.Load"), proven end to end through the
	// real binary. Fix round 1, Important I-5 replaced a tautological, racy sub-test here (it
	// asserted run/state.bin's absence — which does not establish where the read-limit decision
	// came from, and races the very lazy-spawn the same hook triggers on its own connect failure)
	// with two payloads sized relative to the DEFAULT limit's own *4 read margin
	// (hookio.ReadEvent(stdin, int64(st.MaxPayloadBytes)*4)): if the project's own, much smaller
	// projectLimit (8192) were still being consulted, BOTH payloads below — sized around
	// ~4 MiB — would fail to parse identically, so the "just under" case passing is what actually
	// distinguishes "reads the default" from "reads the project config", not merely "exits 0".
	readLimitBoundary := config.Defaults().Runtime.HotPath.MaxPayloadBytes * 4

	t.Run("bare_hook_spools_a_payload_just_under_the_default_read_limit", func(t *testing.T) {
		p := v1LimitProject(t, projectLimit, userLimit)
		payload := v1PayloadOfSize(t, p.Root, readLimitBoundary-256)

		stdout, stderr, code := Run(t, bin, []string{"observe", "tool"}, payload, v1BaseEnv(p))
		require.Equal(t, 0, code, "stderr:\n%s", stderr)
		require.Equal(t, "{}\n", string(stdout))

		files, err := ipc.SpoolFiles(paths.Of(p.Root).Spool)
		require.NoError(t, err)
		require.Len(t, files, 1,
			"a payload just under the DEFAULT read limit must parse and reach the spool step (no daemon reachable)")
		require.Equal(t, 1, v1CountSpoolLines(t, files[0]), "exactly one request must have been spooled")
	})

	t.Run("bare_hook_rejects_a_payload_just_over_the_default_read_limit", func(t *testing.T) {
		p := v1LimitProject(t, projectLimit, userLimit)
		payload := v1PayloadOfSize(t, p.Root, readLimitBoundary+256)

		stdout, stderr, code := Run(t, bin, []string{"observe", "tool"}, payload, v1BaseEnv(p))
		require.Equal(t, 0, code, "stderr:\n%s", stderr)
		require.Equal(t, "{}\n", string(stdout))

		files, err := ipc.SpoolFiles(paths.Of(p.Root).Spool)
		require.NoError(t, err)
		require.Empty(t, files,
			"a payload over the DEFAULT read limit must never reach the spool step at all")
	})

	// Restores the original bootstrap-clamp property in its new shape (fix round 1, Important
	// I-5): a payload far larger than any real limit in play must still never crash, never hang,
	// and always exit 0 — the same "clamp before doing anything expensive" guarantee the deleted
	// 2 MiB sub-test pinned, recalibrated to the new, larger effective boundary (~4 MiB, not
	// ~1 MiB: the *4 read margin above did not exist in the wave-0 body this test originally
	// targeted).
	t.Run("bare_hook_never_hangs_or_crashes_on_a_grossly_oversized_payload", func(t *testing.T) {
		p := v1LimitProject(t, projectLimit, userLimit)
		huge := v1PayloadOfSize(t, p.Root, readLimitBoundary*2)

		stdout, stderr, code := Run(t, bin, []string{"observe", "tool"}, huge, v1BaseEnv(p))
		require.Equal(t, 0, code, "stderr:\n%s", stderr)
		require.Equal(t, "{}\n", string(stdout))
	})
}

// v1CountSpoolLines counts non-empty NDJSON lines in the spool file at p.
func v1CountSpoolLines(t *testing.T, p string) int {
	t.Helper()
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	n := 0
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// hooksManifest is the shape of plugin/hooks/hooks.json this test reads. It is declared here rather
// than imported from internal/pluginmanifest on purpose: IT-5 must read the COMMITTED bundle the
// way Claude Code reads it, so sharing the generator's types would let a change to both sides pass
// unnoticed.
type hooksManifest struct {
	Hooks map[string][]struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
		} `json:"hooks"`
	} `json:"hooks"`
}

// v1ManifestPayload maps each manifest event to the frozen payload that answers it.
var v1ManifestPayload = map[string]string{
	"PostToolUse":      "post_tool_use",
	"UserPromptSubmit": "user_prompt_submit",
	"SessionStart":     "session_start_startup",
	"PreCompact":       "pre_compact",
	"Stop":             "stop",
	"SubagentStop":     "subagent_stop",
	"SessionEnd":       "session_end",
}

// TestV1_PluginManifestCommandsExecuteAgainstRealBinary is §4 IT-5.
//
// Crosses pluginmanifest -> plugin/hooks/hooks.json -> cli's dispatch table -> hookio. This is the
// seam that breaks silently when a subcommand is renamed: the manifest is generated data, the
// dispatch table is code, and nothing else in the tree executes one against the other. Every
// command string is run VERBATIM, argv-split the way a shell would split it.
func TestV1_PluginManifestCommandsExecuteAgainstRealBinary(t *testing.T) {
	bin := Build(t)

	// Stage the binary where ${CLAUDE_PLUGIN_ROOT} would put it, so the substituted command string
	// is a path that actually resolves.
	pluginRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(pluginRoot, "bin"), 0o755))
	staged := filepath.Join(pluginRoot, "bin", filepath.Base(bin))
	src, err := os.ReadFile(bin)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(staged, src, 0o755))

	raw, err := os.ReadFile("../../plugin/hooks/hooks.json")
	require.NoError(t, err)
	var m hooksManifest
	require.NoError(t, json.Unmarshal(raw, &m))
	require.Len(t, m.Hooks, 7, "§7.5 declares seven hook entries")

	registered := v1RegisteredSubcommands(t, bin)
	p := testutil.NewProject(t)
	// SessionStart, among the seven manifest events below, brings up a real detached daemon whose
	// self-spawn path is the STAGED binary — Windows refuses to delete a running process's own
	// executable, which would otherwise make this test's own t.TempDir() cleanup of pluginRoot
	// fail with "Access is denied". Shut it down first.
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })

	for event, entries := range m.Hooks {
		t.Run(event, func(t *testing.T) {
			fixture, ok := v1ManifestPayload[event]
			require.True(t, ok, "no frozen payload for manifest event %q", event)
			payload := v1ReadFixture(t, fixture)

			if event == "PostToolUse" {
				require.Equal(t, "*", entries[0].Matcher,
					"PostToolUse is the only entry that matches every tool")
			}

			for _, entry := range entries {
				for _, h := range entry.Hooks {
					require.Equal(t, "command", h.Type)

					argv := v1SplitCommand(t, h.Command, pluginRoot, staged)

					// The seam: every subcommand the manifest names must be one cli registers.
					sub := v1SubcommandOf(argv)
					require.Contains(t, registered, sub,
						"manifest event %q invokes %q, which cli.All() does not register", event, sub)

					start := time.Now()
					stdout, stderr, code := Run(t, staged, argv, payload, v1BaseEnv(p))
					elapsed := time.Since(start)

					require.Equal(t, 0, code, "stderr:\n%s", stderr)
					requireLoneOutput(t, stdout)

					// Recorded as headroom, not gated tightly: this is B-D (hook_wall), which §2.4
					// says is reported and never gated because process creation is the host's cost.
					budget := time.Duration(h.Timeout) * time.Second
					t.Logf("B-D %s: %v wall against a %v manifest timeout (%.2f%% used)",
						event, elapsed.Round(time.Millisecond), budget,
						100*float64(elapsed)/float64(budget))
					require.Less(t, elapsed, budget,
						"%s exceeded its own declared manifest timeout", event)
				}
			}
		})
	}
}

// requireLoneOutput asserts stdout is exactly one hookio.Output and nothing else. A hook that
// printed a stray line before or after its JSON would still unmarshal with a plain Unmarshal on
// the first object; the host would not.
func requireLoneOutput(t *testing.T, stdout []byte) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(stdout))

	var first map[string]any
	require.NoError(t, dec.Decode(&first), "stdout must be a JSON object:\n%s", stdout)

	var rest any
	err := dec.Decode(&rest)
	require.True(t, errors.Is(err, io.EOF), "stdout carried trailing content after the response:\n%s", stdout)
}

// v1Payload renders one IT-1 call's stdin bytes.
func v1Payload(t *testing.T, c v1Call) []byte {
	t.Helper()
	raw := v1ReadFixture(t, c.fixture)
	if c.derive == nil {
		return raw
	}
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	c.derive(m)
	out, err := json.Marshal(m)
	require.NoError(t, err)
	return out
}

// v1ReadFixture reads one frozen hookio payload.
func v1ReadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(contractsHookioInput, name+".json"))
	require.NoError(t, err, "frozen hookio fixture %q", name)
	return b
}

// v1LimitProject builds a project whose project and user config layers both set
// runtime.hotPath.maxPayloadBytes, to different values.
func v1LimitProject(t *testing.T, project, user int) *testutil.Project {
	t.Helper()
	p := testutil.NewProject(t, testutil.WithConfig(v1LimitJSON(project)))
	// A bare `observe tool` call lazily spawns a detached daemon (fire-and-forget — it never
	// waits for it); shutting it down before this project's own t.TempDir() cleanup runs avoids
	// racing a still-starting-up daemon against directory removal.
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })

	globalDir := paths.Global(p.Home())
	require.NoError(t, os.MkdirAll(globalDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "config.json"), []byte(v1LimitJSON(user)), 0o600))
	return p
}

// v1LimitJSON renders a config document setting only the payload limit.
func v1LimitJSON(limit int) string {
	b, _ := json.Marshal(map[string]any{
		"runtime": map[string]any{"hotPath": map[string]any{"maxPayloadBytes": limit}},
	})
	return string(b)
}

// v1BaseEnv is the environment that confines a spawned binary to a Project's temp tree.
func v1BaseEnv(p *testutil.Project) map[string]string {
	return map[string]string{
		"QOMPACK_PROJECT_ROOT": p.Root,
		"HOME":                 p.Home(),
		"USERPROFILE":          p.Home(),
	}
}

// v1PayloadOfSize renders a valid PostToolUse payload padded to exactly n bytes, so a size-limit
// assertion is exact rather than approximate.
func v1PayloadOfSize(t *testing.T, cwd string, n int) []byte {
	t.Helper()
	build := func(pad int) []byte {
		b, err := json.Marshal(map[string]any{
			"hook_event_name": "PostToolUse",
			"session_id":      "sess_01J8ZQ5R7N3K2M4P6T8V0X2Y4A",
			"cwd":             cwd,
			"tool_name":       "Bash",
			"tool_use_id":     "toolu_01A2B3C4D5E6F7G8H9J0K1L2",
			"tool_input":      map[string]any{"command": "true"},
			"tool_response":   map[string]any{"stdout": strings.Repeat("x", pad)},
		})
		require.NoError(t, err)
		return b
	}
	base := len(build(0))
	require.Less(t, base, n, "requested payload smaller than the envelope")
	out := build(n - base)
	require.Len(t, out, n)
	return out
}

// v1ProvenanceLine returns the single `config print --provenance` line mentioning key.
func v1ProvenanceLine(t *testing.T, out, key string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, key) {
			return line
		}
	}
	t.Fatalf("no provenance line mentions %q in:\n%s", key, out)
	return ""
}

// v1RegisteredSubcommands returns every subcommand name the built binary advertises in its usage.
// Reading it out of --help rather than out of cli.All() is deliberate: IT-5 is asserting that the
// SHIPPED binary and the SHIPPED manifest agree, and a test that consulted the library would still
// pass if main.go stopped registering something.
func v1RegisteredSubcommands(t *testing.T, bin string) string {
	t.Helper()
	stdout, stderr, code := Run(t, bin, []string{"--help"}, nil, nil)
	require.Equal(t, 0, code, "stderr:\n%s", stderr)
	return string(stdout) + string(stderr)
}

// v1SplitCommand argv-splits a manifest command string the way a shell would, after substituting
// ${CLAUDE_PLUGIN_ROOT}. The manifest's own commands contain no quoting or escaping, and
// pluginmanifest's TestManifest_NoUnexpandedTemplateLeftovers keeps it that way, so a plain
// whitespace split is faithful; the assertion below fails loudly if that ever stops being true.
func v1SplitCommand(t *testing.T, command, pluginRoot, staged string) []string {
	t.Helper()
	require.NotContains(t, command, `"`, "manifest commands must not need shell quoting")
	require.NotContains(t, command, `'`, "manifest commands must not need shell quoting")

	expanded := strings.ReplaceAll(command, "${CLAUDE_PLUGIN_ROOT}", pluginRoot)
	fields := strings.Fields(expanded)
	require.NotEmpty(t, fields)
	require.Equal(t, filepath.ToSlash(filepath.Join(pluginRoot, "bin", "qompack")),
		filepath.ToSlash(fields[0]),
		"every manifest command must invoke ${CLAUDE_PLUGIN_ROOT}/bin/qompack")

	// fields[0] is the manifest's extension-less path; the staged executable is what we can run.
	return fields[1:]
}

// v1SubcommandOf reconstructs the dispatch name from an argv tail, dropping flags. cli registers
// two-word names ("observe tool"), so the first two non-flag words are what has to match.
func v1SubcommandOf(argv []string) string {
	var words []string
	for _, a := range argv {
		if strings.HasPrefix(a, "-") {
			continue
		}
		words = append(words, a)
		if len(words) == 2 {
			break
		}
	}
	return strings.Join(words, " ")
}
