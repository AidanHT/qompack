//go:build !windows

package paths

// Long is the identity function on every platform other than Windows: only Windows enforces the
// legacy MAX_PATH limit that the \\?\ prefix works around, so elsewhere Long has nothing to do.
func Long(p string) string { return p }
