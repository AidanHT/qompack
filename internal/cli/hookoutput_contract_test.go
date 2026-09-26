package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pluginmanifest"
)

// The host's side of the hook contract, pinned against what Claude Code actually accepts.
//
// C1.12 is the defect these tests exist for. Claude Code 2.1.280 rejected `qompack checkpoint`'s
// output with "Hook JSON output validation failed — hookSpecificOutput.hookEventName: expected one
// of …" because no hookSpecificOutput variant exists for PreCompact, and then replayed the whole
// rejection, focus text included, into the post-compaction context. Every other suite in this tree
// checked only that a hook's stdout PARSED; none checked it against the host's schema, which is how
// a rejected shape shipped (plans/sdd/V6-closeout/packaging/evidence/c1.12-host-rejection.txt).
//
// The schema is read from testdata/host/hooks-output-schema.json, a hand transcription of the hooks
// reference, deliberately independent of internal/hookio's own table: a mistake in either is caught
// by the other.

// hostHookSchema is testdata/host/hooks-output-schema.json.
type hostHookSchema struct {
	Universal []string                   `json:"universal"`
	Events    map[string]hostEventSchema `json:"events"`
}

// hostEventSchema is one event's accepted output.
type hostEventSchema struct {
	TopLevel []string `json:"topLevel"`
	// HookSpecificOutput is nil when the event has no hookSpecificOutput variant at all.
	HookSpecificOutput []string `json:"hookSpecificOutput"`
	Discarded          []string `json:"discarded"`
}

const hostHookSchemaPath = "../../testdata/host/hooks-output-schema.json"

func loadHostHookSchema(t *testing.T) hostHookSchema {
	t.Helper()
	raw, err := os.ReadFile(hostHookSchemaPath)
	require.NoError(t, err)
	var s hostHookSchema
	require.NoError(t, json.Unmarshal(raw, &s))
	return s
}

// hostSchemaViolations returns every way stdout breaks the documented schema for event, or nil.
//
// Beyond the host's own validation it also reports a field the host DISCARDS for that event: such a
// field reaches nobody, so emitting it is at best noise in the debug log.
func hostSchemaViolations(s hostHookSchema, event string, stdout []byte) []string {
	ev, ok := s.Events[event]
	if !ok {
		return []string{"no documented schema for event " + event}
	}
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return nil // empty stdout on exit 0 is success with no output
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &doc); err != nil {
		return []string{"stdout is not one JSON object: " + err.Error()}
	}
	allowed := map[string]bool{}
	for _, k := range s.Universal {
		allowed[k] = true
	}
	for _, k := range ev.TopLevel {
		allowed[k] = true
	}
	discarded := map[string]bool{}
	for _, k := range ev.Discarded {
		discarded[k] = true
	}

	var bad []string
	for key, val := range doc {
		switch {
		case key == "hookSpecificOutput":
			bad = append(bad, hsoViolations(ev, event, val)...)
		case !allowed[key]:
			bad = append(bad, fmt.Sprintf("top-level field %q is not accepted for %s", key, event))
		case discarded[key]:
			bad = append(bad, fmt.Sprintf("top-level field %q is discarded by the host for %s", key, event))
		}
	}
	sort.Strings(bad)
	return bad
}

func hsoViolations(ev hostEventSchema, event string, raw json.RawMessage) []string {
	if ev.HookSpecificOutput == nil {
		return []string{fmt.Sprintf("%s has no hookSpecificOutput variant; the host rejects one", event)}
	}
	var hso map[string]json.RawMessage
	if err := json.Unmarshal(raw, &hso); err != nil {
		return []string{"hookSpecificOutput is not an object: " + err.Error()}
	}
	var bad []string
	var name string
	if err := json.Unmarshal(hso["hookEventName"], &name); err != nil || name != event {
		bad = append(bad, fmt.Sprintf("hookSpecificOutput.hookEventName is %s, want %q", hso["hookEventName"], event))
	}
	fields := map[string]bool{"hookEventName": true}
	for _, k := range ev.HookSpecificOutput {
		fields[k] = true
	}
	for key := range hso {
		if !fields[key] {
			bad = append(bad, fmt.Sprintf("hookSpecificOutput.%s is not accepted for %s", key, event))
		}
	}
	return bad
}

// entryArgv turns a manifest entry point's subcommand into Dispatch argv.
func entryArgv(e pluginmanifest.HookEntryPoint) []string {
	return append([]string{"qompack"}, strings.Fields(e.Subcommand)...)
}

