package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

const admissionSecret = "PRIVATE-ABCDEFGHIJKL"

func TestHookCapture_RedactsBeforeEveryLegacySpoolMode(t *testing.T) {
	for _, mode := range []string{"connect failure", "hot spool", "daemon disabled"} {
		for _, hook := range hookNames {
			t.Run(mode+"/"+hook, func(t *testing.T) {
				root := t.TempDir()
				writeAdmissionConfig(t, root, `{"runtime":{"redact":{"patterns":["PRIVATE-[A-Z]{12}"]}}}`)
				st := ipc.ReadState(root, config.Defaults())
				switch mode {
				case "hot spool":
					st.Hot = ipc.HotSpool
				case "daemon disabled":
					st.DaemonEnabled = false
				}
				require.NoError(t, os.MkdirAll(paths.Of(root).Run, 0o700))
				require.NoError(t, ipc.WriteState(root, st))
				raw, err := json.Marshal(map[string]any{
					"cwd": root, "session_id": "admission-session", "tool_name": "Read",
					"tool_input":    map[string]any{"password": admissionSecret, "file_path": "src/a.go"},
					"tool_response": admissionSecret, "prompt": admissionSecret,
					"trigger": admissionSecret, "agent_name": admissionSecret,
				})
				require.NoError(t, err)
				args := argvFor(hook)
				if hook == "observe stop" {
					args = append(args, "--subagent")
				}
				var out, errw bytes.Buffer
				code := Dispatch(context.Background(), All(), args, Env{
					Getenv: noEnv, HomeDir: t.TempDir(), Stdin: bytes.NewReader(raw), Clock: testClock(),
				}, &out, &errw)
				require.Equal(t, ExitOK, code)
				require.True(t, json.Valid(bytes.TrimSpace(out.Bytes())))
				req := onlySpooledRequest(t, root)
				require.Equal(t, core.SessionID("admission-session"), req.Session)
				require.NotNil(t, req.Event)
				require.Contains(t, req.Event.Prompt, "redacted")
				assertAdmissionTreeHasNoSecret(t, paths.Of(root).Dot)
				require.NotContains(t, out.String()+errw.String(), admissionSecret)
			})
		}
	}
}

func TestHookCapture_RefusesBeforeSpoolAndDaemonStart(t *testing.T) {
	for _, body := range []string{
		`{"runtime":`,
		`{"runtime":{"redact":{"patterns":"PRIVATE-ABCDEFGHIJKL"}}}`,
		`{"runtime":{"redact":{"patterns":["["]}}}`,
		`{"runtime":{"redact":{"enabled":false,"enabled":true}}}`,
		// runtime.mode is the capture on/off switch: a setting of it the hook path cannot apply as
		// written refuses instead of recording under the default "auto" (C1.8 review, finding 2).
		`{"runtime":{"mode":"OFF"}}`,
		`{"runtime":{"mode":false}}`,
		// A gated runtime.migration switch set to true is NOT in this list any more: since finding
		// S-7 an out-of-range VALUE clamps rather than refusing the delivery, and clamping a gate to
		// false is the safe direction — the switch still cannot be turned on by editing a file.
		// TestHookCapture_GatedSwitchClampsRatherThanDisablingCapture is that case. A newer
		// settingsVersion is the same clamp: TestHookCapture_ConfigViolationClampsAndIsRecorded.
	} {
		t.Run(body, func(t *testing.T) {
			root := t.TempDir()
			writeAdmissionConfig(t, root, body)
			var out, errw bytes.Buffer
			started := false
			raw, err := json.Marshal(map[string]any{"cwd": root, "prompt": admissionSecret})
			require.NoError(t, err)
			err = doHook(hookSpec{op: ipc.OpObservePrompt, preSend: func(string, string, ipc.State, core.Clock) {
				started = true
			}})(context.Background(), Env{
				Getenv: noEnv, HomeDir: t.TempDir(), Stdin: bytes.NewReader(raw), Clock: testClock(),
			}, nil, &out, &errw)
			require.NoError(t, err)
			require.False(t, started, "unavailable policy must precede daemon start")
			require.NoDirExists(t, paths.Of(root).Spool)
			require.Equal(t, "{}\n", out.String())
			require.Empty(t, errw.String())
		})
	}
}

