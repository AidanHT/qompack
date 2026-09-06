package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
)

// writeConfigFile writes content to <root>/.qompack/config.json, matching exactly where
// config.Load looks for both the user-global and project layers (Load joins HomeDir/ProjectRoot
// with ".qompack/config.json" itself).
func writeConfigFile(t *testing.T, root, content string) {
	t.Helper()
	dir := filepath.Join(root, ".qompack")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o644))
}

// baseEnv returns an Env pointing at two fresh, empty temp directories with a no-op Getenv, so a
// test that only cares about one layer does not have to think about the others.
func baseEnv(t *testing.T) config.Env {
	t.Helper()
	return config.Env{
		ProjectRoot: t.TempDir(),
		HomeDir:     t.TempDir(),
		Getenv:      func(string) string { return "" },
	}
}

func TestLoad_PrecedenceFiveLayers(t *testing.T) {
	env := baseEnv(t)
	writeConfigFile(t, env.HomeDir, `{"scheduler":{"softFloorPct":0.5}}`)
	writeConfigFile(t, env.ProjectRoot, `{"scheduler":{"softFloorPct":0.6}}`)
	env.Getenv = func(k string) string {
		if k == "QOMPACK_SCHEDULER__SOFTFLOORPCT" {
			return "0.7"
		}
		return ""
	}
	env.Flags = map[string]string{"scheduler.softFloorPct": "0.8"}

	cfg, prov, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Empty(t, warns)
	require.Equal(t, 0.8, cfg.Scheduler.SoftFloorPct)
	require.Equal(t, config.OriginFlag, prov["scheduler.softFloorPct"].Origin)
	require.Equal(t, "--set", prov["scheduler.softFloorPct"].Location)
}

// TestLoad_PrecedenceStopsAtHighestSetLayer confirms each layer alone, without any layer above
// it, is respected — i.e. the precedence chain is genuinely five independent layers rather than
// flags always winning regardless of whether they were set.
func TestLoad_PrecedenceStopsAtHighestSetLayer(t *testing.T) {
	env := baseEnv(t)
	writeConfigFile(t, env.HomeDir, `{"scheduler":{"softFloorPct":0.5}}`)
	writeConfigFile(t, env.ProjectRoot, `{"scheduler":{"softFloorPct":0.6}}`)

	cfg, prov, _, err := config.Load(env)
	require.NoError(t, err)
	require.Equal(t, 0.6, cfg.Scheduler.SoftFloorPct)
	require.Equal(t, config.OriginProjectFile, prov["scheduler.softFloorPct"].Origin)
}

func TestLoad_DeepMergePerLeaf(t *testing.T) {
	env := baseEnv(t)
	writeConfigFile(t, env.ProjectRoot, `{"scheduler":{"softFloorPct":0.6}}`)

	cfg, _, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Empty(t, warns)
	require.Equal(t, 0.6, cfg.Scheduler.SoftFloorPct)
	require.Equal(t, 0.1, cfg.Scheduler.Cache.ReadMultiplier)
	require.Equal(t, 120, cfg.Scheduler.Idle.DetectAfterSeconds)
	require.Equal(t, 1.25, cfg.Scheduler.Cache.WriteMultiplier)
	require.True(t, cfg.Scheduler.YoungDaly.Enabled)
}

func TestLoad_EnvKeyMapping(t *testing.T) {
	env := baseEnv(t)
	env.Getenv = func(k string) string {
		if k == "QOMPACK_SCHEDULER__CACHE__READMULTIPLIER" {
			return "0.08"
		}
		return ""
	}

	cfg, prov, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Empty(t, warns)
	require.Equal(t, 0.08, cfg.Scheduler.Cache.ReadMultiplier)
	require.Equal(t, config.OriginEnv, prov["scheduler.cache.readMultiplier"].Origin)
	require.Equal(t, "QOMPACK_SCHEDULER__CACHE__READMULTIPLIER", prov["scheduler.cache.readMultiplier"].Location)
}

func TestLoad_NullMeansMeasure(t *testing.T) {
	env := baseEnv(t)
	// Set a non-nil value at a lower layer first, so a nil result actually proves the project
	// layer's explicit null propagated, rather than merely reflecting the untouched default.
	writeConfigFile(t, env.HomeDir, `{"scheduler":{"youngDaly":{"measuredDeltaSeconds":5.0}}}`)
	writeConfigFile(t, env.ProjectRoot, `{"scheduler":{"youngDaly":{"measuredDeltaSeconds":null}}}`)

	cfg, prov, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Empty(t, warns)
	require.Nil(t, cfg.Scheduler.YoungDaly.MeasuredDeltaSeconds)
	require.Equal(t, config.OriginProjectFile, prov["scheduler.youngDaly.measuredDeltaSeconds"].Origin)
}

