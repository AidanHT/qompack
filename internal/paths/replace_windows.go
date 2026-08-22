//go:build windows

package paths

import (
	"encoding/binary"
	"math/bits"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Windows has two different rename-replace semantics, and the difference is the whole reason this
// file exists.
//
// os.Rename is MoveFileEx(from, to, MOVEFILE_REPLACE_EXISTING) (GOROOT/src/os/file_windows.go's
// rename -> internal/syscall/windows.Rename). Underneath, that is the CLASSIC
// FileRenameInformation, whose replace step fails with ERROR_ACCESS_DENIED whenever the
// destination has ANY open handle — and, measured on this host, it fails that way even when the
// handle was opened with FILE_SHARE_READ|FILE_SHARE_WRITE|FILE_SHARE_DELETE, i.e. even when its
// holder has explicitly consented to the file being deleted underneath it. Delete-sharing is
// enough to let another process os.Remove the file; it is NOT enough to let another process
// rename over it.
//
//	holder share mode                 os.Rename over it        POSIX rename over it
//	FILE_SHARE_READ|WRITE (os.Open)   Access is denied         sharing violation
//	+ FILE_SHARE_DELETE               Access is denied         succeeds
//	(no handle at all)                succeeds                 succeeds
//
// The second column is why WriteAtomic could not finish while a reader held the destination, and
// the second row of the third column is the fix: FileRenameInfoEx with
// FILE_RENAME_POSIX_SEMANTICS (Windows 10 1607+ / NTFS) unlinks the destination from the
// directory and installs the replacement in one step, exactly like rename(2), leaving every
// already-open handle reading the bytes it opened. Both halves are load-bearing — a POSIX rename
// against a plain os.Open handle still fails — so openShared below is the other half of this fix,
// not an independent nicety.
//
// FILE_RENAME_INFO's layout, filled by hand rather than with unsafe:
//
//	offset 0             Flags            uint32
//	offset ptrSize       RootDirectory    HANDLE (NULL: FileName is a full path)
//	offset 2*ptrSize     FileNameLength   uint32, BYTES, excluding the terminating NUL
//	offset 2*ptrSize+4   FileName         UTF-16
//
// Only 64-bit Windows ships (00-ARCHITECTURE.md §2.6 lists windows/amd64 and windows/arm64), so
// in practice those are 0, 8, 16 and 20 — but they are derived from the pointer width rather
// than written down, so a 32-bit build would be correct rather than silently wrong.
const (
	renameInfoPtrSize       = bits.UintSize / 8 // sizeof(HANDLE) == sizeof(uintptr)
	renameInfoRootDirOffset = renameInfoPtrSize // Flags(4) padded up to HANDLE's alignment
	renameInfoNameLenOffset = renameInfoRootDirOffset + renameInfoPtrSize
	renameInfoNameOffset    = renameInfoNameLenOffset + 4 // past FileNameLength; WCHAR needs only 2-byte alignment
	renameInfoBytesPerUTF16 = 2
)

// ntObjectPrefix is the NT object-manager spelling of the Win32 \\?\ escape. FILE_RENAME_INFO's
// FileName goes to the NT layer, not to the Win32 path parser, so the destination has to be
// handed over in this form.
const ntObjectPrefix = `\??\`

// ntPath converts a Win32 path into the NT object path FILE_RENAME_INFO wants. It handles the
// three shapes WriteAtomic can produce: a path Long() has already prefixed with \\?\ (or
// \\?\UNC\), a bare UNC path, and an ordinary drive path. ok is false when the path cannot be
// made absolute, which is the caller's signal to leave the rename to os.Rename rather than guess.
func ntPath(p string) (string, bool) {
	switch {
	case strings.HasPrefix(p, longPrefix): // covers longUNCPrefix too: it starts with longPrefix.
		return ntObjectPrefix + p[len(longPrefix):], true
	case strings.HasPrefix(p, `\\`):
		return ntObjectPrefix + `UNC\` + p[len(`\\`):], true
	default:
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", false
		}
		return ntObjectPrefix + abs, true
	}
}

// posixReplace renames tmp onto p with POSIX semantics, so the replace succeeds even while
// another process holds p open (provided that process opened it with FILE_SHARE_DELETE, as
// openShared does).
//
// Every failure here is reported to the caller, which retries with os.Rename: FileRenameInfoEx is
// rejected outright with ERROR_INVALID_PARAMETER by pre-1607 Windows and by filesystems that do
// not implement it (FAT32, some network redirectors), and there is no cheap way to tell that
// apart from a real refusal without a second syscall. Falling through in every case means this
// function can only ADD successes: whatever os.Rename did before, it still does, with its own
// error — which is the *os.LinkError shape isRenameContention, isStateFileContention and their
// tests all classify.
func posixReplace(tmp, p string) error {
	dst, ok := ntPath(p)
	if !ok {
		return os.ErrInvalid
	}
	name, err := windows.UTF16FromString(dst)
	if err != nil {
		return err
	}
	src, err := windows.UTF16PtrFromString(tmp)
	if err != nil {
		return err
	}

	// The rename is issued through a handle on the SOURCE, which must carry DELETE access. The
	// source is WriteAtomic's own freshly-closed staging file, so nothing contends for it.
	h, err := windows.CreateFile(src, windows.DELETE|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()

	buf := make([]byte, renameInfoNameOffset+len(name)*renameInfoBytesPerUTF16)
	binary.LittleEndian.PutUint32(buf, windows.FILE_RENAME_REPLACE_IF_EXISTS|windows.FILE_RENAME_POSIX_SEMANTICS)
	binary.LittleEndian.PutUint32(buf[renameInfoNameLenOffset:], uint32((len(name)-1)*renameInfoBytesPerUTF16))
	for i, u := range name {
		binary.LittleEndian.PutUint16(buf[renameInfoNameOffset+i*renameInfoBytesPerUTF16:], u)
	}
	return windows.SetFileInformationByHandle(h, windows.FileRenameInfoEx, &buf[0], uint32(len(buf)))
}

// replace renames tmp onto p, preferring the POSIX-semantics rename that a concurrent reader
// cannot block and falling back to os.Rename — the call this used to be — whenever that is
// unavailable. The fallback's error is what the caller sees, never posixReplace's, so a failure
// still arrives in exactly the shape it always did.
func replace(tmp, p string) error {
	if err := posixReplace(tmp, p); err == nil {
		return nil
	}
	return os.Rename(tmp, p)
}

// openShared opens p read-only with FILE_SHARE_DELETE added to the share mask Go's own os.Open
// uses.
//
// GOROOT/src/syscall/syscall_windows.go's Open hard-codes
// `sharemode := FILE_SHARE_READ | FILE_SHARE_WRITE`, so every os.Open/os.ReadFile in the process
// takes a handle that forbids anyone else deleting the file — which makes another process's
// os.Remove of it fail with ERROR_SHARING_VIOLATION, and (with posixReplace above) another
// process's replace of it fail too. test/guards/v1_integration_test.go's
// v1StopDaemonAndWaitGone documents the same mechanism from the daemon.lock end: a poller reading
// the lock CAUSES the abandoned lock it is watching for.
//
// The rest of the CreateFile call is Open's, argument for argument, so the errors this returns are
// the errors callers already branch on: GENERIC_READ and OPEN_EXISTING so a missing file is
// ERROR_FILE_NOT_FOUND, FILE_FLAG_BACKUP_SEMANTICS (Open adds it for every read-access open, for
// the same reason) so a directory opens rather than being rejected, and an *os.PathError wrapper
// carrying the raw syscall.Errno that os.IsNotExist and internal/ipc's contention predicate both
// read.
//
// One argument is deliberately NOT Open's: the SecurityAttributes pointer is nil, where Open
// passes an inheritable one unless O_CLOEXEC is set — and os.Open never sets it. That makes this
// handle non-inheritable, which only narrows what it can do: a hook client that spawns the daemon
// (internal/daemon/spawn_windows.go) cannot leak an open state.bin handle into it. Nothing in the
// tree relies on inheriting a read handle.
func openShared(p string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: p, Err: err}
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: p, Err: err}
	}
	return os.NewFile(uintptr(h), p), nil
}