func TestHookCapture_RejectsInvalidInputWithoutPrivateDiagnostics(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(paths.Of(root).Logs, 0o700))
	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), argvFor("observe prompt"), Env{
		Getenv: envWith(map[string]string{"QOMPACK_PROJECT_ROOT": root}), HomeDir: t.TempDir(),
		Stdin: privateAdmissionReader{}, Clock: testClock(),
	}, &out, &errw)
	require.Equal(t, ExitOK, code)
	require.Equal(t, "{}\n", out.String())
	require.NoDirExists(t, paths.Of(root).Spool)
	assertAdmissionTreeHasNoSecret(t, paths.Of(root).Dot)
	require.NotContains(t, out.String()+errw.String(), admissionSecret)
}

func TestHookCapture_DestinationCannotWeakenInitialPolicy(t *testing.T) {
	for _, mode := range []string{"source policy", "destination policy", "destination invalid", "destination changes cwd"} {
		t.Run(mode, func(t *testing.T) {
			source := t.TempDir()
			destination := filepath.Join(t.TempDir(), "destination-marker")
			require.NoError(t, os.MkdirAll(destination, 0o700))
			t.Chdir(source)
			if mode == "source policy" {
				writeAdmissionConfig(t, source, `{"runtime":{"redact":{"patterns":["PRIVATE-[A-Z]{12}"]}}}`)
				writeAdmissionConfig(t, destination, `{"runtime":{"redact":{"enabled":false}}}`)
			} else {
				writeAdmissionConfig(t, source, `{"runtime":{"redact":{"enabled":false}}}`)
				body := `{"runtime":{"redact":{"patterns":["PRIVATE-[A-Z]{12}"]}}}`
				if mode == "destination invalid" {
					body = `{"runtime":`
				} else if mode == "destination changes cwd" {
					body = `{"runtime":{"redact":{"patterns":["destination-marker"]}}}`
				}
				writeAdmissionConfig(t, destination, body)
			}
			raw, err := json.Marshal(map[string]any{"cwd": destination, "prompt": admissionSecret})
			require.NoError(t, err)
			var out, errw bytes.Buffer
			code := Dispatch(context.Background(), All(), argvFor("observe prompt"), Env{
				Getenv: noEnv, HomeDir: t.TempDir(), Stdin: bytes.NewReader(raw), Clock: testClock(),
			}, &out, &errw)
			require.Equal(t, ExitOK, code)
			require.NoDirExists(t, paths.Of(source).Spool)
			if strings.HasPrefix(mode, "destination invalid") || mode == "destination changes cwd" {
				require.NoDirExists(t, paths.Of(destination).Spool)
			} else {
				require.Contains(t, onlySpooledRequest(t, destination).Event.Prompt, "redacted")
			}
			assertAdmissionTreeHasNoSecret(t, paths.Of(source).Dot)
			assertAdmissionTreeHasNoSecret(t, paths.Of(destination).Dot)
		})
	}
}

func TestHookCapture_CompositionPreservesEarlierFacts(t *testing.T) {
	prior := hookio.Capture{PolicyVersion: "redact-json/v1", Redacted: true, Fidelity: core.FidelityRedacted}
	next := hookio.Capture{PolicyVersion: "redact-json/v1", Fidelity: core.FidelityExact, Bytes: []byte(`{}`)}
	got := composeHookCapture(prior, next)
	require.True(t, got.Redacted)
	require.Equal(t, core.FidelityRedacted, got.Fidelity)
	require.Equal(t, "redact-json/v1+redact-json/v1", got.PolicyVersion)
	require.Equal(t, next.Bytes, got.Bytes)
}