func TestLoad_EnvFloatPtrNullLiteral(t *testing.T) {
	env := baseEnv(t)
	writeConfigFile(t, env.ProjectRoot, `{"scheduler":{"youngDaly":{"measuredDeltaSeconds":5}}}`)
	env.Getenv = func(k string) string {
		if k == "QOMPACK_SCHEDULER__YOUNGDALY__MEASUREDDELTASECONDS" {
			return "null"
		}
		return ""
	}

	cfg, prov, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Empty(t, warns)
	require.Nil(t, cfg.Scheduler.YoungDaly.MeasuredDeltaSeconds)
	require.Equal(t, config.OriginEnv, prov["scheduler.youngDaly.measuredDeltaSeconds"].Origin)
}

func TestLoad_UnknownKeyWarnsNeverErrors(t *testing.T) {
	env := baseEnv(t)
	writeConfigFile(t, env.ProjectRoot, `{"store":{"futureKey":1},"newSection":{}}`)

	cfg, _, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Len(t, warns, 2)
	require.ElementsMatch(t, []string{"store.futureKey", "newSection"}, warningKeys(warns))
	for _, w := range warns {
		require.Contains(t, w.Message, "unknown key")
	}
	require.Equal(t, config.Defaults(), cfg)
}

// TestLoad_RelationalViolationFallsBackToSectionDefaults pins the fix for a FuzzConfigLoad
// finding: Load returned a Config that failed its own Validate(). store.chunk.min < target is
// keyed on min, so a bad TARGET produced a violation naming min — which was already 1024, its
// default — and the single-pass fallback restored nothing. Load now iterates and, when a pass
// restores nothing new, widens to the violated key's parent section.
func TestLoad_RelationalViolationFallsBackToSectionDefaults(t *testing.T) {
	for _, tc := range []struct {
		name    string
		file    string
		section string
	}{
		// The bad value is on the side of the comparison the rule does not name.
		{"chunk target below min", `{"store":{"chunk":{"target":0}}}`, "store.chunk"},
		{"chunk target above max", `{"store":{"chunk":{"target":999999999}}}`, "store.chunk"},
		{"rehydrate max below min", `{"runtime":{"rehydrate":{"maxTokens":1}}}`, "runtime.rehydrate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := baseEnv(t)
			writeConfigFile(t, env.ProjectRoot, tc.file)

			cfg, prov, warns, err := config.Load(env)
			require.NoError(t, err)
			require.Empty(t, cfg.Validate(),
				"Load must return an already-validated config, whichever side of the relation is bad")

			// The widened fallback fired, and said so: without this the test would pass vacuously
			// if some future absolute bound caught the value before the relation ever broke.
			require.Contains(t, warningKeys(warns), tc.section,
				"the caller must be told which section was reset")
			require.Equal(t, config.OriginDefault, prov[tc.section].Origin)
		})
	}

	// The narrow path is unaffected: when the NAMED key is the bad one, the leaf is restored and
	// its siblings are left alone.
	t.Run("named key is the bad one keeps its siblings", func(t *testing.T) {
		env := baseEnv(t)
		writeConfigFile(t, env.ProjectRoot, `{"store":{"chunk":{"min":9999,"max":99999}}}`)

		cfg, _, warns, err := config.Load(env)
		require.NoError(t, err)
		require.Empty(t, cfg.Validate())
		require.Equal(t, config.Defaults().Store.Chunk.Min, cfg.Store.Chunk.Min, "the violated leaf falls back")
		require.Equal(t, 99999, cfg.Store.Chunk.Max, "a valid sibling survives the fallback")
		require.ElementsMatch(t, []string{"store.chunk.min"}, warningKeys(warns))
	})
}

func TestLoad_InvalidLeafFallsBackNotCrash(t *testing.T) {
	env := baseEnv(t)
	writeConfigFile(t, env.ProjectRoot, `{"scheduler":{"softFloorPct":1.5},"store":{"chunk":{"min":9999}}}`)

	cfg, prov, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Equal(t, 0.55, cfg.Scheduler.SoftFloorPct)
	require.Equal(t, 1024, cfg.Store.Chunk.Min)

	require.Len(t, warns, 2)
	require.ElementsMatch(t, []string{"scheduler.softFloorPct", "store.chunk.min"}, warningKeys(warns))
	for _, w := range warns {
		require.Contains(t, w.Message, "invalid value, using default")
	}

	violations := config.ViolationsFromWarnings(warns)
	require.Len(t, violations, 2)
	require.ElementsMatch(t, []string{"scheduler.softFloorPct", "store.chunk.min"},
		[]string{violations[0].Key, violations[1].Key})

	// Fallback also updates provenance to say so.
	require.Equal(t, "fallback after violation", prov["scheduler.softFloorPct"].Location)
	require.Equal(t, config.OriginDefault, prov["scheduler.softFloorPct"].Origin)
}

func TestLoad_WrongTypeFallsBackWithWarning(t *testing.T) {
	env := baseEnv(t)
	writeConfigFile(t, env.ProjectRoot, `{"scheduler":{"softFloorPct":"not-a-number"}}`)

	cfg, _, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Equal(t, 0.55, cfg.Scheduler.SoftFloorPct)
	require.Len(t, warns, 1)
	require.Equal(t, "scheduler.softFloorPct", warns[0].Key)
	require.Contains(t, warns[0].Message, "invalid type")
}

