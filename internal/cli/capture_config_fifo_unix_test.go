//go:build unix

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/paths"
)

// fifoHangWatchdog bounds how long TestHookCapture_FIFORecordDoesNotBlockTheHook waits before it
// reports a hook stuck on the record. It is a hang detector, not a performance bound: the fixed
// path never makes a blocking open, so a passing run returns in one hook's time and never comes near
// it, and only a regression ever reaches it.
const fifoHangWatchdog = 30 * time.Second

// TestHookCapture_FIFORecordDoesNotBlockTheHook is wave 20's review finding on the compare-first
// read. Comparing the §11.3 list with state/config-violations.json before writing it opened the
// record with a plain open(2), so a FIFO at that path blocked every hook, for as long as a violation
// stayed in force, until the host's hook timeout killed it (the reviewer's probe: rc=143 after 15 s,
// where the old unconditional rename returned at once). The record is now opened no-follow and
// non-blocking and read only when it is a plain file of the expected size; anything else is replaced
// by the atomic write, as before the compare existed.
func TestHookCapture_FIFORecordDoesNotBlockTheHook(t *testing.T) {
	root := t.TempDir()
	writeAdmissionConfig(t, root, `{"runtime":{"telemetry":{"enabled":true}}}`)
	l := paths.Of(root)
	require.NoError(t, os.MkdirAll(l.Logs, 0o700))
	require.NoError(t, os.MkdirAll(l.State, 0o700))
	record := filepath.Join(l.State, configViolationsFile)
	require.NoError(t, syscall.Mkfifo(record, 0o600))
	t.Chdir(root)

	env := Env{Getenv: noEnv, Clock: testClock(), HomeDir: t.TempDir()}
	env.Stdin = bytes.NewReader(entryPayload(t, hookio.EventUserPromptSubmit, root))
	type result struct {
		code      int
		out, errw string
	}
	done := make(chan result, 1)
	go func() {
		var out, errw bytes.Buffer
		code := Dispatch(context.Background(), All(), []string{"qompack", "observe", "prompt"}, env, &out, &errw)
		done <- result{code, out.String(), errw.String()}
	}()

	select {
	case r := <-done:
		require.Equal(t, ExitOK, r.code, "stderr=%s", r.errw)
	case <-time.After(fifoHangWatchdog):
		// Release the blocked reader so the goroutine ends with the test: a writer that opens and
		// closes the FIFO gives the hook's open its peer and its read an EOF.
		if w, err := os.OpenFile(record, os.O_WRONLY, 0); err == nil {
			_ = w.Close()
		}
		<-done
		t.Fatal("the hook blocked on a FIFO at state/config-violations.json")
	}

	fi, err := os.Lstat(record)
	require.NoError(t, err)
	require.True(t, fi.Mode().IsRegular(), "the record was replaced by a plain file, mode %v", fi.Mode())
	raw, err := os.ReadFile(record)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"Key": "runtime.telemetry.enabled"`)
}
