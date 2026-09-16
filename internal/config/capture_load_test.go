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

	cfg, prov, violations, err := config.LoadForCapture(env)
	require.NoError(t, err)
	require.Empty(t, violations)
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
	cfg, prov, violations, err := config.LoadForCapture(baseEnv(t))
	require.NoError(t, err)
	require.Empty(t, violations)
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

func TestLoadForCapture_RefusesSchemaEnvFlagAndEffectiveConfigurationFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		file  string
		env   func(string) string
		flags map[string]string
	}{
		{
			name: "unknown key",
			file: `{"capture-secret":true}`,
		},
		{
			name: "wrong leaf type",
			file: `{"scheduler":{"softFloorPct":"wrong"}}`,
		},
		// An out-of-range VALUE is no longer a refusal — finding S-7: it clamps, and
		// TestLoadForCapture_ClampsViolationsInsteadOfRefusing covers that. A newer
		// settingsVersion is the same clamp (the whole block resets); those rows live there too.
		{
			name: "invalid environment value",
			env: func(name string) string {
				if name == "QOMPACK_SCHEDULER__SOFTFLOORPCT" {
					return "NaN"
				}
				return ""
			},
		},
		{
			name:  "unknown flag",
			flags: map[string]string{"unknown.capture.option": "capture-secret"},
		},
		{
			name:  "invalid flag value",
			flags: map[string]string{"scheduler.softFloorPct": "NaN"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := baseEnv(t)
			if tc.file != "" {
				writeConfigFile(t, env.ProjectRoot, tc.file)
			}
			env.Getenv = tc.env
			env.Flags = tc.flags
			requireCaptureRefusal(t, env, "capture-secret", tc.file)
		})
	}
}

func requireCaptureRefusal(t *testing.T, env config.Env, hidden ...string) {
	t.Helper()
	cfg, prov, violations, err := config.LoadForCapture(env)
	require.ErrorIs(t, err, core.ErrDegraded)
	require.Equal(t, config.Config{}, cfg)
	require.Nil(t, prov)
	require.Nil(t, violations)
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

			cfg, prov, violations, err := config.LoadForCapture(env)
			require.NoError(t, err, "a violation must clamp, not disable capture")
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
			captured, _, capViolations, err := config.LoadForCapture(env)
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
	cfg, prov, violations, err := config.LoadForCapture(baseEnv(t))
	require.NoError(t, err)
	require.Empty(t, violations)
	require.Equal(t, config.Defaults(), cfg)
	require.NotNil(t, prov)
}
