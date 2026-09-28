package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
)

// TestDaemon_RefusesWhenDaemonDisabled pins coordinator decision D36(b): `qompack daemon` itself
// refuses to run in a project whose runtime.daemon.enabled is false, so docs/release.md §4's
// "no resident process and no lock file" holds whoever starts it — an older binary's spawner that
// never checked the switch, or an operator's hand start — and not only while every spawner in this
// build checks it.
//
// The refusal must come before anything is created: no .qompack/run/, no daemon.lock, no
// spawn.lock, no socket, and nothing else new under .qompack/ (the logs directory the daemon would
// open included). It exits non-zero with one line that says the daemon is disabled for this
// project and names the key that enables it.
//
// Each case disables the daemon through a different layer, because config.Load merges four
// (user file, project file, environment, --set) and the refusal must honour the merged value.
func TestDaemon_RefusesWhenDaemonDisabled(t *testing.T) {
	const disabled = `{"runtime":{"daemon":{"enabled":false}}}`
	cases := []struct {
		name    string
		project string // .qompack/config.json body, "" for none
		user    string // <home>/.qompack/config.json body, "" for none
		env     map[string]string
		argv    []string // extra args after `daemon --project <root>`
		source  string   // a fragment the refusal line must carry to name where the value came from
	}{
		{name: "project file", project: disabled, source: "config.json"},
		{name: "user file", user: disabled, source: "config.json"},
		{
			name: "environment", env: map[string]string{"QOMPACK_RUNTIME__DAEMON__ENABLED": "false"},
			source: "QOMPACK_RUNTIME__DAEMON__ENABLED",
		},
		{name: "set flag", argv: []string{"--set", "runtime.daemon.enabled=false"}, source: "--set"},
		{name: "foreground", project: disabled, argv: []string{"--foreground"}, source: "config.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			home := t.TempDir()
			dot := filepath.Join(root, ".qompack")
			require.NoError(t, os.MkdirAll(dot, 0o700))
			if tc.project != "" {
				require.NoError(t, os.WriteFile(filepath.Join(dot, "config.json"), []byte(tc.project), 0o600))
			}
			if tc.user != "" {
				require.NoError(t, os.MkdirAll(filepath.Join(home, ".qompack"), 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(home, ".qompack", "config.json"), []byte(tc.user), 0o600))
			}
			before := dirEntries(t, dot)

			kv := map[string]string{"HOME": home, "USERPROFILE": home}
			for k, v := range tc.env {
				kv[k] = v
			}
			argv := append([]string{"qompack", "daemon", "--project", root}, tc.argv...)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			var out, errw bytes.Buffer
			code := Dispatch(ctx, []Cmd{{Name: "daemon", Run: runDaemon}}, argv, Env{
				Getenv: envWith(kv), HomeDir: home, Stdin: bytes.NewReader(nil), Clock: testClock(),
			}, &out, &errw)

			require.NotEqual(t, ExitOK, code, "a daemon-disabled project must refuse with a non-zero exit; stderr=%s",
				errw.String())
			msg := strings.TrimRight(errw.String(), "\n")
			t.Logf("refusal: %s", msg)
			require.NotEmpty(t, msg, "the refusal must say why")
			require.NotContains(t, msg, "\n", "the refusal must be exactly one line")
			require.Contains(t, msg, "disabled for this project")
			require.Contains(t, msg, "runtime.daemon.enabled", "the refusal must name the key that enables the daemon")
			require.Contains(t, msg, tc.source, "the refusal must name where the false value came from")
			require.NotContains(t, msg, "starting for project", "--foreground must not announce a start it refuses")
			require.Empty(t, out.String())

			require.NoDirExists(t, filepath.Join(dot, "run"), "the refusal must come before .qompack/run/ exists")
			require.NoFileExists(t, daemon.LockPath(root))
			require.Equal(t, before, dirEntries(t, dot), "the refusal must create nothing under .qompack/")

			addr, err := ipc.Resolve(root)
			require.NoError(t, err)
			if addr.Kind == ipc.UnixSocket {
				_, statErr := os.Lstat(addr.Path)
				require.True(t, os.IsNotExist(statErr), "the refusal must not create the socket %s", addr.Path)
			}
			require.False(t, ipc.Probe(addr, 50*time.Millisecond), "nothing may be listening for a disabled daemon")
		})
	}
}

// dirEntries lists a directory's names, for a before/after "nothing was created" comparison.
func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

// TestDaemon_RunsWhenTheMergedConfigEnablesIt is the control for TestDaemon_RefusesWhenDaemonDisabled:
// the refusal reads the MERGED value, not any one layer. A user-global file that disables the
// daemon, overridden by a project file that enables it, must leave the enabled path exactly as it
// was: the daemon starts, becomes reachable and exits 0 on a clean stop.
func TestDaemon_RunsWhenTheMergedConfigEnablesIt(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".qompack"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".qompack", "config.json"),
		[]byte(`{"runtime":{"daemon":{"enabled":true}}}`), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".qompack"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".qompack", "config.json"),
		[]byte(`{"runtime":{"daemon":{"enabled":false}}}`), 0o600))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	var errw bytes.Buffer
	go func() {
		var out bytes.Buffer
		done <- Dispatch(ctx, []Cmd{{Name: "daemon", Run: runDaemon}},
			[]string{"qompack", "daemon", "--project", root}, Env{
				Getenv: envWith(map[string]string{"HOME": home, "USERPROFILE": home}), HomeDir: home,
				Stdin: bytes.NewReader(nil), Clock: testClock(),
			}, &out, &errw)
	}()

	require.Eventually(t, func() bool {
		addr, err := ipc.Resolve(root)
		return err == nil && ipc.Probe(addr, 50*time.Millisecond)
	}, 5*time.Second, 20*time.Millisecond, "a project that re-enables the daemon must get one")

	cancel()
	select {
	case code := <-done:
		require.Equal(t, ExitOK, code, "stderr=%s", errw.String())
	case <-time.After(30 * time.Second):
		t.Fatal("runDaemon did not return after its context was cancelled")
	}
}
