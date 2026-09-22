//go:build windows

package store

import (
	"os"
	"syscall"
)


// openObjectLeaf opens the object at long for reading without following a reparse point at the
// leaf, and returns the file with the handle's own Stat.
//
// FILE_FLAG_OPEN_REPARSE_POINT makes CreateFile open a reparse point (a symbolic link, a junction,
// an AF_UNIX socket, a cloud placeholder, a deduplicated file) itself rather than whatever it
// redirects to, and is ignored for a file that is not one. So when the opened handle carries no
// FILE_ATTRIBUTE_REPARSE_POINT, it is an ordinary file entry that CreateFile resolved without any
// redirection, and the Stat taken from that handle describes exactly the bytes that will be read:
// there is no window between the check and the read for a replaced leaf to slip through, and the
// check costs one path lookup instead of three.
//
// That is the reason for this file. openObjectChecked's Lstat is a GetFileAttributesEx path lookup,
// and os.SameFile over a path-based Lstat has to open the path a SECOND time to fetch its file ID
// (os.(*fileStat).loadFileId), so the portable check paid three path lookups per object read —
// each of them a pass through every filesystem filter on the volume, antivirus included — where
// this pays one. SP20-D2 is the budget that paid for it.
//
// A leaf that IS a reparse point takes openObjectChecked instead, exactly as every object read did
// before: Go's Lstat decides what such a leaf is (a symbolic link or a placeholder is refused; a
// Data Deduplication file is a regular file whose reads the dedup filter serves, which a handle
// that did not follow the reparse point would not see), so no reparse point is read by a rule this
// function invented.
func openObjectLeaf(long string) (*os.File, os.FileInfo, error) {
	f, err := os.OpenFile(long, os.O_RDONLY|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if !isReparsePoint(fi) {
		if !fi.Mode().IsRegular() {
			_ = f.Close()
			return nil, nil, errObjectNotRegular
		}
		return f, fi, nil
	}
	_ = f.Close()
	return openObjectChecked(long, 0)
}

// isReparsePoint reports whether fi, taken from a handle, carries FILE_ATTRIBUTE_REPARSE_POINT. A
// FileInfo whose Sys is not the Windows attribute data is treated as one, so an unexpected shape
// takes the checked path rather than the fast one.
func isReparsePoint(fi os.FileInfo) bool {
	attrs, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	return !ok || attrs.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