// entryPayload is a well-formed payload for one host event, rooted at root.
func entryPayload(t *testing.T, event, root string) []byte {
	t.Helper()
	m := map[string]any{
		"hook_event_name": event,
		"session_id":      "sess-host-contract",
		"cwd":             root,
		"transcript_path": filepath.Join(root, "transcript.jsonl"),
	}
	switch event {
	case "PostToolUse":
		m["tool_name"] = "Read"
		m["tool_use_id"] = "toolu_host_contract"
		m["tool_input"] = map[string]any{"file_path": filepath.Join(root, "a.txt")}
		m["tool_response"] = map[string]any{"content": "alpha"}
	case "UserPromptSubmit":
		m["prompt"] = "keep the config port at 8443"
	case "SessionStart":
		m["source"] = "compact"
	case "PreCompact":
		m["trigger"] = "manual"
		m["custom_instructions"] = nil
	case "Stop", "SubagentStop":
		m["stop_hook_active"] = false
	case "SessionEnd":
		m["reason"] = "other"
	}
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return b
}

// replyDaemon is a real ipc endpoint at a project's resolved address that answers every Reply
// request through reply and acknowledges every fire-and-forget one. It is not a daemon: it stands
// in for whatever a resident daemon of ANY version might send back, which is exactly the input the
// hook client must never pass to the host unchecked.
func replyDaemon(t *testing.T, root string, reply func(ipc.Request) *hookio.Output) {
	t.Helper()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	srv, err := ipc.NewServer(addr, logging.Nop(), obs.New(testClock()), 0)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx, func(_ context.Context, req ipc.Request) ipc.Response {
			return ipc.Response{OK: true, Output: reply(req)}
		})
	}()
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
		<-done
	})
	require.Eventually(t, func() bool { return ipc.Probe(addr, selfTestProbeTimeout) },
		5*time.Second, 10*time.Millisecond, "the reply daemon's endpoint never came up")
}

// replyProject lays out a project whose state record gives the hook client generous deadlines, so
// a loaded machine cannot turn a slow dial into a spool and a spurious "{}".
func replyProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	st := ipc.StateFromConfig(config.Defaults())
	st.ConnectDeadlineMs = 5000
	st.AckDeadlineMs = 5000
	require.NoError(t, ipc.WriteState(root, st))
	return root
}

// runEntryPoint drives one manifest entry point through Dispatch and returns its stdout.
func runEntryPoint(t *testing.T, root string, e pluginmanifest.HookEntryPoint) []byte {
	t.Helper()
	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), entryArgv(e), Env{
		Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": root}),
		Stdin:   bytes.NewReader(entryPayload(t, e.Event, root)),
		Clock:   testClock(),
		HomeDir: t.TempDir(),
	}, &out, &errw)
	require.Equal(t, ExitOK, code, "%s: a hook always exits 0; stderr=%s", e.Subcommand, errw.String())
	return out.Bytes()
}

// maximalReply is every Output field set at once, naming the given event: the worst a daemon could
// hand the client.
func maximalReply(event string) *hookio.Output {
	f, tr := false, true
	return &hookio.Output{
		Continue:       &f,
		SuppressOutput: &tr,
		SystemMessage:  "qompack host-contract banner",
		HookSpecificOutput: &hookio.HSO{
			HookEventName:      event,
			AdditionalContext:  "context for " + event,
			CustomInstructions: "focus instructions for " + event,
		},
	}
}

// TestHookOutput_PreCompactCarriesNoHookSpecificOutput is C1.12's regression test: the checkpoint
// hook answering a PreCompact must not hand the host a hookSpecificOutput, because the host has no
// PreCompact variant and rejects the whole response. The reply is the one internal/daemon's
// checkpoint seam returned verbatim until C1.18 retired its focus instruction — which is what a
// daemon of that build, still resident after an upgrade, sends today — so this is a real path, not
// a contrived one.
func TestHookOutput_PreCompactCarriesNoHookSpecificOutput(t *testing.T) {
	schema := loadHostHookSchema(t)
	root := replyProject(t)
	shipped := hookio.Output{HookSpecificOutput: &hookio.HSO{
		HookEventName:      hookio.EventPreCompact,
		CustomInstructions: "Encode what a competent engineer with no session history would get wrong.",
	}}
	replyDaemon(t, root, func(ipc.Request) *hookio.Output { return &shipped })

	e := pluginmanifest.HookEntryPoint{Event: "PreCompact", Subcommand: "checkpoint"}
	stdout := runEntryPoint(t, root, e)

	require.Empty(t, hostSchemaViolations(schema, "PreCompact", stdout),
		"`qompack checkpoint` must never emit output Claude Code rejects; stdout=%s", stdout)
	require.Equal(t, "{}\n", string(stdout),
		"PreCompact accepts no hookSpecificOutput and discards systemMessage, so the only conforming "+
			"non-empty answer is the empty object")
}

