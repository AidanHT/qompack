//go:build linux

package paths

import (
	"errors"
	"os"
	"syscall"
)

// syncData is fdatasync(2) on f's descriptor. It is issued through f.SyscallConn, so f cannot be
// closed and its descriptor reused under the call, and it is retried on EINTR, as os.File.Sync
// retries its fsync (GOROOT/src/internal/poll's ignoringEINTR).
//
// It calls the standard library's syscall.Fdatasync, not golang.org/x/sys/unix's: where the
// standard library suffices it is preferred. tools/devtool/bindeps.go admits golang.org/x/sys/unix
// into the shipped binary only because RenameDirectoryNoReplace has no stdlib equivalent
// (00-ARCHITECTURE.md §2.5).
func syncData(f *os.File) error {
	if f == nil {
		return os.ErrInvalid
	}
	rc, err := f.SyscallConn()
	if err != nil {
		return &os.PathError{Op: "fdatasync", Path: f.Name(), Err: err}
	}
	var syncErr error
	if err := rc.Control(func(fd uintptr) {
		for {
			syncErr = syscall.Fdatasync(int(fd))
			if !errors.Is(syncErr, syscall.EINTR) {
				return
			}
		}
	}); err != nil {
		return &os.PathError{Op: "fdatasync", Path: f.Name(), Err: err}
	}
	if syncErr != nil {
		return &os.PathError{Op: "fdatasync", Path: f.Name(), Err: syncErr}
	}
	return nil
}
