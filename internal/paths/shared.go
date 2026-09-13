package paths

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/qompack/qompack/internal/core"
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

// OpenSharedRW opens the existing file p for reading and for writing in place, with a handle that
// does not obstruct anyone else reading, writing, removing or replacing p while it is held.
//
// It is OpenShared's read-write twin, for a caller that holds one handle for a file's whole life
// and overwrites bytes inside it (the delivery journal's v2 seal, SP20-D1 design §2.9). On Windows
// it is CreateFile with GENERIC_READ|GENERIC_WRITE, OpenShared's share mask
// FILE_SHARE_READ|FILE_SHARE_WRITE|FILE_SHARE_DELETE, and OPEN_EXISTING. So OpenShared and os.Open
// readers still open p, os.Remove of p still succeeds (and, with the POSIX delete semantics NTFS
// applies by default on current Windows, frees the name at once), and WriteAtomic's
// POSIX-semantics replace still lands on p. In both cases the handle keeps working on the file it
// opened, which p then no longer names: a POSIX descriptor's behaviour, and exactly what this is
// off Windows, os.OpenFile(p, os.O_RDWR, 0).
//
// A moved path is therefore possible by design, and a caller that must know the handle still
// names p compares os.Lstat(p) with the handle's own Stat through os.SameFile.
//
// It never creates a file: a missing p fails with an error that satisfies
// errors.Is(err, fs.ErrNotExist) and leaves nothing behind. A directory is refused. So is a §7.4
// protected path, with core.ErrAppendOnly, because an in-place overwrite is neither of the two
// writes OpenFile allows there, an append and an exclusive create.
//
// Errors have os.OpenFile's shape, an *os.PathError carrying the platform error.
func OpenSharedRW(p string) (*os.File, error) {
	if root, ok := rootOf(p); ok && IsProtected(root, p) {
		return nil, fmt.Errorf("%w: in-place write on %s", core.ErrAppendOnly, p)
	}
	return openSharedRW(Long(p))
}

// SyncData makes the data written to f durable, for an in-place overwrite of bytes f already has.
//
// It is for one pattern only: a write that overwrites already-allocated bytes of an existing file
// and never changes its size. On Linux it is fdatasync(2), which may skip inode metadata that
// fsync also flushes (the modification time), and is cheaper for exactly that reason. Never use
// it for a newly created or extended file: its size, allocation and inode are metadata that must
// be flushed, which is f.Sync's job, and a new directory entry needs a directory fsync besides.
// This package promises no more than that: when SyncData returns nil, the overwritten bytes of a
// file whose size and allocation the write did not change are durable.
//
// On Windows it is f.Sync (FlushFileBuffers). On darwin it is f.Sync too, which Go implements
// there as fcntl(F_FULLFSYNC), the call that also flushes the drive's own cache. On both it costs
// what f.Sync costs and is never weaker.
func SyncData(f *os.File) error { return syncData(f) }
