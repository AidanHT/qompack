package config_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
)

// TestMigration_DefaultsAreOffExceptTheTestedAdapter pins Qompack.md v1.5 Appendix C's
// disposition: every gated switch defaults off, the hardwired manual-compact block is off, and the
// one tested injection adapter (SessionStart source=compact) is the only switch on.
func TestMigration_DefaultsAreOffExceptTheTestedAdapter(t *testing.T) {
	m := config.Defaults().Runtime.Migration
	require.Equal(t, config.MigrationSettingsVersion, m.SettingsVersion)
	require.Equal(t, config.MigrationCfg{
		SettingsVersion: 1,
		Capture:         config.MigrationCaptureCfg{RawEvidence: false},
		Publication:     config.MigrationPublicationCfg{DurableFrontier: false},
		Reinjection:     config.MigrationReinjectionCfg{SessionStartCompact: true},
		Replacement:     config.MigrationReplacementCfg{NewResult: false},
		Compaction:      config.MigrationCompactionCfg{AutomaticVeto: false, BlockManualCompact: false},
		Experiments:     config.MigrationExperimentsCfg{Enabled: false},
	}, m)
	require.Empty(t, config.Defaults().Validate(), "Defaults() must satisfy every migration rule")
}

// gatedSections are the config blocks a gated switch may live under. Both carry their own
// settingsVersion and are reset independently (migration.go): runtime.migration gates the shipped
// pipeline's capabilities, runtime.phase7 gates SP-16's optional refinements. A gate key outside
// both is a leaf nobody declared, which is what the build-gate table is for instead.
var gatedSections = []string{"runtime.migration.", "runtime.phase7."}

// TestMigrationGates_AllPendingInThisBuild is the gate ledger's tripwire: no owner has landed a
// gate, so every switch is refused. The commit that flips one must update this test deliberately,
// naming the gate it passed.
//
// The count is asserted so that ADDING a gate is deliberate too — SP-16 added five and had to
// come here to say so — and the prefix check is what keeps a gate from naming a key that is not
// under an independently versioned block.
func TestMigrationGates_AllPendingInThisBuild(t *testing.T) {
	gates := config.MigrationGates()
	require.Len(t, gates, 10)
	for _, g := range gates {
		require.False(t, g.Passed, "gate %q (%s) must still be pending", g.Key, g.Owner)
		require.NotEmpty(t, g.Owner, g.Key)
		require.NotEmpty(t, g.Gate, g.Key)
		require.True(t, inGatedSection(g.Key), "gate %q is not under a versioned block", g.Key)
	}
}

// inGatedSection reports whether key sits under one of the gatedSections.
func inGatedSection(key string) bool {
	for _, s := range gatedSections {
		if strings.HasPrefix(key, s) {
			return true
		}
	}
	return false
}

// TestMigrationGates_CoverEveryGatedLeaf walks every bool leaf under runtime.migration and checks
// that each is either in the gate table or on the explicit ungated list, so a switch added to
// MigrationCfg without a gate decision fails here rather than shipping enableable by accident.
func TestMigrationGates_CoverEveryGatedLeaf(t *testing.T) {
	ungated := map[string]string{
		"runtime.migration.reinjection.sessionStartCompact": "the tested adapter; a kill switch, not a gate",
		"runtime.migration.compaction.blockManualCompact":   "hardwired false by its own Validate rule",
	}
	gated := map[string]bool{}
	for _, g := range config.MigrationGates() {
		gated[g.Key] = true
	}
	var schema map[string]any
	require.NoError(t, json.Unmarshal(config.Defaults().JSONSchema(), &schema))
	for leaf, kind := range leafTypes(t, schema, "") {
		if !inGatedSection(leaf) || kind != "boolean" {
			continue
		}
		_, isGated := gated[leaf]
		_, isUngated := ungated[leaf]
		require.True(t, isGated != isUngated, "bool leaf %q must be exactly one of gated or explicitly ungated", leaf)
	}
	for k := range gated {
		require.Equal(t, "boolean", leafTypes(t, schema, "")[k], "gate %q must name a bool leaf", k)
	}
}

// TestMigrationBuildGates_LegacyImportIsPendingAndHasNoConfigLeaf pins SP-20 M1-04's gate.
//
// Two separate properties are asserted, and both matter. The gate is still pending, exactly like
// every entry in the config-switch table, so a build ships with legacy import and cutover
// unreachable. And its key is NOT a runtime.migration.* bool leaf: a build gate must not be
// turnable on by a config edit, which is the whole reason it lives in its own table rather than
// being smuggled into MigrationGates with a leaf nobody declared.
func TestMigrationBuildGates_LegacyImportIsPendingAndHasNoConfigLeaf(t *testing.T) {
	gates := config.MigrationBuildGates()
	require.Len(t, gates, 1)

	g := config.LegacyImportGate()
	require.Equal(t, config.LegacyImportGateKey, g.Key)
	require.Equal(t, gates[0], g)
	require.False(t, g.Passed, "legacy import/cutover must still be gated off in this build")
	require.NotEmpty(t, g.Owner)
	require.Contains(t, g.Gate, "T20-M1-08")

	require.False(t, strings.HasPrefix(g.Key, "runtime.migration."),
		"a build gate must not look like a config leaf")
	var schema map[string]any
	require.NoError(t, json.Unmarshal(config.Defaults().JSONSchema(), &schema))
	leaves := leafTypes(t, schema, "")
	_, isLeaf := leaves[g.Key]
	require.False(t, isLeaf, "the legacy-import gate must have no config leaf a file could set")

	// And it is not silently duplicated into the config-switch table, which would make
	// TestMigrationGates_CoverEveryGatedLeaf fail for a leaf that does not exist.
	for _, cg := range config.MigrationGates() {
		require.NotEqual(t, g.Key, cg.Key)
	}
}

