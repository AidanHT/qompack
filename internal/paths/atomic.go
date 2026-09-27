package paths

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/qompack/qompack/internal/core"
)

// tempFilePrefix names every WriteAtomic staging file, so a crash-interrupted write is
// recognizable as debris rather than mistaken for a real artifact.
const tempFilePrefix = "wa-"

// pathOwner is what ownerOf finds on a path's own directory chain.
type pathOwner struct {
	// root is the project root whose .qompack owns the path, so Of(root).Tmp is where WriteAtomic
	// stages. It is meaningful only when owned is true.
	root  string
	owned bool
	// protected reports that some store on the path, the owner or one enclosing it, holds the path
	// at one of its §7.4 append-only locations.
	protected bool
}

// ownerOf reports which store owns p and whether any store guards it. It is how WriteAtomic,
// OpenFile and OpenSharedRW recognize a protected path, and where WriteAtomic stages, without every
// caller having to thread a Layout or project root through every write.
//
// The store that owns p is the NEAREST element of p's own absolute path that is named .qompack. If
// that directory exists, it owns p; if it does not, p belongs to a store that has not been made yet,
// and no store owns it. The walk never looks beside the path, and it never passes over the nearest
// .qompack to an outer one, so a write stages only inside the store whose tree holds the target or,
// with none, beside the target itself.
//
// The walk used to accept the nearest ancestor that merely CONTAINED a .qompack, and two defects
// followed from that one rule:
//
//   - A write outside any store escaped to an unrelated store higher up. With the user-global layer
//     in a home directory (paths.Global), a write into a project below the home that has no store
//     yet, or into an operator's directory, staged in <home>/.qompack/tmp. Its rename then failed
//     when the target's directory had not been made yet (w2-lifetime runs/21: 2 internal/cli and 6
//     internal/ipc rows on a machine with a real ~/.qompack), and would fail as a cross-device
//     rename wherever the target sits on another filesystem than the home.
//   - A directory named .qompack inside checkpoints/, pins/ or sketches/ became the "owner" of the
//     protected files beside it, IsProtected measured them against it and found them outside, and
//     WriteAtomic replaced a sealed checkpoint.
//
// The user-global layer is reached only by its own path: a write to paths.Global(home)/x is owned by
// that store because paths.Global(home) is on its path, and nothing below the home can stage in it.
//
// Protection is asked of EVERY existing store on the path, not only the owner, so a stray .qompack
// cannot unprotect anything by standing between a protected file and its real store: a path through
// <root>/.qompack/checkpoints/.qompack/ is still under <root>'s checkpoints/.
//
// Every store is asked about p made absolute: filepath.Rel cannot relate a relative path to an
// absolute root, so IsProtected(root, p) answered "not protected" for every relative spelling of a
// protected path. A p that cannot be made absolute is owned and guarded by nothing, as before.
//
// Only an element named .qompack costs a stat, so the walk no longer stats every ancestor up to the
// volume root. Ownership compares that name as isStoreDirName does: case-folded on Windows and
// exactly elsewhere. Protection compares it as IsProtected compares every name (sameName), which
// also folds case on darwin, so a store spelled .QOMPACK on a case-insensitive darwin volume still
// guards its protected files, while staging keeps choosing only a store spelled exactly (a folded
// match there could make a stray .qompack on a case-sensitive volume). The walk runs over abs with
// its NTFS stream suffixes removed, since .qompack::$INDEX_ALLOCATION resolves to the store too.
func ownerOf(p string) pathOwner {
	var o pathOwner
	abs, err := filepath.Abs(p)
	if err != nil {
		return o
	}
	abs = filepath.Clean(streamless(abs))
	nearest := true
	for d := filepath.Dir(abs); ; {
		parent := filepath.Dir(d)
		if parent == d {
			return o
		}
		base := filepath.Base(d)
		owns := isStoreDirName(base)
		if owns || sameName(base, dotDir) {
			if fi, statErr := os.Stat(Long(d)); statErr == nil && fi.IsDir() {
				if owns && nearest {
					o.root, o.owned = parent, true
				}
				if IsProtected(parent, abs) {
					o.protected = true
				}
			}
			if owns {
				nearest = false
			}
		}
		d = parent
	}
}

