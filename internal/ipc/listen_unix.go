//go:build !windows

package ipc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// posixListenProbeTimeout bounds the liveness probe listen makes before binding: a Unix socket
// path left behind by a dead daemon has to be told apart from one a live daemon still owns, and
// 2 ms is enough to detect a listener without meaningfully delaying startup.
const posixListenProbeTimeout = 2 * time.Millisecond

// listen binds the Unix domain socket a.Path names (00-ARCHITECTURE.md §2.4): the containing
// directory at 0700, the socket itself at 0600. log is Loud'd on the rare path a caller needs to
// know about (currently unused on POSIX — kept so this file and listen_windows.go share one
// signature); maxLine is likewise unused here — it is enforced by LineReader in user space, not
// at bind time.
func listen(a Addr, log logging.Logger, maxLine int) (net.Listener, error) {
	if err := os.MkdirAll(paths.Long(filepath.Dir(a.Path)), dirPerm); err != nil {
		return nil, fmt.Errorf("ipc: listen: mkdir: %w", err)
	}

	if conn, err := dial(a, posixListenProbeTimeout); err == nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: %s", ErrAddrInUse, a.Path)
	} else if isStaleSocketError(err) {
		_ = os.Remove(paths.Long(a.Path))
	}

	ln, err := net.Listen("unix", a.Path)
	if err != nil {
		return nil, fmt.Errorf("ipc: listen: %w", err)
	}
	if err := os.Chmod(paths.Long(a.Path), socketPerm); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("ipc: listen: chmod: %w", err)
	}
	return ln, nil
}

// isStaleSocketError reports whether err is the shape a dial against an orphaned socket file
// produces: nothing is listening (ECONNREFUSED) or the file is already gone (ENOENT) — the two
// outcomes §2.4's stale-socket reclaim names explicitly. Anything else (permission denied, a probe
// that timed out without resolving) is left alone: listen falls through to net.Listen, which fails
// on its own terms rather than removing a socket this probe could not actually characterize.
func isStaleSocketError(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT) || errors.Is(err, os.ErrNotExist)
}
