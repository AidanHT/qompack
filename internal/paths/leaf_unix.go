//go:build unix

package paths

import (
	"errors"
	"os"
	"syscall"
)

// openSharedLeaf is OpenSharedLeaf off Windows: open(2) with O_NOFOLLOW, which refuses a final
// symlink with ELOOP (reported as ErrNotLeaf), and O_NONBLOCK, so a FIFO at p opens at once instead
// of waiting for a writer; for a regular file O_NONBLOCK changes nothing. As with openShared, a POSIX
// descriptor blocks no other process's unlink or rename, so nothing more is needed for sharing.
func openSharedLeaf(p string) (*os.File, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, &os.PathError{Op: "open", Path: p, Err: ErrNotLeaf}
		}
		return nil, err
	}
	return f, nil
}