// TestHookOutput_EveryEntryPointConformsToTheHostSchema drives all seven installed entry points —
// read from internal/pluginmanifest, so a new hook cannot escape this — against a daemon that
// answers with every field set, and pins each stdout byte for byte (the golden) and against the
// documented schema (the contract).
func TestHookOutput_EveryEntryPointConformsToTheHostSchema(t *testing.T) {
	schema := loadHostHookSchema(t)

	// The golden: what each entry point writes when the daemon says everything it could. PostToolUse,
	// Stop and SubagentStop are fire-and-forget (an ACK, never a reply), so the client always
	// answers them with the empty object. SessionStart and UserPromptSubmit keep the two fields
	// the host acts on. PreCompact and SessionEnd keep nothing: the host rejects or discards all
	// of it.
	want := map[string]string{
		"PostToolUse": "{}\n",
		"UserPromptSubmit": `{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit",` +
			`"additionalContext":"context for UserPromptSubmit"},"systemMessage":"qompack host-contract banner"}` + "\n",
		"SessionStart": `{"hookSpecificOutput":{"hookEventName":"SessionStart",` +
			`"additionalContext":"context for SessionStart"},"systemMessage":"qompack host-contract banner"}` + "\n",
		"PreCompact":   "{}\n",
		"Stop":         "{}\n",
		"SubagentStop": "{}\n",
		"SessionEnd":   "{}\n",
	}

	entries := pluginmanifest.HookEntryPoints()
	require.Len(t, entries, len(want), "every installed entry point needs a golden here")
	for _, e := range entries {
		t.Run(e.Event, func(t *testing.T) {
			root := replyProject(t)
			event := e.Event
			replyDaemon(t, root, func(ipc.Request) *hookio.Output { return maximalReply(event) })

			stdout := runEntryPoint(t, root, e)
			require.Empty(t, hostSchemaViolations(schema, e.Event, stdout),
				"%s (`qompack %s`) wrote output the host rejects or discards: %s", e.Event, e.Subcommand, stdout)
			golden, ok := want[e.Event]
			require.True(t, ok, "no golden for %s", e.Event)
			require.Equal(t, golden, string(stdout), "%s stdout", e.Event)
		})
	}
}

// TestHookOutput_ForeignEventNameIsDropped covers a reply whose hookSpecificOutput names a
// different event than the one the host is running — an older or confused daemon. The host would
// reject it, so nothing of it may reach stdout.
func TestHookOutput_ForeignEventNameIsDropped(t *testing.T) {
	schema := loadHostHookSchema(t)
	root := replyProject(t)
	replyDaemon(t, root, func(ipc.Request) *hookio.Output {
		return &hookio.Output{HookSpecificOutput: &hookio.HSO{
			HookEventName: "PreCompact", AdditionalContext: "misrouted",
		}}
	})

	e := pluginmanifest.HookEntryPoint{Event: "SessionStart", Subcommand: "session-start"}
	stdout := runEntryPoint(t, root, e)
	require.Empty(t, hostSchemaViolations(schema, "SessionStart", stdout), "stdout=%s", stdout)
	require.Equal(t, "{}\n", string(stdout))
}

// TestHostHookSchema_RejectsTheShippedDefect is the negative control for hostSchemaViolations: the
// exact bytes 2.1.280 rejected must be reported, or the contract tests above prove nothing.
func TestHostHookSchema_RejectsTheShippedDefect(t *testing.T) {
	schema := loadHostHookSchema(t)
	rejected := []byte(`{"hookSpecificOutput":{"hookEventName":"PreCompact","customInstructions":"x"}}`)
	require.NotEmpty(t, hostSchemaViolations(schema, "PreCompact", rejected))

	// And the other direction: a documented SessionStart answer passes.
	ok := []byte(`{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"x"}}`)
	require.Empty(t, hostSchemaViolations(schema, "SessionStart", ok))
	require.NotEmpty(t, hostSchemaViolations(schema, "SessionEnd", []byte(`{"systemMessage":"x"}`)),
		"a field the host discards for SessionEnd is reported too")
}

