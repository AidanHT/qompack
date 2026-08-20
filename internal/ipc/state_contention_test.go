package ipc

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
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
