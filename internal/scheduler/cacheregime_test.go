package scheduler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
)

// envOf is the injected config.Env.Getenv the ladder is driven with: a closed map, so a variable
// the test did not set reads as unset exactly like an absent process variable.
func envOf(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// unknownTestRegime is the rung-1 regime the plan's ttl tables describe: Appendix C's floor for
// the lower bound, the one-hour ceiling for the upper, and the dearer write multiplier.
func unknownTestRegime() CacheRegime {
	return CacheRegime{
		TTLMinSeconds:   300,
		TTLMaxSeconds:   3600,
		ReadMultiplier:  0.1,
		WriteMultiplier: 2.0,
		Source:          "unknown",
	}
}

// packageSourceFiles lists this package's non-test Go files by locating the directory of the
// calling test file, so the source-level assertions below scan whatever is on disk rather than a
// hardcoded file list that would silently miss a file another commit adds.
func packageSourceFiles(t *testing.T) []string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller must resolve the test file")
	dir := filepath.Dir(thisFile)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var files []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	require.NotEmpty(t, files)
	return files
}

func TestResolveCacheRegime_LadderOrder(t *testing.T) {
	cfg := baseCfg()
	const model = "claude-sonnet-5"

	force := ResolveCacheRegime(envOf(map[string]string{"FORCE_PROMPT_CACHING_5M": "1"}), cfg, model, false, HostOneHourTTLSeconds)
	require.Equal(t, "force_5m", force.Source)
	require.Equal(t, CacheRegime{
		TTLMinSeconds: 300, TTLMaxSeconds: 300, ReadMultiplier: 0.1, WriteMultiplier: 1.25, Source: "force_5m",
	}, force)

	disabled := ResolveCacheRegime(envOf(map[string]string{"DISABLE_PROMPT_CACHING": "1"}), cfg, model, false, HostOneHourTTLSeconds)
	require.Equal(t, "disabled", disabled.Source)
	require.True(t, disabled.Disabled)

	oneHour := ResolveCacheRegime(envOf(map[string]string{"ENABLE_PROMPT_CACHING_1H": "1"}), cfg, model, false, HostOneHourTTLSeconds)
	require.Equal(t, "enable_1h", oneHour.Source)
	require.Equal(t, CacheRegime{
		TTLMinSeconds: 3600, TTLMaxSeconds: 3600, ReadMultiplier: 0.1, WriteMultiplier: 2.0, Source: "enable_1h",
	}, oneHour)

	unknown := ResolveCacheRegime(envOf(nil), cfg, model, false, HostOneHourTTLSeconds)
	require.Equal(t, "unknown", unknown.Source)
	require.False(t, unknown.Disabled)

	// FORCE_PROMPT_CACHING_5M applies "regardless of authentication" and exists to override a
	// managed-settings ENABLE_PROMPT_CACHING_1H, so the conflicting pair resolves to force_5m.
	both := ResolveCacheRegime(envOf(map[string]string{
		"FORCE_PROMPT_CACHING_5M":  "1",
		"ENABLE_PROMPT_CACHING_1H": "1",
	}), cfg, model, false, HostOneHourTTLSeconds)
	require.Equal(t, "force_5m", both.Source)
	require.Equal(t, force, both)

	// A disabled cache is not a short cache: DISABLE outranks everything (ruling R1).
	all := ResolveCacheRegime(envOf(map[string]string{
		"DISABLE_PROMPT_CACHING":   "1",
		"FORCE_PROMPT_CACHING_5M":  "1",
		"ENABLE_PROMPT_CACHING_1H": "1",
	}), cfg, model, false, HostOneHourTTLSeconds)
	require.Equal(t, "disabled", all.Source)
	require.True(t, all.Disabled)

	// A nil getenv is "no environment at all" and lands on the unknown rung rather than panicking.
	require.Equal(t, "unknown", ResolveCacheRegime(nil, cfg, model, false, HostOneHourTTLSeconds).Source)
}

