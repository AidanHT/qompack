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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
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
	// logHook is the value the observation line's "hook" field must carry.
	//
	// It is the HOST EVENT NAME, not the subcommand: internal/cli/hooks.go registers each entry
	// point with runHook("<EventName>", ...). Note that `observe stop --subagent` therefore logs
	// "Stop" as well — SP-01 maps both stop events onto one subcommand (see the Summary string on
	// the "observe stop" command), and distinguishing them is SP-08's job when it gives these
	// hooks real bodies.
	logHook string
	// wantStdout is the exact response the host must receive, trailing newline included.
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
		argv: []string{"checkpoint"}, fixture: "pre_compact", logHook: "PreCompact",
		wantStdout: `{"hookSpecificOutput":{"hookEventName":"PreCompact"}}` + "\n",
	},
	{argv: []string{"flush"}, fixture: "session_end", logHook: "SessionEnd", wantStdout: "{}\n"},
	{argv: []string{"observe", "stop", "--subagent"}, fixture: "subagent_stop", logHook: "Stop", wantStdout: "{}\n"},
}

// bashCommand is the command string the derived Bash tool use carries. It is asserted absent from
// the hook log, so it has to be a string that could not appear there by coincidence.
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

// v1Secrets is every substring that MUST NOT reach .qompack/logs/hooks-*.jsonl.
//
// This is the load-bearing half of IT-1. §7.4 makes the hook log an observability record, not a
// content store: it carries the shape of what happened (which hook, which session, how many bytes)
// and nothing a user typed or a tool returned. A regression that started logging payload text
// would be invisible to every unit test in internal/cli — which asserts on a log IT wrote — and
// would leak prompts and file contents into a file nobody thinks of as sensitive.
var v1Secrets = []string{
	"Fix the intermittent 500s on POST /api/session/refresh.", // the prompt, verbatim
	"src/auth.ts", // a file path from tool_input
	"numLines",    // a key from the FileRead tool_response body
	bashCommand,   // the derived Bash command
	"github.com/qompack/qompack/internal/auth", // the derived Bash tool_response body
	"/home/u/.claude/projects/proj/sess.jsonl", // transcript_path
	"/home/u/proj", // the payload cwd
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

	env := map[string]string{
		"QOMPACK_PROJECT_ROOT": p.Root,
		"HOME":                 p.Home(),
		"USERPROFILE":          p.Home(),
	}

	for i, call := range v1Lifecycle {
		payload := v1Payload(t, call)

		stdout, stderr, code := Run(t, bin, call.argv, payload, env)

		require.Equal(t, 0, code,
			"call %d (%v): §2.3 permits a hook no outcome but exit 0\nstdout:\n%s\nstderr:\n%s",
			i+1, call.argv, stdout, stderr)
		require.Equal(t, call.wantStdout, string(stdout),
			"call %d (%v): exact response shape", i+1, call.argv)
		requireLoneOutput(t, stdout)
	}

	// Exactly one observation line per invocation, in call order.
	lines := v1HookLogLines(t, p.Root)
	require.Len(t, lines, len(v1Lifecycle),
		"eight hook invocations must append exactly eight observation lines")

	for i, call := range v1Lifecycle {
		var rec struct {
			TS        int64  `json:"ts"`
			Hook      string `json:"hook"`
			SessionID string `json:"session_id"`
			Bytes     int    `json:"bytes"`
			Truncated bool   `json:"truncated"`
		}
		require.NoError(t, json.Unmarshal([]byte(lines[i]), &rec), "line %d: %s", i+1, lines[i])

		require.Equal(t, call.logHook, rec.Hook, "line %d records the host event name", i+1)
		require.Equal(t, "sess_01J8ZQ5R7N3K2M4P6T8V0X2Y4A", rec.SessionID,
			"line %d echoes the session id the payload carried", i+1)
		require.Positive(t, rec.Bytes, "line %d must record the payload size", i+1)
		require.False(t, rec.Truncated, "line %d: none of these payloads is near the 1 MiB limit", i+1)
	}

	// No payload content anywhere in the log.
	joined := strings.Join(lines, "\n")
	for _, secret := range v1Secrets {
		require.NotContains(t, joined, secret,
			"the hook log is an observability record, not a content store (§7.4): %q leaked", secret)
	}

	// The layout self-ignores, so a project that commits its working tree cannot commit the store.
	gitignore, err := os.ReadFile(filepath.Join(paths.Of(p.Root).Dot, ".gitignore"))
	require.NoError(t, err)
	require.Equal(t, "*\n", string(gitignore), ".qompack/.gitignore must self-ignore (§3.3)")
}