func TestLoad_UnparseableFileWarns(t *testing.T) {
	env := baseEnv(t)
	writeConfigFile(t, env.ProjectRoot, `{`)

	cfg, _, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Len(t, warns, 1)
	require.Contains(t, warns[0].Message, "unparseable config")
	require.Equal(t, config.Defaults(), cfg)
}

func TestLoad_ProvenanceLocationHasLine(t *testing.T) {
	env := baseEnv(t)
	// softFloorPct's key lands on line 4 by construction: {, {, blank, key.
	content := "{\n" +
		"  \"scheduler\": {\n" +
		"    \n" +
		"    \"softFloorPct\": 0.6\n" +
		"  }\n" +
		"}\n"
	writeConfigFile(t, env.ProjectRoot, content)

	_, prov, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Empty(t, warns)
	loc := prov["scheduler.softFloorPct"].Location
	require.True(t, strings.HasSuffix(loc, "config.json:4"), "got location %q", loc)
}

func TestLoad_ProvenanceLocationHasLine_WithLeadingComment(t *testing.T) {
	env := baseEnv(t)
	// A comment line above the key must not shift the reported line number.
	content := "{\n" +
		"  // a comment about the scheduler\n" +
		"  \"scheduler\": { \"softFloorPct\": 0.6 }\n" +
		"}\n"
	writeConfigFile(t, env.ProjectRoot, content)

	_, prov, _, err := config.Load(env)
	require.NoError(t, err)
	loc := prov["scheduler.softFloorPct"].Location
	require.True(t, strings.HasSuffix(loc, "config.json:3"), "got location %q", loc)
}

func TestSubmodularEnabled_DerivedFromRuntime(t *testing.T) {
	env := baseEnv(t)
	writeConfigFile(t, env.ProjectRoot, `{"runtime":{"selection":{"submodularEnabled":true}}}`)

	cfg, _, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Empty(t, warns)
	require.True(t, cfg.Selection.Submodular.Enabled)
	require.True(t, cfg.Runtime.Selection.SubmodularEnabled)

	b, err := json.Marshal(cfg.Selection)
	require.NoError(t, err)
	require.NotContains(t, string(b), `"enabled"`)
}

func TestLoad_FlagsUnknownKeyAndStringSlice(t *testing.T) {
	env := baseEnv(t)
	env.Flags = map[string]string{
		"bogus.key":                "1",
		"store.canonicalize.strip": "timestamps,ansi",
	}

	cfg, prov, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Len(t, warns, 1)
	require.Equal(t, "bogus.key", warns[0].Key)
	require.Equal(t, []string{"timestamps", "ansi"}, cfg.Store.Canonicalize.Strip)
	require.Equal(t, config.OriginFlag, prov["store.canonicalize.strip"].Origin)
}

func TestLoad_EnvBoolAndInvalidValueSkipped(t *testing.T) {
	env := baseEnv(t)
	env.Getenv = func(k string) string {
		switch k {
		case "QOMPACK_STORE__CANONICALIZE__ENABLED":
			return "false"
		case "QOMPACK_EVAL__MINSESSIONS":
			return "not-an-int"
		default:
			return ""
		}
	}

	cfg, _, warns, err := config.Load(env)
	require.NoError(t, err)
	require.False(t, cfg.Store.Canonicalize.Enabled)
	require.Equal(t, 20, cfg.Eval.MinSessions, "an unparseable env value must be skipped, not applied as zero")
	require.Len(t, warns, 1)
	require.Contains(t, warns[0].Message, "invalid env value")
}

func TestLoad_EmptyProjectRootErrors(t *testing.T) {
	cfg, prov, warns, err := config.Load(config.Env{})
	require.Error(t, err)
	require.Equal(t, config.Config{}, cfg)
	require.Nil(t, prov)
	require.Nil(t, warns)
}

func TestLoad_MissingFilesAreNotErrors(t *testing.T) {
	env := baseEnv(t) // neither .qompack/config.json exists in either temp dir
	cfg, _, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Empty(t, warns)
	require.Equal(t, config.Defaults(), cfg)
}

func TestLoad_NilGetenvAndFlagsAreSafe(t *testing.T) {
	cfg, _, warns, err := config.Load(config.Env{ProjectRoot: t.TempDir(), HomeDir: t.TempDir()})
	require.NoError(t, err)
	require.Empty(t, warns)
	require.Equal(t, config.Defaults(), cfg)
}

// TestLoad_ArraysReplaceWholesale confirms a project override of a []string leaf replaces the
// whole list rather than merging element-wise (00-ARCHITECTURE.md §11.2).
func TestLoad_ArraysReplaceWholesale(t *testing.T) {
	env := baseEnv(t)
	writeConfigFile(t, env.ProjectRoot, `{"scheduler":{"changepoint":{"features":["paths"]}}}`)

	cfg, _, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Empty(t, warns)
	require.Equal(t, []string{"paths"}, cfg.Scheduler.Changepoint.Features)
}

func warningKeys(ws []config.Warning) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Key
	}
	return out
}