func TestResolveCacheRegime_WriteMultiplierTracksTTL(t *testing.T) {
	cfg := baseCfg()
	oneHour := ResolveCacheRegime(envOf(map[string]string{"ENABLE_PROMPT_CACHING_1H": "1"}), cfg, "", false, HostOneHourTTLSeconds)
	fiveMin := ResolveCacheRegime(envOf(map[string]string{"FORCE_PROMPT_CACHING_5M": "1"}), cfg, "", false, HostOneHourTTLSeconds)

	require.Equal(t, 2.0, oneHour.WriteMultiplier)
	require.Equal(t, 1.25, fiveMin.WriteMultiplier)
	require.Equal(t, 0.1, oneHour.ReadMultiplier)
	require.Equal(t, 0.1, fiveMin.ReadMultiplier)

	// The ski-rental break-even moves with w: 12.5 reads under one regime, 20 under the other.
	require.True(t, SkiRentalShouldWrite(13, fiveMin.ReadMultiplier, fiveMin.WriteMultiplier))
	require.False(t, SkiRentalShouldWrite(13, oneHour.ReadMultiplier, oneHour.WriteMultiplier))
	require.False(t, SkiRentalShouldWrite(20, oneHour.ReadMultiplier, oneHour.WriteMultiplier))
	require.True(t, SkiRentalShouldWrite(20.1, oneHour.ReadMultiplier, oneHour.WriteMultiplier))
}

func TestResolveCacheRegime_UnknownTakesTheDearerW(t *testing.T) {
	cfg := baseCfg()
	reg := ResolveCacheRegime(envOf(nil), cfg, "claude-opus-5", false, HostOneHourTTLSeconds)
	require.Equal(t, "unknown", reg.Source)
	require.Equal(t, cfg.Cache.TTLSeconds, reg.TTLMinSeconds)
	require.Equal(t, 300, reg.TTLMinSeconds)
	require.Equal(t, 3600, reg.TTLMaxSeconds)
	require.Equal(t, HostOneHourTTLSeconds, reg.TTLMaxSeconds)
	require.Equal(t, 2.0, reg.WriteMultiplier)
	require.Equal(t, cfg.Cache.ReadMultiplier, reg.ReadMultiplier)
	require.False(t, reg.Disabled)
	require.Equal(t, UnknownRegime(cfg, HostOneHourTTLSeconds), reg)
}

// TestUnknownRegime_Bounds pins the runtime.scheduler.cache.assumeMaxTTLSeconds contract:
// non-positive falls back to the one-hour host TTL, the upper bound never drops below the floor,
// and a config write multiplier above the one-hour figure is kept rather than lowered.
func TestUnknownRegime_Bounds(t *testing.T) {
	cfg := baseCfg()

	require.Equal(t, 3600, UnknownRegime(cfg, 0).TTLMaxSeconds)
	require.Equal(t, 3600, UnknownRegime(cfg, -5).TTLMaxSeconds)
	require.Equal(t, 7200, UnknownRegime(cfg, 7200).TTLMaxSeconds)
	require.Equal(t, 300, UnknownRegime(cfg, 100).TTLMaxSeconds, "TTLMax is never below TTLMin")
	require.Equal(t, 300, UnknownRegime(cfg, 100).TTLMinSeconds)

	dear := cfg
	dear.Cache.WriteMultiplier = 3.0
	require.Equal(t, 3.0, UnknownRegime(dear, 0).WriteMultiplier)
	require.Equal(t, "unknown", UnknownRegime(cfg, 0).Source)
	require.False(t, UnknownRegime(cfg, 0).Disabled)
}

