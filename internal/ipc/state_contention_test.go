package ipc

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// TestIsStateFileContention_ClassifiesOnlyTheTwoTransientWindowsErrors asserts the retry
// classifier both ReadState and WriteState now gate on.
//
// The whole classifier is gated on runtime.GOOS == "windows" — the two error numbers have
// unrelated POSIX meanings (32 is EPIPE there) — so every case below expects false off Windows,
// which is exactly what the gate promises. This is the same shape internal/store's
// TestIsRenameContention_ClassifiesOnlyTheTwoTransientWindowsErrors uses for its own copy of the
// predicate, and for the same reason: getting it wrong in either direction is expensive.
func TestIsStateFileContention_ClassifiesOnlyTheTwoTransientWindowsErrors(t *testing.T) {
	t.Parallel()

	pathErr := func(op string, e error) error {
		return &os.PathError{Op: op, Path: `C:\proj\.qompack\run\state.bin`, Err: e}
	}
	linkErr := func(e error) error {
		return &os.LinkError{Op: "rename", Old: `.qompack\tmp\wa-1`, New: `.qompack\run\state.bin`, Err: e}
	}

	cases := []struct {
		name        string
		err         error
		wantWindows bool
	}{
		{name: "no error is not contention", err: nil},
		{name: "bare ERROR_ACCESS_DENIED", err: winErrAccessDenied, wantWindows: true},
		{name: "bare ERROR_SHARING_VIOLATION", err: winErrSharingViolation, wantWindows: true},
		{name: "a reader's open hitting ERROR_SHARING_VIOLATION", err: pathErr("open", winErrSharingViolation), wantWindows: true},
		{name: "a writer's rename hitting ERROR_SHARING_VIOLATION", err: linkErr(winErrSharingViolation), wantWindows: true},
		{name: "a writer's rename hitting ERROR_ACCESS_DENIED", err: linkErr(winErrAccessDenied), wantWindows: true},
		{name: "ERROR_FILE_NOT_FOUND is the ordinary first-run case", err: pathErr("open", syscall.Errno(2))},
		{name: "ERROR_PATH_NOT_FOUND is a real failure", err: pathErr("open", syscall.Errno(3))},
		{name: "ERROR_DISK_FULL is a real failure", err: linkErr(syscall.Errno(112))},
		{name: "os.ErrNotExist is a real failure", err: os.ErrNotExist},
		{name: "a plain error is a real failure", err: errors.New("something else went wrong")},
		{
			name:        "a permission error carrying no errno still classifies",
			err:         fmt.Errorf("wrapped: %w", os.ErrPermission),
			wantWindows: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			want := c.wantWindows && runtime.GOOS == "windows"
			require.Equal(t, want, isStateFileContention(c.err),
				"isStateFileContention(%v) must be %v on %s", c.err, want, runtime.GOOS)
		})
	}
}

// TestWriteStateRetryPredicateCoversTheSharingViolation is the reason WriteState no longer gates
// its retry on os.IsPermission alone.
//
// writeStateMaxAttempts' doc comment says the retry exists to ride out "ERROR_ACCESS_DENIED when
// a concurrent ReadState ... briefly holds the destination open". The error Windows actually
// raises for a file another handle is using is ERROR_SHARING_VIOLATION (32) — that is the exact
// text a racing reader of state.bin gets — and Go's syscall.Errno.Is maps only
// ERROR_ACCESS_DENIED, EACCES and EPERM onto fs.ErrPermission, never 32
// (GOROOT/src/syscall/syscall_windows.go). So the retry loop declined to retry the one failure it
// was written for, and WriteState returned the error to a caller that discards it: a silently
// disabled gate.
//
// This asserts the gap directly rather than describing it, so a future Go release that starts
// mapping 32 onto fs.ErrPermission does not quietly turn this test into a tautology.
func TestWriteStateRetryPredicateCoversTheSharingViolation(t *testing.T) {
	t.Parallel()

	err := &os.LinkError{
		Op: "rename", Old: `.qompack\tmp\wa-1`, New: `.qompack\run\state.bin`,
		Err: winErrSharingViolation,
	}
	require.False(t, os.IsPermission(err),
		"guard: if Go ever starts mapping ERROR_SHARING_VIOLATION onto fs.ErrPermission, this "+
			"test stops proving anything and the predicate below can be simplified")
	require.Equal(t, runtime.GOOS == "windows", isRetryableStateWrite(err),
		"WriteState must retry a rename that failed with ERROR_SHARING_VIOLATION on Windows")

	// The superset property: everything the old os.IsPermission-only predicate retried is still
	// retried, so adding the errno classifier cannot have removed a retry that used to happen.
	for _, e := range []error{
		os.ErrPermission,
		&os.LinkError{Op: "rename", Old: `tmp\wa-1`, New: `run\state.bin`, Err: os.ErrPermission},
		&os.PathError{Op: "open", Path: `run\state.bin`, Err: syscall.EACCES},
	} {
		require.True(t, os.IsPermission(e),
			"guard: %v must actually be an error the OLD predicate retried", e)
		require.True(t, isRetryableStateWrite(e), "%v was retried before and must still be", e)
	}
}

