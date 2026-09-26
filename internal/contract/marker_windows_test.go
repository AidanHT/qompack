package contract

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
)

// The daemon replaces run/marker.json with paths.WriteAtomic at every SessionEnd and PreCompact.
// On Windows that replace is a rename issued through a handle opened with DELETE access on the
// staging file, and the handle stays open, now naming run/marker.json, until the rename call
// returns. While it is open, an ordinary os.ReadFile of the marker (share mode READ|WRITE, no
// DELETE) fails with ERROR_SHARING_VIOLATION; and the other way round, an ordinary read handle held
// by a reader makes the replace itself fail, and WriteMarker is never retried. Both halves are one
// fact: readMarker must take its handle with FILE_SHARE_DELETE (paths.ReadFileShared).
//
// The reader half is the one a test can pin deterministically, by holding the same kind of handle
// the replace holds. The writer half is pinned structurally by test/guards' sharedReaders row for
// readMarker, since readMarker closes its handle before it returns.

// holdReplaceHandle opens p the way a finishing rename-replace has it open — DELETE access, every
// share mode granted — and keeps it open until the test ends.
func holdReplaceHandle(t *testing.T, p string) {
	t.Helper()
	name, err := windows.UTF16PtrFromString(p)
	require.NoError(t, err)
	h, err := windows.CreateFile(name, windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = windows.CloseHandle(h) })

	_, rerr := os.ReadFile(p)
	require.Error(t, rerr, "fixture sanity: an ordinary read must be refused while the replace's "+
		"DELETE handle is open; if Windows stops refusing it, this fixture no longer reproduces the "+
		"in-flight replace and the rows below prove nothing")
}

func TestReadMarker_ReadsThroughAReplaceStillFinishing(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, WriteMarker(root, core.SessionID("sess-prior"), core.UnixMilli(1000)))
	holdReplaceHandle(t, MarkerPath(root))

	rec, err := readMarker(root)
	require.NoError(t, err, "a marker whose replace is still finishing is present, not unreadable")
	require.Equal(t, core.SessionID("sess-prior"), rec.Session)
}

// TestSessionStartFires_AMarkerMidReplaceIsNotAnAbsence is the consequence. A project that already
// missed one marker (StartsWithoutMarker 1) starts a new session while the previous session's
// terminal hook is finishing its marker replace. With an ordinary read the marker is reported
// unreadable, which this check counts as a second consecutive absence and turns into a SevCritical
// §12.1 degradation although the hook fired.
func TestSessionStartFires_AMarkerMidReplaceIsNotAnAbsence(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, WriteMarker(root, core.SessionID("sess-b"), core.UnixMilli(2000)))
	holdReplaceHandle(t, MarkerPath(root))

	h := &SessionHistory{SessionCount: 4, LastSessionID: "sess-b", StartsWithoutMarker: 1}
	r := checkSessionStartFires(context.Background(), Env{
		ProjectRoot: root, History: h, Event: hookio.Event{SessionID: "sess-c"},
	})
	require.True(t, r.OK, "a marker the previous session's terminal hook is still replacing is proof the hook fired: %+v", r)
	require.Equal(t, "marker-found", r.Observed)
	require.Equal(t, 0, h.StartsWithoutMarker)
}
