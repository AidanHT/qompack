package paths

import (
	"os"
	"syscall"
)

// mayBeSameDir is IsHome's cheap negative test on Windows, run before os.SameFile.
//
// An os.Stat of a plain directory is one GetFileAttributesEx call, which carries no file identity;
// os.SameFile then opens BOTH paths to read their volume serial and file index. That pair of
// opens is most of what the check costs a hook, and it answers "no" for every ordinary project.
// The attribute data os.Stat already returned includes the creation time, and one directory seen
// through any spelling or link has one creation time, so two different creation times prove two
// different directories without opening either. Equal ones prove nothing and fall through to
// os.SameFile, which decides.
func mayBeSameDir(a, b os.FileInfo) bool {
	ad, aok := a.Sys().(*syscall.Win32FileAttributeData)
	bd, bok := b.Sys().(*syscall.Win32FileAttributeData)
	if !aok || !bok {
		return true
	}
	return ad.CreationTime == bd.CreationTime
}
