//go:build !windows && !linux && !darwin

package store

import "os"

// stagingOpenFlags adds nothing to writeStaged's exclusive create here.
const stagingOpenFlags = 0

// openObjectLeaf is the portable path-verified open, with no platform flags: Lstat, open, then a
// Stat of the handle that os.SameFile must match. None of the three release targets
// (00-ARCHITECTURE.md §2.6) builds this file; it keeps the package compiling elsewhere.
func openObjectLeaf(long string) (*os.File, os.FileInfo, error) {
	return openObjectChecked(long, 0)
}
