package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/pluginmanifest"
)

// modBundleSources is the hooks module a --mods bundle ships, beside hooks.json.
var modBundleSources = []string{
	"hooks/compact.ts",
	"hooks/format.ts",
	"hooks/register.ts",
	"hooks/target.gen.ts",
	"hooks/wire.gen.ts",
}

// TestAssembleBundle_ModsLayout pins `bundle --mods`: the default bundle's files plus the five mod
// sources under hooks/, a hooks.json that names ./register.ts, and that target's own
// target.gen.ts. Tests are never shipped; mod-test copies them in when it runs them.
func TestAssembleBundle_ModsLayout(t *testing.T) {
	for _, tgt := range []bundleTarget{{"linux", "arm64"}, {"windows", "amd64"}} {
		t.Run(tgt.OS+"-"+tgt.Arch, func(t *testing.T) {
			plainAsm := testAssembly(t, t.TempDir())
			plainDir, _, err := plainAsm.assemble(tgt)
			require.NoError(t, err)

			asm := testAssembly(t, t.TempDir())
			asm.mods = true
			dir, id, err := asm.assemble(tgt)
			require.NoError(t, err)

			want := append(listBundleTree(t, plainDir), modBundleSources...)
			sort.Strings(want)
			require.Equal(t, want, listBundleTree(t, dir))
			for _, p := range listBundleTree(t, dir) {
				require.False(t, strings.HasSuffix(p, ".test.ts"), "%s: tests are never shipped", p)
			}

			listed := make(map[string]bool, len(id.Files))
			for _, f := range id.Files {
				listed[f.Path] = true
			}
			for _, p := range modBundleSources {
				require.True(t, listed[p], "BUNDLE.json lists %s", p)
			}

			var hooks map[string]json.RawMessage
			readBundleJSON(t, dir, "hooks/hooks.json", &hooks)
			require.JSONEq(t, `["./register.ts"]`, string(hooks["modules"]))

			target, err := os.ReadFile(filepath.Join(dir, "hooks", pluginmanifest.ModTargetFile))
			require.NoError(t, err)
			require.Equal(t, string(pluginmanifest.ModTargetSource(tgt.OS)), string(target))
			require.Contains(t, string(target), "'"+bundleBinPath(tgt.OS)+"'",
				"target.gen.ts names the binary this bundle ships")
		})
	}
}

// TestTaskBundle_AcceptsMods proves the flag is defined without building anything: a bad --target
// fails after flag parsing, so the error names the target and not an undefined flag.
func TestTaskBundle_AcceptsMods(t *testing.T) {
	err := taskBundle([]string{"--mods", "--target", "plan9/amd64"})
	require.Error(t, err)
	require.ErrorIs(t, err, errUsage)
	require.Contains(t, err.Error(), "not one of the six release targets")
}
