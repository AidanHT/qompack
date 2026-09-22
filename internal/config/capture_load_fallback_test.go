package config_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
)

// captureFallbackCase is one per-leaf configuration problem: a file, environment or --set value that
// config.Load answers with a Warning and a per-leaf fallback, never with an error.
type captureFallbackCase struct {
	name    string
	user    string
	project string
	env     map[string]string
	flags   map[string]string
	check   func(t *testing.T, cfg config.Config)
}

// captureFallbackCases are the per-leaf problems V6 close-out item C1.8 is about. SP-18 found by
// probing that ONE of them in a project config made every hook admit nothing and create no
// .qompack/ — while `qompack self-test` reported `config.load ok` — because LoadForCapture turned
// every merge Warning into a refusal of the whole delivery. The documented contract (README
// "Configuration", docs/config-reference.md) is the opposite: an invalid value is never fatal and an
// unknown key only warns.
func captureFallbackCases() []captureFallbackCase {
	def := config.Defaults()
	return []captureFallbackCase{
		{
			name:    "unknown top-level key",
			project: `{"capture-secret":true}`,
		},
		{
			name:    "unknown nested key",
			project: `{"runtime":{"notAKey":1}}`,
		},
		{
			name:    "wrong leaf type",
			project: `{"scheduler":{"softFloorPct":"wrong"}}`,
			check: func(t *testing.T, cfg config.Config) {
				require.Equal(t, def.Scheduler.SoftFloorPct, cfg.Scheduler.SoftFloorPct)
			},
		},
		{
			name:    "wrong leaf type keeps the lower layer",
			user:    `{"scheduler":{"softFloorPct":0.5}}`,
			project: `{"scheduler":{"softFloorPct":"wrong"}}`,
			check: func(t *testing.T, cfg config.Config) {
				require.Equal(t, 0.5, cfg.Scheduler.SoftFloorPct)
			},
		},
		{
			name:    "section that is not an object",
			project: `{"scheduler":5,"checkpoint":{"budgetTokens":9000}}`,
			check: func(t *testing.T, cfg config.Config) {
				require.Equal(t, def.Scheduler, cfg.Scheduler)
				require.Equal(t, 9000, cfg.Checkpoint.BudgetTokens, "the rest of the layer still applies")
			},
		},
		{
			name:    "integer leaf beyond the platform int",
			project: `{"checkpoint":{"budgetTokens":1e300},"retrieval":{"promoteAfterExpansions":3}}`,
			check: func(t *testing.T, cfg config.Config) {
				require.Equal(t, def.Checkpoint.BudgetTokens, cfg.Checkpoint.BudgetTokens)
				require.Equal(t, 3, cfg.Retrieval.PromoteAfterExpansions, "the rest of the layer still applies")
			},
		},
		{
			name:    "invalid value beside an unknown key",
			project: `{"runtime":{"mode":"sideways","notAKey":1},"checkpoint":{"budgetTokens":9000}}`,
			check: func(t *testing.T, cfg config.Config) {
				require.Equal(t, def.Runtime.Mode, cfg.Runtime.Mode, "the invalid leaf falls back to its default")
				require.Equal(t, 9000, cfg.Checkpoint.BudgetTokens, "every other leaf still applies")
			},
		},
		{
			name: "unparseable environment value",
			env:  map[string]string{"QOMPACK_RETRIEVAL__EPHEMERALRESULTS": "maybe"},
			check: func(t *testing.T, cfg config.Config) {
				require.Equal(t, def.Retrieval.EphemeralResults, cfg.Retrieval.EphemeralResults)
			},
		},
		{
			name: "nonfinite environment value",
			env:  map[string]string{"QOMPACK_SCHEDULER__SOFTFLOORPCT": "NaN"},
			check: func(t *testing.T, cfg config.Config) {
				require.Equal(t, def.Scheduler.SoftFloorPct, cfg.Scheduler.SoftFloorPct)
			},
		},
		{
			// 9223372036854775807 parses as an int64 and then rounds to 2^63 as a float64, which no
			// Go int decodes: it must cost this leaf, not refuse the capture over the decode.
			name:    "largest int64 environment value",
			project: `{"checkpoint":{"budgetTokens":9000}}`,
			env:     map[string]string{"QOMPACK_RUNTIME__DAEMON__MAXSESSIONS": "9223372036854775807"},
			check: func(t *testing.T, cfg config.Config) {
				require.Equal(t, def.Runtime.Daemon.MaxSessions, cfg.Runtime.Daemon.MaxSessions)
				require.Equal(t, 9000, cfg.Checkpoint.BudgetTokens, "every other layer still applies")
			},
		},
		{
			name:    "largest int64 flag value",
			project: `{"checkpoint":{"budgetTokens":9000}}`,
			flags:   map[string]string{"runtime.daemon.maxSessions": "9223372036854775807"},
			check: func(t *testing.T, cfg config.Config) {
				require.Equal(t, def.Runtime.Daemon.MaxSessions, cfg.Runtime.Daemon.MaxSessions)
				require.Equal(t, 9000, cfg.Checkpoint.BudgetTokens, "every other layer still applies")
			},
		},
		{
			name:  "unknown flag",
			flags: map[string]string{"unknown.capture.option": "capture-secret"},
		},
		{
			name:  "nonfinite flag value",
			flags: map[string]string{"scheduler.softFloorPct": "NaN"},
			check: func(t *testing.T, cfg config.Config) {
				require.Equal(t, def.Scheduler.SoftFloorPct, cfg.Scheduler.SoftFloorPct)
			},
		},
		{
			name:  "unparseable flag value",
			flags: map[string]string{"retrieval.promoteAfterExpansions": "two"},
			check: func(t *testing.T, cfg config.Config) {
				require.Equal(t, def.Retrieval.PromoteAfterExpansions, cfg.Retrieval.PromoteAfterExpansions)
			},
		},
	}
}

