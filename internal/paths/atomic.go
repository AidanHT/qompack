package paths

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/qompack/qompack/internal/core"
)

// tempFilePrefix names every WriteAtomic staging file, so a crash-interrupted write is
// recognizable as debris rather than mistaken for a real artifact.
const tempFilePrefix = "wa-"

// rootOf walks upward from the directory containing p, looking for the nearest ancestor that
// itself contains a .qompack directory, and returns that ancestor as a project root. It is how
// WriteAtomic, OpenFile and AppendOnly recognize a protected path without every caller having to
// thread a Layout or project root through every write.
func rootOf(p string) (string, bool) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", false
	}
	for d := filepath.Dir(abs); ; {
		fi, statErr := os.Stat(Long(filepath.Join(d, dotDir)))
		if statErr == nil && fi.IsDir() {
			return d, true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", false
		}
		d = parent
	}
}

// tmpDirFor returns the directory WriteAtomic stages into for a write to p: <root>/.qompack/tmp
// when p resolves to a project root, so the finishing rename is same-volume by construction and
// can never cross a volume boundary; filepath.Dir(p) otherwise, for callers exercising
// WriteAtomic outside any .qompack tree.
func tmpDirFor(p string) string {
	if root, ok := rootOf(p); ok {
		return Of(root).Tmp
	}
	return filepath.Dir(p)
}

// renameWithRetry performs the rename that finishes WriteAtomic. On Windows, renaming onto a
// read-only destination — which is the state CreateNew leaves every checkpoint in — fails with
// ERROR_ACCESS_DENIED, so this retries once after clearing the destination's read-only
// attribute. Production code never calls WriteAtomic on a path IsProtected refuses in the first
// place, so the retry exists for the paths WriteAtomic is actually allowed to touch, not as a
// way around the guard. If the retry itself fails, the ORIGINAL error is returned, never the
// retry's, so the caller sees the failure that actually explains what happened.
//
// The rename itself goes through replace (replace_windows.go / replace_other.go) rather than
// straight to os.Rename. That is what makes the OTHER Windows failure — a concurrent reader
// holding the destination open — survivable rather than merely retryable: os.Rename's
// MoveFileEx cannot replace a destination anyone has open, at any share mode, so a read of the
// file being replaced could stall a writer indefinitely. The read-only-destination retry below
// is unchanged and still needed: measured on this host, neither rename flavour will replace a
// read-only destination (both return ERROR_ACCESS_DENIED), so this fix removes no check.
func renameWithRetry(tmp, p string) error {
	first := replace(tmp, p)
	if first == nil {
		return nil
	}
	if err := os.Chmod(p, 0o600); err != nil {
		return first
	}
	if err := replace(tmp, p); err != nil {
		return first
	}
	return nil
}

// fsyncDir fsyncs dir's directory entry after a rename — the durability half of the write
// barrier WriteAtomic promises. Without it, a crash between the rename and the next unrelated
// metadata flush can lose the rename on some POSIX filesystems even though the renamed file's
// own data was already synced. Windows needs no equivalent: MoveFileEx's NTFS transaction is
// durable on its own, so fsyncDir is a no-op there — and that still holds now that the rename may
// be replace_windows.go's FileRenameInfoEx instead, because the two differ only in the
// FILE_RENAME_* flags handed to the same NTFS FileRenameInformation path, not in how the metadata
// change is journalled.
func fsyncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(Long(dir))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}

// WriteAtomic writes b to p durably and atomically: stage in a temp file under the project's
// .qompack/tmp (same volume as p by construction), Sync the temp file, Chmod it to perm, Rename
// it onto p, then fsync p's parent directory. It refuses outright to write a §7.4 protected
// path — checkpoints/, pins/, or sketches/tried.bloom — because WriteAtomic replaces whatever is
// at p, and replacing any of those is exactly what the append-only invariant forbids;
// ReplaceBloom is the one sanctioned exception, and it never calls WriteAtomic.
func WriteAtomic(p string, b []byte, perm fs.FileMode) error {
	root, ok := rootOf(p)
	if ok && IsProtected(root, p) {
		return fmt.Errorf("%w: WriteAtomic on protected path %s", core.ErrAppendOnly, p)
	}

	dir := tmpDirFor(p)
	if err := os.MkdirAll(Long(dir), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(Long(dir), tempFilePrefix)
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(Long(tmp)) }()

	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(Long(tmp), perm); err != nil {
		return err
	}
	if err := renameWithRetry(Long(tmp), Long(p)); err != nil {
		return err
	}
	return fsyncDir(filepath.Dir(p))
}
