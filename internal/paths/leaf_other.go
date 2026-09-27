//go:build !unix && !windows

package paths

import "os"

// openSharedLeaf is OpenSharedLeaf where the platform has no no-follow open this package uses. None
// of the six release targets (00-ARCHITECTURE.md §2.6) is such a platform; this exists so the
// package still builds everywhere. It refuses a path Lstat reports as a symlink and otherwise opens
// it as openShared does, which leaves the window between the two that the Unix and Windows
// versions close.
func openSharedLeaf(p string) (*os.File, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, &os.PathError{Op: "open", Path: p, Err: ErrNotLeaf}
	}
	return openShared(p)
}