// TestHookCapture_HardBoundPrecedesConfiguration pins the hard allocation cap's ORDERING: neither
// a state record claiming ^uint32(0) nor any configuration can enlarge the read past
// hookCaptureMaxBytes, and the bound fires without configuration having been consulted at all.
//
// It used to assert that too as "the environment is never scanned" and "no spool directory is ever
// created", because a delivery over the cap was dropped outright. V4-Z's ruling changed what
// happens AFTER the bound fires, not the bound itself: in a project that already has a .qompack
// store, the refusal now leaves the same explicitly-unavailable record the configured budget
// leaves, and clearing that record's prefix under the operator's own rules necessarily loads a
// configuration. So the ordering claim is now made directly — the environment is first consulted
// only once the reader has already read its last permitted byte — instead of through a proxy that
// the ruling turned into an assertion against the trace it requires. TestHooks_HardCapDelivery*
// own the record's contents and the no-store half of the ruling.
func TestHookCapture_HardBoundPrecedesConfiguration(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(paths.Of(root).Run, 0o700))
	st := ipc.ReadState(root, config.Defaults())
	st.MaxPayloadBytes = ^uint32(0)
	require.NoError(t, ipc.WriteState(root, st))
	reader := &countedAdmissionReader{remaining: hookCaptureMaxBytes * 2}
	var out, errw bytes.Buffer
	// How much of the delivery had been read when the environment was first consulted for anything
	// other than the project root and the home directory. -1 means it was never consulted at all.
	readAtFirstLookup := -1
	// How much had been read when the home directory was first looked up, which D18's refusal of a
	// home-directory root does before anything else. -1 means it was never looked up.
	readAtHomeLookup := -1
	code := Dispatch(context.Background(), All(), argvFor("observe prompt"), Env{
		Getenv: func(key string) string {
			switch key {
			case "QOMPACK_PROJECT_ROOT":
				return root
			case "HOME", "USERPROFILE":
				// Whether the resolved root may be used at all (D18) is decided from these, like the
				// root itself is from QOMPACK_PROJECT_ROOT: root resolution, not configuration.
				if readAtHomeLookup < 0 {
					readAtHomeLookup = reader.read
				}
				return ""
			}
			if readAtFirstLookup < 0 {
				readAtFirstLookup = reader.read
			}
			return ""
		}, HomeDir: t.TempDir(), Stdin: reader, Clock: testClock(),
	}, &out, &errw)
	require.Equal(t, ExitOK, code)
	require.Equal(t, hookCaptureMaxBytes+1, reader.read,
		"a state record claiming ^uint32(0) never enlarges the read past the hard allocation cap")
	require.Equal(t, hookCaptureMaxBytes+1, readAtFirstLookup,
		"the bound had already read its last byte and refused the delivery before any configuration "+
			"environment was scanned: the resource bound never depends on policy being available")
	require.Equal(t, 0, readAtHomeLookup,
		"the home-directory refusal (D18) is decided before a byte of the delivery is read")

	req := onlySpooledRequest(t, root)
	require.NotNil(t, req.Capture, "a delivery over the hard cap leaves a record, not nothing (V4-Z)")
	require.Nil(t, req.Event, "the delivery is still refused: nothing admitted it, so nothing derives an Event")
	require.Equal(t, core.FidelityTruncated, req.Capture.Fidelity)
	require.Equal(t, core.CaptureErrorOversize, req.Capture.CaptureError)
	require.Equal(t, core.OutcomeUnavailable, req.Capture.Outcome)
	require.Equal(t, hookCaptureMaxBytes+1, req.Capture.SourceBytes,
		"the record carries the size the cap observed before it stopped, not one it never measured")
	require.LessOrEqual(t, len(req.Capture.Bytes), hookCaptureRefusalPrefixBytes*2,
		"the record carries a bounded prefix, never the buffer the cap exists to refuse")
}

type countedAdmissionReader struct{ remaining, read int }

func (r *countedAdmissionReader) Read(b []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(b), r.remaining)
	for i := range b[:n] {
		b[i] = 'x'
	}
	r.remaining -= n
	r.read += n
	return n, nil
}

type privateAdmissionReader struct{}

func (privateAdmissionReader) Read([]byte) (int, error) { return 0, errors.New(admissionSecret) }

func writeAdmissionConfig(t *testing.T, root, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(paths.Of(root).Dot, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(paths.Of(root).Dot, "config.json"), []byte(body), 0o600))
}

func assertAdmissionTreeHasNoSecret(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		require.NoError(t, err)
		require.False(t, strings.Contains(string(b), admissionSecret), "private bytes persisted in %s", path)
		return nil
	}))
}

// TestHookCapture_HardCapMatchesTheValidationBound keeps the two halves of finding S-2 in step.
//
// internal/config cannot import this package (§3.2 gives config the allow-set {core}), so the
// validation bound it enforces on runtime.hotPath.maxPayloadBytes is a restated literal. This is the
// only thing that would notice if one of them moved: a smaller cap here would refuse deliveries a
// validated configuration says are legal, and a larger one would put the silent switch-off S-2
// describes back.
func TestHookCapture_HardCapMatchesTheValidationBound(t *testing.T) {
	require.Equal(t, hookCaptureMaxBytes, config.HookCaptureHardCapBytes,
		"internal/config restates this cap as a validation bound; the two must not drift")
}

