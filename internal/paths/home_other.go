//go:build !windows

package paths

import "os"

// mayBeSameDir is IsHome's cheap negative test. Off Windows os.Stat already returns the device and
// inode os.SameFile compares, so SameFile costs nothing more and there is nothing to skip.
func mayBeSameDir(_, _ os.FileInfo) bool { return true }