// TestHookEvent_MatchesTheManifest ties hookEvent to internal/pluginmanifest: every installed entry
// point's subcommand, dispatched as hooks.go binds it, conforms its output for the event the
// manifest registers it under. For the fire-and-forget entry points no golden above could notice a
// wrong mapping (their stdout is "{}" either way), so this asserts the mapping itself.
func TestHookEvent_MatchesTheManifest(t *testing.T) {
	ops := map[string]ipc.Op{
		"observe tool":   ipc.OpObserveTool,
		"observe prompt": ipc.OpObservePrompt,
		"observe stop":   ipc.OpObserveStop,
		"session-start":  ipc.OpSessionStart,
		"checkpoint":     ipc.OpCheckpoint,
		"flush":          ipc.OpFlush,
	}
	registered := map[string]bool{}
	for _, c := range hookCmds() {
		registered[c.Name] = true
	}
	for _, e := range pluginmanifest.HookEntryPoints() {
		words := strings.Fields(e.Subcommand)
		name, args := words[0], words[1:]
		if len(words) > 1 && !strings.HasPrefix(words[1], "-") {
			name, args = words[0]+" "+words[1], words[2:]
		}
		require.True(t, registered[name], "manifest entry %q names no registered hook command", e.Subcommand)
		op, ok := ops[name]
		require.True(t, ok, "no op recorded here for %q", name)
		require.Equal(t, e.Event, hookEvent(op, args), "`qompack %s` answers %s", e.Subcommand, e.Event)
	}
	require.Empty(t, hookEvent(ipc.OpStatus, nil), "a non-hook op has no host event")
}

// TestHookOutput_OverTheHostCapIsLoud covers the host limit the schema check above cannot see: a
// hook's additionalContext or systemMessage "capped at 10,000 characters", over which "Claude Code
// saves the output to a file in the session directory and replaces it with the file path and a
// preview of up to the first 2,000 characters" and "doesn't ask Claude to read the file" (hooks
// reference, 2026-09-22). The host accepts such a response, so nothing fails; the rehydration
// payload, budgeted in tokens up to runtime.rehydrate.maxTokens, simply stops reaching Claude
// beyond its first 2,000 characters (observed on 2.1.280:
// plans/sdd/V6-closeout/packaging/evidence/review/f2-live-host-cap-probe/README.txt). The field is
// passed through unchanged — what an over-cap injection should become is an owner decision, recorded
// in that README — and the degradation is made loud, which is the one thing a hook can always do
// about it.
func TestHookOutput_OverTheHostCapIsLoud(t *testing.T) {
	schema := loadHostHookSchema(t)
	e := pluginmanifest.HookEntryPoint{Event: "SessionStart", Subcommand: "session-start"}

	for _, tc := range []struct {
		name  string
		chars int
		loud  bool
	}{
		{"at the cap", hookio.HostFieldMaxChars, false},
		{"one over", hookio.HostFieldMaxChars + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := replyProject(t)
			injected := strings.Repeat("r", tc.chars)
			reply := hookio.SessionStartOutput(injected)
			replyDaemon(t, root, func(ipc.Request) *hookio.Output { return &reply })

			stdout := runEntryPoint(t, root, e)
			require.Empty(t, hostSchemaViolations(schema, e.Event, stdout), "stdout conforms either way")
			var out hookio.Output
			require.NoError(t, json.Unmarshal(stdout, &out))
			require.NotNil(t, out.HookSpecificOutput)
			require.Equal(t, injected, out.HookSpecificOutput.AdditionalContext,
				"the injection is passed through whole; the host, not the hook, cuts it")

			loud, err := os.ReadFile(filepath.Join(paths.Of(root).Logs, "LOUD.log"))
			if !tc.loud {
				if err == nil {
					require.NotContains(t, string(loud), "host's per-field cap", "a field at the cap is delivered whole")
				}
				return
			}
			require.NoError(t, err, "an over-cap injection must leave a LOUD.log line")
			require.Contains(t, string(loud), "host's per-field cap")
			require.Contains(t, string(loud), "hookSpecificOutput.additionalContext")
			require.Contains(t, string(loud), fmt.Sprint(tc.chars))
			require.NotContains(t, string(loud), injected[:64], "the Loud names sizes, never content")
		})
	}
}
