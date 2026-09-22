package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

const captureConfigFileLimit = 1 << 20

func TestLoadForCapture_AcceptsStrictJSONCAndFiveLayerPrecedence(t *testing.T) {
	env := baseEnv(t)
	writeConfigFile(t, env.HomeDir, `{"scheduler":{"softFloorPct":0.5}}`)
	writeConfigFile(t, env.ProjectRoot, `{
  // this comment and the trailing commas are valid JSONC
  "scheduler": { "softFloorPct": 0.6, },
  /* pattern compilation belongs to redact.CapturePolicy, not config admission */
  "runtime": { "redact": { "enabled": false, "patterns": ["["], }, },
}`)
	env.Getenv = func(name string) string {
		if name == "QOMPACK_SCHEDULER__SOFTFLOORPCT" {
			return "0.7"
		}
		return ""
	}
	env.Flags = map[string]string{"scheduler.softFloorPct": "0.8"}

	cfg, prov, violations, warnings, err := config.LoadForCapture(env)
	require.NoError(t, err)
	require.Empty(t, violations)
	require.Empty(t, warnings)
	require.Equal(t, 0.8, cfg.Scheduler.SoftFloorPct)
	require.Equal(t, config.OriginFlag, prov["scheduler.softFloorPct"].Origin)
	require.False(t, cfg.Runtime.Redact.Enabled, "explicit redaction disable is a valid setting")
	require.Equal(t, []string{"["}, cfg.Runtime.Redact.Patterns,
		"configuration admission must not compile or otherwise interpret redaction patterns")
	require.Equal(t, config.OriginProjectFile, prov["runtime.redact.enabled"].Origin)
}

func TestLoadForCapture_RequiresAbsoluteRoots(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  config.Env
	}{
		{name: "missing project", env: config.Env{HomeDir: t.TempDir()}},
		{name: "missing home", env: config.Env{ProjectRoot: t.TempDir()}},
		{name: "relative project", env: config.Env{ProjectRoot: ".", HomeDir: t.TempDir()}},
		{name: "relative home", env: config.Env{ProjectRoot: t.TempDir(), HomeDir: "."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireCaptureRefusal(t, tc.env, tc.env.ProjectRoot, tc.env.HomeDir)
		})
	}
}

func TestLoadForCapture_MissingFilesRemainValid(t *testing.T) {
	cfg, prov, violations, warnings, err := config.LoadForCapture(baseEnv(t))
	require.NoError(t, err)
	require.Empty(t, violations)
	require.Empty(t, warnings)
	require.Equal(t, config.Defaults(), cfg)
	require.Equal(t, config.OriginDefault, prov["runtime.redact.enabled"].Origin)
}

func TestLoadForCapture_RefusesUnsafeConfigFileLeaves(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, env config.Env)
	}{
		{
			name: "directory instead of regular file",
			setup: func(t *testing.T, env config.Env) {
				path := captureConfigPath(env.ProjectRoot)
				require.NoError(t, os.MkdirAll(path, 0o700))
			},
		},
		{
			name: "over size limit",
			setup: func(t *testing.T, env config.Env) {
				path := captureConfigPath(env.ProjectRoot)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
				require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))
				require.NoError(t, os.Truncate(path, captureConfigFileLimit+1))
			},
		},
		{
			name: "symlink leaf",
			setup: func(t *testing.T, env config.Env) {
				path := captureConfigPath(env.ProjectRoot)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
				target := filepath.Join(t.TempDir(), "target.json")
				require.NoError(t, os.WriteFile(target, []byte(`{"runtime":{"redact":{"enabled":false}}}`), 0o600))
				if err := os.Symlink(target, path); err != nil {
					t.Skipf("platform: creating a test-owned symlink is unavailable: %v", err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := baseEnv(t)
			tc.setup(t, env)
			requireCaptureRefusal(t, env, captureConfigPath(env.ProjectRoot))
		})
	}
}