func TestResolveCacheRegime_DisabledZeroesThePremium(t *testing.T) {
	cfg := baseCfg()
	want := CacheRegime{ReadMultiplier: 1, WriteMultiplier: 1, Disabled: true, Source: "disabled"}

	global := ResolveCacheRegime(envOf(map[string]string{"DISABLE_PROMPT_CACHING": "1"}), cfg, "claude-sonnet-5", false, HostOneHourTTLSeconds)
	require.Equal(t, want, global)
	require.Equal(t, 1.0, global.ReadMultiplier)
	require.Equal(t, 1.0, global.WriteMultiplier)
	require.Equal(t, 0, global.TTLMinSeconds)
	require.Equal(t, 0, global.TTLMaxSeconds)

	perFamily := envOf(map[string]string{"DISABLE_PROMPT_CACHING_OPUS": "1"})
	require.Equal(t, want, ResolveCacheRegime(perFamily, cfg, "claude-opus-5", false, HostOneHourTTLSeconds))
	require.Equal(t, want, ResolveCacheRegime(perFamily, cfg, "Claude-OPUS-4-1", false, HostOneHourTTLSeconds), "family match is case-insensitive")

	sonnet := ResolveCacheRegime(perFamily, cfg, "claude-sonnet-5", false, HostOneHourTTLSeconds)
	require.False(t, sonnet.Disabled, "a non-matching family falls through")
	require.Equal(t, "unknown", sonnet.Source)

	// Falling through lands on whatever the next rung says, not on a fixed answer.
	withOneHour := envOf(map[string]string{"DISABLE_PROMPT_CACHING_OPUS": "1", "ENABLE_PROMPT_CACHING_1H": "1"})
	require.Equal(t, "enable_1h", ResolveCacheRegime(withOneHour, cfg, "claude-sonnet-5", false, HostOneHourTTLSeconds).Source)
	require.Equal(t, "disabled", ResolveCacheRegime(withOneHour, cfg, "claude-opus-5", false, HostOneHourTTLSeconds).Source)

	// No model ⇒ the per-family rung is skipped, never matched against an empty family.
	require.Equal(t, "unknown", ResolveCacheRegime(perFamily, cfg, "", false, HostOneHourTTLSeconds).Source)
	require.Equal(t, "unknown", ResolveCacheRegime(perFamily, cfg, "some-gateway-model", false, HostOneHourTTLSeconds).Source)

	// Every documented family name is recognised.
	for _, fam := range []string{"OPUS", "SONNET", "HAIKU", "FABLE", "MYTHOS"} {
		env := envOf(map[string]string{"DISABLE_PROMPT_CACHING_" + fam: "1"})
		require.True(t, ResolveCacheRegime(env, cfg, "claude-"+strings.ToLower(fam)+"-9", false, HostOneHourTTLSeconds).Disabled, fam)
	}
}

func TestResolveCacheRegime_SubagentPinned5m(t *testing.T) {
	cfg := baseCfg()
	env := envOf(map[string]string{"ENABLE_PROMPT_CACHING_1H": "1"})

	reg := ResolveCacheRegime(env, cfg, "claude-opus-5", true, HostOneHourTTLSeconds)
	require.Equal(t, CacheRegime{
		TTLMinSeconds: 300, TTLMaxSeconds: 300, ReadMultiplier: 0.1, WriteMultiplier: 1.25, Source: "subagent_5m",
	}, reg)

	// The five-minute regime is built from cfg, so a re-priced floor is honoured.
	repriced := cfg
	repriced.Cache.TTLSeconds = 600
	repriced.Cache.WriteMultiplier = 1.5
	got := ResolveCacheRegime(env, repriced, "claude-opus-5", true, HostOneHourTTLSeconds)
	require.Equal(t, 600, got.TTLMinSeconds)
	require.Equal(t, 600, got.TTLMaxSeconds)
	require.Equal(t, 1.5, got.WriteMultiplier)

	// The subagent pin outranks FORCE_5M's source label but never a disabled cache (ruling R1).
	require.Equal(t, "subagent_5m", ResolveCacheRegime(envOf(map[string]string{"FORCE_PROMPT_CACHING_5M": "1"}), cfg, "", true, HostOneHourTTLSeconds).Source)
	require.Equal(t, "subagent_5m", ResolveCacheRegime(envOf(nil), cfg, "", true, HostOneHourTTLSeconds).Source)
	require.Equal(t, "disabled", ResolveCacheRegime(envOf(map[string]string{"DISABLE_PROMPT_CACHING": "1"}), cfg, "", true, HostOneHourTTLSeconds).Source)
}

// TestResolveCacheRegime_EnvTruthiness pins the host-variable truth rule: trimmed, lower-cased
// value non-empty and not one of 0/false/no/off.
func TestResolveCacheRegime_EnvTruthiness(t *testing.T) {
	cfg := baseCfg()
	for _, v := range []string{"", " ", "0", "false", "FALSE", "no", "No ", "off", " OFF "} {
		env := envOf(map[string]string{"ENABLE_PROMPT_CACHING_1H": v})
		require.Equal(t, "unknown", ResolveCacheRegime(env, cfg, "", false, HostOneHourTTLSeconds).Source, "value %q must read as unset", v)
	}
	for _, v := range []string{"1", "true", "TRUE", " yes ", "on", "anything"} {
		env := envOf(map[string]string{"ENABLE_PROMPT_CACHING_1H": v})
		require.Equal(t, "enable_1h", ResolveCacheRegime(env, cfg, "", false, HostOneHourTTLSeconds).Source, "value %q must read as set", v)
	}
}

