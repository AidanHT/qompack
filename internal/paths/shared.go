package paths

import (
	"errors"
	"io"
	"os"
)

// readFileMinCap is the smallest buffer ReadFileShared starts with, whatever Stat reported. It is
// os.ReadFile's own floor, copied verbatim and for its own reason: a file that claims size 0 may
// still have content (Linux /proc is the canonical case), and an initial one-byte read would
// misread it.
const readFileMinCap = 512

// OpenShared opens p read-only for a caller that must not obstruct whoever is WRITING p.
//
// It exists because "reading a file" is not a passive act on Windows. Go's os.Open takes a handle
// with FILE_SHARE_READ|FILE_SHARE_WRITE and no FILE_SHARE_DELETE, and while such a handle is
// open, another process's os.Remove of that file fails with ERROR_SHARING_VIOLATION and
// WriteAtomic's finishing replace of it fails with ERROR_ACCESS_DENIED. A reader polling a file
// therefore does not merely observe the writer, it can stall or fail it — the failure mode
// test/guards/v1_integration_test.go's v1StopDaemonAndWaitGone documents for daemon.lock and the
// one internal/ipc.ReadState hit against the daemon's own state.bin.
//
// OpenShared is that same read with FILE_SHARE_DELETE added, which — paired with WriteAtomic's
// POSIX-semantics replace (replace_windows.go) — lets the writer land while this handle is open.
// The reader keeps seeing the bytes it opened, exactly as it would on POSIX; it simply sees the
// previous version rather than the new one. Off Windows it is os.Open, because there a file
// descriptor never blocked anything in the first place.
//
// The caller closes the returned file. Errors have os.Open's shape — an *os.PathError carrying
// the platform error — so os.IsNotExist and friends read them unchanged.
func OpenShared(p string) (*os.File, error) {
	return openShared(Long(p))
}

// ReadFileShared reads all of p through OpenShared: os.ReadFile's result with os.ReadFile's error
// shapes, taken with a handle that cannot block a concurrent writer's replace of p.
//
// Like os.ReadFile it reports a read that ended early as an error rather than as a short buffer,
// so a caller can never mistake a truncated read for a genuinely short file.
func ReadFileShared(p string) ([]byte, error) {
	f, err := OpenShared(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	// The sizing and the grow-then-read loop are os.ReadFile's own (GOROOT/src/os/file.go), kept
	// identical so a caller that swaps one for the other sees the same allocation behaviour on
	// the hot path as well as the same errors.
	size := 0
	if fi, statErr := f.Stat(); statErr == nil {
		if s := fi.Size(); int64(int(s)) == s {
			size = int(s)
		}
	}
	size++ // one byte for the final Read that reports EOF.
	if size < readFileMinCap {
		size = readFileMinCap
	}

	data := make([]byte, 0, size)
	for {
		if len(data) >= cap(data) {
			data = append(data[:cap(data)], 0)[:len(data)]
		}
		n, readErr := f.Read(data[len(data):cap(data)])
		data = data[:len(data)+n]
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				readErr = nil
			}
			return data, readErr
		}
	}
}
