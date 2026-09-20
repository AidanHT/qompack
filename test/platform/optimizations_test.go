package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// gatedSwitches are the four runtime.migration leaves SP17-M7-02 requires to be OFF in a shipped
// build: a capability whose acceptance gate has not passed must be disabled, not merely undocumented
// (Qompack.md v1.5 §7.1 — "unknown host/schema environments report unsupported/degraded capability
// instead of guessing"; internal/config/migration.go's gate table is the mechanism).
//
// They are read out of the ASSEMBLED BINARY's own `config print --json`, not out of
// config.Defaults(), because a compiled-in default and a shipped default are different facts and
// only the second one is what a user gets.
var gatedSwitches = []string{
	"runtime.migration.replacement.newResult",
	"runtime.migration.experiments.enabled",
	"runtime.migration.capture.rawEvidence",
	"runtime.migration.publication.durableFrontier",
}

// TestPlatform_UnsupportedOptimizationsDisabled asserts the shipped bundle reports every gated
// optimization OFF in a fresh project.
func TestPlatform_UnsupportedOptimizationsDisabled(t *testing.T) {
	b := assembledBundle(t)
	rec := newRecord(t, "optimizations-disabled-fresh-project")

	p := newProject(t, plainRootName)
	env := restrictionEnv(p)

	cfg := configPrint(t, b, p, env)

	observed := map[string]any{}
	for _, key := range gatedSwitches {
		v, ok := getPath(cfg, key)
		require.True(t, ok, "`config print --json` must report %s", key)
		require.Equal(t, false, v, "%s must be false in a shipped build: its gate has not passed", key)
		observed[key] = v
	}

	rec.Outcome = OutcomeVerified
	rec.Reason = "every gated optimization is reported disabled by the shipped bundle"
	rec.Detail = fmt.Sprintf("`qompack config print --json` in a fresh project reports %v", observed)
	writeRecord(t, rec)
}

// TestPlatform_UnknownSettingsVersion drives the schema-version half of Qompack.md §7.1: a project
// configuration written by a LATER plugin than this build.
//
// The behaviour is not invented here, it is read out of internal/config/migration.go and pinned:
// applyVersionedSection resets the WHOLE runtime.migration block to defaults when the file declares
// a settingsVersion newer than MigrationSettingsVersion, rewrites the block's provenance to
// OriginDefault and records a Warning — deliberately wholesale rather than Validate's per-leaf
// fallback, because a per-leaf restore would keep every switch the newer file set while resetting
// only the version number, which is the opposite of "unknown future behaviour disabled".
//
// So this case asserts three things: the gated switches the newer file turned ON are OFF in the
// effective configuration; the declared version itself is back to this build's; and the hooks still
// exit 0 rather than treating an unreadable future config as a failure. What the binary REPORTS for
// the unknown version is recorded either way.
func TestPlatform_UnknownSettingsVersion(t *testing.T) {
	b := assembledBundle(t)
	rec := newRecord(t, "optimizations-unknown-settings-version")

	p := newProject(t, plainRootName)
	require.NoError(t, paths.EnsureLayout(paths.Of(p.Root)))

	// A config from the future: a newer block version, and every gated switch turned on.
	future := `{
  "runtime": {
    "migration": {
      "settingsVersion": 99,
      "capture": { "rawEvidence": true },
      "publication": { "durableFrontier": true },
      "replacement": { "newResult": true },
      "experiments": { "enabled": true }
    }
  }
}
`
	cfgPath := filepath.Join(paths.Of(p.Root).Dot, "config.json")
	require.NoError(t, os.WriteFile(paths.Long(cfgPath), []byte(future), 0o600),
		"writing the future project configuration")

	env := restrictionEnv(p)
	cfg := configPrint(t, b, p, env)

	observed := map[string]any{}
	for _, key := range gatedSwitches {
		v, ok := getPath(cfg, key)
		require.True(t, ok, "`config print --json` must report %s", key)
		require.Equal(t, false, v,
			"%s was set true by a configuration declaring settingsVersion 99; a build that cannot "+
				"honour that version must disable it, never guess", key)
		observed[key] = v
	}

	version, ok := getPath(cfg, "runtime.migration.settingsVersion")
	require.True(t, ok, "`config print --json` must report runtime.migration.settingsVersion")
	require.EqualValues(t, 1, version,
		"the whole runtime.migration block is reset to defaults, which includes its own version")

	// And the hooks are unaffected: an unreadable future configuration is a degradation, not a
	// failure (§13 invariant 6).
	stderr := runAllHooksExpectZero(t, b, p, env, "a configuration declaring settingsVersion 99")

	rec.Outcome = OutcomeVerified
	rec.Reason = "a configuration from a newer plugin has its whole runtime.migration block reset to defaults"
	rec.Detail = fmt.Sprintf(
		"project config declared runtime.migration.settingsVersion=99 with all four gated switches true; "+
			"the shipped binary reports settingsVersion=%v and %v, and all six hooks still exit 0 "+
			"(hook stderr %d bytes). The mechanism is internal/config/migration.go's "+
			"applyVersionedSection: a wholesale block reset plus a Warning, not a per-leaf fallback and "+
			"not a load error",
		version, observed, len(stderr))
	writeRecord(t, rec)
}

// configPrint runs `qompack config print --json` from the bundle and decodes it.
func configPrint(t *testing.T, b bundle, p project, env map[string]string) map[string]any {
	t.Helper()
	stdout, stderr, code := run(t, b.Bin, b.Dir, []string{"config", "print", "--json"}, nil, env)
	require.Equal(t, 0, code,
		"`config print --json` must succeed\nstdout:\n%s\nstderr:\n%s", stdout, stderr)

	var cfg map[string]any
	require.NoError(t, jsonUnmarshalTrimmed(stdout, &cfg),
		"`config print --json` must write one JSON document\nstdout:\n%s", stdout)
	return cfg
}

// getPath walks a dotted path through a decoded JSON document.
func getPath(doc map[string]any, dotted string) (any, bool) {
	cur := any(doc)
	for _, seg := range strings.Split(dotted, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}
