package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// runPromptHookForConfig runs the real observe-prompt hook, through Dispatch, in a project whose
// .qompack/config.json is body. The project already has a .qompack (the config file lives in it) and
// a logs/ directory, so the hook has somewhere durable to report what it could not apply. The daemon
// is unreachable, so an admitted delivery lands in the client spool.
func runPromptHookForConfig(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	writeAdmissionConfig(t, root, body)
	require.NoError(t, os.MkdirAll(paths.Of(root).Logs, 0o700))
	require.NoError(t, os.MkdirAll(paths.Of(root).Run, 0o700))
	require.NoError(t, ipc.WriteState(root, ipc.ReadState(root, config.Defaults())))

	raw, err := json.Marshal(map[string]any{
		"hook_event_name": "UserPromptSubmit", "session_id": "sess-c18", "cwd": root,
		"prompt": "an ordinary prompt",
	})
	require.NoError(t, err)
	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), argvFor("observe prompt"), Env{
		Getenv: envWith(map[string]string{"QOMPACK_PROJECT_ROOT": root}), HomeDir: t.TempDir(),
		Stdin: bytes.NewReader(raw), Clock: testClock(),
	}, &out, &errw)
	require.Equal(t, ExitOK, code, "stderr=%s", errw.String())
	require.Equal(t, "{}\n", out.String())
	return root
}

// dayLogText concatenates every day log the hook wrote under the project's logs/ directory.
func dayLogText(t *testing.T, root string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(paths.Of(root).Logs, "qompack-*.log"))
	require.NoError(t, err)
	var b strings.Builder
	for _, m := range matches {
		raw, err := os.ReadFile(m)
		require.NoError(t, err)
		b.Write(raw)
	}
	return b.String()
}

// TestHookCapture_UnknownKeyAndInvalidLeafStillCapture is V6 close-out item C1.8 end to end through
// a real hook. SP-18 found by probing that one unknown key in a project config made every hook admit
// nothing — no spool line, no daemon, no .qompack/ layout — while the hook printed `{}` and exited 0
// and `qompack self-test` said `config.load ok`. The documented contract is that an invalid value
// falls back to its default and is recorded, and that an unknown key only warns; both must hold on
// the hook path, together, in the same file.
func TestHookCapture_UnknownKeyAndInvalidLeafStillCapture(t *testing.T) {
	root := runPromptHookForConfig(t, `{"runtime":{"mode":"sideways","notAKey":1}}`)

	req := onlySpooledRequest(t, root)
	require.NotNil(t, req.Event, "the delivery was admitted, so it carries its Event")
	require.Equal(t, core.SessionID("sess-c18"), req.Session)
	require.Contains(t, req.Event.Prompt, "an ordinary prompt")

	raw, err := os.ReadFile(filepath.Join(paths.Of(root).State, configViolationsFile))
	require.NoError(t, err, "the invalid leaf's fallback must be recorded in state/config-violations.json")
	require.Contains(t, string(raw), "runtime.mode")
	require.NotContains(t, string(raw), "runtime.notAKey",
		"an unknown key is a warning, never a §11.3 violation (docs/troubleshooting.md §1)")

	log := dayLogText(t, root)
	require.Contains(t, log, "runtime.notAKey", "the unknown key must be reported where an operator looks")
	require.Contains(t, log, "unknown key")
	require.Contains(t, log, "runtime.mode")
}

// TestHookCapture_MistypedLeafStillCaptures is the wrong-type half: a leaf of the wrong JSON type
// keeps the value from the layer below (the default here), the delivery is captured, and the type
// error is reported. Only the capture privacy policy is exempt — see
// TestHookCapture_RefusesBeforeSpoolAndDaemonStart's mistyped runtime.redact.patterns row.
func TestHookCapture_MistypedLeafStillCaptures(t *testing.T) {
	body := `{"checkpoint":{"budgetTokens":"9000"},"runtime":{"notAKey":1}}`
	root := runPromptHookForConfig(t, body)

	req := onlySpooledRequest(t, root)
	require.NotNil(t, req.Event)
	require.Contains(t, req.Event.Prompt, "an ordinary prompt")

	log := dayLogText(t, root)
	require.Contains(t, log, "checkpoint.budgetTokens", "the ignored value must be reported")
	require.Contains(t, log, "invalid type")

	in := hookInput{Raw: []byte(`{"hook_event_name":"UserPromptSubmit","session_id":"sess-c18b",` +
		`"cwd":` + mustJSON(t, root) + `,"prompt":"an ordinary prompt"}`)}
	capture, _, cfg, err := admitHookCapture(admissionEnv(t), root, in)
	require.NoError(t, err)
	require.Equal(t, core.OutcomeOK, capture.Outcome)
	require.Equal(t, config.Defaults().Checkpoint.BudgetTokens, cfg.Checkpoint.BudgetTokens,
		"the mistyped leaf keeps the lower layer's value, here the default")
}