func TestLoadForCapture_RefusesAmbiguousOrInvalidJSONC(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{name: "malformed", body: []byte(`{"runtime":`)},
		{name: "top level null", body: []byte(`null`)},
		{name: "top level array", body: []byte(`[]`)},
		{name: "empty trailing comma", body: []byte(`{,}`)},
		{name: "empty array comma", body: []byte(`{"runtime":{"redact":{"patterns":[,]}}}`)},
		{name: "same spelling duplicate", body: []byte(`{"scheduler":{"softFloorPct":0.6,"softFloorPct":0.7}}`)},
		{name: "decoded spelling duplicate", body: []byte(`{"scheduler":{"softFloorPct":0.6,"\u0073oftFloorPct":0.7}}`)},
		{name: "invalid utf8", body: append(append([]byte(`{"runtime":{"logging":{"level":"`), 0xff), []byte(`"}}}`)...)},
		{name: "unpaired surrogate", body: []byte(`{"runtime":{"logging":{"level":"\ud800"}}}`)},
		{name: "unfinished block comment", body: []byte(`{"runtime":{/* unfinished`)},
		{name: "unfinished trailing comment", body: []byte(`{} /* unfinished`)},
		{name: "nonfinite number", body: []byte(`{"scheduler":{"softFloorPct":1e9999}}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := baseEnv(t)
			writeCaptureConfigBytes(t, env.ProjectRoot, tc.body)
			requireCaptureRefusal(t, env, "capture-secret", string(tc.body))
		})
	}
}

func TestLoadForCapture_BoundsEnvironmentFlagsAndPolicy(t *testing.T) {
	for _, source := range []string{"environment", "flag", "flag key", "aggregate", "rule count", "file rule width", "invalid UTF8"} {
		t.Run(source, func(t *testing.T) {
			env := baseEnv(t)
			switch source {
			case "environment":
				env.Getenv = func(name string) string {
					if name == "QOMPACK_RUNTIME__REDACT__PATTERNS" {
						return strings.Repeat("x", 64<<10+1)
					}
					return ""
				}
			case "flag":
				env.Flags = map[string]string{"runtime.redact.patterns": strings.Repeat("x", 64<<10+1)}
			case "flag key":
				env.Flags = map[string]string{strings.Repeat("x", 64<<10+1): "true"}
			case "aggregate":
				env.Getenv = func(string) string { return strings.Repeat("x", 64<<10) }
			case "rule count":
				env.Flags = map[string]string{"runtime.redact.patterns": strings.Repeat("xx,", 256) + "xx"}
			case "file rule width":
				writeConfigFile(t, env.ProjectRoot, `{"runtime":{"redact":{"patterns":["`+strings.Repeat("x", 64<<10+1)+`"]}}}`)
			case "invalid UTF8":
				env.Flags = map[string]string{"runtime.redact.patterns": string([]byte{0xff})}
			}
			requireCaptureRefusal(t, env)
		})
	}
}

// TestLoadForCapture_RefusesAnyPrivacyPolicyProblem pins the one per-leaf problem that stays a
// refusal on the capture path: anything inside runtime.redact, the subtree the capture privacy policy
// is compiled from.
//
// Criterion change (V6 close-out C1.8). This test was
// TestLoadForCapture_RefusesSchemaEnvFlagAndEffectiveConfigurationFailures, and it required a
// refusal of the WHOLE capture for an unknown key, a wrong leaf type, an unparseable environment
// value, an unknown flag and an unparseable flag value anywhere in the schema. That was the defect,
// not the contract: one such line made every hook admit nothing, while the documented contract
// (README "Configuration", docs/config-reference.md) is that an invalid value falls back and an
// unknown key only warns. Those five rows moved, with the same inputs, to captureFallbackCases,
// where they must now fall back per leaf and agree with config.Load.
//
// What they did guard is kept here, narrowed to where it is a security bound rather than an outage:
// every fallback inside runtime.redact would capture under a weaker policy than the operator wrote,
// so a problem there still refuses, reveals nothing, and names only its class.
func TestLoadForCapture_RefusesAnyPrivacyPolicyProblem(t *testing.T) {
	for _, tc := range []struct {
		name  string
		user  string
		file  string
		env   func(string) string
		flags map[string]string
	}{
		{
			name: "policy leaf of the wrong type",
			file: `{"runtime":{"redact":{"patterns":"capture-secret"}}}`,
		},
		{
			// Falling back here would leave the user layer's false in force: capture unredacted.
			name: "mistyped enable over a lower layer's disable",
			user: `{"runtime":{"redact":{"enabled":false}}}`,
			file: `{"runtime":{"redact":{"enabled":"capture-secret"}}}`,
		},
		{
			name: "unknown key inside the policy",
			file: `{"runtime":{"redact":{"capture-secret":["x"]}}}`,
		},
		{
			name: "policy section that is not an object",
			file: `{"runtime":{"redact":"capture-secret"}}`,
		},
		{
			name: "unparseable environment value for a policy leaf",
			env: func(name string) string {
				if name == "QOMPACK_RUNTIME__REDACT__ENABLED" {
					return "capture-secret"
				}
				return ""
			},
		},
		{
			name:  "unparseable flag value for a policy leaf",
			flags: map[string]string{"runtime.redact.enabled": "capture-secret"},
		},
		{
			name:  "unknown flag inside the policy",
			flags: map[string]string{"runtime.redact.capture-secret": "x"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := baseEnv(t)
			if tc.user != "" {
				writeConfigFile(t, env.HomeDir, tc.user)
			}
			if tc.file != "" {
				writeConfigFile(t, env.ProjectRoot, tc.file)
			}
			if tc.env != nil {
				env.Getenv = tc.env
			}
			env.Flags = tc.flags
			requireCaptureRefusal(t, env, "capture-secret", tc.file)
			_, _, _, _, err := config.LoadForCapture(env)
			require.ErrorContains(t, err, "runtime.redact setting cannot be applied as written")
		})
	}
}

// TestLoadForCapture_PolicyAncestorProblemDoesNotRefuse is the boundary of the rule above: a `runtime`
// that is not an object carries no privacy rule to lose. Its layer is dropped with a warning and the
// policy a lower layer set stays exactly as written.
func TestLoadForCapture_PolicyAncestorProblemDoesNotRefuse(t *testing.T) {
	env := baseEnv(t)
	writeConfigFile(t, env.HomeDir, `{"runtime":{"redact":{"patterns":["PRIVATE-[A-Z]{12}"]}}}`)
	writeConfigFile(t, env.ProjectRoot, `{"runtime":5}`)

	cfg, _, _, warnings, err := config.LoadForCapture(env)
	require.NoError(t, err)
	require.Equal(t, []string{"PRIVATE-[A-Z]{12}"}, cfg.Runtime.Redact.Patterns)
	require.True(t, cfg.Runtime.Redact.Enabled)
	require.Len(t, warnings, 1)
	require.Equal(t, "runtime", warnings[0].Key)
}

// TestLoadForCapture_ReturnsWhatItDidNotApply: a per-leaf fallback is only honest if the caller can
// report it. Every dropped key comes back as a Warning naming it, and every clamped leaf as a
// Violation, so the hook path can put both where an operator looks.
func TestLoadForCapture_ReturnsWhatItDidNotApply(t *testing.T) {
	env := baseEnv(t)
	writeConfigFile(t, env.ProjectRoot,
		`{"runtime":{"mode":"sideways","notAKey":1},"checkpoint":{"budgetTokens":"9000"}}`)

	_, prov, violations, warnings, err := config.LoadForCapture(env)
	require.NoError(t, err)

	require.Len(t, violations, 1)
	require.Equal(t, "runtime.mode", violations[0].Key)
	require.Equal(t, config.OriginDefault, prov["runtime.mode"].Origin, "provenance records the fallback")

	byKey := map[string]string{}
	for _, w := range warnings {
		byKey[w.Key] = w.Message
	}
	require.Equal(t, "unknown key", byKey["runtime.notAKey"])
	require.Contains(t, byKey["checkpoint.budgetTokens"], "invalid type")
	require.Len(t, warnings, 2)
}

// TestLoadForCapture_RefusalNamesItsStructuralClassOnly: a refusal must tell an operator which rule
// was broken — `self-test` and `doctor` print it — and still reveal nothing about the input.
func TestLoadForCapture_RefusalNamesItsStructuralClassOnly(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, env *config.Env)
		class string
	}{
		{
			name:  "relative root",
			setup: func(_ *testing.T, env *config.Env) { env.ProjectRoot = "capture-secret" },
			class: "project or home root is not an absolute path",
		},
		{
			name: "project file not strict JSONC",
			setup: func(t *testing.T, env *config.Env) {
				writeConfigFile(t, env.ProjectRoot, `{"capture-secret":`)
			},
			class: "the project config file is not a single strict JSONC object",
		},
		{
			name: "user file not a regular leaf",
			setup: func(t *testing.T, env *config.Env) {
				require.NoError(t, os.MkdirAll(filepath.Join(env.HomeDir, ".qompack", "config.json"), 0o700))
			},
			class: "the user config file is not a bounded regular file",
		},
		{
			name: "oversize flag",
			setup: func(_ *testing.T, env *config.Env) {
				env.Flags = map[string]string{"capture-secret": strings.Repeat("x", 64<<10+1)}
			},
			class: "an environment or --set value exceeds the capture bounds",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := baseEnv(t)
			tc.setup(t, &env)
			_, _, _, _, err := config.LoadForCapture(env)
			require.ErrorIs(t, err, core.ErrDegraded)
			require.ErrorContains(t, err, tc.class)
			require.NotContains(t, err.Error(), "capture-secret")
		})
	}
}

func requireCaptureRefusal(t *testing.T, env config.Env, hidden ...string) {
	t.Helper()
	cfg, prov, violations, warnings, err := config.LoadForCapture(env)
	require.ErrorIs(t, err, core.ErrDegraded)
	require.Equal(t, config.Config{}, cfg)
	require.Nil(t, prov)
	require.Nil(t, violations)
	require.Nil(t, warnings)
	for _, forbidden := range hidden {
		if forbidden != "" {
			require.NotContains(t, err.Error(), forbidden, "capture admission errors must not reveal inputs")
		}
	}
}

func captureConfigPath(root string) string {
	return filepath.Join(root, ".qompack", "config.json")
}

func writeCaptureConfigBytes(t *testing.T, root string, body []byte) {
	t.Helper()
	path := captureConfigPath(root)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, body, 0o600))
}

// TestLoadForCapture_ClampsViolationsInsteadOfRefusing is finding S-7.
//
// LoadForCapture used to end with `if len(cfg.Validate()) != 0 { return fail() }`: ANY violation, in
// ANY key, made the whole capture unavailable with core.ErrDegraded, so internal/cli's hook path
// returned empty output before session-start reached its daemon bootstrap and the project lost
// recording entirely over one bad key. §11.3's contract for an invalid value is the opposite —
// clamp to the default and record the violation — and that is what `config print` does through the
// tolerant loader. Two loaders, opposite meanings, and the hot one failed closed over the product.
//
// The key used here is deliberately unrelated to capture: it is a hardwired-off switch an operator
// can never legitimately enable, so what is measured is what a VIOLATION does, not what this key
// does.
func TestLoadForCapture_ClampsViolationsInsteadOfRefusing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		file    string
		wantKey string
		check   func(t *testing.T, cfg config.Config)
	}{
		{
			name:    "hardwired-off switch",
			file:    `{"runtime":{"telemetry":{"enabled":true}}}`,
			wantKey: "runtime.telemetry.enabled",
			check: func(t *testing.T, cfg config.Config) {
				require.False(t, cfg.Runtime.Telemetry.Enabled)
			},
		},
		{
			name:    "payload bound above the hard capture cap",
			file:    `{"runtime":{"hotPath":{"maxPayloadBytes":33554432}}}`,
			wantKey: "runtime.hotPath.maxPayloadBytes",
			check: func(t *testing.T, cfg config.Config) {
				require.LessOrEqual(t, cfg.Runtime.HotPath.MaxPayloadBytes,
					config.HookCaptureHardCapBytes)
			},
		},
		{
			name:    "relational invalidity",
			file:    `{"store":{"chunk":{"target":0}}}`,
			wantKey: "store.chunk.min",
			check: func(t *testing.T, cfg config.Config) {
				require.Empty(t, cfg.Validate())
			},
		},
		{
			name:    "future migration version",
			file:    `{"runtime":{"migration":{"settingsVersion":2,"reinjection":{"sessionStartCompact":false}}}}`,
			wantKey: "runtime.migration",
			check: func(t *testing.T, cfg config.Config) {
				require.Equal(t, config.Defaults().Runtime.Migration, cfg.Runtime.Migration,
					"a newer settingsVersion resets the whole block, including switches this build knows")
			},
		},
		{
			name:    "future phase7 version",
			file:    `{"runtime":{"phase7":{"settingsVersion":2}}}`,
			wantKey: "runtime.phase7",
			check: func(t *testing.T, cfg config.Config) {
				require.Equal(t, config.Defaults().Runtime.Phase7, cfg.Runtime.Phase7)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := baseEnv(t)
			writeConfigFile(t, env.ProjectRoot, tc.file)

			cfg, prov, violations, warnings, err := config.LoadForCapture(env)
			require.NoError(t, err, "a violation must clamp, not disable capture")
			require.Empty(t, warnings, "a clamp is a violation, not a warning")
			require.NotNil(t, prov)
			require.Empty(t, cfg.Validate(), "the returned configuration must be valid")
			require.NotEmpty(t, violations, "the clamp must be returned so a caller can record it")
			tc.check(t, cfg)

			var keys []string
			for _, v := range violations {
				keys = append(keys, v.Key)
			}
			require.Contains(t, keys, tc.wantKey)
		})
	}
}

// TestLoadForCapture_CleanConfigReturnsNoViolations keeps the S-7 fix honest in the other
// direction: an ordinary project must not start reporting violations it does not have.
// TestLoadAndLoadForCapture_AgreeOnNewerSettingsVersion is the D8-2 close: both loaders feed
// the same file and produce the same reset block and the same warning text. Load reports it as a
// Warning; LoadForCapture returns the same text on a Violation so the hook can persist it.
func TestLoadAndLoadForCapture_AgreeOnNewerSettingsVersion(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		key  string
		got  func(config.Config) any
		want func() any
	}{
		{
			name: "migration",
			file: `{"runtime":{"migration":{"settingsVersion":2,"reinjection":{"sessionStartCompact":false}}}}`,
			key:  "runtime.migration",
			got:  func(c config.Config) any { return c.Runtime.Migration },
			want: func() any { return config.Defaults().Runtime.Migration },
		},
		{
			name: "phase7",
			file: `{"runtime":{"phase7":{"settingsVersion":2}}}`,
			key:  "runtime.phase7",
			got:  func(c config.Config) any { return c.Runtime.Phase7 },
			want: func() any { return config.Defaults().Runtime.Phase7 },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := baseEnv(t)
			writeConfigFile(t, env.ProjectRoot, tc.file)

			loaded, _, loadWarns, err := config.Load(env)
			require.NoError(t, err)
			captured, _, capViolations, _, err := config.LoadForCapture(env)
			require.NoError(t, err)

			require.Equal(t, tc.want(), tc.got(loaded))
			require.Equal(t, tc.got(loaded), tc.got(captured),
				"Load and LoadForCapture must reset the same block to the same defaults")

			var loadMsg, capMsg string
			for _, w := range loadWarns {
				if w.Key == tc.key {
					loadMsg = w.Message
				}
			}
			for _, v := range capViolations {
				if v.Key == tc.key {
					capMsg = v.Message
				}
			}
			require.NotEmpty(t, loadMsg, "Load must warn about the reset")
			require.Equal(t, loadMsg, capMsg, "the two loaders must report the same warning text")
			require.Contains(t, loadMsg, "newer than this build understands")
			require.Contains(t, loadMsg, "reset")
		})
	}
}

func TestLoadForCapture_CleanConfigReturnsNoViolations(t *testing.T) {
	cfg, prov, violations, warnings, err := config.LoadForCapture(baseEnv(t))
	require.NoError(t, err)
	require.Empty(t, violations)
	require.Empty(t, warnings)
	require.Equal(t, config.Defaults(), cfg)
	require.NotNil(t, prov)
}