// isStoreDirName reports whether name spells dotDir, compared as filepath.Rel compares path
// elements on this platform (strings.EqualFold on Windows, == elsewhere), so ownerOf's stores and
// IsProtected's answer about each of them agree on every spelling.
func isStoreDirName(name string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(name, dotDir)
	}
	return name == dotDir
}

// tmpDirFor returns the directory WriteAtomic stages into for a write to p, given ownerOf(p)'s
// answer (root, owned): <root>/.qompack/tmp when a store owns p, so the finishing rename stays
// inside the store that holds both files; filepath.Dir(p) otherwise, which no store owns, so the
// rename never leaves p's own directory. Either way it is on p's volume, barring a mount point
// inside a store. It takes the walk's result rather than walking again, because WriteAtomic has
// already walked for its protected-path check.
func tmpDirFor(p, root string, owned bool) string {
	if owned {
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
//
// The retry is for a read-only REGULAR FILE at p and nothing else. os.Chmod is not a no-op on a
// directory: on POSIX 0o600 strips its execute bits, and a directory nobody can traverse is a
// directory whose contents are lost to every reader — the V5 close-out's first Linux and macOS
// run found ReplacePinsView doing exactly that to a non-empty destination directory, leaving the
// old evidence unreadable behind the failed rename (Windows directories ignore the bit, which is
// why it was never seen). So the destination is stat'ed first and only a regular file is touched,
// and if the retry still fails its previous mode is put back, so a failed replace changes nothing.
func renameWithRetry(tmp, p string) error {
	first := replace(tmp, p)
	if first == nil {
		return nil
	}
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return first
	}
	if err := os.Chmod(p, 0o600); err != nil {
		return first
	}
	if err := replace(tmp, p); err != nil {
		_ = os.Chmod(p, fi.Mode().Perm())
		return first
	}
	return nil
}

// fsyncDir fsyncs dir's directory entry after a rename — the durability half of the write
// barrier WriteAtomic promises. Without it, a crash between the rename and the next unrelated
// metadata flush can lose the rename on some POSIX filesystems even though the renamed file's
// own data was already synced.
//
// On Windows it is a no-op, by owner decision D24, and SyncDir's comment states the premise that
// decision rests on and what it leaves exposed. The no-op is also forced by the API: os.Open of a
// directory yields a handle FlushFileBuffers refuses (ERROR_ACCESS_DENIED), and os.OpenFile(dir,
// O_RDWR) fails with EISDIR (w5-dirsync, runs/win-dir-fsync-probe.txt), so without the short circuit
// every call would fail. That still holds now that the rename may be replace_windows.go's
// FileRenameInfoEx instead of MoveFileEx: the two differ only in the FILE_RENAME_* flags handed to
// the same NTFS FileRenameInformation path, not in how the metadata change is journalled.
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

// SyncDir makes dir's entries durable: the names of the files in it, which on POSIX a file's own
// fsync does not cover. WriteAtomic syncs its destination's directory this way after its rename. A
// caller that makes durable a file another process created, and never synced the directory of, needs
// the same: the daemon's drain, before it consumes a spool file a hook created. So does a caller that
// is about to depend on a new file's name: AppendManifest, before a MANIFEST line names an artifact.
//
// On Windows SyncDir is a no-op (owner decision D24), on the NTFS-journaling premise:
//
//   - NTFS writes every metadata change — a file's creation, a rename, a deletion, a size change —
//     to the volume's write-ahead log as one transaction, and replays that log at mount, so after a
//     power cut each such change is either wholly present or wholly absent. A directory entry is
//     never torn, and a rename never leaves both names or neither. File contents are NOT journaled,
//     which is why every durable write still flushes its file.
//   - FlushFileBuffers on a file (File.Sync) writes the file's bytes and forces the log out through
//     the file's own latest change. The log is sequential, so every metadata change logged before
//     that point — the file's own creation or rename among them — is durable when the flush returns.
//     A directory flush would add nothing to a file that is itself flushed after its last rename.
//
// The residual risk is what follows the LAST flush on the volume: a rename or deletion completed
// after it can be undone by a power cut. WriteAtomic flushes its staging file before the rename, so
// its most recent replacement of a file can revert to the previous complete version — never to a
// torn or empty one — and a removed file can reappear. No product guarantee depends on more than
// the premise: every write whose loss would break one ends with a flush after the directory change it
// relies on (a checkpoint's MANIFEST line, the delivery and WAL journal appends, each object's flush
// in a publication pass, the pins log line, an acknowledged elimination's line), and the files that
// can revert — state/ documents, the pins view, precompact.json, a draft, a restore's final
// directory rename — are derived or rebuilt, or leave the operator the previous state to retry from.
// The premise has not been tested with a power cut on this project; a real Windows directory barrier
// exists (FlushFileBuffers on a CreateFile(GENERIC_WRITE, FILE_FLAG_BACKUP_SEMANTICS) handle,
// w5-dirsync item 4) and D24 declines it. docs/architecture.md §4 and docs/security.md §8 carry the
// same statement for operators.
func SyncDir(dir string) error { return fsyncDir(dir) }

// ownerWriteBit is the permission bit Windows' os.Chmod reads: it maps the whole mode onto the
// FILE_ATTRIBUTE_READONLY attribute, set when this bit is clear and cleared when it is set.
const ownerWriteBit fs.FileMode = 0o200

// chmodChangesStagingFile reports whether WriteAtomic's Chmod of its fresh staging file to perm can
// change anything. Off Windows it always can: a POSIX mode has more than one bit, and umask may have
// narrowed what os.CreateTemp created. On Windows os.Chmod only sets or clears READONLY
// (GOROOT/src/syscall/syscall_windows.go Chmod), and os.CreateTemp's 0o600 create never sets it, so
// a perm that keeps the owner-write bit asks to clear an attribute the file does not have — and the
// call would still spend a GetFileAttributes and a SetFileAttributes path lookup saying so.
func chmodChangesStagingFile(perm fs.FileMode) bool {
	return runtime.GOOS != "windows" || perm&ownerWriteBit == 0
}

// WriteAtomic writes b to p durably and atomically: stage in a temp file under the .qompack/tmp of
// the store that owns p (ownerOf), or beside p when no store does, Sync the temp file, Chmod it to
// perm, Rename it onto p, then fsync p's parent directory. Staged beside p, the write also makes
// p's directory if it is missing; staged in a store, p's directory must already exist, as
// EnsureLayout leaves every directory of a layout. It refuses outright to write a §7.4 protected
// path — checkpoints/, pins/, or sketches/tried.bloom — because WriteAtomic replaces whatever is
// at p, and replacing any of those is exactly what the append-only invariant forbids;
// ReplaceBloom is the one sanctioned exception, and it never calls WriteAtomic.
func WriteAtomic(p string, b []byte, perm fs.FileMode) error {
	o := ownerOf(p)
	if o.protected {
		return fmt.Errorf("%w: WriteAtomic on protected path %s", core.ErrAppendOnly, p)
	}

	dir := tmpDirFor(p, o.root, o.owned)
	if err := os.MkdirAll(Long(dir), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(Long(dir), tempFilePrefix)
	if err != nil {
		return err
	}
	tmp := f.Name()
	// The staging file is removed on every path that leaves it behind, and only on those: after a
	// successful rename it no longer exists, and removing it anyway cost a failed delete and a
	// failed rmdir on every successful write.
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(Long(tmp))
		}
	}()

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
	if chmodChangesStagingFile(perm) {
		if err := os.Chmod(Long(tmp), perm); err != nil {
			return err
		}
	}
	if err := renameWithRetry(Long(tmp), Long(p)); err != nil {
		return err
	}
	renamed = true
	return fsyncDir(filepath.Dir(p))
}
