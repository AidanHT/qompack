package pluginmanifest_test

import (
	"bytes"
	"encoding/json"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/pluginmanifest"
)

// wantAsyncEvents is the V7 F4 set: the three ACK-only capture hooks, whose replies carry nothing
// the model reads. Every other event must stay synchronous: UserPromptSubmit and SessionStart
// deliver their output in the same turn, PreCompact must seal before the summary, and `-p`
// teardown kills an async SessionEnd.
var wantAsyncEvents = map[string]bool{"PostToolUse": true, "Stop": true, "SubagentStop": true}

const hooksJSONPath = "plugin/hooks/hooks.json"

// asyncMember is the exact bytes the option adds to one hook entry: a member after timeout, at the
// entry's 12-space indent.
const asyncMember = ",\n            \"async\": true"

// decodeHookEntries returns event -> the raw keys of its single hook entry.
func decodeHookEntries(t *testing.T, raw []byte) map[string]map[string]json.RawMessage {
	t.Helper()
	var doc struct {
		Hooks map[string][]struct {
			Hooks []map[string]json.RawMessage `json:"hooks"`
		} `json:"hooks"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	out := make(map[string]map[string]json.RawMessage, len(doc.Hooks))
	for event, groups := range doc.Hooks {
		require.Len(t, groups, 1, event)
		require.Len(t, groups[0].Hooks, 1, event)
		out[event] = groups[0].Hooks[0]
	}
	return out
}

// TestManifest_DefaultRenderingHasNoAsyncKey: the field is omitempty and the option is off by
// default, so the shipped bytes (and the golden, and the committed plugin/) carry no `async` key.
func TestManifest_DefaultRenderingHasNoAsyncKey(t *testing.T) {
	for _, goos := range manifestTargets {
		files, err := pluginmanifest.ForTarget(testVersion, goos).Files()
		require.NoError(t, err)
		require.NotContains(t, string(files[hooksJSONPath]), `"async"`, goos)
	}
	files, err := pluginmanifest.Default(testVersion).Files()
	require.NoError(t, err)
	require.NotContains(t, string(files[hooksJSONPath]), `"async"`)
}

// TestWithAsyncHooks_MarksExactlyTheACKOnlyCaptureHooks pins the option-on rendering: exactly
// PostToolUse, Stop and SubagentStop carry "async": true, every other event carries no async key,
// and with the async keys removed every file equals the default rendering byte for byte.
func TestWithAsyncHooks_MarksExactlyTheACKOnlyCaptureHooks(t *testing.T) {
	for _, goos := range manifestTargets {
		def, err := pluginmanifest.ForTarget(testVersion, goos).Files()
		require.NoError(t, err)
		on, err := pluginmanifest.WithAsyncHooks(pluginmanifest.ForTarget(testVersion, goos)).Files()
		require.NoError(t, err)

		require.Len(t, on, len(def), goos)
		for rel, b := range def {
			if rel == hooksJSONPath {
				continue
			}
			require.Equal(t, string(b), string(on[rel]), "%s/%s: only hooks.json may change", goos, rel)
		}

		entries := decodeHookEntries(t, on[hooksJSONPath])
		require.Len(t, entries, 7, goos)
		var gotAsync []string
		for event, keys := range entries {
			v, has := keys["async"]
			if !wantAsyncEvents[event] {
				require.False(t, has, "%s/%s must stay synchronous and carry no async key", goos, event)
				continue
			}
			require.True(t, has, "%s/%s must carry async", goos, event)
			require.Equal(t, "true", string(v), "%s/%s", goos, event)
			gotAsync = append(gotAsync, event)
		}
		sort.Strings(gotAsync)
		require.Equal(t, []string{"PostToolUse", "Stop", "SubagentStop"}, gotAsync, goos)

		// Removing exactly the three `,\n "async": true` members must give the default bytes, so
		// the key sits after timeout and nothing else in the file moved.
		require.Equal(t, 3, bytes.Count(on[hooksJSONPath], []byte(asyncMember)), goos)
		stripped := bytes.ReplaceAll(on[hooksJSONPath], []byte(asyncMember), nil)
		require.Equal(t, string(def[hooksJSONPath]), string(stripped), goos)
	}
}

// TestWithAsyncHooks_DoesNotMutateItsInput: the transform returns a new manifest; the value it was
// given (and so any caller still holding the default rendering) keeps rendering today's bytes.
func TestWithAsyncHooks_DoesNotMutateItsInput(t *testing.T) {
	m := pluginmanifest.Default(testVersion)
	before, err := m.Files()
	require.NoError(t, err)
	_ = pluginmanifest.WithAsyncHooks(m)
	after, err := m.Files()
	require.NoError(t, err)
	require.Equal(t, before, after)
}

// wantAsyncPromptEvents is the V7 F4b set: UserPromptSubmit alone, whose reply is empty in this
// build (no reply producer is wired; test/guards' asyncprompt guard keeps it so).
var wantAsyncPromptEvents = map[string]bool{"UserPromptSubmit": true}

// requireAsyncExactly asserts that the hooks.json in files marks exactly the events in want async,
// that every other event carries no async key, and that with the async members removed the file
// equals def's hooks.json byte for byte. Every other file must equal def's.
func requireAsyncExactly(t *testing.T, label string, def, files map[string][]byte, want map[string]bool) {
	t.Helper()
	require.Len(t, files, len(def), label)
	for rel, b := range def {
		if rel == hooksJSONPath {
			continue
		}
		require.Equal(t, string(b), string(files[rel]), "%s/%s: only hooks.json may change", label, rel)
	}
	entries := decodeHookEntries(t, files[hooksJSONPath])
	require.Len(t, entries, 7, label)
	for event, keys := range entries {
		v, has := keys["async"]
		if !want[event] {
			require.False(t, has, "%s/%s must stay synchronous and carry no async key", label, event)
			continue
		}
		require.True(t, has, "%s/%s must carry async", label, event)
		require.Equal(t, "true", string(v), "%s/%s", label, event)
	}
	require.Equal(t, len(want), bytes.Count(files[hooksJSONPath], []byte(asyncMember)), label)
	stripped := bytes.ReplaceAll(files[hooksJSONPath], []byte(asyncMember), nil)
	require.Equal(t, string(def[hooksJSONPath]), string(stripped), label)
}

// TestWithAsyncPrompt_MarksOnlyUserPromptSubmit pins the F4b rendering: UserPromptSubmit alone
// carries "async": true. SessionStart stays synchronous (its output is the rehydration), and so do
// the capture hooks, which are WithAsyncHooks's to mark, not this transform's.
func TestWithAsyncPrompt_MarksOnlyUserPromptSubmit(t *testing.T) {
	for _, goos := range manifestTargets {
		def, err := pluginmanifest.ForTarget(testVersion, goos).Files()
		require.NoError(t, err)
		on, err := pluginmanifest.WithAsyncPrompt(pluginmanifest.ForTarget(testVersion, goos)).Files()
		require.NoError(t, err)
		requireAsyncExactly(t, goos, def, on, wantAsyncPromptEvents)
	}
}

// TestAsyncTransforms_ComposeInBothOrders: WithAsyncHooks used to assign every entry's flag, so
// applied after WithAsyncPrompt it cleared UserPromptSubmit's. Each transform now only sets the
// flags it owns, so either order renders the union, and the two orders render the same bytes.
func TestAsyncTransforms_ComposeInBothOrders(t *testing.T) {
	union := map[string]bool{}
	for e := range wantAsyncEvents {
		union[e] = true
	}
	for e := range wantAsyncPromptEvents {
		union[e] = true
	}
	for _, goos := range manifestTargets {
		def, err := pluginmanifest.ForTarget(testVersion, goos).Files()
		require.NoError(t, err)
		hooksThenPrompt, err := pluginmanifest.WithAsyncPrompt(
			pluginmanifest.WithAsyncHooks(pluginmanifest.ForTarget(testVersion, goos))).Files()
		require.NoError(t, err)
		promptThenHooks, err := pluginmanifest.WithAsyncHooks(
			pluginmanifest.WithAsyncPrompt(pluginmanifest.ForTarget(testVersion, goos))).Files()
		require.NoError(t, err)

		requireAsyncExactly(t, goos+" hooks then prompt", def, hooksThenPrompt, union)
		requireAsyncExactly(t, goos+" prompt then hooks", def, promptThenHooks, union)
		require.Equal(t, hooksThenPrompt, promptThenHooks, goos)
	}
}

// TestAsyncTransforms_AreIdempotent: applying either transform twice renders what applying it once
// does, so a caller that composes defensively cannot change the output.
func TestAsyncTransforms_AreIdempotent(t *testing.T) {
	m := pluginmanifest.Default(testVersion)
	for name, tr := range map[string]func(pluginmanifest.Manifest) pluginmanifest.Manifest{
		"WithAsyncHooks":  pluginmanifest.WithAsyncHooks,
		"WithAsyncPrompt": pluginmanifest.WithAsyncPrompt,
	} {
		once, err := tr(m).Files()
		require.NoError(t, err)
		twice, err := tr(tr(m)).Files()
		require.NoError(t, err)
		require.Equal(t, once, twice, name)
	}
}

// TestWithAsyncPrompt_DoesNotMutateItsInput mirrors the WithAsyncHooks case: the value passed in
// keeps rendering today's bytes.
func TestWithAsyncPrompt_DoesNotMutateItsInput(t *testing.T) {
	m := pluginmanifest.Default(testVersion)
	before, err := m.Files()
	require.NoError(t, err)
	_ = pluginmanifest.WithAsyncPrompt(m)
	after, err := m.Files()
	require.NoError(t, err)
	require.Equal(t, before, after)
}