func TestResolveCacheRegime_ReadsEnvThroughConfigEnv(t *testing.T) {
	cfg := baseCfg()
	var asked []string
	env := config.Env{Getenv: func(k string) string {
		asked = append(asked, k)
		if k == "ENABLE_PROMPT_CACHING_1H" {
			return "1"
		}
		return ""
	}}

	reg := ResolveCacheRegime(env.Getenv, cfg, "claude-opus-5", false, HostOneHourTTLSeconds)
	require.Equal(t, "enable_1h", reg.Source, "the injected environment must be the one consulted")
	require.Subset(t, asked, []string{
		"DISABLE_PROMPT_CACHING",
		"DISABLE_PROMPT_CACHING_OPUS",
		"FORCE_PROMPT_CACHING_5M",
		"ENABLE_PROMPT_CACHING_1H",
	})
	for _, k := range asked {
		require.False(t, strings.HasPrefix(k, "QOMPACK_"), "host variables are read by their documented names, not through the QOMPACK_ config layer: %q", k)
	}

	// And no production file in this package reaches os.Getenv behind the injection.
	fset := token.NewFileSet()
	for _, path := range packageSourceFiles(t) {
		f, err := parser.ParseFile(fset, path, nil, 0)
		require.NoError(t, err)
		osNames := map[string]bool{}
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) != "os" {
				continue
			}
			name := "os"
			if imp.Name != nil {
				name = imp.Name.Name
			}
			osNames[name] = true
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && osNames[x.Name] && sel.Sel.Name == "Getenv" {
				t.Errorf("%s: calls os.Getenv at %s; read the environment through config.Env.Getenv", filepath.Base(path), fset.Position(sel.Pos()))
			}
			return true
		})
	}
}

func TestResolveCacheRegime_NoAppendixCKeyMoved(t *testing.T) {
	snapshot := config.Defaults().Scheduler.Cache
	cfg := config.Defaults().Scheduler

	rungs := []map[string]string{
		{"DISABLE_PROMPT_CACHING": "1"},
		{"DISABLE_PROMPT_CACHING_OPUS": "1"},
		{"FORCE_PROMPT_CACHING_5M": "1"},
		{"ENABLE_PROMPT_CACHING_1H": "1"},
		{},
	}
	for _, vars := range rungs {
		for _, subagent := range []bool{false, true} {
			_ = ResolveCacheRegime(envOf(vars), cfg, "claude-opus-5", subagent, HostOneHourTTLSeconds)
		}
	}
	_ = UnknownRegime(cfg, 0)
	_ = UnknownRegime(cfg, 7200)

	require.Equal(t, snapshot, cfg.Cache, "resolution must never write back into the config it read")
	require.Equal(t, snapshot, config.Defaults().Scheduler.Cache, "Appendix C's defaults are untouched")
	require.Equal(t, config.CacheCfg{ReadMultiplier: 0.1, WriteMultiplier: 1.25, TTLSeconds: 300}, snapshot)
}

// TestResolveCacheRegime_UnknownRungUsesAssumedMaxTTL pins ruling R42: the unknown rung's upper TTL
// bound is the configured runtime.scheduler.cache.assumeMaxTTLSeconds rather than a literal, so the
// key is live end to end; a non-positive value falls back to the host's one-hour TTL, and a known
// regime never reads the assumption.
func TestResolveCacheRegime_UnknownRungUsesAssumedMaxTTL(t *testing.T) {
	cfg := baseCfg()
	reg := ResolveCacheRegime(envOf(nil), cfg, "claude-opus-5", false, 7200)
	require.Equal(t, "unknown", reg.Source)
	require.Equal(t, 7200, reg.TTLMaxSeconds)
	require.Equal(t, cfg.Cache.TTLSeconds, reg.TTLMinSeconds)
	require.Equal(t, HostOneHourTTLSeconds, ResolveCacheRegime(envOf(nil), cfg, "", false, 0).TTLMaxSeconds)
	oneHour := ResolveCacheRegime(envOf(map[string]string{"ENABLE_PROMPT_CACHING_1H": "1"}), cfg, "", false, 7200)
	require.Equal(t, HostOneHourTTLSeconds, oneHour.TTLMaxSeconds)
}
