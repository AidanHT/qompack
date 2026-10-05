//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestDoctor_FIFORecordDoesNotBlockDoctor is finding #86. Wave 20 made the hook's read of
// state/config-violations.json non-blocking (TestHookCapture_FIFORecordDoesNotBlockTheHook), but
// doctor still opened the same record with a plain blocking open(2), so a FIFO at that path hung
// `qompack doctor` and /qompack:doctor forever on Linux and macOS: an O_RDONLY open of a FIFO waits
// for a writer. doctor now opens the record no-follow and non-blocking and reads only a regular file,
// so it reports the record as not read and finishes. The watchdog is fifoHangWatchdog, the same hang
// detector as the hook's row: a passing run never makes a blocking open and never comes near it.
func TestDoctor_FIFORecordDoesNotBlockDoctor(t *testing.T) {
	root := t.TempDir()
	writeAdmissionConfig(t, root, `{}`)
	l := paths.Of(root)
	require.NoError(t, os.MkdirAll(l.State, 0o700))
	record := violationsRecord(root)
	require.NoError(t, syscall.Mkfifo(record, 0o600))

	done := make(chan map[string]any, 1)
	go func() {
		code, doc, _ := doctorJSON(t, root)
		if code != ExitOK {
			done <- nil
			return
		}
		done <- doc
	}()

	var doc map[string]any
	select {
	case doc = <-done:
	case <-time.After(fifoHangWatchdog):
		// Give the blocked open its peer, so the goroutine ends with the test.
		if w, err := os.OpenFile(record, os.O_WRONLY, 0); err == nil {
			_ = w.Close()
		}
		<-done
		t.Fatal("doctor blocked on a FIFO at state/config-violations.json")
	}
	require.NotNil(t, doc, "doctor exits 0")
	row := doctorFindRow(t, doc, "controls", "config.violations")
	require.Equal(t, "ok", row["status"], "row=%v", row)
	require.Contains(t, row["detail"], "state/config-violations.json not read: not a regular file", "row=%v", row)

	fi, err := os.Lstat(record)
	require.NoError(t, err)
	require.Equal(t, os.ModeNamedPipe, fi.Mode().Type(), "doctor never replaces the record")
}

// TestDoctor_LinkedRecordIsNotFollowed is the link half of findings #22 and #82: doctor's read
// followed a link at state/config-violations.json, so a link to a file elsewhere was read whole and
// listed as this project's record. Like the hook path's read (recordHolds), doctor's open is now
// no-follow, and a link is no persisted list.
func TestDoctor_LinkedRecordIsNotFollowed(t *testing.T) {
	root := t.TempDir()
	writeAdmissionConfig(t, root, `{}`)
	require.NoError(t, os.MkdirAll(paths.Of(root).State, 0o700))
	outside := filepath.Join(t.TempDir(), "elsewhere.json")
	require.NoError(t, os.WriteFile(outside,
		[]byte(`[{"Key":"selection.linkedRecord","Message":"from a file outside the project"}]`), 0o600))
	require.NoError(t, os.Symlink(outside, violationsRecord(root)))

	row := doctorViolationsRow(t, root)
	require.Equal(t, "ok", row["status"], "row=%v", row)
	require.NotContains(t, row["detail"], "selection.linkedRecord", "row=%v", row)
	require.Contains(t, row["detail"], "state/config-violations.json not read:", "row=%v", row)
}
