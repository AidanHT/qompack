package pluginmanifest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/pluginmanifest"
)

// TestForTargetWithMods_AddsTheModuleAndItsSources pins the mods rendering: the target's plugin
// tree, plus the five mod sources under hooks/ (the module path is relative to hooks.json), plus a
// `modules` entry naming register.ts. Nothing else changes, and no test file is shipped.
func TestForTargetWithMods_AddsTheModuleAndItsSources(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			plain, err := pluginmanifest.ForTarget("1.2.3", goos).Files()
			require.NoError(t, err)
			m, err := pluginmanifest.ForTargetWithMods("1.2.3", goos)
			require.NoError(t, err)
			files, err := m.Files()
			require.NoError(t, err)

			src, err := pluginmanifest.ModSources(goos)
			require.NoError(t, err)
			var extra []string
			for rel := range files {
				if _, ok := plain[rel]; !ok {
					extra = append(extra, rel)
				}
			}
			sort.Strings(extra)
			want := make([]string, 0, len(modSourceNames))
			for _, name := range modSourceNames {
				want = append(want, "plugin/hooks/"+name)
			}
			require.Equal(t, want, extra, "the mods rendering adds exactly the mod sources, under hooks/")
			for _, name := range modSourceNames {
				require.Equal(t, src[name], files["plugin/hooks/"+name], "%s is shipped as ModSources renders it", name)
			}

			for rel, b := range plain {
				if rel == "plugin/hooks/hooks.json" {
					continue
				}
				require.Equal(t, b, files[rel], "%s does not change with mods on", rel)
			}

			var hooks map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(files["plugin/hooks/hooks.json"], &hooks))
			require.JSONEq(t, `["./register.ts"]`, string(hooks["modules"]))
			var plainHooks map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(plain["plugin/hooks/hooks.json"], &plainHooks))
			require.JSONEq(t, string(plainHooks["hooks"]), string(hooks["hooks"]),
				"the classic hooks are the same with mods on")
			require.Len(t, hooks, 2, "hooks.json gains `modules` and nothing else")
		})
	}
}

// TestForTarget_HasNoModule is the default staying off: the committed and release renderings name
// no module and ship no mod source, so their bytes are what they were before mods existed.
func TestForTarget_HasNoModule(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		files, err := pluginmanifest.ForTarget("1.2.3", goos).Files()
		require.NoError(t, err)
		require.NotContains(t, string(files["plugin/hooks/hooks.json"]), "modules")
		for rel := range files {
			require.NotEqual(t, ".ts", filepath.Ext(rel), "%s: the default rendering ships no mod source", rel)
		}
	}
}

// TestForTargetWithMods_ValidateRoundTrip keeps Validate meaningful for a mods tree: the rendering
// written to disk validates clean, and a test file dropped beside the sources is reported as a file
// the generator did not produce.
func TestForTargetWithMods_ValidateRoundTrip(t *testing.T) {
	m, err := pluginmanifest.ForTargetWithMods("1.2.3", "windows")
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, pluginmanifest.Write(dir, m))
	require.Empty(t, pluginmanifest.Validate(dir, m))

	stray := filepath.Join(dir, "plugin", "hooks", "format.test.ts")
	require.NoError(t, os.WriteFile(stray, []byte("// not shipped\n"), 0o600))
	diffs := pluginmanifest.Validate(dir, m)
	require.Len(t, diffs, 1)
	require.Equal(t, "plugin/hooks/format.test.ts", diffs[0].Path)
	require.Equal(t, "not produced by the generator", diffs[0].Reason)
}

// TestFiles_RefusesAModuleItDoesNotShip guards the one way the mods rendering could name a module
// the plugin does not carry: hooks.json pointing at a file outside ModSources.
func TestFiles_RefusesAModuleItDoesNotShip(t *testing.T) {
	m, err := pluginmanifest.ForTargetWithMods("1.2.3", "linux")
	require.NoError(t, err)
	m.Hooks.Modules = []string{"./missing.ts"}
	_, err = m.Files()
	require.ErrorContains(t, err, "missing.ts")

	m, err = pluginmanifest.ForTargetWithMods("1.2.3", "linux")
	require.NoError(t, err)
	m.ModSources = map[string][]byte{"register.ts": []byte("x\n"), "hooks.json": []byte("{}\n")}
	_, err = m.Files()
	require.ErrorContains(t, err, "hooks.json")

	m.ModSources = map[string][]byte{"register.ts": []byte("x\n"), "../escape.ts": []byte("x\n")}
	_, err = m.Files()
	require.ErrorContains(t, err, "escape.ts")
}