// TestHookCapture_ConfigViolationClampsAndIsRecorded is finding S-7 end to end through the hook
// path: a hardwired-off switch an operator can never legitimately enable used to make
// config.LoadForCapture refuse the whole delivery, so the hook produced nothing at all. It must now
// clamp, record the violation where an operator would look, and capture the delivery anyway.
func TestHookCapture_ConfigViolationClampsAndIsRecorded(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		wantKey string
		check   func(t *testing.T, cfg config.Config)
	}{
		{
			name:    "hardwired-off switch",
			body:    `{"runtime":{"telemetry":{"enabled":true}}}`,
			wantKey: "runtime.telemetry.enabled",
			check: func(t *testing.T, cfg config.Config) {
				require.False(t, cfg.Runtime.Telemetry.Enabled, "the hardwired-off switch stays off")
			},
		},
		{
			name:    "newer settingsVersion",
			body:    `{"runtime":{"migration":{"settingsVersion":2,"reinjection":{"sessionStartCompact":false}}}}`,
			wantKey: "runtime.migration",
			check: func(t *testing.T, cfg config.Config) {
				require.Equal(t, config.Defaults().Runtime.Migration, cfg.Runtime.Migration,
					"the versioned block resets to defaults, not leaf-by-leaf")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(paths.Of(root).Run, 0o700))
			writeAdmissionConfig(t, root, tc.body)

			in := hookInput{Raw: []byte(`{"hook_event_name":"UserPromptSubmit","session_id":"sess-s7",` +
				`"cwd":` + mustJSON(t, root) + `,"prompt":"an ordinary prompt"}`)}
			capture, _, cfg, err := admitHookCapture(admissionEnv(t), root, in)
			require.NoError(t, err, "one invalid key must not disable capture")
			require.Equal(t, core.OutcomeOK, capture.Outcome, "the ordinary delivery is still admitted")
			tc.check(t, cfg)

			raw, readErr := os.ReadFile(filepath.Join(paths.Of(root).State, "config-violations.json"))
			require.NoError(t, readErr, "the clamp must be recorded in state/config-violations.json")
			require.Contains(t, string(raw), tc.wantKey)
			if tc.name == "newer settingsVersion" {
				require.Contains(t, string(raw), "reset")
			}
		})
	}
}

// TestHookCapture_NoViolationWritesNothing is the other direction: an ordinary project must not
// gain a violations file it has no violations for.
func TestHookCapture_NoViolationWritesNothing(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(paths.Of(root).Run, 0o700))

	in := hookInput{Raw: []byte(`{"hook_event_name":"UserPromptSubmit","session_id":"sess-s7b",` +
		`"cwd":` + mustJSON(t, root) + `,"prompt":"an ordinary prompt"}`)}
	_, _, _, err := admitHookCapture(admissionEnv(t), root, in)
	require.NoError(t, err)

	_, statErr := os.Stat(filepath.Join(paths.Of(root).State, "config-violations.json"))
	require.True(t, os.IsNotExist(statErr), "a clean configuration writes no §11.3 record")
}

// mustJSON renders v as a JSON literal for embedding in a hook payload.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// TestHookCapture_GatedSwitchClampsRatherThanDisablingCapture is the row finding S-7 moved out of
// TestHookCapture_RefusesBeforeSpoolAndDaemonStart.
//
// A gated runtime.migration switch set to true used to make config.LoadForCapture refuse the whole
// delivery, so one line in a config file cost the project every observation. The gate's own contract
// is only that the switch cannot be turned ON by editing a file (internal/config's migrationGates):
// restoring it to false honours that and keeps recording. A newer settingsVersion is the same
// clamp: the whole block resets to defaults and the reset is recorded (finding 4 / D8-2).
func TestHookCapture_GatedSwitchClampsRatherThanDisablingCapture(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(paths.Of(root).Run, 0o700))
	writeAdmissionConfig(t, root, `{"runtime":{"migration":{"capture":{"rawEvidence":true}}}}`)

	in := hookInput{Raw: []byte(`{"hook_event_name":"UserPromptSubmit","session_id":"sess-gate",` +
		`"cwd":` + mustJSON(t, root) + `,"prompt":"an ordinary prompt"}`)}
	capture, _, cfg, err := admitHookCapture(admissionEnv(t), root, in)
	require.NoError(t, err, "a pending gate must not disable capture")
	require.False(t, cfg.Runtime.Migration.Capture.RawEvidence, "the gate stays closed")
	require.Equal(t, core.OutcomeOK, capture.Outcome, "the ordinary delivery is still admitted")
}
