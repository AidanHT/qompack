//go:build !windows && !noinject

package cli

import "os"

// denyWriteDir makes dir non-writable for the spool-readonly fault site: POSIX permission bits
// alone are sufficient here — a directory without the write bit refuses to let its owner create
// new files inside it, which is exactly the condition the spool-readonly site exists to provoke.
//
// Gated !noinject alongside !windows (fix round 1, Minor M-11, applied symmetrically with this
// file's Windows counterpart): the only caller, faultLockSpoolDirIfNeeded in fault.go, is itself
// !noinject-only, so a -tags noinject build never references this function either.
func denyWriteDir(dir string) error {
	return os.Chmod(dir, 0o500)
}
