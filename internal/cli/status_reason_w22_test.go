package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// Audit 2's status findings #11 and #12 (wave 22): the reason status and doctor give when no live
// answer came must describe the client that was built.

// TestStatus_ModeOffNamesTheModeNotADecodeError is #12: with runtime.mode off the command client
// answers at once, OK with no data, and asks no daemon. Status decoded that empty answer and
// reported "decoding status: unexpected end of JSON input", an internal error for what is the
// operator's own setting.
func TestStatus_ModeOffNamesTheModeNotADecodeError(t *testing.T) {
	root := bootstrapProject(t)
	writeProjectConfig(t, root, `{"runtime":{"mode":"off"}}`)
	out, env := statusConnectRead(t, root)
	require.NotEqual(t, "daemon", env.Data.Primary.Source, "stdout=%s", out)
	require.NotContains(t, env.Data.Primary.Reason, "decoding status", "stdout=%s", out)
	require.Contains(t, env.Data.Primary.Reason, "runtime.mode is off", "stdout=%s", out)
}

// TestDoctor_NoDaemonReasonNeverSaysItAskedOneToStart is #11: doctor strips Self, so its status
// client never starts a daemon, but its status.primary row quoted status's own reason, "This
// command asked one to start", beside its own "doctor never SPAWNS a daemon".
func TestDoctor_NoDaemonReasonNeverSaysItAskedOneToStart(t *testing.T) {
	root := bootstrapProject(t)
	_, doc, errw := doctorJSON(t, root)
	primary := doctorFindRow(t, doc, "status", "status.primary")
	detail, _ := primary["detail"].(string)
	require.Contains(t, detail, "no daemon answered", "stderr=%s", errw)
	require.NotContains(t, detail, "asked one to start", "doctor starts no daemon: %s", detail)
	require.Contains(t, detail, "does not start one", "the reason says this command starts none: %s", detail)
}

// TestStatus_NoDaemonReasonSaysItAskedOneToStartOnlyWhenItCan pins the reason against the client
// that was built: status with an executable to start a daemon from asks one to start and says so;
// the same read with none (every Env a test builds, and doctor's) says it starts none.
func TestStatus_NoDaemonReasonSaysItAskedOneToStartOnlyWhenItCan(t *testing.T) {
	var mu sync.Mutex
	var spawned []string
	prev := spawnDaemon
	spawnDaemon = func(root, _ string) error {
		mu.Lock()
		defer mu.Unlock()
		spawned = append(spawned, root)
		return nil
	}
	t.Cleanup(func() { spawnDaemon = prev })

	for _, self := range []string{"", "qompack-self"} {
		t.Run(map[bool]string{false: "no executable", true: "with an executable"}[self != ""], func(t *testing.T) {
			mu.Lock()
			spawned = nil
			mu.Unlock()
			root := bootstrapProject(t)
			env := Env{
				Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": root}),
				Stdin:   bytes.NewReader(nil),
				Clock:   testClock(),
				HomeDir: t.TempDir(),
			}
			if self != "" {
				env.Self = filepath.Join(t.TempDir(), self)
			}
			var out, errw bytes.Buffer
			require.Equal(t, ExitOK, Dispatch(context.Background(), All(), []string{"qompack", "status", "--json"},
				env, &out, &errw), "stderr=%s", errw.String())
			var got statusOrderEnvelope
			require.NoError(t, json.Unmarshal(out.Bytes(), &got), "stdout=%s", out.String())
			reason := got.Data.Primary.Reason
			mu.Lock()
			asked := len(spawned) > 0
			mu.Unlock()
			require.Equal(t, self != "", asked, "only a client with an executable asks a daemon to start")
			require.Contains(t, reason, "no daemon answered", reason)
			if self != "" {
				require.Contains(t, reason, "asked one to start", reason)
				require.NotContains(t, reason, "unless runtime.daemon.enabled is false",
					"a disabled daemon has its own reason, so the clause is unreachable: %s", reason)
				return
			}
			require.NotContains(t, reason, "asked one to start", reason)
			require.Contains(t, reason, "does not start one", reason)
		})
	}
}
