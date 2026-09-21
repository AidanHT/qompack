package paths

import "golang.org/x/sys/unix"

// RenameDirectoryNoReplace atomically refuses an existing destination, including
// an empty directory. Unsupported filesystems fail closed.
func RenameDirectoryNoReplace(from, to string) error {
	return unix.RenamexNp(from, to, unix.RENAME_EXCL)
}
