package config_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
)

// TestLoad_EnvAllKinds exercises the env layer for every leaf kind config.Load supports: bool,
// int, float, string, []string and *float64.
func TestLoad_EnvAllKinds(t *testing.T) {
	env := baseEnv(t)
	env.Getenv = func(k string) string {
		switch k {
		case "QOMPACK_STORE__CANONICALIZE__ENABLED": // bool
			return "true"
		case "QOMPACK_STORE__CHUNK__MIN": // int
			return "2048"
		case "QOMPACK_SCHEDULER__SOFTFLOORPCT": // float
			return "0.4"
		case "QOMPACK_STORE__COMPRESSION": // string
			return "none"
		case "QOMPACK_STORE__CANONICALIZE__STRIP": // []string
			return "timestamps,crlf,paths"
		case "QOMPACK_SCHEDULER__YOUNGDALY__MEASUREDDELTASECONDS": // *float64
			return "42.5"
		default:
			return ""
		}
	}

	cfg, prov, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Empty(t, warns)
	require.True(t, cfg.Store.Canonicalize.Enabled)
	require.Equal(t, 2048, cfg.Store.Chunk.Min)
	require.Equal(t, 0.4, cfg.Scheduler.SoftFloorPct)
	require.Equal(t, "none", cfg.Store.Compression)
	require.Equal(t, []string{"timestamps", "crlf", "paths"}, cfg.Store.Canonicalize.Strip)
	require.NotNil(t, cfg.Scheduler.YoungDaly.MeasuredDeltaSeconds)
	require.Equal(t, 42.5, *cfg.Scheduler.YoungDaly.MeasuredDeltaSeconds)

	for _, k := range []string{
		"store.canonicalize.enabled", "store.chunk.min", "scheduler.softFloorPct",
		"store.compression", "store.canonicalize.strip", "scheduler.youngDaly.measuredDeltaSeconds",
	} {
		require.Equal(t, config.OriginEnv, prov[k].Origin, "key %s", k)
	}
}

// TestLoad_EnvInvalidValuesForEveryKind checks that an unparseable env value is skipped (leaving
// the lower layer's value in place) with a Warning, for every kind that can actually fail to
// parse (bool, int, float, *float64 — string and []string never fail to parse a raw string).
func TestLoad_EnvInvalidValuesForEveryKind(t *testing.T) {
	env := baseEnv(t)
	env.Getenv = func(k string) string {
		switch k {
		case "QOMPACK_STORE__CANONICALIZE__ENABLED":
			return "not-a-bool"
		case "QOMPACK_STORE__CHUNK__MIN":
			return "not-an-int"
		case "QOMPACK_SCHEDULER__SOFTFLOORPCT":
			return "not-a-float"
		case "QOMPACK_SCHEDULER__YOUNGDALY__MEASUREDDELTASECONDS":
			return "not-a-float-and-not-null"
		default:
			return ""
		}
	}

	cfg, _, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Equal(t, config.Defaults(), cfg)
	require.Len(t, warns, 4)
	for _, w := range warns {
		require.Contains(t, w.Message, "invalid env value")
	}
}

// TestLoad_FlagsAllKinds mirrors TestLoad_EnvAllKinds for the --set flag layer.
func TestLoad_FlagsAllKinds(t *testing.T) {
	env := baseEnv(t)
	env.Flags = map[string]string{
		"store.canonicalize.enabled":               "false",
		"store.chunk.target":                       "8192",
		"scheduler.cache.ttlSeconds":               "600",
		"eliminations.defaultScope":                "project",
		"scheduler.changepoint.features":           "paths,tools",
		"scheduler.youngDaly.measuredDeltaSeconds": "null",
	}

	cfg, prov, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Empty(t, warns)
	require.False(t, cfg.Store.Canonicalize.Enabled)
	require.Equal(t, 8192, cfg.Store.Chunk.Target)
	require.Equal(t, 600, cfg.Scheduler.Cache.TTLSeconds)
	require.Equal(t, "project", cfg.Eliminations.DefaultScope)
	require.Equal(t, []string{"paths", "tools"}, cfg.Scheduler.Changepoint.Features)
	require.Nil(t, cfg.Scheduler.YoungDaly.MeasuredDeltaSeconds)
	require.Equal(t, config.OriginFlag, prov["store.chunk.target"].Origin)
}

func TestLoad_FlagsInvalidValuesForEveryKind(t *testing.T) {
	env := baseEnv(t)
	env.Flags = map[string]string{
		"store.canonicalize.enabled": "not-a-bool",
		"store.chunk.target":         "not-an-int",
		"scheduler.softFloorPct":     "not-a-float",
	}

	cfg, _, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Equal(t, config.Defaults(), cfg)
	require.Len(t, warns, 3)
	for _, w := range warns {
		require.Contains(t, w.Message, "invalid --set value")
	}
}

// TestLoad_SectionNotObjectWarns exercises deepMerge's "expected an object" branch: a project
// file that sets an entire section to a scalar instead of a nested object.
func TestLoad_SectionNotObjectWarns(t *testing.T) {
	env := baseEnv(t)
	writeConfigFile(t, env.ProjectRoot, `{"scheduler": "not an object"}`)

	cfg, _, warns, err := config.Load(env)
	require.NoError(t, err)
	require.Equal(t, config.Defaults(), cfg)
	require.Len(t, warns, 1)
	require.Contains(t, warns[0].Message, "expected an object")
}

// TestLoad_WrongTypeForEveryKindViaFile exercises coerceLeafValue's rejection branch for bool,
// int, string, []string (bad element type) and *float64 (non-nil, non-number) leaves coming from
// a project file, complementing TestLoad_WrongTypeFallsBackWithWarning's float case.
func TestLoad_WrongTypeForEveryKindViaFile(t *testing.T) {
	env := baseEnv(t)
	writeConfigFile(t, env.ProjectRoot, `{
		"store": {
			"canonicalize": {
				"enabled": "not-a-bool",
				"strip": ["ok", 123]
			},
			"chunk": { "min": "not-an-int" },
			"compression": 42
		},
		"scheduler": {
			"youngDaly": { "measuredDeltaSeconds": true },
			"cache": { "readMultiplier": 2.5 }
		}
	}`)

	cfg, _, warns, err := config.Load(env)
	require.NoError(t, err)
	def := config.Defaults()
	require.Equal(t, def.Store.Canonicalize.Enabled, cfg.Store.Canonicalize.Enabled)
	require.Equal(t, def.Store.Canonicalize.Strip, cfg.Store.Canonicalize.Strip)
	require.Equal(t, def.Store.Chunk.Min, cfg.Store.Chunk.Min)
	require.Equal(t, def.Store.Compression, cfg.Store.Compression)
	require.Nil(t, cfg.Scheduler.YoungDaly.MeasuredDeltaSeconds)
	// readMultiplier=2.5 is well-typed (a JSON number) but out of range: that is caught by
	// Validate()'s fallback, a different warning shape than the five coerce-time ones above.
	require.Equal(t, def.Scheduler.Cache.ReadMultiplier, cfg.Scheduler.Cache.ReadMultiplier)

	require.Len(t, warns, 6)
}