// TestValidate_RefusesEveryPendingSwitch: a true value on any gated switch is a Violation naming
// that switch and its gate, and the hardwired manual-compact block is refused outright.
func TestValidate_RefusesEveryPendingSwitch(t *testing.T) {
	cases := []struct {
		key string
		set func(*config.Config)
	}{
		{"runtime.migration.capture.rawEvidence", func(c *config.Config) { c.Runtime.Migration.Capture.RawEvidence = true }},
		{"runtime.migration.publication.durableFrontier", func(c *config.Config) { c.Runtime.Migration.Publication.DurableFrontier = true }},
		{"runtime.migration.replacement.newResult", func(c *config.Config) { c.Runtime.Migration.Replacement.NewResult = true }},
		{"runtime.migration.compaction.automaticVeto", func(c *config.Config) { c.Runtime.Migration.Compaction.AutomaticVeto = true }},
		{"runtime.migration.experiments.enabled", func(c *config.Config) { c.Runtime.Migration.Experiments.Enabled = true }},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			cfg := config.Defaults()
			tc.set(&cfg)
			vs := cfg.Validate()
			require.Len(t, vs, 1)
			require.Equal(t, tc.key, vs[0].Key)
			require.Contains(t, vs[0].Message, "has not passed in this build")
			require.Equal(t, false, vs[0].Want)
		})
	}

	cfg := config.Defaults()
	cfg.Runtime.Migration.Compaction.BlockManualCompact = true
	vs := cfg.Validate()
	require.Len(t, vs, 1)
	require.Equal(t, "runtime.migration.compaction.blockManualCompact", vs[0].Key)
	require.Contains(t, vs[0].Message, "never blocked")

	// The kill switch is the one migration bool with no invalid state.
	cfg = config.Defaults()
	cfg.Runtime.Migration.Reinjection.SessionStartCompact = false
	require.Empty(t, cfg.Validate())
}

// TestLoad_PendingSwitchFallsBackWithWarning: through Load, a file that turns a gated switch on
// gets the default back and a warning, never the switch.
func TestLoad_PendingSwitchFallsBackWithWarning(t *testing.T) {
	env := baseEnv(t)
	writeConfigFile(t, env.ProjectRoot, `{"runtime":{"migration":{"replacement":{"newResult":true}}}}`)

	cfg, prov, warns, err := config.Load(env)
	require.NoError(t, err)
	require.False(t, cfg.Runtime.Migration.Replacement.NewResult)
	require.Equal(t, config.OriginDefault, prov["runtime.migration.replacement.newResult"].Origin)
	require.Equal(t, []string{"runtime.migration.replacement.newResult"}, warningKeys(warns))
	require.Contains(t, warns[0].Message, "using default")
}

// TestLoad_NewerSettingsVersionResetsTheWholeBlock is the "unknown future behaviour disabled" rule:
// a file written for a later plugin has its ENTIRE runtime.migration block reset — the switches it
// set that this build does know are not kept while only the version number is repaired — and a
// key this build does not know under the block is dropped as unknown, like any other.
func TestLoad_NewerSettingsVersionResetsTheWholeBlock(t *testing.T) {
	env := baseEnv(t)
	writeConfigFile(t, env.ProjectRoot, `{"runtime":{"migration":{
		"settingsVersion": 2,
		"reinjection": {"sessionStartCompact": false},
		"future": {"flag": true}
	}}}`)

	cfg, prov, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Equal(t, config.Defaults().Runtime.Migration, cfg.Runtime.Migration,
		"a newer settingsVersion resets the block wholesale, including switches this build knows")
	require.Equal(t, config.OriginDefault, prov["runtime.migration.reinjection.sessionStartCompact"].Origin)
	require.Equal(t, config.OriginDefault, prov["runtime.migration.settingsVersion"].Origin)

	keys := warningKeys(warns)
	require.ElementsMatch(t, []string{"runtime.migration.future", "runtime.migration"}, keys)
	for _, w := range warns {
		switch w.Key {
		case "runtime.migration":
			require.Contains(t, w.Message, "newer than this build understands")
			require.Contains(t, w.Location, "config.json:")
		case "runtime.migration.future":
			require.Contains(t, w.Message, "unknown key")
		}
	}
	require.Empty(t, cfg.Validate())
}