// TestV1_ConfigPrecedenceReachesHookBehaviour is §4 IT-2.
//
// Crosses config's five layers -> cli's bootstrap ordering -> hookio's limit -> logging. The point
// is not that config.Load resolves precedence — E4 proves that in isolation — but that the value
// it resolves actually reaches a decision a real process makes about a real payload, and that the
// bootstrap read happens under the DEFAULT limit before any config file is consulted.
func TestV1_ConfigPrecedenceReachesHookBehaviour(t *testing.T) {
	bin := Build(t)

	const (
		userLimit    = 4096  // ~/.qompack/config.json
		projectLimit = 8192  // <root>/.qompack/config.json — must win over the user layer
		envLimit     = 16384 // QOMPACK_RUNTIME__HOTPATH__MAXPAYLOADBYTES — must win over the project file
		flagLimit    = 32768 // --set — must win over everything
		payloadBytes = 12288 // 12 KiB: above the project limit, below the env and flag limits
	)

	cases := []struct {
		name          string
		extraEnv      map[string]string
		extraArgs     []string
		wantTruncated bool
		wantOrigin    string // provenance origin for runtime.hotPath.maxPayloadBytes
	}{
		{name: "project_file_wins_over_user_file", wantTruncated: true, wantOrigin: "project"},
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

			argv := append([]string{"observe", "tool"}, tc.extraArgs...)
			stdout, stderr, code := Run(t, bin, argv, v1PayloadOfSize(t, p.Root, payloadBytes), env)

			require.Equal(t, 0, code, "stderr:\n%s", stderr)
			require.Equal(t, "{}\n", string(stdout))

			lines := v1HookLogLines(t, p.Root)
			require.Len(t, lines, 1)
			require.Equal(t, tc.wantTruncated, v1Truncated(t, lines[0]),
				"effective limit must decide whether a %d-byte payload is over budget", payloadBytes)

			// §12: an over-budget payload is never silent.
			warned := strings.Contains(v1DayLog(t, p.Root), "hook payload exceeds the configured limit")
			require.Equal(t, tc.wantTruncated, warned,
				"a truncation must be logged at warn level, and a non-truncation must not be")

			// The same layer that decided the behaviour must be the one provenance names.
			out, perr, pcode := Run(t, bin, append([]string{"config", "print", "--provenance"}, tc.extraArgs...), nil, env)
			require.Equal(t, 0, pcode, "stderr:\n%s", perr)
			require.Contains(t, v1ProvenanceLine(t, string(out), "maxPayloadBytes"), tc.wantOrigin)
		})
	}

	// Fourth sub-case: the bootstrap read. §2.3's ordering problem is that the payload size limit
	// is a config key, config lives under the project root, and the project root comes out of the
	// payload that has not been read yet. cli breaks the cycle by reading stdin under the DEFAULT
	// limit first. A 2 MiB payload must therefore be clamped at 1 MiB and reported — never crash,
	// never hang, never exit non-zero.
	t.Run("bootstrap_limit_applies_before_any_config_file", func(t *testing.T) {
		p := v1LimitProject(t, projectLimit, userLimit)
		bootstrap := config.Defaults().Runtime.HotPath.MaxPayloadBytes
		huge := 2 << 20

		stdout, stderr, code := Run(t, bin, []string{"observe", "tool"}, v1PayloadOfSize(t, p.Root, huge), v1BaseEnv(p))
		require.Equal(t, 0, code, "stderr:\n%s", stderr)
		require.Equal(t, "{}\n", string(stdout))

		lines := v1HookLogLines(t, p.Root)
		require.Len(t, lines, 1)

		var rec struct {
			Bytes     int  `json:"bytes"`
			Truncated bool `json:"truncated"`
		}
		require.NoError(t, json.Unmarshal([]byte(lines[0]), &rec))
		require.True(t, rec.Truncated, "a payload over the bootstrap limit is truncated")
		require.Equal(t, bootstrap, rec.Bytes,
			"stdin is read under the DEFAULT %d-byte limit, not the configured one", bootstrap)
		require.Less(t, rec.Bytes, huge, "the recorded size must be the clamp, not the payload")
	})
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

// v1HookLogLines returns every observation line in the project, in file order.
func v1HookLogLines(t *testing.T, root string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(paths.Of(root).Logs, "hooks-*.jsonl"))
	require.NoError(t, err)

	var lines []string
	for _, m := range matches {
		b, readErr := os.ReadFile(m)
		require.NoError(t, readErr)
		for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

// v1DayLog returns the concatenated contents of every rotating day log in the project.
func v1DayLog(t *testing.T, root string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(paths.Of(root).Logs, "qompack-*.log"))
	require.NoError(t, err)

	var sb strings.Builder
	for _, m := range matches {
		b, readErr := os.ReadFile(m)
		require.NoError(t, readErr)
		sb.Write(b)
	}
	return sb.String()
}

// v1Truncated reports one observation line's truncated flag.
func v1Truncated(t *testing.T, line string) bool {
	t.Helper()
	var rec struct {
		Truncated bool `json:"truncated"`
	}
	require.NoError(t, json.Unmarshal([]byte(line), &rec), "line: %s", line)
	return rec.Truncated
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
