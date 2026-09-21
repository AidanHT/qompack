package paths

import "golang.org/x/sys/windows"

// RenameDirectoryNoReplace publishes a directory without replacing any existing
// destination, including an empty directory. Both paths must share a filesystem.
func RenameDirectoryNoReplace(from, to string) error {
	src, err := windows.UTF16PtrFromString(Long(from))
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(Long(to))
	if err != nil {
		return err
	}
	return windows.MoveFile(src, dst)
}