// TestWriteStateLandsWhileAHookClientHoldsTheRecordOpen is the deterministic form of the failure
// TestStateReadNeverFallsBackWhileAValidRecordIsOnDisk hit on windows-latest — the WRITER losing,
// at state_test.go's `require.NoError(t, ipc.WriteState(...))`, with
// "rename ...\.qompack\tmp\wa-N ...\.qompack\run\state.bin: Access is denied".
//
// That test finds the bug by scheduling luck: four readers spinning against a writer. Measured on
// a 22-core Windows host with the fix reverted, that shape exhausts writeStateMaxAttempts and
// fails 6 runs out of 6 at GOMAXPROCS=2 (5.6 s to 25.1 s each) — and at the host's own GOMAXPROCS
// it PASSES 5 out of 5, merely dragging the writer from 0.33 s to 6.4 s. A regression that hides
// on a wide machine is one this repository would reintroduce and not notice, so this pins the same
// property with exactly one reader and no concurrency at all: for the whole duration of the
// WriteState below, a handle of the shape a hook client's ReadState takes is open on state.bin.
//
// The direction matters more than the mechanism. It is not "a reader might miss an update"; it is
// a READER, in a different process, making the daemon's WRITE fail — internal/cli/hookclient.go's
// doHook reads state.bin on every hook event, and internal/daemon/handlers.go's persistHotMode
// writes it on every §12.2 transition. A failed persistHotMode is logged and dropped, so the
// transition stays unpublished and every hook client started afterwards keeps reading a record
// that says the hot path is healthy, and keeps dialling a daemon that has stopped accepting.
//
// The two assertions are the two halves of the promise, in the order they have to hold:
//
//   - WriteState must return nil, and must do so because the record LANDED, not because a retry
//     budget papered over it — so the record is read back and compared.
//   - the hook client's already-open handle must keep reading the version it opened, which is what
//     makes the replace safe to do underneath it rather than merely possible.
//
// On POSIX this has always held (rename(2) semantics); it is asserted rather than skipped so the
// file states the platform difference instead of hiding it.
//
// What this does NOT pin is the reader half's wiring — that ReadState's own open is the shared
// one. ReadState closes its handle before it returns, so there is no instant at which another
// goroutine could catch it holding a handle, and no deterministic assertion can reach it; a
// ReadState reverted to os.ReadFile would still satisfy everything below. That half is covered by
// internal/paths' TestOpenSharedLetsWriteAtomicLandUnderAnOpenReader (which fails outright if
// FILE_SHARE_DELETE is dropped) plus
// TestStateReadNeverFallsBackWhileAValidRecordIsOnDisk's concurrent load.
func TestWriteStateLandsWhileAHookClientHoldsTheRecordOpen(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	held := State{
		Mode: contract.ModeFull, Hot: HotSync, ConnectDeadlineMs: 3, AckDeadlineMs: 4,
		DaemonEnabled: true, SpoolOnBreach: true, MaxPayloadBytes: 4096,
		DaemonPID: 4321, Written: core.UnixMilli(1),
	}
	require.NoError(t, WriteState(root, held))

	// paths.OpenShared is the open ReadState performs (via paths.ReadFileShared). Holding it for
	// the whole write is a hook client that has read the record but not yet closed the file.
	f, err := paths.OpenShared(StatePath(root))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	// Hot flips to HotSpool: the §12.2 transition persistHotMode publishes, and the one field a
	// client that missed the update gets exactly backwards.
	transitioned := held
	transitioned.Hot = HotSpool
	transitioned.DaemonPID = 8642
	transitioned.Written = core.UnixMilli(2)

	require.NoError(t, WriteState(root, transitioned),
		"a hook client reading state.bin must not be able to fail the daemon's write of it; on "+
			"Windows this is the rename that returns ERROR_ACCESS_DENIED when the destination is "+
			"held by a handle without FILE_SHARE_DELETE")
	require.Equal(t, transitioned, ReadState(root, config.Defaults()),
		"WriteState reported success, so the new record must actually be the one on disk")

	buf := make([]byte, stateSize)
	n, readErr := f.Read(buf)
	require.NoError(t, readErr, "the reader's own handle must survive the replace")
	got, ok := decodeState(buf[:n])
	require.True(t, ok, "the held handle must still see a well-formed record, not a torn one")
	require.Equal(t, held, got,
		"the handle must keep reading the version it opened — the replace happens underneath it, "+
			"exactly as rename(2) behaves on POSIX")
}
