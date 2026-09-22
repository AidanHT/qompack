//go:build linux || darwin

package store

import (
	"os"
	"syscall"
)

// objectOpenFlags are added to every object read's O_RDONLY here.
//
// O_NOFOLLOW makes the open itself refuse a symbolic link at the leaf (ELOOP), so a leaf replaced by
// a link between openObjectChecked's Lstat and its open is refused rather than followed and then
// detected. O_NONBLOCK makes that same window harmless for a FIFO: the open returns at once instead
// of waiting for a writer, and the handle's Stat then refuses it as nonregular. For the regular
// file an object read is supposed to find, O_NONBLOCK changes nothing about how it reads.
//
// It is also cheaper. os.OpenFile without O_NONBLOCK on Linux sets the descriptor non-blocking to
// try the runtime poller, has the registration refused because a disk file cannot be polled, and
// sets it blocking again — four fcntl calls per open (GOROOT/src/os/file_unix.go newFile). Opened
// non-blocking, the descriptor goes straight to that registration attempt and keeps the flag. On
// darwin newFile already declines to poll a regular file, so the flag costs nothing there either.
const objectOpenFlags = syscall.O_NOFOLLOW | syscall.O_NONBLOCK

// stagingOpenFlags are added to writeStaged's exclusive create, for the second of the reasons above:
// a staging file is a regular file this call creates, so O_NONBLOCK changes nothing about how it is
// written, and it saves the same four fcntl calls on every novel object's open. O_NOFOLLOW is not
// needed there: O_CREAT|O_EXCL already refuses to follow a link at the leaf.
const stagingOpenFlags = syscall.O_NONBLOCK

// openObjectLeaf opens the object at long through openObjectChecked, with the no-follow flags. The
// Lstat stays: it keeps a device node or any other nonregular leaf from being opened at all, and on
// these platforms os.SameFile is a comparison of the two Stat results, with no extra syscall.
func openObjectLeaf(long string) (*os.File, os.FileInfo, error) {
	return openObjectChecked(long, objectOpenFlags)
}