// TestLoad_OlderSettingsVersionFallsBackToCurrent: a version below this build's is not a newer
// format, just an invalid value — the leaf falls back and the switches the file set are kept.
func TestLoad_OlderSettingsVersionFallsBackToCurrent(t *testing.T) {
	env := baseEnv(t)
	writeConfigFile(t, env.ProjectRoot, `{"runtime":{"migration":{"settingsVersion":0,"reinjection":{"sessionStartCompact":false}}}}`)

	cfg, _, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Equal(t, config.MigrationSettingsVersion, cfg.Runtime.Migration.SettingsVersion)
	require.False(t, cfg.Runtime.Migration.Reinjection.SessionStartCompact, "a known switch at a known version is honoured")
	require.Equal(t, []string{"runtime.migration.settingsVersion"}, warningKeys(warns))
}

// TestLoad_RetiredMeaningKeysWarnButStillApply pins the deprecation diagnostic of Qompack.md v1.5
// Appendix C: a user layer that sets one of the retired-meaning keys is told what the key no
// longer means, the value is still applied (reader compatibility), the warning carries the
// Deprecated flag, and an untouched default produces nothing.
func TestLoad_RetiredMeaningKeysWarnButStillApply(t *testing.T) {
	env := baseEnv(t)
	writeConfigFile(t, env.ProjectRoot, `{"scheduler":{"youngDaly":{"enabled":true},"idle":{"deepCutWhenCold":false}},"checkpoint":{"incrementalSpanInstruction":false}}`)

	cfg, _, warns, err := config.Load(env)
	require.NoError(t, err)
	require.True(t, cfg.Scheduler.YoungDaly.Enabled, "the value is applied; only its meaning is deprecated")
	require.False(t, cfg.Scheduler.Idle.DeepCutWhenCold)
	require.False(t, cfg.Checkpoint.IncrementalSpanInstruction)

	require.ElementsMatch(t, []string{
		"scheduler.youngDaly.enabled", "scheduler.idle.deepCutWhenCold", "checkpoint.incrementalSpanInstruction",
	}, warningKeys(warns))
	for _, w := range warns {
		require.True(t, w.Deprecated, w.Key)
		require.Contains(t, w.Message, "deprecated meaning")
		require.Contains(t, w.Message, "v1.5")
		require.Contains(t, w.Location, "config.json:")
	}
	require.Empty(t, config.ViolationsFromWarnings(nil))

	// The default layer never triggers the diagnostic.
	_, _, warns, err = config.Load(baseEnv(t))
	require.NoError(t, err)
	require.Empty(t, warns)
}

// TestMigration_ForwardCompatibleReader is the rollback half of commit 6's contract: a config
// written with the whole runtime.migration block is readable by this build (every key known), and
// a build that predates the block would drop it as an unknown section with a warning — which is
// exactly what deepMerge's unknown-key rule does for any section it does not know. The second
// half is demonstrated on a stand-in unknown section here because an older binary cannot be
// linked into this test.
func TestMigration_ForwardCompatibleReader(t *testing.T) {
	env := baseEnv(t)
	full := fmt.Sprintf(`{"runtime":{"migration":{"settingsVersion":%d,
		"capture":{"rawEvidence":false},"publication":{"durableFrontier":false},
		"reinjection":{"sessionStartCompact":true},"replacement":{"newResult":false},
		"compaction":{"automaticVeto":false,"blockManualCompact":false},"experiments":{"enabled":false}}}}`,
		config.MigrationSettingsVersion)
	writeConfigFile(t, env.ProjectRoot, full)
	cfg, _, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Empty(t, warns, "every key in the block is known to this build")
	require.Equal(t, config.Defaults().Runtime.Migration, cfg.Runtime.Migration)

	env = baseEnv(t)
	writeConfigFile(t, env.ProjectRoot, `{"runtime":{"migrationFromTheFuture":{"settingsVersion":9}}}`)
	cfg, _, warns, err = config.Load(env)
	require.NoError(t, err)
	require.Equal(t, []string{"runtime.migrationFromTheFuture"}, warningKeys(warns))
	require.Contains(t, warns[0].Message, "unknown key")
	require.Equal(t, config.Defaults(), cfg)
}

// leafTypes flattens a JSON Schema document into dotted leaf path -> type name.
func leafTypes(t *testing.T, node map[string]any, prefix string) map[string]string {
	t.Helper()
	out := map[string]string{}
	props, ok := node["properties"].(map[string]any)
	if !ok {
		return out
	}
	for k, v := range props {
		child, ok := v.(map[string]any)
		require.True(t, ok, "schema node %s.%s", prefix, k)
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if _, isObj := child["properties"]; isObj {
			for kk, vv := range leafTypes(t, child, path) {
				out[kk] = vv
			}
			continue
		}
		typ, _ := child["type"].(string)
		out[path] = typ
	}
	return out
}
