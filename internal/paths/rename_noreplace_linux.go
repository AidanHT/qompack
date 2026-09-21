package paths

import "golang.org/x/sys/unix"

// RenameDirectoryNoReplace atomically refuses an existing destination. A kernel
// or filesystem without RENAME_NOREPLACE fails closed; there is no plain rename fallback.
func RenameDirectoryNoReplace(from, to string) error {
	return unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
}
