package daemon

import (
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/paths"
)

// Reject static aliases before pinning a delivery directory. Its caller selects
// the trusted root; descendant access must continue through the returned handle.
// This checks the opened identity, not an atomic prohibition on all path races.
func pinDeliveryDirectory(dir string) (*os.Root, error) {
	info, err := os.Lstat(paths.Long(dir))
	if err != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return nil, deliveryJournalError()
	}
	root, err := os.OpenRoot(paths.Long(dir))
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = root.Close()
		return nil, deliveryJournalError()
	}
	return root, nil
}

func pinDeliveryChild(parent *os.Root, name string, create bool) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if create && os.IsNotExist(err) {
		if err := parent.Mkdir(name, 0o700); err != nil {
			return nil, err
		}
		info, err = parent.Lstat(name)
	}
	if err != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return nil, deliveryJournalError()
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := child.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = child.Close()
		return nil, deliveryJournalError()
	}
	return child, nil
}

// Create-new files keep any interrupted segment attempt visible. A conflicting
// or partial stage is never overwritten to make a later retry look successful.
func createSegmentFile(root *os.Root, name string, data []byte) error {
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	n, writeErr := f.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = deliveryJournalError()
	}
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func samePinnedDirectory(root *os.Root, dir string) bool {
	pinned, err := root.Stat(".")
	if err != nil {
		return false
	}
	current, err := os.Lstat(paths.Long(filepath.Clean(dir)))
	return err == nil && os.SameFile(pinned, current) && current.Mode()&os.ModeSymlink == 0
}

// Recovery already checks the log; repeat the identity check immediately around
// acquiring its append handle. Creating a new file uses O_EXCL, so a competing
// file appearing after absence is refused rather than followed or overwritten.
func openDeliveryAppend(root *os.Root, name string, size int64) (*os.File, error) {
	before, err := root.Lstat(name)
	flags := os.O_WRONLY | os.O_APPEND
	if os.IsNotExist(err) {
		if size != 0 {
			return nil, deliveryJournalError()
		}
		flags |= os.O_CREATE | os.O_EXCL
	} else if err != nil || !before.Mode().IsRegular() || before.Size() != size {
		return nil, deliveryJournalError()
	}
	f, err := root.OpenFile(name, flags, 0o600)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Size() != size ||
		(before != nil && !os.SameFile(before, opened)) {
		_ = f.Close()
		return nil, deliveryJournalError()
	}
	return f, nil
}
