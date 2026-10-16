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

// TestWithAsyncHooks_ComposesWithMods: the async transform applied to a mods rendering keeps the
// hooks module (hooks.json still names ./register.ts and every mod source still ships) and marks
// exactly the three ACK-only capture hooks. With the async members removed, the bytes are the mods
// rendering's, so neither option drops or reorders the other's output.
func TestWithAsyncHooks_ComposesWithMods(t *testing.T) {
	for _, goos := range manifestTargets {
		mods, err := pluginmanifest.ForTargetWithMods(testVersion, goos)
		require.NoError(t, err)
		modFiles, err := mods.Files()
		require.NoError(t, err)
		both, err := pluginmanifest.WithAsyncHooks(mods).Files()
		require.NoError(t, err)

		require.Len(t, both, len(modFiles), goos)
		for rel, b := range modFiles {
			if rel == hooksJSONPath {
				continue
			}
			require.Equal(t, string(b), string(both[rel]), "%s/%s: only hooks.json may change", goos, rel)
		}
		for name := range mods.ModSources {
			require.Contains(t, both, "plugin/hooks/"+name, "%s: mod source %s ships", goos, name)
		}

		var doc struct {
			Modules []string `json:"modules"`
		}
		require.NoError(t, json.Unmarshal(both[hooksJSONPath], &doc))
		require.Equal(t, []string{pluginmanifest.ModuleEntry}, doc.Modules, goos)

		var gotAsync []string
		for event, keys := range decodeHookEntries(t, both[hooksJSONPath]) {
			v, has := keys["async"]
			if !wantAsyncEvents[event] {
				require.False(t, has, "%s/%s must stay synchronous", goos, event)
				continue
			}
			require.Equal(t, "true", string(v), "%s/%s", goos, event)
			gotAsync = append(gotAsync, event)
		}
		sort.Strings(gotAsync)
		require.Equal(t, []string{"PostToolUse", "Stop", "SubagentStop"}, gotAsync, goos)

		stripped := bytes.ReplaceAll(both[hooksJSONPath], []byte(asyncMember), nil)
		require.Equal(t, string(modFiles[hooksJSONPath]), string(stripped), goos)
	}
}
