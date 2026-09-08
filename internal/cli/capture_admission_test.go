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
					"tool_input":    map[string]any{"password": admissionSecret},
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
		`{"runtime":{"migration":{"capture":{"rawEvidence":true}}}}`,
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

func TestHookCapture_HardBoundPrecedesConfiguration(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(paths.Of(root).Run, 0o700))
	st := ipc.ReadState(root, config.Defaults())
	st.MaxPayloadBytes = ^uint32(0)
	require.NoError(t, ipc.WriteState(root, st))
	reader := &countedAdmissionReader{remaining: hookCaptureMaxBytes * 2}
	var out, errw bytes.Buffer
	lookups := 0
	code := Dispatch(context.Background(), All(), argvFor("observe prompt"), Env{
		Getenv: func(key string) string {
			if key == "QOMPACK_PROJECT_ROOT" {
				return root
			}
			lookups++
			return ""
		}, HomeDir: t.TempDir(), Stdin: reader, Clock: testClock(),
	}, &out, &errw)
	require.Equal(t, ExitOK, code)
	require.Equal(t, hookCaptureMaxBytes+1, reader.read)
	require.Zero(t, lookups, "oversize rejection precedes config environment scanning")
	require.NoDirExists(t, paths.Of(root).Spool)
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