func (tc captureFallbackCase) setup(t *testing.T) config.Env {
	t.Helper()
	env := baseEnv(t)
	if tc.user != "" {
		writeConfigFile(t, env.HomeDir, tc.user)
	}
	if tc.project != "" {
		writeConfigFile(t, env.ProjectRoot, tc.project)
	}
	if tc.env != nil {
		env.Getenv = func(name string) string { return tc.env[name] }
	}
	env.Flags = tc.flags
	return env
}

// TestLoadForCapture_PerLeafProblemsFallBackInsteadOfRefusing is C1.8's unit-level regression: one
// per-leaf problem must cost the capture that one leaf, not the whole delivery.
func TestLoadForCapture_PerLeafProblemsFallBackInsteadOfRefusing(t *testing.T) {
	for _, tc := range captureFallbackCases() {
		t.Run(tc.name, func(t *testing.T) {
			env := tc.setup(t)

			cfg, prov, _, warnings, err := config.LoadForCapture(env)
			require.NoError(t, err, "a per-leaf problem must not refuse the whole capture")
			require.NotNil(t, prov)
			require.NotEmpty(t, warnings, "what was not applied must be returned so it can be reported")
			require.Empty(t, cfg.Validate(), "the returned configuration must be valid")
			if tc.check != nil {
				tc.check(t, cfg)
			}
		})
	}
}

// TestLoadForCapture_AgreesWithLoadOnEveryPerLeafProblem pins the two loaders to one contract:
// for every per-leaf problem the capture loader must produce exactly the configuration the soft
// loader does, and exactly the §11.3 violations the soft loader's warnings decode to.
func TestLoadForCapture_AgreesWithLoadOnEveryPerLeafProblem(t *testing.T) {
	for _, tc := range captureFallbackCases() {
		t.Run(tc.name, func(t *testing.T) {
			env := tc.setup(t)

			loaded, _, loadWarns, err := config.Load(env)
			require.NoError(t, err)
			captured, _, violations, warnings, err := config.LoadForCapture(env)
			require.NoError(t, err)

			require.Equal(t, loaded, captured, "the two loaders must agree on the effective configuration")
			require.Equal(t, config.ViolationsFromWarnings(loadWarns), violations,
				"the two loaders must agree on the §11.3 violations")
			for _, w := range warnings {
				require.Contains(t, loadWarns, w, "every capture warning is one Load reports too")
			}
		})
	}
}
